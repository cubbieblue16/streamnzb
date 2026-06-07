package nntp

import (
	"streamnzb/pkg/core/logger"
	"streamnzb/pkg/core/persistence"
	"sync"
	"time"
)

type ProviderUsageData struct {
	LastResetDay string `json:"last_reset_day"`
	TotalBytes   int64  `json:"total_bytes"`
	AllTimeBytes int64  `json:"all_time_bytes"`
}

type ProviderUsageManager struct {
	state         *persistence.StateManager
	data          map[string]*ProviderUsageData
	lastPersisted map[string]int64
	dirty         bool
	mu            sync.RWMutex
}

var providerManager *ProviderUsageManager
var providerManagerMu sync.Mutex

func GetProviderUsageManager(sm *persistence.StateManager) (*ProviderUsageManager, error) {
	providerManagerMu.Lock()
	defer providerManagerMu.Unlock()

	if providerManager != nil {
		return providerManager, nil
	}

	m := &ProviderUsageManager{
		state:         sm,
		data:          make(map[string]*ProviderUsageData),
		lastPersisted: make(map[string]int64),
	}

	if err := m.load(); err != nil {
		return nil, err
	}
	m.initLastPersisted()

	providerManager = m
	go m.flushLoop()
	return m, nil
}

// flushInterval bounds how often accumulated provider-usage counters are written
// back to persistent storage. AddBytes (called per body-read chunk on the download
// goroutine) only flips a dirty flag now; this loop does the actual full-state
// write off the hot path, so streaming no longer triggers a JSON marshal + disk
// write every megabyte.
const flushInterval = 10 * time.Second

func (m *ProviderUsageManager) flushLoop() {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		dirty := m.dirty
		m.dirty = false
		m.mu.Unlock()
		if !dirty {
			continue
		}
		if err := m.persistAndUpdateLast(); err != nil {
			logger.Error("Failed to flush provider usage data", "err", err)
			// Re-arm so the next tick retries rather than dropping the write.
			m.mu.Lock()
			m.dirty = true
			m.mu.Unlock()
		}
	}
}

func (m *ProviderUsageManager) load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, err := m.state.Get("provider_usage", &m.data)
	if err != nil {
		return err
	}

	today := time.Now().Format("2006-01-02")
	needSave := false
	for _, data := range m.data {
		if data == nil {
			continue
		}
		if m.resetIfNeededLocked(data, today) {
			needSave = true
		}
	}
	if needSave {
		_ = m.state.Set("provider_usage", m.data)
	}
	return nil
}

func (m *ProviderUsageManager) initLastPersisted() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, d := range m.data {
		if d != nil {
			m.lastPersisted[name] = d.TotalBytes
		}
	}
}

// snapshot returns a deep copy of the usage map taken under the read lock so
// callers can marshal/persist it without racing concurrent AddBytes mutations.
// ProviderUsageData is a flat value (string + two int64s), so copying the struct
// is a full copy.
func (m *ProviderUsageManager) snapshot() map[string]*ProviderUsageData {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]*ProviderUsageData, len(m.data))
	for name, d := range m.data {
		if d != nil {
			cp := *d
			out[name] = &cp
		}
	}
	return out
}

func (m *ProviderUsageManager) save() error {
	return m.state.Set("provider_usage", m.snapshot())
}

func (m *ProviderUsageManager) GetUsage(name string) *ProviderUsageData {
	today := time.Now().Format("2006-01-02")
	m.mu.Lock()

	data, reset := m.ensureUsageLocked(name, today)
	// Hand back a copy taken under the lock, never the live map pointer. Callers
	// read these fields without any lock — ClientPool.TotalMegabytes runs from
	// collectStats() every second while download goroutines call AddBytes (which
	// mutates the same struct under m.mu). Returning the live pointer made that an
	// unsynchronized read/write pair (a real, pre-existing data race the race
	// detector flags). ProviderUsageData is a flat value, so a struct copy is a
	// full, safe snapshot.
	var snap *ProviderUsageData
	if data != nil {
		cp := *data
		snap = &cp
	}
	m.mu.Unlock()

	if reset {
		if err := m.persistAndUpdateLast(); err != nil {
			logger.Error("Failed to save reset provider usage data", "name", name, "err", err)
		}
	}

	return snap
}

func (m *ProviderUsageManager) AddBytes(name string, delta int64) {
	if delta <= 0 {
		return
	}
	today := time.Now().Format("2006-01-02")
	m.mu.Lock()
	data, _ := m.ensureUsageLocked(name, today)
	data.TotalBytes += delta
	data.AllTimeBytes += delta
	m.dirty = true
	m.mu.Unlock()
	// Persistence is deferred to flushLoop so the download goroutine never does a
	// JSON marshal + disk write on the hot read path. A day-boundary reset (rare)
	// is also picked up by the flusher within flushInterval; on crash it is
	// re-derived by load() on next start, so an unflushed reset is harmless.
}

func (m *ProviderUsageManager) persistAndUpdateLast() error {
	snap := m.snapshot()
	if err := m.state.Set("provider_usage", snap); err != nil {
		return err
	}
	// Only advance lastPersisted on a successful write, and only to what was
	// actually persisted (the snapshot), so FlushProvider's "total > last" skip
	// heuristic never marks unwritten bytes as durable.
	m.mu.Lock()
	for n, d := range snap {
		m.lastPersisted[n] = d.TotalBytes
	}
	m.mu.Unlock()
	return nil
}

func (m *ProviderUsageManager) FlushProvider(name string) {
	m.mu.Lock()
	today := time.Now().Format("2006-01-02")
	data, ok := m.data[name]
	reset := false
	if ok && data != nil {
		reset = m.resetIfNeededLocked(data, today)
	}
	if data == nil {
		m.mu.Unlock()
		return
	}
	total := data.TotalBytes
	last := m.lastPersisted[name]
	m.mu.Unlock()

	if reset || total > last {
		if err := m.persistAndUpdateLast(); err != nil {
			logger.Error("Failed to flush provider usage data", "name", name, "err", err)
		}
	}
}

func (m *ProviderUsageManager) SyncUsage(activeNames []string) {
	m.mu.Lock()

	activeMap := make(map[string]bool)
	for _, name := range activeNames {
		activeMap[name] = true
	}

	changed := false
	for name := range m.data {
		if !activeMap[name] {
			logger.Info("Removing orphaned usage data for provider", "name", name)
			delete(m.data, name)
			delete(m.lastPersisted, name)
			changed = true
		}
	}
	m.mu.Unlock()

	if changed {
		if err := m.save(); err != nil {
			logger.Error("Failed to save provider usage data after sync", "err", err)
		}
	}
}

func (m *ProviderUsageManager) ensureUsageLocked(name, today string) (*ProviderUsageData, bool) {
	data, ok := m.data[name]
	if !ok {
		data = &ProviderUsageData{LastResetDay: today}
		m.data[name] = data
		return data, false
	}

	return data, m.resetIfNeededLocked(data, today)
}

func (m *ProviderUsageManager) resetIfNeededLocked(data *ProviderUsageData, today string) bool {
	if data == nil {
		return false
	}

	if data.LastResetDay == "" {
		if data.TotalBytes > data.AllTimeBytes {
			data.AllTimeBytes = data.TotalBytes
		}
		data.LastResetDay = today
		data.TotalBytes = 0
		return true
	}

	if data.LastResetDay == today {
		return false
	}

	logger.Debug("Resetting daily usage for provider", "last_reset", data.LastResetDay, "today", today)
	data.LastResetDay = today
	data.TotalBytes = 0
	return true
}

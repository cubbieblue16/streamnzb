package nntp

import (
	"sync"
	"testing"
	"time"
)

// These tests exercise the Fix #3 hot path under heavy concurrency with the race
// detector. They validate (a) the atomic byte counters in TrackRead, (b) the
// dedicated usageMu RWMutex for provider attribution, and (c) the deferred
// dirty-flag flush in ProviderUsageManager.

const (
	stressWorkers   = 64
	stressPerWorker = 4000
	stressChunk     = 7
)

// TestStressTrackReadAtomicCounters hammers TrackRead from many goroutines while
// other goroutines concurrently read the derived stats. The post-condition is an
// exact byte-count match (atomics must not lose any increment) and a clean -race run.
func TestStressTrackReadAtomicCounters(t *testing.T) {
	pool := NewClientPool("example.invalid", 119, false, "user", "pass", 4)
	defer pool.Shutdown()

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = pool.GetSpeed()
					_ = pool.TotalMegabytes()
				}
			}
		}()
	}

	var workers sync.WaitGroup
	for w := 0; w < stressWorkers; w++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < stressPerWorker; i++ {
				pool.TrackRead(stressChunk)
			}
		}()
	}
	workers.Wait()
	close(stop)
	readers.Wait()

	want := int64(stressWorkers) * int64(stressPerWorker) * int64(stressChunk)
	if got := pool.totalBytesRead.Load(); got != want {
		t.Fatalf("totalBytesRead = %d, want %d", got, want)
	}
	if got := pool.bytesRead.Load(); got != want {
		t.Fatalf("bytesRead = %d, want %d", got, want)
	}
}

// TestStressTrackReadWithUsageManager drives TrackRead with a wired usage manager
// while a writer goroutine repeatedly re-sets the usage manager (exercising the
// usageMu writer vs. the many TrackRead readers). Both the pool counter and the
// provider's accumulated bytes must match exactly.
func TestStressTrackReadWithUsageManager(t *testing.T) {
	um := newTestProviderUsageManager(t)
	pool := NewClientPool("example.invalid", 119, false, "user", "pass", 4)
	defer pool.Shutdown()

	const name = "stress-provider-wired"
	pool.SetUsageManager(name, um)

	stop := make(chan struct{})
	var aux sync.WaitGroup
	// Rare writer: contends usageMu against TrackRead's RLock.
	aux.Add(1)
	go func() {
		defer aux.Done()
		for {
			select {
			case <-stop:
				return
			default:
				pool.SetUsageManager(name, um)
				_ = pool.TotalMegabytes()
				_ = um.GetUsage(name)
			}
		}
	}()

	var workers sync.WaitGroup
	for w := 0; w < stressWorkers; w++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < stressPerWorker; i++ {
				pool.TrackRead(stressChunk)
			}
		}()
	}
	workers.Wait()
	close(stop)
	aux.Wait()

	want := int64(stressWorkers) * int64(stressPerWorker) * int64(stressChunk)
	if got := pool.totalBytesRead.Load(); got != want {
		t.Fatalf("totalBytesRead = %d, want %d", got, want)
	}
	usage := um.GetUsage(name)
	if usage == nil {
		t.Fatal("GetUsage returned nil")
	}
	if usage.AllTimeBytes != want {
		t.Fatalf("AllTimeBytes = %d, want %d", usage.AllTimeBytes, want)
	}
	if usage.TotalBytes != want {
		t.Fatalf("TotalBytes = %d, want %d", usage.TotalBytes, want)
	}
}

// TestStressAddBytesConcurrentMultiProvider validates the ProviderUsageManager
// under concurrent AddBytes across several providers while other goroutines take
// snapshots, read usage, and persist — all the operations the flusher and stat
// collector perform. Exact per-provider sums must hold and the dirty flag must be
// set (AddBytes never clears it; only flushLoop does).
func TestStressAddBytesConcurrentMultiProvider(t *testing.T) {
	um := newTestProviderUsageManager(t)
	providers := []string{"p-a", "p-b", "p-c", "p-d"}

	stop := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go func() {
		defer aux.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = um.snapshot()
				for _, p := range providers {
					_ = um.GetUsage(p)
				}
				if err := um.persistAndUpdateLast(); err != nil {
					t.Errorf("persistAndUpdateLast: %v", err)
					return
				}
			}
		}
	}()

	var workers sync.WaitGroup
	for _, p := range providers {
		for w := 0; w < stressWorkers; w++ {
			workers.Add(1)
			go func(name string) {
				defer workers.Done()
				for i := 0; i < stressPerWorker; i++ {
					um.AddBytes(name, stressChunk)
				}
			}(p)
		}
	}
	workers.Wait()
	close(stop)
	aux.Wait()

	wantPer := int64(stressWorkers) * int64(stressPerWorker) * int64(stressChunk)
	for _, p := range providers {
		usage := um.GetUsage(p)
		if usage == nil {
			t.Fatalf("GetUsage(%q) nil", p)
		}
		if usage.AllTimeBytes != wantPer {
			t.Fatalf("provider %q AllTimeBytes = %d, want %d", p, usage.AllTimeBytes, wantPer)
		}
		if usage.TotalBytes != wantPer {
			t.Fatalf("provider %q TotalBytes = %d, want %d", p, usage.TotalBytes, wantPer)
		}
	}

	um.mu.RLock()
	dirty := um.dirty
	um.mu.RUnlock()
	if !dirty {
		t.Fatal("expected dirty flag set after concurrent AddBytes (only flushLoop clears it)")
	}
}

// TestFlushDirtyHandshake validates the deferred-flush contract directly (without
// waiting on the 10s production ticker): AddBytes marks dirty; a flush reads+clears
// dirty then persists; a subsequent AddBytes re-marks dirty; and a successful
// persistAndUpdateLast advances lastPersisted to the snapshotted total.
func TestFlushDirtyHandshake(t *testing.T) {
	um := newTestProviderUsageManager(t)
	const name = "handshake"

	um.AddBytes(name, 100)
	um.mu.RLock()
	dirty := um.dirty
	um.mu.RUnlock()
	if !dirty {
		t.Fatal("AddBytes did not set dirty")
	}

	// Mimic exactly one flushLoop iteration.
	um.mu.Lock()
	d := um.dirty
	um.dirty = false
	um.mu.Unlock()
	if !d {
		t.Fatal("flush iteration observed dirty=false")
	}
	if err := um.persistAndUpdateLast(); err != nil {
		t.Fatalf("persistAndUpdateLast: %v", err)
	}

	um.mu.RLock()
	dirtyAfter := um.dirty
	last := um.lastPersisted[name]
	um.mu.RUnlock()
	if dirtyAfter {
		t.Fatal("dirty should remain cleared after flush with no new writes")
	}
	if last != 100 {
		t.Fatalf("lastPersisted = %d, want 100", last)
	}

	um.AddBytes(name, 50)
	um.mu.RLock()
	dirtyRemarked := um.dirty
	um.mu.RUnlock()
	if !dirtyRemarked {
		t.Fatal("AddBytes after flush did not re-mark dirty")
	}
}

// TestStressDayRolloverConcurrent seeds a provider with a stale reset day and a
// non-zero balance, then concurrently adds bytes. The first AddBytes to acquire
// the lock zeroes the daily counter (rollover), so the final daily TotalBytes must
// equal only the summed deltas while AllTimeBytes carries the seed forward.
func TestStressDayRolloverConcurrent(t *testing.T) {
	um := newTestProviderUsageManager(t)
	const name = "rollover"
	yesterday := time.Now().Add(-24 * time.Hour).Format("2006-01-02")
	um.data[name] = &ProviderUsageData{
		LastResetDay: yesterday,
		TotalBytes:   999,
		AllTimeBytes: 1000,
	}
	um.lastPersisted[name] = 999

	var workers sync.WaitGroup
	for w := 0; w < stressWorkers; w++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < stressPerWorker; i++ {
				um.AddBytes(name, stressChunk)
			}
		}()
	}
	workers.Wait()

	delta := int64(stressWorkers) * int64(stressPerWorker) * int64(stressChunk)
	usage := um.GetUsage(name)
	if usage.TotalBytes != delta {
		t.Fatalf("post-rollover TotalBytes = %d, want %d (seed must be zeroed)", usage.TotalBytes, delta)
	}
	if usage.AllTimeBytes != 1000+delta {
		t.Fatalf("AllTimeBytes = %d, want %d (seed carried forward)", usage.AllTimeBytes, 1000+delta)
	}
}

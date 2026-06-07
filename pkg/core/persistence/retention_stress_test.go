package persistence

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests validate Fix #4: the idx_nzb_attempts_slot_path index (existence +
// that the planner actually uses it on the hot slot_path lookup) and the
// provider_metrics / indexer_metrics retention purge (correctness, nil-safety,
// and behavior under concurrent record/delete with the race detector).

// countRows returns the row count of a table using the read lock, matching how
// production reads coordinate with withWriteLock.
func countRows(t *testing.T, m *StateManager, table string) int {
	t.Helper()
	m.mu.RLock()
	defer m.mu.RUnlock()
	var n int
	if err := m.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestSlotPathIndexExists confirms the index object is present in the schema.
func TestSlotPathIndexExists(t *testing.T) {
	mgr := newTestStateManager(t)
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	var name string
	err := mgr.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_nzb_attempts_slot_path'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("slot_path index not found: %v", err)
	}
	if name != "idx_nzb_attempts_slot_path" {
		t.Fatalf("index name = %q, want idx_nzb_attempts_slot_path", name)
	}
}

// TestSlotPathIndexUsedByQueryPlan asserts the planner uses the index for the hot
// playback lookup (WHERE slot_path = ? AND preload = 1) rather than scanning the
// whole table — the actual point of adding the index.
func TestSlotPathIndexUsedByQueryPlan(t *testing.T) {
	mgr := newTestStateManager(t)
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	rows, err := mgr.db.Query(
		`EXPLAIN QUERY PLAN SELECT id FROM nzb_attempts WHERE slot_path = ? AND preload = 1`,
		"some-slot",
	)
	if err != nil {
		t.Fatalf("explain query plan: %v", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	var plan strings.Builder
	for rows.Next() {
		cells := make([]interface{}, len(cols))
		for i := range cells {
			var s interface{}
			cells[i] = &s
		}
		if err := rows.Scan(cells...); err != nil {
			t.Fatalf("scan plan row: %v", err)
		}
		for _, c := range cells {
			plan.WriteString(fmt.Sprintf("%v ", *(c.(*interface{}))))
		}
		plan.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}

	planText := plan.String()
	if !strings.Contains(planText, "idx_nzb_attempts_slot_path") {
		t.Fatalf("query plan does not use slot_path index:\n%s", planText)
	}
	if strings.Contains(planText, "SCAN nzb_attempts") && !strings.Contains(planText, "USING INDEX") {
		t.Fatalf("query plan does a full table scan:\n%s", planText)
	}
}

// TestDeleteProviderMetricsBefore validates retention correctness: only rows
// strictly older than the cutoff are removed and the rest survive.
func TestDeleteProviderMetricsBefore(t *testing.T) {
	mgr := newTestStateManager(t)
	now := time.Now()
	old := now.Add(-48 * time.Hour)
	recent := now.Add(-1 * time.Hour)

	for i := 0; i < 5; i++ {
		if err := mgr.RecordMetricsSnapshot([]ProviderMetric{
			{CollectedAt: old, ProviderName: fmt.Sprintf("old-%d", i), Host: "h"},
		}, nil); err != nil {
			t.Fatalf("record old %d: %v", i, err)
		}
	}
	for i := 0; i < 3; i++ {
		if err := mgr.RecordMetricsSnapshot([]ProviderMetric{
			{CollectedAt: recent, ProviderName: fmt.Sprintf("new-%d", i), Host: "h"},
		}, nil); err != nil {
			t.Fatalf("record recent %d: %v", i, err)
		}
	}
	if got := countRows(t, mgr, "provider_metrics"); got != 8 {
		t.Fatalf("pre-delete rows = %d, want 8", got)
	}

	cutoff := now.Add(-24 * time.Hour)
	deleted, err := mgr.DeleteProviderMetricsBefore(cutoff)
	if err != nil {
		t.Fatalf("DeleteProviderMetricsBefore: %v", err)
	}
	if deleted != 5 {
		t.Fatalf("deleted = %d, want 5", deleted)
	}
	if got := countRows(t, mgr, "provider_metrics"); got != 3 {
		t.Fatalf("post-delete rows = %d, want 3", got)
	}

	// Idempotent: a second purge at the same cutoff deletes nothing.
	deleted2, err := mgr.DeleteProviderMetricsBefore(cutoff)
	if err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if deleted2 != 0 {
		t.Fatalf("second delete = %d, want 0", deleted2)
	}
}

// TestDeleteIndexerMetricsBefore mirrors the provider retention test for indexers.
func TestDeleteIndexerMetricsBefore(t *testing.T) {
	mgr := newTestStateManager(t)
	now := time.Now()
	old := now.Add(-72 * time.Hour)
	recent := now.Add(-30 * time.Minute)

	for i := 0; i < 4; i++ {
		if err := mgr.RecordMetricsSnapshot(nil, []IndexerMetric{
			{CollectedAt: old, IndexerName: fmt.Sprintf("old-%d", i)},
		}); err != nil {
			t.Fatalf("record old %d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := mgr.RecordMetricsSnapshot(nil, []IndexerMetric{
			{CollectedAt: recent, IndexerName: fmt.Sprintf("new-%d", i)},
		}); err != nil {
			t.Fatalf("record recent %d: %v", i, err)
		}
	}
	if got := countRows(t, mgr, "indexer_metrics"); got != 6 {
		t.Fatalf("pre-delete rows = %d, want 6", got)
	}

	cutoff := now.Add(-24 * time.Hour)
	deleted, err := mgr.DeleteIndexerMetricsBefore(cutoff)
	if err != nil {
		t.Fatalf("DeleteIndexerMetricsBefore: %v", err)
	}
	if deleted != 4 {
		t.Fatalf("deleted = %d, want 4", deleted)
	}
	if got := countRows(t, mgr, "indexer_metrics"); got != 2 {
		t.Fatalf("post-delete rows = %d, want 2", got)
	}
}

// TestDeleteMetricsNilReceiverSafe ensures the nil-guard in the retention helpers
// holds — the retention scheduler must never panic if the manager failed to init.
func TestDeleteMetricsNilReceiverSafe(t *testing.T) {
	var m *StateManager
	if n, err := m.DeleteProviderMetricsBefore(time.Now()); err != nil || n != 0 {
		t.Fatalf("nil provider delete = (%d,%v), want (0,nil)", n, err)
	}
	if n, err := m.DeleteIndexerMetricsBefore(time.Now()); err != nil || n != 0 {
		t.Fatalf("nil indexer delete = (%d,%v), want (0,nil)", n, err)
	}
}

// TestConcurrentRecordAndDeleteMetrics drives RecordMetricsSnapshot and the
// retention purge concurrently across both tables. The shared write lock must
// serialize them with no race and no panic; afterward every surviving row must be
// newer than the final cutoff.
func TestConcurrentRecordAndDeleteMetrics(t *testing.T) {
	mgr := newTestStateManager(t)
	base := time.Now()

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Writers: continually append fresh (recent) rows to both tables.
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				ts := base.Add(time.Duration(i) * time.Second)
				if err := mgr.RecordMetricsSnapshot(
					[]ProviderMetric{{CollectedAt: ts, ProviderName: fmt.Sprintf("p%d", w), Host: "h"}},
					[]IndexerMetric{{CollectedAt: ts, IndexerName: fmt.Sprintf("i%d", w)}},
				); err != nil {
					t.Errorf("record: %v", err)
					return
				}
			}
		}(w)
	}

	// Deleters: continually purge anything older than a moving cutoff in the past
	// (so they never delete the freshly-written rows, only ones that don't exist).
	for d := 0; d < 3; d++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				cutoff := base.Add(-1 * time.Hour)
				if _, err := mgr.DeleteProviderMetricsBefore(cutoff); err != nil {
					t.Errorf("delete provider: %v", err)
					return
				}
				if _, err := mgr.DeleteIndexerMetricsBefore(cutoff); err != nil {
					t.Errorf("delete indexer: %v", err)
					return
				}
			}
		}()
	}

	time.Sleep(150 * time.Millisecond)
	close(stop)
	wg.Wait()

	// All writers used timestamps >= base, and deleters used base-1h, so nothing
	// the writers wrote should have been purged. Both tables must be non-empty and
	// readable (proves no corruption / no deadlock).
	if got := countRows(t, mgr, "provider_metrics"); got == 0 {
		t.Fatal("provider_metrics empty after concurrent record/delete")
	}
	if got := countRows(t, mgr, "indexer_metrics"); got == 0 {
		t.Fatal("indexer_metrics empty after concurrent record/delete")
	}

	// A final purge older than everything wipes both tables cleanly.
	future := base.Add(24 * time.Hour)
	if _, err := mgr.DeleteProviderMetricsBefore(future); err != nil {
		t.Fatalf("final provider purge: %v", err)
	}
	if _, err := mgr.DeleteIndexerMetricsBefore(future); err != nil {
		t.Fatalf("final indexer purge: %v", err)
	}
	if got := countRows(t, mgr, "provider_metrics"); got != 0 {
		t.Fatalf("provider_metrics = %d after full purge, want 0", got)
	}
	if got := countRows(t, mgr, "indexer_metrics"); got != 0 {
		t.Fatalf("indexer_metrics = %d after full purge, want 0", got)
	}
}

package session

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These tests validate Fix #6: maybeFreeOSMemory debounces the stop-the-world
// debug.FreeOSMemory() to at most once per minFreeOSMemoryInterval, even under a
// concurrent burst of session teardowns. They substitute the freeOSMemory var
// (its sole reason for being a var) with a counter so the debounce is asserted
// without invoking a real GC. They mutate package globals, so they must not run
// in parallel; globals are saved and restored.

// withFreeOSMemoryStub swaps in a counting stub and a starting timestamp, and
// returns the counter plus a restore function.
func withFreeOSMemoryStub(t *testing.T, start int64) (*atomic.Int64, func()) {
	t.Helper()
	savedFn := freeOSMemory
	savedLast := lastFreeOSMemoryNanos.Load()

	var calls atomic.Int64
	freeOSMemory = func() { calls.Add(1) }
	lastFreeOSMemoryNanos.Store(start)

	return &calls, func() {
		freeOSMemory = savedFn
		lastFreeOSMemoryNanos.Store(savedLast)
	}
}

// TestMaybeFreeOSMemoryEarlyReturnWithinWindow asserts that a call inside the
// debounce window does nothing and does not advance the timestamp.
func TestMaybeFreeOSMemoryEarlyReturnWithinWindow(t *testing.T) {
	recent := time.Now().UnixNano()
	calls, restore := withFreeOSMemoryStub(t, recent)
	defer restore()

	maybeFreeOSMemory()

	if got := calls.Load(); got != 0 {
		t.Fatalf("freeOSMemory calls = %d, want 0 (within debounce window)", got)
	}
	if got := lastFreeOSMemoryNanos.Load(); got != recent {
		t.Fatalf("timestamp = %d, want unchanged %d (early return must not CAS)", got, recent)
	}
}

// TestMaybeFreeOSMemoryRunsAfterWindow asserts that once the window has elapsed a
// single call runs freeOSMemory exactly once and advances the timestamp.
func TestMaybeFreeOSMemoryRunsAfterWindow(t *testing.T) {
	start := time.Now().Add(-minFreeOSMemoryInterval - time.Second).UnixNano()
	calls, restore := withFreeOSMemoryStub(t, start)
	defer restore()

	maybeFreeOSMemory()

	if got := calls.Load(); got != 1 {
		t.Fatalf("freeOSMemory calls = %d, want 1 (window elapsed)", got)
	}
	if lastFreeOSMemoryNanos.Load() <= start {
		t.Fatalf("timestamp not advanced after run: still <= %d", start)
	}

	// An immediate second call is back inside the window and must be a no-op.
	maybeFreeOSMemory()
	if got := calls.Load(); got != 1 {
		t.Fatalf("freeOSMemory calls = %d after immediate re-call, want 1 (debounced)", got)
	}
}

// TestMaybeFreeOSMemoryConcurrentBurstCollapses is the core debounce test: with
// the window open, a large concurrent burst of teardown calls must collapse to
// exactly one freeOSMemory invocation — the CompareAndSwap lets only one goroutine
// win the window.
func TestMaybeFreeOSMemoryConcurrentBurstCollapses(t *testing.T) {
	start := time.Now().Add(-minFreeOSMemoryInterval - time.Second).UnixNano()
	calls, restore := withFreeOSMemoryStub(t, start)
	defer restore()

	const n = 128
	startCh := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startCh
			maybeFreeOSMemory()
		}()
	}
	close(startCh)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("freeOSMemory calls = %d, want exactly 1 (burst must collapse to one GC)", got)
	}
}

// TestMaybeFreeOSMemorySuccessiveWindows verifies the debounce re-opens: each time
// the window elapses, exactly one more invocation is allowed.
func TestMaybeFreeOSMemorySuccessiveWindows(t *testing.T) {
	start := time.Now().Add(-minFreeOSMemoryInterval - time.Second).UnixNano()
	calls, restore := withFreeOSMemoryStub(t, start)
	defer restore()

	maybeFreeOSMemory()
	if got := calls.Load(); got != 1 {
		t.Fatalf("after window 1: calls = %d, want 1", got)
	}

	// Force the window open again and fire a second burst.
	lastFreeOSMemoryNanos.Store(time.Now().Add(-minFreeOSMemoryInterval - time.Second).UnixNano())
	const n = 32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			maybeFreeOSMemory()
		}()
	}
	wg.Wait()

	if got := calls.Load(); got != 2 {
		t.Fatalf("after window 2: calls = %d, want 2 (one per open window)", got)
	}
}

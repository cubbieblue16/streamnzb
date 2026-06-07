package availnzb

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These tests exercise Fix #2 for AvailNZB: the singleflight coalescing of
// concurrent identical GetReleases calls and the short-TTL releasesCache, plus
// the crude max-entries bound on the cache. They run under the race detector.

// imdbBody renders a minimal releases response that echoes the imdb id so the
// caller can prove it got the right result back.
func imdbBody(imdbID string) string {
	return fmt.Sprintf(`{"imdb_id":%q,"count":1,"releases":[{"url":"u","release_name":"R","available":true,"indexer":"idx"}]}`, imdbID)
}

func imdbFromPath(path string) string {
	return path[strings.LastIndex(path, "/")+1:]
}

// TestGetReleasesCachesRepeatCalls validates the basic cache: a repeat lookup of
// the same key is served from cache and never re-hits upstream, then a distinct
// key re-fetches.
func TestGetReleasesCachesRepeatCalls(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(imdbBody(imdbFromPath(r.URL.Path))))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key")

	r1, err := c.GetReleases("tt111", "", "", 0, 0, nil, nil)
	if err != nil {
		t.Fatalf("GetReleases 1: %v", err)
	}
	if r1 == nil || r1.ImdbID != "tt111" {
		t.Fatalf("result 1 = %+v, want imdb tt111", r1)
	}

	r2, err := c.GetReleases("tt111", "", "", 0, 0, nil, nil)
	if err != nil {
		t.Fatalf("GetReleases 2: %v", err)
	}
	if r2 == nil || r2.ImdbID != "tt111" {
		t.Fatalf("result 2 = %+v, want imdb tt111", r2)
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("upstream hits = %d, want 1 (second call must be cached)", got)
	}

	// Distinct key => one more upstream hit.
	if _, err := c.GetReleases("tt222", "", "", 0, 0, nil, nil); err != nil {
		t.Fatalf("GetReleases 3: %v", err)
	}
	if got := atomic.LoadInt64(&hits); got != 2 {
		t.Fatalf("upstream hits = %d, want 2 after distinct key", got)
	}
}

// TestGetReleasesSingleflightCoalescesBurst fires a concurrent burst of identical
// lookups at a handler that blocks until released. Because the first request is
// still in-flight when the others arrive, singleflight collapses them into a
// single upstream hit and fans the one result out to all callers.
func TestGetReleasesSingleflightCoalescesBurst(t *testing.T) {
	var hits int64
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		<-release // hold the request in-flight so the burst coalesces
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(imdbBody(imdbFromPath(r.URL.Path))))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key")

	const n = 32
	var launched sync.WaitGroup
	var done sync.WaitGroup
	results := make([]*ReleasesResult, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		launched.Add(1)
		done.Add(1)
		go func(idx int) {
			defer done.Done()
			launched.Done()
			results[idx], errs[idx] = c.GetReleases("tt-burst", "", "", 0, 0, nil, nil)
		}(i)
	}
	// Wait until every goroutine has entered GetReleases, then give them a moment
	// to settle into the singleflight wait before unblocking the one in-flight
	// request.
	launched.Wait()
	time.Sleep(75 * time.Millisecond)
	close(release)
	done.Wait()

	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("upstream hits = %d, want 1 (singleflight must coalesce the burst)", got)
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("caller %d err: %v", i, errs[i])
		}
		if results[i] == nil || results[i].ImdbID != "tt-burst" {
			t.Fatalf("caller %d result = %+v, want imdb tt-burst", i, results[i])
		}
	}

	// After the in-flight call resolved it populated the cache; a follow-up adds
	// zero hits.
	if _, err := c.GetReleases("tt-burst", "", "", 0, 0, nil, nil); err != nil {
		t.Fatalf("follow-up GetReleases: %v", err)
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("follow-up added upstream hits, got %d want 1 (cache)", got)
	}
}

// TestGetReleasesTTLExpiryRefetches forces the cached entry to expire and asserts
// the next call re-hits upstream (availability is volatile, so stale entries must
// not be served past the TTL).
func TestGetReleasesTTLExpiryRefetches(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(imdbBody(imdbFromPath(r.URL.Path))))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key")

	if _, err := c.GetReleases("tt-ttl", "", "", 0, 0, nil, nil); err != nil {
		t.Fatalf("GetReleases 1: %v", err)
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("hits = %d, want 1", got)
	}

	// Expire every cached entry.
	c.releasesCacheMu.Lock()
	for k, e := range c.releasesCache {
		e.expiresAt = time.Now().Add(-time.Hour)
		c.releasesCache[k] = e
	}
	c.releasesCacheMu.Unlock()

	if _, err := c.GetReleases("tt-ttl", "", "", 0, 0, nil, nil); err != nil {
		t.Fatalf("GetReleases 2 (post-expiry): %v", err)
	}
	if got := atomic.LoadInt64(&hits); got != 2 {
		t.Fatalf("hits = %d, want 2 (expired entry must refetch)", got)
	}
}

// TestGetReleasesErrorNotCached ensures upstream failures are never cached, so a
// transient error does not get pinned for the whole TTL.
func TestGetReleasesErrorNotCached(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key")

	for i := 0; i < 3; i++ {
		if _, err := c.GetReleases("tt-err", "", "", 0, 0, nil, nil); err == nil {
			t.Fatalf("call %d: expected error from 500", i)
		}
	}
	if got := atomic.LoadInt64(&hits); got != 3 {
		t.Fatalf("hits = %d, want 3 (errors must not be cached)", got)
	}
	c.releasesCacheMu.RLock()
	n := len(c.releasesCache)
	c.releasesCacheMu.RUnlock()
	if n != 0 {
		t.Fatalf("cache size = %d, want 0 (no error entry should be stored)", n)
	}
}

// TestSetCachedReleasesWholesaleReset validates the crude bound: at capacity the
// cache map is dropped wholesale rather than growing without limit.
func TestSetCachedReleasesWholesaleReset(t *testing.T) {
	c := NewClient("http://example.invalid", "key")
	for i := 0; i < availReleasesCacheMaxEntries; i++ {
		c.setCachedReleases(fmt.Sprintf("k-%d", i), &ReleasesResult{ImdbID: "x"})
	}
	c.releasesCacheMu.RLock()
	full := len(c.releasesCache)
	c.releasesCacheMu.RUnlock()
	if full != availReleasesCacheMaxEntries {
		t.Fatalf("len at capacity = %d, want %d", full, availReleasesCacheMaxEntries)
	}

	c.setCachedReleases("overflow", &ReleasesResult{ImdbID: "y"})
	c.releasesCacheMu.RLock()
	after := len(c.releasesCache)
	c.releasesCacheMu.RUnlock()
	if after != 1 {
		t.Fatalf("len after overflow = %d, want 1 (wholesale reset)", after)
	}
}

// TestGetReleasesConcurrentDistinctKeys drives many distinct lookups, each from
// several goroutines at once, then verifies every result is correct and a second
// sequential pass adds zero upstream hits — exercising concurrent singleflight +
// cache writes under the race detector.
func TestGetReleasesConcurrentDistinctKeys(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(imdbBody(imdbFromPath(r.URL.Path))))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key")

	const distinct = 24
	const perKey = 6
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < distinct*perKey; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("tt%d", i%distinct)
			<-start
			res, err := c.GetReleases(id, "", "", 0, 0, nil, nil)
			if err != nil {
				t.Errorf("GetReleases(%s): %v", id, err)
				return
			}
			if res == nil || res.ImdbID != id {
				t.Errorf("GetReleases(%s) = %+v, want imdb %s", id, res, id)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	burst := atomic.LoadInt64(&hits)
	if burst < distinct || burst > int64(distinct*perKey) {
		t.Fatalf("burst hits = %d, want within [%d,%d]", burst, distinct, distinct*perKey)
	}

	// Second sequential pass: everything must be cached now.
	for i := 0; i < distinct; i++ {
		id := fmt.Sprintf("tt%d", i)
		if _, err := c.GetReleases(id, "", "", 0, 0, nil, nil); err != nil {
			t.Fatalf("cached GetReleases(%s): %v", id, err)
		}
	}
	if got := atomic.LoadInt64(&hits); got != burst {
		t.Fatalf("second pass added hits: %d -> %d (should all be cached)", burst, got)
	}
}

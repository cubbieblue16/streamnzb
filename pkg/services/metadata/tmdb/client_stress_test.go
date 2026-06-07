package tmdb

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"streamnzb/pkg/core/logger"
)

func init() {
	logger.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
}

// drainResp reads and closes a response body, returning its bytes. The cached and
// live paths must both yield a readable, independently-closeable body.
func drainResp(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close body: %v", err)
	}
	return b
}

// TestDoRequestCachesRepeatCalls validates the core Fix #2 behavior: a repeated
// (endpoint, params) hits the upstream once, then is served from cache; a different
// params value is a distinct key and re-fetches.
func TestDoRequestCachesRepeatCalls(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":42}`))
	}))
	defer srv.Close()

	// Bearer-style key (contains dots) => doRequest does not mutate params.
	c := NewClient("jwt.header.payload")

	mk := func() url.Values { p := url.Values{}; p.Set("language", "en"); return p }

	r1, err := c.doRequest(srv.URL, mk())
	if err != nil {
		t.Fatalf("doRequest 1: %v", err)
	}
	b1 := drainResp(t, r1)

	r2, err := c.doRequest(srv.URL, mk())
	if err != nil {
		t.Fatalf("doRequest 2: %v", err)
	}
	b2 := drainResp(t, r2)

	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("upstream hits = %d, want 1 (second call must be cached)", got)
	}
	if string(b1) != `{"id":42}` || string(b2) != `{"id":42}` {
		t.Fatalf("cached body mismatch: %q / %q", b1, b2)
	}

	// Distinct params => distinct cache key => one more upstream hit.
	p3 := url.Values{}
	p3.Set("language", "de")
	r3, err := c.doRequest(srv.URL, p3)
	if err != nil {
		t.Fatalf("doRequest 3: %v", err)
	}
	_ = drainResp(t, r3)
	if got := atomic.LoadInt64(&hits); got != 2 {
		t.Fatalf("upstream hits = %d, want 2 after distinct params", got)
	}
}

// TestDoRequestCacheKeyExcludesAPIKey ensures a v3 key (injected as the api_key
// query param) never pollutes the cache key, so repeat calls still coalesce.
func TestDoRequestCacheKeyExcludesAPIKey(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	// v3 key (no dots) => doRequest sets api_key on params; pass a fresh map each call.
	c := NewClient("deadbeefdeadbeefdeadbeefdeadbeef")

	for i := 0; i < 5; i++ {
		p := url.Values{}
		p.Set("q", "same")
		r, err := c.doRequest(srv.URL, p)
		if err != nil {
			t.Fatalf("doRequest %d: %v", i, err)
		}
		_ = drainResp(t, r)
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("upstream hits = %d, want 1 (api_key must be excluded from cache key)", got)
	}
}

// TestDoRequestConcurrentBurstThenCached fires a concurrent burst at a cold cache
// (TMDB has no singleflight, so the burst may multi-hit), then asserts the cache is
// populated and a follow-up adds zero hits — all under the race detector.
func TestDoRequestConcurrentBurstThenCached(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		time.Sleep(2 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"v":1}`))
	}))
	defer srv.Close()

	c := NewClient("jwt.a.b")

	const n = 48
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			p := url.Values{}
			p.Set("id", "100")
			r, err := c.doRequest(srv.URL, p)
			if err != nil {
				t.Errorf("doRequest: %v", err)
				return
			}
			b := drainResp(t, r)
			if string(b) != `{"v":1}` {
				t.Errorf("body = %q", b)
			}
		}()
	}
	close(start)
	wg.Wait()

	burst := atomic.LoadInt64(&hits)
	if burst < 1 || burst > n {
		t.Fatalf("burst hits = %d, want within [1,%d]", burst, n)
	}

	// Follow-up must be served from cache: zero additional upstream hits.
	p := url.Values{}
	p.Set("id", "100")
	r, err := c.doRequest(srv.URL, p)
	if err != nil {
		t.Fatalf("follow-up doRequest: %v", err)
	}
	_ = drainResp(t, r)
	if got := atomic.LoadInt64(&hits); got != burst {
		t.Fatalf("follow-up added upstream hits: %d -> %d (should be cached)", burst, got)
	}
}

// TestDoRequestNon200NotCached ensures non-200 responses are passed through and
// never cached, so transient errors don't get stuck.
func TestDoRequestNon200NotCached(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	}))
	defer srv.Close()

	c := NewClient("jwt.a.b")
	for i := 0; i < 3; i++ {
		p := url.Values{}
		p.Set("id", "7")
		r, err := c.doRequest(srv.URL, p)
		if err != nil {
			t.Fatalf("doRequest %d: %v", i, err)
		}
		if r.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", r.StatusCode)
		}
		_ = drainResp(t, r)
	}
	if got := atomic.LoadInt64(&hits); got != 3 {
		t.Fatalf("upstream hits = %d, want 3 (404s must not be cached)", got)
	}
}

// TestCacheGetSetTTLExpiry validates the TTL path: an expired entry is treated as a
// miss and purged.
func TestCacheGetSetTTLExpiry(t *testing.T) {
	c := NewClient("jwt.a.b")
	c.cacheSet("k", []byte("v"))

	if got, ok := c.cacheGet("k"); !ok || string(got) != "v" {
		t.Fatalf("fresh cacheGet = (%q,%v), want (v,true)", got, ok)
	}

	// Force expiry.
	c.cacheMu.Lock()
	e := c.cache["k"]
	e.expiresAt = time.Now().Add(-time.Hour)
	c.cache["k"] = e
	c.cacheMu.Unlock()

	if _, ok := c.cacheGet("k"); ok {
		t.Fatal("expired entry returned as hit")
	}
	c.cacheMu.RLock()
	_, present := c.cache["k"]
	c.cacheMu.RUnlock()
	if present {
		t.Fatal("expired entry not purged on read")
	}
}

// TestCacheMaxEntriesWholesaleReset validates the crude bound: at capacity the map
// is dropped wholesale rather than growing without limit.
func TestCacheMaxEntriesWholesaleReset(t *testing.T) {
	c := NewClient("jwt.a.b")
	for i := 0; i < tmdbCacheMaxEntries; i++ {
		c.cacheSet(string(rune(i))+"-k", []byte("x"))
	}
	c.cacheMu.RLock()
	full := len(c.cache)
	c.cacheMu.RUnlock()
	if full != tmdbCacheMaxEntries {
		t.Fatalf("len at capacity = %d, want %d", full, tmdbCacheMaxEntries)
	}

	c.cacheSet("overflow", []byte("x"))
	c.cacheMu.RLock()
	after := len(c.cache)
	c.cacheMu.RUnlock()
	if after != 1 {
		t.Fatalf("len after overflow = %d, want 1 (wholesale reset)", after)
	}
}

// TestCacheConcurrentSetGet stresses the cache map with concurrent readers and
// writers across overlapping and distinct keys (race detector only assertion).
func TestCacheConcurrentSetGet(t *testing.T) {
	c := NewClient("jwt.a.b")
	const workers = 32
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				key := string(rune('a'+(i%8))) + string(rune('0'+id%10))
				c.cacheSet(key, []byte("payload"))
				_, _ = c.cacheGet(key)
				_, _ = c.cacheGet("shared")
				if i%50 == 0 {
					c.cacheSet("shared", []byte("s"))
				}
			}
		}(w)
	}
	wg.Wait()
}

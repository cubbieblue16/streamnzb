package tvdb

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"streamnzb/pkg/core/logger"
	"streamnzb/pkg/core/persistence"
)

func init() {
	logger.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
		Header:     make(http.Header),
	}
}

// remoteIDToSeries maps a remote ID to a deterministic, non-zero TVDB series ID so
// the fake transport and the test assertions agree.
func remoteIDToSeries(id string) int {
	s := 0
	for _, b := range []byte(id) {
		s += int(b)
	}
	if s == 0 {
		s = 1
	}
	return s
}

type tvdbCounters struct {
	login  int64
	search int64
}

// newTVDBTestClient returns a client whose HTTP transport is faked and whose shared
// persistence token has been cleared so ensureToken performs a real (fake) login.
func newTVDBTestClient(t *testing.T, ctr *tvdbCounters) *Client {
	t.Helper()
	dir := t.TempDir()
	mgr, err := persistence.GetManager(dir)
	if err != nil {
		t.Fatalf("GetManager: %v", err)
	}
	// The persistence manager is a process-wide singleton, so a prior test may have
	// persisted a token. Clear it so this client's ensureToken logs in fresh.
	if err := mgr.Delete(stateKey); err != nil {
		t.Fatalf("clear token: %v", err)
	}

	c := NewClient("test-api-key", dir)
	c.client.Transport = rtFunc(func(r *http.Request) (*http.Response, error) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/login"):
			atomic.AddInt64(&ctr.login, 1)
			return jsonResp(http.StatusOK, `{"status":"success","data":{"token":"fake-token"}}`), nil
		case strings.Contains(path, "/search/remoteid/"):
			atomic.AddInt64(&ctr.search, 1)
			remote := path[strings.LastIndex(path, "/")+1:]
			series := remoteIDToSeries(remote)
			return jsonResp(http.StatusOK, fmt.Sprintf(`{"status":"success","data":[{"episode":{"seriesId":%d}}]}`, series)), nil
		default:
			return jsonResp(http.StatusNotFound, `{"status":"failure"}`), nil
		}
	})
	return c
}

// TestResolveTVDBIDCachesResolution validates Fix #2 for TVDB: a remote ID resolves
// once over the network, then from the resolveCache; the token is reused (one login).
func TestResolveTVDBIDCachesResolution(t *testing.T) {
	var ctr tvdbCounters
	c := newTVDBTestClient(t, &ctr)

	want1 := strconv.Itoa(remoteIDToSeries("imdb111"))
	id1, err := c.ResolveTVDBID("imdb111")
	if err != nil {
		t.Fatalf("resolve 1: %v", err)
	}
	if id1 != want1 {
		t.Fatalf("id1 = %q, want %q", id1, want1)
	}
	if got := atomic.LoadInt64(&ctr.search); got != 1 {
		t.Fatalf("search hits = %d, want 1", got)
	}
	if got := atomic.LoadInt64(&ctr.login); got != 1 {
		t.Fatalf("login hits = %d, want 1", got)
	}

	id2, err := c.ResolveTVDBID("imdb111")
	if err != nil {
		t.Fatalf("resolve 2: %v", err)
	}
	if id2 != id1 {
		t.Fatalf("id2 = %q, want %q", id2, id1)
	}
	if got := atomic.LoadInt64(&ctr.search); got != 1 {
		t.Fatalf("search hits after cache = %d, want 1", got)
	}

	// New remote ID => one more search, but the token is reused (still one login).
	want3 := strconv.Itoa(remoteIDToSeries("imdb222"))
	id3, err := c.ResolveTVDBID("imdb222")
	if err != nil {
		t.Fatalf("resolve 3: %v", err)
	}
	if id3 != want3 {
		t.Fatalf("id3 = %q, want %q", id3, want3)
	}
	if got := atomic.LoadInt64(&ctr.search); got != 2 {
		t.Fatalf("search hits = %d, want 2", got)
	}
	if got := atomic.LoadInt64(&ctr.login); got != 1 {
		t.Fatalf("login hits = %d, want 1 (token reused)", got)
	}
}

// TestEnsureTokenConcurrentSingleLogin validates the mutex that guards tokenCache
// (Fix #2 also closed a pre-existing tokenCache data race): a concurrent cold burst
// of ensureToken serializes into exactly one login, and all callers get the token.
func TestEnsureTokenConcurrentSingleLogin(t *testing.T) {
	var ctr tvdbCounters
	c := newTVDBTestClient(t, &ctr)

	const n = 50
	start := make(chan struct{})
	var wg sync.WaitGroup
	tokens := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			tokens[idx], errs[idx] = c.ensureToken()
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("ensureToken[%d]: %v", i, errs[i])
		}
		if tokens[i] != "fake-token" {
			t.Fatalf("token[%d] = %q, want fake-token", i, tokens[i])
		}
	}
	if got := atomic.LoadInt64(&ctr.login); got != 1 {
		t.Fatalf("login hits = %d, want 1 (mutex must dedupe concurrent logins)", got)
	}
}

// TestResolveTVDBIDConcurrentDistinctAndCached resolves many distinct remote IDs,
// each from several goroutines at once, then verifies every value is correct and a
// second pass adds zero network hits — exercising concurrent resolveCache writes
// under the race detector.
func TestResolveTVDBIDConcurrentDistinctAndCached(t *testing.T) {
	var ctr tvdbCounters
	c := newTVDBTestClient(t, &ctr)

	const distinct = 16
	const perKey = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < distinct*perKey; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			remote := fmt.Sprintf("imdb%d", i%distinct)
			<-start
			id, err := c.ResolveTVDBID(remote)
			if err != nil {
				t.Errorf("resolve %s: %v", remote, err)
				return
			}
			if want := strconv.Itoa(remoteIDToSeries(remote)); id != want {
				t.Errorf("resolve %s = %q, want %q", remote, id, want)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	hitsAfterBurst := atomic.LoadInt64(&ctr.search)
	if hitsAfterBurst < distinct {
		t.Fatalf("search hits = %d, want >= %d (each distinct key fetched at least once)", hitsAfterBurst, distinct)
	}

	// Second sequential pass: everything must now be cached.
	for i := 0; i < distinct; i++ {
		remote := fmt.Sprintf("imdb%d", i)
		id, err := c.ResolveTVDBID(remote)
		if err != nil {
			t.Fatalf("cached resolve %s: %v", remote, err)
		}
		if want := strconv.Itoa(remoteIDToSeries(remote)); id != want {
			t.Fatalf("cached resolve %s = %q, want %q", remote, id, want)
		}
	}
	if got := atomic.LoadInt64(&ctr.search); got != hitsAfterBurst {
		t.Fatalf("second pass added network hits: %d -> %d (should all be cached)", hitsAfterBurst, got)
	}
}

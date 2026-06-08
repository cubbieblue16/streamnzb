package indexer

import (
	"context"
	"encoding/xml"
	"sync/atomic"
	"testing"
)

// quotaIndexer is a mock that exposes a controllable Usage and records whether
// its Search / DownloadNZB methods were actually invoked, so tests can assert an
// exhausted indexer was skipped rather than merely producing no results.
type quotaIndexer struct {
	name        string
	usage       Usage
	searchCalls int32
	dlCalls     int32
	items       []Item
}

func (q *quotaIndexer) Search(req SearchRequest) (*SearchResponse, error) {
	atomic.AddInt32(&q.searchCalls, 1)
	return &SearchResponse{
		XMLName: xml.Name{Local: "rss"},
		Channel: Channel{Items: q.items},
	}, nil
}

func (q *quotaIndexer) DownloadNZB(ctx context.Context, nzbURL string) ([]byte, error) {
	atomic.AddInt32(&q.dlCalls, 1)
	return []byte(q.name), nil
}

func (q *quotaIndexer) Ping() error     { return nil }
func (q *quotaIndexer) Name() string    { return q.name }
func (q *quotaIndexer) GetUsage() Usage { return q.usage }

func (q *quotaIndexer) searched() bool   { return atomic.LoadInt32(&q.searchCalls) > 0 }
func (q *quotaIndexer) downloaded() bool { return atomic.LoadInt32(&q.dlCalls) > 0 }

func apiExhausted(name string) *quotaIndexer {
	return &quotaIndexer{name: name, usage: Usage{APIHitsLimit: 100, APIHitsUsed: 100, APIHitsRemaining: 0}}
}

func apiHeadroom(name string) *quotaIndexer {
	return &quotaIndexer{name: name, usage: Usage{APIHitsLimit: 100, APIHitsUsed: 10, APIHitsRemaining: 90}}
}

func names(idxs []Indexer) []string {
	out := make([]string, len(idxs))
	for i, idx := range idxs {
		out[i] = idx.Name()
	}
	return out
}

func equalNames(got []Indexer, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i].Name() != want[i] {
			return false
		}
	}
	return true
}

func TestEligibleIndexersSkipsExhaustedSearch(t *testing.T) {
	a := apiExhausted("a")
	b := apiHeadroom("b")
	got := eligibleIndexers([]Indexer{a, b}, true, false)
	if !equalNames(got, "b") {
		t.Fatalf("expected only [b] with headroom, got %v", names(got))
	}
}

func TestEligibleIndexersPreservesPriorityOrder(t *testing.T) {
	first := apiHeadroom("first")
	middle := apiExhausted("middle")
	last := apiHeadroom("last")
	got := eligibleIndexers([]Indexer{first, middle, last}, true, false)
	if !equalNames(got, "first", "last") {
		t.Fatalf("expected priority order [first last] with middle dropped, got %v", names(got))
	}
}

func TestEligibleIndexersAllExhaustedFallsBackToFull(t *testing.T) {
	a := apiExhausted("a")
	b := apiExhausted("b")
	got := eligibleIndexers([]Indexer{a, b}, true, false)
	if !equalNames(got, "a", "b") {
		t.Fatalf("all-exhausted should fall back to the full list, got %v", names(got))
	}
}

func TestEligibleIndexersDisabledReturnsAll(t *testing.T) {
	a := apiExhausted("a")
	b := apiHeadroom("b")
	got := eligibleIndexers([]Indexer{a, b}, false, false)
	if !equalNames(got, "a", "b") {
		t.Fatalf("quota-aware off should return all, got %v", names(got))
	}
}

func TestEligibleIndexersSingleIndexerAlwaysReturned(t *testing.T) {
	a := apiExhausted("a")
	got := eligibleIndexers([]Indexer{a}, true, false)
	if !equalNames(got, "a") {
		t.Fatalf("a single exhausted indexer must still be returned, got %v", names(got))
	}
}

func TestEligibleIndexersDownloadUsesDownloadQuota(t *testing.T) {
	// API has headroom but downloads are exhausted: eligible for search, not for download.
	a := &quotaIndexer{name: "a", usage: Usage{
		APIHitsLimit: 100, APIHitsRemaining: 90,
		DownloadsLimit: 10, DownloadsUsed: 10, DownloadsRemaining: 0,
	}}
	b := &quotaIndexer{name: "b", usage: Usage{
		APIHitsLimit: 100, APIHitsRemaining: 90,
		DownloadsLimit: 10, DownloadsUsed: 1, DownloadsRemaining: 9,
	}}
	if got := eligibleIndexers([]Indexer{a, b}, true, false); !equalNames(got, "a", "b") {
		t.Fatalf("search path should keep both (API headroom), got %v", names(got))
	}
	if got := eligibleIndexers([]Indexer{a, b}, true, true); !equalNames(got, "b") {
		t.Fatalf("download path should drop a (downloads exhausted), got %v", names(got))
	}
}

func TestEligibleIndexersUnknownLimitCountsAsHeadroom(t *testing.T) {
	// Limit <= 0 means unknown/unlimited and must count as headroom.
	unknown := &quotaIndexer{name: "unknown", usage: Usage{APIHitsLimit: 0}}
	exhausted := apiExhausted("exhausted")
	got := eligibleIndexers([]Indexer{exhausted, unknown}, true, false)
	if !equalNames(got, "unknown") {
		t.Fatalf("unknown-limit indexer should be eligible, exhausted dropped, got %v", names(got))
	}
}

func TestSearchCombinedSkipsExhaustedIndexer(t *testing.T) {
	exhausted := apiExhausted("exhausted")
	healthy := apiHeadroom("healthy")
	healthy.items = []Item{{Title: "rel", Size: 1}}

	agg := NewAggregator(exhausted, healthy)
	agg.QuotaAware = true

	resp, err := agg.Search(SearchRequest{})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if exhausted.searched() {
		t.Fatal("exhausted indexer should not have been searched")
	}
	if !healthy.searched() {
		t.Fatal("healthy indexer should have been searched")
	}
	if resp == nil || len(resp.Channel.Items) != 1 {
		t.Fatalf("expected 1 item from healthy indexer, got %#v", resp)
	}
}

func TestSearchCombinedDisabledSearchesAll(t *testing.T) {
	exhausted := apiExhausted("exhausted")
	healthy := apiHeadroom("healthy")

	agg := NewAggregator(exhausted, healthy)
	agg.QuotaAware = false

	if _, err := agg.Search(SearchRequest{}); err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if !exhausted.searched() {
		t.Fatal("with quota-awareness off, the exhausted indexer should still be searched")
	}
}

func TestDownloadNZBSkipsDownloadExhausted(t *testing.T) {
	exhausted := &quotaIndexer{name: "exhausted", usage: Usage{
		DownloadsLimit: 10, DownloadsUsed: 10, DownloadsRemaining: 0,
	}}
	healthy := &quotaIndexer{name: "healthy", usage: Usage{
		DownloadsLimit: 10, DownloadsUsed: 1, DownloadsRemaining: 9,
	}}

	agg := NewAggregator(exhausted, healthy)
	agg.QuotaAware = true

	data, err := agg.DownloadNZB(context.Background(), "http://example/nzb")
	if err != nil {
		t.Fatalf("DownloadNZB returned error: %v", err)
	}
	if exhausted.downloaded() {
		t.Fatal("download-exhausted indexer should have been skipped")
	}
	if string(data) != "healthy" {
		t.Fatalf("expected download served by healthy indexer, got %q", string(data))
	}
}

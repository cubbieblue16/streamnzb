package notifier

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"streamnzb/pkg/core/config"
)

// fakeChannel records the events it receives and which types it wants.
type fakeChannel struct {
	name  string
	wants map[EventType]bool

	mu       sync.Mutex
	received []Event
	failWith error
	sendN    int32
}

func (f *fakeChannel) Name() string { return f.name }
func (f *fakeChannel) Wants(t EventType) bool {
	if len(f.wants) == 0 {
		return true
	}
	return f.wants[t]
}
func (f *fakeChannel) Send(ctx context.Context, ev Event) error {
	atomic.AddInt32(&f.sendN, 1)
	if f.failWith != nil {
		return f.failWith
	}
	f.mu.Lock()
	f.received = append(f.received, ev)
	f.mu.Unlock()
	return nil
}
func (f *fakeChannel) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.received)
}

// waitFor polls until cond is true or the deadline elapses.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func TestEmitDeliversToWantingChannels(t *testing.T) {
	want := &fakeChannel{name: "wants", wants: map[EventType]bool{EventProviderDown: true}}
	other := &fakeChannel{name: "other", wants: map[EventType]bool{EventIndexerQuota: true}}

	n := New(Options{
		Enabled:  true,
		Events:   map[EventType]bool{EventProviderDown: true},
		Channels: []Channel{want, other},
	})
	n.Start()
	defer n.Stop()

	n.Emit(Event{Type: EventProviderDown, Title: "down", Message: "provider offline"})

	waitFor(t, func() bool { return want.count() == 1 })
	if other.count() != 0 {
		t.Fatalf("channel that did not want the type received %d events", other.count())
	}
}

func TestEmitSuppressedWhenMasterToggleOff(t *testing.T) {
	ch := &fakeChannel{name: "c"}
	n := New(Options{
		Enabled:  true,
		Events:   map[EventType]bool{EventProviderDown: false}, // explicitly off
		Channels: []Channel{ch},
	})
	n.Start()
	defer n.Stop()

	n.Emit(Event{Type: EventProviderDown, Title: "x"})
	n.Emit(Event{Type: EventIndexerQuota, Title: "y"}) // not in events map at all

	// Give the worker a chance; nothing should arrive.
	time.Sleep(50 * time.Millisecond)
	if ch.count() != 0 {
		t.Fatalf("expected 0 delivered events with master toggles off, got %d", ch.count())
	}
}

func TestEmitDisabledNotifierIsNoop(t *testing.T) {
	ch := &fakeChannel{name: "c"}
	n := New(Options{Enabled: false, Events: map[EventType]bool{EventProviderDown: true}, Channels: []Channel{ch}})
	n.Start()
	defer n.Stop()
	n.Emit(Event{Type: EventProviderDown, Title: "x"})
	time.Sleep(30 * time.Millisecond)
	if ch.count() != 0 {
		t.Fatalf("disabled notifier should deliver nothing, got %d", ch.count())
	}
}

func TestRateLimitDedup(t *testing.T) {
	ch := &fakeChannel{name: "c"}
	clock := int64(0)
	now := func() time.Time { return time.Unix(atomic.LoadInt64(&clock), 0) }
	n := New(Options{
		Enabled:     true,
		Events:      map[EventType]bool{EventPlaybackFailure: true},
		MinInterval: 60 * time.Second,
		Channels:    []Channel{ch},
		Now:         now,
	})
	n.Start()
	defer n.Stop()

	// Two identical events within the window -> only the first delivered.
	n.Emit(Event{Type: EventPlaybackFailure, Title: "same"})
	waitFor(t, func() bool { return ch.count() == 1 })
	n.Emit(Event{Type: EventPlaybackFailure, Title: "same"})
	time.Sleep(40 * time.Millisecond)
	if ch.count() != 1 {
		t.Fatalf("expected dedup to suppress the second event, got %d", ch.count())
	}

	// Advance past the window -> delivered again.
	atomic.StoreInt64(&clock, 120)
	n.Emit(Event{Type: EventPlaybackFailure, Title: "same"})
	waitFor(t, func() bool { return ch.count() == 2 })
}

func TestChannelSendErrorDoesNotBlockOthers(t *testing.T) {
	bad := &fakeChannel{name: "bad", failWith: errors.New("boom")}
	good := &fakeChannel{name: "good"}
	n := New(Options{
		Enabled:  true,
		Events:   map[EventType]bool{EventProviderDown: true},
		Channels: []Channel{bad, good},
	})
	n.Start()
	defer n.Stop()

	n.Emit(Event{Type: EventProviderDown, Title: "x"})
	waitFor(t, func() bool { return good.count() == 1 })
	if atomic.LoadInt32(&bad.sendN) == 0 {
		t.Fatal("failing channel should still have been attempted")
	}
}

func TestEmitNonBlockingUnderFloodAndNeverDropsBelowOne(t *testing.T) {
	// A blocking channel must not wedge Emit; excess events drop silently.
	release := make(chan struct{})
	blocker := &blockingChannel{release: release}
	n := New(Options{
		Enabled:  true,
		Events:   map[EventType]bool{EventProviderDown: true},
		Channels: []Channel{blocker},
	})
	n.Start()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100_000; i++ {
			n.Emit(Event{Type: EventProviderDown, Title: "flood"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		close(release)
		n.Stop()
		t.Fatal("Emit blocked under flood; should be non-blocking")
	}
	close(release)
	n.Stop()
}

type blockingChannel struct {
	release chan struct{}
	first   int32
}

func (b *blockingChannel) Name() string         { return "blocker" }
func (b *blockingChannel) Wants(EventType) bool { return true }
func (b *blockingChannel) Send(ctx context.Context, ev Event) error {
	if atomic.AddInt32(&b.first, 1) == 1 {
		select {
		case <-b.release:
		case <-ctx.Done():
		}
	}
	return nil
}

func TestTestChannelBypassesTogglesAndRateLimit(t *testing.T) {
	ch := &fakeChannel{name: "target"}
	n := New(Options{
		Enabled:     true,
		Events:      map[EventType]bool{}, // nothing enabled
		MinInterval: time.Hour,
		Channels:    []Channel{ch},
	})
	// No Start needed; TestChannel sends synchronously.
	if err := n.TestChannel(context.Background(), "target", Event{Type: EventProviderDown, Title: "test"}); err != nil {
		t.Fatalf("TestChannel returned error: %v", err)
	}
	if ch.count() != 1 {
		t.Fatalf("expected synthetic event delivered, got %d", ch.count())
	}
	if err := n.TestChannel(context.Background(), "nope", Event{}); err == nil {
		t.Fatal("expected error for unknown channel")
	}
}

func TestFromConfigBuildsChannelsAndSkipsInvalid(t *testing.T) {
	disabled := false
	cfg := &config.NotifierConfig{
		Enabled:            true,
		Events:             map[string]bool{"provider_down": true, "bogus_event": true},
		MinIntervalSeconds: 30,
		Channels: []config.NotifierChannelConfig{
			{Type: "webhook", URL: "http://example/hook", Name: "hook"},
			{Type: "ntfy", URL: "http://ntfy/topic"},
			{Type: "discord", URL: "http://discord/wh"},
			{Type: "webhook", URL: ""},                             // skipped: empty URL
			{Type: "bogus", URL: "http://x"},                       // skipped: unknown type
			{Type: "webhook", URL: "http://x", Enabled: &disabled}, // skipped: disabled
		},
	}
	n := FromConfig(cfg, nil, nil)
	if n == nil {
		t.Fatal("expected a notifier")
	}
	got := n.ChannelNames()
	if len(got) != 3 {
		t.Fatalf("expected 3 usable channels, got %d: %v", len(got), got)
	}
	if !n.events[EventProviderDown] || n.events["bogus_event"] {
		t.Fatalf("expected only valid event types enabled, got %v", n.events)
	}
}

func TestFromConfigDisabledReturnsNil(t *testing.T) {
	if FromConfig(nil, nil, nil) != nil {
		t.Fatal("nil config should yield nil notifier")
	}
	if FromConfig(&config.NotifierConfig{Enabled: false}, nil, nil) != nil {
		t.Fatal("disabled config should yield nil notifier")
	}
	// Enabled but no usable channels -> nil.
	if FromConfig(&config.NotifierConfig{Enabled: true}, nil, nil) != nil {
		t.Fatal("no channels should yield nil notifier")
	}
}

func TestWebhookChannelPostsJSON(t *testing.T) {
	var gotBody []byte
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = readAll(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch := &WebhookChannel{
		baseChannel: baseChannel{name: "hook"},
		url:         srv.URL,
		header:      "Authorization: Bearer secret",
		client:      srv.Client(),
	}
	err := ch.Send(context.Background(), Event{Type: EventProviderDown, Title: "t", Message: "m", Fields: map[string]string{"host": "news"}})
	if err != nil {
		t.Fatalf("webhook send failed: %v", err)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("expected auth header forwarded, got %q", gotAuth)
	}
	if len(gotBody) == 0 || !contains(gotBody, "provider_down") {
		t.Fatalf("expected event type in body, got %s", gotBody)
	}
}

func TestWebhookChannelNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	ch := &WebhookChannel{baseChannel: baseChannel{name: "hook"}, url: srv.URL, client: srv.Client()}
	if err := ch.Send(context.Background(), Event{Type: EventProviderDown}); err == nil {
		t.Fatal("expected error on 500 response")
	}
}

func TestSendTestDeliversSyntheticEventAndIgnoresEnabledFlag(t *testing.T) {
	var hits int32
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotBody, _ = readAll(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	disabled := false
	// Even though the channel is marked disabled, a test send must still fire.
	cc := config.NotifierChannelConfig{Type: "webhook", URL: srv.URL, Name: "hook", Enabled: &disabled}
	if err := SendTest(context.Background(), cc, srv.Client()); err != nil {
		t.Fatalf("SendTest returned error: %v", err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected exactly one delivery, got %d", hits)
	}
	if !contains(gotBody, "test") {
		t.Fatalf("expected synthetic test payload, got %s", gotBody)
	}
}

func TestSendTestRejectsInvalidChannel(t *testing.T) {
	if err := SendTest(context.Background(), config.NotifierChannelConfig{Type: "webhook", URL: ""}, nil); err == nil {
		t.Fatal("expected error for empty URL")
	}
	if err := SendTest(context.Background(), config.NotifierChannelConfig{Type: "bogus", URL: "http://x"}, nil); err == nil {
		t.Fatal("expected error for unknown channel type")
	}
}

// small helpers to avoid extra imports in assertions
func readAll(r *http.Request) ([]byte, error) {
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 256)
	for {
		nr, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:nr]...)
		if err != nil {
			return buf, nil
		}
	}
}

func contains(b []byte, sub string) bool {
	return len(b) >= len(sub) && indexOf(string(b), sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

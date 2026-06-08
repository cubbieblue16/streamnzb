// Package notifier delivers proactive alerts about addon health events
// (provider auth failures, exhausted indexers, playback failures, etc.) to
// user-configured channels. The user chooses which event types fire and which
// channels receive each type.
//
// Design notes:
//   - Emit is non-blocking. Event sites call it on hot paths (playback, NNTP
//     auth) and must never block on a slow or unreachable webhook, so Emit drops
//     events when the queue is full rather than applying backpressure.
//   - A single worker goroutine drains the queue and fans each event out to the
//     channels that want it, with per-(channel,event,title) rate limiting to
//     prevent alert storms when a fault repeats rapidly.
//   - A package-level singleton lets deep event sites emit without threading a
//     dependency through every call. Emit is a no-op when no notifier is set.
package notifier

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"streamnzb/pkg/core/logger"
)

// EventType identifies a class of alertable event.
type EventType string

const (
	EventProviderAuthFail  EventType = "provider_auth_fail"
	EventProviderDown      EventType = "provider_down"
	EventIndexerQuota      EventType = "indexer_quota"
	EventPlaybackFailure   EventType = "playback_failure"
	EventFailoverExhausted EventType = "failover_exhausted"
)

// AllEventTypes returns every event type the addon can emit, for building the
// settings UI and validating config.
func AllEventTypes() []EventType {
	return []EventType{
		EventProviderAuthFail,
		EventProviderDown,
		EventIndexerQuota,
		EventPlaybackFailure,
		EventFailoverExhausted,
	}
}

// IsValidEventType reports whether s names a known event type.
func IsValidEventType(s string) bool {
	for _, e := range AllEventTypes() {
		if string(e) == s {
			return true
		}
	}
	return false
}

// Event is a single alert. Fields carries structured context (indexer name,
// provider host, stream id, error text) rendered per channel.
type Event struct {
	Type    EventType
	Title   string
	Message string
	Fields  map[string]string
	Time    time.Time
}

// Channel delivers events to one destination (webhook, ntfy, Discord, ...).
type Channel interface {
	Name() string
	Wants(EventType) bool
	Send(ctx context.Context, ev Event) error
}

const (
	defaultQueueSize   = 256
	defaultSendTimeout = 10 * time.Second
)

// Notifier owns the queue, worker, channels, and rate-limit state.
type Notifier struct {
	enabled     bool
	events      map[EventType]bool
	minInterval time.Duration
	channels    []Channel

	queue   chan Event
	started bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	mu       sync.Mutex
	lastSent map[string]time.Time

	now         func() time.Time
	sendTimeout time.Duration
}

// Options configures a Notifier independently of any config types so the core
// stays reusable and unit-testable.
type Options struct {
	Enabled bool
	// Events is the master per-type toggle. A type absent or false is suppressed
	// for every channel.
	Events map[EventType]bool
	// MinInterval is the per-(channel,type,title) dedupe window. Zero disables
	// rate limiting.
	MinInterval time.Duration
	Channels    []Channel
	// Now and SendTimeout are injectable for tests; zero values use defaults.
	Now         func() time.Time
	SendTimeout time.Duration
}

// New builds a Notifier. It does not start the worker; call Start.
func New(opts Options) *Notifier {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	timeout := opts.SendTimeout
	if timeout <= 0 {
		timeout = defaultSendTimeout
	}
	events := opts.Events
	if events == nil {
		events = map[EventType]bool{}
	}
	return &Notifier{
		enabled:     opts.Enabled,
		events:      events,
		minInterval: opts.MinInterval,
		channels:    opts.Channels,
		queue:       make(chan Event, defaultQueueSize),
		lastSent:    map[string]time.Time{},
		now:         now,
		sendTimeout: timeout,
	}
}

// Enabled reports whether the notifier will deliver anything.
func (n *Notifier) Enabled() bool {
	return n != nil && n.enabled && len(n.channels) > 0
}

// Start launches the worker goroutine. Safe to call once.
func (n *Notifier) Start() {
	if n == nil || n.started {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.cancel = cancel
	n.started = true
	n.wg.Add(1)
	go n.run(ctx)
}

// Stop drains in-flight work and stops the worker. Safe to call multiple times.
func (n *Notifier) Stop() {
	if n == nil || !n.started {
		return
	}
	n.started = false
	close(n.queue)
	n.wg.Wait()
	if n.cancel != nil {
		n.cancel()
	}
}

// Emit queues an event for delivery. Non-blocking: drops (with a debug log) if
// the queue is full or the notifier is disabled. Stamps Time if unset.
func (n *Notifier) Emit(ev Event) {
	if !n.Enabled() {
		return
	}
	if !n.events[ev.Type] {
		return
	}
	if ev.Time.IsZero() {
		ev.Time = n.now()
	}
	select {
	case n.queue <- ev:
	default:
		logger.Debug("Notifier queue full, dropping event", "type", string(ev.Type), "title", ev.Title)
	}
}

func (n *Notifier) run(ctx context.Context) {
	defer n.wg.Done()
	for ev := range n.queue {
		n.dispatch(ctx, ev)
	}
}

func (n *Notifier) dispatch(ctx context.Context, ev Event) {
	for _, ch := range n.channels {
		if !ch.Wants(ev.Type) {
			continue
		}
		if n.rateLimited(ch.Name(), ev) {
			logger.Debug("Notifier rate-limited event", "channel", ch.Name(), "type", string(ev.Type), "title", ev.Title)
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, n.sendTimeout)
		err := ch.Send(sendCtx, ev)
		cancel()
		if err != nil {
			logger.Warn("Notifier channel send failed", "channel", ch.Name(), "type", string(ev.Type), "err", err)
		}
	}
}

// rateLimited reports whether an identical (channel,type,title) event fired
// within minInterval, and records this send when it is allowed.
func (n *Notifier) rateLimited(channel string, ev Event) bool {
	if n.minInterval <= 0 {
		return false
	}
	key := channel + "\x00" + string(ev.Type) + "\x00" + ev.Title
	now := n.now()
	n.mu.Lock()
	defer n.mu.Unlock()
	if last, ok := n.lastSent[key]; ok && now.Sub(last) < n.minInterval {
		return true
	}
	n.lastSent[key] = now
	return false
}

// TestChannel delivers a synthetic event to one named channel, bypassing the
// queue, master toggles, and rate limiting, so the settings UI can verify a
// channel's configuration. Returns an error if no such channel exists.
func (n *Notifier) TestChannel(ctx context.Context, name string, ev Event) error {
	var target Channel
	for _, ch := range n.channels {
		if ch.Name() == name {
			target = ch
			break
		}
	}
	if target == nil {
		return errUnknownChannel(name)
	}
	if ev.Time.IsZero() {
		ev.Time = n.now()
	}
	sendCtx, cancel := context.WithTimeout(ctx, n.sendTimeout)
	defer cancel()
	return target.Send(sendCtx, ev)
}

// ChannelNames returns the configured channel names in stable order.
func (n *Notifier) ChannelNames() []string {
	if n == nil {
		return nil
	}
	names := make([]string, 0, len(n.channels))
	for _, ch := range n.channels {
		names = append(names, ch.Name())
	}
	sort.Strings(names)
	return names
}

// sharedHTTPClient is used by HTTP-based channels unless overridden.
func sharedHTTPClient() *http.Client {
	return &http.Client{Timeout: defaultSendTimeout}
}

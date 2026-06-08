package notifier

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"streamnzb/pkg/core/config"
	"streamnzb/pkg/core/logger"
)

// SendTest builds a single channel from cc and synchronously delivers a synthetic
// alert to it, returning any delivery error. The settings UI uses this so a user
// can validate a channel's URL/headers before saving. The channel's Enabled flag
// is ignored (a user typically tests a channel they have not yet turned on), but
// an empty URL or unknown type still yields an error. Pass nil client for the
// default.
func SendTest(ctx context.Context, cc config.NotifierChannelConfig, client *http.Client) error {
	if client == nil {
		client = sharedHTTPClient()
	}
	cc.Enabled = nil // never gate a test on the saved enabled flag
	ch := buildChannel(cc, 0, client)
	if ch == nil {
		return errors.New("invalid or incomplete channel configuration (check type and URL)")
	}
	return ch.Send(ctx, Event{
		Type:    EventProviderDown,
		Title:   "StreamNZB test alert",
		Message: "This is a test notification from StreamNZB. If you can see this, the channel is configured correctly.",
		Fields: map[string]string{
			"source": "test",
		},
	})
}

// FromConfig builds a started-ready Notifier from config. Returns nil when
// alerting is disabled or no usable channels are configured, in which case the
// global Emit becomes a no-op. The caller is responsible for Start/SetGlobal.
//
// now and client are injectable for tests; pass nil to use defaults.
func FromConfig(cfg *config.NotifierConfig, now func() time.Time, client *http.Client) *Notifier {
	if cfg == nil || !cfg.Enabled {
		return nil
	}
	if client == nil {
		client = sharedHTTPClient()
	}

	events := map[EventType]bool{}
	for k, v := range cfg.Events {
		if v && IsValidEventType(k) {
			events[EventType(k)] = true
		}
	}

	channels := make([]Channel, 0, len(cfg.Channels))
	for i := range cfg.Channels {
		ch := buildChannel(cfg.Channels[i], i, client)
		if ch != nil {
			channels = append(channels, ch)
		}
	}
	if len(channels) == 0 {
		logger.Warn("Notifier enabled but no usable channels configured; alerts disabled")
		return nil
	}

	return New(Options{
		Enabled:     true,
		Events:      events,
		MinInterval: time.Duration(cfg.MinIntervalSeconds) * time.Second,
		Channels:    channels,
		Now:         now,
	})
}

func buildChannel(cc config.NotifierChannelConfig, index int, client *http.Client) Channel {
	if cc.Enabled != nil && !*cc.Enabled {
		return nil
	}
	url := strings.TrimSpace(cc.URL)
	if url == "" {
		logger.Warn("Notifier channel skipped: empty URL", "type", cc.Type, "name", cc.Name)
		return nil
	}
	base := baseChannel{
		name:  channelName(cc, index),
		wants: wantedEvents(cc.Events),
	}
	switch strings.ToLower(strings.TrimSpace(cc.Type)) {
	case "webhook":
		return &WebhookChannel{baseChannel: base, url: url, header: cc.Header, client: client}
	case "ntfy":
		return &NtfyChannel{baseChannel: base, url: url, priority: strings.TrimSpace(cc.Priority), client: client}
	case "discord":
		return &DiscordChannel{baseChannel: base, url: url, client: client}
	default:
		logger.Warn("Notifier channel skipped: unknown type", "type", cc.Type, "name", cc.Name)
		return nil
	}
}

func channelName(cc config.NotifierChannelConfig, index int) string {
	if n := strings.TrimSpace(cc.Name); n != "" {
		return n
	}
	t := strings.TrimSpace(cc.Type)
	if t == "" {
		t = "channel"
	}
	return t + "-" + strconv.Itoa(index+1)
}

func wantedEvents(events []string) map[EventType]bool {
	if len(events) == 0 {
		return nil
	}
	out := map[EventType]bool{}
	for _, e := range events {
		if IsValidEventType(e) {
			out[EventType(e)] = true
		}
	}
	return out
}

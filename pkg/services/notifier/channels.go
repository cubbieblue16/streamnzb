package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// baseChannel holds the common name + per-event subscription. An empty wants set
// means "every master-enabled event" so users don't have to enumerate types.
type baseChannel struct {
	name  string
	wants map[EventType]bool
}

func (b baseChannel) Name() string { return b.name }

func (b baseChannel) Wants(t EventType) bool {
	if len(b.wants) == 0 {
		return true
	}
	return b.wants[t]
}

// WebhookChannel POSTs a generic JSON document to an arbitrary URL.
type WebhookChannel struct {
	baseChannel
	url    string
	header string // optional single header line, e.g. "Authorization: Bearer xyz"
	client *http.Client
}

func (w *WebhookChannel) Send(ctx context.Context, ev Event) error {
	payload := map[string]any{
		"type":    string(ev.Type),
		"title":   ev.Title,
		"message": ev.Message,
		"fields":  ev.Fields,
		"time":    ev.Time.UTC().Format(time.RFC3339),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if k, v, ok := parseHeaderLine(w.header); ok {
		req.Header.Set(k, v)
	}
	return doAndDiscard(w.client, req)
}

// NtfyChannel publishes to an ntfy topic URL (https://ntfy.sh/<topic>).
type NtfyChannel struct {
	baseChannel
	url      string
	priority string
	client   *http.Client
}

func (n *NtfyChannel) Send(ctx context.Context, ev Event) error {
	msg := ev.Message
	if extra := renderFields(ev.Fields); extra != "" {
		msg = strings.TrimSpace(msg + "\n" + extra)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, strings.NewReader(msg))
	if err != nil {
		return err
	}
	if ev.Title != "" {
		req.Header.Set("Title", ev.Title)
	}
	priority := n.priority
	if priority == "" {
		priority = ntfyPriority(ev.Type)
	}
	if priority != "" {
		req.Header.Set("Priority", priority)
	}
	req.Header.Set("Tags", "warning")
	return doAndDiscard(n.client, req)
}

// DiscordChannel posts a rich embed to a Discord webhook URL.
type DiscordChannel struct {
	baseChannel
	url    string
	client *http.Client
}

func (d *DiscordChannel) Send(ctx context.Context, ev Event) error {
	embed := map[string]any{
		"title":       ev.Title,
		"description": ev.Message,
		"color":       discordColor(ev.Type),
		"timestamp":   ev.Time.UTC().Format(time.RFC3339),
	}
	if fields := discordFields(ev.Fields); len(fields) > 0 {
		embed["fields"] = fields
	}
	payload := map[string]any{"embeds": []any{embed}}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return doAndDiscard(d.client, req)
}

// --- helpers ---

func doAndDiscard(client *http.Client, req *http.Request) error {
	if client == nil {
		client = sharedHTTPClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notifier: %s returned status %d", req.URL.Host, resp.StatusCode)
	}
	return nil
}

func parseHeaderLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}
	idx := strings.IndexByte(line, ':')
	if idx <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])
	if key == "" {
		return "", "", false
	}
	return key, value, true
}

func renderFields(fields map[string]string) string {
	if len(fields) == 0 {
		return ""
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+": "+fields[k])
	}
	return strings.Join(lines, "\n")
}

func discordFields(fields map[string]string) []any {
	if len(fields) == 0 {
		return nil
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]any{"name": k, "value": fields[k], "inline": true})
	}
	return out
}

func ntfyPriority(t EventType) string {
	switch t {
	case EventProviderAuthFail, EventProviderDown, EventFailoverExhausted:
		return "high"
	case EventIndexerQuota, EventPlaybackFailure:
		return "default"
	default:
		return "default"
	}
}

func discordColor(t EventType) int {
	switch t {
	case EventProviderAuthFail, EventProviderDown:
		return 0xE01E5A // red
	case EventFailoverExhausted, EventPlaybackFailure:
		return 0xECB22E // amber
	case EventIndexerQuota:
		return 0x4A9EFF // blue
	default:
		return 0x9AA0A6 // grey
	}
}

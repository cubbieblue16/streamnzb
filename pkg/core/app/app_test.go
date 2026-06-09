package app

import (
	"testing"

	"streamnzb/pkg/core/config"
	"streamnzb/pkg/services/availnzb"
	"streamnzb/pkg/services/notifier"
)

func TestSetAvailNZBAPIKeyUpdatesOptsAndLiveClient(t *testing.T) {
	t.Parallel()

	client := availnzb.NewClient("https://snzb.stream", "")
	a := &App{
		components: &Components{AvailClient: client},
	}

	a.SetAvailNZBAPIKey(" updated-key ")

	if got := client.GetAPIKey(); got != "updated-key" {
		t.Fatalf("client key = %q, want %q", got, "updated-key")
	}
	if got := a.opts.AvailNZBAPIKey; got != "updated-key" {
		t.Fatalf("stored opts key = %q, want %q", got, "updated-key")
	}
}

// notifierTestConfig returns a minimal config whose only interesting content is
// the notifier block. Indexers/providers/proxy are zero-valued on both old and
// new configs so ConfigChanged classifies the reload as ReloadConfigOnly.
func notifierTestConfig(channelName string, playbackFailure bool) *config.Config {
	return &config.Config{
		Notifier: &config.NotifierConfig{
			Enabled: true,
			Events: map[string]bool{
				"playback_failure": playbackFailure,
				"provider_down":    true,
			},
			Channels: []config.NotifierChannelConfig{
				{Name: channelName, Type: "ntfy", URL: "https://ntfy.sh/streamnzb-test-" + channelName},
			},
		},
	}
}

// A config-only reload (no indexer/provider/proxy change) must rebuild the
// global notifier so saved alert settings take effect without a restart.
// Regression test for notification toggles being write-only until restart.
func TestReloadConfigOnlyRebuildsNotifier(t *testing.T) {
	// Not parallel: mutates the process-wide notifier singleton.
	oldCfg := notifierTestConfig("old", true)
	prev := notifier.FromConfig(oldCfg.Notifier, nil, nil)
	if prev == nil {
		t.Fatal("test setup: FromConfig returned nil for enabled config")
	}
	notifier.SetGlobal(prev)
	defer notifier.SetGlobal(nil)

	a := &App{components: &Components{Config: oldCfg}}

	newCfg := notifierTestConfig("new", false)
	comp, full, err := a.Reload(newCfg)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if full {
		t.Fatal("expected config-only reload, got full rebuild")
	}
	if comp.Config != newCfg {
		t.Fatal("reloaded components do not carry the new config")
	}

	g := notifier.Global()
	if g == nil {
		t.Fatal("global notifier is nil after config-only reload with notifier enabled")
	}
	if g == prev {
		t.Fatal("global notifier was not rebuilt on config-only reload; stale event/channel settings stay live until restart")
	}
	names := g.ChannelNames()
	if len(names) != 1 || names[0] != "new" {
		t.Fatalf("rebuilt notifier channels = %v, want [new]", names)
	}
}

// Disabling the notifier via a config-only save must clear the global so Emit
// becomes a no-op immediately.
func TestReloadConfigOnlyDisablesNotifier(t *testing.T) {
	oldCfg := notifierTestConfig("old", true)
	prev := notifier.FromConfig(oldCfg.Notifier, nil, nil)
	notifier.SetGlobal(prev)
	defer notifier.SetGlobal(nil)

	a := &App{components: &Components{Config: oldCfg}}

	newCfg := notifierTestConfig("old", true)
	newCfg.Notifier.Enabled = false
	if _, full, err := a.Reload(newCfg); err != nil || full {
		t.Fatalf("Reload: full=%v err=%v, want config-only and nil", full, err)
	}

	if g := notifier.Global(); g != nil {
		t.Fatal("global notifier still set after disabling via config-only reload")
	}
}

package config

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	coreenv "streamnzb/pkg/core/env"
)

func TestMergeIndexerSearchDefaultsSeriesSeasonAndCompleteSearchOn(t *testing.T) {
	merged := MergeIndexerSearch(&IndexerConfig{}, nil, &Config{})
	if merged.EnableSeriesSeasonSearch == nil || !*merged.EnableSeriesSeasonSearch {
		t.Fatalf("expected EnableSeriesSeasonSearch default true, got %#v", merged.EnableSeriesSeasonSearch)
	}
	if merged.EnableSeriesCompleteSearch == nil || !*merged.EnableSeriesCompleteSearch {
		t.Fatalf("expected EnableSeriesCompleteSearch default true, got %#v", merged.EnableSeriesCompleteSearch)
	}
}

func TestMergeIndexerSearchLegacySeriesPackSearchAppliesToSeasonAndComplete(t *testing.T) {
	merged := MergeIndexerSearch(
		&IndexerConfig{EnableSeriesPackSearch: ptrBool(false)},
		nil,
		&Config{},
	)
	if merged.EnableSeriesSeasonSearch == nil || *merged.EnableSeriesSeasonSearch {
		t.Fatalf("expected legacy pack search setting to disable season search, got %#v", merged.EnableSeriesSeasonSearch)
	}
	if merged.EnableSeriesCompleteSearch == nil || *merged.EnableSeriesCompleteSearch {
		t.Fatalf("expected legacy pack search setting to disable complete search, got %#v", merged.EnableSeriesCompleteSearch)
	}
}

func TestMergeIndexerSearchExplicitSeriesSearchOverridesWin(t *testing.T) {
	merged := MergeIndexerSearch(
		&IndexerConfig{
			EnableSeriesPackSearch:     ptrBool(false),
			EnableSeriesSeasonSearch:   ptrBool(true),
			EnableSeriesCompleteSearch: ptrBool(false),
		},
		&IndexerSearchConfig{
			EnableSeriesSeasonSearch:   ptrBool(false),
			EnableSeriesCompleteSearch: ptrBool(true),
		},
		&Config{},
	)
	if merged.EnableSeriesSeasonSearch == nil || *merged.EnableSeriesSeasonSearch {
		t.Fatalf("expected explicit season override to win, got %#v", merged.EnableSeriesSeasonSearch)
	}
	if merged.EnableSeriesCompleteSearch == nil || !*merged.EnableSeriesCompleteSearch {
		t.Fatalf("expected explicit complete override to win, got %#v", merged.EnableSeriesCompleteSearch)
	}
}

func TestNormalizeSeriesSearchScopeDefaultsToSeasonEpisode(t *testing.T) {
	if got := NormalizeSeriesSearchScope(""); got != SeriesSearchScopeSeasonEpisode {
		t.Fatalf("NormalizeSeriesSearchScope() = %q, want %q", got, SeriesSearchScopeSeasonEpisode)
	}
}

func TestSeriesSearchScopeRequiresValidation(t *testing.T) {
	if !SeriesSearchScopeRequiresValidation(SeriesSearchScopeSeason) {
		t.Fatalf("expected season scope to require validation")
	}
	if !SeriesSearchScopeRequiresValidation(SeriesSearchScopeNone) {
		t.Fatalf("expected none scope to require validation")
	}
	if SeriesSearchScopeRequiresValidation(SeriesSearchScopeSeasonEpisode) {
		t.Fatalf("did not expect season_episode scope to require validation")
	}
}

func TestDefaultSearchQuerySettingsMatchExpectedModes(t *testing.T) {
	cfg := &Config{}
	if !cfg.applyStreamModelUpgradeDefaults() {
		t.Fatalf("expected defaults to be applied")
	}

	if cfg.MovieSearchQueries[0].SearchResultLimit != 0 {
		t.Fatalf("expected DefaultMovieText limit max/0, got %d", cfg.MovieSearchQueries[0].SearchResultLimit)
	}
	if cfg.MovieSearchQueries[0].IncludeYear == nil || !*cfg.MovieSearchQueries[0].IncludeYear {
		t.Fatalf("expected DefaultMovieText year enabled")
	}
	if cfg.MovieSearchQueries[1].SearchResultLimit != 0 {
		t.Fatalf("expected DefaultMovieID limit max/0, got %d", cfg.MovieSearchQueries[1].SearchResultLimit)
	}
	if cfg.MovieSearchQueries[1].IncludeYear == nil || *cfg.MovieSearchQueries[1].IncludeYear {
		t.Fatalf("expected DefaultMovieID year disabled")
	}
	if got := cfg.MovieSearchQueries[1].SearchTitleLanguages; !strings.EqualFold(strings.Join(got, "|"), strings.Join([]string{"en-US", ""}, "|")) {
		t.Fatalf("expected DefaultMovieID title languages [en-US original], got %#v", got)
	}
	if cfg.SeriesSearchQueries[0].SearchResultLimit != 0 {
		t.Fatalf("expected DefaultTVText limit max/0, got %d", cfg.SeriesSearchQueries[0].SearchResultLimit)
	}
	if cfg.SeriesSearchQueries[0].IncludeYear == nil || !*cfg.SeriesSearchQueries[0].IncludeYear {
		t.Fatalf("expected DefaultTVText year enabled")
	}
	if cfg.SeriesSearchQueries[1].SearchResultLimit != 0 {
		t.Fatalf("expected DefaultTVID limit max/0, got %d", cfg.SeriesSearchQueries[1].SearchResultLimit)
	}
	if cfg.SeriesSearchQueries[1].IncludeYear == nil || *cfg.SeriesSearchQueries[1].IncludeYear {
		t.Fatalf("expected DefaultTVID year disabled")
	}
	if got := cfg.SeriesSearchQueries[1].SearchTitleLanguages; !strings.EqualFold(strings.Join(got, "|"), strings.Join([]string{"en-US", ""}, "|")) {
		t.Fatalf("expected DefaultTVID title languages [en-US original], got %#v", got)
	}
}

func TestBackfillLegacySearchQuerySettings(t *testing.T) {
	cfg := &Config{
		MovieSearchQueries: []SearchQueryConfig{
			{Name: "DefaultMovieText", SearchMode: "text"},
			{Name: "DefaultMovieID", SearchMode: "id", LegacyIncludeYearInTextSearch: ptrBool(true), SearchTitleLanguage: "original"},
		},
		SeriesSearchQueries: []SearchQueryConfig{
			{Name: "DefaultTVText", SearchMode: "text", UseSeasonEpisodeParams: ptrBool(false)},
			{Name: "DefaultTVID", SearchMode: "id", LegacyIncludeYearInTextSearch: ptrBool(false)},
		},
	}

	if !cfg.backfillLegacySearchQuerySettings() {
		t.Fatal("expected legacy search query settings to be backfilled")
	}

	if cfg.MovieSearchQueries[0].IncludeYear == nil || !*cfg.MovieSearchQueries[0].IncludeYear {
		t.Fatal("expected DefaultMovieText year enabled after backfill")
	}
	if cfg.MovieSearchQueries[1].IncludeYear == nil || !*cfg.MovieSearchQueries[1].IncludeYear {
		t.Fatal("expected DefaultMovieID year enabled after backfill from legacy year field")
	}
	if got := cfg.MovieSearchQueries[1].SearchTitleLanguages; !reflect.DeepEqual(got, []string{"en-US", ""}) {
		t.Fatalf("expected DefaultMovieID title languages [en-US original] after backfill, got %#v", got)
	}
	if cfg.SeriesSearchQueries[0].IncludeYear == nil || !*cfg.SeriesSearchQueries[0].IncludeYear {
		t.Fatal("expected DefaultTVText year enabled after backfill")
	}
	if cfg.SeriesSearchQueries[0].SeriesSearchScope != SeriesSearchScopeSeasonEpisode {
		t.Fatalf("expected DefaultTVText scope %q after legacy backfill, got %q", SeriesSearchScopeSeasonEpisode, cfg.SeriesSearchQueries[0].SeriesSearchScope)
	}
	if cfg.SeriesSearchQueries[1].IncludeYear == nil || *cfg.SeriesSearchQueries[1].IncludeYear {
		t.Fatal("expected DefaultTVID year disabled after backfill")
	}
	if got := cfg.SeriesSearchQueries[1].SearchTitleLanguages; !reflect.DeepEqual(got, []string{"en-US", ""}) {
		t.Fatalf("expected DefaultTVID title languages [en-US original] after backfill, got %#v", got)
	}
}

func TestIndexerConfigEffectiveTimeoutDefaults(t *testing.T) {
	tests := []struct {
		name string
		cfg  IndexerConfig
		want int
	}{
		{name: "default newznab", cfg: IndexerConfig{}, want: DefaultInternalIndexerTimeoutSeconds},
		{name: "aggregator", cfg: IndexerConfig{Type: "aggregator"}, want: DefaultAggregatorIndexerTimeoutSeconds},
		{name: "nzbhydra", cfg: IndexerConfig{Type: "nzbhydra"}, want: DefaultAggregatorIndexerTimeoutSeconds},
		{name: "prowlarr", cfg: IndexerConfig{Type: "prowlarr"}, want: DefaultAggregatorIndexerTimeoutSeconds},
		{name: "easynews", cfg: IndexerConfig{Type: "easynews"}, want: DefaultEasynewsIndexerTimeoutSeconds},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.EffectiveTimeoutSeconds(); got != tt.want {
				t.Fatalf("EffectiveTimeoutSeconds() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestIndexerConfigEffectiveTimeoutHonorsExplicitOverride(t *testing.T) {
	cfg := IndexerConfig{Type: "aggregator", TimeoutSeconds: 7}

	if got := cfg.EffectiveTimeoutSeconds(); got != 7 {
		t.Fatalf("EffectiveTimeoutSeconds() = %d, want 7", got)
	}
	if got := cfg.EffectiveTimeout(); got != 7*time.Second {
		t.Fatalf("EffectiveTimeout() = %v, want %v", got, 7*time.Second)
	}
}

func TestValidateIndexerProxyURL(t *testing.T) {
	if err := ValidateIndexerProxyURL(""); err != nil {
		t.Fatal(err)
	}
	if err := ValidateIndexerProxyURL("http://proxy:8888"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateIndexerProxyURL("socks5://127.0.0.1:1080"); err == nil {
		t.Fatal("expected error for socks5 scheme")
	}
	if err := ValidateIndexerProxyURL("http://"); err == nil {
		t.Fatal("expected error for missing host")
	}
}

func TestValidateIndexerProxyReachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	if err := ValidateIndexerProxyReachable("http://" + ln.Addr().String()); err != nil {
		t.Fatalf("expected reachable proxy, got err: %v", err)
	}
}

func TestRedactProxyURLForAPI(t *testing.T) {
	got := RedactProxyURLForAPI("http://user:secret@proxy:8888")
	want := "http://proxy:8888"
	if got != want {
		t.Fatalf("RedactProxyURLForAPI = %q, want %q", got, want)
	}
}

func TestMigrateLegacyIndexersBackfillsEasynewsTimeout(t *testing.T) {
	cfg := &Config{
		Indexers: []IndexerConfig{
			{Name: "Easynews", Type: "easynews"},
			{Name: "SceneNZBs", Type: "newznab"},
		},
	}

	if !cfg.MigrateLegacyIndexers() {
		t.Fatalf("expected legacy indexers to be migrated")
	}
	if cfg.Indexers[0].TimeoutSeconds != DefaultEasynewsIndexerTimeoutSeconds {
		t.Fatalf("Easynews timeout = %d, want %d", cfg.Indexers[0].TimeoutSeconds, DefaultEasynewsIndexerTimeoutSeconds)
	}
	if cfg.Indexers[1].TimeoutSeconds != 0 {
		t.Fatalf("non-Easynews timeout = %d, want 0", cfg.Indexers[1].TimeoutSeconds)
	}
}

func TestMigrateLegacyIndexersKeepsExplicitEasynewsTimeout(t *testing.T) {
	cfg := &Config{
		Indexers: []IndexerConfig{
			{Name: "Easynews", Type: "easynews", TimeoutSeconds: DefaultInternalIndexerTimeoutSeconds, Enabled: ptrBool(true)},
		},
	}

	if cfg.MigrateLegacyIndexers() {
		t.Fatalf("did not expect explicit Easynews timeout to be migrated")
	}
	if cfg.Indexers[0].TimeoutSeconds != DefaultInternalIndexerTimeoutSeconds {
		t.Fatalf("Easynews timeout = %d, want %d", cfg.Indexers[0].TimeoutSeconds, DefaultInternalIndexerTimeoutSeconds)
	}
}

func TestConfigEffectivePlaybackStartupTimeoutDefaults(t *testing.T) {
	cfg := &Config{}

	if got := cfg.EffectivePlaybackStartupTimeoutSeconds(); got != DefaultPlaybackStartupTimeoutSeconds {
		t.Fatalf("EffectivePlaybackStartupTimeoutSeconds() = %d, want %d", got, DefaultPlaybackStartupTimeoutSeconds)
	}
	if got := cfg.EffectivePlaybackStartupTimeout(); got != time.Duration(DefaultPlaybackStartupTimeoutSeconds)*time.Second {
		t.Fatalf("EffectivePlaybackStartupTimeout() = %v", got)
	}
}

func TestConfigEffectivePlaybackStartupTimeoutHonorsExplicitOverride(t *testing.T) {
	cfg := &Config{PlaybackStartupTimeoutSeconds: 12}

	if got := cfg.EffectivePlaybackStartupTimeoutSeconds(); got != 12 {
		t.Fatalf("EffectivePlaybackStartupTimeoutSeconds() = %d, want 12", got)
	}
	if got := cfg.EffectivePlaybackStartupTimeout(); got != 12*time.Second {
		t.Fatalf("EffectivePlaybackStartupTimeout() = %v, want %v", got, 12*time.Second)
	}
}

func TestConfigEffectivePlaybackStartupTimeoutRejectsOutOfRangeValues(t *testing.T) {
	cfg := &Config{PlaybackStartupTimeoutSeconds: 61}

	if got := cfg.EffectivePlaybackStartupTimeoutSeconds(); got != DefaultPlaybackStartupTimeoutSeconds {
		t.Fatalf("EffectivePlaybackStartupTimeoutSeconds() = %d, want %d", got, DefaultPlaybackStartupTimeoutSeconds)
	}
}

func TestConfigEffectiveFailoverFastModeDefaultsEnabled(t *testing.T) {
	var cfg *Config
	if !cfg.EffectiveFailoverFastMode() {
		t.Fatalf("EffectiveFailoverFastMode() = false, want true")
	}
}

func TestConfigEffectiveFailoverFastModeHonorsEnabledValue(t *testing.T) {
	cfg := &Config{FailoverFastMode: true}
	if !cfg.EffectiveFailoverFastMode() {
		t.Fatalf("EffectiveFailoverFastMode() = false, want true")
	}
}

func TestConfigEffectiveFailoverFastModeHonorsDisabledValue(t *testing.T) {
	cfg := &Config{FailoverFastMode: false}
	if cfg.EffectiveFailoverFastMode() {
		t.Fatalf("EffectiveFailoverFastMode() = true, want false")
	}
}

func TestApplyEnvOverridesForcesAdminPasswordResetPrompt(t *testing.T) {
	t.Setenv(coreenv.AdminForcePasswordResetEnv, "true")
	o, keys := coreenv.ReadConfigOverrides()
	cfg := &Config{}

	ApplyEnvOverrides(cfg, o, keys)

	if !cfg.AdminMustChangePassword {
		t.Fatalf("AdminMustChangePassword = false, want true")
	}
}

func TestConfigEffectiveAvailNZBFilterReportedBadDefaultsDisabled(t *testing.T) {
	cfg := &Config{}
	if cfg.EffectiveAvailNZBFilterReportedBad() {
		t.Fatalf("EffectiveAvailNZBFilterReportedBad() = true, want false")
	}
}

func TestConfigEffectiveAvailNZBFilterReportedBadHonorsExplicitValue(t *testing.T) {
	cfg := &Config{AvailNZBFilterReportedBad: ptrBool(true)}
	if !cfg.EffectiveAvailNZBFilterReportedBad() {
		t.Fatalf("EffectiveAvailNZBFilterReportedBad() = false, want true")
	}
	cfg = &Config{AvailNZBFilterReportedBad: ptrBool(false)}
	if cfg.EffectiveAvailNZBFilterReportedBad() {
		t.Fatalf("EffectiveAvailNZBFilterReportedBad() = true, want false")
	}
}

func TestConfigEffectiveAvailNZBFilterReportedBadDisabledWhenAvailNZBModeOff(t *testing.T) {
	cfg := &Config{
		AvailNZBMode:              "off",
		AvailNZBFilterReportedBad: ptrBool(true),
	}
	if cfg.EffectiveAvailNZBFilterReportedBad() {
		t.Fatalf("EffectiveAvailNZBFilterReportedBad() = true, want false when mode is off")
	}
}

func TestApplyStreamModelUpgradeDefaultsCreatesQueriesAndDefaultStream(t *testing.T) {
	cfg := &Config{
		Providers: []Provider{
			{Host: "news.newshosting.com"},
			{Name: "eweka", Host: "news.eweka.nl"},
		},
		Indexers: []IndexerConfig{
			{Name: "Indexer A"},
			{Name: "Indexer B"},
		},
	}

	if !cfg.ApplyProviderDefaults() {
		t.Fatalf("expected provider defaults to derive provider names")
	}

	if !cfg.applyStreamModelUpgradeDefaults() {
		t.Fatalf("expected stream model upgrade defaults to change config")
	}

	if len(cfg.MovieSearchQueries) != 2 {
		t.Fatalf("expected 2 movie queries, got %d", len(cfg.MovieSearchQueries))
	}
	if len(cfg.SeriesSearchQueries) != 2 {
		t.Fatalf("expected 2 series queries, got %d", len(cfg.SeriesSearchQueries))
	}
	if got := NormalizeSeriesSearchScope(cfg.SeriesSearchQueries[0].SeriesSearchScope); got != SeriesSearchScopeSeasonEpisode {
		t.Fatalf("expected DefaultTVText scope season_episode, got %q", got)
	}
	if cfg.SeriesSearchQueries[0].IncludeYear == nil || !*cfg.SeriesSearchQueries[0].IncludeYear {
		t.Fatalf("expected DefaultTVText year enabled")
	}
	if got := NormalizeSeriesSearchScope(cfg.SeriesSearchQueries[1].SeriesSearchScope); got != SeriesSearchScopeSeasonEpisode {
		t.Fatalf("expected DefaultTVID scope season_episode, got %q", got)
	}
	if cfg.SeriesSearchQueries[1].IncludeYear == nil || *cfg.SeriesSearchQueries[1].IncludeYear {
		t.Fatalf("expected DefaultTVID year disabled")
	}

	stream := cfg.Streams[defaultMigratedStreamID]
	if stream == nil {
		t.Fatalf("expected migrated default stream to be created")
	}
	if stream.Token == "" {
		t.Fatalf("expected migrated default stream token to be populated")
	}
	if stream.IndexerMode != "combine" {
		t.Fatalf("expected default stream indexer mode combine, got %q", stream.IndexerMode)
	}
	if stream.FilterSortingMode != "aiostreams" {
		t.Fatalf("expected default stream filter sorting mode aiostreams, got %q", stream.FilterSortingMode)
	}
	if stream.ResultsMode != "display_all" {
		t.Fatalf("expected default stream results mode display_all, got %q", stream.ResultsMode)
	}
	if stream.EnableFailover == nil || !*stream.EnableFailover {
		t.Fatalf("expected default stream failover enabled, got %#v", stream.EnableFailover)
	}
	if stream.AutoAddProviders == nil || !*stream.AutoAddProviders {
		t.Fatalf("expected default stream auto add providers enabled, got %#v", stream.AutoAddProviders)
	}
	if stream.AutoAddIndexers == nil || !*stream.AutoAddIndexers {
		t.Fatalf("expected default stream auto add indexers enabled, got %#v", stream.AutoAddIndexers)
	}
	if len(stream.ProviderSelections) != 2 || stream.ProviderSelections[0] != "newshosting" {
		t.Fatalf("unexpected provider selections: %#v", stream.ProviderSelections)
	}
	if len(stream.IndexerSelections) != 2 {
		t.Fatalf("unexpected indexer selections: %#v", stream.IndexerSelections)
	}
	if len(stream.MovieSearchQueries) != 2 || stream.MovieSearchQueries[0] != "DefaultMovieText" {
		t.Fatalf("unexpected movie search queries: %#v", stream.MovieSearchQueries)
	}
	if len(stream.SeriesSearchQueries) != 2 || stream.SeriesSearchQueries[0] != "DefaultTVText" {
		t.Fatalf("unexpected series search queries: %#v", stream.SeriesSearchQueries)
	}

	if cfg.applyStreamModelUpgradeDefaults() {
		t.Fatalf("expected second upgrade application to be a no-op")
	}
}

func TestLoadFilePreservesLoadedPath(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"addon_port":7001}`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg := &Config{}
	if err := cfg.LoadFile(configPath); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	if cfg.LoadedPath != configPath {
		t.Fatalf("LoadedPath = %q, want %q", cfg.LoadedPath, configPath)
	}
}

func TestSaveFileUpdatesLoadedPath(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	cfg := &Config{AddonPort: 7001}
	if err := cfg.SaveFile(configPath); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	if cfg.LoadedPath != configPath {
		t.Fatalf("LoadedPath = %q, want %q", cfg.LoadedPath, configPath)
	}
}

func TestSaveFileDoesNotPersistAvailNZBAPIKey(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	cfg := &Config{
		AddonPort:      7001,
		AvailNZBAPIKey: "secret-should-not-be-written",
	}
	if err := cfg.SaveFile(configPath); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(raw)
	if strings.Contains(content, "availnzb_api_key") {
		t.Fatalf("config.json should not contain availnzb_api_key, got: %s", content)
	}
	if strings.Contains(content, "secret-should-not-be-written") {
		t.Fatalf("config.json should not contain AvailNZBAPIKey value")
	}
}

func TestRedactForAPIHidesNotifierChannelSecrets(t *testing.T) {
	enabled := true
	cfg := &Config{
		Notifier: &NotifierConfig{
			Enabled: true,
			Events:  map[string]bool{"provider_down": true},
			Channels: []NotifierChannelConfig{
				{
					Name:    "discord",
					Type:    "discord",
					URL:     "https://discord.com/api/webhooks/secret-token",
					Header:  "Authorization: Bearer top-secret",
					Enabled: &enabled,
				},
			},
		},
	}
	red := cfg.RedactForAPI()
	if red.Notifier == nil || len(red.Notifier.Channels) != 1 {
		t.Fatalf("expected notifier channel structure preserved, got %+v", red.Notifier)
	}
	ch := red.Notifier.Channels[0]
	if ch.URL != "" || ch.Header != "" {
		t.Fatalf("expected URL and Header redacted, got url=%q header=%q", ch.URL, ch.Header)
	}
	if ch.Name != "discord" || ch.Type != "discord" {
		t.Fatalf("expected non-secret fields preserved, got name=%q type=%q", ch.Name, ch.Type)
	}
	// The original config must be untouched (RedactForAPI returns a copy).
	if cfg.Notifier.Channels[0].URL == "" {
		t.Fatal("RedactForAPI mutated the source config's channel URL")
	}
}

func TestPar2ConfigDefaults(t *testing.T) {
	// Unset config: defaults applied, disabled.
	var nilCfg *Config
	def := nilCfg.Par2Config()
	if def.Enabled {
		t.Error("nil config Par2 enabled, want disabled")
	}
	if def.BinaryPath != defaultPar2BinaryPath || def.MaxConcurrent != defaultPar2MaxConcurrent || def.TimeoutMinutes != defaultPar2TimeoutMinutes {
		t.Errorf("nil config defaults = %+v", def)
	}

	empty := (&Config{}).Par2Config()
	if empty.Enabled {
		t.Error("empty config Par2 enabled, want disabled (opt-in)")
	}
	if empty.BinaryPath != "par2" || empty.MaxConcurrent != 1 || empty.TimeoutMinutes != 30 {
		t.Errorf("empty config defaults = %+v, want par2/1/30", empty)
	}
	if empty.MaxBytes != 0 {
		t.Errorf("empty MaxBytes = %d, want 0 (unlimited)", empty.MaxBytes)
	}
}

func TestPar2ConfigOverridesAndClamps(t *testing.T) {
	on := true
	cfg := &Config{
		Par2Enable:         &on,
		Par2BinaryPath:     "  /usr/bin/par2  ",
		Par2WorkDir:        "  /scratch  ",
		Par2MaxConcurrent:  -5,  // clamps to default 1
		Par2TimeoutMinutes: 0,   // clamps to default 30
		Par2MaxBytes:       -10, // clamps to 0
	}
	s := cfg.Par2Config()
	if !s.Enabled {
		t.Error("Enabled = false, want true")
	}
	if s.BinaryPath != "/usr/bin/par2" {
		t.Errorf("BinaryPath = %q, want trimmed /usr/bin/par2", s.BinaryPath)
	}
	if s.WorkDir != "/scratch" {
		t.Errorf("WorkDir = %q, want trimmed /scratch", s.WorkDir)
	}
	if s.MaxConcurrent != 1 {
		t.Errorf("MaxConcurrent = %d, want clamped 1", s.MaxConcurrent)
	}
	if s.TimeoutMinutes != 30 {
		t.Errorf("TimeoutMinutes = %d, want clamped 30", s.TimeoutMinutes)
	}
	if s.MaxBytes != 0 {
		t.Errorf("MaxBytes = %d, want clamped 0", s.MaxBytes)
	}
}

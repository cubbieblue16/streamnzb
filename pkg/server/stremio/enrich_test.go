package stremio

import (
	"strings"
	"testing"
	"time"

	"streamnzb/pkg/release"
	"streamnzb/pkg/search/parser"
)

func TestResolutionTag(t *testing.T) {
	cases := []struct {
		name string
		meta *parser.ParsedRelease
		want string
	}{
		{"nil", nil, ""},
		{"explicit", &parser.ParsedRelease{Resolution: "2160p"}, "2160p"},
		{"group-4k", &parser.ParsedRelease{Resolution: "4K"}, "4K"},
		{"group-fallback", &parser.ParsedRelease{Resolution: "uhd 2160"}, "uhd 2160"},
		{"none", &parser.ParsedRelease{}, ""},
	}
	for _, c := range cases {
		if got := resolutionTag(c.meta); got != c.want {
			t.Errorf("%s: resolutionTag = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestNormalizeCodecAndQuality(t *testing.T) {
	if got := normalizeCodec("H.265"); got != "HEVC" {
		t.Errorf("normalizeCodec(H.265) = %q, want HEVC", got)
	}
	if got := normalizeCodec("x264"); got != "AVC" {
		t.Errorf("normalizeCodec(x264) = %q, want AVC", got)
	}
	if got := normalizeQuality("Blu-ray"); got != "BluRay" {
		t.Errorf("normalizeQuality(Blu-ray) = %q, want BluRay", got)
	}
	if got := normalizeQuality("WEB-DL"); got != "WEB" {
		t.Errorf("normalizeQuality(WEB-DL) = %q, want WEB", got)
	}
}

func TestBuildEnrichedStreamName(t *testing.T) {
	meta := &parser.ParsedRelease{Resolution: "2160p"}
	if got := buildEnrichedStreamName("StreamNZB", meta, false); got != "StreamNZB\n2160p" {
		t.Errorf("name = %q, want StreamNZB\\n2160p", got)
	}
	if got := buildEnrichedStreamName("StreamNZB", meta, true); got != "⚡ StreamNZB\n2160p" {
		t.Errorf("avail name = %q, want bolt prefix", got)
	}
	// No resolution -> falls back to quality.
	if got := buildEnrichedStreamName("StreamNZB", &parser.ParsedRelease{Quality: "WEB-DL"}, false); got != "StreamNZB\nWEB" {
		t.Errorf("quality fallback = %q, want StreamNZB\\nWEB", got)
	}
	// No metadata at all -> plain brand.
	if got := buildEnrichedStreamName("StreamNZB", nil, false); got != "StreamNZB" {
		t.Errorf("nil meta name = %q, want StreamNZB", got)
	}
}

func TestSizeTag(t *testing.T) {
	cases := []struct {
		bytes int64
		want  string
	}{
		{0, ""},
		{-5, ""},
		{500 * 1024, "500 KB"},
		{3 * 1024 * 1024, "3 MB"},
		{int64(2.5 * 1024 * 1024 * 1024), "2.50 GB"},
	}
	for _, c := range cases {
		if got := sizeTag(c.bytes); got != c.want {
			t.Errorf("sizeTag(%d) = %q, want %q", c.bytes, got, c.want)
		}
	}
}

func TestHumanAgeFrom(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	mk := func(d time.Duration) string {
		return now.Add(-d).Format(time.RFC1123Z)
	}
	cases := []struct {
		name    string
		pubDate string
		want    string
	}{
		{"empty", "", ""},
		{"garbage", "not-a-date", ""},
		{"minutes", mk(30 * time.Minute), "30m"},
		{"hours", mk(5 * time.Hour), "5h"},
		{"days", mk(3 * 24 * time.Hour), "3d"},
		{"months", mk(60 * 24 * time.Hour), "2mo"},
		{"years", mk(800 * 24 * time.Hour), "2y"},
		{"future", now.Add(time.Hour).Format(time.RFC1123Z), ""},
	}
	for _, c := range cases {
		if got := humanAgeFrom(c.pubDate, now); got != c.want {
			t.Errorf("%s: humanAgeFrom(%q) = %q, want %q", c.name, c.pubDate, got, c.want)
		}
	}
}

func TestParsePubDateLayouts(t *testing.T) {
	for _, s := range []string{
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"2006-01-02T15:04:05Z",
	} {
		if _, ok := parsePubDate(s); !ok {
			t.Errorf("expected to parse layout sample %q", s)
		}
	}
	if _, ok := parsePubDate("definitely not a date"); ok {
		t.Error("expected garbage to fail parsing")
	}
}

func TestBuildEnrichedStreamDescriptionFull(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	meta := &parser.ParsedRelease{
		Resolution: "2160p",
		Quality:    "BluRay",
		Codec:      "x265",
		HDR:        []string{"HDR10"},
		Audio:      []string{"TrueHD"},
		Channels:   []string{"7.1"},
		Group:      "FraMeSToR",
	}
	rel := &release.Release{
		Title:   "Movie.2024.2160p.BluRay.x265-FraMeSToR",
		Size:    45_000_000_000,
		PubDate: now.Add(-2 * 24 * time.Hour).Format(time.RFC1123Z),
	}
	got := buildEnrichedStreamDescription(meta, rel, true, "NZBgeek", now)

	for _, want := range []string{
		"2160p", "BluRay", "HEVC", // line 1
		"HDR10", "TrueHD 7.1", // line 2
		"💾 41.91 GB", "🕒 2d", "👥 FraMeSToR", // line 3
		"⚡ Available",                            // line 4
		"Movie.2024.2160p.BluRay.x265-FraMeSToR", // line 5
		"🔍 NZBgeek",                              // line 6
	} {
		if !strings.Contains(got, want) {
			t.Errorf("description missing %q:\n%s", want, got)
		}
	}
}

func TestBuildEnrichedStreamDescriptionDegradesGracefully(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	// No metadata, no release -> safe fallback, never empty.
	if got := buildEnrichedStreamDescription(nil, nil, false, "", now); got != "StreamNZB" {
		t.Errorf("all-nil description = %q, want StreamNZB", got)
	}
	// Release with only a title still surfaces the title.
	rel := &release.Release{Title: "Some.Release.Name"}
	got := buildEnrichedStreamDescription(nil, rel, false, "", now)
	if !strings.Contains(got, "Some.Release.Name") {
		t.Errorf("expected raw title in degraded description, got %q", got)
	}
}

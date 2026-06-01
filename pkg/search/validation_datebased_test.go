package search

import (
	"testing"

	"streamnzb/pkg/release"
)

// rawSceneTitles mirrors what the handler builds for a registered WWE Raw show:
// one validation query per scene title (no date/year suffix).
var rawSceneTitles = []string{"WWE Monday Night RAW", "WWE RAW"}

func titlesOf(releases []*release.Release) []string {
	out := make([]string, 0, len(releases))
	for _, r := range releases {
		out = append(out, r.Title)
	}
	return out
}

func TestDateMatchAcceptsAirDateRelease(t *testing.T) {
	releases := []*release.Release{
		{Title: "WWE.Monday.Night.RAW.2024.01.08.1080p.HDTV.x264-Heel"},  // exact air date
		{Title: "WWE.RAW.2024.01.08.720p.HDTV.x264-NWCHD"},               // alt scene title, exact date
		{Title: "WWE.Monday.Night.RAW.2024.01.15.1080p.HDTV.x264-Heel"},  // wrong week -> drop
		{Title: "WWE.Friday.Night.SmackDown.2024.01.05.1080p.HDTV.x264"}, // wrong show -> drop on title
	}

	dm := &DateMatch{AirDate: "2024-01-08", ToleranceDays: 1}
	filtered, stats := ValidateSearchResultsWithDateMatch(releases, "series", rawSceneTitles, "1", "1", true, false, dm)

	if len(filtered) != 2 {
		t.Fatalf("expected 2 accepted, got %d: %v", len(filtered), titlesOf(filtered))
	}
	if stats.AcceptedExactEpisode != 2 {
		t.Fatalf("expected 2 air-date accepts, got %d", stats.AcceptedExactEpisode)
	}
	if stats.DroppedEpisodeRequest != 1 {
		t.Fatalf("expected 1 wrong-date drop, got %d", stats.DroppedEpisodeRequest)
	}
	if stats.DroppedTitle != 1 {
		t.Fatalf("expected 1 wrong-show title drop, got %d", stats.DroppedTitle)
	}
}

func TestDateMatchTolerance(t *testing.T) {
	releases := []*release.Release{
		{Title: "WWE.Monday.Night.RAW.2024.01.09.1080p.HDTV.x264-Heel"}, // +1 day -> accept
		{Title: "WWE.Monday.Night.RAW.2024.01.10.1080p.HDTV.x264-Heel"}, // +2 days -> drop
	}
	dm := &DateMatch{AirDate: "2024-01-08", ToleranceDays: 1}
	filtered, _ := ValidateSearchResultsWithDateMatch(releases, "series", rawSceneTitles, "1", "1", true, false, dm)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 accepted within tolerance, got %d: %v", len(filtered), titlesOf(filtered))
	}
	if filtered[0].Title != "WWE.Monday.Night.RAW.2024.01.09.1080p.HDTV.x264-Heel" {
		t.Fatalf("expected the +1 day release, got %q", filtered[0].Title)
	}
}

// Without a DateMatch the old SxxExx gate still drops date-named WWE releases —
// this is the bug the date path fixes.
func TestDateNamedReleaseDroppedWithoutDateMatch(t *testing.T) {
	releases := []*release.Release{
		{Title: "WWE.Monday.Night.RAW.2024.01.08.1080p.HDTV.x264-Heel"},
	}
	filtered, _ := ValidateSearchResultsWithDateMatch(releases, "series", rawSceneTitles, "1", "1", true, false, nil)
	if len(filtered) != 0 {
		t.Fatalf("expected SxxExx gate to drop date-named release, got %d: %v", len(filtered), titlesOf(filtered))
	}
}

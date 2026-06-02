package search

import (
	"testing"

	"streamnzb/pkg/release"
)

// wweWrestleManiaSceneTitles mirrors what the handler builds for a registered
// WWE PLE: prefix + un-prefixed variants of the TMDB title.
var wweWrestleManiaSceneTitles = []string{"WWE WrestleMania 41", "WrestleMania 41"}

func TestEventMatchAcceptsPLEReleases(t *testing.T) {
	releases := []*release.Release{
		// Canonical scene name with year
		{Title: "WWE.WrestleMania.41.Night.1.2025.1080p.WEB.h264-HEEL"},
		// Different separators, no year
		{Title: "WWE WrestleMania 41 Night 2 1080p WEB x265-NOGRP"},
		// Without WWE prefix
		{Title: "WrestleMania.41.Saturday.1080p.WEB-NOGRP"},
		// Wrong year/edition - 40 not 41
		{Title: "WWE.WrestleMania.40.Night.1.2024.1080p.WEB-HEEL"},
		// Wrong event entirely
		{Title: "WWE.Royal.Rumble.2025.1080p.WEB.h264-HEEL"},
		// Unrelated movie
		{Title: "Interstellar.2014.UHD.BluRay.2160p"},
	}
	em := &EventMatch{SceneTitles: wweWrestleManiaSceneTitles}
	filtered, stats := ValidateSearchResultsWithMatchers(releases, "movie", []string{"WWE WrestleMania 41"}, "", "", true, false, nil, em)

	if len(filtered) != 3 {
		t.Fatalf("expected 3 accepted, got %d: %v", len(filtered), titlesOf(filtered))
	}
	if stats.DroppedTitle != 3 {
		t.Fatalf("expected 3 dropped (wrong edition + wrong event + unrelated), got %d", stats.DroppedTitle)
	}
}

func TestEventMatchAcceptsYearOnlySceneTitle(t *testing.T) {
	// When the TMDB title carries a roman numeral edition that scene releases
	// don't use, a year-suffix scene title still matches because the matcher
	// looks at the full release title (which carries the year).
	releases := []*release.Release{
		{Title: "WWE.WrestleMania.40.Night.1.2024.1080p.WEB-HEEL"},
		{Title: "WWE.WrestleMania.40.Night.2.2024.1080p.WEB-HEEL"},
		{Title: "WWE.WrestleMania.41.Night.1.2025.1080p.WEB-HEEL"}, // wrong year
	}
	em := &EventMatch{SceneTitles: []string{"WWE WrestleMania 2024"}}
	filtered, _ := ValidateSearchResultsWithMatchers(releases, "movie", []string{"WWE WrestleMania 2024"}, "", "", true, false, nil, em)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 accepted (both 2024 releases), got %d: %v", len(filtered), titlesOf(filtered))
	}
}

func TestEventMatchRoyalRumble(t *testing.T) {
	releases := []*release.Release{
		{Title: "WWE.Royal.Rumble.2025.1080p.WEB.h264-HEEL"},
		{Title: "WWE Royal Rumble 2025 720p HDTV x264-NWCHD"},
		{Title: "WWE.Royal.Rumble.2024.1080p.WEB-HEEL"}, // wrong year
		{Title: "WWE.WrestleMania.41.Night.1.2025.1080p"}, // wrong event
	}
	em := &EventMatch{SceneTitles: []string{"WWE Royal Rumble 2025", "Royal Rumble 2025"}}
	filtered, _ := ValidateSearchResultsWithMatchers(releases, "movie", []string{"WWE Royal Rumble 2025"}, "", "", true, false, nil, em)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 accepted, got %d: %v", len(filtered), titlesOf(filtered))
	}
}

// Without an EventMatch the default movie title gate drops PLE releases - this
// is the bug the event path fixes.
func TestPLEReleaseDroppedWithoutEventMatch(t *testing.T) {
	releases := []*release.Release{
		// Leading "WWE" token + trailing "Night 1" both break the contiguous
		// block rule of fuzzyTitleMatches.
		{Title: "WWE.WrestleMania.41.Night.1.2025.1080p.WEB-HEEL"},
	}
	filtered, _ := ValidateSearchResultsWithMatchers(releases, "movie", []string{"WrestleMania 41"}, "", "", true, false, nil, nil)
	if len(filtered) != 0 {
		t.Fatalf("expected default title gate to drop PLE release, got %d: %v", len(filtered), titlesOf(filtered))
	}
}

func TestEventMatchWithoutPLEEntriesRejectsAll(t *testing.T) {
	em := &EventMatch{SceneTitles: nil}
	releases := []*release.Release{{Title: "Anything.2024"}}
	filtered, _ := ValidateSearchResultsWithMatchers(releases, "movie", []string{"x"}, "", "", true, false, nil, em)
	if len(filtered) != 0 {
		t.Fatalf("expected empty scene titles to drop all, got %d", len(filtered))
	}
}

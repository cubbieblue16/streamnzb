package datebased

import "testing"

func TestLookupMatchesWWEByName(t *testing.T) {
	cases := []struct {
		name     string
		tmdbName string
		want     string
	}{
		{"raw localized", "WWE Monday Night Raw", "WWE Raw"},
		{"raw short", "WWE Raw", "WWE Raw"},
		{"smackdown localized", "WWE Friday Night SmackDown", "WWE SmackDown"},
		{"smackdown short", "WWE SmackDown", "WWE SmackDown"},
		{"nxt", "WWE NXT", "WWE NXT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			show, ok := Lookup(nil, "", 0, tc.tmdbName, "")
			if !ok {
				t.Fatalf("expected %q to match a date-based show", tc.tmdbName)
			}
			if show.Name != tc.want {
				t.Fatalf("Lookup(%q) = %q, want %q", tc.tmdbName, show.Name, tc.want)
			}
			if len(show.SceneTitles) == 0 {
				t.Fatalf("expected scene titles for %q", show.Name)
			}
			if show.Tolerance() != 1 {
				t.Fatalf("expected default tolerance 1, got %d", show.Tolerance())
			}
		})
	}
}

func TestLookupMatchesRawByPinnedID(t *testing.T) {
	// Real TMDB data: WWE Raw is id 4656 with the bare name "Raw" (imdb tt0185103).
	// Keyword match ("wwe"+"raw") fails on "Raw" alone; the pinned id must catch it.
	if show, ok := Lookup(nil, "", 4656, "Raw", "Raw"); !ok || show.Name != "WWE Raw" {
		t.Fatalf("expected TMDB id 4656 to match WWE Raw, got ok=%v show=%+v", ok, show)
	}
	if show, ok := Lookup(nil, "tt0185103", 0, "Raw", "Raw"); !ok || show.Name != "WWE Raw" {
		t.Fatalf("expected imdb tt0185103 to match WWE Raw, got ok=%v show=%+v", ok, show)
	}
	// Name-only "Raw" with no id must NOT match (avoids false positives like the film "Raw").
	if _, ok := Lookup(nil, "", 0, "Raw", "Raw"); ok {
		t.Fatalf("bare name 'Raw' with no id should not match")
	}
}

func TestLookupRejectsNonDateBased(t *testing.T) {
	if _, ok := Lookup(nil, "tt0903747", 1396, "Breaking Bad", ""); ok {
		t.Fatalf("Breaking Bad should not match a date-based show")
	}
	if _, ok := Lookup(nil, "", 0, "RuPaul's Drag Race", ""); ok {
		t.Fatalf("a non-WWE 'Raw'-less show should not match")
	}
}

func TestLookupMatchesByID(t *testing.T) {
	extra := []Show{{
		Name:        "Custom Daily",
		TMDBIDs:     []int{99999},
		IMDbIDs:     []string{"tt1234567"},
		SceneTitles: []string{"Custom Daily Show"},
	}}
	if show, ok := Lookup(extra, "", 99999, "Anything", ""); !ok || show.Name != "Custom Daily" {
		t.Fatalf("expected TMDB id match, got ok=%v show=%+v", ok, show)
	}
	if show, ok := Lookup(extra, "TT1234567", 0, "Anything", ""); !ok || show.Name != "Custom Daily" {
		t.Fatalf("expected case-insensitive IMDb id match, got ok=%v show=%+v", ok, show)
	}
}

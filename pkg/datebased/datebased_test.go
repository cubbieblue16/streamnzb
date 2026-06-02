package datebased

import (
	"reflect"
	"testing"
)

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
			show, ok := Lookup(nil, "", 0, tc.tmdbName, "", nil)
			if !ok {
				t.Fatalf("expected %q to match a date-based show", tc.tmdbName)
			}
			if show.Name != tc.want {
				t.Fatalf("Lookup(%q) = %q, want %q", tc.tmdbName, show.Name, tc.want)
			}
			titles := show.DeriveSceneTitles(tc.tmdbName)
			if len(titles) == 0 {
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
	if show, ok := Lookup(nil, "", 4656, "Raw", "Raw", nil); !ok || show.Name != "WWE Raw" {
		t.Fatalf("expected TMDB id 4656 to match WWE Raw, got ok=%v show=%+v", ok, show)
	}
	if show, ok := Lookup(nil, "tt0185103", 0, "Raw", "Raw", nil); !ok || show.Name != "WWE Raw" {
		t.Fatalf("expected imdb tt0185103 to match WWE Raw, got ok=%v show=%+v", ok, show)
	}
	// Name-only "Raw" with no id must NOT match (avoids false positives like the film "Raw").
	if _, ok := Lookup(nil, "", 0, "Raw", "Raw", nil); ok {
		t.Fatalf("bare name 'Raw' with no id should not match")
	}
}

func TestLookupMatchesAEWByPinnedID(t *testing.T) {
	cases := []struct {
		name     string
		tmdbID   int
		imdbID   string
		tmdbName string
		want     string
		want0    string // expected first derived scene title
	}{
		{"dynamite by tmdb", 91555, "", "All Elite Wrestling: Dynamite", "AEW Dynamite", "AEW Dynamite"},
		{"dynamite by imdb", 0, "tt10691888", "All Elite Wrestling: Dynamite", "AEW Dynamite", "AEW Dynamite"},
		{"collision by tmdb", 226687, "", "All Elite Wrestling: Collision", "AEW Collision", "AEW Collision"},
		{"rampage by tmdb", 126997, "", "All Elite Wrestling: Rampage", "AEW Rampage", "AEW Rampage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			show, ok := Lookup(nil, tc.imdbID, tc.tmdbID, tc.tmdbName, "", nil)
			if !ok {
				t.Fatalf("expected match")
			}
			if show.Name != tc.want {
				t.Fatalf("got %q want %q", show.Name, tc.want)
			}
			titles := show.DeriveSceneTitles(tc.tmdbName)
			if len(titles) == 0 || titles[0] != tc.want0 {
				t.Fatalf("DeriveSceneTitles[0]=%v want %q", titles, tc.want0)
			}
		})
	}
}

// Production-company catch-all: a future AEW show with an unknown id but
// produced by AEW (company 119828) must be picked up and have its scene title
// derived correctly. "Battle of the Belts" → 4 tokens → unprefixed kept.
func TestLookupMatchesByAEWProductionCompany(t *testing.T) {
	show, ok := Lookup(nil, "", 999999, "All Elite Wrestling: Battle of the Belts", "", []int{119828})
	if !ok {
		t.Fatalf("expected AEW production-company match")
	}
	if show.Name != "AEW (production company)" {
		t.Fatalf("got %q want %q", show.Name, "AEW (production company)")
	}
	got := show.DeriveSceneTitles("All Elite Wrestling: Battle of the Belts")
	want := []string{"AEW Battle of the Belts", "Battle of the Belts"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveSceneTitles = %v, want %v", got, want)
	}
}

// WWE production-company catch-all should fire for shows produced by WWE
// (company 146598) even when name/id aren't in the registry. Multi-token →
// un-prefixed fallback retained.
func TestLookupMatchesByWWEProductionCompany(t *testing.T) {
	show, ok := Lookup(nil, "", 999998, "Some New WWE Show", "", []int{146598})
	if !ok {
		t.Fatalf("expected WWE production-company match")
	}
	if show.Name != "WWE (production company)" {
		t.Fatalf("got %q want %q", show.Name, "WWE (production company)")
	}
	got := show.DeriveSceneTitles("Some New WWE Show")
	want := []string{"WWE Some New WWE Show", "Some New WWE Show"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveSceneTitles = %v, want %v", got, want)
	}
}

// Explicit pinned entries should win over the production-company catch-all.
func TestLookupExplicitWinsOverProductionCompany(t *testing.T) {
	// WWE Raw (TMDB 4656) is also produced by WWE (146598). Explicit entry
	// "WWE Raw" should match, not the catch-all.
	show, ok := Lookup(nil, "", 4656, "Raw", "", []int{146598, 13651})
	if !ok {
		t.Fatalf("expected match")
	}
	if show.Name != "WWE Raw" {
		t.Fatalf("got %q want %q", show.Name, "WWE Raw")
	}
}

func TestLookupRejectsNonDateBased(t *testing.T) {
	if _, ok := Lookup(nil, "tt0903747", 1396, "Breaking Bad", "", nil); ok {
		t.Fatalf("Breaking Bad should not match a date-based show")
	}
	if _, ok := Lookup(nil, "", 0, "RuPaul's Drag Race", "", nil); ok {
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
	if show, ok := Lookup(extra, "", 99999, "Anything", "", nil); !ok || show.Name != "Custom Daily" {
		t.Fatalf("expected TMDB id match, got ok=%v show=%+v", ok, show)
	}
	if show, ok := Lookup(extra, "TT1234567", 0, "Anything", "", nil); !ok || show.Name != "Custom Daily" {
		t.Fatalf("expected case-insensitive IMDb id match, got ok=%v show=%+v", ok, show)
	}
}

// Empty production company set must not match the catch-all entries.
func TestLookupEmptyProductionCompanyDoesNotMatchCatchAll(t *testing.T) {
	if _, ok := Lookup(nil, "", 0, "Some Random Show", "", nil); ok {
		t.Fatalf("empty prod-company set should not trigger catch-all match")
	}
}

func TestDeriveSceneTitlesStripsAndPrefixes(t *testing.T) {
	cases := []struct {
		name     string
		show     Show
		input    string
		expected []string
	}{
		{
			name:     "AEW catch-all strips AEW long-form",
			show:     Show{RequirePrefix: "AEW", StripPrefixes: []string{"All Elite Wrestling:"}},
			input:    "All Elite Wrestling: Battle of the Belts",
			expected: []string{"AEW Battle of the Belts", "Battle of the Belts"},
		},
		{
			name:     "explicit scene titles win over derivation, single-token unprefixed dropped",
			show:     Show{SceneTitles: []string{"WWE Monday Night RAW", "WWE RAW"}, RequirePrefix: "WWE"},
			input:    "Raw",
			expected: []string{"WWE Monday Night RAW", "Monday Night RAW", "WWE RAW"},
		},
		{
			name:     "no scene titles, no TMDB name returns nil",
			show:     Show{RequirePrefix: "AEW"},
			input:    "",
			expected: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.show.DeriveSceneTitles(tc.input)
			if !reflect.DeepEqual(got, tc.expected) {
				t.Fatalf("DeriveSceneTitles = %v, want %v", got, tc.expected)
			}
		})
	}
}

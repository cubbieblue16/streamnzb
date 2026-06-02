package eventbased

import (
	"reflect"
	"strings"
	"testing"
)

func TestLookupMatchesPLEsByKeyword(t *testing.T) {
	cases := []struct {
		tmdbTitle string
		wantName  string
	}{
		{"WrestleMania XL", "WWE PLE: WrestleMania"},
		{"WrestleMania 41", "WWE PLE: WrestleMania"},
		{"Royal Rumble 2025", "WWE PLE: Royal Rumble"},
		{"SummerSlam 2024", "WWE PLE: SummerSlam"},
		{"Survivor Series WarGames 2024", "WWE PLE: Survivor Series"},
		{"Money in the Bank 2024", "WWE PLE: Money in the Bank"},
		{"Elimination Chamber: Perth", "WWE PLE: Elimination Chamber"},
		{"Hell in a Cell 2022", "WWE PLE: Hell in a Cell"},
		{"Backlash France", "WWE PLE: Backlash"},
		{"Crown Jewel 2024", "WWE PLE: Crown Jewel"},
		{"King and Queen of the Ring 2024", "WWE PLE: King and Queen of the Ring"},
		{"Bash in Berlin", "WWE PLE: Bash in Berlin"},
		{"Clash at the Castle: Scotland", "WWE PLE: Clash at the Castle"},
		{"Bad Blood 2024", "WWE PLE: Bad Blood"},
		{"Night of Champions 2023", "WWE PLE: Night of Champions"},
		{"Saturday Night's Main Event XXXVII", "WWE PLE: Saturday Night's Main Event"},
		{"WWE Some Future PLE", "WWE PLE (wwe-prefixed)"},
	}
	for _, tc := range cases {
		t.Run(tc.tmdbTitle, func(t *testing.T) {
			m, ok := Lookup(nil, "", 0, tc.tmdbTitle, "", nil)
			if !ok {
				t.Fatalf("expected %q to match a PLE", tc.tmdbTitle)
			}
			if m.Name != tc.wantName {
				t.Fatalf("Lookup(%q) = %q, want %q", tc.tmdbTitle, m.Name, tc.wantName)
			}
		})
	}
}

func TestLookupRejectsNonPLE(t *testing.T) {
	for _, title := range []string{"Interstellar", "Breaking Bad", "Royal Tenenbaums", "Bash", "Inception"} {
		if m, ok := Lookup(nil, "", 0, title, "", nil); ok {
			t.Fatalf("Lookup(%q) unexpectedly matched %+v", title, m)
		}
	}
}

func TestLookupMatchesByID(t *testing.T) {
	extra := []Movie{{
		Name:             "Custom PLE",
		TMDBIDs:          []int{99999},
		IMDbIDs:          []string{"tt1234567"},
		SceneTitles:      []string{"WWE Custom Show"},
		RequireWWEPrefix: true,
	}}
	if m, ok := Lookup(extra, "", 99999, "Anything", "", nil); !ok || m.Name != "Custom PLE" {
		t.Fatalf("expected TMDB id match, got ok=%v movie=%+v", ok, m)
	}
	if m, ok := Lookup(extra, "TT1234567", 0, "Anything", "", nil); !ok || m.Name != "Custom PLE" {
		t.Fatalf("expected case-insensitive IMDb id match, got ok=%v movie=%+v", ok, m)
	}
}

// "Clash in Italy" (TMDB 1704958) — title carries NO "WWE" prefix and no PLE
// keyword in the registry matches. Production-company match is what catches it.
func TestLookupMatchesByProductionCompany(t *testing.T) {
	m, ok := Lookup(nil, "tt40017618", 1704958, "Clash in Italy", "Clash in Italy", []int{146598})
	if !ok {
		t.Fatalf("expected production-company match for Clash in Italy")
	}
	if m.Name != "WWE PLE (production company)" {
		t.Fatalf("Lookup name = %q, want %q", m.Name, "WWE PLE (production company)")
	}
	titles := m.DeriveSceneTitles("Clash in Italy")
	want := []string{"WWE Clash in Italy", "Clash in Italy"}
	if !reflect.DeepEqual(titles, want) {
		t.Fatalf("DeriveSceneTitles = %v, want %v", titles, want)
	}
}

// Specific keyword entries should still win over the production-company fallback
// when both could match — keyword entries are listed earlier in builtin.
func TestLookupKeywordWinsOverProductionCompany(t *testing.T) {
	m, ok := Lookup(nil, "tt32755928", 1309070, "WWE Royal Rumble 2025", "", []int{146598})
	if !ok {
		t.Fatalf("expected match")
	}
	if m.Name != "WWE PLE: Royal Rumble" {
		t.Fatalf("Lookup name = %q, want %q", m.Name, "WWE PLE: Royal Rumble")
	}
}

// Production-company match should be ignored when company set is empty (i.e.
// MovieDetails didn't return production_companies for some reason).
func TestLookupNoProductionCompanyDoesNotMatch(t *testing.T) {
	if m, ok := Lookup(nil, "", 1704958, "Clash in Italy", "Clash in Italy", nil); ok {
		t.Fatalf("Lookup unexpectedly matched %+v", m)
	}
}

func TestDeriveSceneTitlesPrependsWWE(t *testing.T) {
	m, ok := Lookup(nil, "", 0, "WrestleMania XL", "", nil)
	if !ok {
		t.Fatalf("expected WrestleMania match")
	}
	got := m.DeriveSceneTitles("WrestleMania XL")
	want := []string{"WWE WrestleMania XL", "WrestleMania XL"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveSceneTitles = %v, want %v", got, want)
	}
}

func TestDeriveSceneTitlesKeepsWWEPrefixWhenAlreadyPresent(t *testing.T) {
	m, ok := Lookup(nil, "", 0, "WWE SummerSlam 2024", "", nil)
	if !ok {
		t.Fatalf("expected SummerSlam match")
	}
	got := m.DeriveSceneTitles("WWE SummerSlam 2024")
	want := []string{"WWE SummerSlam 2024", "SummerSlam 2024"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveSceneTitles = %v, want %v", got, want)
	}
}

func TestDeriveSceneTitlesExplicitOverridesUsedAsIs(t *testing.T) {
	m := Movie{
		SceneTitles:      []string{"Foo", "Bar"},
		RequireWWEPrefix: true,
	}
	got := m.DeriveSceneTitles("Ignored")
	// RequireWWEPrefix still applies to explicit titles when missing.
	want := []string{"WWE Foo", "Foo", "WWE Bar", "Bar"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveSceneTitles = %v, want %v", got, want)
	}
}

func TestDeriveSceneTitlesDedups(t *testing.T) {
	m := Movie{SceneTitles: []string{"WWE Foo", "wwe foo", "WWE Foo "}}
	got := m.DeriveSceneTitles("")
	if len(got) != 1 {
		t.Fatalf("expected dedup to 1 entry, got %v", got)
	}
	if !strings.EqualFold(got[0], "WWE Foo") {
		t.Fatalf("unexpected entry %q", got[0])
	}
}

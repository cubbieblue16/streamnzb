// Package datebased holds the registry of "date-organised" TV shows whose
// usenet releases are named by air date (YYYY MM DD) instead of SxxExx.
//
// WWE Raw / SmackDown / NXT are the canonical case: TMDB organises them into
// seasons+episodes and gives each episode an air_date, but scene/p2p usenet
// releases are named e.g. "WWE Monday Night RAW 2024 01 08 ...". The normal
// "<title> SxxExx" query never matches and the SxxExx validation gate drops
// every result. For a registered show we instead:
//   - override the search title to the scene title(s),
//   - emit "<scene title> YYYY MM DD" queries from the episode air date, and
//   - validate releases by air date (+/- ToleranceDays) instead of SxxExx.
//
// This mirrors the torrentio fork's TMDB_TO_IMDB_OVERRIDES + date-tolerance
// approach. The built-in registry covers the common WWE shows; operators can
// add or override entries via the "date_based_shows" config field without
// recompiling.
package datebased

import "strings"

// Show describes a date-organised TV show and how to search for it.
type Show struct {
	// Name is a human label used only in logs.
	Name string `json:"name"`
	// TMDBIDs / IMDbIDs match the show by exact id (preferred when known).
	TMDBIDs []int    `json:"tmdb_ids,omitempty"`
	IMDbIDs []string `json:"imdb_ids,omitempty"`
	// Keywords match the show by TMDB name: every keyword (lower-cased) must be
	// a substring of the TMDB name or original name. Used when ids are unknown.
	Keywords []string `json:"keywords,omitempty"`
	// SceneTitles are the title strings used to build search + validation
	// queries, most-specific first (e.g. "WWE Monday Night RAW", then "WWE RAW").
	SceneTitles []string `json:"scene_titles"`
	// ToleranceDays is the +/- window (in days) used to match a release's date
	// against the episode air date. Defaults to 1 when zero.
	ToleranceDays int `json:"tolerance_days,omitempty"`
}

// Tolerance returns the configured date tolerance in days, defaulting to 1.
func (s Show) Tolerance() int {
	if s.ToleranceDays > 0 {
		return s.ToleranceDays
	}
	return 1
}

// builtin is the default registry of date-organised shows.
//
// IDs are pinned so detection is language-proof: TMDB lists WWE Raw under the
// bare name "Raw" (id 4656), which would never satisfy the "wwe"+"raw" keyword
// match. Keywords remain as a fallback for re-numbered/duplicate TMDB entries.
var builtin = []Show{
	{Name: "WWE Raw", TMDBIDs: []int{4656}, IMDbIDs: []string{"tt0185103"}, Keywords: []string{"wwe", "raw"}, SceneTitles: []string{"WWE Monday Night RAW", "WWE RAW"}},
	{Name: "WWE SmackDown", TMDBIDs: []int{1549}, IMDbIDs: []string{"tt0227972"}, Keywords: []string{"wwe", "smackdown"}, SceneTitles: []string{"WWE Friday Night SmackDown", "WWE SmackDown"}},
	{Name: "WWE NXT", TMDBIDs: []int{31991}, IMDbIDs: []string{"tt1601141"}, Keywords: []string{"wwe", "nxt"}, SceneTitles: []string{"WWE NXT"}},
	{Name: "WWE Main Event", TMDBIDs: []int{46707}, IMDbIDs: []string{"tt2659152"}, Keywords: []string{"wwe", "main event"}, SceneTitles: []string{"WWE Main Event"}},
}

// Builtin returns a copy of the default registry.
func Builtin() []Show {
	return append([]Show(nil), builtin...)
}

// Lookup reports whether the requested show is date-organised. extra entries
// (from config) are checked before the built-ins so operators can override.
// Matching is by TMDB id, then IMDb id, then keyword match against the names.
func Lookup(extra []Show, imdbID string, tmdbID int, tmdbName, originalName string) (Show, bool) {
	name := strings.ToLower(strings.TrimSpace(tmdbName) + " " + strings.TrimSpace(originalName))
	imdb := strings.ToLower(strings.TrimSpace(imdbID))

	candidates := make([]Show, 0, len(extra)+len(builtin))
	candidates = append(candidates, extra...)
	candidates = append(candidates, builtin...)

	for _, show := range candidates {
		if len(show.SceneTitles) == 0 {
			continue
		}
		if tmdbID > 0 {
			for _, id := range show.TMDBIDs {
				if id == tmdbID {
					return show, true
				}
			}
		}
		if imdb != "" {
			for _, id := range show.IMDbIDs {
				if strings.EqualFold(strings.TrimSpace(id), imdb) {
					return show, true
				}
			}
		}
		if len(show.Keywords) > 0 && strings.TrimSpace(name) != "" {
			matched := true
			for _, kw := range show.Keywords {
				kw = strings.ToLower(strings.TrimSpace(kw))
				if kw == "" {
					continue
				}
				if !strings.Contains(name, kw) {
					matched = false
					break
				}
			}
			if matched {
				return show, true
			}
		}
	}
	return Show{}, false
}

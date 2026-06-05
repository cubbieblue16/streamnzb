// Package datebased holds the registry of "date-organised" TV shows whose
// usenet releases are named by air date (YYYY MM DD) instead of SxxExx.
//
// WWE Raw / SmackDown / NXT and AEW Dynamite / Collision / Rampage are the
// canonical cases: TMDB organises them into seasons+episodes and gives each
// episode an air_date, but scene/p2p usenet releases are named e.g.
// "WWE Monday Night RAW 2024 01 08 ..." or "AEW Dynamite 2025 09 10 ...".
// The normal "<title> SxxExx" query never matches and the SxxExx validation
// gate drops every result. For a registered show we instead:
//   - override the search title to the scene title(s),
//   - emit "<scene title> YYYY MM DD" queries from the episode air date, and
//   - validate releases by air date (+/- ToleranceDays) instead of SxxExx.
//
// Two kinds of registry entries:
//   - Explicit entries with pinned ids and SceneTitles (e.g. WWE Raw, AEW
//     Dynamite): used for shows whose scene names diverge from the TMDB name
//     ("Raw" on TMDB = "WWE Monday Night RAW" on scene).
//   - Production-company catch-all entries (e.g. WWE id 146598, AEW id 119828):
//     match any show produced by that company, then derive scene titles by
//     stripping the company's long-form name from the TMDB title and
//     re-applying the brand prefix. Future shows by these companies "just work"
//     without registry updates.
//
// Operators can add or override entries via the "date_based_shows" config
// field without recompiling.
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
	// ProductionCompanyIDs match by TMDB production_companies: any one of the
	// listed ids appearing on the show counts as a match. Used as a structural
	// fallback so any show produced by e.g. WWE (146598) or AEW (119828) is
	// auto-detected without an explicit registry entry.
	ProductionCompanyIDs []int `json:"production_company_ids,omitempty"`
	// SceneTitles are the title strings used to build search + validation
	// queries, most-specific first (e.g. "WWE Monday Night RAW", then "WWE RAW").
	// When empty, DeriveSceneTitles falls back to RequirePrefix + TMDB name
	// (with StripPrefixes applied first).
	SceneTitles []string `json:"scene_titles,omitempty"`
	// RequirePrefix, when non-empty, prepends "<prefix> " to the TMDB-derived
	// scene title (and to explicit SceneTitles entries that don't already lead
	// with it). Common values: "WWE", "AEW".
	RequirePrefix string `json:"require_prefix,omitempty"`
	// StripPrefixes are case-insensitive title prefixes stripped from the TMDB
	// name before RequirePrefix is applied. Used by production-company
	// catch-all entries so e.g. "All Elite Wrestling: Dynamite" -> "AEW Dynamite".
	StripPrefixes []string `json:"strip_prefixes,omitempty"`
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
//
// Production-company catch-all entries are listed LAST so the explicit entries
// (with their hand-tuned SceneTitles) win when both could match.
var builtin = []Show{
	// --- WWE weekly shows (explicit) ---
	{Name: "WWE Raw", TMDBIDs: []int{4656}, IMDbIDs: []string{"tt0185103"}, Keywords: []string{"wwe", "raw"}, SceneTitles: []string{"WWE Monday Night RAW", "WWE RAW"}},
	{Name: "WWE SmackDown", TMDBIDs: []int{1549}, IMDbIDs: []string{"tt0227972"}, Keywords: []string{"wwe", "smackdown"}, SceneTitles: []string{"WWE Friday Night SmackDown", "WWE SmackDown"}},
	{Name: "WWE NXT", TMDBIDs: []int{31991}, IMDbIDs: []string{"tt1601141"}, Keywords: []string{"wwe", "nxt"}, SceneTitles: []string{"WWE NXT"}},
	{Name: "WWE Main Event", TMDBIDs: []int{46707}, IMDbIDs: []string{"tt2659152"}, Keywords: []string{"wwe", "main event"}, SceneTitles: []string{"WWE Main Event"}},

	// --- AEW weekly shows (explicit) ---
	// TMDB names these "All Elite Wrestling: Dynamite" etc., but scene releases
	// always use "AEW Dynamite", "AEW Collision", "AEW Rampage".
	{Name: "AEW Dynamite", TMDBIDs: []int{91555}, IMDbIDs: []string{"tt10691888"}, Keywords: []string{"dynamite"}, SceneTitles: []string{"AEW Dynamite"}},
	{Name: "AEW Collision", TMDBIDs: []int{226687}, IMDbIDs: []string{"tt27776045"}, Keywords: []string{"collision"}, SceneTitles: []string{"AEW Collision"}},
	{Name: "AEW Rampage", TMDBIDs: []int{126997}, IMDbIDs: []string{"tt14738480"}, Keywords: []string{"rampage"}, SceneTitles: []string{"AEW Rampage"}},

	// --- Production-company catch-alls ---
	// WWE = 146598. Any future WWE-produced show that isn't in the explicit
	// list above is detected via this entry. SceneTitles are derived from the
	// TMDB name with "World Wrestling Entertainment[:]" stripped and "WWE"
	// re-applied.
	{
		Name:                 "WWE (production company)",
		ProductionCompanyIDs: []int{146598, 13651}, // 13651 = WWE Home Video
		RequirePrefix:        "WWE",
		StripPrefixes:        []string{"World Wrestling Entertainment:", "World Wrestling Entertainment", "WWE:"},
	},
	// AEW = 119828. TMDB titles AEW shows as "All Elite Wrestling: <Show>" —
	// strip that long-form prefix and re-apply "AEW".
	{
		Name:                 "AEW (production company)",
		ProductionCompanyIDs: []int{119828},
		RequirePrefix:        "AEW",
		StripPrefixes:        []string{"All Elite Wrestling:", "All Elite Wrestling", "AEW:"},
	},
}

// Builtin returns a copy of the default registry.
func Builtin() []Show {
	return append([]Show(nil), builtin...)
}

// Lookup reports whether the requested show is date-organised. extra entries
// (from config) are checked before the built-ins so operators can override.
// Matching is by TMDB id, then IMDb id, then keyword match against the names,
// then TMDB production-company id.
//
// showType/genres are TMDB's series type and genre names. They gate ONLY the
// production-company catch-all: a documentary or miniseries produced by WWE/AEW
// (e.g. "Hulk Hogan: Real American") is episode-numbered, not date-organised, so
// it must not inherit the date-based override. Explicit id/keyword/config matches
// are intentional and never gated by type.
func Lookup(extra []Show, imdbID string, tmdbID int, tmdbName, originalName string, productionCompanyIDs []int, showType string, genres []string) (Show, bool) {
	name := strings.ToLower(strings.TrimSpace(tmdbName) + " " + strings.TrimSpace(originalName))
	imdb := strings.ToLower(strings.TrimSpace(imdbID))

	companySet := make(map[int]struct{}, len(productionCompanyIDs))
	for _, id := range productionCompanyIDs {
		if id > 0 {
			companySet[id] = struct{}{}
		}
	}

	candidates := make([]Show, 0, len(extra)+len(builtin))
	candidates = append(candidates, extra...)
	candidates = append(candidates, builtin...)

	for _, show := range candidates {
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
			// Keyword entries without an explicit id and without an explicit
			// SceneTitles list are too risky to match (false positives on
			// generic English words like "raw" or "dynamite"). Require either
			// SceneTitles to be set OR the entry to also pin an id.
			if len(show.SceneTitles) == 0 && len(show.TMDBIDs) == 0 && len(show.IMDbIDs) == 0 {
				continue
			}
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
		if len(show.ProductionCompanyIDs) > 0 && len(companySet) > 0 {
			// The catch-all auto-detects *weekly* WWE/AEW programs. Documentaries
			// and miniseries by the same companies are SxxExx-numbered, so skip
			// them here (they fall through to the normal episode search).
			if isNonWeeklyFormat(showType, genres) {
				continue
			}
			for _, id := range show.ProductionCompanyIDs {
				if _, ok := companySet[id]; ok {
					return show, true
				}
			}
		}
	}
	return Show{}, false
}

// isNonWeeklyFormat reports whether the show's TMDB type/genres mark it as a
// documentary or limited series rather than a recurring weekly program. Date-based
// wrestling shows (Raw, SmackDown, Dynamite, ...) are type "Scripted"/"Reality";
// WWE/AEW documentaries are type "Documentary"/"Miniseries" and/or genre
// "Documentary". The check is a denylist so an empty/unknown type still counts as
// weekly, preserving the catch-all's "future weekly shows just work" behaviour.
func isNonWeeklyFormat(showType string, genres []string) bool {
	switch strings.ToLower(strings.TrimSpace(showType)) {
	case "documentary", "miniseries":
		return true
	}
	for _, g := range genres {
		if strings.EqualFold(strings.TrimSpace(g), "documentary") {
			return true
		}
	}
	return false
}

// DeriveSceneTitles returns the scene-title variants for the given TMDB name.
// Explicit SceneTitles take precedence; otherwise the TMDB name is used as
// the base after stripping any configured StripPrefixes, then prepended with
// RequirePrefix (if set) and the un-prefixed variant retained as a fallback.
//
// Returns an empty slice when no usable title can be derived (no SceneTitles
// and no usable TMDB name) — callers should treat that as "don't apply the
// date-based override."
func (s Show) DeriveSceneTitles(tmdbName string) []string {
	var base []string
	if len(s.SceneTitles) > 0 {
		for _, t := range s.SceneTitles {
			if trimmed := strings.TrimSpace(t); trimmed != "" {
				base = append(base, trimmed)
			}
		}
		return uniqueLowerOrdered(applyPrefix(base, s.RequirePrefix))
	}
	tmdbName = strings.TrimSpace(tmdbName)
	if tmdbName == "" {
		return nil
	}
	stripped := stripLeadingPrefix(tmdbName, s.StripPrefixes)
	if stripped != "" {
		base = append(base, stripped)
	}
	return uniqueLowerOrdered(applyPrefix(base, s.RequirePrefix))
}

// applyPrefix returns titles with the brand prefix applied (when set). The
// prefix-prepended form is always emitted. The un-prefixed fallback is ONLY
// emitted when the remaining base (minus a trailing year) is multi-token —
// single-token bases like "Revolution 2025" → "Revolution" collide with common
// English words and produce false positives. Real WWE/AEW releases virtually
// always carry the brand prefix, so dropping the single-token fallback is safe.
func applyPrefix(in []string, prefix string) []string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return in
	}
	out := make([]string, 0, len(in)*2)
	for _, t := range in {
		var prefixed, rest string
		if hasPrefix(t, prefix) {
			prefixed = t
			rest = strings.TrimSpace(t[len(prefix):])
		} else {
			prefixed = prefix + " " + t
			rest = t
		}
		out = append(out, prefixed)
		if rest != "" && isMultiTokenAfterYear(rest) {
			out = append(out, rest)
		}
	}
	return out
}

// isMultiTokenAfterYear reports whether s has 2+ non-year tokens. Used to gate
// the un-prefixed fallback variant in applyPrefix: single-token bases like
// "Revolution" or "Dynasty" (after stripping the year) are too generic to match
// safely without the brand prefix.
func isMultiTokenAfterYear(s string) bool {
	count := 0
	for _, w := range strings.Fields(s) {
		// strip a trailing 4-digit year token
		if len(w) == 4 {
			allDigit := true
			for _, r := range w {
				if r < '0' || r > '9' {
					allDigit = false
					break
				}
			}
			if allDigit {
				continue
			}
		}
		count++
		if count >= 2 {
			return true
		}
	}
	return false
}

// stripLeadingPrefix returns name with the first matching prefix from
// prefixes stripped (case-insensitive, followed by an optional separator).
// Returns name unchanged when none match.
func stripLeadingPrefix(name string, prefixes []string) string {
	name = strings.TrimSpace(name)
	for _, p := range prefixes {
		p = strings.TrimSpace(p)
		if p == "" || len(name) <= len(p) {
			continue
		}
		if !strings.EqualFold(name[:len(p)], p) {
			continue
		}
		rest := strings.TrimLeft(name[len(p):], " :-_.")
		if rest != "" {
			return rest
		}
	}
	return name
}

// hasPrefix reports whether s starts with prefix followed by a separator
// character. The separator check prevents "WWES..." from matching prefix "WWE".
func hasPrefix(s, prefix string) bool {
	if len(s) < len(prefix)+1 {
		return false
	}
	if !strings.EqualFold(s[:len(prefix)], prefix) {
		return false
	}
	switch s[len(prefix)] {
	case ' ', '.', '-', '_', ':':
		return true
	}
	return false
}

func uniqueLowerOrdered(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		key := strings.ToLower(s)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

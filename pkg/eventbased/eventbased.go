// Package eventbased holds the registry of "event-organised" movies whose
// usenet releases are named by event + year/edition rather than by a single
// canonical title.
//
// WWE Premium Live Events (WrestleMania, Royal Rumble, SummerSlam, Survivor
// Series, ...) are the canonical case: TMDB lists each PLE as its own movie
// entry, often without the "WWE" prefix that scene releases always carry
// ("WWE.WrestleMania.40.Night.1.2024..."). The default movie search emits
// "<TMDB title> <year>" and validation's fuzzy title gate (which expects the
// requested words as a contiguous block with only articles before) drops any
// release whose parsed title leads with extra tokens like "WWE" or trails
// with "Night 1". Both gates fire even when the release is correct.
//
// For a registered event movie we instead:
//   - override the search title to the scene title(s) (with "WWE " prefix
//     prepended when required by the entry), and
//   - validate with token-subset matching: a release is accepted when its
//     parsed title contains every token of at least one scene title, in any
//     order, regardless of leading/trailing extras.
//
// This mirrors the pkg/datebased pattern but for the movie path. The built-in
// registry covers WWE PLE event keywords; operators can add or override
// entries via the "event_based_movies" config field without recompiling.
package eventbased

import "strings"

// Movie describes an event-organised movie and how to search/validate it.
type Movie struct {
	// Name is a human label used only in logs.
	Name string `json:"name"`
	// TMDBIDs / IMDbIDs match the movie by exact id (preferred when known).
	TMDBIDs []int    `json:"tmdb_ids,omitempty"`
	IMDbIDs []string `json:"imdb_ids,omitempty"`
	// Keywords match by TMDB title: every keyword (lower-cased) must be a
	// substring of the TMDB title or original title. Used when ids are unknown.
	Keywords []string `json:"keywords,omitempty"`
	// ProductionCompanyIDs match by TMDB production_companies: any one of the
	// listed ids appearing on the movie counts as a match. Used as a structural
	// fallback for franchises where TMDB titles drift in/out of carrying the
	// brand prefix (e.g. WWE PLEs that TMDB sometimes titles without "WWE").
	ProductionCompanyIDs []int `json:"production_company_ids,omitempty"`
	// SceneTitles, when set, override the derived scene title list. Each entry
	// is used as-is. When empty, the TMDB title is used as the base (optionally
	// prepended with "WWE " per RequireWWEPrefix).
	SceneTitles []string `json:"scene_titles,omitempty"`
	// RequireWWEPrefix prepends "WWE " to the TMDB-derived scene title when it
	// is missing. Has no effect on explicit SceneTitles entries.
	RequireWWEPrefix bool `json:"require_wwe_prefix,omitempty"`
}

// builtin is the default registry of event-organised movies.
//
// Keyword matching is intentionally tolerant - any movie whose TMDB title
// contains a known PLE event word (case-insensitive, every listed keyword must
// appear) is treated as a WWE PLE. The catch-all "wwe" entry covers PLEs not
// otherwise listed when TMDB happens to include the WWE prefix.
var builtin = []Movie{
	{Name: "WWE PLE: WrestleMania", Keywords: []string{"wrestlemania"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Royal Rumble", Keywords: []string{"royal rumble"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: SummerSlam", Keywords: []string{"summerslam"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Survivor Series", Keywords: []string{"survivor series"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Money in the Bank", Keywords: []string{"money in the bank"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Elimination Chamber", Keywords: []string{"elimination chamber"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Hell in a Cell", Keywords: []string{"hell in a cell"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Extreme Rules", Keywords: []string{"extreme rules"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Backlash", Keywords: []string{"backlash"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Crown Jewel", Keywords: []string{"crown jewel"}, RequireWWEPrefix: true},
	// More specific PLE first so "King and Queen" doesn't lose to "Queen of the Ring".
	{Name: "WWE PLE: King and Queen of the Ring", Keywords: []string{"king and queen of the ring"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: King of the Ring", Keywords: []string{"king of the ring"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Queen of the Ring", Keywords: []string{"queen of the ring"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Bash in Berlin", Keywords: []string{"bash in berlin"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Clash at the Castle", Keywords: []string{"clash at the castle"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Bad Blood", Keywords: []string{"bad blood"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Night of Champions", Keywords: []string{"night of champions"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Saturday Night's Main Event", Keywords: []string{"saturday night", "main event"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Fastlane", Keywords: []string{"fastlane"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Payback", Keywords: []string{"payback"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Vengeance", Keywords: []string{"vengeance"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: No Way Out", Keywords: []string{"no way out"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Battleground", Keywords: []string{"battleground"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Stomping Grounds", Keywords: []string{"stomping grounds"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Great American Bash", Keywords: []string{"great american bash"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Greatest Royal Rumble", Keywords: []string{"greatest royal rumble"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: TLC", Keywords: []string{"tlc"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Day 1", Keywords: []string{"wwe day 1"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Evolution", Keywords: []string{"wwe evolution"}, RequireWWEPrefix: true},
	{Name: "WWE PLE: Worlds Collide", Keywords: []string{"worlds collide"}, RequireWWEPrefix: true},
	// Catch-all by TMDB title: any movie whose TMDB title contains "wwe".
	{Name: "WWE PLE (wwe-prefixed)", Keywords: []string{"wwe"}, RequireWWEPrefix: false},
	// Catch-all by TMDB production company: any movie produced by WWE
	// (production_companies id 146598). TMDB sometimes drops the "WWE " prefix
	// from PLE titles (e.g. "Clash in Italy" 2026), so neither the specific
	// keyword entries above nor the "wwe"-keyword catch-all fire. Matching the
	// production company catches those. Listed LAST so the more-specific entries
	// still win when they match (better log label and explicit scene-title
	// overrides take effect).
	{Name: "WWE PLE (production company)", ProductionCompanyIDs: []int{146598}, RequireWWEPrefix: true},
}

// Builtin returns a copy of the default registry.
func Builtin() []Movie {
	return append([]Movie(nil), builtin...)
}

// Lookup reports whether the requested movie is event-organised. extra entries
// (from config) are checked before the built-ins so operators can override.
// Matching is by TMDB id, then IMDb id, then keyword match against the title,
// then TMDB production-company id.
func Lookup(extra []Movie, imdbID string, tmdbID int, tmdbTitle, originalTitle string, productionCompanyIDs []int) (Movie, bool) {
	name := strings.ToLower(strings.TrimSpace(tmdbTitle) + " " + strings.TrimSpace(originalTitle))
	imdb := strings.ToLower(strings.TrimSpace(imdbID))

	companySet := make(map[int]struct{}, len(productionCompanyIDs))
	for _, id := range productionCompanyIDs {
		if id > 0 {
			companySet[id] = struct{}{}
		}
	}

	candidates := make([]Movie, 0, len(extra)+len(builtin))
	candidates = append(candidates, extra...)
	candidates = append(candidates, builtin...)

	for _, movie := range candidates {
		if tmdbID > 0 {
			for _, id := range movie.TMDBIDs {
				if id == tmdbID {
					return movie, true
				}
			}
		}
		if imdb != "" {
			for _, id := range movie.IMDbIDs {
				if strings.EqualFold(strings.TrimSpace(id), imdb) {
					return movie, true
				}
			}
		}
		if len(movie.Keywords) > 0 && strings.TrimSpace(name) != "" {
			matched := true
			for _, kw := range movie.Keywords {
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
				return movie, true
			}
		}
		if len(movie.ProductionCompanyIDs) > 0 && len(companySet) > 0 {
			for _, id := range movie.ProductionCompanyIDs {
				if _, ok := companySet[id]; ok {
					return movie, true
				}
			}
		}
	}
	return Movie{}, false
}

// DeriveSceneTitles returns the scene-title variants for the given TMDB title.
// Explicit SceneTitles on the movie take precedence; otherwise the TMDB title
// is used as the base. When RequireWWEPrefix is set, a "WWE "-prefixed variant
// is added (and the un-prefixed variant retained as a fallback) so wrongly-named
// releases without the WWE prefix still pass validation.
func (m Movie) DeriveSceneTitles(tmdbTitle string) []string {
	tmdbTitle = strings.TrimSpace(tmdbTitle)
	var base []string
	if len(m.SceneTitles) > 0 {
		for _, t := range m.SceneTitles {
			if trimmed := strings.TrimSpace(t); trimmed != "" {
				base = append(base, trimmed)
			}
		}
	} else if tmdbTitle != "" {
		base = append(base, tmdbTitle)
	}
	if !m.RequireWWEPrefix {
		return uniqueLowerOrdered(base)
	}
	out := make([]string, 0, len(base)*2)
	for _, t := range base {
		if hasWWEPrefix(t) {
			out = append(out, t)
			if rest := strings.TrimSpace(t[len("WWE"):]); rest != "" {
				out = append(out, rest)
			}
		} else {
			out = append(out, "WWE "+t)
			out = append(out, t)
		}
	}
	return uniqueLowerOrdered(out)
}

func hasWWEPrefix(s string) bool {
	if len(s) < 4 {
		return false
	}
	if !strings.EqualFold(s[:3], "WWE") {
		return false
	}
	// require a separator after the prefix so "WWES..." isn't matched.
	switch s[3] {
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

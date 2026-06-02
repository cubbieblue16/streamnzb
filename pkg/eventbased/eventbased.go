// Package eventbased holds the registry of "event-organised" movies whose
// usenet releases are named by event + year/edition rather than by a single
// canonical title.
//
// WWE Premium Live Events (WrestleMania, Royal Rumble, SummerSlam, Survivor
// Series, ...) and AEW PPVs (All In, Double or Nothing, Revolution, ...) are
// the canonical cases: TMDB lists each event as its own movie entry, often
// without the brand prefix that scene releases always carry
// ("WWE.WrestleMania.40.Night.1.2024...", "AEW.WrestleDream.2025..."). The
// default movie search emits "<TMDB title> <year>" and validation's fuzzy
// title gate (which expects the requested words as a contiguous block with
// only articles before) drops any release whose parsed title leads with extra
// tokens or trails with "Night 1". Both gates fire even when the release is
// correct.
//
// For a registered event movie we instead:
//   - override the search title to the scene title(s) (with the brand prefix
//     prepended when required by the entry), and
//   - validate with token-subset matching: a release is accepted when its
//     parsed title contains every token of at least one scene title, in any
//     order, regardless of leading/trailing extras.
//
// This mirrors the pkg/datebased pattern but for the movie path. The built-in
// registry covers WWE and AEW event keywords; operators can add or override
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
	// brand prefix (e.g. WWE PLEs that TMDB sometimes titles without "WWE",
	// AEW PPVs distributed under variant names).
	ProductionCompanyIDs []int `json:"production_company_ids,omitempty"`
	// SceneTitles, when set, override the derived scene title list. Each entry
	// is used as-is. When empty, the TMDB title is used as the base (optionally
	// prepended with the brand prefix per RequirePrefix).
	SceneTitles []string `json:"scene_titles,omitempty"`
	// RequirePrefix, when non-empty, prepends "<prefix> " to the derived scene
	// title (and to explicit SceneTitles entries that don't already lead with
	// it) so releases that always carry the brand prefix still match. Common
	// values: "WWE", "AEW". Replaces the older RequireWWEPrefix boolean — JSON
	// configs using "require_wwe_prefix" must migrate to "require_prefix".
	RequirePrefix string `json:"require_prefix,omitempty"`
}

// builtin is the default registry of event-organised movies.
//
// Keyword matching is intentionally tolerant - any movie whose TMDB title
// contains a known event word (case-insensitive, every listed keyword must
// appear) is treated as a registered event. Catch-all entries by TMDB title
// prefix and by production_company id handle PPVs not otherwise listed.
var builtin = []Movie{
	// --- WWE PLEs (keyword) ---
	{Name: "WWE PLE: WrestleMania", Keywords: []string{"wrestlemania"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Royal Rumble", Keywords: []string{"royal rumble"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: SummerSlam", Keywords: []string{"summerslam"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Survivor Series", Keywords: []string{"survivor series"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Money in the Bank", Keywords: []string{"money in the bank"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Elimination Chamber", Keywords: []string{"elimination chamber"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Hell in a Cell", Keywords: []string{"hell in a cell"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Extreme Rules", Keywords: []string{"extreme rules"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Backlash", Keywords: []string{"backlash"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Crown Jewel", Keywords: []string{"crown jewel"}, RequirePrefix: "WWE"},
	// More specific PLE first so "King and Queen" doesn't lose to "Queen of the Ring".
	{Name: "WWE PLE: King and Queen of the Ring", Keywords: []string{"king and queen of the ring"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: King of the Ring", Keywords: []string{"king of the ring"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Queen of the Ring", Keywords: []string{"queen of the ring"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Bash in Berlin", Keywords: []string{"bash in berlin"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Clash at the Castle", Keywords: []string{"clash at the castle"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Bad Blood", Keywords: []string{"bad blood"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Night of Champions", Keywords: []string{"night of champions"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Saturday Night's Main Event", Keywords: []string{"saturday night", "main event"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Fastlane", Keywords: []string{"fastlane"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Payback", Keywords: []string{"payback"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Vengeance", Keywords: []string{"vengeance"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: No Way Out", Keywords: []string{"no way out"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Battleground", Keywords: []string{"battleground"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Stomping Grounds", Keywords: []string{"stomping grounds"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Great American Bash", Keywords: []string{"great american bash"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Greatest Royal Rumble", Keywords: []string{"greatest royal rumble"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: TLC", Keywords: []string{"tlc"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Day 1", Keywords: []string{"wwe day 1"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Evolution", Keywords: []string{"wwe evolution"}, RequirePrefix: "WWE"},
	{Name: "WWE PLE: Worlds Collide", Keywords: []string{"worlds collide"}, RequirePrefix: "WWE"},

	// --- AEW PPVs (keyword) ---
	// TMDB titles all carry an "AEW " prefix today (e.g. "AEW Revolution 2025"),
	// so RequirePrefix is mostly belt-and-suspenders. Keyword "aew" is required
	// alongside the event word so generic English titles ("Revolution", "Dynasty")
	// don't false-positive into AEW.
	{Name: "AEW PPV: All In", Keywords: []string{"aew", "all in"}, RequirePrefix: "AEW"},
	{Name: "AEW PPV: All Out", Keywords: []string{"aew", "all out"}, RequirePrefix: "AEW"},
	{Name: "AEW PPV: Double or Nothing", Keywords: []string{"double or nothing"}, RequirePrefix: "AEW"},
	{Name: "AEW PPV: Revolution", Keywords: []string{"aew", "revolution"}, RequirePrefix: "AEW"},
	{Name: "AEW PPV: Forbidden Door", Keywords: []string{"forbidden door"}, RequirePrefix: "AEW"},
	{Name: "AEW PPV: Full Gear", Keywords: []string{"full gear"}, RequirePrefix: "AEW"},
	{Name: "AEW PPV: WrestleDream", Keywords: []string{"wrestledream"}, RequirePrefix: "AEW"},
	{Name: "AEW PPV: Worlds End", Keywords: []string{"worlds end"}, RequirePrefix: "AEW"},
	{Name: "AEW PPV: Dynasty", Keywords: []string{"aew", "dynasty"}, RequirePrefix: "AEW"},
	{Name: "AEW PPV: Grand Slam", Keywords: []string{"aew", "grand slam"}, RequirePrefix: "AEW"},
	{Name: "AEW PPV: Beach Break", Keywords: []string{"beach break"}, RequirePrefix: "AEW"},
	{Name: "AEW PPV: Blood and Guts", Keywords: []string{"blood and guts"}, RequirePrefix: "AEW"},
	{Name: "AEW PPV: Fight for the Fallen", Keywords: []string{"fight for the fallen"}, RequirePrefix: "AEW"},
	{Name: "AEW PPV: Fyter Fest", Keywords: []string{"fyter fest"}, RequirePrefix: "AEW"},
	{Name: "AEW PPV: Winter Is Coming", Keywords: []string{"winter is coming"}, RequirePrefix: "AEW"},

	// Catch-all by TMDB title: any movie whose TMDB title contains "wwe".
	{Name: "WWE PLE (wwe-prefixed)", Keywords: []string{"wwe"}, RequirePrefix: ""},
	// Catch-all by TMDB title: any movie whose TMDB title contains "aew".
	{Name: "AEW PPV (aew-prefixed)", Keywords: []string{"aew"}, RequirePrefix: "AEW"},

	// Catch-all by TMDB production company. Listed LAST so the more-specific
	// keyword entries still win (better log label, explicit RequirePrefix).
	// WWE = id 146598. TMDB sometimes drops the "WWE " prefix from PLE titles
	// (e.g. "Clash in Italy" 2026), so neither the WWE keywords nor the
	// "wwe"-keyword catch-all fire. Matching the production company catches those.
	{Name: "WWE PLE (production company)", ProductionCompanyIDs: []int{146598}, RequirePrefix: "WWE"},
	// AEW = id 119828. Future AEW PPVs that aren't in the keyword list above
	// (or whose TMDB titles drift) still get matched via this entry.
	{Name: "AEW PPV (production company)", ProductionCompanyIDs: []int{119828}, RequirePrefix: "AEW"},
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
// is used as the base. When RequirePrefix is set, a prefix-prepended variant
// is added (and the un-prefixed variant retained as a fallback) so wrongly-named
// releases without the brand prefix still pass validation.
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
	prefix := strings.TrimSpace(m.RequirePrefix)
	if prefix == "" {
		return uniqueLowerOrdered(base)
	}
	out := make([]string, 0, len(base)*2)
	for _, t := range base {
		var prefixed, rest string
		if hasPrefix(t, prefix) {
			prefixed = t
			rest = strings.TrimSpace(t[len(prefix):])
		} else {
			prefixed = prefix + " " + t
			rest = t
		}
		out = append(out, prefixed)
		// Only emit the un-prefixed fallback when the base (minus year) is
		// multi-token. Single-token bases like "Revolution 2025" → "Revolution"
		// collide with common English words and produce false positives.
		if rest != "" && isMultiTokenAfterYear(rest) {
			out = append(out, rest)
		}
	}
	return uniqueLowerOrdered(out)
}

// isMultiTokenAfterYear reports whether s has 2+ non-year tokens.
func isMultiTokenAfterYear(s string) bool {
	count := 0
	for _, w := range strings.Fields(s) {
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

package parser

import (
	"regexp"
	"strconv"
	"strings"

	"streamnzb/pkg/core/config/pttoptions"

	"github.com/MunifTanjim/go-ptt"
)

var dashedSeasonEpisodePattern = regexp.MustCompile(`(?i)\bS(?:eason)?\s*0*([0-9]{1,2})\s*-\s*0*([0-9]{1,3})(?:$|[\s._()[\]])`)

// firstMetadataTokenPattern locates the first unambiguous release-metadata token
// (season/episode marker, 4-digit year, resolution, or video codec). None of
// these occur inside real show/movie titles, so the text preceding the first
// match is the title. Used only to reconstruct a title that go-ptt blanked out.
var firstMetadataTokenPattern = regexp.MustCompile(`(?i)\b(?:s[0-9]{1,2}(?:e[0-9]{1,3})?|[0-9]{1,2}x[0-9]{1,3}|(?:19|20)[0-9]{2}|(?:480|540|576|720|1080|1440|2160|4320)[pi]|x26[45]|h\.?26[45]|hevc|xvid|av1)\b`)

type ParsedRelease struct {
	Title      string
	Year       int
	Resolution string
	Quality    string
	Codec      string
	Audio      []string
	Channels   []string
	HDR        []string
	Container  string
	Group      string
	Season     int
	Episode    int
	Seasons    []int
	Episodes   []int

	Languages []string
	Network   string
	Repack    bool
	Proper    bool
	Extended  bool
	Unrated   bool
	ThreeD    string
	Size      string
	BitDepth  string
	Dubbed    bool
	Hardcoded bool

	Edition      string
	Date         string
	Commentary   bool
	Complete     bool
	Convert      bool
	Documentary  bool
	Remastered   bool
	Retail       bool
	Subbed       bool
	Uncensored   bool
	Upscaled     bool
	Region       string
	ReleaseTypes []string
	EpisodeCode  string
	Site         string
	Extension    string
	Volumes      []int
}

func ParseReleaseTitle(title string) *ParsedRelease {
	info := ptt.Parse(title)

	codec := pttoptions.NormalizeCodec(info.Codec)
	if codec == "" && info.Codec != "" {
		codec = info.Codec
	}

	parsed := &ParsedRelease{
		Title:        info.Title,
		Resolution:   info.Resolution,
		Quality:      info.Quality,
		Codec:        codec,
		Audio:        info.Audio,
		Channels:     info.Channels,
		HDR:          info.HDR,
		Container:    info.Container,
		Group:        info.Group,
		Languages:    info.Languages,
		Network:      info.Network,
		Repack:       info.Repack,
		Proper:       info.Proper,
		Extended:     info.Extended,
		Unrated:      info.Unrated,
		ThreeD:       info.ThreeD,
		Size:         info.Size,
		BitDepth:     info.BitDepth,
		Dubbed:       info.Dubbed,
		Hardcoded:    info.Hardcoded,
		Edition:      info.Edition,
		Date:         info.Date,
		Commentary:   info.Commentary,
		Complete:     info.Complete,
		Convert:      info.Convert,
		Documentary:  info.Documentary,
		Remastered:   info.Remastered,
		Retail:       info.Retail,
		Subbed:       info.Subbed,
		Uncensored:   info.Uncensored,
		Upscaled:     info.Upscaled,
		Region:       info.Region,
		ReleaseTypes: info.ReleaseTypes,
		EpisodeCode:  info.EpisodeCode,
		Site:         info.Site,
		Extension:    info.Extension,
		Seasons:      uniqueInts(info.Seasons),
		Episodes:     uniqueInts(info.Episodes),
		Volumes:      info.Volumes,
	}

	if info.Year != "" {
		if year, err := strconv.Atoi(info.Year); err == nil {
			parsed.Year = year
		}
	}
	if len(parsed.Seasons) > 0 {
		parsed.Season = parsed.Seasons[0]
	}
	if len(parsed.Episodes) > 0 {
		parsed.Episode = parsed.Episodes[0]
	}
	applyDashedSeasonEpisodeFallback(title, parsed)

	// Repair titles corrupted by go-ptt's bare-domain "site" handler (see
	// ReclaimTitle). Applied here so every ParseReleaseTitle caller is covered.
	parsed.Title = ReclaimTitle(title, parsed.Title, parsed.Site)

	return parsed
}

// ReclaimTitle repairs titles that go-ptt's bare-domain "site" handler corrupts.
// That handler treats any dotted token whose tail is a TLD keyword
// (party, vip, pics, tv, co, nu, ms, mx, com, org, net) as a release-site domain
// and strips it from the title — so "Word.Party.S01E02" loses its whole title and
// "The.Block.Party.Show.S01E01" loses the middle, leaving "The Show". streamnzb
// never reads the Site field, so this stripping is pure damage.
//
// The corruption signature is unambiguous: go-ptt populated Site with a *bare*
// domain (no "www"/"http" prefix — those are real site tags) whose text lies in
// the title region (before the first release-metadata token). When that holds, the
// real title is the raw name's leading segment, reconstructed by deriveFallbackTitle.
// Otherwise pttTitle is returned untouched, so every name go-ptt parses correctly —
// including genuine www/bracketed site tags like "www.1TamilMV.world" or "[eztv.re]"
// — is preserved.
func ReclaimTitle(raw, pttTitle, pttSite string) string {
	if !titleCorruptedBySite(raw, pttTitle, pttSite) {
		return pttTitle
	}
	if reclaimed := deriveFallbackTitle(raw); reclaimed != "" {
		return reclaimed
	}
	return pttTitle
}

func titleCorruptedBySite(raw, pttTitle, pttSite string) bool {
	// Whole title swallowed (e.g. "Word.Party.S01E02" -> "").
	if strings.TrimSpace(pttTitle) == "" {
		return true
	}
	site := strings.ToLower(strings.TrimSpace(pttSite))
	if site == "" || strings.HasPrefix(site, "www") || strings.HasPrefix(site, "http") {
		return false
	}
	// A bare site value that overlaps the title region means a real title word
	// was misread as a domain (partial corruption, e.g. "Greek.Co" inside
	// "My.Big.Fat.Greek.Co.S01E01"). Trailing site tags (e.g. "[eztv.re]") fall
	// after the first metadata token and are excluded.
	region := raw
	if loc := firstMetadataTokenPattern.FindStringIndex(raw); loc != nil {
		region = raw[:loc[0]]
	}
	return strings.Contains(strings.ToLower(region), site)
}

// deriveFallbackTitle returns the leading portion of a raw release name up to
// the first release-metadata token, with separators normalised to spaces. It is
// a last-resort title source for names go-ptt fails to title.
func deriveFallbackTitle(rawTitle string) string {
	head := strings.TrimSpace(rawTitle)
	if head == "" {
		return ""
	}
	if loc := firstMetadataTokenPattern.FindStringIndex(head); loc != nil {
		head = head[:loc[0]]
	}
	head = strings.Map(func(r rune) rune {
		if r == '.' || r == '_' {
			return ' '
		}
		return r
	}, head)
	return strings.Join(strings.Fields(head), " ")
}

func applyDashedSeasonEpisodeFallback(rawTitle string, parsed *ParsedRelease) {
	if parsed == nil {
		return
	}
	matches := dashedSeasonEpisodePattern.FindStringSubmatch(rawTitle)
	if len(matches) != 3 {
		return
	}
	season, seasonErr := strconv.Atoi(matches[1])
	episode, episodeErr := strconv.Atoi(matches[2])
	if seasonErr != nil || episodeErr != nil || season <= 0 || episode <= 0 {
		return
	}
	if parsed.Season == 0 {
		if !hasInt(parsed.Seasons, season) {
			parsed.Seasons = append(parsed.Seasons, season)
		}
		parsed.Season = season
	}
	if parsed.Episode == 0 {
		if !hasInt(parsed.Episodes, episode) {
			parsed.Episodes = append(parsed.Episodes, episode)
		}
		parsed.Episode = episode
	}
	if parsed.Title != "" {
		cleanedTitle := strings.TrimSpace(dashedSeasonEpisodePattern.ReplaceAllString(parsed.Title, " "))
		cleanedTitle = strings.Join(strings.Fields(cleanedTitle), " ")
		if cleanedTitle != "" {
			parsed.Title = cleanedTitle
		}
	}
}

func uniqueInts(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[int]struct{}, len(values))
	out := make([]int, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func hasInt(values []int, want int) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func (p *ParsedRelease) HasSeason(season int) bool {
	if p == nil || season <= 0 {
		return false
	}
	if hasInt(p.Seasons, season) {
		return true
	}
	return p.Season == season
}

func (p *ParsedRelease) HasEpisode(episode int) bool {
	if p == nil || episode <= 0 {
		return false
	}
	if hasInt(p.Episodes, episode) {
		return true
	}
	return p.Episode == episode
}

func (p *ParsedRelease) IsEpisodeRelease(season, episode int) bool {
	if p == nil || season <= 0 || episode <= 0 {
		return false
	}
	return p.HasSeason(season) && p.HasEpisode(episode) && len(p.Episodes) <= 1
}

func (p *ParsedRelease) IsMultiEpisodeRelease(season, episode int) bool {
	if p == nil || season <= 0 || episode <= 0 {
		return false
	}
	return p.HasSeason(season) && p.HasEpisode(episode) && len(p.Episodes) > 1
}

func (p *ParsedRelease) IsSeasonPack(season int) bool {
	if p == nil || season <= 0 {
		return false
	}
	if !p.HasSeason(season) || len(p.Episodes) > 0 {
		return false
	}
	return !p.IsShowPack()
}

func (p *ParsedRelease) IsShowPack() bool {
	if p == nil || !p.Complete || len(p.Episodes) > 0 {
		return false
	}
	return len(p.Seasons) == 0 || len(p.Seasons) > 1
}

func (p *ParsedRelease) EpisodeMatchRank(season, episode int) int {
	if p == nil || season <= 0 || episode <= 0 {
		return 0
	}
	if p.IsEpisodeRelease(season, episode) {
		return 4
	}
	if p.IsMultiEpisodeRelease(season, episode) {
		return 3
	}
	if p.IsSeasonPack(season) {
		return 2
	}
	if p.IsShowPack() {
		return 1
	}
	return 0
}

func (p *ParsedRelease) MatchesEpisodeRequest(season, episode int) bool {
	return p.EpisodeMatchRank(season, episode) > 0
}

func (p *ParsedRelease) ResolutionGroup() string {
	if p == nil {
		return "sd"
	}
	res := strings.ToLower(p.Resolution)
	if strings.Contains(res, "2160") || strings.Contains(res, "4k") {
		return "4k"
	}
	if strings.Contains(res, "1080") {
		return "1080p"
	}
	if strings.Contains(res, "720") {
		return "720p"
	}
	return "sd"
}

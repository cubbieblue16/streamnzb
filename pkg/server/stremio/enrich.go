package stremio

import (
	"fmt"
	"strings"
	"time"

	"streamnzb/pkg/release"
	"streamnzb/pkg/search/parser"
)

// Stream label enrichment (feature: stream_label_format = "detailed").
//
// These helpers turn the bare "StreamNZB" row into a glanceable label that
// distinguishes releases by resolution / quality / codec / HDR / audio / size /
// age / group / availability. Everything is null-safe: meta and rel may be nil
// and every field is optional, so a release with no parsed metadata degrades to
// the plain brand name rather than emitting empty decorations.

// resolutionTag returns a short display resolution like "2160p" / "1080p".
// Falls back to the coarse resolution group when the exact value is absent.
func resolutionTag(meta *parser.ParsedRelease) string {
	if meta == nil {
		return ""
	}
	if r := strings.TrimSpace(meta.Resolution); r != "" {
		return r
	}
	switch meta.ResolutionGroup() {
	case "4k":
		return "2160p"
	case "1080p":
		return "1080p"
	case "720p":
		return "720p"
	}
	return ""
}

// normalizeQuality collapses source spellings to compact display forms.
func normalizeQuality(quality string) string {
	q := strings.TrimSpace(quality)
	q = strings.ReplaceAll(q, "Blu-ray", "BluRay")
	q = strings.ReplaceAll(q, "WEB-DL", "WEB")
	return q
}

// normalizeCodec compacts codec spellings (e.g. "H.265" -> "HEVC").
func normalizeCodec(codec string) string {
	c := strings.ToUpper(strings.TrimSpace(codec))
	c = strings.ReplaceAll(c, "H.265", "HEVC")
	c = strings.ReplaceAll(c, "H.264", "AVC")
	c = strings.ReplaceAll(c, "X265", "HEVC")
	c = strings.ReplaceAll(c, "X264", "AVC")
	return c
}

// buildEnrichedStreamName builds the bold left-hand label: the brand, an
// availability bolt when cached, and a resolution/quality tag on a second line.
func buildEnrichedStreamName(brand string, meta *parser.ParsedRelease, isAvail bool) string {
	label := brand
	if isAvail {
		label = "⚡ " + brand
	}
	tag := resolutionTag(meta)
	if tag == "" && meta != nil {
		tag = normalizeQuality(meta.Quality)
	}
	if tag != "" {
		return label + "\n" + tag
	}
	return label
}

// humanAgeFrom renders a compact age ("3d", "5mo", "2y") from an RSS pubDate
// relative to now. Returns "" when the date is missing or unparseable. now is a
// parameter so the formatting is deterministic under test.
func humanAgeFrom(pubDate string, now time.Time) string {
	t, ok := parsePubDate(pubDate)
	if !ok {
		return ""
	}
	d := now.Sub(t)
	if d < 0 {
		return ""
	}
	switch {
	case d < time.Hour:
		m := int(d.Minutes())
		if m <= 0 {
			return "now"
		}
		return fmt.Sprintf("%dm", m)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/(24*365)))
	}
}

// parsePubDate parses the common RSS / newznab date layouts.
func parsePubDate(pubDate string) (time.Time, bool) {
	s := strings.TrimSpace(pubDate)
	if s == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC1123Z,
		time.RFC1123,
		time.RFC3339,
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// sizeTag renders a release size as "45.3 GB" / "780 MB". Returns "" for <= 0.
func sizeTag(sizeBytes int64) string {
	if sizeBytes <= 0 {
		return ""
	}
	const (
		kb = 1024.0
		mb = kb * 1024
		gb = mb * 1024
	)
	f := float64(sizeBytes)
	switch {
	case f >= gb:
		return fmt.Sprintf("%.2f GB", f/gb)
	case f >= mb:
		return fmt.Sprintf("%.0f MB", f/mb)
	default:
		return fmt.Sprintf("%.0f KB", f/kb)
	}
}

// buildEnrichedStreamDescription builds the multi-line detail block shown under
// the stream name. Lines are emitted only when their data is present.
func buildEnrichedStreamDescription(meta *parser.ParsedRelease, rel *release.Release, isAvail bool, indexerName string, now time.Time) string {
	lines := make([]string, 0, 6)

	// Line 1: resolution • quality • codec
	tech := make([]string, 0, 3)
	if r := resolutionTag(meta); r != "" {
		tech = append(tech, r)
	}
	if meta != nil {
		if q := normalizeQuality(meta.Quality); q != "" {
			tech = append(tech, q)
		}
		if c := normalizeCodec(meta.Codec); c != "" {
			tech = append(tech, c)
		}
	}
	if len(tech) > 0 {
		lines = append(lines, "🎬 "+strings.Join(tech, " • "))
	}

	// Line 2: HDR • audio + channels
	if meta != nil {
		av := make([]string, 0, 2)
		if len(meta.HDR) > 0 {
			av = append(av, strings.Join(meta.HDR, " "))
		}
		if len(meta.Audio) > 0 {
			audio := meta.Audio[0]
			if len(meta.Channels) > 0 {
				audio = audio + " " + meta.Channels[0]
			}
			av = append(av, audio)
		}
		if len(av) > 0 {
			lines = append(lines, "🎧 "+strings.Join(av, " • "))
		}
	}

	// Line 3: size • age • group
	info := make([]string, 0, 3)
	if rel != nil {
		if s := sizeTag(rel.Size); s != "" {
			info = append(info, "💾 "+s)
		}
		if rel != nil {
			if age := humanAgeFrom(rel.PubDate, now); age != "" {
				info = append(info, "🕒 "+age)
			}
		}
	}
	if meta != nil && strings.TrimSpace(meta.Group) != "" {
		info = append(info, "👥 "+meta.Group)
	}
	if len(info) > 0 {
		lines = append(lines, strings.Join(info, " • "))
	}

	// Line 4: availability
	if isAvail {
		lines = append(lines, "⚡ Available")
	}

	// Line 5: raw release title (the ground truth the user can eyeball)
	if rel != nil && strings.TrimSpace(rel.Title) != "" {
		lines = append(lines, rel.Title)
	}

	// Line 6: indexer source
	if n := strings.TrimSpace(indexerName); n != "" {
		lines = append(lines, "🔍 "+n)
	}

	if len(lines) == 0 {
		return "StreamNZB"
	}
	return strings.Join(lines, "\n")
}

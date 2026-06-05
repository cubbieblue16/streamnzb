package search

import (
	"testing"

	"streamnzb/pkg/release"
	"streamnzb/pkg/search/parser"
)

// Reproduction for the "Word Party" (and similar generic shows) not-found bug.
// These are the real release titles NzbGeek returns for tvdbid=315008 S01E02.
func TestWordPartyReproduction(t *testing.T) {
	titles := []string{
		"Word.Party.S01E02.Down.on.the.Farm.1080p.WEBRip.10bit.EAC3.5.1.x265-iVy",
		"Word.Party.S01E02.Down.on.the.Farm.EAC3.5.1.1080p.WEBRip.x265-iVy",
		"Word.Party.S01E02.720p.WEB.x264-MEMENTO-Obfuscated",
	}

	for _, ti := range titles {
		p := parser.ParseReleaseTitle(ti)
		t.Logf("RAW=%q -> parsed.Title=%q seasons=%v episodes=%v year=%d", ti, p.Title, p.Seasons, p.Episodes, p.Year)
		t.Logf("   NormalizeTitleForDedup(parsed.Title)=%q  expectDedup=%q",
			release.NormalizeTitleForDedup(p.Title), release.NormalizeTitleForDedup("Word Party"))
		t.Logf("   normalizedTitleMatches(\"word party\", parsed.Title)=%v", normalizedTitleMatches("word party", p.Title))
		t.Logf("   titleWordsForMatch(parsed.Title)=%v", titleWordsForMatch(p.Title))
	}

	rels := make([]*release.Release, 0, len(titles))
	for _, ti := range titles {
		rels = append(rels, &release.Release{Title: ti})
	}
	// Mirrors the live id-mode call: validation_query="Word Party", season=1, episode=2.
	_, stats := ValidateSearchResultsWithStats(rels, "series", "Word Party", "1", "2", true, false)
	t.Logf("STATS raw=%d final=%d droppedTitle=%d droppedEpisode=%d droppedSeason=%d expectTitle=%q",
		stats.RawResults, stats.FinalResults, stats.DroppedTitle, stats.DroppedEpisodeRequest, stats.DroppedSeason, stats.ExpectedTitle)
	if stats.FinalResults == 0 {
		t.Fatalf("BUG REPRODUCED: all %d Word Party releases dropped (droppedTitle=%d)", stats.RawResults, stats.DroppedTitle)
	}
}

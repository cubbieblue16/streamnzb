package parser

import "testing"

// Regression: go-ptt's "site" handler treats a dotted token whose second word
// is a TLD keyword (party, vip, pics, tv, co, ...) as a release-site domain and
// strips it from the title, e.g. "Word.Party.S01E02..." -> Title="" Site="Word.Party".
// ParseReleaseTitle must reclaim the real title in that case, without disturbing
// release names PTT already parses correctly.
func TestParseReleaseTitleReclaimsFalseSiteTLD(t *testing.T) {
	cases := []struct {
		raw       string
		wantTitle string
		wantS     int
		wantE     int
	}{
		// Previously broken: TLD-keyword final word -> PTT blanks the whole title.
		{"Word.Party.S01E02.720p.WEB.x264-MEMENTO", "Word Party", 1, 2},
		{"Word.Party.S01E02.Down.on.the.Farm.1080p.WEBRip.10bit.EAC3.5.1.x265-iVy", "Word Party", 1, 2},
		{"Word.Party.2016.S01E02.1080p.WEBRip.x265-iVy", "Word Party", 1, 2},
		{"Search.Party.S03E01.1080p.WEB.x264-GROUP", "Search Party", 3, 1},
		{"Block.Party.S01E05.720p.HDTV.x264-LOL", "Block Party", 1, 5},
		{"Comedy.tv.S01E01.720p.WEB.x264-Z", "Comedy tv", 1, 1},
		// Previously broken (partial): TLD-keyword token mid-title -> PTT keeps a
		// truncated, wrong title. The empty-only guard missed these.
		{"The.Block.Party.Show.S01E01.720p.WEB.x264-GROUP", "The Block Party Show", 1, 1},
		{"Live.From.The.Pics.Studio.S02E03.1080p.WEB.x264-X", "Live From The Pics Studio", 2, 3},
		{"My.Big.Fat.Greek.Co.S01E01.720p.HDTV.x264-Y", "My Big Fat Greek Co", 1, 1},
		// Controls: PTT already parses these; reclamation must not alter them.
		{"The.Office.S01E02.720p.HDTV.x264-LOL", "The Office", 1, 2},
		{"Party.Down.S01E02.720p.HDTV.x264-LOL", "Party Down", 1, 2},
		{"Party.Monster.2003.1080p.BluRay.x264-GROUP", "Party Monster", 0, 0},
		{"Breaking.Bad.S01E01.1080p.BluRay.x264-GROUP", "Breaking Bad", 1, 1},
		// Real site tags: genuine www/bracketed sites must NOT trigger reclamation.
		{"The.Expanse.S05E02.1080p.AMZN.WEB.DDP5.1.x264-NTb[eztv.re].mp4", "The Expanse", 5, 2},
	}
	for _, tc := range cases {
		p := ParseReleaseTitle(tc.raw)
		if p.Title != tc.wantTitle {
			t.Errorf("%q: Title = %q, want %q", tc.raw, p.Title, tc.wantTitle)
		}
		if tc.wantS > 0 && (!p.HasSeason(tc.wantS) || !p.HasEpisode(tc.wantE)) {
			t.Errorf("%q: season/episode = %v/%v, want %d/%d", tc.raw, p.Seasons, p.Episodes, tc.wantS, tc.wantE)
		}
	}
}

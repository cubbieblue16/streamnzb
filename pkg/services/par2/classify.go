// Package par2 performs last-resort PAR2 download-repair-serve for Usenet
// releases whose data files are incomplete or corrupt. It is decoupled from the
// nzb/usenet/loader packages on purpose: it operates only on filenames, a
// working directory on disk, and the external `par2` binary, so its logic can be
// unit-tested with a fake binary and fixture files. The feature is gated OFF by
// default (see config par2_enable); a nil or disabled *Service is a safe no-op.
package par2

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Ext is the PAR2 file extension (lower-case, with leading dot).
const Ext = ".par2"

// recoveryVolumePattern matches PAR2 recovery-volume files, e.g.
// "release.vol000+01.par2" or "release.vol12-34.par2". The index file
// ("release.par2") has no such .volNNN(+|-)NN. segment.
var recoveryVolumePattern = regexp.MustCompile(`(?i)\.vol\d+[+-]\d+\.par2$`)

// IsPar2File reports whether name has a .par2 extension (case-insensitive).
// name may be a full path; only the base extension is considered.
func IsPar2File(name string) bool {
	return strings.EqualFold(filepath.Ext(strings.TrimSpace(name)), Ext)
}

// IsRecoveryVolume reports whether name is a PAR2 recovery-volume file (carries
// recovery blocks) as opposed to the index file.
func IsRecoveryVolume(name string) bool {
	return recoveryVolumePattern.MatchString(strings.TrimSpace(name))
}

// Classification splits a fileset into PAR2 files and the data files they
// protect. Names are preserved verbatim (no path stripping) so callers can map
// them back to on-disk entries.
type Classification struct {
	Par2Files []string
	DataFiles []string
}

// Classify partitions filenames into PAR2 files and data files. Order within
// each slice follows the input order.
func Classify(filenames []string) Classification {
	c := Classification{}
	for _, name := range filenames {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if IsPar2File(name) {
			c.Par2Files = append(c.Par2Files, name)
		} else {
			c.DataFiles = append(c.DataFiles, name)
		}
	}
	return c
}

// HasRecovery reports whether the fileset carries any PAR2 file (and is
// therefore a candidate for repair).
func (c Classification) HasRecovery() bool {
	return len(c.Par2Files) > 0
}

// MainPar2 returns the best PAR2 file to hand to `par2 repair`. par2cmdline
// discovers sibling recovery volumes automatically given any file in the set,
// but the index file (no .volNNN+NN. segment) is the canonical entry point and
// is the smallest, so prefer it. Falls back to the shortest-named recovery
// volume when only volumes are present, and "" when there are no PAR2 files.
func (c Classification) MainPar2() string {
	if len(c.Par2Files) == 0 {
		return ""
	}
	indexes := make([]string, 0, len(c.Par2Files))
	for _, p := range c.Par2Files {
		if !IsRecoveryVolume(p) {
			indexes = append(indexes, p)
		}
	}
	pick := func(candidates []string) string {
		best := candidates[0]
		for _, name := range candidates[1:] {
			if shorterBase(name, best) {
				best = name
			}
		}
		return best
	}
	if len(indexes) > 0 {
		return pick(indexes)
	}
	// Only recovery volumes present: pick deterministically by shortest base.
	vols := append([]string(nil), c.Par2Files...)
	return pick(vols)
}

// shorterBase reports whether a's base name should sort before b's: shorter base
// first, then lexicographic for stability.
func shorterBase(a, b string) bool {
	ba, bb := filepath.Base(a), filepath.Base(b)
	if len(ba) != len(bb) {
		return len(ba) < len(bb)
	}
	return ba < bb
}

// sortedBaseNames returns the unique base names of files, sorted, for stable
// logging/diagnostics.
func sortedBaseNames(files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, filepath.Base(f))
	}
	sort.Strings(out)
	return out
}

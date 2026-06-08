package stremio

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"

	"streamnzb/pkg/core/logger"
	"streamnzb/pkg/session"
)

// sessionFilesetDownloader adapts the session manager's full-fileset
// materialization to the par2.Downloader interface. It writes the session's NZB
// data files plus .par2 recovery files into the repair work dir.
type sessionFilesetDownloader struct {
	mgr  *session.Manager
	sess *session.Session
}

func (d sessionFilesetDownloader) Download(ctx context.Context, workDir string) ([]string, error) {
	return d.mgr.MaterializeFilesetToDir(ctx, d.sess, workDir)
}

// cleanupReadSeekCloser serves a repaired file from disk and removes the repair
// scratch directory exactly once when the stream is closed.
type cleanupReadSeekCloser struct {
	io.ReadSeekCloser
	cleanup func()
	once    sync.Once
}

func (c *cleanupReadSeekCloser) Close() error {
	err := c.ReadSeekCloser.Close()
	if c.cleanup != nil {
		c.once.Do(c.cleanup)
	}
	return err
}

// attemptPar2Repair is the gated last-resort fallback invoked once normal
// playback slots are exhausted. When PAR2 repair is enabled and the release
// carries recovery files, it downloads the full fileset to a scratch dir,
// repairs it with the `par2` binary, and returns a seekable reader over the
// recovered media file (whose Close removes the scratch dir). When the feature
// is disabled (the default) it returns ok=false immediately, so the playback
// path behaves exactly as before. It never returns an error: any failure is a
// logged no-op, because this runs after the release has already failed.
func (s *Server) attemptPar2Repair(ctx context.Context, sess *session.Session) (stream io.ReadSeekCloser, name string, size int64, ok bool) {
	if s.par2 == nil || !s.par2.Enabled() || sess == nil {
		return nil, "", 0, false
	}
	sessionID := sess.ID

	// Ensure the NZB is loaded so we can inspect/size the fileset.
	if _, err := sess.GetOrDownloadNZBWithContext(ctx, s.sessionManager); err != nil {
		logger.Warn("PAR2 fallback: NZB load failed", "session", sessionID, "err", err)
		return nil, "", 0, false
	}

	// Pre-download disk-budget check: skip large filesets before spending
	// bandwidth (Repair re-checks the on-disk total as well).
	total := sess.FilesetTotalBytes()
	if total > 0 && !s.par2.WithinBudget(total) {
		logger.Info("PAR2 fallback skipped: fileset over disk cap",
			"session", sessionID, "fileset_bytes", total)
		return nil, "", 0, false
	}

	dl := sessionFilesetDownloader{mgr: s.sessionManager, sess: sess}
	path, cleanup, err := s.par2.DownloadRepairLocate(ctx, sessionID, dl)
	if err != nil {
		logger.Info("PAR2 fallback did not recover release", "session", sessionID, "err", err)
		return nil, "", 0, false
	}

	f, err := os.Open(path)
	if err != nil {
		logger.Warn("PAR2 fallback: open repaired file failed", "session", sessionID, "err", err)
		cleanup()
		return nil, "", 0, false
	}
	info, err := f.Stat()
	if err != nil {
		logger.Warn("PAR2 fallback: stat repaired file failed", "session", sessionID, "err", err)
		_ = f.Close()
		cleanup()
		return nil, "", 0, false
	}

	logger.Info("PAR2 fallback recovered release",
		"session", sessionID,
		"file", filepath.Base(path),
		"size", info.Size())
	return &cleanupReadSeekCloser{ReadSeekCloser: f, cleanup: cleanup}, filepath.Base(path), info.Size(), true
}

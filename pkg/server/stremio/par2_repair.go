package stremio

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"

	"streamnzb/pkg/core/logger"
	"streamnzb/pkg/services/par2"
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

// filesetDownloaderFor returns the par2.Downloader for a session's fileset. The
// production path materializes from the session manager; tests override via
// newFilesetDownloader.
func (s *Server) filesetDownloaderFor(sess *session.Session) par2.Downloader {
	if s.newFilesetDownloader != nil {
		return s.newFilesetDownloader(sess)
	}
	return sessionFilesetDownloader{mgr: s.sessionManager, sess: sess}
}

// attemptLastResortRepair runs the PAR2 fallback while the session is still
// live, then tears the session down regardless of outcome.
//
// Ordering is the whole point: DeleteSession closes the session and nils its
// NZB, so it MUST run AFTER attemptPar2Repair. The previous code deleted first,
// which guaranteed the repair's NZB load failed every time — the feature was
// dead code.
func (s *Server) attemptLastResortRepair(ctx context.Context, sess *session.Session, sessionID string) (io.ReadSeekCloser, string, int64, bool) {
	stream, name, size, ok := s.attemptPar2Repair(ctx, sess)
	s.sessionManager.DeleteSession(sessionID)
	return stream, name, size, ok
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

	dl := s.filesetDownloaderFor(sess)
	path, cleanup, err := s.par2.DownloadRepairLocateShared(ctx, sessionID, dl)
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

package stremio

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"streamnzb/pkg/core/logger"
	"streamnzb/pkg/media/nzb"
	"streamnzb/pkg/services/par2"
	"streamnzb/pkg/session"
)

// TestAttemptLastResortRepairRunsBeforeDeletingSession is the regression test
// for the PAR2 dead-code bug: the playback fallback deleted (and therefore
// closed, niling its NZB) the session BEFORE attempting the repair, so the
// repair's NZB load always failed and the feature never fired. The repair must
// run while the session is still live; the session is torn down only afterward.
func TestAttemptLastResortRepairRunsBeforeDeletingSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake par2 shell script unsupported on windows")
	}
	logger.Init("ERROR")

	manager := session.NewManager(nil, nil, time.Minute)
	t.Cleanup(manager.Shutdown)

	const sessionID = "stream_test:movie:tt123:0"
	nzbData := &nzb.NZB{Files: []nzb.File{{
		Subject:  "movie.mkv",
		Segments: []nzb.Segment{{ID: "<a>", Bytes: 10}},
	}}}
	if _, err := manager.CreateSession(sessionID, nzbData, nil, nil); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess, err := manager.GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	par2bin := writeFakePar2Binary(t, "#!/bin/sh\necho ok\nexit 0\n")
	svc := par2.New(par2.Config{Enabled: true, BinaryPath: par2bin, WorkDir: t.TempDir()})

	server := &Server{
		sessionManager: manager,
		par2:           svc,
		// Inject a fileset downloader so the repair can complete without a real
		// Usenet download — the bug under test is the ordering, not the transfer.
		newFilesetDownloader: func(*session.Session) par2.Downloader {
			return fixedFilesetDownloader{files: map[string]string{
				"movie.mkv":  "the recovered movie bytes",
				"movie.par2": "index",
			}}
		},
	}

	stream, name, size, ok := server.attemptLastResortRepair(context.Background(), sess, sessionID)
	if !ok {
		t.Fatal("repair returned ok=false: the session was already closed (NZB niled) before repair ran")
	}
	if stream == nil {
		t.Fatal("repair returned ok=true but nil stream")
	}
	t.Cleanup(func() { _ = stream.Close() })
	if name != "movie.mkv" {
		t.Errorf("served name = %q, want movie.mkv", name)
	}
	if size <= 0 {
		t.Errorf("served size = %d, want > 0", size)
	}

	// The session must be torn down once the repair has been attempted.
	if _, err := manager.GetSession(sessionID); err == nil {
		t.Error("session still present after repair; it should be deleted")
	}
}

// fixedFilesetDownloader implements par2.Downloader by writing a fixed set of
// files into the repair work dir — standing in for the real session-backed
// Usenet materializer.
type fixedFilesetDownloader struct {
	files map[string]string
}

func (d fixedFilesetDownloader) Download(_ context.Context, workDir string) ([]string, error) {
	var names []string
	for name, content := range d.files {
		if err := os.WriteFile(filepath.Join(workDir, name), []byte(content), 0o644); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, nil
}

func writeFakePar2Binary(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-par2")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake par2: %v", err)
	}
	return path
}

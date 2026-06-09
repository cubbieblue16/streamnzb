package par2

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsPar2File(t *testing.T) {
	cases := map[string]bool{
		"release.par2":           true,
		"release.PAR2":           true,
		"release.vol000+01.par2": true,
		"/tmp/a/b/movie.PaR2":    true,
		"movie.mkv":              false,
		"archive.rar":            false,
		"par2":                   false,
		"trailing.par2.mkv":      false,
		"":                       false,
		"   ":                    false,
	}
	for name, want := range cases {
		if got := IsPar2File(name); got != want {
			t.Errorf("IsPar2File(%q)=%v want %v", name, got, want)
		}
	}
}

func TestIsRecoveryVolume(t *testing.T) {
	cases := map[string]bool{
		"release.vol000+01.par2": true,
		"release.vol12+34.par2":  true,
		"release.vol5-10.par2":   true,
		"RELEASE.VOL00+01.PAR2":  true,
		"release.par2":           false, // index file
		"release.mkv":            false,
		"release.vol.par2":       false, // no digits
	}
	for name, want := range cases {
		if got := IsRecoveryVolume(name); got != want {
			t.Errorf("IsRecoveryVolume(%q)=%v want %v", name, got, want)
		}
	}
}

func TestClassify(t *testing.T) {
	in := []string{
		"movie.mkv",
		"movie.par2",
		"movie.vol000+01.par2",
		"movie.nfo",
		"",
		"   ",
		"movie.vol001+02.PAR2",
	}
	c := Classify(in)
	if len(c.DataFiles) != 2 {
		t.Fatalf("data files = %v, want 2", c.DataFiles)
	}
	if len(c.Par2Files) != 3 {
		t.Fatalf("par2 files = %v, want 3", c.Par2Files)
	}
	if !c.HasRecovery() {
		t.Error("HasRecovery() = false, want true")
	}
}

func TestClassifyNoRecovery(t *testing.T) {
	c := Classify([]string{"a.mkv", "b.nfo"})
	if c.HasRecovery() {
		t.Error("HasRecovery() = true, want false")
	}
	if c.MainPar2() != "" {
		t.Errorf("MainPar2() = %q, want empty", c.MainPar2())
	}
}

func TestMainPar2PrefersIndex(t *testing.T) {
	c := Classify([]string{
		"show.vol000+01.par2",
		"show.vol001+02.par2",
		"show.par2", // index — must be chosen even though listed last
		"show.mkv",
	})
	if got := c.MainPar2(); got != "show.par2" {
		t.Errorf("MainPar2() = %q, want show.par2", got)
	}
}

func TestMainPar2VolumesOnlyFallback(t *testing.T) {
	c := Classify([]string{
		"show.vol010+10.par2",
		"show.vol000+01.par2",
	})
	// No index present: pick the shortest/lowest base deterministically.
	if got := c.MainPar2(); got != "show.vol000+01.par2" {
		t.Errorf("MainPar2() = %q, want show.vol000+01.par2", got)
	}
}

func TestServiceNilAndDisabledAreNoop(t *testing.T) {
	var nilSvc *Service
	if nilSvc.Enabled() {
		t.Error("nil service Enabled() = true")
	}
	if _, err := nilSvc.Repair(context.Background(), t.TempDir()); err != ErrDisabled {
		t.Errorf("nil Repair err = %v, want ErrDisabled", err)
	}

	off := New(Config{Enabled: false})
	if off.Enabled() {
		t.Error("disabled service Enabled() = true")
	}
	if _, err := off.Repair(context.Background(), t.TempDir()); err != ErrDisabled {
		t.Errorf("disabled Repair err = %v, want ErrDisabled", err)
	}
	if _, _, err := off.DownloadRepairLocate(context.Background(), "s", stubDownloader{}); err != ErrDisabled {
		t.Errorf("disabled DownloadRepairLocate err = %v, want ErrDisabled", err)
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	s := New(Config{Enabled: true})
	if s.cfg.BinaryPath != DefaultBinaryPath {
		t.Errorf("BinaryPath = %q, want %q", s.cfg.BinaryPath, DefaultBinaryPath)
	}
	if s.cfg.MaxConcurrent != DefaultMaxConcurrent {
		t.Errorf("MaxConcurrent = %d, want %d", s.cfg.MaxConcurrent, DefaultMaxConcurrent)
	}
	if s.cfg.Timeout != DefaultTimeout {
		t.Errorf("Timeout = %v, want %v", s.cfg.Timeout, DefaultTimeout)
	}
	if cap(s.sem) != DefaultMaxConcurrent {
		t.Errorf("sem cap = %d, want %d", cap(s.sem), DefaultMaxConcurrent)
	}
}

func TestWithinBudget(t *testing.T) {
	unlimited := New(Config{Enabled: true, MaxBytes: 0})
	if !unlimited.WithinBudget(1 << 40) {
		t.Error("unlimited.WithinBudget(1TB) = false")
	}
	capped := New(Config{Enabled: true, MaxBytes: 1000})
	if !capped.WithinBudget(1000) {
		t.Error("capped.WithinBudget(1000) = false, want true (boundary)")
	}
	if capped.WithinBudget(1001) {
		t.Error("capped.WithinBudget(1001) = true, want false")
	}
	var nilSvc *Service
	if nilSvc.WithinBudget(0) {
		t.Error("nil.WithinBudget = true")
	}
}

func TestRepairSuccess(t *testing.T) {
	skipNoShell(t)
	dir := t.TempDir()
	writeFile(t, dir, "movie.mkv", "video-bytes")
	writeFile(t, dir, "movie.par2", "index")
	writeFile(t, dir, "movie.vol000+01.par2", "recovery")

	bin := writeFakePar2(t, "#!/bin/sh\necho \"Repairing\"\necho \"Repair complete.\"\nexit 0\n")
	s := New(Config{Enabled: true, BinaryPath: bin})

	res, err := s.Repair(context.Background(), dir)
	if err != nil {
		t.Fatalf("Repair err = %v", err)
	}
	if !res.Repaired {
		t.Error("Repaired = false")
	}
	if res.AlreadyComplete {
		t.Error("AlreadyComplete = true, want false")
	}
	if res.MainPar2 != "movie.par2" {
		t.Errorf("MainPar2 = %q, want movie.par2", res.MainPar2)
	}
}

func TestRepairAlreadyComplete(t *testing.T) {
	skipNoShell(t)
	dir := t.TempDir()
	writeFile(t, dir, "movie.mkv", "video-bytes")
	writeFile(t, dir, "movie.par2", "index")

	bin := writeFakePar2(t, "#!/bin/sh\necho \"All files are correct, repair is not required.\"\nexit 0\n")
	s := New(Config{Enabled: true, BinaryPath: bin})

	res, err := s.Repair(context.Background(), dir)
	if err != nil {
		t.Fatalf("Repair err = %v", err)
	}
	if !res.AlreadyComplete {
		t.Error("AlreadyComplete = false, want true")
	}
}

func TestRepairNoRecovery(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "movie.mkv", "video-bytes")
	s := New(Config{Enabled: true, BinaryPath: "/nonexistent/par2"})
	if _, err := s.Repair(context.Background(), dir); err != ErrNoRecovery {
		t.Errorf("Repair err = %v, want ErrNoRecovery", err)
	}
}

func TestRepairOverBudget(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "movie.mkv", strings.Repeat("x", 5000))
	writeFile(t, dir, "movie.par2", "index")
	s := New(Config{Enabled: true, BinaryPath: "/nonexistent/par2", MaxBytes: 100})
	_, err := s.Repair(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), "disk cap") {
		t.Errorf("Repair err = %v, want over-budget", err)
	}
}

func TestRepairBinaryFailure(t *testing.T) {
	skipNoShell(t)
	dir := t.TempDir()
	writeFile(t, dir, "movie.mkv", "video-bytes")
	writeFile(t, dir, "movie.par2", "index")
	bin := writeFakePar2(t, "#!/bin/sh\necho \"Repair is not possible.\"\nexit 1\n")
	s := New(Config{Enabled: true, BinaryPath: bin})

	res, err := s.Repair(context.Background(), dir)
	if err == nil {
		t.Fatal("Repair err = nil, want failure")
	}
	if res == nil || res.Repaired {
		t.Errorf("res = %+v, want non-nil with Repaired=false", res)
	}
	if !strings.Contains(err.Error(), "repair failed") {
		t.Errorf("err = %v, want 'repair failed'", err)
	}
}

func TestRepairTimeout(t *testing.T) {
	skipNoShell(t)
	dir := t.TempDir()
	writeFile(t, dir, "movie.mkv", "video-bytes")
	writeFile(t, dir, "movie.par2", "index")
	bin := writeFakePar2(t, "#!/bin/sh\nsleep 30\nexit 0\n")
	s := New(Config{Enabled: true, BinaryPath: bin, Timeout: 100 * time.Millisecond})

	start := time.Now()
	_, err := s.Repair(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("Repair err = %v, want timeout", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("timeout took too long: %v", time.Since(start))
	}
}

func TestRepairConcurrencyLimited(t *testing.T) {
	skipNoShell(t)
	counter := filepath.Join(t.TempDir(), "concurrency.log")
	// Each invocation records enter/exit around a short sleep; with
	// MaxConcurrent=1 the semaphore must serialize them.
	body := "#!/bin/sh\n" +
		"echo enter >> " + shQuote(counter) + "\n" +
		"sleep 0.2\n" +
		"echo exit >> " + shQuote(counter) + "\n" +
		"exit 0\n"
	bin := writeFakePar2(t, body)
	s := New(Config{Enabled: true, BinaryPath: bin, MaxConcurrent: 1})

	const n = 3
	dirs := make([]string, n)
	for i := range dirs {
		d := t.TempDir()
		writeFile(t, d, "movie.mkv", "v")
		writeFile(t, d, "movie.par2", "index")
		dirs[i] = d
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			if _, err := s.Repair(context.Background(), d); err != nil {
				t.Errorf("Repair err = %v", err)
			}
		}(dirs[i])
	}
	wg.Wait()

	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	cur, max := 0, 0
	for _, line := range strings.Fields(string(data)) {
		switch line {
		case "enter":
			cur++
			if cur > max {
				max = cur
			}
		case "exit":
			cur--
		}
	}
	if max > 1 {
		t.Errorf("max concurrent par2 invocations = %d, want <= 1 (sem broken)", max)
	}
}

func TestLocatePlayable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "small.mkv", strings.Repeat("a", 100))
	writeFile(t, dir, "big.mkv", strings.Repeat("b", 5000))
	writeFile(t, dir, "movie.par2", "index")
	writeFile(t, dir, "notes.nfo", "text")
	s := New(Config{Enabled: true})

	got, err := s.LocatePlayable(dir)
	if err != nil {
		t.Fatalf("LocatePlayable err = %v", err)
	}
	if filepath.Base(got) != "big.mkv" {
		t.Errorf("LocatePlayable = %q, want big.mkv (largest video)", got)
	}
}

func TestLocatePlayableNone(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "movie.par2", "index")
	writeFile(t, dir, "notes.nfo", "text")
	s := New(Config{Enabled: true})
	if _, err := s.LocatePlayable(dir); err != ErrNoPlayable {
		t.Errorf("LocatePlayable err = %v, want ErrNoPlayable", err)
	}
}

func TestDownloadRepairLocateSuccess(t *testing.T) {
	skipNoShell(t)
	bin := writeFakePar2(t, "#!/bin/sh\necho ok\nexit 0\n")
	s := New(Config{Enabled: true, BinaryPath: bin, WorkDir: t.TempDir()})

	dl := stubDownloader{files: map[string]string{
		"movie.mkv":  strings.Repeat("v", 4000),
		"movie.par2": "index",
	}}

	path, cleanup, err := s.DownloadRepairLocate(context.Background(), "sess-123", dl)
	if err != nil {
		t.Fatalf("DownloadRepairLocate err = %v", err)
	}
	defer cleanup()
	if filepath.Base(path) != "movie.mkv" {
		t.Errorf("playable = %q, want movie.mkv", path)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("playable file not present: %v", statErr)
	}

	// cleanup removes the scratch dir.
	cleanup()
	if _, statErr := os.Stat(filepath.Dir(path)); !os.IsNotExist(statErr) {
		t.Errorf("scratch dir survived cleanup: stat err = %v", statErr)
	}
}

func TestDownloadRepairLocateCleansUpOnRepairFailure(t *testing.T) {
	skipNoShell(t)
	bin := writeFakePar2(t, "#!/bin/sh\nexit 1\n")
	base := t.TempDir()
	s := New(Config{Enabled: true, BinaryPath: bin, WorkDir: base})

	dl := stubDownloader{files: map[string]string{
		"movie.mkv":  "v",
		"movie.par2": "index",
	}}
	_, _, err := s.DownloadRepairLocate(context.Background(), "sess", dl)
	if err == nil {
		t.Fatal("err = nil, want repair failure")
	}
	// No scratch dirs should remain under base.
	entries, _ := os.ReadDir(base)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "streamnzb-par2-") {
			t.Errorf("scratch dir leaked on failure: %s", e.Name())
		}
	}
}

func TestDownloadRepairLocateNoRecovery(t *testing.T) {
	base := t.TempDir()
	s := New(Config{Enabled: true, BinaryPath: "/nonexistent/par2", WorkDir: base})
	dl := stubDownloader{files: map[string]string{"movie.mkv": "v"}} // no .par2
	_, _, err := s.DownloadRepairLocate(context.Background(), "sess", dl)
	if err != ErrNoRecovery {
		t.Errorf("err = %v, want ErrNoRecovery", err)
	}
	entries, _ := os.ReadDir(base)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "streamnzb-par2-") {
			t.Errorf("scratch dir leaked: %s", e.Name())
		}
	}
}

func TestSanitizeLabel(t *testing.T) {
	cases := map[string]string{
		"sess-123":               "sess-123",
		"a/b\\c:d":               "a_b_c_d",
		"":                       "session",
		"   ":                    "session",
		strings.Repeat("x", 100): strings.Repeat("x", 40),
	}
	for in, want := range cases {
		if got := sanitizeLabel(in); got != want {
			t.Errorf("sanitizeLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDownloadRepairLocateSharedCoalescesConcurrentCallers verifies the
// last-resort repair coordinator: when many playback requests for the same
// release exhaust their slots at once, only ONE multi-GB fileset download +
// repair runs; every caller receives the same playable path with its own
// cleanup; and the shared scratch dir survives until the last caller closes.
func TestDownloadRepairLocateSharedCoalescesConcurrentCallers(t *testing.T) {
	skipNoShell(t)
	bin := writeFakePar2(t, "#!/bin/sh\necho ok\nexit 0\n")
	s := New(Config{Enabled: true, BinaryPath: bin, WorkDir: t.TempDir()})

	var downloads int64
	dl := blockingDownloader{
		downloads: &downloads,
		started:   make(chan struct{}),
		release:   make(chan struct{}),
		startOnce: &sync.Once{},
		files: map[string]string{
			"movie.mkv":  strings.Repeat("v", 4000),
			"movie.par2": "index",
		},
	}

	const n = 6
	type outcome struct {
		path    string
		cleanup func()
		err     error
	}
	results := make([]outcome, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, c, e := s.DownloadRepairLocateShared(context.Background(), "release-key", dl)
			results[i] = outcome{path: p, cleanup: c, err: e}
		}(i)
	}

	// Wait until the (single) leader is blocked inside the download, then let it
	// finish. Any caller that arrives while the leader holds the entry coalesces.
	select {
	case <-dl.started:
	case <-time.After(5 * time.Second):
		t.Fatal("download never started")
	}
	close(dl.release)
	wg.Wait()

	if got := atomic.LoadInt64(&downloads); got != 1 {
		t.Fatalf("downloads = %d, want 1 (concurrent callers must coalesce)", got)
	}

	want := results[0].path
	if want == "" {
		t.Fatal("leader returned empty path")
	}
	for i, r := range results {
		if r.err != nil {
			t.Fatalf("caller %d err = %v, want nil", i, r.err)
		}
		if r.path != want {
			t.Fatalf("caller %d path = %q, want shared %q", i, r.path, want)
		}
		if r.cleanup == nil {
			t.Fatalf("caller %d cleanup = nil", i)
		}
	}

	scratch := filepath.Dir(want)
	if _, err := os.Stat(scratch); err != nil {
		t.Fatalf("scratch dir missing before any cleanup: %v", err)
	}
	// Releasing all-but-one must keep the shared dir alive for the last reader.
	for i := 0; i < n-1; i++ {
		results[i].cleanup()
	}
	if _, err := os.Stat(scratch); err != nil {
		t.Fatalf("scratch dir removed while a caller still holds it: %v", err)
	}
	results[n-1].cleanup()
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Fatalf("scratch dir survived final cleanup: stat err = %v", err)
	}
}

// TestDownloadRepairLocateSharedRetriesAfterAllReleased verifies that once every
// caller of a coalesced repair has released, the key is forgotten so a later
// request runs a fresh download rather than reusing a removed scratch dir.
func TestDownloadRepairLocateSharedRetriesAfterAllReleased(t *testing.T) {
	skipNoShell(t)
	bin := writeFakePar2(t, "#!/bin/sh\necho ok\nexit 0\n")
	s := New(Config{Enabled: true, BinaryPath: bin, WorkDir: t.TempDir()})

	var downloads int64
	mk := func() blockingDownloader {
		return blockingDownloader{
			downloads: &downloads,
			started:   make(chan struct{}),
			release:   closedChan(),
			startOnce: &sync.Once{},
			files:     map[string]string{"movie.mkv": "vvvv", "movie.par2": "index"},
		}
	}

	p1, c1, err := s.DownloadRepairLocateShared(context.Background(), "k", mk())
	if err != nil {
		t.Fatalf("first repair err = %v", err)
	}
	c1()
	if _, statErr := os.Stat(filepath.Dir(p1)); !os.IsNotExist(statErr) {
		t.Fatalf("first scratch dir not cleaned: %v", statErr)
	}

	p2, c2, err := s.DownloadRepairLocateShared(context.Background(), "k", mk())
	if err != nil {
		t.Fatalf("second repair err = %v", err)
	}
	defer c2()
	if got := atomic.LoadInt64(&downloads); got != 2 {
		t.Fatalf("downloads = %d, want 2 (sequential calls must not share a freed result)", got)
	}
	if p2 == p1 {
		t.Fatalf("second repair reused freed scratch path %q", p2)
	}
}

// --- test helpers ---

type stubDownloader struct {
	files map[string]string
	err   error
}

// blockingDownloader counts invocations and blocks each Download on release
// until the test unblocks it, so concurrent coalescing can be observed
// deterministically. started is closed exactly once, when the first (leader)
// download begins.
type blockingDownloader struct {
	downloads *int64
	started   chan struct{}
	release   chan struct{}
	startOnce *sync.Once
	files     map[string]string
}

func (d blockingDownloader) Download(_ context.Context, workDir string) ([]string, error) {
	atomic.AddInt64(d.downloads, 1)
	d.startOnce.Do(func() { close(d.started) })
	<-d.release
	var names []string
	for name, content := range d.files {
		if err := os.WriteFile(filepath.Join(workDir, name), []byte(content), 0o644); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, nil
}

func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func (d stubDownloader) Download(_ context.Context, workDir string) ([]string, error) {
	if d.err != nil {
		return nil, d.err
	}
	var names []string
	for name, content := range d.files {
		if err := os.WriteFile(filepath.Join(workDir, name), []byte(content), 0o644); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, nil
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func writeFakePar2(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-par2")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake par2: %v", err)
	}
	return path
}

func skipNoShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake par2 shell script unsupported on windows")
	}
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

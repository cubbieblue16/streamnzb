package par2

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"streamnzb/pkg/core/logger"
	"streamnzb/pkg/media/fileutil"
)

// Default settings applied by New when a field is zero/unset.
const (
	DefaultBinaryPath    = "par2"
	DefaultMaxConcurrent = 1
	DefaultTimeout       = 30 * time.Minute
	// repairWaitDelay bounds how long Wait blocks after the repair context is
	// canceled or the process exits. Without it, if par2 (or a descendant it
	// spawned) keeps the output pipe open after being killed at the timeout,
	// CombinedOutput would block until that descendant exits — defeating the
	// configured Timeout. With it set, Wait force-closes the inherited pipes
	// shortly after the kill and returns, so the timeout is a real guarantee.
	repairWaitDelay = 2 * time.Second
)

// Sentinel errors returned by Repair / DownloadRepairLocate.
var (
	// ErrDisabled is returned when the service is nil or not enabled. Callers
	// treat it as "feature off; do nothing" rather than a failure.
	ErrDisabled = errors.New("par2: repair disabled")
	// ErrNoRecovery means the fileset carries no .par2 files, so repair is
	// impossible.
	ErrNoRecovery = errors.New("par2: no recovery (.par2) files present")
	// ErrOverBudget means the fileset exceeds the configured disk cap.
	ErrOverBudget = errors.New("par2: fileset exceeds par2_max_bytes disk cap")
	// ErrNoPlayable means repair succeeded but no playable media file was found.
	ErrNoPlayable = errors.New("par2: no playable media file after repair")
)

// Config controls the repair service. The zero value is disabled. All fields map
// to par2_* config keys; New applies defaults for unset values.
type Config struct {
	Enabled       bool
	BinaryPath    string        // path to `par2` binary (default "par2")
	WorkDir       string        // base scratch dir for repairs ("" = os.TempDir)
	MaxConcurrent int           // simultaneous repairs (default 1)
	Timeout       time.Duration // per-repair wall clock (default 30m)
	MaxBytes      int64         // skip filesets larger than this (0 = unlimited)
}

// Service runs gated, concurrency-limited PAR2 repairs. A nil *Service is a safe
// no-op (Enabled reports false; Repair returns ErrDisabled).
type Service struct {
	cfg Config
	sem chan struct{}
}

// New builds a Service, applying defaults. When cfg.Enabled is false the service
// still constructs (so callers can hold a non-nil handle) but Enabled reports
// false and all repair entry points short-circuit to ErrDisabled.
func New(cfg Config) *Service {
	if strings.TrimSpace(cfg.BinaryPath) == "" {
		cfg.BinaryPath = DefaultBinaryPath
	}
	if cfg.MaxConcurrent < 1 {
		cfg.MaxConcurrent = DefaultMaxConcurrent
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.MaxBytes < 0 {
		cfg.MaxBytes = 0
	}
	return &Service{
		cfg: cfg,
		sem: make(chan struct{}, cfg.MaxConcurrent),
	}
}

// Enabled reports whether the service is non-nil and turned on.
func (s *Service) Enabled() bool {
	return s != nil && s.cfg.Enabled
}

// WorkDirBase returns the configured scratch base directory, or os.TempDir when
// unset.
func (s *Service) WorkDirBase() string {
	if s == nil || strings.TrimSpace(s.cfg.WorkDir) == "" {
		return os.TempDir()
	}
	return s.cfg.WorkDir
}

// WithinBudget reports whether a fileset of totalBytes is allowed under the disk
// cap. A cap of <=0 means unlimited.
func (s *Service) WithinBudget(totalBytes int64) bool {
	if s == nil {
		return false
	}
	return s.cfg.MaxBytes <= 0 || totalBytes <= s.cfg.MaxBytes
}

// Result describes the outcome of a repair run.
type Result struct {
	Repaired        bool          // par2 exited successfully
	AlreadyComplete bool          // files were already correct (no repair needed)
	MainPar2        string        // the .par2 file handed to the binary (base name)
	Output          string        // trimmed combined stdout/stderr
	Duration        time.Duration // wall-clock of the par2 invocation
}

// Repair runs `par2 repair` against the PAR2 set already present in workDir. It
// scans workDir for files, classifies them, enforces the disk budget over the
// data files, acquires a concurrency slot (respecting ctx), then invokes the
// binary with a timeout. The repaired files are left in workDir for the caller
// to serve. Returns ErrDisabled/ErrNoRecovery/ErrOverBudget as appropriate.
func (s *Service) Repair(ctx context.Context, workDir string) (*Result, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	names, totalBytes, err := scanDir(workDir)
	if err != nil {
		return nil, fmt.Errorf("par2: scan work dir: %w", err)
	}
	class := Classify(names)
	if !class.HasRecovery() {
		return nil, ErrNoRecovery
	}
	if !s.WithinBudget(totalBytes) {
		return nil, fmt.Errorf("%w (%d bytes > %d)", ErrOverBudget, totalBytes, s.cfg.MaxBytes)
	}
	main := class.MainPar2()

	// Acquire a concurrency slot, honoring cancellation.
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	runCtx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()

	start := nowFn()
	cmd := exec.CommandContext(runCtx, s.cfg.BinaryPath, "repair", "--", filepath.Base(main))
	cmd.Dir = workDir
	// Bound the post-kill wait so a stuck/long-running par2 process (or a
	// descendant holding the output pipe) cannot block past the timeout.
	cmd.WaitDelay = repairWaitDelay
	out, runErr := cmd.CombinedOutput()
	dur := nowFn().Sub(start)
	output := strings.TrimSpace(string(out))

	res := &Result{
		MainPar2: filepath.Base(main),
		Output:   output,
		Duration: dur,
	}

	if runErr != nil {
		// Distinguish timeout from a genuine repair failure for the caller's log.
		if runCtx.Err() == context.DeadlineExceeded {
			return res, fmt.Errorf("par2: repair timed out after %s: %w", s.cfg.Timeout, runCtx.Err())
		}
		if errors.Is(runCtx.Err(), context.Canceled) {
			return res, runCtx.Err()
		}
		return res, fmt.Errorf("par2: repair failed: %w (output: %s)", runErr, truncate(output, 512))
	}

	res.Repaired = true
	res.AlreadyComplete = indicatesAlreadyComplete(output)
	logger.Info("PAR2 repair succeeded",
		"workdir", workDir,
		"main_par2", res.MainPar2,
		"already_complete", res.AlreadyComplete,
		"duration", dur.String())
	return res, nil
}

// LocatePlayable returns the path to the largest video file in workDir — the
// media file to serve after a successful repair. Returns ErrNoPlayable when none
// is found.
func (s *Service) LocatePlayable(workDir string) (string, error) {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return "", fmt.Errorf("par2: read work dir: %w", err)
	}
	var bestPath string
	var bestSize int64 = -1
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !fileutil.IsVideoFile(name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Size() > bestSize {
			bestSize = info.Size()
			bestPath = filepath.Join(workDir, name)
		}
	}
	if bestPath == "" {
		return "", ErrNoPlayable
	}
	return bestPath, nil
}

// Downloader materializes an NZB fileset into workDir. Implementations live in
// the stremio layer (backed by the loader); the par2 package depends only on
// this interface so the orchestration can be tested with a fixture writer.
type Downloader interface {
	// Download writes the release's data and .par2 files into workDir and
	// returns the base names written.
	Download(ctx context.Context, workDir string) ([]string, error)
}

// DownloadRepairLocate is the full last-resort flow: create a unique scratch
// dir, materialize the fileset via dl, repair it, locate the playable file, and
// return its path. The caller is responsible for opening/serving the file and
// for invoking the returned cleanup once the stream is done (which removes the
// scratch dir). On any error the scratch dir is removed before returning.
//
// label is a short, filesystem-safe identifier (e.g. session id) used only to
// name the scratch dir for diagnostics.
func (s *Service) DownloadRepairLocate(ctx context.Context, label string, dl Downloader) (playablePath string, cleanup func(), err error) {
	if !s.Enabled() {
		return "", noopCleanup, ErrDisabled
	}
	if dl == nil {
		return "", noopCleanup, errors.New("par2: nil downloader")
	}

	workDir, err := os.MkdirTemp(s.WorkDirBase(), "streamnzb-par2-"+sanitizeLabel(label)+"-*")
	if err != nil {
		return "", noopCleanup, fmt.Errorf("par2: create scratch dir: %w", err)
	}
	// rm is a distinct local (not the named return) so the failure-path defer
	// always removes the scratch dir even though the named cleanup return is
	// reassigned to noopCleanup on the error returns below.
	rm := func() {
		if rmErr := os.RemoveAll(workDir); rmErr != nil {
			logger.Warn("PAR2 scratch cleanup failed", "dir", workDir, "err", rmErr)
		}
	}
	success := false
	defer func() {
		if !success {
			rm()
		}
	}()

	written, dlErr := dl.Download(ctx, workDir)
	if dlErr != nil {
		return "", noopCleanup, fmt.Errorf("par2: download fileset: %w", dlErr)
	}
	logger.Info("PAR2 fileset materialized", "dir", workDir, "files", sortedBaseNames(written))

	if _, repErr := s.Repair(ctx, workDir); repErr != nil {
		return "", noopCleanup, repErr
	}

	playablePath, err = s.LocatePlayable(workDir)
	if err != nil {
		return "", noopCleanup, err
	}

	success = true
	return playablePath, rm, nil
}

// --- helpers ---

// nowFn is a clock indirection so tests can keep duration deterministic if
// needed; defaults to time.Now.
var nowFn = time.Now

func noopCleanup() {}

// scanDir lists regular-file base names in dir and their total size.
func scanDir(dir string) (names []string, totalBytes int64, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, infoErr := e.Info()
		if infoErr != nil {
			continue
		}
		names = append(names, e.Name())
		totalBytes += info.Size()
	}
	sort.Strings(names)
	return names, totalBytes, nil
}

// indicatesAlreadyComplete scans par2 output for the "no repair needed" message.
func indicatesAlreadyComplete(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "repair is not required") ||
		strings.Contains(lower, "all files are correct")
}

// sanitizeLabel keeps a label safe for use in a directory name.
func sanitizeLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return "session"
	}
	var b strings.Builder
	for _, r := range label {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 40 {
			break
		}
	}
	if b.Len() == 0 {
		return "session"
	}
	return b.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

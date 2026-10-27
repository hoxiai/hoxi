// Package reclaim records the temporary processes and directories a Hoxi
// process creates, so a later Hoxi process can clean them up after a crash,
// kill -9 or `wails dev` restart left them behind.
//
// Ownership is explicit and per process: every record names the Hoxi process
// that created it (PID plus start time). Sweep only touches records whose
// owner is gone, so a dev and a release instance, or a desktop app and a
// headless CLI, sharing one ledger never reclaim each other's live resources.
// Sweep never scans the process table or the temp directory for candidates.
//
// It is a foundation package: it must not import gateway, app, cli or any
// business domain package.
package reclaim

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	ledgerVersion = 1
	// DirPrefix starts every directory name the ledger creates and the only
	// names Sweep will remove.
	DirPrefix   = "hoxi-"
	lockTimeout = 5 * time.Second
)

type process struct {
	Identity
	// Args is a run of consecutive argv elements the live process must carry
	// verbatim before it may be signalled, e.g. Chrome's
	// --user-data-dir=<profile>, or `serve … --port <port>` for OpenCode.
	Args []string `json:"args"`
}

type entry struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	Owner   Identity  `json:"owner"`
	Dir     string    `json:"dir,omitempty"`
	Process *process  `json:"process,omitempty"`
	Created time.Time `json:"created"`
}

type ledgerFile struct {
	Version int     `json:"version"`
	Entries []entry `json:"entries"`
}

// Ledger is the on-disk record of resources owned by Hoxi processes. A nil
// *Ledger is valid: it creates directories without recording them.
type Ledger struct {
	path string
	self Identity
	// root is where directories are created; rootResolved is the only parent
	// Sweep accepts for a recorded directory.
	root, rootResolved string
	logger             *slog.Logger

	inspect  func(int) (procInfo, error)
	signal   signalFunc
	termWait time.Duration
	killWait time.Duration

	mu sync.Mutex
}

// Open returns the ledger stored at path for the current process. It fails
// on platforms where process identity cannot be verified.
func Open(path string, logger *slog.Logger) (*Ledger, error) {
	if !supported {
		return nil, errors.New("reclaim: process identity is not supported on this platform")
	}
	if path == "" || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("reclaim: ledger path must be absolute")
	}
	if logger == nil {
		logger = slog.Default()
	}
	info, err := inspectProcess(os.Getpid())
	if err != nil {
		return nil, fmt.Errorf("reclaim: inspect self: %w", err)
	}
	root := os.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("reclaim: resolve temp dir: %w", err)
	}
	return &Ledger{
		path:         filepath.Clean(path),
		self:         Identity{PID: os.Getpid(), Start: info.start},
		root:         root,
		rootResolved: resolved,
		logger:       logger,
		inspect:      inspectProcess,
		signal:       signalProcess,
		termWait:     5 * time.Second,
		killWait:     3 * time.Second,
	}, nil
}

// MkdirTemp creates a directory named prefix+random under the system temp
// directory and records it. prefix must start with DirPrefix. A recording
// failure is logged and leaves id empty; the directory is still usable.
func (l *Ledger) MkdirTemp(kind, prefix string) (dir, id string, err error) {
	if !strings.HasPrefix(prefix, DirPrefix) || strings.ContainsAny(prefix, `/\*`) {
		return "", "", fmt.Errorf("reclaim: directory prefix %q must start with %q", prefix, DirPrefix)
	}
	if l == nil {
		dir, err = os.MkdirTemp("", prefix+"*")
		return dir, "", err
	}
	dir, err = os.MkdirTemp(l.root, prefix+"*")
	if err != nil {
		return "", "", err
	}
	id = newID()
	e := entry{ID: id, Kind: kind, Owner: l.self, Dir: dir, Created: time.Now().UTC()}
	if err := l.update(func(entries []entry) []entry { return append(entries, e) }); err != nil {
		l.logger.Warn("reclaim: could not record temporary directory", "kind", kind, "error", err)
		return dir, "", nil
	}
	return dir, id, nil
}

// AttachProcess binds a started child process to the record id. Sweep will
// only signal it while its PID, start time and args all still match.
func (l *Ledger) AttachProcess(id string, pid int, args ...string) {
	if l == nil || id == "" {
		return
	}
	p := l.identify(pid, args)
	if p == nil {
		return
	}
	err := l.update(func(entries []entry) []entry {
		for i := range entries {
			if entries[i].ID == id && entries[i].Owner == l.self {
				entries[i].Process = p
			}
		}
		return entries
	})
	if err != nil {
		l.logger.Warn("reclaim: could not record child process", "pid", pid, "error", err)
	}
}

// RecordProcess records a started child process that owns no directory, such
// as a daemon, and returns its record id, or "" when it could not be recorded.
// Sweep will only signal it while its PID, start time and args all still match.
func (l *Ledger) RecordProcess(kind string, pid int, args ...string) string {
	if l == nil {
		return ""
	}
	p := l.identify(pid, args)
	if p == nil {
		return ""
	}
	e := entry{ID: newID(), Kind: kind, Owner: l.self, Process: p, Created: time.Now().UTC()}
	if err := l.update(func(entries []entry) []entry { return append(entries, e) }); err != nil {
		l.logger.Warn("reclaim: could not record child process", "kind", kind, "pid", pid, "error", err)
		return ""
	}
	return e.ID
}

// identify captures the incarnation of a live child process together with
// the argv run it must keep carrying.
func (l *Ledger) identify(pid int, args []string) *process {
	if pid <= 1 || !validArgs(args) {
		return nil
	}
	info, err := l.inspect(pid)
	if err != nil || info.zombie {
		l.logger.Warn("reclaim: could not identify child process", "pid", pid, "error", err)
		return nil
	}
	return &process{Identity: Identity{PID: pid, Start: info.start}, Args: append([]string(nil), args...)}
}

// validArgs requires at least one argument and no empty element, so a record
// never verifies a process by an empty or absent argv.
func validArgs(args []string) bool {
	for _, a := range args {
		if a == "" {
			return false
		}
	}
	return len(args) > 0
}

// Forget drops the record id after its owner cleaned the resources up itself.
func (l *Ledger) Forget(id string) {
	if l == nil || id == "" {
		return
	}
	err := l.update(func(entries []entry) []entry {
		return removeEntries(entries, func(e entry) bool { return e.ID == id && e.Owner == l.self })
	})
	if err != nil {
		l.logger.Warn("reclaim: could not drop record", "error", err)
	}
}

func removeEntries(entries []entry, drop func(entry) bool) []entry {
	out := entries[:0]
	for _, e := range entries {
		if !drop(e) {
			out = append(out, e)
		}
	}
	return out
}

// update runs one read-modify-write under the in-process mutex and the
// cross-process file lock; the lock exists only to serialise this file.
func (l *Ledger) update(mutate func([]entry) []entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	unlock, err := lockFile(l.path+".lock", lockTimeout)
	if err != nil {
		return fmt.Errorf("lock ledger: %w", err)
	}
	defer unlock()
	entries, err := l.load()
	if err != nil {
		return err
	}
	return l.save(mutate(entries))
}

// load reads the ledger. A corrupt file is moved aside first and never
// overwritten; if it cannot be moved, the ledger stays read-only.
func (l *Ledger) load() ([]entry, error) {
	raw, err := os.ReadFile(l.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f ledgerFile
	parseErr := json.Unmarshal(raw, &f)
	if parseErr == nil && f.Version > ledgerVersion {
		return nil, fmt.Errorf("ledger version %d is newer than %d", f.Version, ledgerVersion)
	}
	if parseErr == nil && f.Version == ledgerVersion {
		return f.Entries, nil
	}
	if parseErr == nil {
		parseErr = fmt.Errorf("unsupported ledger version %d", f.Version)
	}
	backup := fmt.Sprintf("%s.corrupt-%s", l.path, time.Now().UTC().Format("20060102T150405.000000000"))
	if err := os.Rename(l.path, backup); err != nil {
		return nil, fmt.Errorf("ledger is corrupt (%v) and could not be backed up: %w", parseErr, err)
	}
	l.logger.Warn("reclaim: corrupt ledger moved aside; its records are not acted on", "backup", backup, "error", parseErr)
	return nil, nil
}

func (l *Ledger) save(entries []entry) error {
	if entries == nil {
		entries = []entry{}
	}
	raw, err := json.MarshalIndent(ledgerFile{Version: ledgerVersion, Entries: entries}, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(l.path, append(raw, '\n'))
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(name, 0o600)
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

func newID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

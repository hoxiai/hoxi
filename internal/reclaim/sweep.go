package reclaim

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Report counts what one Sweep did.
type Report struct {
	// Processes is the number of recorded processes terminated.
	Processes int
	// Dirs is the number of recorded directories removed.
	Dirs int
	// Skipped counts records dropped without action because they failed verification.
	Skipped int
	// Pending counts records kept for a later sweep (inspection or cleanup failed).
	Pending int
}

var errRejectedDir = errors.New("recorded directory failed verification")

// Sweep reclaims the records of owner processes that are gone. Records are
// claimed under the file lock by re-owning them to this process, so two
// sweepers never act on one record, and a sweeper that dies mid-way leaves
// them for the next one. Signals and removals run outside the lock.
func (l *Ledger) Sweep() Report {
	var r Report
	if l == nil {
		return r
	}
	var claimed []entry
	err := l.update(func(entries []entry) []entry {
		claimed = claimed[:0]
		out := entries[:0]
		for _, e := range entries {
			if !e.valid() {
				l.logger.Warn("reclaim: dropping malformed record", "kind", e.Kind)
				r.Skipped++
				continue
			}
			if e.Owner != l.self && processState(l.inspect, e.Owner, nil) == stateGone {
				e.Owner = l.self
				claimed = append(claimed, e)
			}
			out = append(out, e)
		}
		return out
	})
	if err != nil {
		l.logger.Warn("reclaim: sweep skipped", "error", err)
		return Report{}
	}
	done := make(map[string]bool, len(claimed))
	for _, e := range claimed {
		if l.reclaim(e, &r) {
			done[e.ID] = true
		} else {
			r.Pending++
		}
	}
	if len(done) > 0 {
		err = l.update(func(entries []entry) []entry {
			return removeEntries(entries, func(e entry) bool { return done[e.ID] && e.Owner == l.self })
		})
		if err != nil {
			l.logger.Warn("reclaim: could not drop reclaimed records", "error", err)
		}
	}
	return r
}

func (e entry) valid() bool {
	if e.ID == "" || !e.Owner.valid() || (e.Dir == "" && e.Process == nil) {
		return false
	}
	return e.Process == nil || (e.Process.valid() && validArgs(e.Process.Args))
}

// reclaim cleans up one claimed record and reports whether it can be dropped.
// The directory is removed only once the recorded process is confirmed gone.
func (l *Ledger) reclaim(e entry, r *Report) bool {
	log := l.logger.With("kind", e.Kind, "owner", e.Owner.PID)
	if p := e.Process; p != nil {
		switch processState(l.inspect, p.Identity, p.Args) {
		case stateMismatch:
			log.Warn("reclaim: recorded process lacks its recorded arguments; leaving it and its directory alone", "pid", p.PID)
			r.Skipped++
			return true
		case stateUnknown:
			log.Warn("reclaim: cannot inspect recorded process; will retry", "pid", p.PID)
			return false
		case stateMatch:
			if !l.terminate(*p) {
				log.Warn("reclaim: recorded process did not exit; will retry", "pid", p.PID)
				return false
			}
			r.Processes++
		}
	}
	if e.Dir == "" {
		return true
	}
	removed, err := l.removeDir(e.Dir)
	switch {
	case errors.Is(err, errRejectedDir):
		log.Warn("reclaim: recorded directory failed verification; not removed", "dir", e.Dir)
		r.Skipped++
	case err != nil:
		log.Warn("reclaim: could not remove recorded directory; will retry", "dir", e.Dir, "error", err)
		return false
	case removed:
		r.Dirs++
	}
	return true
}

// terminate asks the recorded process to exit, then kills it. Identity is
// re-verified before every signal so a recycled PID is never signalled.
func (l *Ledger) terminate(p process) bool {
	steps := []struct {
		sig  syscall.Signal
		wait time.Duration
	}{{syscall.SIGTERM, l.termWait}, {syscall.SIGKILL, l.killWait}}
	for _, step := range steps {
		switch processState(l.inspect, p.Identity, p.Args) {
		case stateGone:
			return true
		case stateMatch:
		default:
			return false
		}
		if err := l.signal(p.PID, step.sig); err != nil && !errors.Is(err, syscall.ESRCH) {
			return false
		}
		for deadline := time.Now().Add(step.wait); time.Now().Before(deadline); {
			if processState(l.inspect, p.Identity, nil) == stateGone {
				return true
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	return processState(l.inspect, p.Identity, nil) == stateGone
}

// removeDir deletes a recorded directory only if it is still a real
// directory owned by this user, directly inside the temp root, and named with
// DirPrefix. RemoveAll never follows symlinks inside it.
func (l *Ledger) removeDir(dir string) (bool, error) {
	base := filepath.Base(dir)
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || !strings.HasPrefix(base, DirPrefix) || len(base) == len(DirPrefix) {
		return false, errRejectedDir
	}
	if parent, err := filepath.EvalSymlinks(filepath.Dir(dir)); err != nil || parent != l.rootResolved {
		return false, errRejectedDir
	}
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || !ownedByUser(info) {
		return false, errRejectedDir
	}
	if err := os.RemoveAll(dir); err != nil {
		return false, err
	}
	return true, nil
}

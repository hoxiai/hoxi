package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const maxTrackedFilesPerSession = 2048

type lineSpan struct {
	start int
	end   int
}

type fileSnapshot struct {
	size       int64
	sha256     [32]byte
	totalLines int
	readSpans  []lineSpan
	fullRead   bool
	seenDirect bool
}

// FileRevisionTracker tracks SHA-256 content digests of workspace files read or
// written during a session/turn so mutating file tools reject blind full-file
// overwrites of unread files and detect concurrent external edits on disk.
type FileRevisionTracker struct {
	mu    sync.RWMutex
	files map[string]fileSnapshot
	order []string
}

// NewFileRevisionTracker constructs an empty thread-safe FileRevisionTracker.
func NewFileRevisionTracker() *FileRevisionTracker {
	return &FileRevisionTracker{
		files: make(map[string]fileSnapshot),
	}
}

func canonicalRevisionKey(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(trimmed); err == nil && real != "" {
		return filepath.Clean(real)
	}
	abs := trimmed
	if a, err := filepath.Abs(trimmed); err == nil && a != "" {
		abs = a
	}
	cleanAbs := filepath.Clean(abs)
	// Walk up to the nearest existing ancestor so symlinked roots (e.g. macOS
	// /var -> /private/var) resolve identically even after os.Remove.
	cur := cleanAbs
	var tail []string
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
		if realCur, err := filepath.EvalSymlinks(cur); err == nil && realCur != "" {
			for i := len(tail) - 1; i >= 0; i-- {
				realCur = filepath.Join(realCur, tail[i])
			}
			return filepath.Clean(realCur)
		}
	}
	return cleanAbs
}

func displayFileLabel(displayPath, fallbackPath string) string {
	if s := strings.TrimSpace(displayPath); s != "" {
		return s
	}
	return filepath.Base(fallbackPath)
}

func (r *FileRevisionTracker) storeLocked(key string, snap fileSnapshot) {
	if _, exists := r.files[key]; !exists {
		for len(r.order) >= maxTrackedFilesPerSession {
			oldest := r.order[0]
			r.order = r.order[1:]
			delete(r.files, oldest)
		}
		r.order = append(r.order, key)
	}
	r.files[key] = snap
}

func mergeLineSpans(existing []lineSpan, added lineSpan) []lineSpan {
	if added.start < 1 || added.end < added.start {
		return existing
	}
	all := make([]lineSpan, 0, len(existing)+1)
	all = append(all, existing...)
	all = append(all, added)
	sort.Slice(all, func(i, j int) bool {
		if all[i].start != all[j].start {
			return all[i].start < all[j].start
		}
		return all[i].end < all[j].end
	})
	merged := all[:1]
	for _, sp := range all[1:] {
		last := &merged[len(merged)-1]
		if sp.start <= last.end+1 {
			if sp.end > last.end {
				last.end = sp.end
			}
		} else {
			merged = append(merged, sp)
		}
	}
	return merged
}

// RecordBytes records a complete byte snapshot read in full or written at path.
func (r *FileRevisionTracker) RecordBytes(path string, content []byte) {
	if r == nil {
		return
	}
	key := canonicalRevisionKey(path)
	if key == "" {
		return
	}
	snap := fileSnapshot{
		size:       int64(len(content)),
		sha256:     sha256.Sum256(content),
		fullRead:   true,
		seenDirect: true,
	}
	r.mu.Lock()
	r.storeLocked(key, snap)
	r.mu.Unlock()
}

// RecordReadLines records a text read over [startLine, endLine] of totalLines.
// Full-file write eligibility is granted only when every line 1..totalLines has
// been delivered to this agent without output truncation (`deliveredComplete`).
func (r *FileRevisionTracker) RecordReadLines(path string, content []byte, startLine, endLine, totalLines int, deliveredComplete bool) {
	if r == nil {
		return
	}
	key := canonicalRevisionKey(path)
	if key == "" {
		return
	}
	sum := sha256.Sum256(content)
	r.mu.Lock()
	prev, hadPrev := r.files[key]
	sameDigest := hadPrev && prev.sha256 == sum
	var spans []lineSpan
	fullRead := sameDigest && prev.fullRead
	if sameDigest && len(prev.readSpans) > 0 {
		spans = append(spans, prev.readSpans...)
	}
	if deliveredComplete && totalLines >= 1 && startLine >= 1 && endLine >= startLine {
		clampedEnd := min(endLine, totalLines)
		spans = mergeLineSpans(spans, lineSpan{start: startLine, end: clampedEnd})
		if len(spans) == 1 && spans[0].start == 1 && spans[0].end >= totalLines {
			fullRead = true
		}
	}
	snap := fileSnapshot{
		size:       int64(len(content)),
		sha256:     sum,
		totalLines: totalLines,
		readSpans:  spans,
		fullRead:   fullRead,
		seenDirect: true,
	}
	r.storeLocked(key, snap)
	r.mu.Unlock()
}

// RecordPartialRead records the current disk digest from a ranged or truncated read
// so targeted edits still detect external disk changes, without authorizing a
// full-file `write` overwrite unless the exact digest was already read in full.
func (r *FileRevisionTracker) RecordPartialRead(path string, content []byte) {
	r.RecordReadLines(path, content, 0, 0, 0, false)
}

// RecordTargetedEdit updates the disk digest after a targeted edit (`edit`,
// `multiedit`, or `apply_patch` Update), preserving `fullRead` only if this
// agent had already read or written the entire prior revision.
func (r *FileRevisionTracker) RecordTargetedEdit(path string, prevContent, updatedContent []byte) {
	r.RecordTargetedMove(path, path, prevContent, updatedContent)
}

// RecordTargetedMove updates the disk digest after a targeted edit + optional
// rename, preserving `fullRead` from srcPath only when srcPath was fully read.
func (r *FileRevisionTracker) RecordTargetedMove(srcPath, dstPath string, prevContent, updatedContent []byte) {
	if r == nil {
		return
	}
	dstKey := canonicalRevisionKey(dstPath)
	if dstKey == "" {
		return
	}
	srcKey := canonicalRevisionKey(srcPath)
	if srcKey == "" {
		srcKey = dstKey
	}
	prevSum := sha256.Sum256(prevContent)
	newSum := sha256.Sum256(updatedContent)
	r.mu.Lock()
	prev, hadPrev := r.files[srcKey]
	snap := fileSnapshot{
		size:       int64(len(updatedContent)),
		sha256:     newSum,
		fullRead:   hadPrev && prev.fullRead && prev.sha256 == prevSum,
		seenDirect: true,
	}
	r.storeLocked(dstKey, snap)
	r.mu.Unlock()
}

// NewChildTracker creates a child subagent tracker seeded only with external
// disk-conflict digests (`sha256`) and never inheriting `fullRead` privileges.
func (r *FileRevisionTracker) NewChildTracker() *FileRevisionTracker {
	child := NewFileRevisionTracker()
	if r == nil {
		return child
	}
	r.mu.RLock()
	for _, k := range r.order {
		if snap, ok := r.files[k]; ok {
			child.storeLocked(k, fileSnapshot{
				size:       snap.size,
				sha256:     snap.sha256,
				fullRead:   false,
				seenDirect: false,
			})
		}
	}
	r.mu.RUnlock()
	return child
}

// MergeChildObservations records disk digests observed by a child subagent for
// files the parent has not yet tracked, without granting `fullRead` to the
// parent (which only saw the subagent's summary) or masking child mutations on
// files the parent previously read at an older revision.
func (r *FileRevisionTracker) MergeChildObservations(child *FileRevisionTracker) {
	if r == nil || child == nil || r == child {
		return
	}
	child.mu.RLock()
	entries := make([]struct {
		key  string
		snap fileSnapshot
	}, 0, len(child.order))
	for _, k := range child.order {
		if snap, ok := child.files[k]; ok {
			entries = append(entries, struct {
				key  string
				snap fileSnapshot
			}{key: k, snap: snap})
		}
	}
	child.mu.RUnlock()

	r.mu.Lock()
	for _, e := range entries {
		if _, exists := r.files[e.key]; !exists {
			r.storeLocked(e.key, fileSnapshot{
				size:       e.snap.size,
				sha256:     e.snap.sha256,
				fullRead:   false,
				seenDirect: false,
			})
		}
	}
	r.mu.Unlock()
}

// Forget removes any recorded snapshot for path (used after delete or move).
func (r *FileRevisionTracker) Forget(path string) {
	if r == nil {
		return
	}
	key := canonicalRevisionKey(path)
	if key == "" {
		return
	}
	r.mu.Lock()
	if _, exists := r.files[key]; exists {
		delete(r.files, key)
		for i, k := range r.order {
			if k == key {
				r.order = append(r.order[:i], r.order[i+1:]...)
				break
			}
		}
	}
	r.mu.Unlock()
}

func (r *FileRevisionTracker) lookup(path string) (fileSnapshot, bool) {
	if r == nil {
		return fileSnapshot{}, false
	}
	key := canonicalRevisionKey(path)
	if key == "" {
		return fileSnapshot{}, false
	}
	r.mu.RLock()
	snap, ok := r.files[key]
	r.mu.RUnlock()
	return snap, ok
}

// CheckWrite validates full-file overwrites (`write` and `apply_patch` Add File):
//   - Creating a new file or populating a 0-byte empty file is always allowed.
//   - Overwriting an existing non-empty file requires a prior full `read` (or write)
//     in this tracker AND matching disk content so unread ranges or externally
//     edited files are never silently clobbered.
func (r *FileRevisionTracker) CheckWrite(path, displayPath string) error {
	if r == nil {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !st.Mode().IsRegular() || st.Size() == 0 {
		return nil
	}
	diskBytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(diskBytes) == 0 {
		return nil
	}
	label := displayFileLabel(displayPath, path)
	snap, ok := r.lookup(path)
	if !ok {
		return fmt.Errorf(
			"cannot overwrite existing non-empty file %q (%d bytes) without reading it first; call read on %q to inspect its current contents (or use edit for targeted changes)",
			label,
			len(diskBytes),
			label,
		)
	}
	if sha256.Sum256(diskBytes) != snap.sha256 {
		return fmt.Errorf(
			"file %q was modified on disk since it was last read; call read on %q first to inspect external changes before overwriting",
			label,
			label,
		)
	}
	if !snap.seenDirect {
		return fmt.Errorf(
			"cannot overwrite existing non-empty file %q (%d bytes) without reading it first; call read on %q directly to inspect its full contents (or use edit for targeted changes)",
			label,
			len(diskBytes),
			label,
		)
	}
	if !snap.fullRead {
		return fmt.Errorf(
			"cannot overwrite existing file %q (%d bytes) with full-file write after reading only a partial range (or truncated output); read all lines with offset/limit first or use edit for targeted changes",
			label,
			len(diskBytes),
		)
	}
	return nil
}

// CheckEdit validates targeted edits (`edit`, `multiedit`, `apply_patch` Update/Delete):
// when path was previously read or written in this session, verifies its disk
// digest still matches so concurrent user/external edits are never overwritten.
func (r *FileRevisionTracker) CheckEdit(path, displayPath string, currentDiskBytes []byte) error {
	if r == nil {
		return nil
	}
	snap, ok := r.lookup(path)
	if !ok {
		return nil
	}
	if sha256.Sum256(currentDiskBytes) != snap.sha256 {
		label := displayFileLabel(displayPath, path)
		return fmt.Errorf(
			"file %q was modified on disk since it was last read; call read on %q first to inspect external changes before editing",
			label,
			label,
		)
	}
	return nil
}

// HasReadDigest reports whether path was already read or written with exact digest.
func (r *FileRevisionTracker) HasReadDigest(path string, digest [32]byte) bool {
	snap, ok := r.lookup(path)
	return ok && snap.sha256 == digest
}

type pathRefMutex struct {
	mu   sync.Mutex
	refs int
}

var (
	localPathLocksMu sync.Mutex
	localPathLocks   = make(map[string]*pathRefMutex)
)

func acquireSortedPathLocks(paths []string) func() {
	seen := make(map[string]struct{}, len(paths))
	keys := make([]string, 0, len(paths))
	for _, p := range paths {
		k := canonicalRevisionKey(p)
		if k == "" {
			continue
		}
		if _, dup := seen[k]; !dup {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return func() {}
	}
	sort.Strings(keys)

	entries := make([]*pathRefMutex, len(keys))
	localPathLocksMu.Lock()
	for i, k := range keys {
		e := localPathLocks[k]
		if e == nil {
			e = &pathRefMutex{}
			localPathLocks[k] = e
		}
		e.refs++
		entries[i] = e
	}
	localPathLocksMu.Unlock()

	for _, e := range entries {
		e.mu.Lock()
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			for i := len(entries) - 1; i >= 0; i-- {
				entries[i].mu.Unlock()
			}
			localPathLocksMu.Lock()
			for i, k := range keys {
				entries[i].refs--
				if entries[i].refs <= 0 && localPathLocks[k] == entries[i] {
					delete(localPathLocks, k)
				}
			}
			localPathLocksMu.Unlock()
		})
	}
}

func coordinateFileMutation(
	ctx context.Context,
	tc ToolContext,
	absPaths []string,
	labels map[string]string,
) (func(), func(string) bool, error) {
	if tc.CoordinateFileEdit == nil {
		return acquireSortedPathLocks(absPaths), func(string) bool { return false }, nil
	}
	coord, err := tc.CoordinateFileEdit(ctx, absPaths, func(waitedPath string) {
		if tc.OnToolOutput == nil {
			return
		}
		label := labels[waitedPath]
		if label == "" {
			label = labels[canonicalRevisionKey(waitedPath)]
		}
		label = displayFileLabel(label, waitedPath)
		tc.OnToolOutput(fmt.Sprintf("Waiting for another active task in this workspace to finish modifying %s...", label))
	})
	if err != nil {
		return nil, nil, err
	}
	baseRelease := coord.Release
	if baseRelease == nil {
		baseRelease = acquireSortedPathLocks(absPaths)
	}
	var once sync.Once
	release := func() {
		once.Do(baseRelease)
	}
	peerModified := func(absPath string) bool {
		if len(coord.ModifiedByPeer) == 0 {
			return false
		}
		if coord.ModifiedByPeer[absPath] {
			return true
		}
		return coord.ModifiedByPeer[canonicalRevisionKey(absPath)]
	}
	return release, peerModified, nil
}

func checkEditWithPeerGuard(
	r *FileRevisionTracker,
	target, displayPath string,
	currentDiskBytes []byte,
	peerModified bool,
) error {
	if err := r.CheckEdit(target, displayPath, currentDiskBytes); err != nil {
		return err
	}
	if peerModified && !r.HasReadDigest(target, sha256.Sum256(currentDiskBytes)) {
		label := displayFileLabel(displayPath, target)
		return fmt.Errorf(
			"file %q was modified on disk by another concurrent session while this task was running; call read on %q first to inspect the latest changes before editing",
			label,
			label,
		)
	}
	return nil
}

func recordCompletedFileEdit(tc ToolContext, paths ...string) {
	if tc.RecordFileEdit != nil && len(paths) > 0 {
		tc.RecordFileEdit(paths)
	}
}

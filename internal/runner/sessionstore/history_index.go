package sessionstore

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const HistoryIndexFile = "turns.index.json"
const historyIndexFile = HistoryIndexFile
const maxHistoryIndexBytes = 16 << 20

// Offsets retain all upserts of a selected message, not just its last part.
// Replaying those entries preserves edit-input fallback, ContinueTask and
// same-step reasoning order exactly as a full JSONL replay would.
type historyMessageIndex struct {
	ID      string  `json:"id"`
	Offsets []int64 `json:"offsets"`
}

type historyLogIndex struct {
	Version       int                   `json:"version"`
	Size          int64                 `json:"size"`
	ModTime       int64                 `json:"modTime"`
	Messages      []historyMessageIndex `json:"messages"`
	EditsComplete bool                  `json:"editsComplete,omitempty"`
	EditedPaths   []string              `json:"editedPaths,omitempty"`
	ChildIDs      []string              `json:"childIDs,omitempty"`
	byID          map[string]int
	editPaths     map[string]bool
	childIDs      map[string]bool
}

func (idx *historyLogIndex) note(infoID, partMessageID string, offset int64, isInfo bool) {
	if idx.byID == nil {
		idx.byID = make(map[string]int)
	}
	if isInfo {
		i, ok := idx.byID[infoID]
		if !ok {
			i = len(idx.Messages)
			idx.byID[infoID] = i
			idx.Messages = append(idx.Messages, historyMessageIndex{ID: infoID})
		}
		idx.Messages[i].Offsets = append(idx.Messages[i].Offsets, offset)
	} else if i, ok := idx.byID[partMessageID]; ok {
		idx.Messages[i].Offsets = append(idx.Messages[i].Offsets, offset)
	}
}

func (idx *historyLogIndex) noteEntry(entry turnLogEntry, offset int64) {
	if entry.Info != nil {
		idx.note(entry.Info.ID, "", offset, true)
	} else if entry.Part != nil {
		idx.note("", entry.Part.MessageID, offset, false)
		if idx.EditsComplete {
			idx.noteEditSummary(*entry.Part)
		}
	}
}

func (idx *historyLogIndex) noteEditSummary(part MessagePart) {
	if part.Type != "tool" || part.State == nil {
		return
	}
	if idx.editPaths == nil {
		idx.editPaths = make(map[string]bool)
		idx.childIDs = make(map[string]bool)
	}
	if IsRunnerTaskTool(part.Tool) {
		for _, id := range ExtractChildSessionIDsFromPart(part) {
			if !idx.childIDs[id] {
				idx.childIDs[id] = true
				idx.ChildIDs = append(idx.ChildIDs, id)
			}
		}
	} else if part.State.Status == "completed" && IsRunnerEditingTool(part.Tool) {
		for _, path := range ExtractEditedPathsFromPart(part) {
			if !idx.editPaths[path] {
				idx.editPaths[path] = true
				idx.EditedPaths = append(idx.EditedPaths, path)
			}
		}
	}
}

func (s *Store) readIndexedEditSummary(rec SessionRecord) ([]string, []string, error) {
	f, err := os.Open(filepath.Join(s.sessionDir(rec.ID), sessionTurnsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	idx, err := s.historyIndex(rec.ID, f)
	if err != nil {
		return nil, nil, err
	}
	if !idx.EditsComplete {
		// Legacy logs pay for one transient replay to recover input-fallback
		// semantics. Newly written canonical logs already carry this summary.
		turns, _ := s.readTurns(rec.ID)
		hydrateEditParts(rec.ExternalID, turns)
		idx.EditedPaths, idx.ChildIDs = nil, nil
		for _, turn := range turns {
			for _, part := range turn.Parts {
				idx.noteEditSummary(part)
			}
		}
		idx.EditsComplete = true
		if info, err := f.Stat(); err == nil {
			recordPersistenceError(s.saveHistoryIndex(rec.ID, idx, info))
		}
	}
	paths := make([]string, 0, len(idx.EditedPaths))
	for _, path := range idx.EditedPaths {
		if path = NormalizeSessionRelPath(path, rec.ProjectDir, rec.WorktreePath); path != "" {
			paths = append(paths, path)
		}
	}
	return paths, idx.ChildIDs, nil
}

func validHistoryIndex(idx *historyLogIndex) bool {
	if idx.Version != 1 || idx.Size < 0 {
		return false
	}
	seen := make(map[string]bool, len(idx.Messages))
	previousFirst := int64(-1)
	for _, message := range idx.Messages {
		if seen[message.ID] || len(message.Offsets) == 0 || message.Offsets[0] <= previousFirst {
			return false
		}
		seen[message.ID], previousFirst = true, message.Offsets[0]
		previous := int64(-1)
		for _, offset := range message.Offsets {
			if offset <= previous || offset >= idx.Size {
				return false
			}
			previous = offset
		}
	}
	return true
}

func (s *Store) loadHistoryIndex(id string, info os.FileInfo) (*historyLogIndex, error) {
	path := filepath.Join(s.sessionDir(id), historyIndexFile)
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxHistoryIndexBytes+1))
	_ = f.Close()
	if err != nil || len(raw) > maxHistoryIndexBytes {
		return nil, err
	}
	var idx historyLogIndex
	if json.Unmarshal(raw, &idx) != nil || !validHistoryIndex(&idx) {
		// This is a disposable index, but preserve malformed state before replacing it.
		if err := os.Rename(path, path+".corrupt-"+time.Now().UTC().Format("20060102T150405.000000000")); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if idx.Size != info.Size() || idx.ModTime != info.ModTime().UnixNano() {
		return nil, nil
	}
	return &idx, nil
}

func (s *Store) saveHistoryIndex(id string, idx *historyLogIndex, source os.FileInfo) error {
	info, err := os.Stat(filepath.Join(s.sessionDir(id), sessionTurnsFile))
	if err != nil {
		return err
	}
	if !os.SameFile(info, source) || info.Size() != source.Size() || !info.ModTime().Equal(source.ModTime()) {
		return nil // The log changed/replaced while building a disposable index.
	}
	idx.Version, idx.Size, idx.ModTime = 1, source.Size(), source.ModTime().UnixNano()
	raw, err := json.Marshal(idx)
	if err != nil || len(raw) > maxHistoryIndexBytes {
		return err
	}
	return writeFileAtomic(filepath.Join(s.sessionDir(id), historyIndexFile), raw)
}

func buildHistoryIndex(f *os.File) (*historyLogIndex, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	idx := &historyLogIndex{Version: 1}
	var offset int64
	for line, err := range turnLogLines(f) {
		if err != nil {
			return nil, err
		}
		var probe struct {
			Info *struct {
				ID string `json:"id"`
			} `json:"info"`
			Part *struct {
				MessageID string `json:"messageID"`
			} `json:"part"`
		}
		if json.Unmarshal(line, &probe) == nil {
			if probe.Info != nil {
				idx.note(probe.Info.ID, "", offset, true)
			} else if probe.Part != nil {
				idx.note("", probe.Part.MessageID, offset, false)
			}
		}
		offset += int64(len(line))
	}
	return idx, nil
}

// Called under this session's lock (and its disk lock when a writer is active).
// The JSONL remains the source of truth: missing/stale indexes are rebuilt once.
func (s *Store) historyIndex(id string, f *os.File) (*historyLogIndex, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	loaded, loadErr := s.loadHistoryIndex(id, info)
	if idx := loaded; idx != nil {
		return idx, nil
	}
	idx, err := buildHistoryIndex(f)
	if err != nil {
		return nil, err
	}
	// Failure to cache an index must not make an otherwise readable history fail.
	if loadErr == nil {
		recordPersistenceError(s.saveHistoryIndex(id, idx, info))
	}
	return idx, nil
}

func (s *Store) readIndexedHistoryPage(id string, page HistoryPage) ([]MessageTurn, error) {
	f, err := os.Open(filepath.Join(s.sessionDir(id), sessionTurnsFile))
	if errors.Is(err, os.ErrNotExist) {
		return []MessageTurn{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	idx, err := s.historyIndex(id, f)
	if err != nil {
		return nil, err
	}
	end := len(idx.Messages)
	if page.Before != "" {
		end = -1
		for i, message := range idx.Messages {
			if message.ID == page.Before {
				end = i
				break
			}
		}
		if end < 0 {
			return []MessageTurn{}, nil
		}
	}
	start := max(0, end-page.Limit)
	type recordOffset struct {
		offset    int64
		messageID string
	}
	var offsets []recordOffset
	for _, message := range idx.Messages[start:end] {
		for _, offset := range message.Offsets {
			offsets = append(offsets, recordOffset{offset, message.ID})
		}
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i].offset < offsets[j].offset })
	var turns []MessageTurn
	var transcript transcriptIndex
	reader := bufio.NewReaderSize(f, 64*1024)
	for _, offset := range offsets {
		if _, err := f.Seek(offset.offset, io.SeekStart); err != nil {
			return nil, err
		}
		reader.Reset(f)
		line, err := reader.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		var entry turnLogEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, err
		}
		if entry.Info != nil {
			if entry.Info.ID != offset.messageID {
				return nil, errors.New("history index message mismatch")
			}
			transcript.upsertInfo(&turns, *entry.Info)
		} else if entry.Part != nil {
			if entry.Part.MessageID != offset.messageID {
				return nil, errors.New("history index part mismatch")
			}
			transcript.upsertPart(turns, *entry.Part)
		}
	}
	for i := range turns {
		turns[i].Parts = pruneDuplicateToolParts(dedupeClaudeStreamParts(turns[i].Parts))
	}
	return turns, nil
}

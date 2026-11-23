package agent

import (
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"strings"
	"sync"
)

const (
	maxTrackedSubtaskSessions   = 64
	maxSavedSubtasksPerSession  = 16
	maxSavedSubtaskMessageCount = 256
)

type savedSubtaskState struct {
	ID            string
	SubagentType  string
	ReadOnly      bool
	ProviderID    string
	ModelID       string
	WorktreeDir   string
	Messages      []Message
	FileRevisions *FileRevisionTracker
}

type sessionSubtaskBucket struct {
	order            []string
	items            map[string]savedSubtaskState
	latestExecutorID string
}

var (
	subtaskStateMu       sync.Mutex
	subtaskStateSessions = make(map[string]*sessionSubtaskBucket)
	subtaskStateOrder    []string
)

func nextSubtaskID() string {
	var raw [4]byte
	_, _ = rand.Read(raw[:])
	return "sub_" + hex.EncodeToString(raw[:])
}

func messageHasToolUse(m Message) bool {
	for _, b := range m.Content {
		if b.Type == ContentToolUse {
			return true
		}
	}
	return false
}

func cloneMessagesForSubtask(msgs []Message) []Message {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		out[i].Role = m.Role
		if len(m.Content) > 0 {
			out[i].Content = make([]ContentBlock, len(m.Content))
			for j, b := range m.Content {
				cp := b
				if len(b.Input) > 0 {
					cp.Input = append([]byte(nil), b.Input...)
				}
				out[i].Content[j] = cp
			}
		}
	}
	return out
}

func saveSessionSubtask(sessionID string, st savedSubtaskState) bool {
	sid := strings.TrimSpace(sessionID)
	id := strings.TrimSpace(st.ID)
	if sid == "" || id == "" || len(st.Messages) == 0 || len(st.Messages) > maxSavedSubtaskMessageCount {
		return false
	}
	last := st.Messages[len(st.Messages)-1]
	if last.Role != RoleAssistant || messageHasToolUse(last) {
		return false
	}
	st.ID = id
	st.WorktreeDir = filepath.Clean(strings.TrimSpace(st.WorktreeDir))
	st.Messages = cloneMessagesForSubtask(st.Messages)
	if st.FileRevisions != nil {
		st.FileRevisions = st.FileRevisions.Clone()
	}

	subtaskStateMu.Lock()
	defer subtaskStateMu.Unlock()

	bucket, exists := subtaskStateSessions[sid]
	if !exists {
		for len(subtaskStateOrder) >= maxTrackedSubtaskSessions {
			oldest := subtaskStateOrder[0]
			subtaskStateOrder = subtaskStateOrder[1:]
			delete(subtaskStateSessions, oldest)
		}
		bucket = &sessionSubtaskBucket{items: make(map[string]savedSubtaskState)}
		subtaskStateSessions[sid] = bucket
		subtaskStateOrder = append(subtaskStateOrder, sid)
	}
	if _, already := bucket.items[id]; !already {
		for len(bucket.order) >= maxSavedSubtasksPerSession {
			oldestID := bucket.order[0]
			bucket.order = bucket.order[1:]
			delete(bucket.items, oldestID)
		}
		bucket.order = append(bucket.order, id)
	}
	bucket.items[id] = st
	if strings.EqualFold(st.SubagentType, "executor") {
		bucket.latestExecutorID = id
	}
	return true
}

func loadLatestSessionExecutor(sessionID, providerID, modelID, worktreeDir string) (savedSubtaskState, bool) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return savedSubtaskState{}, false
	}
	subtaskStateMu.Lock()
	defer subtaskStateMu.Unlock()
	bucket := subtaskStateSessions[sid]
	if bucket == nil || bucket.latestExecutorID == "" {
		return savedSubtaskState{}, false
	}
	st, ok := bucket.items[bucket.latestExecutorID]
	if !ok {
		return savedSubtaskState{}, false
	}
	if strings.TrimSpace(providerID) != "" && st.ProviderID != strings.TrimSpace(providerID) {
		return savedSubtaskState{}, false
	}
	if strings.TrimSpace(modelID) != "" && st.ModelID != strings.TrimSpace(modelID) {
		return savedSubtaskState{}, false
	}
	cleanDir := filepath.Clean(strings.TrimSpace(worktreeDir))
	if st.WorktreeDir != "" && cleanDir != "" && st.WorktreeDir != cleanDir {
		return savedSubtaskState{}, false
	}
	st.Messages = cloneMessagesForSubtask(st.Messages)
	if st.FileRevisions != nil {
		st.FileRevisions = st.FileRevisions.Clone()
	}
	return st, true
}

func loadSessionSubtask(sessionID, subtaskID, worktreeDir string) (savedSubtaskState, bool) {
	sid := strings.TrimSpace(sessionID)
	id := strings.TrimSpace(subtaskID)
	if sid == "" || id == "" {
		return savedSubtaskState{}, false
	}
	subtaskStateMu.Lock()
	defer subtaskStateMu.Unlock()
	bucket := subtaskStateSessions[sid]
	if bucket == nil {
		return savedSubtaskState{}, false
	}
	st, ok := bucket.items[id]
	if !ok {
		return savedSubtaskState{}, false
	}
	cleanDir := filepath.Clean(strings.TrimSpace(worktreeDir))
	if st.WorktreeDir != "" && cleanDir != "" && st.WorktreeDir != cleanDir {
		return savedSubtaskState{}, false
	}
	st.Messages = cloneMessagesForSubtask(st.Messages)
	if st.FileRevisions != nil {
		st.FileRevisions = st.FileRevisions.Clone()
	}
	return st, true
}

// DeleteSessionSubtasks clears in-memory resumable subagent transcripts for sessionID.
func DeleteSessionSubtasks(sessionID string) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return
	}
	subtaskStateMu.Lock()
	defer subtaskStateMu.Unlock()
	if _, ok := subtaskStateSessions[sid]; !ok {
		return
	}
	delete(subtaskStateSessions, sid)
	for i, k := range subtaskStateOrder {
		if k == sid {
			subtaskStateOrder = append(subtaskStateOrder[:i], subtaskStateOrder[i+1:]...)
			break
		}
	}
}

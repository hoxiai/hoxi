package sessionstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

const UsageSummaryFile = "turns.usage.json"
const usageSummaryFile = UsageSummaryFile
const usageSummaryVersion = 2
const maxUsageSummaryBytes = 4 << 20

var (
	usageInfoKeyBytes = []byte(`"info"`)
	usageToolKeyBytes = []byte(`"tool"`)
)

// TurnUsageSummary holds the compact assistant-turn accounting data needed by
// devpulse rollups without retaining transcript text or tool payloads.
type TurnUsageSummary struct {
	ID              string         `json:"id"`
	Role            string         `json:"role"`
	ProviderID      string         `json:"providerID,omitempty"`
	ModelID         string         `json:"modelID,omitempty"`
	Created         int64          `json:"created,omitempty"`
	Completed       int64          `json:"completed,omitempty"`
	Imported        bool           `json:"imported,omitempty"`
	Cost            float64        `json:"cost,omitempty"`
	Tokens          *MessageTokens `json:"tokens,omitempty"`
	ToolCallsTotal  int            `json:"toolCallsTotal,omitempty"`
	ToolCallsFailed int            `json:"toolCallsFailed,omitempty"`
}

type usageSummaryCache struct {
	Version int                `json:"version"`
	Size    int64              `json:"size"`
	ModTime int64              `json:"modTime"`
	Turns   []TurnUsageSummary `json:"turns"`
}

func summarizeTurnsUsage(turns []MessageTurn) []TurnUsageSummary {
	if len(turns) == 0 {
		return nil
	}
	var out []TurnUsageSummary
	for i := range turns {
		info := turns[i].Info
		if info.Role != "assistant" {
			continue
		}
		var toolsTotal, toolsFailed int
		for j := range turns[i].Parts {
			p := &turns[i].Parts[j]
			if p.Type != "tool" {
				continue
			}
			toolsTotal++
			if p.State != nil && p.State.Status == "error" {
				toolsFailed++
			}
		}
		var tok *MessageTokens
		if info.Tokens != nil {
			cp := *info.Tokens
			tok = &cp
		}
		out = append(out, TurnUsageSummary{
			ID:              info.ID,
			Role:            info.Role,
			ProviderID:      info.ProviderID,
			ModelID:         info.ModelID,
			Created:         info.Time.Created,
			Completed:       info.Time.Completed,
			Imported:        info.Imported,
			Cost:            info.Cost,
			Tokens:          tok,
			ToolCallsTotal:  toolsTotal,
			ToolCallsFailed: toolsFailed,
		})
	}
	return out
}

func scanTurnsUsage(f *os.File) ([]TurnUsageSummary, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	type usagePartProbe struct {
		ID        string `json:"id"`
		MessageID string `json:"messageID"`
		Type      string `json:"type"`
		State     *struct {
			Status string `json:"status"`
		} `json:"state"`
	}
	type usageLogEntry struct {
		Info *MessageInfo    `json:"info"`
		Part *usagePartProbe `json:"part"`
	}

	infos := make(map[string]MessageInfo)
	order := make([]string, 0, 16)
	toolsByMsg := make(map[string]map[string]bool)

	for raw, err := range turnLogLines(f) {
		if err != nil {
			return nil, err
		}
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		// Skip text/reasoning/file parts without JSON unmarshaling.
		if !bytes.Contains(line, usageInfoKeyBytes) && !bytes.Contains(line, usageToolKeyBytes) {
			continue
		}
		var entry usageLogEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			// A crash can leave an unfinished final append after the last full turn.
			if raw[len(raw)-1] != '\n' {
				continue
			}
			return nil, err
		}
		switch {
		case entry.Info != nil:
			id := entry.Info.ID
			if _, exists := infos[id]; !exists {
				order = append(order, id)
			}
			infos[id] = *entry.Info
		case entry.Part != nil:
			msgID := entry.Part.MessageID
			if _, exists := infos[msgID]; !exists {
				continue
			}
			if entry.Part.Type == "tool" {
				if toolsByMsg[msgID] == nil {
					toolsByMsg[msgID] = make(map[string]bool)
				}
				isErr := entry.Part.State != nil && entry.Part.State.Status == "error"
				toolsByMsg[msgID][entry.Part.ID] = isErr
			} else if m := toolsByMsg[msgID]; m != nil {
				delete(m, entry.Part.ID)
			}
		}
	}

	var out []TurnUsageSummary
	for _, id := range order {
		info := infos[id]
		if info.Role != "assistant" {
			continue
		}
		var toolsTotal, toolsFailed int
		for _, failed := range toolsByMsg[id] {
			toolsTotal++
			if failed {
				toolsFailed++
			}
		}
		var tok *MessageTokens
		if info.Tokens != nil {
			cp := *info.Tokens
			tok = &cp
		}
		out = append(out, TurnUsageSummary{
			ID:              info.ID,
			Role:            info.Role,
			ProviderID:      info.ProviderID,
			ModelID:         info.ModelID,
			Created:         info.Time.Created,
			Completed:       info.Time.Completed,
			Imported:        info.Imported,
			Cost:            info.Cost,
			Tokens:          tok,
			ToolCallsTotal:  toolsTotal,
			ToolCallsFailed: toolsFailed,
		})
	}
	return out, nil
}

func (s *Store) loadUsageSummary(id string, info os.FileInfo) ([]TurnUsageSummary, bool, error) {
	path := filepath.Join(s.sessionDir(id), usageSummaryFile)
	f, err := os.Open(path)
	if err != nil {
		return nil, false, nil
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxUsageSummaryBytes+1))
	_ = f.Close()
	if err != nil || len(raw) > maxUsageSummaryBytes {
		return nil, false, err
	}
	var cache usageSummaryCache
	if json.Unmarshal(raw, &cache) != nil || cache.Version <= 0 || cache.Size < 0 {
		if err := os.Rename(path, path+".corrupt-"+time.Now().UTC().Format("20060102T150405.000000000")); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	if cache.Version != usageSummaryVersion {
		return nil, false, nil
	}
	if cache.Size != info.Size() || cache.ModTime != info.ModTime().UnixNano() {
		return nil, false, nil
	}
	return cache.Turns, true, nil
}

func (s *Store) saveUsageSummary(id string, turns []TurnUsageSummary, source os.FileInfo) error {
	info, err := os.Stat(filepath.Join(s.sessionDir(id), sessionTurnsFile))
	if err != nil {
		return err
	}
	if !os.SameFile(info, source) || info.Size() != source.Size() || !info.ModTime().Equal(source.ModTime()) {
		return nil
	}
	cache := usageSummaryCache{
		Version: usageSummaryVersion,
		Size:    source.Size(),
		ModTime: source.ModTime().UnixNano(),
		Turns:   turns,
	}
	raw, err := json.Marshal(cache)
	if err != nil || len(raw) > maxUsageSummaryBytes {
		return err
	}
	return writeFileAtomic(filepath.Join(s.sessionDir(id), usageSummaryFile), raw)
}

// VisitUsageTurns supplies lightweight per-turn token and tool counts for
// usage/history rollups without hydrating full transcripts, running Antigravity
// reasoning backfills. Cold usage and transcript summaries share a bounded LRU.
func (s *Store) VisitUsageTurns(sessionID string, fn func(meta SessionRecord, turns []TurnUsageSummary)) error {
	ss := s.lockSession(sessionID, false)
	if ss == nil {
		return nil
	}
	defer func() {
		cached := ss.usageCached
		if cached {
			s.mu.Lock()
			s.accountSummaryLocked(ss)
			s.mu.Unlock()
		}
		ss.mu.Unlock()
		if cached {
			s.trimSummaryCache()
		}
	}()

	if ss.turnsLoaded && !ss.turnsIncomplete {
		fn(ss.rec, summarizeTurnsUsage(ss.rec.Turns))
		return nil
	}
	if ss.usageCached {
		fn(ss.rec, ss.usageTurns)
		return nil
	}

	f, err := os.Open(filepath.Join(s.sessionDir(ss.rec.ID), sessionTurnsFile))
	if errors.Is(err, os.ErrNotExist) {
		ss.usageTurns = nil
		ss.usageCached = true
		fn(ss.rec, nil)
		return nil
	}
	if err != nil {
		return err
	}
	info, statErr := f.Stat()
	if statErr != nil {
		_ = f.Close()
		return statErr
	}
	cached, ok, loadErr := s.loadUsageSummary(ss.rec.ID, info)
	if ok {
		_ = f.Close()
		ss.usageTurns = cached
		ss.usageCached = true
		fn(ss.rec, cached)
		return nil
	}
	turns, scanErr := scanTurnsUsage(f)
	_ = f.Close()
	if scanErr != nil {
		return scanErr
	}
	if loadErr == nil {
		recordPersistenceError(s.saveUsageSummary(ss.rec.ID, turns, info))
	}
	ss.usageTurns = turns
	ss.usageCached = true
	fn(ss.rec, turns)
	return nil
}

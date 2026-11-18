package sessionstore

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// MaxHistoryPageTurns caps one paged history response; callers that need the
// whole transcript omit `limit` instead of asking for a huge page.
const MaxHistoryPageTurns = 2000
const maxHistoryPageTurns = MaxHistoryPageTurns

// HistoryPage selects a window of GET /api/hoxi/runner/sessions/{id}/message.
// Without `limit` the full transcript is returned (CLI and mobile rely on
// that). `limit=N` returns the newest N turns; `before=<messageID>` pages
// backwards from that message so the desktop can lazy-load older history.
type HistoryPage struct {
	Limit  int
	Before string
}

func ParseHistoryPage(q url.Values) HistoryPage {
	page := HistoryPage{Before: strings.TrimSpace(q.Get("before"))}
	if n, err := strconv.Atoi(strings.TrimSpace(q.Get("limit"))); err == nil && n > 0 {
		page.Limit = min(n, MaxHistoryPageTurns)
	}
	return page
}

// SliceHistoryPage returns the requested window without copying turns. An
// unknown `before` cursor yields an empty page: the caller's oldest turn no
// longer exists (reverted or compacted), so there is nothing older to append.
func SliceHistoryPage(turns []MessageTurn, page HistoryPage) []MessageTurn {
	end := len(turns)
	if page.Before != "" {
		end = -1
		for i := range turns {
			if turns[i].Info.ID == page.Before {
				end = i
				break
			}
		}
		if end < 0 {
			return []MessageTurn{}
		}
	}
	start := 0
	if page.Limit > 0 && end > page.Limit {
		start = end - page.Limit
	}
	return turns[start:end]
}

// MarshalTurnsPage serializes one history window holding only this session's
// lock, so a large history response never blocks streaming writes or metadata
// reads for other conversations. Shell tools persist their output twice
// (`state.output` and `state.metadata.output`); identical copies are dropped
// from the response only, since clients fall back to `state.output` for
// completed tools.
func (s *Store) MarshalTurnsPage(sessionID string, page HistoryPage) ([]byte, bool) {
	ss := s.lockSession(sessionID, false)
	if ss == nil {
		return nil, false
	}
	var turns []MessageTurn
	if page.Limit > 0 && !ss.turnsLoaded {
		var err error
		ss.diskMu.Lock()
		s.applyPersistResultLocked(ss)
		turns, err = s.readIndexedHistoryPage(sessionID, page)
		ss.diskMu.Unlock()
		if err != nil {
			// Index/cache failures retain the legacy recovery path.
			s.ensureTurnsLocked(ss)
			turns = cloneTurns(SliceHistoryPage(ss.rec.Turns, page))
		} else {
			hydrateEditParts(ss.rec.ExternalID, turns)
			hydrateTurnTokens(ss.rec.ExternalID, turns)
		}
	} else {
		s.ensureTurnsLocked(ss)
		turns = cloneTurns(SliceHistoryPage(ss.rec.Turns, page))
	}
	s.finishSession(ss)
	turns = dedupeToolOutputs(turns)
	if turns == nil {
		turns = []MessageTurn{}
	}
	raw, err := json.Marshal(turns)
	if err != nil {
		return []byte("[]"), true
	}
	return raw, true
}

// dedupeToolOutputs copies only the turns and parts it changes, so the stored
// record is never mutated.
func dedupeToolOutputs(turns []MessageTurn) []MessageTurn {
	var out []MessageTurn
	for i := range turns {
		var parts []MessagePart
		for j := range turns[i].Parts {
			trimmed, ok := withoutDuplicateToolOutput(turns[i].Parts[j])
			if !ok {
				continue
			}
			if parts == nil {
				parts = append([]MessagePart(nil), turns[i].Parts...)
			}
			parts[j] = trimmed
		}
		if parts == nil {
			continue
		}
		if out == nil {
			out = append([]MessageTurn(nil), turns...)
		}
		out[i].Parts = parts
	}
	if out == nil {
		return turns
	}
	return out
}

func withoutDuplicateToolOutput(part MessagePart) (MessagePart, bool) {
	state := part.State
	if part.Type != "tool" || state == nil || state.Status != "completed" || state.Output == "" {
		return part, false
	}
	if metaOut, ok := state.Metadata["output"].(string); !ok || metaOut != state.Output {
		return part, false
	}
	meta := make(map[string]interface{}, len(state.Metadata)-1)
	for k, v := range state.Metadata {
		if k != "output" {
			meta[k] = v
		}
	}
	nextState := *state
	nextState.Metadata = meta
	part.State = &nextState
	return part, true
}

package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/opencode/ocstate"
)

const (
	internalRecoveryMsgPrefix      = "msg_u_recover_"
	maxOpenCodeReasoningRecoveries = 2
	reasoningDeltaTailLimit        = 1024
	runawayClosingTagThreshold     = 6
	runawayDuplicateToolThreshold  = 3
	maxOpenCodeSSELineBytes        = 16 << 20
	postTurnSnapshotLimit          = 50
	reasoningCheckSnapshotLimit    = 20
)

// scanOpenCodeSSEDataLines reads SSE lines from r and invokes onData with the
// trimmed payload of each "data:" line. Unlike bufio.Scanner, a single line
// exceeding maxOpenCodeSSELineBytes (for example a multi-tens-of-MB
// session.diff event) is drained to the next newline without terminating the
// stream for subsequent events.
func scanOpenCodeSSEDataLines(r io.Reader, onData func([]byte)) {
	br := bufio.NewReaderSize(r, 64*1024)
	var lineBuf bytes.Buffer
	oversized := false
	flushLine := func(raw []byte) {
		line := strings.TrimSpace(strings.TrimRight(string(raw), "\r\n"))
		if strings.HasPrefix(line, "data:") {
			onData([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))))
		}
	}
	for {
		frag, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			if !oversized {
				if lineBuf.Len()+len(frag) <= maxOpenCodeSSELineBytes {
					_, _ = lineBuf.Write(frag)
				} else {
					lineBuf.Reset()
					oversized = true
				}
			}
			continue
		}
		if len(frag) > 0 {
			if !oversized {
				if lineBuf.Len() == 0 && len(frag) <= maxOpenCodeSSELineBytes {
					flushLine(frag)
				} else if lineBuf.Len()+len(frag) <= maxOpenCodeSSELineBytes {
					_, _ = lineBuf.Write(frag)
					flushLine(lineBuf.Bytes())
					lineBuf.Reset()
				} else {
					lineBuf.Reset()
				}
			}
			oversized = false
		}
		if err != nil {
			return
		}
	}
}

func (s *Service) fetchOpenCodeRecentMessages(ctx context.Context, sessionID, directory string, limit int) ([]MessageTurn, error) {
	endpoint := "/session/" + sessionID + "/message"
	if limit > 0 {
		endpoint = fmt.Sprintf("%s?limit=%d", endpoint, limit)
	}
	var turns []MessageTurn
	err := s.openCodeJSON(ctx, "GET", endpoint, directory, nil, &turns)
	if err != nil && (errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(err.Error(), "unexpected EOF")) {
		if dbPath := hoxipaths.DBPath(); dbPath != "" {
			_ = ocstate.PruneOversizedMessageDiffs(ctx, dbPath)
		}
		turns = nil
		err = s.openCodeJSON(ctx, "GET", endpoint, directory, nil, &turns)
	}
	return turns, err
}

const openCodeReasoningRecoveryPrompt = "Your previous step stopped inside reasoning without emitting a tool call or visible reply (or emitted raw <invoke>/<parameter> tags inside reasoning). Continue from where you left off: either invoke the next tool via standard tool calls (never write <invoke> or <parameter> XML inside reasoning) or output your visible reply directly."

// IsInternalRecoveryMessageID reports whether id is a synthetic user message
// injected by Hoxi to auto-continue an interrupted OpenCode reasoning step.
func IsInternalRecoveryMessageID(id string) bool {
	return strings.HasPrefix(strings.TrimSpace(id), internalRecoveryMsgPrefix)
}

func isInternalRecoveryMessageID(id string) bool {
	return IsInternalRecoveryMessageID(id)
}

var leakedReasoningOpenMarkers = []string{
	"<｜DSML｜",
	"</｜DSML｜",
	"<invoke ",
	"<invoke>",
	"<parameter ",
	"<tool_call>",
	"<tool_call ",
}

var leakedReasoningCloseMarkers = []string{
	"</invoke>",
	"</parameter>",
	"</tool_call>",
	"</｜DSML｜",
	"<｜DSML｜",
}

// stripLeakedReasoningToolMarkup removes raw DSML / XML tool-call payloads and
// runaway closing-tag repetition loops (`</invoke>\n</parameter>...`) that some
// OpenAI-compatible reasoning proxies leak into `reasoning_content`.
func stripLeakedReasoningToolMarkup(raw string) (string, bool) {
	if raw == "" {
		return raw, false
	}
	hasClose := false
	for _, m := range leakedReasoningCloseMarkers {
		if strings.Contains(raw, m) {
			hasClose = true
			break
		}
	}
	if !hasClose {
		return raw, false
	}

	cutIdx := -1
	noteCandidate := func(idx int) {
		if idx >= 0 && (cutIdx < 0 || idx < cutIdx) {
			cutIdx = idx
		}
	}
	for _, m := range leakedReasoningOpenMarkers {
		noteCandidate(strings.Index(raw, m))
	}
	if hasRunawayReasoningToolLoop(raw) {
		for _, m := range leakedReasoningCloseMarkers {
			noteCandidate(strings.Index(raw, m))
		}
	}
	if cutIdx < 0 {
		return raw, false
	}

	head := strings.TrimRight(raw[:cutIdx], " \t\r\n")
	if head == "---" {
		head = ""
	} else if strings.HasSuffix(head, "\n---") {
		head = strings.TrimRight(strings.TrimSuffix(head, "\n---"), " \t\r\n")
	}
	return head, true
}

// hasRunawayReasoningToolLoop detects degenerate repetition of closing tool-call
// tags (`</invoke>`, `</parameter>`, `</tool_call>`, `</｜DSML｜...>`) separated
// only by whitespace inside a reasoning stream.
func hasRunawayReasoningToolLoop(text string) bool {
	if len(text) < 40 {
		return false
	}
	if !strings.Contains(text, "</invoke>") && !strings.Contains(text, "</parameter>") &&
		!strings.Contains(text, "</tool_call>") && !strings.Contains(text, "</｜DSML｜") {
		return false
	}
	consecutive := 0
	i := 0
	for i < len(text) {
		for i < len(text) && (text[i] == ' ' || text[i] == '\t' || text[i] == '\n' || text[i] == '\r') {
			i++
		}
		if i >= len(text) {
			break
		}
		if tagLen := matchClosingToolTagAt(text[i:]); tagLen > 0 {
			consecutive++
			if consecutive >= runawayClosingTagThreshold {
				return true
			}
			i += tagLen
			continue
		}
		consecutive = 0
		next := strings.IndexByte(text[i+1:], '<')
		if next < 0 {
			break
		}
		i += 1 + next
	}
	return false
}

func matchClosingToolTagAt(s string) int {
	for _, literal := range []string{"</invoke>", "</parameter>", "</tool_call>"} {
		if strings.HasPrefix(s, literal) {
			return len(literal)
		}
	}
	if strings.HasPrefix(s, "</｜DSML｜") {
		if end := strings.IndexByte(s, '>'); end > 0 && end <= 48 {
			return end + 1
		}
	}
	return 0
}

type openCodeReasoningGuard struct {
	mu              sync.Mutex
	partTails       map[string]string
	partTypes       map[string]string
	toolPartFP      map[string]string
	toolPartMsg     map[string]string
	msgToolCounts   map[string]map[string]int
	msgFirstToolID  map[string]map[string]string
	droppedPartIDs  map[string]bool
	triggeredMsgs   map[string]bool
	pendingRecovery bool
}

func (g *openCodeReasoningGuard) observeDelta(msgID, partID, delta string) (newlyTriggered bool, dropDelta bool) {
	if delta == "" {
		return false, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if msgID != "" && g.triggeredMsgs[msgID] {
		return false, true
	}
	if partID != "" && g.partTypes != nil {
		if pt := g.partTypes[partID]; pt != "" && pt != "reasoning" {
			return false, false
		}
	}
	if g.partTails == nil {
		g.partTails = make(map[string]string)
	}
	key := partID
	if key == "" {
		key = msgID
	}
	tail := g.partTails[key] + delta
	if len(tail) > reasoningDeltaTailLimit {
		cut := len(tail) - reasoningDeltaTailLimit
		for cut < len(tail) && !utf8.RuneStart(tail[cut]) {
			cut++
		}
		tail = tail[cut:]
	}
	g.partTails[key] = tail
	if !hasRunawayReasoningToolLoop(tail) {
		return false, false
	}
	if g.triggeredMsgs == nil {
		g.triggeredMsgs = make(map[string]bool)
	}
	if msgID != "" {
		g.triggeredMsgs[msgID] = true
	}
	g.pendingRecovery = true
	return true, true
}

func (g *openCodeReasoningGuard) observePart(part MessagePart) (newlyTriggered bool, dropPart bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if part.ID != "" && part.Type != "" {
		if g.partTypes == nil {
			g.partTypes = make(map[string]string)
		}
		g.partTypes[part.ID] = part.Type
	}
	if part.Type == "tool" && part.ID != "" && part.MessageID != "" {
		if g.droppedPartIDs[part.ID] {
			return false, true
		}
		fp := canonicalToolPartFingerprint(part)
		if fp == "" {
			if g.triggeredMsgs[part.MessageID] && (part.State == nil || len(part.State.Input) == 0) {
				if g.droppedPartIDs == nil {
					g.droppedPartIDs = make(map[string]bool)
				}
				g.droppedPartIDs[part.ID] = true
				return false, true
			}
			return false, false
		}
		if g.toolPartFP == nil {
			g.toolPartFP = make(map[string]string)
			g.toolPartMsg = make(map[string]string)
			g.msgToolCounts = make(map[string]map[string]int)
			g.msgFirstToolID = make(map[string]map[string]string)
			g.droppedPartIDs = make(map[string]bool)
		}
		if g.toolPartFP[part.ID] == "" {
			g.toolPartFP[part.ID] = fp
			g.toolPartMsg[part.ID] = part.MessageID
			if g.msgToolCounts[part.MessageID] == nil {
				g.msgToolCounts[part.MessageID] = make(map[string]int)
				g.msgFirstToolID[part.MessageID] = make(map[string]string)
			}
			count := g.msgToolCounts[part.MessageID][fp] + 1
			g.msgToolCounts[part.MessageID][fp] = count
			if count == 1 {
				g.msgFirstToolID[part.MessageID][fp] = part.ID
			}
			if count > runawayDuplicateToolThreshold {
				firstID := g.msgFirstToolID[part.MessageID][fp]
				g.droppedPartIDs[part.ID] = true
				for pid, pfp := range g.toolPartFP {
					if pfp == fp && g.toolPartMsg[pid] == part.MessageID && pid != firstID {
						g.droppedPartIDs[pid] = true
					}
				}
				if g.triggeredMsgs == nil {
					g.triggeredMsgs = make(map[string]bool)
				}
				newly := !g.triggeredMsgs[part.MessageID]
				g.triggeredMsgs[part.MessageID] = true
				g.pendingRecovery = true
				return newly, true
			}
		}
		return false, g.droppedPartIDs[part.ID]
	}
	if part.Type != "reasoning" || !hasRunawayReasoningToolLoop(part.Text) {
		return false, false
	}
	if g.triggeredMsgs == nil {
		g.triggeredMsgs = make(map[string]bool)
	}
	if part.MessageID != "" && g.triggeredMsgs[part.MessageID] {
		return false, false
	}
	if part.MessageID != "" {
		g.triggeredMsgs[part.MessageID] = true
	}
	g.pendingRecovery = true
	return true, false
}

func (g *openCodeReasoningGuard) hasTriggered() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pendingRecovery || len(g.triggeredMsgs) > 0
}

func (g *openCodeReasoningGuard) droppedPartsSnapshot() map[string]bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.droppedPartIDs) == 0 {
		return nil
	}
	out := make(map[string]bool, len(g.droppedPartIDs))
	for k, v := range g.droppedPartIDs {
		if v {
			out[k] = true
		}
	}
	return out
}

func (g *openCodeReasoningGuard) wasTriggered(msgID string) bool {
	if msgID == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.triggeredMsgs[msgID]
}

func (g *openCodeReasoningGuard) hasPendingRecovery() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pendingRecovery
}

func (g *openCodeReasoningGuard) consumePendingRecovery() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	was := g.pendingRecovery
	g.pendingRecovery = false
	return was
}

func (g *openCodeReasoningGuard) shouldSuppressAbortError(rawErr interface{}) bool {
	g.mu.Lock()
	hasTriggered := g.pendingRecovery || len(g.triggeredMsgs) > 0
	g.mu.Unlock()
	if !hasTriggered || rawErr == nil {
		return false
	}
	raw, err := json.Marshal(rawErr)
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(raw))
	return strings.Contains(lower, "abort")
}

func sanitizeRunawayTriggeredInfo(info *MessageInfo, ap *activeProcess) {
	if info == nil || ap == nil || !ap.reasoningLoop.wasTriggered(info.ID) {
		return
	}
	if info.Error != nil && strings.Contains(strings.ToLower(info.Error.Name), "abort") {
		info.Error = nil
	}
	if info.Finish == "aborted" {
		info.Finish = "length"
	}
}

func (s *Service) abortOpenCodeRunawayTurn(rec SessionRecord) {
	if rec.ExternalID == "" {
		return
	}
	go func() {
		abortCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.openCodeJSON(abortCtx, "POST", "/session/"+rec.ExternalID+"/abort", rec.WorktreePath, map[string]interface{}{}, nil)
	}()
}

// shouldRecoverOpenCodeTurn reports whether the latest assistant step ended
// prematurely inside reasoning (e.g. runaway DSML tool-tag loop, leaked tool
// call in reasoning, or `finish: "length"` before any visible text or tool call
// was emitted) and should be automatically continued within the same turn.
func shouldRecoverOpenCodeTurn(turns []MessageTurn, ap *activeProcess) bool {
	if ap == nil || ap.wasAborted() {
		return false
	}
	loopAborted := ap.reasoningLoop.hasPendingRecovery()
	var last *MessageTurn
	for i := len(turns) - 1; i >= 0; i-- {
		if isInternalRecoveryMessageID(turns[i].Info.ID) {
			continue
		}
		last = &turns[i]
		break
	}
	if last == nil || last.Info.Role != "assistant" {
		return loopAborted
	}
	if last.Info.Error != nil && !ap.reasoningLoop.wasTriggered(last.Info.ID) {
		return false
	}
	hasReasoning := false
	hasLeakedTool := false
	for _, p := range last.Parts {
		switch p.Type {
		case "tool":
			return false
		case "text":
			if strings.TrimSpace(p.Text) != "" {
				return false
			}
		case "reasoning":
			if strings.TrimSpace(p.Text) != "" {
				hasReasoning = true
			}
			if _, leaked := stripLeakedReasoningToolMarkup(p.Text); leaked || hasRunawayReasoningToolLoop(p.Text) {
				hasLeakedTool = true
			}
		}
	}
	if loopAborted || hasLeakedTool {
		return true
	}
	return hasReasoning && last.Info.Finish == "length"
}

// isSupersededEmptyRecoveryStubTurn reports whether turns[idx] is an aborted or
// truncated assistant turn immediately followed by an internal recovery prompt
// that has no remaining visible text, tool calls, or non-empty reasoning after
// stripping leaked tool-call markup.
func isSupersededEmptyRecoveryStubTurn(turns []MessageTurn, idx int) bool {
	if idx < 0 || idx+1 >= len(turns) {
		return false
	}
	turn := turns[idx]
	if turn.Info.Role != "assistant" || !isInternalRecoveryMessageID(turns[idx+1].Info.ID) {
		return false
	}
	for _, p := range turn.Parts {
		switch p.Type {
		case "tool", "file":
			return false
		case "text":
			if strings.TrimSpace(p.Text) != "" {
				return false
			}
		case "reasoning":
			cleaned, _ := stripLeakedReasoningToolMarkup(p.Text)
			if strings.TrimSpace(cleaned) != "" {
				return false
			}
		}
	}
	return true
}

func buildOpenCodeReasoningRecoveryPayload(rec SessionRecord, mode, modelID, effort, system string, attempt int) map[string]interface{} {
	recoverID := fmt.Sprintf("%s%d_%d", internalRecoveryMsgPrefix, time.Now().UnixMilli(), attempt)
	payload := map[string]interface{}{
		"messageID": recoverID,
		"agent":     mode,
		"parts": []map[string]interface{}{
			{"type": "text", "text": openCodeReasoningRecoveryPrompt},
		},
	}
	if rec.ProviderID != "" && modelID != "" {
		payload["model"] = map[string]string{"providerID": rec.ProviderID, "modelID": modelID}
	}
	if effort != "" {
		payload["variant"] = effort
	}
	if system != "" {
		payload["system"] = system
	}
	return payload
}

func (s *Service) recoverOpenCodeReasoningIfNeeded(ctx context.Context, rec SessionRecord, ap *activeProcess, mode, modelID, effort, system string, initialErr error) error {
	curErr := initialErr
	for attempt := 1; attempt <= maxOpenCodeReasoningRecoveries && !ap.wasAborted(); attempt++ {
		s.waitForOpenCodeIdle(ctx, rec, ap)
		if ap.wasAborted() {
			break
		}
		checkCtx, checkCancel := context.WithTimeout(ctx, 10*time.Second)
		checkTurns, fetchErr := s.fetchOpenCodeRecentMessages(checkCtx, rec.ExternalID, rec.WorktreePath, reasoningCheckSnapshotLimit)
		checkCancel()
		if fetchErr != nil || !shouldRecoverOpenCodeTurn(checkTurns, ap) {
			ap.reasoningLoop.consumePendingRecovery()
			return curErr
		}
		ap.reasoningLoop.consumePendingRecovery()
		_, _ = ap.takePendingError()
		curErr = nil
		ap.stall.touch(time.Now())
		recoverPayload := buildOpenCodeReasoningRecoveryPayload(rec, mode, modelID, effort, system, attempt)
		postErr := s.openCodeJSON(ctx, "POST", "/session/"+rec.ExternalID+"/message", rec.WorktreePath, recoverPayload, nil)
		if postErr != nil && !ap.reasoningLoop.hasPendingRecovery() {
			return postErr
		}
		if ap.wasSteered() && !ap.wasAborted() {
			s.waitForOpenCodeIdle(ctx, rec, ap)
		}
	}
	return curErr
}

func (s *Service) rewriteRecoveryParentID(sessionID string, info *MessageInfo) {
	if info == nil || !isInternalRecoveryMessageID(info.ParentID) {
		return
	}
	if current, ok := s.store.Get(sessionID); ok {
		for i := len(current.Turns) - 1; i >= 0; i-- {
			if current.Turns[i].Info.Role == "user" && !isInternalRecoveryMessageID(current.Turns[i].Info.ID) {
				info.ParentID = current.Turns[i].Info.ID
				return
			}
		}
	}
	info.ParentID = ""
}

func (s *Service) pruneOpenCodeTurnsFromStore(sessionID string, dropIDs map[string]bool) {
	if len(dropIDs) == 0 {
		return
	}
	_, _, _ = s.store.updateWithoutActivity(sessionID, func(r *SessionRecord) {
		filtered := r.Turns[:0]
		for _, t := range r.Turns {
			if dropIDs[t.Info.ID] || isInternalRecoveryMessageID(t.Info.ID) {
				continue
			}
			filtered = append(filtered, t)
		}
		r.Turns = filtered
	})
}

func (s *Service) pruneOpenCodePartsFromStore(sessionID, messageID string, dropPartIDs map[string]bool) {
	if len(dropPartIDs) == 0 {
		return
	}
	_, _, _ = s.store.updateWithoutActivity(sessionID, func(r *SessionRecord) {
		for i := range r.Turns {
			if messageID != "" && r.Turns[i].Info.ID != messageID {
				continue
			}
			parts := r.Turns[i].Parts
			filtered := parts[:0]
			for _, p := range parts {
				if dropPartIDs[p.ID] {
					continue
				}
				filtered = append(filtered, p)
			}
			r.Turns[i].Parts = pruneRunawayDuplicateToolParts(filtered)
		}
	})
}

func (s *Service) reconcileOpenCodePostTurnMessages(rec SessionRecord, ap *activeProcess, userMsgID string, fileURLByMsgAndName map[string]string) {
	refreshCtx, done := context.WithTimeout(context.Background(), 15*time.Second)
	defer done()
	turns, fetchErr := s.fetchOpenCodeRecentMessages(refreshCtx, rec.ExternalID, rec.WorktreePath, postTurnSnapshotLimit)
	if fetchErr != nil {
		return
	}
	droppedParts := ap.reasoningLoop.droppedPartsSnapshot()
	existingInfoByID := make(map[string]MessageInfo)
	existingPartByKey := make(map[partKey]MessagePart)
	if current, ok := s.store.Get(rec.ID); ok {
		for _, t := range current.Turns {
			existingInfoByID[t.Info.ID] = t.Info
			for _, p := range t.Parts {
				existingPartByKey[partKey{messageID: p.MessageID, partID: p.ID}] = p
			}
		}
	}
	lastVisibleUserID := userMsgID
	dropTurnIDs := make(map[string]bool)
	for i, turn := range turns {
		if isInternalRecoveryMessageID(turn.Info.ID) {
			dropTurnIDs[turn.Info.ID] = true
			continue
		}
		if isSupersededEmptyRecoveryStubTurn(turns, i) {
			dropTurnIDs[turn.Info.ID] = true
			continue
		}
		if turn.Info.Role == "user" {
			lastVisibleUserID = turn.Info.ID
		}
		if isInternalRecoveryMessageID(turn.Info.ParentID) {
			turn.Info.ParentID = lastVisibleUserID
		}
		sanitizeRunawayTriggeredInfo(&turn.Info, ap)
		turn.Info.SessionID = rec.ID
		if prevInfo, exists := existingInfoByID[turn.Info.ID]; !exists || !equalMessageInfo(prevInfo, turn.Info) {
			s.broadcast(rec.ID, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": turn.Info}})
		}
		normalizedParts := make([]MessagePart, 0, len(turn.Parts))
		for _, part := range turn.Parts {
			if droppedParts[part.ID] {
				continue
			}
			part.SessionID = rec.ID
			if !s.normalizeOpenCodePart(rec, &part, fileURLByMsgAndName) {
				continue
			}
			normalizedParts = append(normalizedParts, part)
		}
		prunedParts := pruneRunawayDuplicateToolParts(normalizedParts)
		if len(prunedParts) < len(normalizedParts) {
			keep := make(map[string]bool, len(prunedParts))
			for _, p := range prunedParts {
				keep[p.ID] = true
			}
			if droppedParts == nil {
				droppedParts = make(map[string]bool)
			}
			for _, p := range normalizedParts {
				if !keep[p.ID] {
					droppedParts[p.ID] = true
				}
			}
		}
		for _, part := range prunedParts {
			if prevPart, exists := existingPartByKey[partKey{messageID: part.MessageID, partID: part.ID}]; !exists || !equalMessagePart(prevPart, part) {
				s.broadcast(rec.ID, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": part}})
			}
		}
	}
	if len(dropTurnIDs) > 0 {
		s.pruneOpenCodeTurnsFromStore(rec.ID, dropTurnIDs)
	}
	if len(droppedParts) > 0 {
		s.pruneOpenCodePartsFromStore(rec.ID, "", droppedParts)
	}
}

func (s *Service) abandonOpenCodeInFlightParts(sessionID string, now int64) {
	rec, ok := s.store.Get(sessionID)
	if !ok || len(rec.Turns) == 0 {
		return
	}
	for i := range rec.Turns {
		for j := range rec.Turns[i].Parts {
			p := rec.Turns[i].Parts[j]
			if p.Type == "tool" && p.Tool != "task" && p.State != nil &&
				(p.State.Status == "pending" || p.State.Status == "running") {
				start := now
				if p.State.Time != nil && p.State.Time.Start > 0 {
					start = p.State.Time.Start
				}
				p.State.Status = "error"
				if strings.TrimSpace(p.State.Error) == "" {
					p.State.Error = "Interrupted before completion"
				}
				p.State.Time = &PartTime{Start: start, End: now}
				s.broadcast(sessionID, SSEEvent{
					Type:       "message.part.updated",
					Properties: map[string]interface{}{"part": p},
				})
			} else if p.Type == "reasoning" && (p.Time == nil || p.Time.End == 0) {
				start := now
				if p.Time != nil && p.Time.Start > 0 {
					start = p.Time.Start
				}
				p.Time = &PartTime{Start: start, End: now}
				s.broadcast(sessionID, SSEEvent{
					Type:       "message.part.updated",
					Properties: map[string]interface{}{"part": p},
				})
			}
		}
	}
}

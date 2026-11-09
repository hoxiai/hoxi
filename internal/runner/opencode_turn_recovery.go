package runner

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const maxTransitionalActionRunes = 400

var transitionalSentencePrefixes = []string{
	"let me ",
	"let's ",
	"now let me ",
	"first, let me ",
	"first let me ",
	"next, let me ",
	"next let me ",
	"so let me ",
	"then let me ",
	"i will now ",
	"i'll now ",
	"i am going to ",
	"i'm going to ",
	"now i will ",
	"now i'll ",
	"next, i will ",
	"next, i'll ",
	"next i will ",
	"next i'll ",
	"now verify ",
	"now check ",
	"now inspect ",
	"now run ",
	"now start ",
	"now test ",
	"now fix ",
	"让我",
	"先让我",
	"接下来让我",
	"下面让我",
	"我现在",
	"接下来我",
	"下面我",
	"我先",
	"我来",
	"我接着",
	"我继续",
	"先看看",
	"先检查",
	"先查看",
	"先验证",
	"先确认",
	"先排查",
	"先测试",
	"先执行",
	"先运行",
	"接着检查",
	"继续检查",
	"接下来检查",
	"接下来验证",
	"现在检查",
	"现在验证",
	"现在启动",
	"现在执行",
	"现在运行",
}

var transitionalClausePrefixes = []string{
	"let me ",
	"now let me ",
	"then let me ",
	"so let me ",
	"让我",
	"先让我",
	"接下来让我",
	"我现在",
	"接下来我",
	"我先",
	"我来",
}

const (
	openCodePreambleRecoveryPrompt          = "Your previous reply ended with a transitional sentence announcing your next action without emitting a tool call. Execute that action now by calling the appropriate tool, or provide your final answer directly."
	openCodePermissionCascadeRecoveryPrompt = "One or more concurrent tool calls in your previous step were not executed because an approval request was rejected or timed out (which also cancelled sibling pending tool calls in the same step). Do not re-run the rejected command; either continue with an alternative that does not require that permission or summarize your findings and current status for the user."
	openCodeFeedbackRejectPrefix            = "The user rejected permission to use this specific tool call with the following feedback:"
	openCodeBareRejectError                 = "The user rejected permission to use this specific tool call."
	recoveryKindNone                        = ""
	recoveryKindReasoning                   = "reasoning"
	recoveryKindPreamble                    = "preamble"
	recoveryKindPermissionCascade           = "permission-cascade"
)

// shouldRecoverOpenCodeTurn reports whether the latest assistant step ended
// prematurely (runaway DSML tool-tag loop, leaked tool call in reasoning,
// `finish: "length"` before any visible text or tool call was emitted, or a
// mid-task transitional action preamble that returned `finish: "stop"` without
// emitting its announced tool call) and should be automatically continued
// within the same turn.
func shouldRecoverOpenCodeTurn(turns []MessageTurn, ap *activeProcess) bool {
	return classifyOpenCodeTurnRecovery(turns, ap) != recoveryKindNone
}

func classifyOpenCodeTurnRecovery(turns []MessageTurn, ap *activeProcess) string {
	if ap == nil || ap.wasAborted() {
		return recoveryKindNone
	}
	loopAborted := ap.reasoningLoop.hasPendingRecovery()
	lastIdx := -1
	for i := len(turns) - 1; i >= 0; i-- {
		if isInternalRecoveryMessageID(turns[i].Info.ID) {
			continue
		}
		lastIdx = i
		break
	}
	if lastIdx < 0 || turns[lastIdx].Info.Role != "assistant" {
		if loopAborted {
			return recoveryKindReasoning
		}
		return recoveryKindNone
	}
	last := &turns[lastIdx]
	if last.Info.Error != nil && !ap.reasoningLoop.wasTriggered(last.Info.ID) {
		return recoveryKindNone
	}
	hasReasoning := false
	hasLeakedTool := false
	hasTool := false
	hasActiveTool := false
	hasFeedbackRejection := false
	hasCascadeBareRejection := false
	var visibleText strings.Builder
	for _, p := range last.Parts {
		switch p.Type {
		case "tool":
			hasTool = true
			if p.State == nil || p.State.Status == "pending" || p.State.Status == "running" {
				hasActiveTool = true
				continue
			}
			errText := strings.TrimSpace(p.State.Error)
			if strings.HasPrefix(errText, openCodeFeedbackRejectPrefix) {
				hasFeedbackRejection = true
			} else if errText == openCodeBareRejectError {
				hasCascadeBareRejection = true
			}
		case "text":
			if trimmed := strings.TrimSpace(p.Text); trimmed != "" {
				if visibleText.Len() > 0 {
					visibleText.WriteByte('\n')
				}
				visibleText.WriteString(trimmed)
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
	if hasTool {
		if !hasActiveTool && hasFeedbackRejection && hasCascadeBareRejection && (last.Info.Finish == "tool-calls" || last.Info.Finish == "") {
			return recoveryKindPermissionCascade
		}
		return recoveryKindNone
	}
	if loopAborted || hasLeakedTool {
		return recoveryKindReasoning
	}
	text := visibleText.String()
	if text == "" {
		if hasReasoning && last.Info.Finish == "length" {
			return recoveryKindReasoning
		}
		return recoveryKindNone
	}
	if last.Info.Finish == "stop" && (hasReasoning || hasPriorToolInCycle(turns[:lastIdx])) && isTransitionalActionPreamble(text) {
		return recoveryKindPreamble
	}
	return recoveryKindNone
}

func hasPriorToolInCycle(priorTurns []MessageTurn) bool {
	for i := len(priorTurns) - 1; i >= 0; i-- {
		t := priorTurns[i]
		if isInternalRecoveryMessageID(t.Info.ID) {
			continue
		}
		if t.Info.Role == "user" {
			break
		}
		if t.Info.Role != "assistant" {
			continue
		}
		if t.Info.Finish == "tool-calls" {
			return true
		}
		for _, p := range t.Parts {
			if p.Type == "tool" {
				return true
			}
		}
	}
	return false
}

// isTransitionalActionPreamble reports whether text is a compact transitional
// status update whose final sentence or clause announces an immediate tool
// action (e.g. "Let me verify the available release keys and set pins.")
// without asking the user a question or emitting a goal verdict.
func isTransitionalActionPreamble(text string) bool {
	s := strings.TrimSpace(text)
	if s == "" || utf8.RuneCountInString(s) > maxTransitionalActionRunes {
		return false
	}
	if strings.Contains(s, "[[goal:") || strings.Contains(s, "```") {
		return false
	}
	lastLine := s
	if strings.ContainsAny(s, "\r\n") {
		lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
		nonEmpty := 0
		for _, rawLine := range lines {
			line := strings.TrimSpace(rawLine)
			if line == "" {
				continue
			}
			nonEmpty++
			if nonEmpty > 4 || isStructuredListOrHeadingLine(line) {
				return false
			}
			lastLine = line
		}
	}
	for _, suffix := range []string{"?", "？", ":", "："} {
		if strings.HasSuffix(lastLine, suffix) {
			return false
		}
	}
	tail := strings.TrimLeft(strings.ToLower(trailingSentence(lastLine)), " \t—-–,;，")
	if tail == "" {
		return false
	}
	if hasAnyPrefix(tail, transitionalSentencePrefixes) {
		return true
	}
	if clause := trailingCommaClause(tail); clause != "" && clause != tail {
		return hasAnyPrefix(clause, transitionalClausePrefixes)
	}
	return false
}

func isStructuredListOrHeadingLine(line string) bool {
	if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "- ") ||
		strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ ") ||
		strings.HasPrefix(line, "> ") {
		return true
	}
	digits := 0
	for digits < len(line) && line[digits] >= '0' && line[digits] <= '9' {
		digits++
	}
	if digits > 0 && digits+1 < len(line) && (line[digits] == '.' || line[digits] == ')') && line[digits+1] == ' ' {
		return true
	}
	return false
}

func trailingSentence(s string) string {
	s = strings.TrimSpace(strings.TrimRight(s, ".!。！\"'`)）"))
	if s == "" {
		return ""
	}
	lastCut := 0
	for i := 0; i < len(s); {
		r, width := utf8.DecodeRuneInString(s[i:])
		switch r {
		case '。', '！', '？', '；':
			if rest := strings.TrimSpace(s[i+width:]); rest != "" {
				lastCut = i + width
			}
		case '.', '!', '?', ';':
			// Require whitespace after ASCII punctuation so version numbers,
			// domains, and filenames (v1.2.4, myshopark.com, package.json) do not split.
			if i+width < len(s) && (s[i+width] == ' ' || s[i+width] == '\t') {
				if rest := strings.TrimSpace(s[i+width:]); rest != "" {
					lastCut = i + width
				}
			}
		}
		i += width
	}
	return strings.TrimSpace(s[lastCut:])
}

func trailingCommaClause(s string) string {
	lastCut := -1
	for i := 0; i < len(s); {
		r, width := utf8.DecodeRuneInString(s[i:])
		if r == '，' || (r == ',' && i+width < len(s) && (s[i+width] == ' ' || s[i+width] == '\t')) {
			if rest := strings.TrimSpace(s[i+width:]); rest != "" {
				lastCut = i + width
			}
		}
		i += width
	}
	if lastCut < 0 {
		return ""
	}
	return strings.TrimLeft(s[lastCut:], " \t—-–")
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
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

func extractLeakedToolFromTurns(turns []MessageTurn, ap *activeProcess) string {
	for i := len(turns) - 1; i >= 0; i-- {
		if isInternalRecoveryMessageID(turns[i].Info.ID) {
			continue
		}
		if turns[i].Info.Role != "assistant" {
			break
		}
		for _, p := range turns[i].Parts {
			if p.Type == "reasoning" {
				if tool := extractLeakedReasoningToolName(p.Text); tool != "" {
					return tool
				}
			}
		}
		break
	}
	if ap != nil {
		return ap.reasoningLoop.leakedToolName()
	}
	return ""
}

func buildOpenCodeReasoningRecoveryPayload(rec SessionRecord, mode, modelID, effort, system string, attempt int, recoveryKind string, leakedTool string) map[string]interface{} {
	recoverID := fmt.Sprintf("%s%d_%d", internalRecoveryMsgPrefix, time.Now().UnixMilli(), attempt)
	prompt := openCodeReasoningRecoveryPrompt
	switch recoveryKind {
	case recoveryKindPreamble:
		prompt = openCodePreambleRecoveryPrompt
	case recoveryKindPermissionCascade:
		prompt = openCodePermissionCascadeRecoveryPrompt
	default:
		if leakedTool = strings.TrimSpace(leakedTool); leakedTool != "" {
			prompt += fmt.Sprintf(" Specifically, invoke the %q tool now via a standard structured tool call.", leakedTool)
		}
	}
	payload := map[string]interface{}{
		"messageID": recoverID,
		"agent":     mode,
		"parts": []map[string]interface{}{
			{"type": "text", "text": prompt},
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
		recoveryKind := classifyOpenCodeTurnRecovery(checkTurns, ap)
		if fetchErr != nil || recoveryKind == recoveryKindNone {
			ap.reasoningLoop.consumePendingRecovery()
			return curErr
		}
		leakedTool := extractLeakedToolFromTurns(checkTurns, ap)
		ap.reasoningLoop.consumePendingRecovery()
		_, _ = ap.takePendingError()
		curErr = nil
		ap.stall.touch(time.Now())
		recoverPayload := buildOpenCodeReasoningRecoveryPayload(rec, mode, modelID, effort, system, attempt, recoveryKind, leakedTool)
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
		sanitizeAbortedTurnErrorInfo(&turn.Info, ap)
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
			if isInFlightToolPart(p) {
				start := now
				if p.State.Time != nil && p.State.Time.Start > 0 {
					start = p.State.Time.Start
				}
				p.State.Status = "error"
				if strings.TrimSpace(p.State.Error) == "" {
					p.State.Error = interruptedToolError
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

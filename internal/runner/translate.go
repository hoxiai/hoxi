package runner

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

type TurnTranslator struct {
	sessionID          string
	runtime            string
	messageID          string
	parentUserMsgID    string
	modelID            string
	providerID         string
	mode               string
	externalID         string
	claudeMessageID    string
	claudeReasonPartID string
	claudeTextPartID   string
	claudeRetrying     bool
	geminiStepIdx      int
	geminiReasonPartID string
	geminiTextPartID   string
	geminiLastErrMsg   string
	startedAtMs        int64

	msgInfo              MessageInfo
	partOrder            []string
	partsByID            map[string]*MessagePart
	stepStartMs          map[int]int64
	hasTextPart          bool
	agyFinalResponseDone bool
	turnFinished         bool
	lookupThink          func(externalID string, stepIndex int) string
	lookupStep           func(externalID string, stepIndex int) string
	editPayloads         *agyEditPayloadCursor

	// mu serialises stdout feeding against the subagent activity poller.
	mu             sync.Mutex
	convPinned     bool
	lastActivityMs int64
	subagentOrder  []string
	subagentByPart map[string]*agySubagentTrack
	subagentByConv map[string]*agySubagentTrack
	claudeTasks    map[string]*claudeSubagentTask

	// createChild registers a subagent's child session with the host.
	createChild func(ChildSessionSpec) (SessionInfo, bool)
	children    map[string]*childSink
	childOrder  []string

	claudeMsgUsage map[string]claudeUsage
	agyStepUsage   map[string]agyUsage
}

func NewTurnTranslator(sessionID, runtime, messageID, parentUserMsgID, providerID, modelID, mode, externalID string) *TurnTranslator {
	now := time.Now().UnixMilli()
	if mode == "" {
		mode = "build"
	}
	info := MessageInfo{
		ID:         messageID,
		SessionID:  sessionID,
		Role:       "assistant",
		ParentID:   parentUserMsgID,
		Time:       MessageTime{Created: now},
		ModelID:    modelID,
		ProviderID: providerID,
		Mode:       mode,
		Agent:      mode,
	}
	return &TurnTranslator{
		sessionID:       sessionID,
		runtime:         runtime,
		messageID:       messageID,
		parentUserMsgID: parentUserMsgID,
		modelID:         modelID,
		providerID:      providerID,
		mode:            mode,
		externalID:      externalID,
		startedAtMs:     now,
		msgInfo:         info,
		partsByID:       make(map[string]*MessagePart),
		stepStartMs:     make(map[int]int64),
		lookupThink:     lookupAgyTranscriptThinking,
		lookupStep:      lookupAgyTranscriptContent,
		subagentByPart:  make(map[string]*agySubagentTrack),
		subagentByConv:  make(map[string]*agySubagentTrack),
		claudeTasks:     make(map[string]*claudeSubagentTask),
		children:        make(map[string]*childSink),
		claudeMsgUsage:  make(map[string]claudeUsage),
		agyStepUsage:    make(map[string]agyUsage),
	}
}

func (t *TurnTranslator) ExternalID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.externalID
}

func (t *TurnTranslator) IsTurnFinished() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.turnFinished
}

// ResetForFreshRetry clears any conversation ID pinned during a failed
// --resume attempt so the fresh retry turn can pin its new external ID.
func (t *TurnTranslator) ResetForFreshRetry() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.externalID = ""
	t.convPinned = false
	t.editPayloads = nil
	t.geminiLastErrMsg = ""
	t.agyFinalResponseDone = false
}

// noteAgyConversation pins externalID to the first conversation this turn
// reports (the parent's) and tells whether id belongs to it.
func (t *TurnTranslator) noteAgyConversation(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return true
	}
	if !t.convPinned {
		t.convPinned = true
		t.externalID = id
		t.editPayloads = newAgyEditPayloadCursor(id)
	}
	return id == t.externalID
}

func (t *TurnTranslator) InitialEvents(userInfo MessageInfo, userParts []MessagePart) []SSEEvent {
	evts := []SSEEvent{
		{Type: "session.status", Properties: map[string]interface{}{"sessionID": t.sessionID, "status": SessionStatus{Type: "busy"}}},
		{Type: "message.updated", Properties: map[string]interface{}{"info": userInfo}},
	}
	for _, p := range userParts {
		evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": p}})
	}
	evts = append(evts, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": t.msgInfo}})
	return evts
}

func (t *TurnTranslator) FeedLine(rawLine string) []SSEEvent {
	line := strings.TrimSpace(rawLine)
	if line == "" || !strings.HasPrefix(line, "{") {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if IsClaudeProtocolRuntime(t.runtime) {
		return t.feedClaudeLine(line)
	}
	if t.runtime == string(RuntimeGeminiCLI) {
		return t.feedGeminiLine(line)
	}
	return t.feedAgyLine(line)
}

type agyStepUpdate struct {
	ConversationID  string  `json:"conversation_id"`
	StepIndex       int     `json:"step_index"`
	State           string  `json:"state"`
	StepType        string  `json:"step_type"`
	TextDelta       string  `json:"text_delta"`
	Thinking        string  `json:"thinking"`
	ThinkingDelta   string  `json:"thinking_delta"`
	DurationSeconds float64 `json:"duration_seconds"`
	ToolName        string  `json:"tool_name"`
	ToolInfo        *struct {
		Name       string                 `json:"name"`
		Parameters map[string]interface{} `json:"parameters"`
		Output     string                 `json:"output"`
		Error      *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"tool_info"`
	SubagentInfo *agySubagentInfo `json:"subagent_info"`
	Usage        *agyUsage        `json:"usage"`
}

type agyResult struct {
	ConversationID  string    `json:"conversation_id"`
	Status          string    `json:"status"`
	Response        string    `json:"response"`
	Error           string    `json:"error"`
	DurationSeconds float64   `json:"duration_seconds"`
	NumTurns        int       `json:"num_turns"`
	Usage           *agyUsage `json:"usage"`
	DeniedActions   []struct {
		Action      string `json:"action"`
		DisplayName string `json:"display_name"`
	} `json:"denied_actions"`
}

type agyRawEvent struct {
	Event          string `json:"event"`
	ConversationID string `json:"conversation_id"`
	Init           *struct {
		Model          string `json:"model"`
		CWD            string `json:"cwd"`
		PermissionMode string `json:"permission_mode"`
	} `json:"init"`
	StepUpdate *agyStepUpdate `json:"step_update"`
	Result     *agyResult     `json:"result"`
}

type agyUsage struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	ThinkingTokens  int `json:"thinking_tokens"`
	CacheReadTokens int `json:"cache_read_tokens"`
	TotalTokens     int `json:"total_tokens"`
}

func (u agyUsage) empty() bool {
	return u.InputTokens == 0 && u.OutputTokens == 0 && u.ThinkingTokens == 0 && u.CacheReadTokens == 0 && u.TotalTokens == 0
}

func (t *TurnTranslator) feedAgyLine(line string) []SSEEvent {
	var env agyRawEvent
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		return nil
	}
	t.lastActivityMs = time.Now().UnixMilli()
	if !t.noteAgyConversation(env.ConversationID) {
		return nil
	}

	switch env.Event {
	case "init":
		if env.Init != nil && env.Init.Model != "" && (t.msgInfo.ModelID == "" || strings.EqualFold(t.msgInfo.ModelID, "auto")) {
			t.msgInfo.ModelID = env.Init.Model
			return []SSEEvent{{Type: "message.updated", Properties: map[string]interface{}{"info": t.msgInfo}}}
		}
		return nil

	case "step_update":
		su := env.StepUpdate
		if su == nil {
			return nil
		}
		now := time.Now().UnixMilli()
		// Step indexes are per conversation; never let another one's steps
		// overwrite the parent's parts.
		if !t.noteAgyConversation(su.ConversationID) {
			return t.noteSubagentStep(su, now)
		}
		if _, ok := t.stepStartMs[su.StepIndex]; !ok {
			t.stepStartMs[su.StepIndex] = now
		}

		switch su.StepType {
		case "agent_response":
			return t.handleAgyAgentResponse(su, now)
		case "subagent":
			t.agyFinalResponseDone = false
			return t.handleAgySubagentStep(su, now)
		case "tool":
			t.agyFinalResponseDone = false
			if isAgySubagentDispatch(su) {
				return t.handleAgySubagentStep(su, now)
			}
			return t.handleAgyToolStep(su, now)
		case "system_message":
			if su.State != "DONE" {
				return nil
			}
			return t.handleAgySystemMessage(su, now)
		}
		return nil

	case "result":
		res := env.Result
		if res == nil {
			return nil
		}
		if !t.noteAgyConversation(res.ConversationID) {
			return nil
		}
		return t.handleAgyResult(res)
	}
	return nil
}

func (t *TurnTranslator) handleAgyAgentResponse(su *agyStepUpdate, now int64) []SSEEvent {
	t.agyFinalResponseDone = false
	var evts []SSEEvent
	startMs := t.stepStartMs[su.StepIndex]
	endMs := now
	if su.DurationSeconds > 0 {
		if calcStart := endMs - int64(su.DurationSeconds*1000); calcStart > 0 && calcStart < startMs {
			startMs = calcStart
		}
	}

	// 1. Handle reasoning/thinking
	thinkDelta := su.ThinkingDelta
	if thinkDelta == "" && su.Thinking != "" {
		thinkDelta = su.Thinking
	}
	if thinkDelta == "" && su.State == "DONE" && su.Usage != nil && su.Usage.ThinkingTokens > 0 && t.lookupThink != nil && t.externalID != "" {
		thinkDelta = t.lookupThink(t.externalID, su.StepIndex)
	}
	rID := fmt.Sprintf("prt_%s_reason_%d", t.messageID, su.StepIndex)
	if thinkDelta != "" {
		part, exists := t.partsByID[rID]
		if !exists {
			part = &MessagePart{
				ID:        rID,
				SessionID: t.sessionID,
				MessageID: t.messageID,
				Type:      "reasoning",
				Text:      "",
				Time:      &PartTime{Start: startMs},
			}
			t.partsByID[rID] = part
			t.partOrder = append(t.partOrder, rID)
			evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
		}
		part.Text += thinkDelta
		evts = append(evts, SSEEvent{
			Type: "message.part.delta",
			Properties: map[string]interface{}{
				"sessionID": t.sessionID,
				"messageID": t.messageID,
				"partID":    rID,
				"field":     "text",
				"delta":     thinkDelta,
			},
		})
	}
	if su.TextDelta != "" || su.State == "DONE" {
		if rPart, exists := t.partsByID[rID]; exists && (rPart.Time == nil || rPart.Time.End == 0) {
			rStart := startMs
			if rPart.Time != nil && rPart.Time.Start > 0 {
				rStart = rPart.Time.Start
			}
			rPart.Time = &PartTime{Start: rStart, End: endMs}
			evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*rPart)}})
		}
	}

	// 2. Handle text delta
	txtID := fmt.Sprintf("prt_%s_text_%d", t.messageID, su.StepIndex)
	if su.TextDelta != "" {
		t.hasTextPart = true
		part, exists := t.partsByID[txtID]
		if !exists {
			part = &MessagePart{
				ID:        txtID,
				SessionID: t.sessionID,
				MessageID: t.messageID,
				Type:      "text",
				Text:      "",
				Time:      &PartTime{Start: startMs},
			}
			t.partsByID[txtID] = part
			t.partOrder = append(t.partOrder, txtID)
			evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
		}
		part.Text += su.TextDelta
		evts = append(evts, SSEEvent{
			Type: "message.part.delta",
			Properties: map[string]interface{}{
				"sessionID": t.sessionID,
				"messageID": t.messageID,
				"partID":    txtID,
				"field":     "text",
				"delta":     su.TextDelta,
			},
		})
	}
	if su.State == "DONE" {
		var tokensUpdated bool
		if su.Usage != nil {
			tokensUpdated = t.noteAgyStepUsage(fmt.Sprintf("step:%d", su.StepIndex), *su.Usage)
		}
		if part, exists := t.partsByID[txtID]; exists {
			part.Time = &PartTime{Start: startMs, End: endMs}
			t.agyFinalResponseDone = strings.TrimSpace(part.Text) != ""
			evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
		}
		if tokensUpdated {
			evts = append(evts, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": t.msgInfo}})
		}
	}
	return evts
}

func messageTokensEqual(a, b *MessageTokens) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (t *TurnTranslator) noteAgyStepUsage(key string, u agyUsage) bool {
	if u.empty() {
		return false
	}
	t.agyStepUsage[key] = u
	var in, out, reasoning, cacheRead, total int
	for _, s := range t.agyStepUsage {
		in += s.InputTokens
		out += s.OutputTokens
		reasoning += s.ThinkingTokens
		cacheRead += s.CacheReadTokens
		total += s.TotalTokens
	}
	if total == 0 {
		total = in + out + reasoning + cacheRead
	}
	tokens := &MessageTokens{
		Total:     total,
		Input:     in,
		Output:    out,
		Reasoning: reasoning,
	}
	tokens.Cache.Read = cacheRead
	if messageTokensEqual(t.msgInfo.Tokens, tokens) {
		return false
	}
	t.msgInfo.Tokens = tokens
	return true
}

func (t *TurnTranslator) handleAgyToolStep(su *agyStepUpdate, now int64) []SSEEvent {
	var evts []SSEEvent
	partID := fmt.Sprintf("prt_%s_tool_%d", t.messageID, su.StepIndex)
	callID := fmt.Sprintf("call_%s_%d", t.messageID, su.StepIndex)
	rawName := su.ToolName
	var rawParams map[string]interface{}
	var output, errMsg string
	if su.ToolInfo != nil {
		if rawName == "" {
			rawName = su.ToolInfo.Name
		}
		rawParams = su.ToolInfo.Parameters
		output = su.ToolInfo.Output
		if su.ToolInfo.Error != nil {
			errMsg = su.ToolInfo.Error.Message
		}
	}
	if existing, ok := t.partsByID[partID]; ok && existing.State != nil && len(existing.State.Input) > 0 {
		rawParams = mergeAgyEditParams(rawParams, existing.State.Input)
		if existing.Tool == "write" && strings.EqualFold(strings.TrimSpace(rawName), "edit_file") {
			rawName = "write_file"
		}
	}
	if t.editPayloads != nil && agyEditPayloadNames[strings.ToLower(strings.TrimSpace(rawName))] && agyParamsLackEditPayload(rawParams) {
		if name, full := t.editPayloads.lookup(su.StepIndex, rawParams, su.State == "DONE"); full != nil {
			rawParams = mergeAgyEditParams(rawParams, full)
			// Stdout names every edit step edit_file, even the writes, so the
			// planned call is what distinguishes creating a file from editing one.
			if name != "" {
				rawName = name
			}
		}
	}
	normTool, title, cleanInput := normalizeRunnerTool(rawName, rawParams)
	startMs := t.stepStartMs[su.StepIndex]
	endMs := now
	if su.DurationSeconds > 0 {
		endMs = startMs + int64(su.DurationSeconds*1000)
		if endMs <= startMs {
			endMs = startMs + 1
		}
	}

	part, exists := t.partsByID[partID]
	if !exists {
		pendingPart := MessagePart{
			ID:        partID,
			SessionID: t.sessionID,
			MessageID: t.messageID,
			Type:      "tool",
			Tool:      normTool,
			CallID:    callID,
			State: &ToolState{
				Status: "pending",
				Input:  cleanInput,
				Title:  title,
				Time:   &PartTime{Start: startMs},
			},
		}
		t.partsByID[partID] = &pendingPart
		t.partOrder = append(t.partOrder, partID)
		part = &pendingPart
		if su.State != "PENDING" {
			evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
		}
	}

	status := "running"
	partTime := &PartTime{Start: startMs}
	switch su.State {
	case "PENDING":
		status = "pending"
	case "ACTIVE":
		status = "running"
	case "DONE":
		status = "completed"
		partTime = &PartTime{Start: startMs, End: endMs}
	case "ERROR":
		status = "error"
		partTime = &PartTime{Start: startMs, End: endMs}
		if errMsg == "" {
			errMsg = "tool execution failed"
		}
	}

	part.Tool = normTool
	part.State = &ToolState{
		Status: status,
		Input:  cleanInput,
		Output: output,
		Title:  title,
		Error:  errMsg,
		Time:   partTime,
	}
	if normTool == "agent_wait" && t.hasWaitingSubagents() {
		part.State.Metadata = map[string]interface{}{"waitingFor": "subagents"}
	}
	evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
	return evts
}

// IsStaleAgyResultError reports whether a stream `result` line is a historical
// trajectory error re-emitted by `agy` on a resumed conversation after the
// current turn has already finished its final assistant response cleanly.
func (t *TurnTranslator) IsStaleAgyResultError(rawLine string) bool {
	line := strings.TrimSpace(rawLine)
	if line == "" || !strings.HasPrefix(line, "{") {
		return false
	}
	var env agyRawEvent
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		return false
	}
	if env.Event != "result" || env.Result == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if id := strings.TrimSpace(firstNonEmptyStr(env.Result.ConversationID, env.ConversationID)); id != "" && t.convPinned && id != t.externalID {
		return false
	}
	return t.isStaleAgyResultErrorLocked(env.Result)
}

func (t *TurnTranslator) isStaleAgyResultErrorLocked(res *agyResult) bool {
	if res == nil || res.Status != "ERROR" || !t.agyFinalResponseDone {
		return false
	}
	if len(res.DeniedActions) > 0 || t.hasWaitingSubagents() {
		return false
	}
	errMsg := strings.TrimSpace(res.Error)
	if strings.EqualFold(errMsg, "interrupted") || !isRetryableTurnError(errMsg) {
		return false
	}
	for _, p := range t.partsByID {
		if p != nil && p.Type == "tool" && p.State != nil {
			switch p.State.Status {
			case "pending", "running", "error":
				return false
			}
		}
	}
	return true
}

func (t *TurnTranslator) handleAgyResult(res *agyResult) []SSEEvent {
	t.turnFinished = true
	now := time.Now().UnixMilli()
	var evts []SSEEvent

	isErr := res.Status == "ERROR" && !t.isStaleAgyResultErrorLocked(res)
	t.msgInfo.Time.Completed = now
	evts = append(evts, t.finishAgySubagents(isErr, now)...)
	evts = append(evts, t.closeAllChildren(isErr, now)...)
	// On resumed conversations (`--conversation <id>`), `result.usage` reports the
	// cumulative total across the entire conversation since step 0, and on errors
	// or interruptions it reports all zeros. Prefer the per-step usage accumulated
	// during this turn, falling back to `result.usage` only when no step usage was seen.
	if len(t.agyStepUsage) == 0 && res.Usage != nil {
		t.noteAgyStepUsage("result", *res.Usage)
	}

	if !t.hasTextPart && strings.TrimSpace(res.Response) != "" {
		txtID := fmt.Sprintf("prt_%s_text_final", t.messageID)
		part := MessagePart{
			ID:        txtID,
			SessionID: t.sessionID,
			MessageID: t.messageID,
			Type:      "text",
			Text:      res.Response,
			Time:      &PartTime{Start: t.startedAtMs, End: now},
		}
		t.partsByID[txtID] = &part
		t.partOrder = append(t.partOrder, txtID)
		t.hasTextPart = true
		evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": part}})
	} else if !t.hasTextPart && len(res.DeniedActions) > 0 {
		var names []string
		for _, da := range res.DeniedActions {
			if da.DisplayName != "" {
				names = append(names, da.DisplayName)
			} else if da.Action != "" {
				names = append(names, da.Action)
			}
		}
		// Hook-gated run_command / view_file calls never reach here; a soft-deny
		// means the Hoxi approval hook was missing or the tool is not covered.
		notice := fmt.Sprintf("Action blocked by Antigravity headless permission policy (%s). Hoxi's approval hook did not handle it; check `~/.gemini/config/hooks.json` (entry `%s`).", strings.Join(names, ", "), agyHookName)
		txtID := fmt.Sprintf("prt_%s_text_denied", t.messageID)
		part := MessagePart{
			ID:        txtID,
			SessionID: t.sessionID,
			MessageID: t.messageID,
			Type:      "text",
			Text:      notice,
			Time:      &PartTime{Start: t.startedAtMs, End: now},
		}
		t.partsByID[txtID] = &part
		t.partOrder = append(t.partOrder, txtID)
		evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": part}})
	}

	if isErr {
		if strings.EqualFold(strings.TrimSpace(res.Error), "interrupted") {
			t.msgInfo.Finish = "aborted"
			evts = append(evts,
				SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": t.msgInfo}},
				SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": t.sessionID, "status": SessionStatus{Type: "idle"}}},
			)
			return evts
		}
		errMsg := strings.TrimSpace(res.Error)
		if errMsg == "" {
			errMsg = "Antigravity CLI execution error"
		}
		msgErr := &MessageError{
			Name: "RunnerError",
			Data: &MessageErrorData{Message: errMsg, IsRetryable: isRetryableTurnError(errMsg)},
		}
		t.msgInfo.Error = msgErr
		t.msgInfo.Finish = "error"
		evts = append(evts,
			SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": t.msgInfo}},
			SSEEvent{Type: "session.error", Properties: map[string]interface{}{"sessionID": t.sessionID, "error": msgErr}},
			SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": t.sessionID, "status": SessionStatus{Type: "idle"}}},
		)
		return evts
	}

	t.msgInfo.Finish = "stop"
	evts = append(evts,
		SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": t.msgInfo}},
		SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": t.sessionID, "status": SessionStatus{Type: "idle"}}},
		SSEEvent{Type: "session.idle", Properties: map[string]interface{}{"sessionID": t.sessionID}},
	)
	return evts
}

func (t *TurnTranslator) HasToolParts() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, p := range t.partsByID {
		if p != nil && p.Type == "tool" {
			return true
		}
	}
	return len(t.subagentOrder) > 0 || len(t.claudeTasks) > 0 || len(t.childOrder) > 0
}

func (t *TurnTranslator) LastErrorText() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.geminiLastErrMsg
}

// ResetUncommittedTurnForRetry clears any partial text/reasoning parts emitted
// before a retry when no tool calls have executed yet in this turn.
func (t *TurnTranslator) ResetUncommittedTurnForRetry(initialExternalID string) []SSEEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	var evts []SSEEvent
	for _, id := range t.partOrder {
		part := t.partsByID[id]
		if part == nil || (part.Type != "text" && part.Type != "reasoning") {
			continue
		}
		cleared := clonePart(*part)
		cleared.Text = ""
		evts = append(evts, SSEEvent{
			Type:       "message.part.updated",
			Properties: map[string]interface{}{"part": cleared},
		})
	}
	t.partsByID = make(map[string]*MessagePart)
	t.partOrder = nil
	t.stepStartMs = make(map[int]int64)
	t.agyStepUsage = make(map[string]agyUsage)
	t.claudeMsgUsage = make(map[string]claudeUsage)
	t.hasTextPart = false
	t.agyFinalResponseDone = false
	t.turnFinished = false
	t.externalID = initialExternalID
	t.convPinned = false
	t.editPayloads = nil
	t.geminiLastErrMsg = ""
	t.claudeMessageID = ""
	t.claudeReasonPartID = ""
	t.claudeTextPartID = ""
	t.claudeRetrying = false
	t.geminiReasonPartID = ""
	t.geminiTextPartID = ""
	return evts
}

// PrepareForResumeRetry settles any in-flight parts and resets active streaming
// cursors so a resumed retry CLI appends new steps to the same assistant turn.
func (t *TurnTranslator) PrepareForResumeRetry(nowMs int64) []SSEEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	evts := t.finishAgySubagents(true, nowMs)
	evts = append(evts, t.finishClaudeSubagents(true, nowMs)...)
	evts = append(evts, t.closeAllChildren(true, nowMs)...)
	evts = append(evts, t.closeActiveGeminiPart(t.geminiReasonPartID, nowMs)...)
	evts = append(evts, t.closeActiveGeminiPart(t.geminiTextPartID, nowMs)...)
	evts = append(evts, t.closeActiveGeminiPart(t.claudeReasonPartID, nowMs)...)
	evts = append(evts, t.closeActiveGeminiPart(t.claudeTextPartID, nowMs)...)
	for _, id := range t.partOrder {
		part := t.partsByID[id]
		if part == nil {
			continue
		}
		if (part.Type == "text" || part.Type == "reasoning") && (part.Time == nil || part.Time.End == 0) {
			start := nowMs
			if part.Time != nil && part.Time.Start > 0 {
				start = part.Time.Start
			}
			part.Time = &PartTime{Start: start, End: nowMs}
			evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
		} else if part.Type == "tool" && part.Tool != "task" && part.State != nil &&
			(part.State.Status == "pending" || part.State.Status == "running") {
			start := nowMs
			if part.State.Time != nil && part.State.Time.Start > 0 {
				start = part.State.Time.Start
			}
			part.State.Status = "error"
			if strings.TrimSpace(part.State.Error) == "" {
				part.State.Error = "Interrupted before completion"
			}
			part.State.Time = &PartTime{Start: start, End: nowMs}
			evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
		}
	}
	t.agyFinalResponseDone = false
	t.turnFinished = false
	t.geminiStepIdx++
	t.geminiReasonPartID = ""
	t.geminiTextPartID = ""
	t.geminiLastErrMsg = ""
	t.claudeMessageID = ""
	t.claudeReasonPartID = ""
	t.claudeTextPartID = ""
	t.claudeRetrying = false
	return evts
}

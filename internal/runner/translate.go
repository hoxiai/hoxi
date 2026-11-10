package runner

import (
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
	partBuilders         map[string]*strings.Builder
	stepStartMs          map[int]int64
	hasTextPart          bool
	agyFinalResponseDone bool
	agyFinalResponseStep int
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

	claudeMsgUsage         map[string]claudeUsage
	claudeLastParentMsgKey string
	agyStepUsage           map[string]agyUsage
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
		partBuilders:    make(map[string]*strings.Builder),
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
	t.agyFinalResponseStep = 0
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

func (t *TurnTranslator) ResumeEvents() []SSEEvent {
	return []SSEEvent{
		{Type: "session.status", Properties: map[string]interface{}{"sessionID": t.sessionID, "status": SessionStatus{Type: "busy"}}},
		{Type: "message.updated", Properties: map[string]interface{}{"info": t.msgInfo}},
	}
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

func (t *TurnTranslator) appendPartText(part *MessagePart, delta string) {
	if part == nil || delta == "" {
		return
	}
	if t.partBuilders == nil {
		t.partBuilders = make(map[string]*strings.Builder)
	}
	b := t.partBuilders[part.ID]
	if b == nil {
		b = &strings.Builder{}
		if part.Text != "" {
			b.Grow(len(part.Text) + len(delta))
			b.WriteString(part.Text)
		}
		t.partBuilders[part.ID] = b
	}
	b.WriteString(delta)
	part.Text = b.String()
}

func (t *TurnTranslator) setPartText(part *MessagePart, text string) {
	if part == nil {
		return
	}
	part.Text = text
	delete(t.partBuilders, part.ID)
}

func (t *TurnTranslator) insertPartOrderBefore(id, beforeID string) {
	for i, existing := range t.partOrder {
		if existing == beforeID {
			t.partOrder = append(t.partOrder, "")
			copy(t.partOrder[i+1:], t.partOrder[i:])
			t.partOrder[i] = id
			return
		}
	}
	t.partOrder = append(t.partOrder, id)
}

func messageTokensEqual(a, b *MessageTokens) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
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
	if t.geminiLastErrMsg != "" {
		return t.geminiLastErrMsg
	}
	return t.lastClaudeErrorText()
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
	t.partBuilders = make(map[string]*strings.Builder)
	t.partOrder = nil
	t.stepStartMs = make(map[int]int64)
	t.agyStepUsage = make(map[string]agyUsage)
	t.claudeMsgUsage = make(map[string]claudeUsage)
	t.claudeLastParentMsgKey = ""
	t.hasTextPart = false
	t.agyFinalResponseDone = false
	t.agyFinalResponseStep = 0
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
		} else if isInFlightToolPart(*part) {
			start := nowMs
			if part.State.Time != nil && part.State.Time.Start > 0 {
				start = part.State.Time.Start
			}
			part.State.Status = "error"
			if strings.TrimSpace(part.State.Error) == "" {
				part.State.Error = interruptedToolError
			}
			part.State.Time = &PartTime{Start: start, End: nowMs}
			evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
		}
	}
	t.agyFinalResponseDone = false
	t.agyFinalResponseStep = 0
	t.turnFinished = false
	t.partBuilders = make(map[string]*strings.Builder)
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

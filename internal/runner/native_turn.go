package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
	"github.com/hoxiai/hoxi/internal/runner/memstore"
)

// NativeEngine is the narrow contract injected by internal/gateway so runner
// drives the in-process Go Agent without cross-importing internal/agent.
type NativeEngine interface {
	RunTurn(ctx context.Context, req NativeTurnRequest) error
	Summarize(ctx context.Context, providerID, modelID, transcript string) (string, error)
}

// NativeTodoItem, NativeMessage, and NativeContentBlock are aliased in canonical_bridge.go.

// NativeTurnRequest configures one native in-process agent turn.
type NativeTurnRequest struct {
	GoalActive          bool
	SessionID           string
	WorktreeDir         string
	Mode                string
	ProviderID          string
	ModelID             string
	Effort              string
	FastMode            bool
	Orchestrator        *NativeOrchestratorConfig
	System              string
	History             []NativeMessage
	InitialTodos        []NativeTodoItem
	UserPrompt          string
	UserBlocks          []NativeContentBlock
	SteerDrain          func() []string
	SteerDrainItems     func() []NativeSteerItem
	SteerHasPending     func() bool
	SteerNotify         func() <-chan struct{}
	PermissionHook      func(ctx context.Context, req NativePermissionRequest) error
	CoordinateFileEdit  func(ctx context.Context, paths []string, onWait func(waitedPath string)) (NativeFileEditCoordination, error)
	RecordFileEdit      func(paths []string)
	CommandRunner       func(ctx context.Context, dir, command string) (output string, exitCode int, err error)
	StreamCommandRunner func(ctx context.Context, dir, command string, capture io.Writer, onOutput func(string)) (output string, exitCode int, err error)
	BackgroundManager   NativeBackgroundManager
	Diagnostics         func(ctx context.Context, dir, filePath string) string
	AskQuestion         func(ctx context.Context, callID, rawJSON string) (output string, metadata map[string]any, err error)
	Emit                func(evt NativeEvent)
}

// NativePermissionRequest describes a PreToolUse check from the native engine.
type NativePermissionRequest struct {
	SessionID string
	CallID    string
	ToolName  string
	Input     json.RawMessage
	IsRead    bool
	ForceAsk  bool
}

// NativeTokenUsage captures token usage reported by NativeEngine.
type NativeTokenUsage = llm.TokenUsage

// NativeEvent carries streaming text, reasoning, tool lifecycle, and usage updates.
type NativeEvent struct {
	Kind         string
	ParentCallID string
	Step         int
	PartID       string
	CallID       string
	ToolName     string
	Text         string
	Signature    string
	Title        string
	Finish       string
	Input        map[string]any
	Metadata     map[string]any
	Media        []NativeContentBlock
	ErrorText    string
	Usage        *NativeTokenUsage
}

const (
	NativeEventTextStart              = "text_start"
	NativeEventTextDelta              = "text_delta"
	NativeEventTextEnd                = "text_end"
	NativeEventThinkingStart          = "thinking_start"
	NativeEventThinkingDelta          = "thinking_delta"
	NativeEventThinkingEnd            = "thinking_end"
	NativeEventToolPending            = "tool_pending"
	NativeEventToolRunning            = "tool_running"
	NativeEventToolCompleted          = "tool_completed"
	NativeEventToolError              = "tool_error"
	NativeEventSummaryUsage           = "summary_usage"
	NativeEventTurnUsage              = "turn_usage"
	NativeEventTurnFinish             = "turn_finish"
	NativeEventContextCompacted       = "context_compacted"
	NativeEventStepRetry              = "step_retry"
	NativeEventStepRetryDone          = "step_retry_done"
	NativeEventThinkingSignatureReset = "thinking_signature_reset"
	NativeEventSubtaskStart           = "subtask_start"
	NativeEventSubtaskEnd             = "subtask_end"
)

// SetNativeEngine injects the in-process Go Agent engine for the native runtime.
func (s *Service) SetNativeEngine(eng NativeEngine) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.nativeEngine = eng
	s.mu.Unlock()
}

type nativeSteerQueue struct {
	mu     sync.Mutex
	items  []NativeSteerItem
	notify chan struct{}
}

func (q *nativeSteerQueue) Push(text string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	q.PushItem(NativeSteerItem{
		Prompt: trimmed,
		Blocks: []NativeContentBlock{{Type: "text", Text: trimmed}},
	})
}

func (q *nativeSteerQueue) PushItem(item NativeSteerItem) {
	q.mu.Lock()
	q.items = append(q.items, item)
	if q.notify != nil {
		close(q.notify)
		q.notify = nil
	}
	q.mu.Unlock()
}

func (q *nativeSteerQueue) DrainItems() []NativeSteerItem {
	q.mu.Lock()
	out := q.items
	q.items = nil
	q.mu.Unlock()
	return out
}

func (q *nativeSteerQueue) Drain() []string {
	items := q.DrainItems()
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if strings.TrimSpace(it.Prompt) != "" {
			out = append(out, strings.TrimSpace(it.Prompt))
		}
	}
	return out
}

func (s *Service) steerNativeTurn(sessionID, promptText string) bool {
	s.mu.Lock()
	ap := s.procs[sessionID]
	s.mu.Unlock()
	if ap == nil || ap.runtime != string(RuntimeNative) || ap.wasAborted() || ap.steerQueue == nil {
		return false
	}
	ap.steerQueue.Push(promptText)
	return true
}

func (s *Service) startNativeTurn(
	rec SessionRecord,
	mode, modelID, effort, systemPrompt, promptText string,
	userInfo MessageInfo,
	storedParts []MessagePart,
	asstMsgID string,
	deliveryParts ...MessagePart,
) error {
	return s.launchNativeTurn(rec, mode, modelID, effort, systemPrompt, promptText, userInfo, storedParts, asstMsgID, false, nil, deliveryParts...)
}

func (s *Service) launchNativeTurn(
	rec SessionRecord,
	mode, modelID, effort, systemPrompt, promptText string,
	userInfo MessageInfo,
	storedParts []MessagePart,
	asstMsgID string,
	resumeFromHistory bool,
	orchestrator *NativeOrchestratorConfig,
	deliveryParts ...MessagePart,
) error {
	s.mu.Lock()
	eng := s.nativeEngine
	s.mu.Unlock()
	if eng == nil {
		return fmt.Errorf("native Go agent engine is not configured")
	}

	ctx, cancel := context.WithCancel(context.Background())
	steerQ := &nativeSteerQueue{}
	ap := &activeProcess{
		sessionID:  rec.ID,
		runtime:    string(RuntimeNative),
		done:       make(chan struct{}),
		abortCh:    make(chan struct{}),
		cancel:     cancel,
		steerQueue: steerQ,
	}
	ap.stall.touch(time.Now())
	s.mu.Lock()
	s.procs[rec.ID] = ap
	s.mu.Unlock()
	s.beginSessionFileTurn(rec.ID)

	if s.status != nil {
		s.status.SetSessionBusy(rec.ID, true)
	}
	go s.watchStall(ap)

	history := s.projectCanonicalNativeHistory(rec.WorktreePath, rec.Turns, rec.ProviderID, modelID)
	userBlocks := buildNativeUserBlocks(promptText, deliveryParts)
	projectMemory, handoffMemory := memstore.NativeMemoryBlocks(rec, resumeFromHistory)
	if projectMemory != "" && !strings.Contains(systemPrompt, "<project_memory>") {
		systemPrompt = strings.TrimSpace(systemPrompt + "\n\n" + projectMemory)
	}
	if handoffMemory != "" && !strings.Contains(systemPrompt, "<session_handoff_memory>") {
		block := NativeContentBlock{Type: "text", Text: strings.TrimSpace(handoffMemory)}
		if rec.ContextPending || resumeFromHistory || len(history) == 0 {
			userBlocks = append([]NativeContentBlock{block}, userBlocks...)
		} else if history[0].Role == "user" {
			history[0].Content = append([]NativeContentBlock{block}, history[0].Content...)
		} else {
			history = append([]NativeMessage{{Role: "user", Content: []NativeContentBlock{block}}}, history...)
		}
	}

	tr := NewTurnTranslator(rec.ID, string(RuntimeNative), asstMsgID, userInfo.ID, rec.ProviderID, modelID, mode, "")
	tr.CreateChild = s.childSessionFactory(rec, modelID, "plan")
	subBridge := newNativeSubagentBridge(s, rec.ID, modelID, tr)
	if resumeFromHistory {
		s.broadcast(rec.ID, tr.ResumeEvents()...)
	} else {
		s.broadcast(rec.ID, tr.InitialEvents(userInfo, storedParts)...)
	}

	go func() {
		var terminalEvts []SSEEvent
		var activeMu sync.Mutex
		curUserMsgID := userInfo.ID
		curAsstMsgID := asstMsgID
		emittedAnyPart := false

		defer func() {
			s.cancelSessionApprovals(rec.ID, "the native agent turn ended")
			activeMu.Lock()
			latestUserID := curUserMsgID
			activeMu.Unlock()
			if !ap.wasRestarting() {
				s.clearInflightTurnForUserMsg(rec.ID, latestUserID)
			}
			s.syncSessionMemoryFile(rec.ID)
			s.store.FlushSession(rec.ID)
			s.releaseActiveProcess(rec.ID, ap)
			if len(terminalEvts) > 0 && !ap.wasSteered() && !ap.wasRestarting() {
				s.broadcastRouted(rec.ID, terminalEvts)
			}
			s.clearSessionQuestionState(rec.ID)
			close(ap.done)
		}()

		var totalUsage, summaryUsage NativeTokenUsage
		var latestParentStep NativeTokenUsage
		turnFinish := "stop"
		partStartTimes := make(map[string]int64)

		drainSteerItems := func() []NativeSteerItem {
			items := steerQ.DrainItems()
			if len(items) == 0 {
				return nil
			}
			nowMs := time.Now().UnixMilli()
			last := items[len(items)-1]
			if strings.TrimSpace(last.UserMsgID) != "" {
				activeMu.Lock()
				oldAsstID := curAsstMsgID
				hadParts := emittedAnyPart
				prevInfo := tr.MsgInfo
				hadUsage := totalUsage != (NativeTokenUsage{})
				if hadUsage {
					prevInfo.Tokens = buildNativeMessageTokens(totalUsage, latestParentStep, summaryUsage)
					prevInfo.Cost = totalUsage.CostUSD
				}
				totalUsage, latestParentStep, summaryUsage = NativeTokenUsage{}, NativeTokenUsage{}, NativeTokenUsage{}
				nextAsstID := s.nextNativeAssistantMessageID(nowMs)
				curUserMsgID = last.UserMsgID
				curAsstMsgID = nextAsstID
				emittedAnyPart = false
				newInfo := MessageInfo{
					ID:         nextAsstID,
					SessionID:  rec.ID,
					Role:       "assistant",
					ParentID:   curUserMsgID,
					Time:       MessageTime{Created: nowMs},
					ModelID:    modelID,
					ProviderID: rec.ProviderID,
					Mode:       mode,
					Agent:      mode,
				}
				tr.AdvanceMessage(nextAsstID, curUserMsgID, newInfo)
				activeMu.Unlock()

				if !hadParts && !hadUsage {
					s.pruneEmptyAssistantTurn(rec.ID, oldAsstID)
				} else {
					prevInfo.Time.Completed = nowMs
					prevInfo.Finish = "tool-calls"
					s.broadcast(rec.ID, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": prevInfo}})
				}
				s.broadcast(rec.ID, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": newInfo}})
			}
			return items
		}

		stepMeta := func(step int) map[string]interface{} {
			if step <= 0 {
				return nil
			}
			return map[string]interface{}{"step": step}
		}

		emit := func(evt NativeEvent) {
			s.noteActivity(ap)
			nowMs := time.Now().UnixMilli()
			activeMu.Lock()
			asstID := curAsstMsgID
			activeMu.Unlock()
			if strings.TrimSpace(evt.ParentCallID) != "" {
				if evt.Kind == NativeEventTurnUsage || evt.Kind == NativeEventSummaryUsage {
					if evt.Usage != nil {
						activeMu.Lock()
						totalUsage.Add(*evt.Usage)
						if evt.Kind == NativeEventSummaryUsage {
							summaryUsage.Add(*evt.Usage)
						}
						activeMu.Unlock()
					}
					return
				}
				if childEvts := subBridge.handleChildEvent(evt, nowMs); len(childEvts) > 0 {
					s.noteToolEvents(rec.ID, childEvts)
					s.broadcastRouted(rec.ID, childEvts)
				}
				return
			}
			switch evt.Kind {
			case NativeEventThinkingStart:
				activeMu.Lock()
				emittedAnyPart = true
				partStartTimes[evt.PartID] = nowMs
				activeMu.Unlock()
				meta := map[string]interface{}{
					"providerID": rec.ProviderID,
					"modelID":    modelID,
				}
				if evt.Step > 0 {
					meta["step"] = evt.Step
				}
				s.broadcastRouted(rec.ID, []SSEEvent{{
					Type: "message.part.updated",
					Properties: map[string]interface{}{
						"part": MessagePart{
							ID: evt.PartID, SessionID: rec.ID, MessageID: asstID,
							Type: "reasoning", Text: "", Time: &PartTime{Start: nowMs},
							Metadata: meta,
						},
					},
				}})
			case NativeEventThinkingDelta:
				s.broadcastRouted(rec.ID, []SSEEvent{{
					Type: "message.part.delta",
					Properties: map[string]interface{}{
						"sessionID": rec.ID, "messageID": asstID, "partID": evt.PartID, "field": "text", "delta": evt.Text,
					},
				}})
			case NativeEventThinkingEnd:
				activeMu.Lock()
				emittedAnyPart = true
				startMs := partStartTimes[evt.PartID]
				activeMu.Unlock()
				if startMs == 0 {
					startMs = nowMs
				}
				meta := map[string]interface{}{
					"providerID": rec.ProviderID,
					"modelID":    modelID,
				}
				if evt.Step > 0 {
					meta["step"] = evt.Step
				}
				if strings.TrimSpace(evt.Signature) != "" {
					meta["signature"] = strings.TrimSpace(evt.Signature)
				}
				if rd := stringMetaField(evt.Metadata, "redactedData"); rd != "" {
					meta["redactedData"] = rd
				}
				s.broadcastRouted(rec.ID, []SSEEvent{{
					Type: "message.part.updated",
					Properties: map[string]interface{}{
						"part": MessagePart{
							ID: evt.PartID, SessionID: rec.ID, MessageID: asstID,
							Type: "reasoning", Text: evt.Text, Time: &PartTime{Start: startMs, End: nowMs},
							Metadata: meta,
						},
					},
				}})
			case NativeEventTextStart:
				activeMu.Lock()
				emittedAnyPart = true
				partStartTimes[evt.PartID] = nowMs
				activeMu.Unlock()
				s.broadcastRouted(rec.ID, []SSEEvent{{
					Type: "message.part.updated",
					Properties: map[string]interface{}{
						"part": MessagePart{
							ID: evt.PartID, SessionID: rec.ID, MessageID: asstID,
							Type: "text", Text: "", Time: &PartTime{Start: nowMs},
							Metadata: stepMeta(evt.Step),
						},
					},
				}})
			case NativeEventTextDelta:
				s.broadcastRouted(rec.ID, []SSEEvent{{
					Type: "message.part.delta",
					Properties: map[string]interface{}{
						"sessionID": rec.ID, "messageID": asstID, "partID": evt.PartID, "field": "text", "delta": evt.Text,
					},
				}})
			case NativeEventTextEnd:
				activeMu.Lock()
				emittedAnyPart = true
				startMs := partStartTimes[evt.PartID]
				activeMu.Unlock()
				if startMs == 0 {
					startMs = nowMs
				}
				s.broadcastRouted(rec.ID, []SSEEvent{{
					Type: "message.part.updated",
					Properties: map[string]interface{}{
						"part": MessagePart{
							ID: evt.PartID, SessionID: rec.ID, MessageID: asstID,
							Type: "text", Text: evt.Text, Time: &PartTime{Start: startMs, End: nowMs},
							Metadata: stepMeta(evt.Step),
						},
					},
				}})
			case NativeEventToolPending, NativeEventToolRunning, NativeEventToolCompleted, NativeEventToolError:
				status := "pending"
				var pt *PartTime
				activeMu.Lock()
				emittedAnyPart = true
				startMs := partStartTimes[evt.PartID]
				if evt.Kind == NativeEventToolPending || startMs == 0 {
					startMs = nowMs
					partStartTimes[evt.PartID] = startMs
				}
				activeMu.Unlock()
				meta := cloneGenericMap(evt.Metadata)
				if evt.Step > 0 {
					if meta == nil {
						meta = map[string]interface{}{}
					}
					meta["step"] = evt.Step
				}
				switch evt.Kind {
				case NativeEventToolRunning:
					status = "running"
					pt = &PartTime{Start: startMs}
				case NativeEventToolCompleted:
					status = "completed"
					pt = &PartTime{Start: startMs, End: nowMs}
					if meta == nil {
						meta = map[string]interface{}{}
					}
					meta["outcome"] = ToolOutcomeCompleted
				case NativeEventToolError:
					status = "error"
					pt = &PartTime{Start: startMs, End: nowMs}
					if meta == nil {
						meta = map[string]interface{}{}
					}
					if _, exists := meta["outcome"]; !exists {
						meta["outcome"] = ToolOutcomeFailed
					}
				}
				if evt.Kind == NativeEventToolCompleted || evt.Kind == NativeEventToolError {
					if refs := s.saveNativeToolMediaAttachments(rec, evt.Media); len(refs) > 0 {
						meta["mediaAttachments"] = refs
					}
				}
				part := MessagePart{
					ID: evt.PartID, SessionID: rec.ID, MessageID: asstID,
					Type: "tool", Tool: evt.ToolName, CallID: evt.CallID,
					Metadata: stepMeta(evt.Step),
					State: &ToolState{
						Status: status, Input: evt.Input, Output: evt.Text,
						Title: evt.Title, Error: evt.ErrorText, Metadata: meta, Time: pt,
					},
				}
				subBridge.enrichParentTaskPart(&part, nowMs)
				sseEvts := []SSEEvent{{
					Type:       "message.part.updated",
					Properties: map[string]interface{}{"part": part},
				}}
				s.noteToolEvents(rec.ID, sseEvts)
				s.broadcastRouted(rec.ID, sseEvts)
			case NativeEventContextCompacted:
				if strings.TrimSpace(evt.Text) != "" {
					activeMu.Lock()
					emittedAnyPart = true
					activeMu.Unlock()
					cmpMeta := cloneGenericMap(evt.Metadata)
					if cmpMeta == nil {
						cmpMeta = map[string]interface{}{}
					}
					if evt.Step > 0 {
						cmpMeta["step"] = evt.Step
					}
					s.broadcastRouted(rec.ID, []SSEEvent{{
						Type: "message.part.updated",
						Properties: map[string]interface{}{
							"part": MessagePart{
								ID:        "prt_cmp_" + asstID,
								SessionID: rec.ID,
								MessageID: asstID,
								Type:      "compaction",
								Text:      strings.TrimSpace(evt.Text),
								Metadata:  cmpMeta,
								Time:      &PartTime{Start: nowMs, End: nowMs},
							},
						},
					}})
				}
			case "mode_promoted":
				if nextMode := strings.TrimSpace(evt.Text); nextMode != "" {
					activeMu.Lock()
					mode = nextMode
					rec.Mode = nextMode
					activeMu.Unlock()
					s.applyNativeModePromotion(rec.ID, nextMode, tr)
				}
			case NativeEventTurnFinish:
				if f := strings.TrimSpace(evt.Finish); f != "" {
					activeMu.Lock()
					turnFinish = f
					activeMu.Unlock()
				}
			case NativeEventStepRetry:
				attempt := intFromMap(evt.Metadata, "attempt")
				if attempt <= 0 {
					attempt = 1
				}
				var nextMs int64
				switch v := evt.Metadata["next"].(type) {
				case int64:
					nextMs = v
				case int:
					nextMs = int64(v)
				case float64:
					nextMs = int64(v)
				}
				s.broadcastRouted(rec.ID, []SSEEvent{{
					Type: "session.status",
					Properties: map[string]interface{}{
						"sessionID": rec.ID,
						"status": SessionStatus{
							Type:    "retry",
							Attempt: attempt,
							Message: summarizeTurnRetryReason(evt.ErrorText),
							Next:    nextMs,
						},
					},
				}})
			case NativeEventStepRetryDone:
				s.broadcastRouted(rec.ID, []SSEEvent{{
					Type: "session.status",
					Properties: map[string]interface{}{
						"sessionID": rec.ID,
						"status":    SessionStatus{Type: "busy"},
					},
				}})
			case NativeEventThinkingSignatureReset:
				s.clearSessionThinkingSignatures(rec.ID)
			case NativeEventTurnUsage, NativeEventSummaryUsage:
				if evt.Usage != nil {
					activeMu.Lock()
					totalUsage.Add(*evt.Usage)
					if evt.Kind == NativeEventSummaryUsage {
						summaryUsage.Add(*evt.Usage)
					}
					if evt.Kind == NativeEventTurnUsage && evt.Usage.CacheDrop != nil {
						totalUsage.CacheDrop = evt.Usage.CacheDrop
					}
					if evt.Usage.ServiceTier != "" {
						totalUsage.ServiceTier = evt.Usage.ServiceTier
						tr.MsgInfo.ServiceTier = evt.Usage.ServiceTier
					}
					if evt.Kind == NativeEventTurnUsage && nativeStepContextTokens(*evt.Usage) > 0 {
						latestParentStep = *evt.Usage
					}
					liveTokens := buildNativeMessageTokens(totalUsage, latestParentStep, summaryUsage)
					liveInfo := tr.MsgInfo
					liveInfo.Tokens = liveTokens
					if totalUsage.CostUSD > 0 {
						liveInfo.Cost = totalUsage.CostUSD
					}
					activeMu.Unlock()
					s.broadcastRouted(rec.ID, []SSEEvent{{
						Type:       "message.updated",
						Properties: map[string]interface{}{"info": liveInfo},
					}})
				}
			}
		}

		currentAsstID := func() string {
			activeMu.Lock()
			defer activeMu.Unlock()
			return curAsstMsgID
		}

		err := eng.RunTurn(ctx, NativeTurnRequest{
			SessionID:       rec.ID,
			WorktreeDir:     rec.WorktreePath,
			Mode:            mode,
			ProviderID:      rec.ProviderID,
			ModelID:         modelID,
			Effort:          effort,
			FastMode:        userInfo.ServiceTier == "fast",
			Orchestrator:    orchestrator,
			GoalActive:      userInfo.GoalActive,
			System:          systemPrompt,
			History:         history,
			InitialTodos:    extractLatestSessionTodos(rec.Turns),
			UserPrompt:      promptText,
			UserBlocks:      userBlocks,
			SteerDrain:      steerQ.Drain,
			SteerDrainItems: drainSteerItems,
			SteerHasPending: steerQ.HasPending,
			SteerNotify:     steerQ.NotifyCh,
			PermissionHook: func(pCtx context.Context, pr NativePermissionRequest) error {
				return s.nativePermissionHook(rec, currentAsstID())(pCtx, pr)
			},
			CoordinateFileEdit:  s.nativeCoordinateFileEdit(rec, ap),
			RecordFileEdit:      s.nativeRecordFileEdit(rec),
			CommandRunner:       s.nativePromotableCommandRunner(rec, ap),
			StreamCommandRunner: s.nativePromotableStreamCommandRunner(rec, ap),
			BackgroundManager:   s.nativeBackgroundManager(rec),
			Diagnostics:         s.nativeDiagnostics(rec),
			AskQuestion: func(qCtx context.Context, callID, rawJSON string) (string, map[string]any, error) {
				return s.nativeQuestionAsker(rec, currentAsstID())(qCtx, callID, rawJSON)
			},
			Emit: emit,
		})

		nowCompleted := time.Now().UnixMilli()
		activeMu.Lock()
		finalTokens := buildNativeMessageTokens(totalUsage, latestParentStep, summaryUsage)
		finalCost := totalUsage.CostUSD
		resolvedFinish := turnFinish
		finalAsstID := curAsstMsgID
		hadParts := emittedAnyPart
		hadUsage := totalUsage != (NativeTokenUsage{})
		activeMu.Unlock()
		if resolvedFinish == "" {
			resolvedFinish = "stop"
		}
		tr.MsgInfo.Time.Completed = nowCompleted
		tr.MsgInfo.Tokens = finalTokens
		if finalCost > 0 {
			tr.MsgInfo.Cost = finalCost
		}

		if err != nil {
			if abandoned := subBridge.abandonAll(nowCompleted); len(abandoned) > 0 {
				s.broadcastRouted(rec.ID, abandoned)
			}
			stallErr := ap.stall.abortError()
			userCancelled := ap.wasAborted() && stallErr == nil
			s.settleSessionInFlightParts(rec.ID, nowCompleted, userCancelled)
			if userCancelled {
				if !hadParts && !hadUsage {
					s.pruneEmptyAssistantTurn(rec.ID, finalAsstID)
				} else {
					tr.MsgInfo.Finish = "aborted"
					s.broadcast(rec.ID, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": tr.MsgInfo}})
				}
				terminalEvts = append(terminalEvts, SSEEvent{
					Type: "session.status", Properties: map[string]interface{}{"sessionID": rec.ID, "status": SessionStatus{Type: "idle"}},
				})
				return
			}
			finish, msgErr := s.classifyNativeTurnFailure(rec.ID, finalAsstID, err, stallErr)
			tr.MsgInfo.Error = msgErr
			tr.MsgInfo.Finish = finish
			s.broadcast(rec.ID, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": tr.MsgInfo}})
			terminalEvts = append(terminalEvts,
				SSEEvent{Type: "session.error", Properties: map[string]interface{}{"sessionID": rec.ID, "error": msgErr}},
				SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": rec.ID, "status": SessionStatus{Type: "idle"}}},
			)
			return
		}

		if rec.ContextPending {
			s.markContextDelivered(rec.ID)
		}
		tr.MsgInfo.Finish = resolvedFinish
		s.broadcast(rec.ID, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": tr.MsgInfo}})
		terminalEvts = append(terminalEvts,
			SSEEvent{Type: "session.idle", Properties: map[string]interface{}{"sessionID": rec.ID}},
			SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": rec.ID, "status": SessionStatus{Type: "idle"}}},
		)
	}()

	return nil
}

func (s *Service) nativeQuestionAsker(rec SessionRecord, asstMsgID string) func(context.Context, string, string) (string, map[string]any, error) {
	return func(ctx context.Context, callID, rawJSON string) (string, map[string]any, error) {
		var rawArgs map[string]interface{}
		_ = json.Unmarshal([]byte(rawJSON), &rawArgs)
		if rawArgs == nil {
			rawArgs = map[string]interface{}{}
		}
		fakeHTTP, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
		cred := hookCredential{runtime: string(RuntimeNative), sessionID: rec.ID, messageID: asstMsgID}
		_, reason := s.gateQuestion(fakeHTTP, cred, rec, "question", rawArgs, callID)
		if resolved, ok := s.lookupSessionQuestion(rec.ID, callID); ok {
			meta := map[string]any{}
			if resolved.rejected {
				meta["rejected"] = true
				return "", meta, fmt.Errorf("%s", firstNonEmptyStr(resolved.reason, questionDismissedReason))
			}
			meta["answers"] = resolved.answers
			return firstNonEmptyStr(resolved.output, reason), meta, nil
		}
		return reason, nil, nil
	}
}

func (s *Service) buildNativeHistory(turns []MessageTurn) []NativeMessage {
	return s.projectCanonicalNativeHistory(s.rootDir, turns, "", "")
}

func nativeStepContextTokens(u NativeTokenUsage) int {
	return u.Input + u.Output + u.Reasoning + u.CacheRead + u.CacheWrite
}

// buildNativeMessageTokens keeps cumulative billing buckets across all steps
// and subagents in Input/Output/Reasoning/Cache while setting Total & Context
// strictly to the latest parent step's real context-window occupancy.
func buildNativeMessageTokens(total, latestParent, summary NativeTokenUsage) *MessageTokens {
	ctxTokens := nativeStepContextTokens(latestParent)
	if ctxTokens <= 0 {
		ctxTokens = nativeStepContextTokens(total) - nativeStepContextTokens(summary)
	}
	tok := &MessageTokens{
		Total:     ctxTokens,
		Context:   ctxTokens,
		Input:     total.Input,
		Output:    total.Output,
		Reasoning: total.Reasoning,
	}
	if summary != (NativeTokenUsage{}) {
		tok.Summary = &summary
	}
	tok.ReportedCacheInput = total.ReportedCacheInput
	tok.CacheDrop = total.CacheDrop
	tok.Cache.Read = total.CacheRead
	tok.Cache.Write = total.CacheWrite
	return tok
}

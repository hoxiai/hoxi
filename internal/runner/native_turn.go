package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/attachment"
)

// NativeEngine is the narrow contract injected by internal/gateway so runner
// drives the in-process Go Agent without cross-importing internal/agent.
type NativeEngine interface {
	RunTurn(ctx context.Context, req NativeTurnRequest) error
	Summarize(ctx context.Context, providerID, modelID, transcript string) (string, error)
}

// NativeTurnRequest configures one native in-process agent turn.
type NativeTurnRequest struct {
	SessionID      string
	WorktreeDir    string
	Mode           string
	ProviderID     string
	ModelID        string
	Effort         string
	System         string
	History        []NativeMessage
	UserPrompt     string
	UserBlocks     []NativeContentBlock
	SteerDrain     func() []string
	PermissionHook func(ctx context.Context, req NativePermissionRequest) error
	CommandRunner  func(ctx context.Context, dir, command string) (output string, exitCode int, err error)
	Diagnostics    func(ctx context.Context, dir, filePath string) string
	AskQuestion    func(ctx context.Context, callID, rawJSON string) (output string, metadata map[string]any, err error)
	Emit           func(evt NativeEvent)
}

// NativeMessage represents one canonical message passed into NativeEngine.
type NativeMessage struct {
	Role    string               `json:"role"`
	Content []NativeContentBlock `json:"content"`
}

// NativeContentBlock represents one content block inside NativeMessage.
type NativeContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	MimeType  string          `json:"mime_type,omitempty"`
	Data      string          `json:"data,omitempty"`
	Filename  string          `json:"filename,omitempty"`
	Signature string          `json:"signature,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
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
type NativeTokenUsage struct {
	Input      int
	Output     int
	Reasoning  int
	CacheRead  int
	CacheWrite int
}

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
	NativeEventTextStart        = "text_start"
	NativeEventTextDelta        = "text_delta"
	NativeEventTextEnd          = "text_end"
	NativeEventThinkingStart    = "thinking_start"
	NativeEventThinkingDelta    = "thinking_delta"
	NativeEventThinkingEnd      = "thinking_end"
	NativeEventToolPending      = "tool_pending"
	NativeEventToolRunning      = "tool_running"
	NativeEventToolCompleted    = "tool_completed"
	NativeEventToolError        = "tool_error"
	NativeEventTurnUsage        = "turn_usage"
	NativeEventTurnFinish       = "turn_finish"
	NativeEventContextCompacted = "context_compacted"
	NativeEventSubtaskStart     = "subtask_start"
	NativeEventSubtaskEnd       = "subtask_end"
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
	mu    sync.Mutex
	items []string
}

func (q *nativeSteerQueue) Push(text string) {
	q.mu.Lock()
	if strings.TrimSpace(text) != "" {
		q.items = append(q.items, strings.TrimSpace(text))
	}
	q.mu.Unlock()
}

func (q *nativeSteerQueue) Drain() []string {
	q.mu.Lock()
	out := q.items
	q.items = nil
	q.mu.Unlock()
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

	if s.status != nil {
		s.status.SetSessionBusy(rec.ID, true)
	}
	go s.watchStall(ap)

	tr := NewTurnTranslator(rec.ID, string(RuntimeNative), asstMsgID, userInfo.ID, rec.ProviderID, modelID, mode, "")
	tr.createChild = s.childSessionFactory(rec, modelID, "plan")
	subBridge := newNativeSubagentBridge(s, rec.ID, modelID, tr)
	s.broadcast(rec.ID, tr.InitialEvents(userInfo, storedParts)...)

	history := s.projectCanonicalNativeHistory(rec.Turns, rec.ProviderID, modelID)
	userBlocks := buildNativeUserBlocks(promptText, deliveryParts)

	go func() {
		var terminalEvts []SSEEvent
		defer func() {
			s.cancelSessionApprovals(rec.ID, "the native agent turn ended")
			if !ap.wasRestarting() {
				s.clearInflightTurnForUserMsg(rec.ID, userInfo.ID)
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

		var totalUsage NativeTokenUsage
		var latestParentStep NativeTokenUsage
		turnFinish := "stop"
		var activeMu sync.Mutex
		partStartTimes := make(map[string]int64)

		stepMeta := func(step int) map[string]interface{} {
			if step <= 0 {
				return nil
			}
			return map[string]interface{}{"step": step}
		}

		emit := func(evt NativeEvent) {
			s.noteActivity(ap)
			nowMs := time.Now().UnixMilli()
			if strings.TrimSpace(evt.ParentCallID) != "" {
				if evt.Kind == NativeEventTurnUsage {
					if evt.Usage != nil {
						activeMu.Lock()
						totalUsage.Input += evt.Usage.Input
						totalUsage.Output += evt.Usage.Output
						totalUsage.Reasoning += evt.Usage.Reasoning
						totalUsage.CacheRead += evt.Usage.CacheRead
						totalUsage.CacheWrite += evt.Usage.CacheWrite
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
							ID: evt.PartID, SessionID: rec.ID, MessageID: asstMsgID,
							Type: "reasoning", Text: "", Time: &PartTime{Start: nowMs},
							Metadata: meta,
						},
					},
				}})
			case NativeEventThinkingDelta:
				s.broadcastRouted(rec.ID, []SSEEvent{{
					Type: "message.part.delta",
					Properties: map[string]interface{}{
						"sessionID": rec.ID, "messageID": asstMsgID, "partID": evt.PartID, "field": "text", "delta": evt.Text,
					},
				}})
			case NativeEventThinkingEnd:
				activeMu.Lock()
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
				s.broadcastRouted(rec.ID, []SSEEvent{{
					Type: "message.part.updated",
					Properties: map[string]interface{}{
						"part": MessagePart{
							ID: evt.PartID, SessionID: rec.ID, MessageID: asstMsgID,
							Type: "reasoning", Text: evt.Text, Time: &PartTime{Start: startMs, End: nowMs},
							Metadata: meta,
						},
					},
				}})
			case NativeEventTextStart:
				activeMu.Lock()
				partStartTimes[evt.PartID] = nowMs
				activeMu.Unlock()
				s.broadcastRouted(rec.ID, []SSEEvent{{
					Type: "message.part.updated",
					Properties: map[string]interface{}{
						"part": MessagePart{
							ID: evt.PartID, SessionID: rec.ID, MessageID: asstMsgID,
							Type: "text", Text: "", Time: &PartTime{Start: nowMs},
							Metadata: stepMeta(evt.Step),
						},
					},
				}})
			case NativeEventTextDelta:
				s.broadcastRouted(rec.ID, []SSEEvent{{
					Type: "message.part.delta",
					Properties: map[string]interface{}{
						"sessionID": rec.ID, "messageID": asstMsgID, "partID": evt.PartID, "field": "text", "delta": evt.Text,
					},
				}})
			case NativeEventTextEnd:
				activeMu.Lock()
				startMs := partStartTimes[evt.PartID]
				activeMu.Unlock()
				if startMs == 0 {
					startMs = nowMs
				}
				s.broadcastRouted(rec.ID, []SSEEvent{{
					Type: "message.part.updated",
					Properties: map[string]interface{}{
						"part": MessagePart{
							ID: evt.PartID, SessionID: rec.ID, MessageID: asstMsgID,
							Type: "text", Text: evt.Text, Time: &PartTime{Start: startMs, End: nowMs},
							Metadata: stepMeta(evt.Step),
						},
					},
				}})
			case NativeEventToolPending, NativeEventToolRunning, NativeEventToolCompleted, NativeEventToolError:
				status := "pending"
				var pt *PartTime
				activeMu.Lock()
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
					if refs := s.saveNativeToolMediaAttachments(rec, evt.Media); len(refs) > 0 {
						meta["mediaAttachments"] = refs
					}
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
				part := MessagePart{
					ID: evt.PartID, SessionID: rec.ID, MessageID: asstMsgID,
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
								ID:        "prt_cmp_" + asstMsgID,
								SessionID: rec.ID,
								MessageID: asstMsgID,
								Type:      "compaction",
								Text:      strings.TrimSpace(evt.Text),
								Metadata:  cmpMeta,
								Time:      &PartTime{Start: nowMs, End: nowMs},
							},
						},
					}})
				}
			case NativeEventTurnFinish:
				if f := strings.TrimSpace(evt.Finish); f != "" {
					activeMu.Lock()
					turnFinish = f
					activeMu.Unlock()
				}
			case NativeEventTurnUsage:
				if evt.Usage != nil {
					activeMu.Lock()
					totalUsage.Input += evt.Usage.Input
					totalUsage.Output += evt.Usage.Output
					totalUsage.Reasoning += evt.Usage.Reasoning
					totalUsage.CacheRead += evt.Usage.CacheRead
					totalUsage.CacheWrite += evt.Usage.CacheWrite
					if nativeStepContextTokens(*evt.Usage) > 0 {
						latestParentStep = *evt.Usage
					}
					liveTokens := buildNativeMessageTokens(totalUsage, latestParentStep)
					liveInfo := tr.msgInfo
					liveInfo.Tokens = liveTokens
					activeMu.Unlock()
					s.broadcastRouted(rec.ID, []SSEEvent{{
						Type:       "message.updated",
						Properties: map[string]interface{}{"info": liveInfo},
					}})
				}
			}
		}

		err := eng.RunTurn(ctx, NativeTurnRequest{
			SessionID:      rec.ID,
			WorktreeDir:    rec.WorktreePath,
			Mode:           mode,
			ProviderID:     rec.ProviderID,
			ModelID:        modelID,
			Effort:         effort,
			System:         systemPrompt,
			History:        history,
			UserPrompt:     promptText,
			UserBlocks:     userBlocks,
			SteerDrain:     steerQ.Drain,
			PermissionHook: s.nativePermissionHook(rec, asstMsgID),
			CommandRunner:  s.nativeCommandRunner(rec, ap),
			Diagnostics:    s.nativeDiagnostics(rec),
			AskQuestion:    s.nativeQuestionAsker(rec, asstMsgID),
			Emit:           emit,
		})

		nowCompleted := time.Now().UnixMilli()
		activeMu.Lock()
		finalTokens := buildNativeMessageTokens(totalUsage, latestParentStep)
		resolvedFinish := turnFinish
		activeMu.Unlock()
		if resolvedFinish == "" {
			resolvedFinish = "stop"
		}
		tr.msgInfo.Time.Completed = nowCompleted
		tr.msgInfo.Tokens = finalTokens

		if err != nil {
			if abandoned := subBridge.abandonAll(nowCompleted); len(abandoned) > 0 {
				s.broadcastRouted(rec.ID, abandoned)
			}
			stallErr := ap.stall.abortError()
			userCancelled := ap.wasAborted() && stallErr == nil
			s.settleSessionInFlightParts(rec.ID, nowCompleted, userCancelled)
			if userCancelled {
				tr.msgInfo.Finish = "aborted"
				s.broadcast(rec.ID, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": tr.msgInfo}})
				terminalEvts = append(terminalEvts, SSEEvent{
					Type: "session.status", Properties: map[string]interface{}{"sessionID": rec.ID, "status": SessionStatus{Type: "idle"}},
				})
				return
			}
			finish, msgErr := s.classifyNativeTurnFailure(rec.ID, asstMsgID, err, stallErr)
			tr.msgInfo.Error = msgErr
			tr.msgInfo.Finish = finish
			s.broadcast(rec.ID, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": tr.msgInfo}})
			terminalEvts = append(terminalEvts,
				SSEEvent{Type: "session.error", Properties: map[string]interface{}{"sessionID": rec.ID, "error": msgErr}},
				SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": rec.ID, "status": SessionStatus{Type: "idle"}}},
			)
			return
		}

		tr.msgInfo.Finish = resolvedFinish
		s.broadcast(rec.ID, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": tr.msgInfo}})
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

func (s *Service) saveNativeToolMediaAttachments(rec SessionRecord, media []NativeContentBlock) []map[string]interface{} {
	if s == nil || s.attachments == nil || len(media) == 0 {
		return nil
	}
	var refs []map[string]interface{}
	for _, mb := range media {
		mime := strings.TrimSpace(mb.MimeType)
		data := strings.TrimSpace(mb.Data)
		if mime == "" || data == "" {
			continue
		}
		fname := strings.TrimSpace(mb.Filename)
		if fname == "" {
			fname = "tool_media"
		}
		saved, err := s.attachments.Save(attachment.SaveRequest{
			SessionID: rec.ID,
			Directory: rec.WorktreePath,
			Filename:  fname,
			Mime:      mime,
			DataURL:   "data:" + mime + ";base64," + data,
		})
		if err != nil || saved.ID == "" {
			continue
		}
		refs = append(refs, map[string]interface{}{
			"id":       saved.ID,
			"url":      saved.URL,
			"mime":     saved.Mime,
			"filename": saved.Filename,
		})
	}
	return refs
}

func buildNativeUserBlocks(promptText string, deliveryParts []MessagePart) []NativeContentBlock {
	var blocks []NativeContentBlock
	if strings.TrimSpace(promptText) != "" {
		blocks = append(blocks, NativeContentBlock{Type: "text", Text: promptText})
	}
	for _, p := range deliveryParts {
		if p.Type != "file" || strings.TrimSpace(p.URL) == "" {
			continue
		}
		if mimeType, b64Data, _, ok := parseBase64DataURL(p.URL, p.Mime); ok {
			blocks = append(blocks, NativeContentBlock{
				Type:     "media",
				MimeType: mimeType,
				Data:     b64Data,
				Filename: p.Filename,
			})
		}
	}
	if len(blocks) == 0 {
		blocks = append(blocks, NativeContentBlock{Type: "text", Text: promptText})
	}
	return blocks
}

func (s *Service) resolveNativeHistoryFileBlock(p MessagePart) (NativeContentBlock, bool) {
	rawURL := strings.TrimSpace(p.URL)
	if !strings.HasPrefix(rawURL, "data:") && s != nil && s.attachments != nil && rawURL != "" {
		for _, prefix := range []string{"/api/hoxi/attachments/", "hoxi-attachment://"} {
			if idx := strings.Index(rawURL, prefix); idx >= 0 {
				refID := strings.TrimSpace(rawURL[idx+len(prefix):])
				if slash := strings.IndexByte(refID, '/'); slash >= 0 {
					refID = refID[:slash]
				}
				if q := strings.IndexByte(refID, '?'); q >= 0 {
					refID = refID[:q]
				}
				if refID != "" {
					if _, dataURL, err := s.attachments.ReadAsDataURL(refID); err == nil && dataURL != "" {
						rawURL = dataURL
					}
				}
				break
			}
		}
	}
	if rawURL == "" {
		return NativeContentBlock{}, false
	}
	mimeType, b64Data, _, ok := parseBase64DataURL(rawURL, p.Mime)
	if !ok {
		return NativeContentBlock{}, false
	}
	return NativeContentBlock{
		Type:     "media",
		MimeType: mimeType,
		Data:     b64Data,
		Filename: p.Filename,
	}, true
}

func (s *Service) buildNativeHistory(turns []MessageTurn) []NativeMessage {
	return s.projectCanonicalNativeHistory(turns, "", "")
}

func nativeStepContextTokens(u NativeTokenUsage) int {
	return u.Input + u.Output + u.Reasoning + u.CacheRead + u.CacheWrite
}

// buildNativeMessageTokens keeps cumulative billing buckets across all steps
// and subagents in Input/Output/Reasoning/Cache while setting Total & Context
// strictly to the latest parent step's real context-window occupancy.
func buildNativeMessageTokens(total, latestParent NativeTokenUsage) *MessageTokens {
	ctxTokens := nativeStepContextTokens(latestParent)
	if ctxTokens <= 0 {
		ctxTokens = nativeStepContextTokens(total)
	}
	tok := &MessageTokens{
		Total:     ctxTokens,
		Context:   ctxTokens,
		Input:     total.Input,
		Output:    total.Output,
		Reasoning: total.Reasoning,
	}
	tok.Cache.Read = total.CacheRead
	tok.Cache.Write = total.CacheWrite
	return tok
}

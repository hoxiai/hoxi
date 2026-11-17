package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

const (
	subtaskMaxSteps                = 12
	subtaskTimeout                 = 90 * time.Second
	defaultMaxConcurrentSubtasks   = 6
	maxStepTransientRetries        = 2
	maxReactiveOverflowCompactions = 2
)

var defaultStepRetryDelays = []time.Duration{
	250 * time.Millisecond,
	750 * time.Millisecond,
}

// DriverFactory creates a ModelDriver for a given StreamProtocol.
type DriverFactory interface {
	Create(proto llm.StreamProtocol) (ModelDriver, error)
}

// ToolRegistry exposes mode-filtered tool specs and executes tool calls.
type ToolRegistry interface {
	SpecsForMode(mode string) []ToolSpec
	Get(name string) (Tool, bool)
	Execute(ctx context.Context, tc ToolContext, call ToolCall, permHook PermissionHook) (ToolOutput, error)
}

type turnScopedRegistry interface {
	SpecsForTurn(mode string, subtaskDepth int) []ToolSpec
}

// CompactFunc prunes or compacts messages before an upstream LLM call.
type CompactFunc func(msgs []Message, system string, contextLimit int) []Message

// TurnRequest configures a single agentic turn (potentially multi-step tool loop).
type TurnRequest struct {
	SessionID      string
	WorktreeDir    string
	Mode           string
	ProviderID     string
	ModelID        string
	Effort         string
	System         string
	History        []Message
	UserPrompt     string
	UserBlocks     []ContentBlock
	MaxSteps       int
	SubtaskDepth   int
	Steer          SteerSource
	PermissionHook PermissionHook
	CommandRunner  CommandRunner
	Diagnostics    DiagnosticHook
	CodeSearch     CodeSearchHook
	SymbolInspect  SymbolInspectHook
	ImpactInspect  ImpactInspectHook
	AskQuestion    QuestionAsker
	MCPConfig      MCPConfigSource
	HostInvoke     HostInvokeHook
	Sink           EventSink
}

// EventSink receives streaming engine events.
type EventSink interface {
	Emit(evt EngineEvent)
}

// EventSinkFunc adapts a function into an EventSink.
type EventSinkFunc func(evt EngineEvent)

// Emit implements EventSink.
func (f EventSinkFunc) Emit(evt EngineEvent) { f(evt) }

// Engine runs an in-process agentic loop using injected factories and hooks.
type Engine struct {
	stream     llm.StreamClient
	drivers    DriverFactory
	registry   ToolRegistry
	compact    CompactFunc
	retryDelay []time.Duration
	subtaskMu  sync.RWMutex
	subtaskSem chan struct{}
}

// NewEngine constructs an Engine with explicit dependencies.
func NewEngine(stream llm.StreamClient, drivers DriverFactory, registry ToolRegistry, compact CompactFunc) *Engine {
	return &Engine{
		stream:     stream,
		drivers:    drivers,
		registry:   registry,
		compact:    compact,
		retryDelay: defaultStepRetryDelays,
		subtaskSem: make(chan struct{}, defaultMaxConcurrentSubtasks),
	}
}

// SetStepRetryDelays overrides step-level retry backoff durations (used by tests).
func (e *Engine) SetStepRetryDelays(delays []time.Duration) {
	if e != nil {
		e.retryDelay = delays
	}
}

// SetMaxConcurrentSubtasks configures the maximum number of parallel in-process subagents.
func (e *Engine) SetMaxConcurrentSubtasks(limit int) {
	if e == nil || limit <= 0 {
		return
	}
	e.subtaskMu.Lock()
	e.subtaskSem = make(chan struct{}, limit)
	e.subtaskMu.Unlock()
}

func (e *Engine) acquireSubtaskSlot(ctx context.Context) (func(), error) {
	if e == nil {
		return func() {}, nil
	}
	e.subtaskMu.RLock()
	sem := e.subtaskSem
	e.subtaskMu.RUnlock()
	if sem == nil {
		return func() {}, nil
	}
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (e *Engine) waitStepRetry(ctx context.Context, attempt int) bool {
	if ctx.Err() != nil {
		return false
	}
	delays := e.retryDelay
	if len(delays) == 0 {
		return ctx.Err() == nil
	}
	idx := attempt - 1
	if idx >= len(delays) {
		idx = len(delays) - 1
	}
	d := delays[idx]
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return ctx.Err() == nil
	}
}

func isRetryableStepFailure(err error) bool {
	if err == nil {
		return false
	}
	if IsIncompleteStreamError(err) {
		return true
	}
	return llm.IsTransientTurnError(err.Error())
}

// RunTurn executes the multi-step LLM + parallel-read tool loop until completion or cancellation.
func (e *Engine) RunTurn(ctx context.Context, req TurnRequest) error {
	if e.stream == nil {
		return fmt.Errorf("native stream client is not configured")
	}
	var cfgDoc map[string]any
	if req.MCPConfig != nil {
		cfgDoc = req.MCPConfig()
	}
	_, agentMap := llm.LoadAgents(req.WorktreeDir, cfgDoc)
	origMode := strings.TrimSpace(req.Mode)
	if origMode == "" {
		origMode = "build"
	}
	effectiveMode := origMode
	callerSystem := req.System
	if req.SubtaskDepth == 0 {
		if spec, ok := agentMap[strings.ToLower(origMode)]; ok {
			if req.MaxSteps <= 0 && spec.Steps > 0 {
				req.MaxSteps = spec.Steps
			}
			if strings.TrimSpace(req.ModelID) == "" && strings.TrimSpace(spec.Model) != "" {
				if p, m := llm.SplitModelRef(spec.Model); m != "" {
					if p != "" {
						req.ProviderID = p
					}
					req.ModelID = m
				}
			}
			if p := strings.TrimSpace(spec.Prompt); p != "" && !strings.Contains(callerSystem, p) {
				if strings.TrimSpace(callerSystem) == "" {
					callerSystem = p
				} else {
					callerSystem = p + "\n\n" + callerSystem
				}
			}
			if spec.ReadOnly {
				effectiveMode = "plan"
			}
		} else if strings.EqualFold(origMode, "ask") {
			effectiveMode = "plan"
		}
	}
	target := e.stream.ResolveStreamTarget(req.ProviderID, req.ModelID)
	if !target.HasEndpoint {
		return fmt.Errorf("no HTTP endpoint configured for %s/%s", req.ProviderID, req.ModelID)
	}
	drv, err := e.drivers.Create(target.Protocol)
	if err != nil {
		return err
	}
	execReq := req
	execReq.Mode = effectiveMode

	maxSteps, autoScaledSteps := resolveEffectiveMaxSteps(req.MaxSteps, effectiveMode, req.SubtaskDepth, cfgDoc)
	systemPrompt := buildSystemPrompt(req.WorktreeDir, callerSystem, drv.SystemPromptPatch(), effectiveMode, req.SubtaskDepth)
	var toolSpecs []ToolSpec
	if ts, ok := e.registry.(turnScopedRegistry); ok {
		toolSpecs = ts.SpecsForTurn(effectiveMode, req.SubtaskDepth)
	} else {
		toolSpecs = e.registry.SpecsForMode(effectiveMode)
	}
	compactLimit := effectiveCompactContextLimit(target.ContextLimit, toolSpecs)
	loopGuard := newStepLoopGuard()

	historyLen := len(req.History)
	msgs := make([]Message, 0, historyLen+2)
	msgs = append(msgs, req.History...)
	userContent := req.UserBlocks
	if len(userContent) == 0 {
		userContent = []ContentBlock{{Type: ContentText, Text: req.UserPrompt}}
	}
	msgs = append(msgs, Message{
		Role:    RoleUser,
		Content: userContent,
	})

	var emitMu sync.Mutex
	emit := func(evt EngineEvent) {
		if req.Sink != nil {
			emitMu.Lock()
			req.Sink.Emit(evt)
			emitMu.Unlock()
		}
	}

	partSeq := 0
	nextPartID := func(prefix string) string {
		emitMu.Lock()
		partSeq++
		id := fmt.Sprintf("%s_%d", prefix, partSeq)
		emitMu.Unlock()
		return id
	}

	overflowCompactions := 0
	truncationRecoveries := 0
	for step := 1; step <= maxSteps; step++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if req.Steer != nil {
			if steers := req.Steer.Drain(); len(steers) > 0 {
				for _, s := range steers {
					if strings.TrimSpace(s) != "" {
						msgs = append(msgs, Message{
							Role:    RoleUser,
							Content: []ContentBlock{{Type: ContentText, Text: s}},
						})
					}
				}
			}
		}
		if e.compact != nil {
			msgs = e.compact(msgs, systemPrompt, compactLimit)
		}

		maxOut := target.OutputLimit
		if maxOut <= 0 {
			maxOut = 8192
		}
		buildStepReq := func() (llm.StreamHTTPRequest, error) {
			return drv.BuildRequest(ModelRequest{
				Target:    target,
				System:    systemPrompt,
				Messages:  msgs,
				Tools:     toolSpecs,
				MaxTokens: maxOut,
				Effort:    req.Effort,
			})
		}
		httpReq, err := buildStepReq()
		if err != nil {
			return err
		}

		var textBuf, thinkBuf, sigBuf strings.Builder
		var toolCalls []ToolCall
		var stepFinish string
		var stepErr error

		for attempt := 0; attempt <= maxStepTransientRetries; attempt++ {
			if attempt > 0 && !e.waitStepRetry(ctx, attempt) {
				return ctx.Err()
			}
			resp, openErr := e.stream.OpenStream(ctx, httpReq)
			if openErr != nil {
				if overflowCompactions < maxReactiveOverflowCompactions && ctx.Err() == nil && isContextOverflowError(openErr) {
					if compacted, cpInfo, ok := e.compactContextOverflow(ctx, req.ProviderID, req.ModelID, msgs, historyLen, step); ok {
						overflowCompactions++
						historyLen = 0
						loopGuard.ResetOnCompaction()
						if autoScaledSteps {
							maxSteps += compactionRecoveryStepBonus(step)
						}
						if req.SubtaskDepth == 0 && cpInfo != nil && cpInfo.Summary != "" {
							emit(EngineEvent{
								Kind: EngineContextCompacted,
								Step: step,
								Text: cpInfo.Summary,
								Metadata: map[string]any{
									"scope":        cpInfo.Scope,
									"keepFromStep": cpInfo.KeepFromStep,
								},
							})
						}
						msgs = compacted
						httpReq, err = buildStepReq()
						if err != nil {
							return err
						}
						attempt = -1
						continue
					}
				}
				stepErr = openErr
				if attempt < maxStepTransientRetries && ctx.Err() == nil && isRetryableStepFailure(openErr) {
					continue
				}
				return openErr
			}

			var textPartID, thinkPartID string
			emittedVisible := false
			textBuf.Reset()
			thinkBuf.Reset()
			sigBuf.Reset()
			stepFinish = ""
			toolCalls = toolCalls[:0]

			parseErr := drv.ParseSSE(ctx, resp.Body, func(se StreamEvent) {
				switch se.Kind {
				case EventThinkingDelta:
					emittedVisible = true
					if thinkPartID == "" {
						thinkPartID = nextPartID("think")
						emit(EngineEvent{Kind: EngineThinkingStart, Step: step, PartID: thinkPartID})
					}
					thinkBuf.WriteString(se.Delta)
					emit(EngineEvent{Kind: EngineThinkingDelta, Step: step, PartID: thinkPartID, Text: se.Delta})
				case EventSignatureDelta:
					sigBuf.WriteString(se.Delta)
				case EventTextDelta:
					emittedVisible = true
					if thinkPartID != "" {
						emit(EngineEvent{Kind: EngineThinkingEnd, Step: step, PartID: thinkPartID, Text: thinkBuf.String(), Signature: sigBuf.String()})
						thinkPartID = ""
					}
					if textPartID == "" {
						textPartID = nextPartID("text")
						emit(EngineEvent{Kind: EngineTextStart, Step: step, PartID: textPartID})
					}
					textBuf.WriteString(se.Delta)
					emit(EngineEvent{Kind: EngineTextDelta, Step: step, PartID: textPartID, Text: se.Delta})
				case EventToolCallReady:
					if se.ToolCall != nil {
						toolCalls = append(toolCalls, *se.ToolCall)
					}
				case EventUsage:
					if se.Usage != nil {
						emit(EngineEvent{Kind: EngineTurnUsage, Step: step, Usage: se.Usage})
					}
				case EventFinish:
					if se.Finish != "" {
						stepFinish = se.Finish
					}
				}
			})
			_ = resp.Body.Close()

			if thinkPartID != "" {
				emit(EngineEvent{Kind: EngineThinkingEnd, Step: step, PartID: thinkPartID, Text: thinkBuf.String(), Signature: sigBuf.String()})
			} else if sigBuf.Len() > 0 && thinkBuf.Len() > 0 {
				// Update sealed thinking block with trailing signature delta if signature arrived after text started.
				emit(EngineEvent{Kind: EngineThinkingEnd, Step: step, PartID: nextPartID("think_sig"), Text: thinkBuf.String(), Signature: sigBuf.String()})
			}
			if textPartID != "" {
				emit(EngineEvent{Kind: EngineTextEnd, Step: step, PartID: textPartID, Text: textBuf.String()})
			}
			if parseErr == nil {
				stepErr = nil
				break
			}
			if !emittedVisible && overflowCompactions < maxReactiveOverflowCompactions && ctx.Err() == nil && isContextOverflowError(parseErr) {
				if compacted, cpInfo, ok := e.compactContextOverflow(ctx, req.ProviderID, req.ModelID, msgs, historyLen, step); ok {
					overflowCompactions++
					historyLen = 0
					loopGuard.ResetOnCompaction()
					if autoScaledSteps {
						maxSteps += compactionRecoveryStepBonus(step)
					}
					if req.SubtaskDepth == 0 && cpInfo != nil && cpInfo.Summary != "" {
						emit(EngineEvent{
							Kind: EngineContextCompacted,
							Step: step,
							Text: cpInfo.Summary,
							Metadata: map[string]any{
								"scope":        cpInfo.Scope,
								"keepFromStep": cpInfo.KeepFromStep,
							},
						})
					}
					msgs = compacted
					httpReq, err = buildStepReq()
					if err != nil {
						return err
					}
					attempt = -1
					continue
				}
			}
			stepErr = parseErr
			// Safe automatic retry boundary: only retry when no visible draft was
			// streamed to consumers yet; otherwise seal visible draft and surface
			// the interrupted stream error cleanly without duplicating output.
			if !emittedVisible && attempt < maxStepTransientRetries && ctx.Err() == nil && isRetryableStepFailure(parseErr) {
				continue
			}
			return parseErr
		}
		if stepErr != nil {
			return stepErr
		}

		var asstBlocks []ContentBlock
		if thinkBuf.Len() > 0 || sigBuf.Len() > 0 {
			asstBlocks = append(asstBlocks, ContentBlock{
				Type:      ContentThinking,
				Text:      thinkBuf.String(),
				Signature: sigBuf.String(),
			})
		}
		if textBuf.Len() > 0 {
			asstBlocks = append(asstBlocks, ContentBlock{
				Type: ContentText,
				Text: textBuf.String(),
			})
		}
		for _, tc := range toolCalls {
			asstBlocks = append(asstBlocks, ContentBlock{
				Type:      ContentToolUse,
				ID:        tc.ID,
				Name:      tc.Name,
				Input:     tc.Input,
				Signature: tc.Signature,
			})
		}
		if len(asstBlocks) > 0 {
			msgs = append(msgs, Message{Role: RoleAssistant, Content: asstBlocks})
		}

		if len(toolCalls) == 0 {
			if isTruncatedFinishReason(stepFinish) {
				if textBuf.Len() == 0 && truncationRecoveries == 0 && step < maxSteps {
					truncationRecoveries++
					if len(asstBlocks) == 0 {
						msgs = append(msgs, Message{
							Role:    RoleAssistant,
							Content: []ContentBlock{{Type: ContentText, Text: "[response truncated at output token limit before completion]"}},
						})
					}
					msgs = append(msgs, Message{
						Role: RoleUser,
						Content: []ContentBlock{{
							Type: ContentText,
							Text: "[System notice: your previous response hit the output token limit before completing a tool call or visible output. Break large file writes or patches into smaller incremental steps or write a concise reply, then continue.]",
						}},
					})
					continue
				}
				if req.SubtaskDepth > 0 && textBuf.Len() == 0 {
					return fmt.Errorf("subagent output truncated at token limit (%s)", stepFinish)
				}
				emit(EngineEvent{Kind: EngineTurnFinish, Step: step, Finish: "length"})
				return nil
			}
			if strings.EqualFold(stepFinish, "content_filter") {
				if textBuf.Len() == 0 {
					return fmt.Errorf("model output blocked by content filter")
				}
				emit(EngineEvent{Kind: EngineTurnFinish, Step: step, Finish: "content_filter"})
				return nil
			}
			emit(EngineEvent{Kind: EngineTurnFinish, Step: step, Finish: "stop"})
			return nil
		}

		results := e.executeToolCalls(ctx, execReq, origMode, agentMap, cfgDoc, step, toolCalls, loopGuard, nextPartID, emit)
		resultBlocks := make([]ContentBlock, 0, len(results))
		for i, res := range results {
			resultBlocks = append(resultBlocks, ContentBlock{
				Type:      ContentToolResult,
				ToolUseID: toolCalls[i].ID,
				Text:      res.Output,
				IsError:   res.IsError,
			})
			for _, mb := range res.Media {
				if mb.Type == ContentMedia && mb.Data != "" && mb.MimeType != "" {
					resultBlocks = append(resultBlocks, mb)
				}
			}
		}
		msgs = append(msgs, Message{Role: RoleUser, Content: resultBlocks})
		if step == maxSteps {
			if e.runGracefulFinalizeStep(ctx, drv, target, req, systemPrompt, msgs, maxOut, maxSteps, nextPartID, emit) {
				return nil
			}
		}
	}
	return fmt.Errorf("reached maximum step limit (%d)", maxSteps)
}

func (e *Engine) executeToolCalls(
	ctx context.Context,
	req TurnRequest,
	origMode string,
	agentMap map[string]llm.AgentSpec,
	cfgDoc map[string]any,
	step int,
	calls []ToolCall,
	loopGuard *stepLoopGuard,
	nextPartID func(string) string,
	emit func(EngineEvent),
) []ToolOutput {
	results := make([]ToolOutput, len(calls))
	partIDs := make([]string, len(calls))
	tc := ToolContext{
		SessionID:     req.SessionID,
		WorktreeDir:   req.WorktreeDir,
		Mode:          req.Mode,
		SubtaskDepth:  req.SubtaskDepth,
		RunCommand:    req.CommandRunner,
		Diagnostics:   req.Diagnostics,
		CodeSearch:    req.CodeSearch,
		SymbolInspect: req.SymbolInspect,
		ImpactInspect: req.ImpactInspect,
		AskQuestion:   req.AskQuestion,
		MCPConfig:     req.MCPConfig,
		HostInvoke:    req.HostInvoke,
	}
	if req.SubtaskDepth == 0 {
		parentReq := req
		parentReq.Mode = origMode
		tc.RunSubtask = func(subCtx context.Context, subReq SubtaskRequest) (SubtaskResult, error) {
			return e.runSubtask(subCtx, parentReq, req.Mode, agentMap, cfgDoc, subReq, emit)
		}
	}

	for i, c := range calls {
		partIDs[i] = nextPartID("tool")
		var inputMap map[string]any
		_ = json.Unmarshal(c.Input, &inputMap)
		var meta map[string]any
		if c.Signature != "" {
			meta = map[string]any{"thoughtSignature": c.Signature}
		}
		emit(EngineEvent{
			Kind:     EngineToolPending,
			Step:     step,
			PartID:   partIDs[i],
			CallID:   c.ID,
			ToolName: c.Name,
			Input:    inputMap,
			Metadata: meta,
		})
	}

	// Partition consecutive parallel-safe calls for concurrent fan-out; run mutating tools/subagents serially.
	i := 0
	for i < len(calls) {
		if ctx.Err() != nil {
			for k := i; k < len(calls); k++ {
				results[k] = ToolOutput{Title: calls[k].Name, Output: ctx.Err().Error(), IsError: true}
			}
			break
		}
		if e.isParallelSafeCall(calls[i], req.Mode, agentMap) {
			j := i
			for j < len(calls) && e.isParallelSafeCall(calls[j], req.Mode, agentMap) {
				j++
			}
			var wg sync.WaitGroup
			for k := i; k < j; k++ {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					results[idx] = e.runSingleTool(ctx, tc, step, calls[idx], partIDs[idx], loopGuard, req.PermissionHook, emit)
				}(k)
			}
			wg.Wait()
			i = j
			continue
		}
		results[i] = e.runSingleTool(ctx, tc, step, calls[i], partIDs[i], loopGuard, req.PermissionHook, emit)
		i++
	}
	loopGuard.ObserveStepCalls(calls, results, e.isReadOnlyCall, origMode, agentMap)
	return results
}

func (e *Engine) isReadOnlyCall(name string) bool {
	t, ok := e.registry.Get(name)
	return ok && t.IsReadOnly()
}

func (e *Engine) runSingleTool(
	ctx context.Context,
	tc ToolContext,
	step int,
	call ToolCall,
	partID string,
	loopGuard *stepLoopGuard,
	permHook PermissionHook,
	emit func(EngineEvent),
) ToolOutput {
	callTC := tc
	callTC.CallID = call.ID
	var inputMap map[string]any
	_ = json.Unmarshal(call.Input, &inputMap)
	var baseMeta map[string]any
	if call.Signature != "" {
		baseMeta = map[string]any{"thoughtSignature": call.Signature}
	}
	emit(EngineEvent{
		Kind:     EngineToolRunning,
		Step:     step,
		PartID:   partID,
		CallID:   call.ID,
		ToolName: call.Name,
		Input:    inputMap,
		Metadata: baseMeta,
	})
	if blocked, blockMsg := loopGuard.CheckBeforeExecute(call); blocked {
		emit(EngineEvent{
			Kind:      EngineToolError,
			Step:      step,
			PartID:    partID,
			CallID:    call.ID,
			ToolName:  call.Name,
			Title:     call.Name,
			Input:     inputMap,
			ErrorText: blockMsg,
			Metadata:  baseMeta,
		})
		return ToolOutput{Title: call.Name, Output: blockMsg, IsError: true}
	}
	out, err := e.registry.Execute(ctx, callTC, call, permHook)
	mergedMeta := out.Metadata
	if call.Signature != "" {
		mergedMeta = make(map[string]any, len(out.Metadata)+1)
		for k, v := range out.Metadata {
			mergedMeta[k] = v
		}
		mergedMeta["thoughtSignature"] = call.Signature
	}
	if err != nil || out.IsError {
		errText := out.Output
		if errText == "" && err != nil {
			errText = err.Error()
		}
		emit(EngineEvent{
			Kind:      EngineToolError,
			Step:      step,
			PartID:    partID,
			CallID:    call.ID,
			ToolName:  call.Name,
			Title:     out.Title,
			Input:     inputMap,
			ErrorText: errText,
			Metadata:  mergedMeta,
		})
		return out
	}
	emit(EngineEvent{
		Kind:     EngineToolCompleted,
		Step:     step,
		PartID:   partID,
		CallID:   call.ID,
		ToolName: call.Name,
		Title:    out.Title,
		Input:    inputMap,
		Text:     out.Output,
		Metadata: mergedMeta,
		Media:    out.Media,
	})
	return out
}

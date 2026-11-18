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
	SessionID           string
	WorktreeDir         string
	Mode                string
	ProviderID          string
	ModelID             string
	Effort              string
	System              string
	History             []Message
	UserPrompt          string
	UserBlocks          []ContentBlock
	MaxSteps            int
	SubtaskDepth        int
	FileRevisions       *FileRevisionTracker
	CoordinateFileEdit  FileEditCoordinatorHook
	RecordFileEdit      FileEditRecordedHook
	Steer               SteerSource
	PermissionHook      PermissionHook
	CommandRunner       CommandRunner
	StreamCommandRunner StreamingCommandRunner
	BackgroundCommands  BackgroundCommandManager
	Diagnostics         DiagnosticHook
	CodeSearch          CodeSearchHook
	SymbolInspect       SymbolInspectHook
	ImpactInspect       ImpactInspectHook
	AskQuestion         QuestionAsker
	MCPConfig           MCPConfigSource
	projectConfig       map[string]any
	HostInvoke          HostInvokeHook
	Sink                EventSink
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
	stream         llm.StreamClient
	drivers        DriverFactory
	registry       ToolRegistry
	compact        CompactFunc
	defaultCompact bool
	retryDelay     []time.Duration
	retryBudget    time.Duration
	customRetry    bool
	subtaskMu      sync.RWMutex
	subtaskSem     chan struct{}
}

func (e *Engine) applyCompact(msgs []Message, system, worktreeDir string, contextLimit int) []Message {
	if e == nil {
		return msgs
	}
	if e.defaultCompact {
		return pruneOldToolResultsInWorkspace(msgs, system, worktreeDir, contextLimit)
	}
	if e.compact != nil {
		return e.compact(msgs, system, contextLimit)
	}
	return msgs
}

// NewEngine constructs an Engine with explicit dependencies.
func NewEngine(stream llm.StreamClient, drivers DriverFactory, registry ToolRegistry, compact CompactFunc) *Engine {
	return &Engine{
		stream:      stream,
		drivers:     drivers,
		registry:    registry,
		compact:     compact,
		retryDelay:  defaultStepRetryDelays,
		retryBudget: defaultMaxTurnRetryBudget,
		subtaskSem:  make(chan struct{}, defaultMaxConcurrentSubtasks),
	}
}

// SetStepRetryDelays overrides step-level retry backoff durations (used by tests).
func (e *Engine) SetStepRetryDelays(delays []time.Duration) {
	if e != nil {
		e.retryDelay = delays
		e.customRetry = true
	}
}

// SetMaxTurnRetryBudget overrides the cumulative retry wait ceiling for a turn.
func (e *Engine) SetMaxTurnRetryBudget(budget time.Duration) {
	if e != nil && budget > 0 {
		e.retryBudget = budget
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

// RunTurn executes the multi-step LLM + parallel-read tool loop until completion or cancellation.
func (e *Engine) RunTurn(ctx context.Context, req TurnRequest) error {
	if e.stream == nil {
		return fmt.Errorf("native stream client is not configured")
	}
	cfgDoc, err := prepareProjectTurnConfig(&req)
	if err != nil {
		return err
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
	if execReq.FileRevisions == nil {
		execReq.FileRevisions = NewFileRevisionTracker()
	}

	maxSteps, autoScaledSteps := resolveEffectiveMaxSteps(req.MaxSteps, effectiveMode, req.SubtaskDepth, cfgDoc)
	systemPrompt, rulesLoad := buildSystemPromptWithRules(req.WorktreeDir, callerSystem, drv.SystemPromptPatch(), effectiveMode, req.SubtaskDepth, cfgDoc)
	rulesTracker := NewSubdirRuleTracker(rulesLoad.LoadedFiles, DefaultSubdirRuleBudgetBytes)
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

	if req.SubtaskDepth == 0 && rulesLoad.OverBudget {
		emit(EngineEvent{
			Kind: EngineRulesBudget,
			Step: 1,
			Text: fmt.Sprintf("Project rules exceeded context budget (%d / %d bytes); truncated: %s", rulesLoad.TotalBytes, rulesLoad.BudgetBytes, strings.Join(rulesLoad.TruncatedFiles, ", ")),
			Metadata: map[string]any{
				"budgetBytes":    rulesLoad.BudgetBytes,
				"totalBytes":     rulesLoad.TotalBytes,
				"truncatedFiles": rulesLoad.TruncatedFiles,
			},
		})
	}
	overflowCompactions := 0
	truncationRecoveries := 0
	foldUnsignedToolSteps := false
	effectiveEffort := req.Effort
	var totalRetryWait time.Duration
	for step := 1; step <= maxSteps; step++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if steered := drainSteerMessages(req.Steer); len(steered) > 0 {
			msgs = append(msgs, steered...)
		}
		if foldUnsignedToolSteps && !tailToolExchangeHasSignedThinking(msgs) {
			msgs, _ = recoverMessagesFromThinkingSignatureError(msgs)
		}
		msgs = e.applyCompact(msgs, systemPrompt, req.WorktreeDir, compactLimit)

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
				Effort:    effectiveEffort,
			})
		}
		httpReq, err := buildStepReq()
		if err != nil {
			return err
		}

		var streamed stepStreamResult
		var stepErr error
		stepThinkingRecovered := false

		tryRecoverThinkingSignature := func(cause error) bool {
			if stepThinkingRecovered || ctx.Err() != nil || !isRecoverableThinkingSignatureError(cause) {
				return false
			}
			recovered, changed := recoverMessagesFromThinkingSignatureError(msgs)
			needDisable := messagesRequireThinkingDisabled(recovered)
			if !changed && (!needDisable || effectiveEffort == "") {
				return false
			}
			stepThinkingRecovered = true
			foldUnsignedToolSteps = true
			msgs = recovered
			if needDisable {
				effectiveEffort = ""
			}
			emit(EngineEvent{
				Kind:      EngineThinkingSignatureReset,
				Step:      step,
				ErrorText: summarizeRetryReason(cause),
			})
			return true
		}

		for attempt := 0; ; attempt++ {
			openStarted := time.Now()
			resp, openErr := e.stream.OpenStream(ctx, httpReq)
			headerLatency := time.Since(openStarted)
			if openErr != nil {
				if target.FallbackOpenAI && ctx.Err() == nil && isUnsupportedAnthropicMessagesEndpoint(openErr) {
					target.Protocol = llm.ProtocolOpenAI
					target.FallbackOpenAI = false
					drv, err = e.drivers.Create(target.Protocol)
					if err != nil {
						return err
					}
					httpReq, err = buildStepReq()
					if err != nil {
						return err
					}
					attempt = -1
					continue
				}
				if tryRecoverThinkingSignature(openErr) {
					httpReq, err = buildStepReq()
					if err != nil {
						return err
					}
					attempt = -1
					continue
				}
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
				if ctx.Err() == nil {
					if dec := e.planStepRetry(openErr, attempt+1, totalRetryWait, time.Now()); dec.shouldRetry {
						if !e.waitForStepRetry(ctx, step, req.SubtaskDepth, attempt+1, dec, emit) {
							return ctx.Err()
						}
						totalRetryWait += dec.delay
						continue
					}
				}
				return openErr
			}

			var parseErr error
			streamed, parseErr = collectStepStream(ctx, drv, resp.Body, target, step, nextPartID, emit)
			_ = resp.Body.Close()
			if parseErr == nil {
				e.reportStreamOutcome(ctx, target, headerLatency, nil)
				stepErr = nil
				break
			}
			e.reportStreamOutcome(ctx, target, time.Since(openStarted), parseErr)
			if !streamed.emittedVisible && tryRecoverThinkingSignature(parseErr) {
				httpReq, err = buildStepReq()
				if err != nil {
					return err
				}
				attempt = -1
				continue
			}
			if !streamed.emittedVisible && overflowCompactions < maxReactiveOverflowCompactions && ctx.Err() == nil && isContextOverflowError(parseErr) {
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
			if !streamed.emittedVisible && ctx.Err() == nil {
				if dec := e.planStepRetry(parseErr, attempt+1, totalRetryWait, time.Now()); dec.shouldRetry {
					if !e.waitForStepRetry(ctx, step, req.SubtaskDepth, attempt+1, dec, emit) {
						return ctx.Err()
					}
					totalRetryWait += dec.delay
					continue
				}
			}
			return parseErr
		}
		if stepErr != nil {
			return stepErr
		}

		if len(streamed.asstBlocks) > 0 {
			msgs = append(msgs, Message{Role: RoleAssistant, Content: streamed.asstBlocks})
		}

		if len(streamed.toolCalls) == 0 {
			if isTruncatedFinishReason(streamed.stepFinish) {
				if streamed.visibleTextLen == 0 && truncationRecoveries == 0 && step < maxSteps {
					truncationRecoveries++
					if len(streamed.asstBlocks) == 0 {
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
				if req.SubtaskDepth > 0 && streamed.visibleTextLen == 0 {
					return fmt.Errorf("subagent output truncated at token limit (%s)", streamed.stepFinish)
				}
				emit(EngineEvent{Kind: EngineTurnFinish, Step: step, Finish: "length"})
				return nil
			}
			if strings.EqualFold(streamed.stepFinish, "content_filter") {
				if streamed.visibleTextLen == 0 {
					return fmt.Errorf("model output blocked by content filter")
				}
				emit(EngineEvent{Kind: EngineTurnFinish, Step: step, Finish: "content_filter"})
				return nil
			}
			if step < maxSteps {
				if steered := drainSteerMessages(req.Steer); len(steered) > 0 {
					msgs = append(msgs, steered...)
					continue
				}
			}
			emit(EngineEvent{Kind: EngineTurnFinish, Step: step, Finish: "stop"})
			return nil
		}

		results := e.executeToolCalls(ctx, execReq, origMode, agentMap, cfgDoc, rulesTracker, step, streamed.toolCalls, loopGuard, nextPartID, emit)
		resultBlocks := make([]ContentBlock, 0, len(results))
		promoteToBuild := false
		for i, res := range results {
			resultBlocks = append(resultBlocks, ContentBlock{
				Type:      ContentToolResult,
				ToolUseID: streamed.toolCalls[i].ID,
				Text:      res.Output,
				IsError:   res.IsError,
			})
			if !res.IsError && req.SubtaskDepth == 0 && strings.EqualFold(effectiveMode, "plan") {
				if pm, _ := res.Metadata["promoteMode"].(string); strings.EqualFold(strings.TrimSpace(pm), "build") {
					promoteToBuild = true
				}
			}
		}
		for _, res := range results {
			for _, mb := range res.Media {
				if mb.Type == ContentMedia && mb.Data != "" && mb.MimeType != "" {
					resultBlocks = append(resultBlocks, mb)
				}
			}
		}
		msgs = append(msgs, Message{Role: RoleUser, Content: resultBlocks})
		if promoteToBuild {
			prevMode := effectiveMode
			effectiveMode = "build"
			origMode = "build"
			execReq.Mode = "build"
			if spec, ok := agentMap["plan"]; ok && strings.TrimSpace(spec.Prompt) != "" {
				callerSystem = strings.TrimSpace(strings.Replace(callerSystem, strings.TrimSpace(spec.Prompt), "", 1))
			}
			systemPrompt, _ = buildSystemPromptWithRules(req.WorktreeDir, callerSystem, drv.SystemPromptPatch(), effectiveMode, req.SubtaskDepth, cfgDoc)
			if ts, ok := e.registry.(turnScopedRegistry); ok {
				toolSpecs = ts.SpecsForTurn(effectiveMode, req.SubtaskDepth)
			} else {
				toolSpecs = e.registry.SpecsForMode(effectiveMode)
			}
			compactLimit = effectiveCompactContextLimit(target.ContextLimit, toolSpecs)
			buildMaxSteps, _ := resolveEffectiveMaxSteps(req.MaxSteps, "build", req.SubtaskDepth, cfgDoc)
			if step+buildMaxSteps > maxSteps {
				maxSteps = step + buildMaxSteps
			}
			emit(EngineEvent{
				Kind: EngineModePromoted,
				Step: step,
				Text: "build",
				Metadata: map[string]any{
					"from": prevMode,
					"to":   "build",
				},
			})
		}
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
	rulesTracker *SubdirRuleTracker,
	step int,
	calls []ToolCall,
	loopGuard *stepLoopGuard,
	nextPartID func(string) string,
	emit func(EngineEvent),
) []ToolOutput {
	results := make([]ToolOutput, len(calls))
	partIDs := make([]string, len(calls))
	tc := ToolContext{
		SessionID:          req.SessionID,
		WorktreeDir:        req.WorktreeDir,
		Mode:               req.Mode,
		SubtaskDepth:       req.SubtaskDepth,
		RulesTracker:       rulesTracker,
		FileRevisions:      req.FileRevisions,
		CoordinateFileEdit: req.CoordinateFileEdit,
		RecordFileEdit:     req.RecordFileEdit,
		RunCommand:         req.CommandRunner,
		StreamRunCommand:   req.StreamCommandRunner,
		BackgroundCommands: req.BackgroundCommands,
		Diagnostics:        req.Diagnostics,
		CodeSearch:         req.CodeSearch,
		SymbolInspect:      req.SymbolInspect,
		ImpactInspect:      req.ImpactInspect,
		AskQuestion:        req.AskQuestion,
		MCPConfig:          req.MCPConfig,
		projectConfig:      req.projectConfig,
		HostInvoke:         req.HostInvoke,
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
	callTC.OnToolOutput = func(partial string) {
		if strings.TrimSpace(partial) == "" {
			return
		}
		meta := make(map[string]any, len(baseMeta)+1)
		for k, v := range baseMeta {
			meta[k] = v
		}
		meta["output"] = stripTerminalControlNoise(partial)
		emit(EngineEvent{
			Kind:     EngineToolRunning,
			Step:     step,
			PartID:   partID,
			CallID:   call.ID,
			ToolName: call.Name,
			Input:    inputMap,
			Metadata: meta,
		})
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
			Media:     out.Media,
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

package agent

import (
	"context"
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
	// A failed proactive attempt already paid for an in-place summary and a
	// chunked fallback; later steps rely on reactive overflow compaction.
	maxProactiveCompactionFailures = 1
)

var defaultStepRetryDelays = []time.Duration{
	250 * time.Millisecond,
	750 * time.Millisecond,
}

// TurnRequest configures a single agentic turn (potentially multi-step tool loop).
type TurnRequest struct {
	commandLogs         *turnCommandLogs
	SessionID           string
	CacheKey            string
	WorktreeDir         string
	Mode                string
	ProviderID          string
	ModelID             string
	Effort              string
	FastMode            bool
	Orchestrator        *OrchestratorConfig
	yieldOnParentSteer  SteerSource
	System              string
	SystemSuffix        string
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
	RepoMap             RepoMapHook
	CodeSearch          CodeSearchHook
	SymbolInspect       SymbolInspectHook
	ImpactInspect       ImpactInspectHook
	AskQuestion         QuestionAsker
	MCPConfig           MCPConfigSource
	projectConfig       map[string]any
	onFinalMessages     func([]Message)
	HostInvoke          HostInvokeHook
	Sink                EventSink
}

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
	nowFn          func() time.Time
	noFastMode     sync.Map
	turnContextMu  sync.Mutex
	turnContextMap map[string]turnContextSnapshot
	subtaskMu      sync.RWMutex
	subtaskSem     chan struct{}
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

// ForgetSession removes cached cross-turn context calibration/prefix snapshots
// associated with sessionID.
func (e *Engine) ForgetSession(sessionID string) {
	sid := strings.TrimSpace(sessionID)
	if e == nil || sid == "" {
		return
	}
	prefix := sid + "\x00"
	e.turnContextMu.Lock()
	for k, v := range e.turnContextMap {
		if strings.HasPrefix(k, prefix) || v.sessionID == sid {
			delete(e.turnContextMap, k)
		}
	}
	e.turnContextMu.Unlock()
}

func (e *Engine) now() time.Time {
	if e != nil && e.nowFn != nil {
		return e.nowFn()
	}
	return time.Now()
}

func (e *Engine) loadTurnContextSnapshot(key string) (turnContextSnapshot, bool) {
	if e == nil || key == "" {
		return turnContextSnapshot{}, false
	}
	e.turnContextMu.Lock()
	snap, ok := e.turnContextMap[key]
	e.turnContextMu.Unlock()
	return snap, ok
}

func (e *Engine) saveTurnContextSnapshot(key string, snap turnContextSnapshot) {
	if e == nil || key == "" {
		return
	}
	e.turnContextMu.Lock()
	defer e.turnContextMu.Unlock()
	if e.turnContextMap == nil {
		e.turnContextMap = make(map[string]turnContextSnapshot)
	}
	if _, exists := e.turnContextMap[key]; !exists && len(e.turnContextMap) >= maxTurnContextCacheEntries {
		var oldestKey string
		var oldestAt time.Time
		first := true
		for k, v := range e.turnContextMap {
			if first || v.lastObservedAt.Before(oldestAt) {
				oldestKey = k
				oldestAt = v.lastObservedAt
				first = false
			}
		}
		if oldestKey != "" {
			delete(e.turnContextMap, oldestKey)
		}
	}
	e.turnContextMap[key] = snap
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
	externalCollab := isExternalCollabSession(callerSystem)
	if externalCollab {
		execReq.AskQuestion = nil
	}
	if execReq.FileRevisions == nil {
		execReq.FileRevisions = NewFileRevisionTracker()
	}
	if execReq.commandLogs == nil {
		execReq.commandLogs = &turnCommandLogs{}
	}

	maxSteps, autoScaledSteps := resolveEffectiveMaxSteps(req.MaxSteps, effectiveMode, req.SubtaskDepth, cfgDoc)
	systemPrompt, rulesLoad := buildSystemPromptWithRules(req.WorktreeDir, callerSystem, drv.SystemPromptPatch(), effectiveMode, req.SubtaskDepth, cfgDoc)
	systemSuffix := strings.TrimSpace(req.SystemSuffix)
	rulesTracker := NewSubdirRuleTracker(rulesLoad.LoadedFiles, DefaultSubdirRuleBudgetBytes)
	rulesTracker.SeedFromHistory(req.WorktreeDir, req.History)
	catalog := newHostToolCatalog(cfgDoc, req.History)
	catalog.configureMCP(req.WorktreeDir, cfgDoc, req.MCPConfig, req.History)
	catalog.configureAppScope(callerSystem)
	catalog.configureTaskPolicy(origMode, agentMap, cfgDoc)
	if isOrchestratorActive(req) {
		catalog.configureOrchestrator(true, effectiveMode)
		systemPrompt = appendSystemPromptSuffix(systemPrompt, orchestratorProtocolGuidance)
	}
	toolSpecs := catalog.specs(e.registry, effectiveMode, req.SubtaskDepth)
	compactLimit := effectiveCompactContextLimit(target.ContextLimit, toolSpecs)
	contextThresholds := resolveTurnContextThresholds(target)
	tokenTracker := newContextTokenTracker()
	tokenTracker.nowFn = e.nowFn
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
	if req.onFinalMessages != nil {
		defer func() { req.onFinalMessages(msgs) }()
	}
	effectiveCacheKey := strings.TrimSpace(req.CacheKey)
	if effectiveCacheKey == "" {
		effectiveCacheKey = strings.TrimSpace(req.SessionID)
	}
	if req.SubtaskDepth == 0 && effectiveCacheKey != "" {
		if snap, ok := e.loadTurnContextSnapshot(makeTurnContextCacheKey(effectiveCacheKey, target)); ok {
			initialFullSystem := appendSystemPromptSuffix(systemPrompt, systemSuffix)
			tokenTracker.SeedFromPriorTurn(snap, e.now(), effectiveCacheKey, initialFullSystem, toolSpecs, msgs)
		}
		defer func() {
			if snap, ok := tokenTracker.Snapshot(); ok {
				snap.sessionID = strings.TrimSpace(req.SessionID)
				e.saveTurnContextSnapshot(makeTurnContextCacheKey(effectiveCacheKey, target), snap)
			}
		}()
	}

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
	proactiveCompactionFailures := 0
	truncationRecoveries := 0
	chantingRecoveries := 0
	foldUnsignedToolSteps := false
	effectiveEffort := req.Effort
	effectiveFast := req.FastMode && req.SubtaskDepth == 0
	if effectiveFast {
		if _, blocked := e.noFastMode.Load(fastModeMemoKey(target)); blocked {
			effectiveFast = false
		}
	}
	var totalRetryWait time.Duration
	for step := 1; step <= maxSteps; step++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if req.SubtaskDepth > 0 && step > 1 && hasPendingParentSteer(req.yieldOnParentSteer) {
			emit(EngineEvent{Kind: EngineTextEnd, Step: step, Text: executorYieldNotice})
			emit(EngineEvent{Kind: EngineTurnFinish, Step: step, Finish: "stop"})
			return nil
		}
		if steered := drainSteerMessages(req.Steer); len(steered) > 0 {
			msgs = append(msgs, steered...)
		}
		if foldUnsignedToolSteps && !tailToolExchangeHasSignedThinking(msgs) {
			var recovered bool
			if msgs, recovered = recoverMessagesFromThinkingSignatureError(msgs); recovered {
				tokenTracker.InvalidateAnchor(true)
			}
		}
		maxOut := target.OutputLimit
		if maxOut <= 0 {
			maxOut = 8192
		}
		fullSystem := appendSystemPromptSuffix(systemPrompt, systemSuffix)
		prePruneMsgs := msgs
		wasCacheWarm := tokenTracker.IsPrefixCacheWarm()
		prePruneTokens := tokenTracker.EstimateTotalTokens(msgs, fullSystem, toolSpecs)
		var prunedChanged bool
		msgs, prunedChanged = e.applyCompactWithBudget(msgs, fullSystem, req.WorktreeDir, toolSpecs, compactLimit, contextThresholds, tokenTracker)
		if prunedChanged {
			tokenTracker.RebaseAfterPrune(prePruneMsgs, msgs, fullSystem, toolSpecs)
		}
		if overflowCompactions < maxReactiveOverflowCompactions && proactiveCompactionFailures < maxProactiveCompactionFailures &&
			shouldProactivelyCompactWithBudget(msgs, fullSystem, toolSpecs, contextThresholds, tokenTracker, target.ContextLimit, historyLen, step) {
			inPlaceMsgs := msgs
			inPlaceCacheWarm := tokenTracker.IsPrefixCacheWarm()
			inPlaceTokens := tokenTracker.EstimateTotalTokens(msgs, fullSystem, toolSpecs)
			// If an emergency tight-prune just mutated the prefix but failed to keep
			// context under hardCap, prefer the unmodified warm prefix for in-place
			// compaction so the summary call still hits the upstream prompt cache.
			if prunedChanged && wasCacheWarm && prePruneTokens+minInPlaceCompactionHeadroom < target.ContextLimit {
				inPlaceMsgs = prePruneMsgs
				inPlaceCacheWarm = true
				inPlaceTokens = prePruneTokens
			}
			inPlace := &inPlaceCompactionContext{
				Driver:         drv,
				Target:         target,
				SessionID:      req.SessionID,
				CacheKey:       req.CacheKey,
				System:         systemPrompt,
				SystemSuffix:   systemSuffix,
				Tools:          toolSpecs,
				Messages:       inPlaceMsgs,
				MaxTokens:      maxOut,
				Effort:         effectiveEffort,
				FastMode:       effectiveFast,
				CacheWarm:      inPlaceCacheWarm,
				MeasuredTokens: inPlaceTokens,
			}
			if compacted, cpInfo, ok := e.compactContextOverflowWithInPlace(ctx, req.ProviderID, req.ModelID, msgs, historyLen, step, inPlace, emit); ok {
				overflowCompactions++
				historyLen = 0
				tokenTracker.InvalidateAnchor(true)
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
							"proactive":    true,
						},
					})
				}
				msgs = compacted
			} else if ctx.Err() == nil {
				proactiveCompactionFailures++
			}
		}

		buildStepReq := func() (llm.StreamHTTPRequest, error) {
			return drv.BuildRequest(ModelRequest{
				SessionID:    req.SessionID,
				CacheKey:     req.CacheKey,
				Target:       target,
				System:       systemPrompt,
				SystemSuffix: systemSuffix,
				Messages:     msgs,
				Tools:        toolSpecs,
				MaxTokens:    maxOut,
				Effort:       effectiveEffort,
				FastMode:     effectiveFast,
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
			tokenTracker.InvalidateAnchor(true)
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
			stepStreamCtx, cancelStepStream := context.WithCancel(ctx)
			resp, openErr := e.stream.OpenStream(stepStreamCtx, httpReq)
			headerLatency := time.Since(openStarted)
			if openErr != nil {
				cancelStepStream()
				if target.FallbackOpenAI && ctx.Err() == nil && isUnsupportedPromotedProtocolEndpoint(target.Protocol, openErr) {
					target.Protocol = llm.ProtocolOpenAI
					target.FallbackOpenAI = false
					tokenTracker.InvalidateAnchor(true)
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
				if effectiveFast && ctx.Err() == nil && isUnsupportedFastModeError(openErr) {
					effectiveFast = false
					e.noFastMode.Store(fastModeMemoKey(target), struct{}{})
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
					if compacted, cpInfo, ok := e.compactContextOverflow(ctx, req.ProviderID, req.ModelID, msgs, historyLen, step, emit); ok {
						overflowCompactions++
						historyLen = 0
						tokenTracker.InvalidateAnchor(true)
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
						if llm.IsHTTP2ProtocolError(openErr) {
							httpReq.ForceHTTP1 = true
						}
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
			streamed, parseErr = collectStepStream(stepStreamCtx, cancelStepStream, drv, resp.Body, target, step, nextPartID, emit)
			_ = resp.Body.Close()
			cancelStepStream()
			if parseErr == nil {
				e.reportStreamOutcome(ctx, target, headerLatency, nil)
				stepErr = nil
				break
			}
			if streamed.chantingLoop && ctx.Err() == nil {
				if canRecoverThinkingChant(streamed, chantingRecoveries, step, maxSteps) {
					chantingRecoveries++
					msgs = appendThinkingChantRecoveryMessages(msgs)
					stepErr = nil
					break
				}
				return parseErr
			}
			e.reportStreamOutcome(ctx, target, time.Since(openStarted), parseErr)
			if !streamed.emittedVisible && effectiveFast && ctx.Err() == nil && isUnsupportedFastModeError(parseErr) {
				effectiveFast = false
				e.noFastMode.Store(fastModeMemoKey(target), struct{}{})
				httpReq, err = buildStepReq()
				if err != nil {
					return err
				}
				attempt = -1
				continue
			}
			if !streamed.emittedVisible && tryRecoverThinkingSignature(parseErr) {
				httpReq, err = buildStepReq()
				if err != nil {
					return err
				}
				attempt = -1
				continue
			}
			if !streamed.emittedVisible && overflowCompactions < maxReactiveOverflowCompactions && ctx.Err() == nil && isContextOverflowError(parseErr) {
				if compacted, cpInfo, ok := e.compactContextOverflow(ctx, req.ProviderID, req.ModelID, msgs, historyLen, step, emit); ok {
					overflowCompactions++
					historyLen = 0
					tokenTracker.InvalidateAnchor(true)
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
					if llm.IsHTTP2ProtocolError(parseErr) {
						httpReq.ForceHTTP1 = true
					}
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
		if streamed.chantingLoop {
			continue
		}

		if streamed.usage != nil {
			tokenTracker.ObserveStep(msgs, fullSystem, toolSpecs, *streamed.usage)
			cacheKey := strings.TrimSpace(req.CacheKey)
			if cacheKey == "" {
				cacheKey = strings.TrimSpace(req.SessionID)
			}
			tokenTracker.ObservePrefixCacheStability(req.SessionID, target, cacheKey, fullSystem, toolSpecs, msgs, *streamed.usage)
		}

		if len(streamed.toolCalls) > 0 {
			streamed.asstBlocks, streamed.toolCalls = disambiguateToolCallIDs(msgs, streamed.asstBlocks, streamed.toolCalls)
		}
		if len(streamed.asstBlocks) > 0 {
			msgs = append(msgs, Message{Role: RoleAssistant, Content: streamed.asstBlocks})
		}

		if len(streamed.toolCalls) == 0 {
			if isTruncatedFinishReason(streamed.stepFinish) {
				if (streamed.visibleTextLen == 0 || streamed.droppedTruncatedTool) && truncationRecoveries == 0 && step < maxSteps {
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

		results := e.executeToolCalls(ctx, execReq, origMode, agentMap, cfgDoc, rulesTracker, msgs, step, streamed.toolCalls, loopGuard, nextPartID, emit)
		maybeAppendStepBudgetNotice(step, maxSteps, results)
		resultBlocks := make([]ContentBlock, 0, len(results))
		promoteToBuild := false
		for i, res := range results {
			resultBlocks = append(resultBlocks, ContentBlock{
				Type:      ContentToolResult,
				ToolUseID: streamed.toolCalls[i].ID,
				Text:      res.Output,
				IsError:   res.IsError,
			})
			if !externalCollab && !res.IsError && req.SubtaskDepth == 0 && strings.EqualFold(effectiveMode, "plan") {
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
		loadedTools := catalog.record(streamed.toolCalls, results)
		if promoteToBuild {
			prevMode := effectiveMode
			effectiveMode = "build"
			origMode = "build"
			execReq.Mode = "build"
			if spec, ok := agentMap["plan"]; ok && strings.TrimSpace(spec.Prompt) != "" {
				callerSystem = strings.TrimSpace(strings.Replace(callerSystem, strings.TrimSpace(spec.Prompt), "", 1))
			}
			systemPrompt, _ = buildSystemPromptWithRules(req.WorktreeDir, callerSystem, drv.SystemPromptPatch(), effectiveMode, req.SubtaskDepth, cfgDoc)
			catalog.configureTaskPolicy(origMode, agentMap, cfgDoc)
			if isOrchestratorActive(req) {
				catalog.configureOrchestrator(true, effectiveMode)
				systemPrompt = appendSystemPromptSuffix(systemPrompt, orchestratorProtocolGuidance)
			}
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
		if promoteToBuild || loadedTools {
			toolSpecs = catalog.specs(e.registry, effectiveMode, req.SubtaskDepth)
			compactLimit = effectiveCompactContextLimit(target.ContextLimit, toolSpecs)
			tokenTracker.InvalidateAnchor(true)
		}
		if step == maxSteps {
			if finalBlocks, ok := e.runGracefulFinalizeStep(ctx, drv, target, req, systemPrompt, msgs, toolSpecs, maxOut, compactLimit, contextThresholds, tokenTracker, maxSteps, effectiveEffort, foldUnsignedToolSteps, nextPartID, emit); ok {
				if len(finalBlocks) > 0 {
					msgs = append(msgs, Message{Role: RoleAssistant, Content: finalBlocks})
				}
				return nil
			}
		}
	}
	return fmt.Errorf("reached maximum step limit (%d)", maxSteps)
}

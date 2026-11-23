package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/hoxiai/hoxi/internal/llm"
)

func (e *Engine) executeToolCalls(
	ctx context.Context,
	req TurnRequest,
	origMode string,
	agentMap map[string]llm.AgentSpec,
	cfgDoc map[string]any,
	rulesTracker *SubdirRuleTracker,
	visibleMsgs []Message,
	step int,
	calls []ToolCall,
	loopGuard *stepLoopGuard,
	nextPartID func(string) string,
	emit func(EngineEvent),
) []ToolOutput {
	results := make([]ToolOutput, len(calls))
	partIDs := make([]string, len(calls))
	tc := ToolContext{
		commandLogs:        req.commandLogs,
		SessionID:          req.SessionID,
		WorktreeDir:        req.WorktreeDir,
		Mode:               req.Mode,
		SubtaskDepth:       req.SubtaskDepth,
		OrchestratorOnly:   isOrchestratorActive(req) && !strings.EqualFold(strings.TrimSpace(req.Mode), "plan"),
		VisibleMessages:    visibleMsgs,
		RulesTracker:       rulesTracker,
		FileRevisions:      req.FileRevisions,
		CoordinateFileEdit: req.CoordinateFileEdit,
		RecordFileEdit:     req.RecordFileEdit,
		RunCommand:         req.CommandRunner,
		StreamRunCommand:   req.StreamCommandRunner,
		BackgroundCommands: req.BackgroundCommands,
		Diagnostics:        req.Diagnostics,
		RepoMap:            req.RepoMap,
		CodeSearch:         req.CodeSearch,
		SymbolInspect:      req.SymbolInspect,
		ImpactInspect:      req.ImpactInspect,
		AskQuestion:        req.AskQuestion,
		MCPConfig:          req.MCPConfig,
		projectConfig:      req.projectConfig,
		HostInvoke:         req.HostInvoke,
	}
	if req.SubtaskDepth == 0 {
		tc.Steer = req.Steer
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
			loopGuard.ObserveStepCalls(calls[i:j], results[i:j], e.isReadOnlyCall)
			i = j
			continue
		}
		results[i] = e.runSingleTool(ctx, tc, step, calls[i], partIDs[i], loopGuard, req.PermissionHook, emit)
		loopGuard.ObserveStepCalls(calls[i:i+1], results[i:i+1], e.isReadOnlyCall)
		i++
	}
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

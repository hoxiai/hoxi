package gateway

import (
	"context"
	"strconv"
	"strings"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/codeindex"
	"github.com/hoxiai/hoxi/internal/llm"
	"github.com/hoxiai/hoxi/internal/runner"
)

type nativeSearchHit struct {
	Path      string
	Symbol    string
	StartLine int
	EndLine   int
	Score     float64
	Snippet   string
}

type gatewayNativeAgent struct {
	engine        *agent.Engine
	codeSearch    func(ctx context.Context, dir, query, pathPrefix string, limit int) ([]nativeSearchHit, error)
	symbolInspect func(ctx context.Context, dir, relPath, symbol string, includeCallers bool) (string, error)
	impactInspect func(ctx context.Context, dir, relPath, symbol string, depth, limit int) (string, error)
	mcpConfig     func() map[string]any
	hostInvoke    agent.HostInvokeHook
	skillCatalog  func(ctx context.Context, sessionID, worktreeDir string) string
}

func newGatewayNativeAgent(
	stream llm.StreamClient,
	searchFn func(ctx context.Context, dir, query, pathPrefix string, limit int) ([]nativeSearchHit, error),
	inspectFn func(ctx context.Context, dir, relPath, symbol string, includeCallers bool) (string, error),
	impactFn func(ctx context.Context, dir, relPath, symbol string, depth, limit int) (string, error),
	mcpCfgFn func() map[string]any,
	hostFn agent.HostInvokeHook,
	skillCatalogFns ...func(ctx context.Context, sessionID, worktreeDir string) string,
) runner.NativeEngine {
	var skillCatFn func(ctx context.Context, sessionID, worktreeDir string) string
	if len(skillCatalogFns) > 0 {
		skillCatFn = skillCatalogFns[0]
	}
	return &gatewayNativeAgent{
		engine:        agent.NewDefaultEngine(stream),
		codeSearch:    searchFn,
		symbolInspect: inspectFn,
		impactInspect: impactFn,
		mcpConfig:     mcpCfgFn,
		hostInvoke:    hostFn,
		skillCatalog:  skillCatFn,
	}
}

func (g *gatewayNativeAgent) Summarize(ctx context.Context, providerID, modelID, transcript string) (string, error) {
	return g.engine.Summarize(ctx, providerID, modelID, transcript)
}

func mapNativeBlocksToAgent(src []runner.NativeContentBlock) []agent.ContentBlock {
	if len(src) == 0 {
		return nil
	}
	blocks := make([]agent.ContentBlock, 0, len(src))
	for _, b := range src {
		blocks = append(blocks, agent.ContentBlock{
			Type:      agent.ContentType(b.Type),
			Text:      b.Text,
			MimeType:  b.MimeType,
			Data:      b.Data,
			Filename:  b.Filename,
			Signature: b.Signature,
			ID:        b.ID,
			Name:      b.Name,
			Input:     b.Input,
			ToolUseID: b.ToolUseID,
			IsError:   b.IsError,
		})
	}
	return blocks
}

func mapAgentBlocksToNative(src []agent.ContentBlock) []runner.NativeContentBlock {
	if len(src) == 0 {
		return nil
	}
	blocks := make([]runner.NativeContentBlock, 0, len(src))
	for _, b := range src {
		blocks = append(blocks, runner.NativeContentBlock{
			Type:      string(b.Type),
			Text:      b.Text,
			MimeType:  b.MimeType,
			Data:      b.Data,
			Filename:  b.Filename,
			Signature: b.Signature,
			ID:        b.ID,
			Name:      b.Name,
			Input:     b.Input,
			ToolUseID: b.ToolUseID,
			IsError:   b.IsError,
		})
	}
	return blocks
}

func (g *gatewayNativeAgent) RunTurn(ctx context.Context, req runner.NativeTurnRequest) error {
	history := make([]agent.Message, 0, len(req.History))
	for _, m := range req.History {
		history = append(history, agent.Message{
			Role:    agent.Role(m.Role),
			Content: mapNativeBlocksToAgent(m.Content),
		})
	}

	var permHook agent.PermissionHook
	if req.PermissionHook != nil {
		permHook = func(ctx context.Context, pr agent.PermissionRequest) error {
			return req.PermissionHook(ctx, runner.NativePermissionRequest{
				SessionID: pr.SessionID,
				CallID:    pr.CallID,
				ToolName:  pr.ToolName,
				Input:     pr.Input,
				IsRead:    pr.IsRead,
				ForceAsk:  pr.ForceAsk,
			})
		}
	}

	var sink agent.EventSink
	if req.Emit != nil {
		sink = agent.EventSinkFunc(func(evt agent.EngineEvent) {
			var u *runner.NativeTokenUsage
			if evt.Usage != nil {
				u = &runner.NativeTokenUsage{
					Input:      evt.Usage.Input,
					Output:     evt.Usage.Output,
					Reasoning:  evt.Usage.Reasoning,
					CacheRead:  evt.Usage.CacheRead,
					CacheWrite: evt.Usage.CacheWrite,
				}
			}
			req.Emit(runner.NativeEvent{
				Kind:         string(evt.Kind),
				ParentCallID: evt.ParentCallID,
				Step:         evt.Step,
				PartID:       evt.PartID,
				CallID:       evt.CallID,
				ToolName:     evt.ToolName,
				Text:         evt.Text,
				Signature:    evt.Signature,
				Title:        evt.Title,
				Finish:       evt.Finish,
				Input:        evt.Input,
				Metadata:     evt.Metadata,
				Media:        mapAgentBlocksToNative(evt.Media),
				ErrorText:    evt.ErrorText,
				Usage:        u,
			})
		})
	}

	var searchHook agent.CodeSearchHook
	if g.codeSearch != nil {
		searchHook = func(ctx context.Context, dir, query, pathPrefix string, limit int) ([]agent.CodeSearchHit, error) {
			rawHits, err := g.codeSearch(ctx, dir, query, pathPrefix, limit)
			if err != nil {
				return nil, err
			}
			out := make([]agent.CodeSearchHit, len(rawHits))
			for i, h := range rawHits {
				out[i] = agent.CodeSearchHit{
					Path:      h.Path,
					Symbol:    h.Symbol,
					StartLine: h.StartLine,
					EndLine:   h.EndLine,
					Score:     h.Score,
					Snippet:   h.Snippet,
				}
			}
			return out, nil
		}
	}

	sysPrompt := req.System
	if g.skillCatalog != nil && !strings.Contains(sysPrompt, "<available_skills>") {
		if cat := strings.TrimSpace(g.skillCatalog(ctx, req.SessionID, req.WorktreeDir)); cat != "" {
			if strings.TrimSpace(sysPrompt) == "" {
				sysPrompt = cat
			} else {
				sysPrompt = sysPrompt + "\n\n" + cat
			}
		}
	}

	return g.engine.RunTurn(ctx, agent.TurnRequest{
		SessionID:      req.SessionID,
		WorktreeDir:    req.WorktreeDir,
		Mode:           req.Mode,
		ProviderID:     req.ProviderID,
		ModelID:        req.ModelID,
		Effort:         req.Effort,
		System:         sysPrompt,
		History:        history,
		UserPrompt:     req.UserPrompt,
		UserBlocks:     mapNativeBlocksToAgent(req.UserBlocks),
		Steer:          steerFunc(req.SteerDrain),
		PermissionHook: permHook,
		CommandRunner:  req.CommandRunner,
		Diagnostics:    req.Diagnostics,
		CodeSearch:     searchHook,
		SymbolInspect:  g.symbolInspect,
		ImpactInspect:  g.impactInspect,
		AskQuestion:    req.AskQuestion,
		MCPConfig:      g.mcpConfig,
		HostInvoke:     g.hostInvoke,
		Sink:           sink,
	})
}

type steerFunc func() []string

func (f steerFunc) Drain() []string {
	if f == nil {
		return nil
	}
	return f()
}

func buildNativeCodeIndexHooks(
	idx *codeindex.Service,
) (
	func(ctx context.Context, dir, query, pathPrefix string, limit int) ([]nativeSearchHit, error),
	func(ctx context.Context, dir, relPath, symbol string, includeCallers bool) (string, error),
	func(ctx context.Context, dir, relPath, symbol string, depth, limit int) (string, error),
) {
	if idx == nil {
		return nil, nil, nil
	}
	searchFn := func(ctx context.Context, dir, query, pathPrefix string, limit int) ([]nativeSearchHit, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		res, err := idx.SearchWithPath(dir, query, pathPrefix, limit)
		if err != nil {
			return nil, err
		}
		hits := make([]nativeSearchHit, len(res.Results))
		for i, r := range res.Results {
			hits[i] = nativeSearchHit{
				Path:      r.Path,
				Symbol:    r.Symbol,
				StartLine: r.StartLine,
				EndLine:   r.EndLine,
				Score:     r.Score,
				Snippet:   r.Snippet,
			}
		}
		return hits, nil
	}
	inspectFn := func(ctx context.Context, dir, relPath, symbol string, includeCallers bool) (string, error) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		insp, err := idx.Inspect(dir, relPath, symbol, 0, 0, 5)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		b.WriteString("Definition: " + insp.Path + ":" + strconv.Itoa(insp.StartLine) + "-" + strconv.Itoa(insp.EndLine))
		if insp.Symbol != "" {
			b.WriteString(" (" + insp.Symbol + ")")
		}
		b.WriteString("\n" + insp.Content)
		if includeCallers {
			if callers, cErr := idx.Callers(dir, symbol, insp.Path, 12); cErr == nil {
				if len(callers.Callers) > 0 {
					b.WriteString("\n\nCallers:\n")
					for _, c := range callers.Callers {
						b.WriteString("- " + c.Path + ":" + strconv.Itoa(c.Line) + ": " + c.Snippet + "\n")
					}
				}
				if len(callers.Tests) > 0 {
					b.WriteString("\nTest References:\n")
					for _, tr := range callers.Tests {
						b.WriteString("- " + tr.Path + ":" + strconv.Itoa(tr.Line) + ": " + tr.Snippet + "\n")
					}
				}
			}
		}
		return strings.TrimSpace(b.String()), nil
	}
	impactFn := func(ctx context.Context, dir, relPath, symbol string, depth, limit int) (string, error) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		imp, err := idx.Impact(dir, symbol, relPath, depth, limit)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		b.WriteString("Symbol: " + imp.Symbol + " (" + imp.Path + ")\n")
		if callees, cErr := idx.Callees(dir, imp.Symbol, imp.Path, limit); cErr == nil && len(callees.Callees) > 0 {
			b.WriteString("\nOutgoing Callees:\n")
			for _, cal := range callees.Callees {
				b.WriteString("- " + cal.Path + ":" + strconv.Itoa(cal.StartLine) + " [" + cal.Symbol + "]\n")
			}
		}
		if len(imp.DirectCallers) > 0 {
			b.WriteString("\nDirect Callers:\n")
			for _, c := range imp.DirectCallers {
				b.WriteString("- " + c.Path + ":" + strconv.Itoa(c.Line) + ": " + c.Snippet + "\n")
			}
		}
		if len(imp.TransitiveCallers) > 0 {
			b.WriteString("\nTransitive Callers:\n")
			for _, tc := range imp.TransitiveCallers {
				b.WriteString("- [depth " + strconv.Itoa(tc.Depth) + " via " + tc.Via + "] " + tc.Path + ":" + strconv.Itoa(tc.Line) + "\n")
			}
		}
		if len(imp.Tests) > 0 {
			b.WriteString("\nRelated Tests:\n")
			for _, t := range imp.Tests {
				b.WriteString("- " + t.Path + ":" + strconv.Itoa(t.Line) + ": " + t.Snippet + "\n")
			}
		}
		if len(imp.AffectedFiles) > 0 {
			b.WriteString("\nAffected Files (" + strconv.Itoa(len(imp.AffectedFiles)) + "): " + strings.Join(imp.AffectedFiles, ", ") + "\n")
		}
		return strings.TrimSpace(b.String()), nil
	}
	return searchFn, inspectFn, impactFn
}

func (g *Gateway) loadNativeAgentsForDir(dir string) []llm.AgentSpec {
	if strings.TrimSpace(dir) == "" {
		dir = g.rootDir
	}
	var cfgDoc map[string]any
	if g.providers != nil {
		cfgDoc = g.providers.LoadHoxiConfigDoc()
	}
	active, _ := llm.LoadAgents(dir, cfgDoc)
	return active
}

func (g *Gateway) loadNativeCommandsForDir(dir string) []llm.SlashCommandSpec {
	if strings.TrimSpace(dir) == "" {
		dir = g.rootDir
	}
	var cfgDoc map[string]any
	if g.providers != nil {
		cfgDoc = g.providers.LoadHoxiConfigDoc()
	}
	return llm.LoadCommands(dir, cfgDoc)
}

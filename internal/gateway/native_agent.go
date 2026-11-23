package gateway

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/codeindex"
	"github.com/hoxiai/hoxi/internal/llm"
	"github.com/hoxiai/hoxi/internal/runner"
)

const maxTrackedNativeRevisionSessions = 128

type nativeSearchHit struct {
	Path      string
	Symbol    string
	StartLine int
	EndLine   int
	Score     float64
	Snippet   string
}

type gatewayNativeAgent struct {
	engine          *agent.Engine
	codeSearch      func(ctx context.Context, dir, query, pathPrefix string, limit int) ([]nativeSearchHit, error)
	symbolInspect   func(ctx context.Context, dir, relPath, symbol string, includeCallers bool) (string, error)
	impactInspect   func(ctx context.Context, dir, relPath, symbol string, depth, limit int) (string, error)
	repoMap         agent.RepoMapHook
	codeIndexDirty  func(dir string)
	mcpConfig       func() map[string]any
	directoryConfig func(string) map[string]any
	sessionMCP      func(sessionID, directory string) map[string]any
	hostInvoke      agent.HostInvokeHook
	skillCatalog    func(ctx context.Context, sessionID, worktreeDir string) string

	revMu     sync.Mutex
	revBySess map[string]*agent.FileRevisionTracker
	revOrder  []string
}

func newGatewayNativeAgent(
	stream llm.StreamClient,
	searchFn func(ctx context.Context, dir, query, pathPrefix string, limit int) ([]nativeSearchHit, error),
	inspectFn func(ctx context.Context, dir, relPath, symbol string, includeCallers bool) (string, error),
	impactFn func(ctx context.Context, dir, relPath, symbol string, depth, limit int) (string, error),
	mcpCfgFn func() map[string]any,
	hostFn agent.HostInvokeHook,
	skillCatalogFns ...func(ctx context.Context, sessionID, worktreeDir string) string,
) *gatewayNativeAgent {
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
		revBySess:     make(map[string]*agent.FileRevisionTracker),
	}
}

func (g *gatewayNativeAgent) revisionTrackerForSession(sessionID string) *agent.FileRevisionTracker {
	sid := strings.TrimSpace(sessionID)
	if g == nil || sid == "" {
		return agent.NewFileRevisionTracker()
	}
	g.revMu.Lock()
	defer g.revMu.Unlock()
	if g.revBySess == nil {
		g.revBySess = make(map[string]*agent.FileRevisionTracker)
	}
	if tr, ok := g.revBySess[sid]; ok && tr != nil {
		return tr
	}
	for len(g.revOrder) >= maxTrackedNativeRevisionSessions {
		oldest := g.revOrder[0]
		g.revOrder = g.revOrder[1:]
		delete(g.revBySess, oldest)
	}
	tr := agent.NewFileRevisionTracker()
	g.revBySess[sid] = tr
	g.revOrder = append(g.revOrder, sid)
	return tr
}

func (g *gatewayNativeAgent) ForgetSession(sessionID string) {
	sid := strings.TrimSpace(sessionID)
	if sid != "" {
		agent.DeleteSessionTodos(sid)
		agent.DeleteSessionBackgroundProcs(sid)
		agent.DeleteSessionSubtasks(sid)
	}
	if g == nil || sid == "" {
		return
	}
	g.revMu.Lock()
	defer g.revMu.Unlock()
	if _, ok := g.revBySess[sid]; !ok {
		return
	}
	delete(g.revBySess, sid)
	for i, k := range g.revOrder {
		if k == sid {
			g.revOrder = append(g.revOrder[:i], g.revOrder[i+1:]...)
			break
		}
	}
}

func (g *gatewayNativeAgent) SummarizeWithUsage(ctx context.Context, providerID, modelID, transcript string, record func(agent.TokenUsage)) (string, error) {
	return g.engine.SummarizeWithUsage(ctx, providerID, modelID, transcript, record)
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

func wrapNativeRunnerError(err error) error {
	if err == nil {
		return nil
	}
	var promErr *runner.NativeBackgroundPromotedError
	if errors.As(err, &promErr) && promErr != nil {
		return &agent.BackgroundPromotedError{
			Status: mapNativeBgStatus(promErr.Proc),
		}
	}
	return err
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
				Usage:        evt.Usage,
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
	if g.skillCatalog != nil &&
		!strings.Contains(sysPrompt, "<available_skills>") &&
		!strings.Contains(sysPrompt, "On-demand skills, listed by name only:") {
		if cat := strings.TrimSpace(g.skillCatalog(ctx, req.SessionID, req.WorktreeDir)); cat != "" {
			if strings.TrimSpace(sysPrompt) == "" {
				sysPrompt = cat
			} else {
				sysPrompt = sysPrompt + "\n\n" + cat
			}
		}
	}

	if len(req.InitialTodos) > 0 {
		seeded := make([]agent.TodoItem, 0, len(req.InitialTodos))
		for _, item := range req.InitialTodos {
			seeded = append(seeded, agent.TodoItem{
				ID:       item.ID,
				Content:  item.Content,
				Status:   item.Status,
				Priority: item.Priority,
			})
		}
		agent.SeedSessionTodosIfAbsent(req.SessionID, seeded)
	}

	var coordEditHook agent.FileEditCoordinatorHook
	if req.CoordinateFileEdit != nil {
		coordEditHook = func(ctx context.Context, paths []string, onWait func(string)) (agent.FileEditCoordination, error) {
			res, err := req.CoordinateFileEdit(ctx, paths, onWait)
			if err != nil {
				return agent.FileEditCoordination{}, err
			}
			return agent.FileEditCoordination{
				Release:        res.Release,
				Waited:         res.Waited,
				ModifiedByPeer: res.ModifiedByPeer,
			}, nil
		}
	}

	var bgMgr agent.BackgroundCommandManager
	if req.BackgroundManager != nil {
		bgMgr = nativeBgManagerAdapter{inner: req.BackgroundManager}
	}

	var cmdRunner func(ctx context.Context, dir, command string) (string, int, error)
	if req.CommandRunner != nil {
		cmdRunner = func(ctx context.Context, dir, command string) (string, int, error) {
			out, code, err := req.CommandRunner(ctx, dir, command)
			return out, code, wrapNativeRunnerError(err)
		}
	}
	var streamCmdRunner func(ctx context.Context, dir, command string, capture io.Writer, onOutput func(string)) (string, int, error)
	if req.StreamCommandRunner != nil {
		streamCmdRunner = func(ctx context.Context, dir, command string, capture io.Writer, onOutput func(string)) (string, int, error) {
			out, code, err := req.StreamCommandRunner(ctx, dir, command, capture, onOutput)
			return out, code, wrapNativeRunnerError(err)
		}
	}

	baseConfig := g.mcpConfig
	if g.directoryConfig != nil {
		baseConfig = func() map[string]any { return g.directoryConfig(req.WorktreeDir) }
	}
	config := baseConfig
	if g.sessionMCP != nil {
		config = func() map[string]any {
			var doc map[string]any
			if baseConfig != nil {
				doc = baseConfig()
			}
			extra := g.sessionMCP(req.SessionID, req.WorktreeDir)
			if len(extra) == 0 {
				return doc
			}
			cloned := make(map[string]any, len(doc)+1)
			for k, v := range doc {
				cloned[k] = v
			}
			mcpMap := make(map[string]any, len(extra))
			if existing, ok := doc["mcp"].(map[string]any); ok {
				for k, v := range existing {
					mcpMap[k] = v
				}
			}
			for k, v := range extra {
				if _, exists := mcpMap[k]; !exists {
					mcpMap[k] = v
				}
			}
			cloned["mcp"] = mcpMap
			return cloned
		}
	}
	recordEdit := req.RecordFileEdit
	if g.codeIndexDirty != nil && strings.TrimSpace(req.WorktreeDir) != "" {
		dir := req.WorktreeDir
		dirtyFn := g.codeIndexDirty
		prev := req.RecordFileEdit
		recordEdit = func(paths []string) {
			dirtyFn(dir)
			if prev != nil {
				prev(paths)
			}
		}
	}
	var orchCfg *agent.OrchestratorConfig
	if req.Orchestrator != nil &&
		strings.TrimSpace(req.Orchestrator.ExecutorProviderID) != "" &&
		strings.TrimSpace(req.Orchestrator.ExecutorModelID) != "" {
		orchCfg = &agent.OrchestratorConfig{
			ExecutorProviderID: strings.TrimSpace(req.Orchestrator.ExecutorProviderID),
			ExecutorModelID:    strings.TrimSpace(req.Orchestrator.ExecutorModelID),
			ExecutorEffort:     strings.TrimSpace(req.Orchestrator.ExecutorEffort),
		}
	}
	return g.engine.RunTurn(ctx, agent.TurnRequest{
		SessionID:          req.SessionID,
		WorktreeDir:        req.WorktreeDir,
		Mode:               req.Mode,
		ProviderID:         req.ProviderID,
		ModelID:            req.ModelID,
		Effort:             req.Effort,
		FastMode:           req.FastMode,
		Orchestrator:       orchCfg,
		System:             sysPrompt,
		History:            history,
		UserPrompt:         req.UserPrompt,
		UserBlocks:         mapNativeBlocksToAgent(req.UserBlocks),
		FileRevisions:      g.revisionTrackerForSession(req.SessionID),
		CoordinateFileEdit: coordEditHook,
		RecordFileEdit:     recordEdit,
		Steer: nativeSteerAdapter{
			drainStr:   req.SteerDrain,
			drainItems: req.SteerDrainItems,
			hasPending: req.SteerHasPending,
			notifyCh:   req.SteerNotify,
		},
		PermissionHook:      permHook,
		CommandRunner:       cmdRunner,
		StreamCommandRunner: streamCmdRunner,
		BackgroundCommands:  bgMgr,
		Diagnostics:         req.Diagnostics,
		CodeSearch:          searchHook,
		SymbolInspect:       g.symbolInspect,
		ImpactInspect:       g.impactInspect,
		RepoMap:             g.repoMap,
		AskQuestion:         req.AskQuestion,
		MCPConfig:           config,
		HostInvoke:          g.hostInvoke,
		Sink:                sink,
	})
}

type nativeBgManagerAdapter struct {
	inner runner.NativeBackgroundManager
}

func mapNativeBgStatus(st runner.NativeBackgroundProc) agent.BackgroundProcStatus {
	return agent.BackgroundProcStatus{
		ProcID:    st.ProcID,
		Command:   st.Command,
		PID:       st.PID,
		Running:   st.Running,
		ExitCode:  st.ExitCode,
		StartedAt: st.StartedAt,
		Output:    st.Output,
		Truncated: st.Truncated,
	}
}

func (a nativeBgManagerAdapter) Start(ctx context.Context, dir, command string) (agent.BackgroundProcStatus, error) {
	st, err := a.inner.Start(ctx, dir, command)
	return mapNativeBgStatus(st), err
}

func (a nativeBgManagerAdapter) Output(ctx context.Context, procID string, wait time.Duration) (agent.BackgroundProcStatus, error) {
	st, err := a.inner.Output(ctx, procID, wait)
	return mapNativeBgStatus(st), err
}

func (a nativeBgManagerAdapter) Stop(ctx context.Context, procID string) (agent.BackgroundProcStatus, error) {
	st, err := a.inner.Stop(ctx, procID)
	return mapNativeBgStatus(st), err
}

func (a nativeBgManagerAdapter) List(ctx context.Context) ([]agent.BackgroundProcStatus, error) {
	items, err := a.inner.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]agent.BackgroundProcStatus, len(items))
	for i, it := range items {
		out[i] = mapNativeBgStatus(it)
	}
	return out, nil
}

type nativeSteerAdapter struct {
	drainStr   func() []string
	drainItems func() []runner.NativeSteerItem
	hasPending func() bool
	notifyCh   func() <-chan struct{}
}

func (a nativeSteerAdapter) HasPending() bool {
	if a.hasPending == nil {
		return false
	}
	return a.hasPending()
}

func (a nativeSteerAdapter) NotifyCh() <-chan struct{} {
	if a.notifyCh == nil {
		return nil
	}
	return a.notifyCh()
}

func (a nativeSteerAdapter) Drain() []string {
	if a.drainItems != nil {
		items := a.drainItems()
		if len(items) == 0 {
			return nil
		}
		out := make([]string, 0, len(items))
		for _, it := range items {
			if strings.TrimSpace(it.Prompt) != "" {
				out = append(out, it.Prompt)
			}
		}
		return out
	}
	if a.drainStr == nil {
		return nil
	}
	return a.drainStr()
}

func (a nativeSteerAdapter) DrainMessages() []agent.Message {
	if a.drainItems != nil {
		items := a.drainItems()
		if len(items) == 0 {
			return nil
		}
		msgs := make([]agent.Message, 0, len(items))
		for _, it := range items {
			blocks := mapNativeBlocksToAgent(it.Blocks)
			if len(blocks) == 0 && strings.TrimSpace(it.Prompt) != "" {
				blocks = []agent.ContentBlock{{Type: agent.ContentText, Text: it.Prompt}}
			}
			if len(blocks) > 0 {
				msgs = append(msgs, agent.Message{
					Role:    agent.RoleUser,
					Content: blocks,
				})
			}
		}
		return msgs
	}
	raw := a.Drain()
	if len(raw) == 0 {
		return nil
	}
	msgs := make([]agent.Message, 0, len(raw))
	for _, s := range raw {
		if strings.TrimSpace(s) != "" {
			msgs = append(msgs, agent.Message{
				Role:    agent.RoleUser,
				Content: []agent.ContentBlock{{Type: agent.ContentText, Text: s}},
			})
		}
	}
	return msgs
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

func buildNativeRepoMapHook(idx *codeindex.Service) agent.RepoMapHook {
	if idx == nil {
		return nil
	}
	return func(ctx context.Context, dir, pathPrefix, query string, limit int) (string, error) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		res, err := idx.Map(dir, pathPrefix, query, limit)
		if err != nil {
			return "", err
		}
		if len(res.Entries) == 0 {
			return "No indexed files matched the requested scope.", nil
		}
		var b strings.Builder
		b.WriteString("Repository Map (" + strconv.Itoa(len(res.Entries)) + " of " + strconv.Itoa(res.Files) + " indexed files):\n")
		for _, e := range res.Entries {
			b.WriteString(e.Path + " (rank " + strconv.Itoa(e.Score) + ")\n")
			for _, sym := range e.Symbols {
				line := "  L" + strconv.Itoa(sym.StartLine) + "-" + strconv.Itoa(sym.EndLine) + " "
				if sym.Signature != "" {
					line += sym.Signature
				} else {
					line += sym.Symbol
				}
				if sym.References > 0 {
					line += " (" + strconv.Itoa(sym.References) + " refs)"
				}
				b.WriteString(line + "\n")
			}
		}
		return strings.TrimSpace(b.String()), nil
	}
}

func (g *Gateway) loadNativeAgentsForDir(dir string) []llm.AgentSpec {
	if strings.TrimSpace(dir) == "" {
		dir = g.rootDir
	}
	var cfgDoc map[string]any
	if g.providers != nil {
		cfgDoc = g.providers.LoadNativeConfigDoc(dir)
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
		cfgDoc = g.providers.LoadNativeConfigDoc(dir)
	}
	return llm.LoadCommands(dir, cfgDoc)
}

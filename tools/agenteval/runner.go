package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/codeindex"
	"github.com/hoxiai/hoxi/internal/llm"
	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/subproc"
)

const (
	defaultMaxEvalSteps   = 25
	defaultEvalTimeout    = 3 * time.Minute
	maxEvalCmdOutputBytes = 128 * 1024
)

// RunOptions configures how agenteval executes the benchmark suite.
type RunOptions struct {
	ProviderID         string
	ModelID            string
	Effort             string
	ExecutorProviderID string
	ExecutorModelID    string
	ExecutorEffort     string
	MaxSteps           int
	TaskTimeout        time.Duration
	Verbose            bool
	Smoke              bool
	StreamClient       llm.StreamClient
	LogWriter          io.Writer
}

// RunSuite executes tasks sequentially against the real Hoxi Native Agent Engine.
func RunSuite(ctx context.Context, tasks []Task, opts RunOptions) Report {
	if opts.MaxSteps <= 0 {
		opts.MaxSteps = defaultMaxEvalSteps
	}
	if opts.TaskTimeout <= 0 {
		opts.TaskTimeout = defaultEvalTimeout
	}
	if opts.LogWriter == nil {
		opts.LogWriter = io.Discard
	}
	mode := "live"
	if opts.Smoke {
		mode = "smoke"
	}
	results := make([]TaskResult, 0, len(tasks))
	for _, task := range tasks {
		res := runSingleTask(ctx, task, opts)
		results = append(results, res)
	}
	rep := Report{
		CreatedAt:        nowRFC3339(),
		Mode:             mode,
		ProviderID:       opts.ProviderID,
		ModelID:          opts.ModelID,
		Effort:           opts.Effort,
		ExecutorProvider: opts.ExecutorProviderID,
		ExecutorModel:    opts.ExecutorModelID,
		Summary:          ComputeSummary(results),
		Results:          results,
	}
	return rep
}

func runSingleTask(ctx context.Context, task Task, opts RunOptions) TaskResult {
	res := TaskResult{
		TaskID:   task.ID,
		Language: task.Language,
		Category: task.Category,
	}
	if task.RequireTool != "" {
		if _, err := exec.LookPath(task.RequireTool); err != nil {
			res.Skipped = true
			res.SkipReason = fmt.Sprintf("required tool %q not found on PATH", task.RequireTool)
			return res
		}
	}

	workDir, err := os.MkdirTemp("", "hoxi-agenteval-"+task.ID+"-*")
	if err != nil {
		res.Error = fmt.Sprintf("create temp dir: %v", err)
		return res
	}
	defer os.RemoveAll(workDir)

	if err := Materialize(task, workDir); err != nil {
		res.Error = fmt.Sprintf("materialize task: %v", err)
		return res
	}

	indexDir, err := os.MkdirTemp("", "hoxi-agenteval-idx-*")
	if err != nil {
		res.Error = fmt.Sprintf("create index dir: %v", err)
		return res
	}
	defer os.RemoveAll(indexDir)

	streamClient := opts.StreamClient
	if streamClient == nil && opts.Smoke {
		streamClient = newSmokeStreamClient()
	}
	if streamClient == nil {
		res.Error = "no stream client configured"
		return res
	}

	engine := agent.NewDefaultEngine(streamClient)
	runnerSvc := runner.NewService(workDir, nil, nil, "")
	idxSvc := codeindex.NewService(indexDir)
	sessID := "ses_eval_" + task.ID

	taskCtx, cancel := context.WithTimeout(ctx, opts.TaskTimeout)
	defer cancel()

	var mu sync.Mutex
	sink := agent.EventSinkFunc(func(evt agent.EngineEvent) {
		mu.Lock()
		defer mu.Unlock()
		if evt.Step > res.Steps {
			res.Steps = evt.Step
		}
		switch evt.Kind {
		case agent.EngineToolRunning:
			res.ToolCalls++
			if isEditOrPatchTool(evt.ToolName) {
				res.EditPatchCalls++
			}
			if opts.Verbose {
				fmt.Fprintf(opts.LogWriter, "[%s] step=%d tool=%s %s\n", task.ID, evt.Step, evt.ToolName, summarizeEvalToolCall(evt))
			}
		case agent.EngineToolError:
			res.ToolErrors++
			if isEditOrPatchTool(evt.ToolName) {
				res.EditPatchErrors++
			}
			if opts.Verbose {
				fmt.Fprintf(opts.LogWriter, "[%s] step=%d tool=%s ERROR=%s\n", task.ID, evt.Step, evt.ToolName, truncateLine(evt.ErrorText, 140))
			}
		case agent.EngineToolCompleted:
			if diag, ok := extractDiagnosticsBlock(evt.Text); ok {
				res.DiagnosticsTriggered++
				if isDiagnosticFailure(diag) {
					res.DiagnosticErrorsCaught++
				}
			}
		case agent.EngineTurnUsage, agent.EngineSummaryUsage:
			if evt.Usage != nil {
				res.InputTokens += evt.Usage.Input
				res.OutputTokens += evt.Usage.Output
				res.ReasoningTokens += evt.Usage.Reasoning
				res.CacheReadTokens += evt.Usage.CacheRead
				res.CacheWriteTokens += evt.Usage.CacheWrite
				res.CostUSD += evt.Usage.CostUSD
			}
		}
	})

	var orch *agent.OrchestratorConfig
	if strings.TrimSpace(opts.ExecutorProviderID) != "" && strings.TrimSpace(opts.ExecutorModelID) != "" {
		orch = &agent.OrchestratorConfig{
			ExecutorProviderID: strings.TrimSpace(opts.ExecutorProviderID),
			ExecutorModelID:    strings.TrimSpace(opts.ExecutorModelID),
			ExecutorEffort:     strings.TrimSpace(opts.ExecutorEffort),
		}
	}

	searchHook, inspectHook, impactHook, repoMapHook := buildEvalCodeIndexHooks(idxSvc)
	start := time.Now()
	turnErr := engine.RunTurn(taskCtx, agent.TurnRequest{
		SessionID:    sessID,
		WorktreeDir:  workDir,
		Mode:         "build",
		ProviderID:   opts.ProviderID,
		ModelID:      opts.ModelID,
		Effort:       opts.Effort,
		Orchestrator: orch,
		UserPrompt:   task.Prompt,
		MaxSteps:     opts.MaxSteps,
		CommandRunner: func(c context.Context, dir, command string) (string, int, error) {
			return runEvalShellCommand(c, workDir, dir, command)
		},
		Diagnostics: func(c context.Context, dir, filePath string) string {
			return runnerSvc.RunNativeDiagnostics(c, sessID, dir, filePath)
		},
		RecordFileEdit: func(_ []string) {
			idxSvc.MarkDirty(workDir)
		},
		CodeSearch:    searchHook,
		SymbolInspect: inspectHook,
		ImpactInspect: impactHook,
		RepoMap:       repoMapHook,
		Sink:          sink,
	})
	res.DurationMs = time.Since(start).Milliseconds()
	res.CacheHitRate = computeCacheHitRate(res.InputTokens, res.CacheReadTokens, res.CacheWriteTokens)
	if turnErr != nil {
		res.Error = turnErr.Error()
		return res
	}

	verifyCtx, verifyCancel := context.WithTimeout(ctx, 45*time.Second)
	defer verifyCancel()
	passed, verifyOut, verifyErr := VerifyTask(verifyCtx, task, workDir)
	res.Passed = passed
	if verifyErr != nil {
		res.Error = fmt.Sprintf("verify error: %v", verifyErr)
	} else if !passed {
		res.VerifyOutput = truncateLine(verifyOut, 600)
	}
	return res
}

func isEditOrPatchTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "edit", "multiedit", "write", "apply_patch":
		return true
	default:
		return false
	}
}

func extractDiagnosticsBlock(output string) (string, bool) {
	const marker = "\n\nDiagnostics:\n"
	idx := strings.Index(output, marker)
	if idx < 0 {
		return "", false
	}
	return strings.TrimSpace(output[idx+len(marker):]), true
}

func isDiagnosticFailure(diag string) bool {
	trimmed := strings.TrimSpace(diag)
	if trimmed == "" {
		return false
	}
	for _, benign := range []string{"Passed:", "Not run:", "Not completed:"} {
		if strings.HasPrefix(trimmed, benign) {
			return false
		}
	}
	return true
}

func truncateLine(s string, maxChars int) string {
	clean := strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if len(clean) <= maxChars {
		return clean
	}
	return clean[:maxChars] + "…"
}

func summarizeEvalToolCall(evt agent.EngineEvent) string {
	if t := strings.TrimSpace(evt.Title); t != "" {
		return truncateLine(t, 80)
	}
	for _, k := range []string{"filePath", "path", "command", "pattern", "symbol", "query"} {
		if raw, ok := evt.Input[k].(string); ok && strings.TrimSpace(raw) != "" {
			return k + "=" + truncateLine(raw, 80)
		}
	}
	return ""
}

func runEvalShellCommand(ctx context.Context, rootDir, dir, command string) (string, int, error) {
	targetDir := strings.TrimSpace(dir)
	if targetDir == "" {
		targetDir = rootDir
	}
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return "", 1, err
	}
	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return "", 1, err
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", 1, fmt.Errorf("command directory %q escapes task workspace", targetDir)
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = subproc.CommandContext(ctx, "cmd.exe", "/C", command)
	} else {
		cmd = subproc.CommandContext(ctx, "/bin/sh", "-c", command)
	}
	cmd.Dir = absTarget
	cmd.Env = sanitizedEvalEnv()
	var buf bytes.Buffer
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		return "", 1, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(&buf, io.LimitReader(pr, maxEvalCmdOutputBytes))
		_, _ = io.Copy(io.Discard, pr)
	}()
	waitErr := cmd.Wait()
	_ = pw.Close()
	<-done
	_ = pr.Close()
	out := buf.String()
	if waitErr != nil {
		code := 1
		if ee, ok := waitErr.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		return out, code, nil
	}
	return out, 0, nil
}

func buildEvalCodeIndexHooks(
	idx *codeindex.Service,
) (agent.CodeSearchHook, agent.SymbolInspectHook, agent.ImpactInspectHook, agent.RepoMapHook) {
	searchHook := func(ctx context.Context, dir, query, pathPrefix string, limit int) ([]agent.CodeSearchHit, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		res, err := idx.SearchWithPath(dir, query, pathPrefix, limit)
		if err != nil {
			return nil, err
		}
		hits := make([]agent.CodeSearchHit, len(res.Results))
		for i, r := range res.Results {
			hits[i] = agent.CodeSearchHit{
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
	inspectHook := func(ctx context.Context, dir, relPath, symbol string, includeCallers bool) (string, error) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		insp, err := idx.Inspect(dir, relPath, symbol, 0, 0, 5)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		b.WriteString("Definition: " + insp.Path + ":" + strconv.Itoa(insp.StartLine) + "-" + strconv.Itoa(insp.EndLine) + "\n" + insp.Content)
		if includeCallers {
			if callers, cErr := idx.Callers(dir, symbol, insp.Path, 10); cErr == nil && len(callers.Callers) > 0 {
				b.WriteString("\n\nCallers:\n")
				for _, c := range callers.Callers {
					b.WriteString("- " + c.Path + ":" + strconv.Itoa(c.Line) + ": " + c.Snippet + "\n")
				}
			}
		}
		return strings.TrimSpace(b.String()), nil
	}
	impactHook := func(ctx context.Context, dir, relPath, symbol string, depth, limit int) (string, error) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		imp, err := idx.Impact(dir, symbol, relPath, depth, limit)
		if err != nil {
			return "", err
		}
		return "Symbol: " + imp.Symbol + " (" + imp.Path + ") Affected: " + strings.Join(imp.AffectedFiles, ", "), nil
	}
	repoMapHook := func(ctx context.Context, dir, pathPrefix, query string, limit int) (string, error) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		res, err := idx.Map(dir, pathPrefix, query, limit)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for _, e := range res.Entries {
			b.WriteString(e.Path + "\n")
		}
		return strings.TrimSpace(b.String()), nil
	}
	return searchHook, inspectHook, impactHook, repoMapHook
}

// smokeStreamClient drives the real agent.Engine + real tools + real go vet
// diagnostics + real go test verification offline without external network calls.
type smokeStreamClient struct {
	mu   sync.Mutex
	step int
}

func newSmokeStreamClient() *smokeStreamClient {
	return &smokeStreamClient{}
}

func (c *smokeStreamClient) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	return llm.StreamTarget{
		ProviderID:   providerID,
		ModelID:      modelID,
		BaseURL:      "http://smoke.local",
		Protocol:     llm.ProtocolAnthropic,
		HasEndpoint:  true,
		ContextLimit: 200_000,
		OutputLimit:  8192,
	}
}

func (c *smokeStreamClient) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	c.mu.Lock()
	c.step++
	step := c.step
	c.mu.Unlock()

	var sse string
	switch step {
	case 1:
		sse = buildAnthropicToolSSE(1200, 120, 0, 800, []smokeToolCall{
			{ID: "call_read_1", Name: "read", Input: map[string]any{"filePath": "retry.go"}},
			{ID: "call_read_2", Name: "read", Input: map[string]any{"filePath": "retry_test.go"}},
		})
	case 2:
		// Intentionally introduce a compile error so post-edit go vet diagnostics catch it.
		sse = buildAnthropicToolSSE(300, 140, 1800, 0, []smokeToolCall{
			{
				ID:   "call_edit_bad",
				Name: "edit",
				Input: map[string]any{
					"filePath":  "retry.go",
					"oldString": "\t\tdelay := baseDelay << (attempt - 1)\n\t\ttime.Sleep(delay)",
					"newString": "\t\tdelay := baseDelay << (attempt - 1)\n\t\t_ = undefinedSmokeSymbol\n\t\ttime.Sleep(delay)",
				},
			},
		})
	case 3:
		if !bytes.Contains(req.Body, []byte("Diagnostics:")) || !bytes.Contains(req.Body, []byte("undefinedSmokeSymbol")) {
			return nil, fmt.Errorf("smoke assertion failed: expected post-edit go vet diagnostic in step 3 request body")
		}
		sse = buildAnthropicToolSSE(250, 260, 2100, 0, []smokeToolCall{
			{
				ID:   "call_edit_fix",
				Name: "edit",
				Input: map[string]any{
					"filePath":  "retry.go",
					"oldString": "\t\tdelay := baseDelay << (attempt - 1)\n\t\t_ = undefinedSmokeSymbol\n\t\ttime.Sleep(delay)\n\t}\n\tif lastErr == nil {\n\t\treturn errors.New(\"retry exhausted\")\n\t}\n\treturn lastErr\n}\n\n// ParseRetryAfter parses an HTTP Retry-After header (delta-seconds or HTTP-date).\n// Returns (duration, true) for valid headers; past HTTP-dates clamp to 0.\nfunc ParseRetryAfter(header string, now time.Time) (time.Duration, bool) {\n\tv := strings.TrimSpace(header)\n\tif v == \"\" {\n\t\treturn 0, false\n\t}\n\tif secs, err := strconv.Atoi(v); err == nil {\n\t\treturn time.Duration(secs) * time.Second, true\n\t}\n\tif ts, err := http.ParseTime(v); err == nil {\n\t\treturn ts.Sub(now), true\n\t}\n\treturn 0, false\n}",
					"newString": "\t\tdelay := baseDelay << (attempt - 1)\n\t\ttimer := time.NewTimer(delay)\n\t\tselect {\n\t\tcase <-ctx.Done():\n\t\t\ttimer.Stop()\n\t\t\treturn ctx.Err()\n\t\tcase <-timer.C:\n\t\t}\n\t}\n\tif lastErr == nil {\n\t\treturn errors.New(\"retry exhausted\")\n\t}\n\treturn lastErr\n}\n\n// ParseRetryAfter parses an HTTP Retry-After header (delta-seconds or HTTP-date).\n// Returns (duration, true) for valid headers; past HTTP-dates clamp to 0.\nfunc ParseRetryAfter(header string, now time.Time) (time.Duration, bool) {\n\tv := strings.TrimSpace(header)\n\tif v == \"\" {\n\t\treturn 0, false\n\t}\n\tif secs, err := strconv.Atoi(v); err == nil {\n\t\tif secs < 0 {\n\t\t\treturn 0, false\n\t\t}\n\t\treturn time.Duration(secs) * time.Second, true\n\t}\n\tif ts, err := http.ParseTime(v); err == nil {\n\t\td := ts.Sub(now)\n\t\tif d < 0 {\n\t\t\td = 0\n\t\t}\n\t\treturn d, true\n\t}\n\treturn 0, false\n}",
				},
			},
		})
	case 4:
		sse = buildAnthropicToolSSE(200, 80, 2400, 0, []smokeToolCall{
			{
				ID:    "call_bash_test",
				Name:  "bash",
				Input: map[string]any{"command": "go test -race -count=1 ./..."},
			},
		})
	default:
		sse = buildAnthropicTextSSE(150, 60, 2600, 0, "Fixed context cancellation during backoff and Retry-After validation; all tests pass.")
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}, nil
}

type smokeToolCall struct {
	ID    string
	Name  string
	Input map[string]any
}

func buildAnthropicToolSSE(inTok, outTok, cacheRead, cacheWrite int, calls []smokeToolCall) string {
	var b strings.Builder
	fmt.Fprintf(&b, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_smoke\",\"usage\":{\"input_tokens\":%d,\"cache_read_input_tokens\":%d,\"cache_creation_input_tokens\":%d}}}\n\n",
		inTok, cacheRead, cacheWrite)
	for i, call := range calls {
		rawIn, _ := json.Marshal(call.Input)
		partial, _ := json.Marshal(string(rawIn))
		fmt.Fprintf(&b, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":%d,\"content_block\":{\"type\":\"tool_use\",\"id\":%q,\"name\":%q,\"input\":{}}}\n\n",
			i, call.ID, call.Name)
		fmt.Fprintf(&b, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":%d,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%s}}\n\n",
			i, string(partial))
		fmt.Fprintf(&b, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", i)
	}
	fmt.Fprintf(&b, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":%d}}\n\n", outTok)
	b.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	return b.String()
}

func buildAnthropicTextSSE(inTok, outTok, cacheRead, cacheWrite int, text string) string {
	var b strings.Builder
	quoted, _ := json.Marshal(text)
	fmt.Fprintf(&b, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_smoke_end\",\"usage\":{\"input_tokens\":%d,\"cache_read_input_tokens\":%d,\"cache_creation_input_tokens\":%d}}}\n\n",
		inTok, cacheRead, cacheWrite)
	b.WriteString("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
	fmt.Fprintf(&b, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%s}}\n\n", string(quoted))
	b.WriteString("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
	fmt.Fprintf(&b, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":%d}}\n\n", outTok)
	b.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	return b.String()
}

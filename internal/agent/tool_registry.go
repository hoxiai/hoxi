package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const maxToolOutputBytes = 32 * 1024

// BuiltinToolRegistry manages registered tools and assembles mode-scoped toolsets.
type BuiltinToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func newToolRegistry() *BuiltinToolRegistry {
	r := &BuiltinToolRegistry{tools: make(map[string]Tool)}
	r.Register(&readTool{})
	r.Register(&globTool{})
	r.Register(&grepTool{})
	r.Register(&codeSearchTool{})
	r.Register(&symbolInspectTool{})
	r.Register(&impactAnalysisTool{})
	r.Register(&todoReadTool{})
	r.Register(&todoWriteTool{})
	r.Register(&questionTool{})
	r.Register(&webFetchTool{})
	r.Register(&mcpListToolsTool{})
	r.Register(&mcpCallTool{})
	r.Register(&subtaskTool{})
	r.Register(&writeTool{})
	r.Register(&editTool{})
	r.Register(&multiEditTool{})
	r.Register(&applyPatchTool{})
	r.Register(&bashTool{})
	r.Register(&hoxiBrowserTool{})
	r.Register(&hoxiComputerTool{})
	r.Register(&hoxiImageTool{})
	r.Register(&hoxiToolsGatewayTool{})
	r.Register(&hoxiDataProfileTool{})
	r.Register(&skillLoaderTool{})
	r.Register(&goalReportTool{})
	return r
}

func isAllowedInPlanMode(t Tool) bool {
	if t == nil {
		return false
	}
	if t.IsReadOnly() {
		return true
	}
	switch t.Name() {
	case "todowrite", "question", "goal_report":
		return true
	default:
		return false
	}
}

// Register adds or replaces a tool in the registry.
func (r *BuiltinToolRegistry) Register(t Tool) {
	if r == nil || t == nil {
		return
	}
	r.mu.Lock()
	r.tools[t.Name()] = t
	r.mu.Unlock()
}

// Get returns a registered tool by name.
func (r *BuiltinToolRegistry) Get(name string) (Tool, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	t, ok := r.tools[name]
	r.mu.RUnlock()
	return t, ok
}

// SpecsForMode returns sorted ToolSpec definitions permitted for mode ("plan" = read-only + todo/question).
func (r *BuiltinToolRegistry) SpecsForMode(mode string) []ToolSpec {
	return r.SpecsForTurn(mode, 0)
}

// SpecsForTurn returns sorted ToolSpec definitions filtered by mode and subtask depth.
func (r *BuiltinToolRegistry) SpecsForTurn(mode string, subtaskDepth int) []ToolSpec {
	planMode := strings.EqualFold(strings.TrimSpace(mode), "plan")
	r.mu.RLock()
	names := make([]string, 0, len(r.tools))
	for name, t := range r.tools {
		if planMode && !isAllowedInPlanMode(t) {
			continue
		}
		if subtaskDepth > 0 && (name == "task" || name == "question" || name == "todowrite" || name == "goal_report") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	specs := make([]ToolSpec, 0, len(names))
	for _, name := range names {
		t := r.tools[name]
		specs = append(specs, ToolSpec{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		})
	}
	r.mu.RUnlock()
	return specs
}

// Execute invokes a tool through PermissionHook, panic recovery, and bounded output truncation.
func (r *BuiltinToolRegistry) Execute(
	ctx context.Context,
	tc ToolContext,
	call ToolCall,
	permHook PermissionHook,
) (out ToolOutput, err error) {
	t, ok := r.Get(call.Name)
	if !ok {
		return ToolOutput{Title: call.Name, Output: fmt.Sprintf("unknown tool: %s", call.Name), IsError: true}, fmt.Errorf("unknown tool: %s", call.Name)
	}
	if strings.EqualFold(strings.TrimSpace(tc.Mode), "plan") && !isAllowedInPlanMode(t) {
		return ToolOutput{Title: call.Name, Output: fmt.Sprintf("tool %s is disabled in plan (read-only) mode", call.Name), IsError: true}, fmt.Errorf("tool %s is disabled in plan mode", call.Name)
	}
	if tc.SubtaskDepth > 0 && (call.Name == "task" || call.Name == "question" || call.Name == "todowrite" || call.Name == "goal_report") {
		return ToolOutput{Title: call.Name, Output: fmt.Sprintf("tool %s is disabled inside subagents", call.Name), IsError: true}, fmt.Errorf("tool %s is disabled inside subagents", call.Name)
	}
	if permHook != nil {
		if err := permHook(ctx, PermissionRequest{
			SessionID: tc.SessionID,
			CallID:    call.ID,
			ToolName:  call.Name,
			Input:     call.Input,
			IsRead:    t.IsReadOnly(),
		}); err != nil {
			return ToolOutput{Title: call.Name, Output: err.Error(), IsError: true}, err
		}
		tc.PermissionVetted = true
	}

	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("tool %s panicked: %v", call.Name, rec)
			out = ToolOutput{Title: call.Name, Output: err.Error(), IsError: true}
		}
	}()

	rawInput := call.Input
	if len(rawInput) == 0 {
		rawInput = json.RawMessage("{}")
	}
	out, err = t.Execute(ctx, tc, rawInput)
	maxBytes := maxToolOutputBytes
	if call.Name == "webfetch" {
		maxBytes = maxWebFetchOutputBytes
	}
	out.Output = truncateToolOutput(out.Output, maxBytes)
	if err != nil {
		out.IsError = true
		if out.Output == "" {
			out.Output = err.Error()
		}
	}
	return out, err
}

func resolveWorkspacePath(worktreeDir, relOrAbs string, mustStayInside bool) (string, error) {
	p := strings.TrimSpace(relOrAbs)
	if p == "" {
		return "", fmt.Errorf("path is required")
	}
	base, err := filepath.Abs(worktreeDir)
	if err != nil {
		return "", err
	}
	var target string
	if filepath.IsAbs(p) {
		target = filepath.Clean(p)
	} else {
		target = filepath.Clean(filepath.Join(base, p))
	}
	if mustStayInside {
		if !isLexicalPathWithin(base, target) {
			return "", fmt.Errorf("path %q escapes workspace %q", p, base)
		}
		realBase, okBase := evalAgentRealPath(base, 0)
		realTarget, okTarget := evalAgentRealPath(target, 0)
		if !okBase || !okTarget || !isLexicalPathWithin(realBase, realTarget) {
			return "", fmt.Errorf("path %q resolves outside workspace %q", p, base)
		}
	}
	return target, nil
}

func isLexicalPathWithin(base, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(target))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func isRealPathWithin(base, target string) bool {
	if strings.TrimSpace(base) == "" || strings.TrimSpace(target) == "" {
		return false
	}
	realBase, okBase := evalAgentRealPath(base, 0)
	realTarget, okTarget := evalAgentRealPath(target, 0)
	if !okBase || !okTarget {
		return false
	}
	return isLexicalPathWithin(realBase, realTarget)
}

func evalAgentRealPath(p string, depth int) (string, bool) {
	if depth > 40 {
		return "", false
	}
	cleaned := filepath.Clean(strings.TrimSpace(p))
	if cleaned == "" {
		return "", false
	}
	if real, err := filepath.EvalSymlinks(cleaned); err == nil {
		return real, true
	}
	suffix := ""
	for cur := cleaned; ; {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			if suffix == "" {
				return real, true
			}
			return filepath.Join(real, suffix), true
		}
		info, err := os.Lstat(cur)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				link, lErr := os.Readlink(cur)
				if lErr != nil {
					return "", false
				}
				if !filepath.IsAbs(link) {
					link = filepath.Join(filepath.Dir(cur), link)
				}
				next := link
				if suffix != "" {
					next = filepath.Join(link, suffix)
				}
				return evalAgentRealPath(next, depth+1)
			}
		} else if !os.IsNotExist(err) {
			return "", false
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return cleaned, true
		}
		if suffix == "" {
			suffix = filepath.Base(cur)
		} else {
			suffix = filepath.Join(filepath.Base(cur), suffix)
		}
		cur = parent
	}
}

func truncateToolOutput(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	head := maxBytes * 2 / 3
	tail := maxBytes - head
	omitted := len(s) - head - tail
	return s[:head] + fmt.Sprintf("\n\n... [%d bytes truncated] ...\n\n", omitted) + s[len(s)-tail:]
}

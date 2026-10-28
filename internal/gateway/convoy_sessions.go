package gateway

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/orchestration"
	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/worktree"
)

// convoySessions backs orchestration with the runner, so convoy tasks are
// ordinary Hoxi conversations on the unified session plane.
type convoySessions struct {
	runner *runner.Service
}

func (c convoySessions) CanIsolate(directory string) bool {
	return worktree.HasCommittedHead(directory)
}

func (c convoySessions) Create(ctx context.Context, req orchestration.SessionRequest) (string, error) {
	if c.runner == nil {
		return "", errors.New("runner is unavailable")
	}
	provider := strings.TrimSpace(req.Provider)
	model := strings.TrimSpace(req.Model)
	runtime := "opencode"
	if parentID := strings.TrimSpace(req.ParentSessionID); parentID != "" {
		if parent, ok := c.runner.Store().Get(parentID); ok {
			if parent.Runtime != "" {
				runtime = parent.Runtime
			}
			if provider == "" || provider == "hoxi-auto" {
				provider = strings.TrimSpace(parent.ProviderID)
			}
			if model == "" || req.Provider == "hoxi-auto" {
				model = strings.TrimSpace(parent.ModelID)
			}
		}
	}
	if provider == "hoxi-auto" {
		provider = ""
		model = ""
	}
	body := runner.CreateSessionRequest{
		Title:       req.Title,
		Directory:   req.Directory,
		Runtime:     runtime,
		Mode:        req.Mode,
		ProviderID:  provider,
		ModelID:     model,
		SandboxMode: "local",
	}
	if runner.IsValidRuntime(provider) {
		// Native runtimes are addressed by their runtime ID as the provider.
		body.Runtime = provider
	}
	if req.Isolated {
		body.SandboxMode = "worktree"
	}
	info, _, err := c.runner.CreateConversation(ctx, body, req.Directory)
	if err != nil {
		return "", err
	}
	return info.ID, nil
}

func (c convoySessions) Run(ctx context.Context, sessionID, prompt string, req orchestration.PlanRequest) (string, error) {
	turn := runner.PromptRequest{Text: prompt, Agent: strings.TrimSpace(req.Mode)}
	provider := strings.TrimSpace(req.Provider)
	model := strings.TrimSpace(req.Model)
	if (provider == "" || provider == "hoxi-auto" || model == "") && strings.TrimSpace(req.ParentSessionID) != "" {
		if parent, ok := c.runner.Store().Get(strings.TrimSpace(req.ParentSessionID)); ok {
			if provider == "" || provider == "hoxi-auto" {
				provider = strings.TrimSpace(parent.ProviderID)
			}
			if model == "" || req.Provider == "hoxi-auto" {
				model = strings.TrimSpace(parent.ModelID)
			}
		}
	}
	if provider != "" && provider != "hoxi-auto" && model != "" {
		turn.Model = &runner.PromptModel{ProviderID: provider, ModelID: model}
	}
	return c.runner.RunPromptAndWait(ctx, sessionID, turn)
}

func sessionProjectRoot(rec runner.SessionRecord) string {
	if root := hoxipaths.ProjectRootForWorktree(rec.WorktreePath); root != "" {
		return filepath.Clean(root)
	}
	if dir := strings.TrimSpace(rec.ProjectDir); dir != "" {
		return filepath.Clean(dir)
	}
	return filepath.Clean(strings.TrimSpace(rec.WorktreePath))
}

func (c convoySessions) Merge(targetSessionID string, sourceSessionIDs []string) error {
	target, ok := c.runner.Store().Get(targetSessionID)
	if !ok {
		return errors.New("convoy session not found: " + targetSessionID)
	}
	targetRoot := sessionProjectRoot(target)
	sources := make([]string, 0, len(sourceSessionIDs))
	for _, id := range sourceSessionIDs {
		rec, ok := c.runner.Store().Get(id)
		if !ok {
			return errors.New("convoy session not found: " + id)
		}
		if sessionProjectRoot(rec) != targetRoot {
			return errors.New("convoy session belongs to a different project: " + id)
		}
		sources = append(sources, rec.WorktreePath)
	}
	return worktree.MergeWorkingState(target.WorktreePath, sources...)
}

func (c convoySessions) Abort(sessionID string) {
	c.runner.AbortSession(sessionID)
}

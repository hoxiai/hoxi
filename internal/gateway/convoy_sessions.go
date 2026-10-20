package gateway

import (
	"context"
	"errors"
	"strings"

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
	body := runner.CreateSessionRequest{
		Title:       req.Title,
		Directory:   req.Directory,
		Runtime:     "opencode",
		Mode:        req.Mode,
		ProviderID:  req.Provider,
		ModelID:     req.Model,
		SandboxMode: "local",
	}
	if runner.IsValidRuntime(req.Provider) {
		// Native runtimes are addressed by their runtime ID as the provider.
		body.Runtime = req.Provider
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
	if req.Provider != "" && req.Model != "" {
		turn.Model = &runner.PromptModel{ProviderID: req.Provider, ModelID: req.Model}
	}
	return c.runner.RunPromptAndWait(ctx, sessionID, turn)
}

func (c convoySessions) Merge(targetSessionID string, sourceSessionIDs []string) error {
	target, ok := c.runner.Store().Get(targetSessionID)
	if !ok {
		return errors.New("convoy session not found: " + targetSessionID)
	}
	sources := make([]string, 0, len(sourceSessionIDs))
	for _, id := range sourceSessionIDs {
		rec, ok := c.runner.Store().Get(id)
		if !ok {
			return errors.New("convoy session not found: " + id)
		}
		sources = append(sources, rec.WorktreePath)
	}
	return worktree.MergeWorkingState(target.WorktreePath, sources...)
}

func (c convoySessions) Abort(sessionID string) {
	c.runner.AbortSession(sessionID)
}

package gateway

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/hoxiai/hoxi/internal/devpulse"
	"github.com/hoxiai/hoxi/internal/handoff"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/worktree"
)

const historyMaxDays = devpulse.HistoryMaxDays

var isGeneratedOrLockFile = devpulse.IsGeneratedOrLockFile

type (
	dailyModelStat          = devpulse.DailyModelStat
	gitDailyPulseResponse   = devpulse.DailyPulseResponse
	historyModelStat        = devpulse.HistoryModelStat
	historyDay              = devpulse.HistoryDay
	devPulseHistoryResponse = devpulse.HistoryResponse
)

type gatewayDevPulseSource struct {
	g *Gateway
}

func (s *gatewayDevPulseSource) SessionDirectories(sessionID string) (workDir, projectDir string) {
	if s.g == nil {
		return "", ""
	}
	return s.g.sessionPulseDirectories(sessionID)
}

func (s *gatewayDevPulseSource) IsDismissedDirectory(dir string) bool {
	if s.g == nil || s.g.workspaces == nil {
		return false
	}
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return false
	}
	if s.g.workspaces.IsDismissed(trimmed) {
		return true
	}
	if root := hoxipaths.ProjectRootForWorktree(trimmed); root != "" && s.g.workspaces.IsDismissed(root) {
		return true
	}
	return false
}

func (s *gatewayDevPulseSource) CandidateDirectories() []string {
	if s.g == nil {
		return nil
	}
	var dirs []string
	if s.g.rootDir != "" && !s.IsDismissedDirectory(s.g.rootDir) {
		dirs = append(dirs, s.g.rootDir)
	}
	if s.g.worktrees != nil {
		if root := s.g.worktrees.RootDir(); root != "" && !s.IsDismissedDirectory(root) {
			dirs = append(dirs, root)
		}
	}
	if s.g.workspaces != nil {
		dirs = append(dirs, s.g.workspaces.AllProjectRoots()...)
	}
	if s.g.runner != nil {
		store := s.g.runner.Store()
		store.EachMeta(func(rec runner.SessionRecord) {
			if rec.ProjectDir != "" && !s.IsDismissedDirectory(rec.ProjectDir) {
				dirs = append(dirs, rec.ProjectDir)
			}
			if rec.WorktreePath != "" && !s.IsDismissedDirectory(rec.WorktreePath) {
				dirs = append(dirs, rec.WorktreePath)
			}
		})
	}
	return dirs
}

func (s *gatewayDevPulseSource) SourceKey() devpulse.SourceKey {
	if s.g == nil {
		return devpulse.SourceKey{}
	}
	k := s.g.requestLogSourceKey()
	return devpulse.SourceKey{
		DBSize:    k.dbSize,
		DBMod:     k.dbMod,
		WALSize:   k.walSize,
		WALMod:    k.walMod,
		RunnerRev: k.runnerRev,
	}
}

func (s *gatewayDevPulseSource) VisitSessions(minUpdatedMs int64, fn func(sessionID string, visitTurns func(func(devpulse.TurnUsage)))) error {
	if s.g == nil || s.g.runner == nil {
		return nil
	}
	store := s.g.runner.Store()
	var readErr error
	store.EachMeta(func(rec runner.SessionRecord) {
		if readErr != nil || rec.UpdatedAt < minUpdatedMs || rec.Subagent {
			return
		}
		fn(rec.ID, func(yield func(devpulse.TurnUsage)) {
			readErr = store.VisitUsageTurns(rec.ID, func(full runner.SessionRecord, turns []runner.TurnUsageSummary) {
				for i := range turns {
					if isImportedRunnerTurn(turns[i].ID, turns[i].Imported, full) {
						continue
					}
					yield(usageSummaryToPulseUsage(turns[i], full))
				}
			})
		})
	})
	return readErr
}

func (s *gatewayDevPulseSource) NotImportedTurnSQLClause(titleCol, dataCol, rowTimeCol, sessTimeCol string) string {
	return handoff.SQLNotImportedTurnClause(titleCol, dataCol, rowTimeCol, sessTimeCol)
}

func (s *gatewayDevPulseSource) RunGit(workDir string, args ...string) (string, error) {
	return worktree.RunGit(workDir, args...)
}

func (s *gatewayDevPulseSource) RunGitContext(ctx context.Context, workDir string, args ...string) (string, error) {
	return worktree.RunGitContext(ctx, workDir, args...)
}

func (s *gatewayDevPulseSource) RunGitRaw(workDir string, args ...string) (string, error) {
	return worktree.RunGitRaw(workDir, args...)
}

func (s *gatewayDevPulseSource) RunGitRawContext(ctx context.Context, workDir string, args ...string) (string, error) {
	return worktree.RunGitRawContext(ctx, workDir, args...)
}

func (s *gatewayDevPulseSource) ParseBranchFromPorcelainHeader(headerLine string) string {
	return worktree.ParseBranchFromPorcelainHeader(headerLine)
}

func usageSummaryToPulseUsage(turn runner.TurnUsageSummary, full runner.SessionRecord) devpulse.TurnUsage {
	providerID, isNative := nativeTurnProviderParts(turn.ID, turn.ProviderID, full)
	u := devpulse.TurnUsage{
		ID:              turn.ID,
		Role:            turn.Role,
		Created:         turn.Created,
		Completed:       turn.Completed,
		ProviderID:      providerID,
		ModelID:         firstNonEmptyString(turn.ModelID, full.ModelID, "unknown"),
		IsNative:        isNative,
		Cost:            turn.Cost,
		ToolCallsTotal:  turn.ToolCallsTotal,
		ToolCallsFailed: turn.ToolCallsFailed,
	}
	if !isNative {
		u.ToolCallsTotal = 0
		u.ToolCallsFailed = 0
	}
	if tokens := turn.Tokens; tokens != nil {
		if tokens.Input > 0 || tokens.Output > 0 || tokens.Reasoning > 0 || tokens.Cache.Read > 0 || tokens.Cache.Write > 0 {
			u.HasTokens = true
			u.InputTokens = accountingInputTokens(tokens, full.Runtime, turn.ProviderID)
			u.OutputTokens = int64(tokens.Output)
			u.ReasoningTokens = int64(tokens.Reasoning)
			u.CacheReadTokens = int64(tokens.Cache.Read)
			u.CacheWriteTokens = int64(tokens.Cache.Write)
		}
	}
	return u
}

// Before v0.7.5 Codex stored inclusive input, and Total was exactly Input +
// Output. New Codex records include cache reads in Total as a separate bucket.
// Correct only the unambiguous old shape, including in precomputed sidecars.
func accountingInputTokens(t *runner.MessageTokens, runtime, provider string) int64 {
	if (provider == string(runner.RuntimeCodex) || runtime == string(runner.RuntimeCodex)) &&
		t.Cache.Read > 0 && t.Input >= t.Cache.Read && t.Output >= 0 && t.Reasoning == 0 &&
		t.Cache.Write == 0 && t.Total > 0 && t.Total-t.Input == t.Output {
		return int64(t.Input - t.Cache.Read)
	}
	return int64(t.Input)
}

func (g *Gateway) ensureDevPulse() *devpulse.Service {
	g.devPulseOnce.Do(func() {
		if g.devPulse == nil {
			g.devPulse = devpulse.NewService(&gatewayDevPulseSource{g: g})
		}
	})
	return g.devPulse
}

func (g *Gateway) handleDevPulse(w http.ResponseWriter, r *http.Request) {
	g.ensureDevPulse().HandleDailyPulse(w, r)
}

func (g *Gateway) handleDevPulseHistory(w http.ResponseWriter, r *http.Request) {
	g.ensureDevPulse().HandleHistory(w, r)
}

func (g *Gateway) collectRunnerDailyUsage(startOfTodayMs int64, res *gitDailyPulseResponse) {
	g.ensureDevPulse().CollectRunnerDailyUsage(startOfTodayMs, res)
}

// sessionPulseDirectory returns the directory a session actually works in, or
// "" when the session is unknown or its directory is unavailable.
func (g *Gateway) sessionPulseDirectory(sessionID string) string {
	dir, _ := g.sessionPulseDirectories(sessionID)
	return dir
}

func (g *Gateway) sessionPulseDirectories(sessionID string) (workDir, projectDir string) {
	if sessionID == "" {
		return "", ""
	}
	if g.runner != nil {
		if rec, ok := g.runner.Store().Meta(sessionID); ok {
			proj := strings.TrimSpace(rec.ProjectDir)
			if proj != "" {
				if info, err := os.Stat(proj); err == nil && info.IsDir() {
					projectDir = proj
				}
			}
			dir := strings.TrimSpace(rec.WorktreePath)
			if dir == "" {
				dir = proj
			}
			if dir != "" {
				if info, err := os.Stat(dir); err == nil && info.IsDir() {
					return dir, projectDir
				}
				return projectDir, projectDir
			}
		}
	}
	if g.worktrees != nil {
		if b, ok, err := g.worktrees.BindingForSession(sessionID); err == nil && ok && b.Directory != "" {
			if info, err := os.Stat(b.Directory); err == nil && info.IsDir() {
				return b.Directory, hoxipaths.ProjectRootForWorktree(b.Directory)
			}
		}
	}
	return "", ""
}

// isImportedRunnerTurn reports whether a turn in the runner session store came
// from external CLI handoff materialization rather than being executed inside
// Hoxi. Newly materialized turns carry Imported=true; pre-fix sessions are
// identified by their handoff title prefix and non-runner turn ID.
func isImportedRunnerTurn(turnID string, imported bool, rec runner.SessionRecord) bool {
	if imported {
		return true
	}
	return handoff.IsImportedSessionTitle(rec.Title) && !strings.HasPrefix(strings.TrimSpace(turnID), "msg_a_")
}

// nativeTurnProvider reports whether turn was executed by a native CLI runtime
// (as opposed to OpenCode, whose turns already live in hoxi.db) and returns its
// runtime/provider identifier.
func nativeTurnProvider(turn runner.MessageTurn, rec runner.SessionRecord) (string, bool) {
	return nativeTurnProviderParts(turn.Info.ID, turn.Info.ProviderID, rec)
}

func nativeTurnProviderParts(turnID, turnProviderID string, rec runner.SessionRecord) (string, bool) {
	prov := strings.TrimSpace(turnProviderID)
	rt := strings.TrimSpace(rec.Runtime)
	if runner.IsValidRuntime(prov) {
		return prov, true
	}
	if strings.HasPrefix(turnID, "msg_a_") {
		if prov != "" && prov != "opencode" {
			return prov, true
		}
		if runner.IsValidRuntime(rt) {
			return rt, true
		}
		if p := strings.TrimSpace(rec.ProviderID); runner.IsValidRuntime(p) {
			return p, true
		}
		return firstNonEmptyString(prov, "native"), true
	}
	if len(rec.OpenCodeIDs) == 0 && runner.IsValidRuntime(rt) {
		return firstNonEmptyString(prov, rt), true
	}
	return "", false
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

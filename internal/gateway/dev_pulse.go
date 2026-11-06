package gateway

import (
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

func (s *gatewayDevPulseSource) CandidateDirectories() []string {
	if s.g == nil {
		return nil
	}
	var dirs []string
	if s.g.rootDir != "" {
		dirs = append(dirs, s.g.rootDir)
	}
	if s.g.worktrees != nil {
		if root := s.g.worktrees.RootDir(); root != "" {
			dirs = append(dirs, root)
		}
	}
	if s.g.workspaces != nil {
		dirs = append(dirs, s.g.workspaces.AllProjectRoots()...)
	}
	if s.g.runner != nil {
		store := s.g.runner.Store()
		for _, info := range store.List("") {
			if info.Directory != "" {
				dirs = append(dirs, info.Directory)
			}
			if rec, ok := store.Meta(info.ID); ok {
				if rec.ProjectDir != "" {
					dirs = append(dirs, rec.ProjectDir)
				}
				if rec.WorktreePath != "" {
					dirs = append(dirs, rec.WorktreePath)
				}
			}
		}
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

func (s *gatewayDevPulseSource) VisitSessions(minUpdatedMs int64, fn func(sessionID string, visitTurns func(func(devpulse.TurnUsage)))) {
	if s.g == nil || s.g.runner == nil {
		return
	}
	store := s.g.runner.Store()
	for _, info := range store.List("") {
		if info.Time.Updated < minUpdatedMs {
			continue
		}
		rec, ok := store.Meta(info.ID)
		if !ok || rec.Subagent {
			continue
		}
		fn(rec.ID, func(yield func(devpulse.TurnUsage)) {
			store.VisitTurnsSummary(rec.ID, func(full runner.SessionRecord) {
				for i := range full.Turns {
					yield(turnToPulseUsage(full.Turns[i], full))
				}
			})
		})
	}
}

func (s *gatewayDevPulseSource) NotImportedTurnSQLClause(titleCol, dataCol, rowTimeCol, sessTimeCol string) string {
	return handoff.SQLNotImportedTurnClause(titleCol, dataCol, rowTimeCol, sessTimeCol)
}

func (s *gatewayDevPulseSource) RunGit(workDir string, args ...string) (string, error) {
	return worktree.RunGit(workDir, args...)
}

func (s *gatewayDevPulseSource) RunGitRaw(workDir string, args ...string) (string, error) {
	return worktree.RunGitRaw(workDir, args...)
}

func (s *gatewayDevPulseSource) ParseBranchFromPorcelainHeader(headerLine string) string {
	return worktree.ParseBranchFromPorcelainHeader(headerLine)
}

func turnToPulseUsage(turn runner.MessageTurn, full runner.SessionRecord) devpulse.TurnUsage {
	providerID, isNative := nativeTurnProvider(turn, full)
	u := devpulse.TurnUsage{
		Role:       turn.Info.Role,
		Created:    turn.Info.Time.Created,
		Completed:  turn.Info.Time.Completed,
		ProviderID: providerID,
		ModelID:    firstNonEmptyString(turn.Info.ModelID, full.ModelID, "unknown"),
		IsNative:   isNative,
		Cost:       turn.Info.Cost,
	}
	if isNative {
		for _, part := range turn.Parts {
			if part.Type != "tool" {
				continue
			}
			u.ToolCallsTotal++
			if part.State != nil && part.State.Status == "error" {
				u.ToolCallsFailed++
			}
		}
	}
	if tokens := turn.Info.Tokens; tokens != nil {
		if tokens.Input > 0 || tokens.Output > 0 || tokens.Reasoning > 0 || tokens.Cache.Read > 0 || tokens.Cache.Write > 0 {
			u.HasTokens = true
			u.InputTokens = int64(tokens.Input)
			u.OutputTokens = int64(tokens.Output)
			u.ReasoningTokens = int64(tokens.Reasoning)
			u.CacheReadTokens = int64(tokens.Cache.Read)
			u.CacheWriteTokens = int64(tokens.Cache.Write)
		}
	}
	return u
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

// nativeTurnProvider reports whether turn was executed by a native CLI runtime
// (as opposed to OpenCode, whose turns already live in hoxi.db) and returns its
// runtime/provider identifier.
func nativeTurnProvider(turn runner.MessageTurn, rec runner.SessionRecord) (string, bool) {
	prov := strings.TrimSpace(turn.Info.ProviderID)
	rt := strings.TrimSpace(rec.Runtime)
	if runner.IsValidRuntime(prov) {
		return prov, true
	}
	if strings.HasPrefix(turn.Info.ID, "msg_a_") {
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

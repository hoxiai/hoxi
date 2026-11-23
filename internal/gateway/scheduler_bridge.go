package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/scheduler"
)

type gatewaySchedulerHost struct {
	g *Gateway
}

func newGatewaySchedulerService(g *Gateway) *scheduler.Service {
	svc := scheduler.NewService(
		hoxipaths.SchedulesPath(),
		hoxipaths.ScheduleRunsDir(),
		&gatewaySchedulerHost{g: g},
	)
	if g.runner != nil {
		g.runner.SetScheduledTurnChecker(svc.IsSessionRunningScheduledTurn)
	}
	return svc
}

// StartScheduler starts the background schedule execution loop until ctx is cancelled.
func (g *Gateway) StartScheduler(ctx context.Context) {
	if g == nil || g.scheduler == nil {
		return
	}
	g.scheduler.Start(ctx)
}

// StopScheduler stops the background schedule loop and cancels in-flight scheduled turns.
func (g *Gateway) StopScheduler() {
	if g == nil || g.scheduler == nil {
		return
	}
	g.scheduler.Stop()
}

// SchedulerService exposes the schedule service for tests and extensions.
func (g *Gateway) SchedulerService() *scheduler.Service {
	if g == nil {
		return nil
	}
	return g.scheduler
}

func (h *gatewaySchedulerHost) ResolveWorkspaceDirectory(projectID, directory string) (string, string, error) {
	g := h.g
	projID := strings.TrimSpace(projectID)
	dir := strings.TrimSpace(directory)

	if dir == "" && projID != "" && g.workspaces != nil {
		if roots := g.workspaces.ProjectRoots(projID); len(roots) > 0 {
			dir = roots[0]
		}
	}
	if dir == "" {
		return "", "", errors.New("workspace directory is required")
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", "", err
	}
	stat, err := os.Stat(absDir)
	if err != nil || !stat.IsDir() {
		return "", "", fmt.Errorf("workspace directory does not exist: %s", absDir)
	}
	if g.workspaces != nil {
		if canonical, ok := g.workspaces.ProjectIDForDirectory(absDir); ok {
			projID = canonical
		} else if projID == "" {
			if ensured, eErr := g.workspaces.EnsureProjectForDirectory(absDir); eErr == nil {
				projID = ensured
			}
		}
	}
	return projID, filepath.Clean(absDir), nil
}

func (h *gatewaySchedulerHost) SessionExists(sessionID string) (bool, string) {
	if h.g == nil || h.g.runner == nil || h.g.runner.Store() == nil {
		return false, ""
	}
	rec, ok := h.g.runner.Store().Meta(strings.TrimSpace(sessionID))
	if !ok {
		return false, ""
	}
	dir := strings.TrimSpace(rec.ProjectDir)
	if dir == "" {
		dir = strings.TrimSpace(rec.WorktreePath)
	}
	return true, dir
}

func (h *gatewaySchedulerHost) IsSessionBusy(sessionID string) bool {
	if h.g == nil || h.g.runner == nil {
		return false
	}
	return h.g.runner.IsSessionBusy(strings.TrimSpace(sessionID))
}

func (h *gatewaySchedulerHost) CreateSession(ctx context.Context, req scheduler.CreateSessionInput) (string, error) {
	if h.g == nil || h.g.runner == nil {
		return "", errors.New("runner is unavailable")
	}
	provider := strings.TrimSpace(req.ProviderID)
	model := strings.TrimSpace(req.ModelID)
	rt := strings.TrimSpace(req.Runtime)
	if rt == "" {
		rt = string(runner.RuntimeNative)
	}
	if runner.IsValidRuntime(provider) {
		rt = provider
	}
	info, _, err := h.g.runner.CreateConversation(ctx, runner.CreateSessionRequest{
		Title:          req.Title,
		Directory:      req.Directory,
		ScheduleID:     req.ScheduleID,
		Runtime:        rt,
		Mode:           req.Mode,
		ProviderID:     provider,
		ModelID:        model,
		SandboxMode:    req.SandboxMode,
		CommandSandbox: req.CommandSandbox,
	}, req.Directory)
	if err != nil {
		return "", err
	}
	return info.ID, nil
}

func (h *gatewaySchedulerHost) ResolveSkill(ctx context.Context, directory, sessionID, runtime string, ref scheduler.SkillRef) (scheduler.SkillRef, error) {
	if h.g == nil || h.g.skills == nil {
		if strings.TrimSpace(ref.Name) == "" {
			return scheduler.SkillRef{}, errors.New("skill name is required")
		}
		return ref, nil
	}
	rt := strings.TrimSpace(runtime)
	if rt == "" {
		rt = string(runner.RuntimeNative)
	}
	desc, err := h.g.skills.ResolveByNameOrKey(ctx, directory, sessionID, rt, ref.Key, ref.Name)
	if err != nil {
		return scheduler.SkillRef{}, err
	}
	return scheduler.SkillRef{
		Key:     desc.Key,
		Name:    desc.Name,
		Runtime: firstNonEmptyGateway(desc.Runtime, rt),
	}, nil
}

func isVirtualScheduleProvider(prov string) bool {
	switch strings.ToLower(strings.TrimSpace(prov)) {
	case "", "auto", "hoxi-auto", "hoxi-orchestrator":
		return true
	default:
		return false
	}
}

func firstNonEmptyGateway(vals ...string) string {
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

func (h *gatewaySchedulerHost) RunTurn(ctx context.Context, sessionID string, req scheduler.RunTurnInput) (string, error) {
	if h.g == nil || h.g.runner == nil {
		return "", errors.New("runner is unavailable")
	}
	turn := runner.PromptRequest{
		Text:    req.Prompt,
		Agent:   strings.TrimSpace(req.Mode),
		Variant: strings.TrimSpace(req.Variant),
		System:  req.System,
		Runtime: strings.TrimSpace(req.Runtime),
	}
	if prov := strings.TrimSpace(req.ProviderID); prov != "" && !isVirtualScheduleProvider(prov) && strings.TrimSpace(req.ModelID) != "" {
		turn.Model = &runner.PromptModel{
			ProviderID: prov,
			ModelID:    strings.TrimSpace(req.ModelID),
		}
	}
	if req.SkillSelection != nil && (strings.TrimSpace(req.SkillSelection.Name) != "" || strings.TrimSpace(req.SkillSelection.Key) != "") {
		resolved := *req.SkillSelection
		if !strings.HasPrefix(strings.TrimSpace(resolved.Key), "skl_") || strings.TrimSpace(resolved.Name) == "" {
			sessDir := ""
			if h.g.runner.Store() != nil {
				if rec, ok := h.g.runner.Store().Meta(sessionID); ok {
					sessDir = firstNonEmptyGateway(rec.WorktreePath, rec.ProjectDir)
				}
			}
			if sessDir != "" {
				canonical, err := h.ResolveSkill(ctx, sessDir, sessionID, req.Runtime, resolved)
				if err != nil {
					return "", err
				}
				resolved = canonical
			}
		}
		turn.SkillSelection = &runner.SkillSelection{
			Key:     strings.TrimSpace(resolved.Key),
			Name:    strings.TrimSpace(resolved.Name),
			Runtime: strings.TrimSpace(resolved.Runtime),
		}
	}
	return h.g.runner.RunPromptAndWait(ctx, sessionID, turn)
}

func (h *gatewaySchedulerHost) AbortSession(sessionID string) {
	if h.g == nil || h.g.runner == nil {
		return
	}
	h.g.runner.AbortSession(sessionID)
}

func (h *gatewaySchedulerHost) Notify(title, body, sessionID string) {
	if h.g == nil {
		return
	}
	h.g.notifyMu.RLock()
	notifyFn := h.g.onDesktopNotify
	h.g.notifyMu.RUnlock()
	if notifyFn != nil {
		_ = notifyFn(DesktopNotification{
			Title:     strings.TrimSpace(title),
			Body:      strings.TrimSpace(body),
			SessionID: strings.TrimSpace(sessionID),
		})
	}
}

func (h *gatewaySchedulerHost) BroadcastUpdated(scheduleID string) {
	if h.g == nil || h.g.runner == nil {
		return
	}
	h.g.runner.BroadcastScheduleUpdated(scheduleID)
}

// invokeNativeScheduleTool handles in-conversation hoxi_schedule tool calls.
// Approval gating has already run in runner.nativePermissionHook before this runs.
func (g *Gateway) invokeNativeScheduleTool(
	_ context.Context,
	tc agent.ToolContext,
	input json.RawMessage,
) (agent.HostToolResult, error) {
	if g == nil || g.scheduler == nil {
		return agent.HostToolResult{Title: "hoxi_schedule"}, errors.New("scheduler service is not available")
	}
	var req struct {
		Action             string  `json:"action"`
		ID                 string  `json:"id"`
		Name               string  `json:"name"`
		Prompt             string  `json:"prompt"`
		Cron               string  `json:"cron"`
		IntervalMinutes    int     `json:"intervalMinutes"`
		Timezone           string  `json:"timezone"`
		Misfire            string  `json:"misfire"`
		BindCurrentSession *bool   `json:"bindCurrentSession"`
		SessionID          *string `json:"sessionID"`
		Mode               string  `json:"mode"`
		SandboxMode        string  `json:"sandboxMode"`
		CommandSandbox     *bool   `json:"commandSandbox"`
		SkillName          string  `json:"skillName"`
		SkillKey           string  `json:"skillKey"`
		ProviderID         string  `json:"providerID"`
		ModelID            string  `json:"modelID"`
		NotifyOn           string  `json:"notifyOn"`
		InjectLastSummary  *bool   `json:"injectLastSummary"`
		TimeoutSeconds     int     `json:"timeoutSeconds"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return agent.HostToolResult{Title: "hoxi_schedule"}, fmt.Errorf("invalid hoxi_schedule arguments: %w", err)
	}
	act := strings.ToLower(strings.TrimSpace(req.Action))

	// Resolve workspace root & session defaults from the active session.
	var sessRec runner.SessionRecord
	var hasSess bool
	if g.runner != nil && g.runner.Store() != nil && strings.TrimSpace(tc.SessionID) != "" {
		sessRec, hasSess = g.runner.Store().Meta(tc.SessionID)
	}
	workspaceDir := strings.TrimSpace(tc.WorktreeDir)
	if hasSess && strings.TrimSpace(sessRec.ProjectDir) != "" {
		workspaceDir = sessRec.ProjectDir
	}
	projectID := ""
	if hasSess {
		projectID = strings.TrimSpace(sessRec.ProjectID)
	}

	// Defense in depth against recursive schedule mutation inside scheduled turns.
	if act != "list" && act != "get" {
		if (hasSess && strings.TrimSpace(sessRec.ScheduleID) != "") || g.scheduler.IsSessionRunningScheduledTurn(tc.SessionID) {
			return agent.HostToolResult{Title: "hoxi_schedule"}, errors.New("scheduled runs cannot create, modify, or delete scheduled tasks")
		}
	}

	switch act {
	case "list":
		items := g.scheduler.List(projectID, workspaceDir)
		raw, _ := json.MarshalIndent(items, "", "  ")
		return agent.HostToolResult{
			Title:  fmt.Sprintf("Schedules (%d)", len(items)),
			Output: string(raw),
		}, nil

	case "get":
		id := strings.TrimSpace(req.ID)
		if id == "" {
			return agent.HostToolResult{Title: "hoxi_schedule get"}, errors.New("id is required")
		}
		sch, ok := g.scheduler.Get(id)
		if !ok {
			return agent.HostToolResult{Title: "hoxi_schedule get"}, scheduler.ErrScheduleNotFound
		}
		runs, _ := g.scheduler.History(id)
		raw, _ := json.MarshalIndent(map[string]any{
			"schedule": sch,
			"runs":     runs,
		}, "", "  ")
		return agent.HostToolResult{
			Title:  "Schedule: " + sch.Name,
			Output: string(raw),
		}, nil

	case "create":
		targetSessionID := ""
		if req.BindCurrentSession != nil && *req.BindCurrentSession {
			targetSessionID = strings.TrimSpace(tc.SessionID)
		} else if req.SessionID != nil {
			targetSessionID = strings.TrimSpace(*req.SessionID)
		}
		prov := strings.TrimSpace(req.ProviderID)
		mod := strings.TrimSpace(req.ModelID)
		rt := ""
		if hasSess {
			rt = strings.TrimSpace(sessRec.Runtime)
			sessProv := strings.TrimSpace(sessRec.ProviderID)
			if prov == "" && !isVirtualScheduleProvider(sessProv) {
				prov = sessProv
			}
			if mod == "" && !isVirtualScheduleProvider(sessProv) {
				mod = strings.TrimSpace(sessRec.ModelID)
			}
		}
		if rt == "" {
			rt = string(runner.RuntimeNative)
		}
		var skill *scheduler.SkillRef
		if strings.TrimSpace(req.SkillName) != "" || strings.TrimSpace(req.SkillKey) != "" {
			skill = &scheduler.SkillRef{
				Key:     strings.TrimSpace(req.SkillKey),
				Name:    strings.TrimSpace(req.SkillName),
				Runtime: rt,
			}
		}
		cmdSandbox := false
		if req.CommandSandbox != nil {
			cmdSandbox = *req.CommandSandbox
		}
		trig := scheduler.TriggerSpec{
			Cron:            strings.TrimSpace(req.Cron),
			IntervalMinutes: req.IntervalMinutes,
			Timezone:        strings.TrimSpace(req.Timezone),
			Misfire:         strings.TrimSpace(req.Misfire),
		}
		if trig.Cron != "" {
			trig.Kind = scheduler.TriggerKindCron
		} else if trig.IntervalMinutes > 0 {
			trig.Kind = scheduler.TriggerKindInterval
		}
		created, err := g.scheduler.Create(scheduler.UpsertInput{
			Name:      strings.TrimSpace(req.Name),
			ProjectID: projectID,
			Directory: workspaceDir,
			SessionID: targetSessionID,
			Trigger:   trig,
			Execution: scheduler.ExecutionSpec{
				Mode:           strings.TrimSpace(req.Mode),
				SandboxMode:    strings.TrimSpace(req.SandboxMode),
				CommandSandbox: cmdSandbox,
				Runtime:        rt,
				ProviderID:     prov,
				ModelID:        mod,
				TimeoutSeconds: req.TimeoutSeconds,
			},
			SkillSelection:    skill,
			Prompt:            strings.TrimSpace(req.Prompt),
			InjectLastSummary: req.InjectLastSummary,
			NotifyOn:          strings.TrimSpace(req.NotifyOn),
		})
		if err != nil {
			return agent.HostToolResult{Title: "hoxi_schedule create"}, err
		}
		raw, _ := json.MarshalIndent(created, "", "  ")
		return agent.HostToolResult{
			Title:  "Scheduled: " + created.Name,
			Output: string(raw),
		}, nil

	case "pause", "resume":
		id := strings.TrimSpace(req.ID)
		if id == "" {
			return agent.HostToolResult{Title: "hoxi_schedule " + act}, errors.New("id is required")
		}
		enabled := act == "resume"
		updated, err := g.scheduler.Update(id, scheduler.PatchInput{Enabled: &enabled})
		if err != nil {
			return agent.HostToolResult{Title: "hoxi_schedule " + act}, err
		}
		raw, _ := json.MarshalIndent(updated, "", "  ")
		return agent.HostToolResult{
			Title:  fmt.Sprintf("Schedule %sd: %s", act, updated.Name),
			Output: string(raw),
		}, nil

	case "update":
		id := strings.TrimSpace(req.ID)
		if id == "" {
			return agent.HostToolResult{Title: "hoxi_schedule update"}, errors.New("id is required")
		}
		existing, ok := g.scheduler.Get(id)
		if !ok {
			return agent.HostToolResult{Title: "hoxi_schedule update"}, scheduler.ErrScheduleNotFound
		}
		patch := scheduler.PatchInput{}
		if strings.TrimSpace(req.Name) != "" {
			n := strings.TrimSpace(req.Name)
			patch.Name = &n
		}
		if strings.TrimSpace(req.Prompt) != "" {
			p := strings.TrimSpace(req.Prompt)
			patch.Prompt = &p
		}
		if req.BindCurrentSession != nil {
			if *req.BindCurrentSession {
				sid := strings.TrimSpace(tc.SessionID)
				patch.SessionID = &sid
			} else {
				empty := ""
				patch.SessionID = &empty
			}
		} else if req.SessionID != nil {
			sid := strings.TrimSpace(*req.SessionID)
			patch.SessionID = &sid
		}
		if strings.TrimSpace(req.Cron) != "" || req.IntervalMinutes > 0 || strings.TrimSpace(req.Timezone) != "" || strings.TrimSpace(req.Misfire) != "" {
			tr := existing.Trigger
			if strings.TrimSpace(req.Cron) != "" {
				tr.Kind = scheduler.TriggerKindCron
				tr.Cron = strings.TrimSpace(req.Cron)
				tr.IntervalMinutes = 0
			} else if req.IntervalMinutes > 0 {
				tr.Kind = scheduler.TriggerKindInterval
				tr.IntervalMinutes = req.IntervalMinutes
				tr.Cron = ""
			}
			if strings.TrimSpace(req.Timezone) != "" {
				tr.Timezone = strings.TrimSpace(req.Timezone)
			}
			if strings.TrimSpace(req.Misfire) != "" {
				tr.Misfire = strings.TrimSpace(req.Misfire)
			}
			patch.Trigger = &tr
		}
		if strings.TrimSpace(req.Mode) != "" || strings.TrimSpace(req.SandboxMode) != "" || req.CommandSandbox != nil ||
			strings.TrimSpace(req.ProviderID) != "" || strings.TrimSpace(req.ModelID) != "" || req.TimeoutSeconds > 0 {
			ex := existing.Execution
			if strings.TrimSpace(req.Mode) != "" {
				ex.Mode = strings.TrimSpace(req.Mode)
			}
			if strings.TrimSpace(req.SandboxMode) != "" {
				ex.SandboxMode = strings.TrimSpace(req.SandboxMode)
			}
			if req.CommandSandbox != nil {
				ex.CommandSandbox = *req.CommandSandbox
			}
			if strings.TrimSpace(req.ProviderID) != "" {
				ex.ProviderID = strings.TrimSpace(req.ProviderID)
			}
			if strings.TrimSpace(req.ModelID) != "" {
				ex.ModelID = strings.TrimSpace(req.ModelID)
			}
			if req.TimeoutSeconds > 0 {
				ex.TimeoutSeconds = req.TimeoutSeconds
			}
			patch.Execution = &ex
		}
		if strings.TrimSpace(req.SkillName) != "" || strings.TrimSpace(req.SkillKey) != "" {
			patch.SkillSelection = &scheduler.SkillRef{
				Key:  strings.TrimSpace(req.SkillKey),
				Name: strings.TrimSpace(req.SkillName),
			}
		}
		if strings.TrimSpace(req.NotifyOn) != "" {
			n := strings.TrimSpace(req.NotifyOn)
			patch.NotifyOn = &n
		}
		if req.InjectLastSummary != nil {
			patch.InjectLastSummary = req.InjectLastSummary
		}
		updated, err := g.scheduler.Update(id, patch)
		if err != nil {
			return agent.HostToolResult{Title: "hoxi_schedule update"}, err
		}
		raw, _ := json.MarshalIndent(updated, "", "  ")
		return agent.HostToolResult{
			Title:  "Schedule updated: " + updated.Name,
			Output: string(raw),
		}, nil

	case "delete":
		id := strings.TrimSpace(req.ID)
		if id == "" {
			return agent.HostToolResult{Title: "hoxi_schedule delete"}, errors.New("id is required")
		}
		if err := g.scheduler.Delete(id); err != nil {
			return agent.HostToolResult{Title: "hoxi_schedule delete"}, err
		}
		return agent.HostToolResult{
			Title:  "Schedule deleted",
			Output: fmt.Sprintf(`{"deleted":true,"id":%q}`, id),
		}, nil

	default:
		return agent.HostToolResult{Title: "hoxi_schedule"}, fmt.Errorf("unsupported hoxi_schedule action %q", act)
	}
}

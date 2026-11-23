// Package scheduler manages local workspace-scoped scheduled conversation tasks.
// Each schedule triggers a Hoxi conversation turn either inside a bound session
// or by creating a fresh session with user-selected sandbox and model options.
package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	ScheduleIDPrefix = "sch_"
	RunIDPrefix      = "run_"

	TriggerKindCron     = "cron"
	TriggerKindInterval = "interval"

	MisfireSkip     = "skip"
	MisfireCoalesce = "coalesce"

	NotifyAlertOrError = "alert_or_error"
	NotifyAlways       = "always"
	NotifyNever        = "never"

	StatusRunning     = "running"
	StatusOK          = "ok"
	StatusAlert       = "alert"
	StatusBlocked     = "blocked"
	StatusError       = "error"
	StatusSkippedBusy = "skipped_busy"
	StatusInterrupted = "interrupted"

	MinIntervalMinutes     = 5
	MaxIntervalMinutes     = 60 * 24 * 30 // 30 days
	DefaultTimeoutSeconds  = 300
	MinTimeoutSeconds      = 30
	MaxTimeoutSeconds      = 3600
	DefaultMaxFails        = 3
	MaxConsecutiveFailsCap = 20
	MaxNameRunes           = 160
	MaxPromptBytes         = 32 << 10
	MaxSummaryRunes        = 1500
	MaxStoredRunsPerJob    = 30
	MaxTotalSchedules      = 200
)

var (
	ErrScheduleNotFound    = errors.New("schedule not found")
	ErrScheduleBusy        = errors.New("schedule is already running")
	ErrStorageUnavailable  = errors.New("schedule storage is unavailable due to a previous load error")
	ErrTooManySchedules    = errors.New("maximum number of schedules reached")
	ErrInvalidSchedule     = errors.New("invalid schedule configuration")
	ErrBoundSessionMissing = errors.New("bound session was not found")
	ErrModelRequired       = errors.New("explicit providerID and modelID are required for scheduled tasks")
)

// TriggerSpec defines when a schedule fires.
type TriggerSpec struct {
	Kind            string `json:"kind"`                      // "cron" | "interval"
	Cron            string `json:"cron,omitempty"`            // 5-field cron expression
	IntervalMinutes int    `json:"intervalMinutes,omitempty"` // >= 5 when kind == "interval"
	Timezone        string `json:"timezone,omitempty"`        // IANA timezone, empty = Local
	Misfire         string `json:"misfire,omitempty"`         // "skip" (default) | "coalesce"
}

// SkillRef optionally binds one skill to each scheduled turn.
type SkillRef struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Runtime string `json:"runtime,omitempty"`
}

// ExecutionSpec describes how each scheduled turn creates or runs its conversation.
type ExecutionSpec struct {
	Mode           string `json:"mode,omitempty"`           // "build" (default) | "plan"
	SandboxMode    string `json:"sandboxMode,omitempty"`    // "local" (default) | "worktree"
	CommandSandbox bool   `json:"commandSandbox,omitempty"` // false by default
	Runtime        string `json:"runtime,omitempty"`        // "native" (default) or external runtime
	ProviderID     string `json:"providerID,omitempty"`
	ModelID        string `json:"modelID,omitempty"`
	Variant        string `json:"variant,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"` // default 300
}

// Schedule is a persisted workspace-scoped scheduled conversation task.
type Schedule struct {
	ID                  string        `json:"id"`
	Name                string        `json:"name"`
	Enabled             bool          `json:"enabled"`
	ProjectID           string        `json:"projectID,omitempty"`
	Directory           string        `json:"directory"`
	SessionID           string        `json:"sessionID,omitempty"` // bound session; empty creates fresh session per run
	Trigger             TriggerSpec   `json:"trigger"`
	Execution           ExecutionSpec `json:"execution"`
	SkillSelection      *SkillRef     `json:"skillSelection,omitempty"`
	Prompt              string        `json:"prompt"`
	InjectLastSummary   bool          `json:"injectLastSummary"`
	NotifyOn            string        `json:"notifyOn"`
	MaxConsecutiveFails int           `json:"maxConsecutiveFails"`

	LastRunAt        int64  `json:"lastRunAt,omitempty"`
	NextRunAt        int64  `json:"nextRunAt,omitempty"`
	LastOutcome      string `json:"lastOutcome,omitempty"`
	LastSummary      string `json:"lastSummary,omitempty"`
	LastError        string `json:"lastError,omitempty"`
	LastSessionID    string `json:"lastSessionID,omitempty"`
	ConsecutiveFails int    `json:"consecutiveFails,omitempty"`
	PausedReason     string `json:"pausedReason,omitempty"`
	AlertUnseen      bool   `json:"alertUnseen,omitempty"`
	Running          bool   `json:"running,omitempty"`
	CreatedAt        int64  `json:"createdAt"`
	UpdatedAt        int64  `json:"updatedAt"`
}

// RunRecord captures one execution attempt of a schedule.
type RunRecord struct {
	ID          string `json:"id"`
	ScheduleID  string `json:"scheduleID"`
	SessionID   string `json:"sessionID,omitempty"`
	Trigger     string `json:"trigger"` // "scheduled" | "manual"
	Status      string `json:"status"`  // running|ok|alert|blocked|error|skipped_busy|interrupted
	Summary     string `json:"summary,omitempty"`
	Error       string `json:"error,omitempty"`
	StartedAt   int64  `json:"startedAt"`
	CompletedAt int64  `json:"completedAt,omitempty"`
}

// UpsertInput creates or replaces a schedule configuration.
type UpsertInput struct {
	Name                string        `json:"name"`
	Enabled             *bool         `json:"enabled,omitempty"`
	ProjectID           string        `json:"projectID,omitempty"`
	Directory           string        `json:"directory"`
	SessionID           string        `json:"sessionID,omitempty"`
	Trigger             TriggerSpec   `json:"trigger"`
	Execution           ExecutionSpec `json:"execution"`
	SkillSelection      *SkillRef     `json:"skillSelection,omitempty"`
	Prompt              string        `json:"prompt"`
	InjectLastSummary   *bool         `json:"injectLastSummary,omitempty"`
	NotifyOn            string        `json:"notifyOn,omitempty"`
	MaxConsecutiveFails *int          `json:"maxConsecutiveFails,omitempty"`
}

// PatchInput applies a partial update to an existing schedule.
type PatchInput struct {
	Name                *string        `json:"name,omitempty"`
	Enabled             *bool          `json:"enabled,omitempty"`
	ProjectID           *string        `json:"projectID,omitempty"`
	Directory           *string        `json:"directory,omitempty"`
	SessionID           *string        `json:"sessionID,omitempty"`
	Trigger             *TriggerSpec   `json:"trigger,omitempty"`
	Execution           *ExecutionSpec `json:"execution,omitempty"`
	SkillSelection      *SkillRef      `json:"skillSelection,omitempty"`
	ClearSkill          bool           `json:"clearSkill,omitempty"`
	Prompt              *string        `json:"prompt,omitempty"`
	InjectLastSummary   *bool          `json:"injectLastSummary,omitempty"`
	NotifyOn            *string        `json:"notifyOn,omitempty"`
	MaxConsecutiveFails *int           `json:"maxConsecutiveFails,omitempty"`
}

// PreviewResponse validates a trigger and lists upcoming trigger timestamps (unix ms).
type PreviewResponse struct {
	Valid    bool    `json:"valid"`
	Error    string  `json:"error,omitempty"`
	Timezone string  `json:"timezone,omitempty"`
	NextRuns []int64 `json:"nextRuns,omitempty"`
}

// CreateSessionInput is passed to Host.CreateSession when a schedule creates a fresh conversation.
type CreateSessionInput struct {
	ScheduleID     string
	Title          string
	Directory      string
	Runtime        string
	Mode           string
	ProviderID     string
	ModelID        string
	SandboxMode    string
	CommandSandbox bool
}

// RunTurnInput is passed to Host.RunTurn for one scheduled conversation turn.
type RunTurnInput struct {
	ScheduleID     string
	Prompt         string
	System         string
	Mode           string
	Runtime        string
	ProviderID     string
	ModelID        string
	Variant        string
	SkillSelection *SkillRef
}

// Host abstracts gateway/runner/workspace/notification dependencies so internal/scheduler
// stays decoupled from other Tier 1 domain packages.
type Host interface {
	ResolveWorkspaceDirectory(projectID, directory string) (resolvedProjectID, resolvedDir string, err error)
	SessionExists(sessionID string) (exists bool, dir string)
	IsSessionBusy(sessionID string) bool
	CreateSession(ctx context.Context, req CreateSessionInput) (string, error)
	RunTurn(ctx context.Context, sessionID string, req RunTurnInput) (string, error)
	AbortSession(sessionID string)
	Notify(title, body, sessionID string)
	BroadcastUpdated(scheduleID string)
}

// SkillResolver is optionally implemented by Host to validate and canonicalize
// bound skill references (filling the canonical skl_ key and exact name) when
// schedules are created or updated.
type SkillResolver interface {
	ResolveSkill(ctx context.Context, directory, sessionID, runtime string, ref SkillRef) (SkillRef, error)
}

func newRandomID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

func cloneSkillRef(in *SkillRef) *SkillRef {
	if in == nil {
		return nil
	}
	cp := *in
	return &cp
}

func cloneSchedule(in Schedule) Schedule {
	in.SkillSelection = cloneSkillRef(in.SkillSelection)
	return in
}

func normalizeScheduleFields(s *Schedule) error {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidSchedule)
	}
	if len([]rune(s.Name)) > MaxNameRunes {
		return fmt.Errorf("%w: name exceeds %d characters", ErrInvalidSchedule, MaxNameRunes)
	}
	s.ProjectID = strings.TrimSpace(s.ProjectID)
	s.Directory = strings.TrimSpace(s.Directory)
	s.SessionID = strings.TrimSpace(s.SessionID)
	s.Prompt = strings.TrimSpace(s.Prompt)
	if len(s.Prompt) > MaxPromptBytes {
		return fmt.Errorf("%w: prompt exceeds %d bytes", ErrInvalidSchedule, MaxPromptBytes)
	}
	if s.SkillSelection != nil {
		s.SkillSelection.Key = strings.TrimSpace(s.SkillSelection.Key)
		s.SkillSelection.Name = strings.TrimSpace(s.SkillSelection.Name)
		s.SkillSelection.Runtime = strings.TrimSpace(s.SkillSelection.Runtime)
		if s.SkillSelection.Key == "" && s.SkillSelection.Name == "" {
			s.SkillSelection = nil
		}
	}
	if s.Prompt == "" && s.SkillSelection == nil {
		return fmt.Errorf("%w: prompt or skillSelection is required", ErrInvalidSchedule)
	}

	// Normalize trigger
	s.Trigger.Kind = strings.ToLower(strings.TrimSpace(s.Trigger.Kind))
	s.Trigger.Cron = strings.TrimSpace(s.Trigger.Cron)
	s.Trigger.Timezone = strings.TrimSpace(s.Trigger.Timezone)
	if s.Trigger.Kind == "" {
		if s.Trigger.Cron != "" {
			s.Trigger.Kind = TriggerKindCron
		} else if s.Trigger.IntervalMinutes > 0 {
			s.Trigger.Kind = TriggerKindInterval
		}
	}
	switch strings.ToLower(strings.TrimSpace(s.Trigger.Misfire)) {
	case "", MisfireSkip:
		s.Trigger.Misfire = MisfireSkip
	case MisfireCoalesce:
		s.Trigger.Misfire = MisfireCoalesce
	default:
		return fmt.Errorf("%w: unsupported misfire policy %q", ErrInvalidSchedule, s.Trigger.Misfire)
	}
	if err := ValidateTrigger(s.Trigger); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSchedule, err)
	}

	// Normalize execution. Bound sessions leave empty Mode blank so they
	// inherit the bound session's existing mode (e.g. plan vs build).
	switch strings.ToLower(strings.TrimSpace(s.Execution.Mode)) {
	case "":
		if s.SessionID == "" {
			s.Execution.Mode = "build"
		} else {
			s.Execution.Mode = ""
		}
	case "build":
		s.Execution.Mode = "build"
	case "plan":
		s.Execution.Mode = "plan"
	default:
		return fmt.Errorf("%w: unsupported mode %q (expected build or plan)", ErrInvalidSchedule, s.Execution.Mode)
	}
	switch strings.ToLower(strings.TrimSpace(s.Execution.SandboxMode)) {
	case "", "local":
		s.Execution.SandboxMode = "local"
	case "worktree":
		s.Execution.SandboxMode = "worktree"
	default:
		return fmt.Errorf("%w: unsupported sandboxMode %q (expected local or worktree)", ErrInvalidSchedule, s.Execution.SandboxMode)
	}
	s.Execution.Runtime = strings.TrimSpace(s.Execution.Runtime)
	if s.Execution.Runtime == "" {
		s.Execution.Runtime = "native"
	}
	s.Execution.ProviderID = strings.TrimSpace(s.Execution.ProviderID)
	s.Execution.ModelID = strings.TrimSpace(s.Execution.ModelID)
	s.Execution.Variant = strings.TrimSpace(s.Execution.Variant)
	if s.Execution.TimeoutSeconds == 0 {
		s.Execution.TimeoutSeconds = DefaultTimeoutSeconds
	}
	if s.Execution.TimeoutSeconds < MinTimeoutSeconds || s.Execution.TimeoutSeconds > MaxTimeoutSeconds {
		return fmt.Errorf("%w: timeoutSeconds must be between %d and %d", ErrInvalidSchedule, MinTimeoutSeconds, MaxTimeoutSeconds)
	}

	// Normalize notification policy & failure cap
	switch strings.ToLower(strings.TrimSpace(s.NotifyOn)) {
	case "", NotifyAlertOrError:
		s.NotifyOn = NotifyAlertOrError
	case NotifyAlways:
		s.NotifyOn = NotifyAlways
	case NotifyNever:
		s.NotifyOn = NotifyNever
	default:
		return fmt.Errorf("%w: unsupported notifyOn %q", ErrInvalidSchedule, s.NotifyOn)
	}
	if s.MaxConsecutiveFails < 0 || s.MaxConsecutiveFails > MaxConsecutiveFailsCap {
		return fmt.Errorf("%w: maxConsecutiveFails must be between 0 and %d", ErrInvalidSchedule, MaxConsecutiveFailsCap)
	}
	return nil
}

func truncateRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return strings.TrimSpace(string(runes[:max-1])) + "…"
}

package sessionstore

import (
	"encoding/json"
	"strings"

	"github.com/hoxiai/hoxi/internal/agentcli"
	"github.com/hoxiai/hoxi/internal/llm"
)

type RuntimeType string

const (
	RuntimeOpenCode    RuntimeType = agentcli.RuntimeOpenCode
	RuntimeCodex       RuntimeType = agentcli.RuntimeCodex
	RuntimeAntigravity RuntimeType = agentcli.RuntimeAntigravity
	RuntimeClaudeCode  RuntimeType = agentcli.RuntimeClaudeCode
	RuntimeGeminiCLI   RuntimeType = agentcli.RuntimeGeminiCLI
	RuntimeCodeBuddy   RuntimeType = agentcli.RuntimeCodeBuddy
	RuntimeQoder       RuntimeType = agentcli.RuntimeQoder
	RuntimeNative      RuntimeType = "native"
)

func IsValidRuntime(rt string) bool {
	return rt == string(RuntimeAntigravity) ||
		rt == string(RuntimeClaudeCode) ||
		rt == string(RuntimeGeminiCLI) ||
		rt == string(RuntimeCodeBuddy) ||
		rt == string(RuntimeQoder) ||
		rt == string(RuntimeCodex) ||
		rt == string(RuntimeNative)
}

func IsClaudeProtocolRuntime(rt string) bool {
	return rt == string(RuntimeClaudeCode) ||
		rt == string(RuntimeCodeBuddy) ||
		rt == string(RuntimeQoder)
}

type SessionTime struct {
	Created  int64 `json:"created"`
	Updated  int64 `json:"updated"`
	Archived int64 `json:"archived,omitempty"`
}

type SessionRevert struct {
	MessageID string `json:"messageID"`
	PartID    string `json:"partID,omitempty"`
	// CodeChanged reports whether the revert rolled back any workspace file
	// changes. Conversation-only reverts leave it false so the UI can avoid
	// claiming code was reverted when nothing was touched.
	CodeChanged bool `json:"codeChanged"`
}

type SessionCapabilities struct {
	Approval    bool `json:"approval"`
	Steer       bool `json:"steer"`
	Resume      bool `json:"resume"`
	Revert      bool `json:"revert"`
	Fork        bool `json:"fork"`
	Attachments bool `json:"attachments"`
	CostStats   bool `json:"costStats"`
	// Compact is true only where Hoxi can compact the context itself
	// (session_summarize.go); native CLIs manage their own window.
	Compact bool `json:"compact"`
}

// RuntimeCapabilities returns the capability matrix for a given runtime.
func RuntimeCapabilities(rt string, subagent bool) SessionCapabilities {
	if subagent {
		return SessionCapabilities{
			Approval:    false,
			Steer:       false,
			Resume:      false,
			Revert:      false,
			Fork:        false,
			Attachments: false,
			CostStats:   false,
		}
	}
	switch rt {
	case string(RuntimeClaudeCode):
		return SessionCapabilities{
			Approval:    true,
			Steer:       false,
			Resume:      true,
			Revert:      true,
			Fork:        true,
			Attachments: true,
			CostStats:   true,
		}
	case string(RuntimeAntigravity):
		return SessionCapabilities{
			Approval:    true,
			Steer:       true,
			Resume:      true,
			Revert:      true,
			Fork:        true,
			Attachments: true,
			CostStats:   true,
		}
	case string(RuntimeGeminiCLI):
		return SessionCapabilities{
			Approval:    true,
			Steer:       false,
			Resume:      true,
			Revert:      true,
			Fork:        true,
			Attachments: true,
			CostStats:   true,
		}
	case string(RuntimeCodeBuddy):
		return SessionCapabilities{
			Approval:    false,
			Steer:       false,
			Resume:      true,
			Revert:      true,
			Fork:        true,
			Attachments: false,
			CostStats:   true,
		}
	case string(RuntimeQoder):
		return SessionCapabilities{
			Approval:    false,
			Steer:       false,
			Resume:      true,
			Revert:      true,
			Fork:        true,
			Attachments: false,
			CostStats:   true,
		}
	case string(RuntimeCodex):
		return SessionCapabilities{
			Approval:    false,
			Steer:       false,
			Resume:      true,
			Revert:      true,
			Fork:        true,
			Attachments: false,
			CostStats:   true,
		}
	default: // opencode or native gateway session
		return SessionCapabilities{
			Approval:    true,
			Steer:       true,
			Resume:      true,
			Revert:      true,
			Fork:        true,
			Attachments: true,
			CostStats:   true,
			Compact:     rt == "opencode" || rt == string(RuntimeNative),
		}
	}
}

type SessionInfo struct {
	ID             string               `json:"id"`
	Slug           string               `json:"slug,omitempty"`
	ProjectID      string               `json:"projectID,omitempty"`
	Directory      string               `json:"directory"`
	ParentID       string               `json:"parentID,omitempty"`
	ScheduleID     string               `json:"scheduleID,omitempty"`
	Surface        string               `json:"surface,omitempty"`
	Title          string               `json:"title"`
	Version        string               `json:"version,omitempty"`
	Time           SessionTime          `json:"time"`
	Runtime        string               `json:"runtime"`
	Mode           string               `json:"mode,omitempty"`
	ModelID        string               `json:"modelID,omitempty"`
	ProviderID     string               `json:"providerID,omitempty"`
	ExternalID     string               `json:"externalID,omitempty"`
	SandboxMode    string               `json:"sandboxMode,omitempty"`
	CommandSandbox bool                 `json:"commandSandbox,omitempty"`
	ReadOnlyPaths  []string             `json:"readOnlyPaths,omitempty"`
	WritablePaths  []string             `json:"writablePaths,omitempty"`
	DeniedTools    []string             `json:"deniedTools,omitempty"`
	Branch         string               `json:"branch,omitempty"`
	Managed        bool                 `json:"managed,omitempty"`
	Revert         *SessionRevert       `json:"revert,omitempty"`
	Capabilities   *SessionCapabilities `json:"capabilities,omitempty"`
}

func NormalizeSessionSurface(surface, directory string) string {
	switch strings.ToLower(strings.TrimSpace(surface)) {
	case "work":
		return "work"
	case "code":
		return "code"
	}
	norm := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(directory)), "\\", "/")
	if strings.Contains(norm, "/.hoxi/workspaces/default") {
		return "work"
	}
	return "code"
}

type SessionStatus struct {
	Type    string `json:"type"`
	Attempt int    `json:"attempt,omitempty"`
	Message string `json:"message,omitempty"`
	// Next is when a retry fires (unix ms).
	Next int64 `json:"next,omitempty"`
}

type MessageTime struct {
	Created   int64 `json:"created"`
	Completed int64 `json:"completed,omitempty"`
}

type MessageTokens struct {
	Summary   *llm.TokenUsage `json:"summary,omitempty"` // Already included in the cumulative billing buckets.
	Total     int             `json:"total"`
	Context   int             `json:"context,omitempty"`
	Input     int             `json:"input"`
	Output    int             `json:"output"`
	Reasoning int             `json:"reasoning"`
	Cache     struct {
		Write int `json:"write"`
		Read  int `json:"read"`
	} `json:"cache"`
}

type MessageErrorData struct {
	Message      string                       `json:"message,omitempty"`
	StatusCode   int                          `json:"statusCode,omitempty"`
	IsRetryable  bool                         `json:"isRetryable,omitempty"`
	Kind         string                       `json:"kind,omitempty"`
	RetryAfterMs int64                        `json:"retryAfterMs,omitempty"`
	Upstream     *llm.UpstreamResponseDetails `json:"upstream,omitempty"`
}

type MessageError struct {
	Name string            `json:"name"`
	Data *MessageErrorData `json:"data,omitempty"`
}

type MessageInfo struct {
	GoalActive   bool           `json:"goalActive,omitempty"`
	ID           string         `json:"id"`
	SessionID    string         `json:"sessionID"`
	Role         string         `json:"role"`
	ParentID     string         `json:"parentID,omitempty"`
	Time         MessageTime    `json:"time"`
	ModelID      string         `json:"modelID,omitempty"`
	ProviderID   string         `json:"providerID,omitempty"`
	Mode         string         `json:"mode,omitempty"`
	Agent        string         `json:"agent,omitempty"`
	ServiceTier  string         `json:"serviceTier,omitempty"`
	ContinueTask bool           `json:"continueTask,omitempty"`
	Imported     bool           `json:"imported,omitempty"`
	Cost         float64        `json:"cost,omitempty"`
	Tokens       *MessageTokens `json:"tokens,omitempty"`
	Finish       string         `json:"finish,omitempty"`
	Error        *MessageError  `json:"error,omitempty"`
}

type PartTime struct {
	Start int64 `json:"start"`
	End   int64 `json:"end,omitempty"`
}

type ToolState struct {
	Status   string                 `json:"status"` // "pending" | "running" | "completed" | "error"
	Input    map[string]interface{} `json:"input"`
	Output   string                 `json:"output,omitempty"`
	Title    string                 `json:"title,omitempty"`
	Error    string                 `json:"error,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
	Time     *PartTime              `json:"time,omitempty"`
}

type MessagePart struct {
	ID        string                 `json:"id"`
	SessionID string                 `json:"sessionID"`
	MessageID string                 `json:"messageID"`
	Type      string                 `json:"type"` // "text" | "reasoning" | "tool" | "file"
	Text      string                 `json:"text,omitempty"`
	Tool      string                 `json:"tool,omitempty"`
	CallID    string                 `json:"callID,omitempty"`
	State     *ToolState             `json:"state,omitempty"`
	Time      *PartTime              `json:"time,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Mime      string                 `json:"mime,omitempty"`
	Filename  string                 `json:"filename,omitempty"`
	URL       string                 `json:"url,omitempty"`
}

// Text streams start with an empty snapshot before their first delta arrives.
// Keep that field on the wire so clients can append immediately.
func (p MessagePart) MarshalJSON() ([]byte, error) {
	type plainPart MessagePart
	if p.Type == "text" || p.Type == "reasoning" {
		return json.Marshal(struct {
			plainPart
			Text string `json:"text"`
		}{plainPart: plainPart(p), Text: p.Text})
	}
	return json.Marshal(plainPart(p))
}

type MessageTurn struct {
	Info  MessageInfo   `json:"info"`
	Parts []MessagePart `json:"parts"`
}

type SSEEvent struct {
	Seq        uint64                 `json:"seq,omitempty"`
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties"`
}

type SessionRecord struct {
	ID             string         `json:"id"`
	ProjectID      string         `json:"projectID,omitempty"`
	ParentID       string         `json:"parentID,omitempty"`
	ScheduleID     string         `json:"scheduleID,omitempty"`
	Surface        string         `json:"surface,omitempty"`
	Subagent       bool           `json:"subagent,omitempty"`
	Runtime        string         `json:"runtime"`
	ExternalID     string         `json:"externalID,omitempty"`
	ProjectDir     string         `json:"projectDir"`
	WorktreePath   string         `json:"worktreePath"`
	Branch         string         `json:"branch,omitempty"`
	BaseCommit     string         `json:"baseCommit,omitempty"`
	SandboxMode    string         `json:"sandboxMode,omitempty"`
	CommandSandbox bool           `json:"commandSandbox,omitempty"`
	ReadOnlyPaths  []string       `json:"readOnlyPaths,omitempty"`
	WritablePaths  []string       `json:"writablePaths,omitempty"`
	DeniedTools    []string       `json:"deniedTools,omitempty"`
	RetainedBranch string         `json:"retainedBranch,omitempty"`
	ModelID        string         `json:"modelID"`
	ProviderID     string         `json:"providerID"`
	Mode           string         `json:"mode,omitempty"`
	Title          string         `json:"title"`
	CreatedAt      int64          `json:"createdAt"`
	UpdatedAt      int64          `json:"updatedAt"`
	ArchivedAt     int64          `json:"archivedAt,omitempty"`
	Turns          []MessageTurn  `json:"turns,omitempty"`
	Revert         *SessionRevert `json:"revert,omitempty"`
	RevertedTurns  []MessageTurn  `json:"revertedTurns,omitempty"`
	RevertSnapshot string         `json:"revertSnapshot,omitempty"`
	RevertPaths    []string       `json:"revertPaths,omitempty"`
	// RevertScope records which part of the workspace an unrevert may restore:
	// the whole exclusive worktree or only RevertPaths (which may be empty).
	RevertScope         string              `json:"revertScope,omitempty"`
	Snapshots           map[string]string   `json:"snapshots,omitempty"`
	OpenCodeIDs         []string            `json:"openCodeIDs,omitempty"`
	OpenCodeChildIDs    []string            `json:"openCodeChildIDs,omitempty"`
	OpenCodeDirectories map[string]string   `json:"openCodeDirectories,omitempty"`
	CLISystemDigests    map[string][]string `json:"cliSystemDigests,omitempty"`
	ContextPending      bool                `json:"contextPending,omitempty"`
	ContextTurnCount    int                 `json:"contextTurnCount,omitempty"`
}

type PartKey struct {
	MessageID string
	PartID    string
}

func (r SessionRecord) SandboxModeValue() string {
	if r.SandboxMode != "" {
		return r.SandboxMode
	}
	// Records created before mode selection was supported always used worktrees.
	if NormalizeComparePath(r.WorktreePath) == NormalizeComparePath(r.ProjectDir) {
		return "local"
	}
	return "worktree"
}

// HasPathScope reports whether this session restricts writes via read-only or
// writable path boundaries.
func (r SessionRecord) HasPathScope() bool {
	return len(r.ReadOnlyPaths) > 0 || len(r.WritablePaths) > 0
}

func (r SessionRecord) ToSessionInfo() SessionInfo {
	dir := r.WorktreePath
	if dir == "" {
		dir = r.ProjectDir
	}
	caps := RuntimeCapabilities(r.Runtime, r.Subagent)
	var readOnlyPaths, writablePaths, deniedTools []string
	if len(r.ReadOnlyPaths) > 0 {
		readOnlyPaths = append([]string(nil), r.ReadOnlyPaths...)
	}
	if len(r.WritablePaths) > 0 {
		writablePaths = append([]string(nil), r.WritablePaths...)
	}
	if len(r.DeniedTools) > 0 {
		deniedTools = append([]string(nil), r.DeniedTools...)
	}
	return SessionInfo{
		ID:             r.ID,
		ParentID:       r.ParentID,
		ScheduleID:     r.ScheduleID,
		Surface:        NormalizeSessionSurface(r.Surface, dir),
		Managed:        true,
		Slug:           r.ID,
		ProjectID:      strings.TrimSpace(r.ProjectID),
		Directory:      dir,
		Title:          r.Title,
		Time:           SessionTime{Created: r.CreatedAt, Updated: r.UpdatedAt, Archived: r.ArchivedAt},
		Runtime:        r.Runtime,
		Mode:           r.Mode,
		ModelID:        r.ModelID,
		ProviderID:     r.ProviderID,
		ExternalID:     r.ExternalID,
		SandboxMode:    r.SandboxModeValue(),
		CommandSandbox: r.CommandSandbox,
		ReadOnlyPaths:  readOnlyPaths,
		WritablePaths:  writablePaths,
		DeniedTools:    deniedTools,
		Branch:         r.Branch,
		Revert:         r.Revert,
		Capabilities:   &caps,
	}
}

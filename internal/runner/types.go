package runner

import (
	"encoding/json"

	"github.com/hoxiai/hoxi/internal/agentcli"
)

type RuntimeType string

const (
	RuntimeAntigravity RuntimeType = agentcli.RuntimeAntigravity
	RuntimeClaudeCode  RuntimeType = agentcli.RuntimeClaudeCode
	RuntimeCodeBuddy   RuntimeType = agentcli.RuntimeCodeBuddy
	RuntimeQoder       RuntimeType = agentcli.RuntimeQoder
)

func IsValidRuntime(rt string) bool {
	return rt == string(RuntimeAntigravity) ||
		rt == string(RuntimeClaudeCode) ||
		rt == string(RuntimeCodeBuddy) ||
		rt == string(RuntimeQoder)
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
}

type SessionCapabilities struct {
	Approval    bool `json:"approval"`
	Steer       bool `json:"steer"`
	Resume      bool `json:"resume"`
	Revert      bool `json:"revert"`
	Fork        bool `json:"fork"`
	Attachments bool `json:"attachments"`
	CostStats   bool `json:"costStats"`
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
	default: // opencode or native gateway session
		return SessionCapabilities{
			Approval:    true,
			Steer:       true,
			Resume:      true,
			Revert:      true,
			Fork:        true,
			Attachments: true,
			CostStats:   true,
		}
	}
}

type SessionInfo struct {
	ID             string               `json:"id"`
	Slug           string               `json:"slug,omitempty"`
	ProjectID      string               `json:"projectID,omitempty"`
	Directory      string               `json:"directory"`
	ParentID       string               `json:"parentID,omitempty"`
	Title          string               `json:"title"`
	Version        string               `json:"version,omitempty"`
	Time           SessionTime          `json:"time"`
	Runtime        string               `json:"runtime"`
	ModelID        string               `json:"modelID,omitempty"`
	ProviderID     string               `json:"providerID,omitempty"`
	ExternalID     string               `json:"externalID,omitempty"`
	SandboxMode    string               `json:"sandboxMode,omitempty"`
	CommandSandbox bool                 `json:"commandSandbox,omitempty"`
	Branch         string               `json:"branch,omitempty"`
	Managed        bool                 `json:"managed,omitempty"`
	Revert         *SessionRevert       `json:"revert,omitempty"`
	Capabilities   *SessionCapabilities `json:"capabilities,omitempty"`
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
	Total     int `json:"total"`
	Input     int `json:"input"`
	Output    int `json:"output"`
	Reasoning int `json:"reasoning"`
	Cache     struct {
		Write int `json:"write"`
		Read  int `json:"read"`
	} `json:"cache"`
}

type MessageErrorData struct {
	Message     string `json:"message,omitempty"`
	StatusCode  int    `json:"statusCode,omitempty"`
	IsRetryable bool   `json:"isRetryable,omitempty"`
}

type MessageError struct {
	Name string            `json:"name"`
	Data *MessageErrorData `json:"data,omitempty"`
}

type MessageInfo struct {
	ID         string         `json:"id"`
	SessionID  string         `json:"sessionID"`
	Role       string         `json:"role"`
	ParentID   string         `json:"parentID,omitempty"`
	Time       MessageTime    `json:"time"`
	ModelID    string         `json:"modelID,omitempty"`
	ProviderID string         `json:"providerID,omitempty"`
	Mode       string         `json:"mode,omitempty"`
	Agent      string         `json:"agent,omitempty"`
	Cost       float64        `json:"cost,omitempty"`
	Tokens     *MessageTokens `json:"tokens,omitempty"`
	Finish     string         `json:"finish,omitempty"`
	Error      *MessageError  `json:"error,omitempty"`
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
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties"`
}

type SessionRecord struct {
	ID                  string            `json:"id"`
	ParentID            string            `json:"parentID,omitempty"`
	Subagent            bool              `json:"subagent,omitempty"`
	Runtime             string            `json:"runtime"`
	ExternalID          string            `json:"externalID,omitempty"`
	ProjectDir          string            `json:"projectDir"`
	WorktreePath        string            `json:"worktreePath"`
	Branch              string            `json:"branch,omitempty"`
	BaseCommit          string            `json:"baseCommit,omitempty"`
	SandboxMode         string            `json:"sandboxMode,omitempty"`
	CommandSandbox      bool              `json:"commandSandbox,omitempty"`
	RetainedBranch      string            `json:"retainedBranch,omitempty"`
	ModelID             string            `json:"modelID"`
	ProviderID          string            `json:"providerID"`
	Mode                string            `json:"mode,omitempty"`
	Title               string            `json:"title"`
	CreatedAt           int64             `json:"createdAt"`
	UpdatedAt           int64             `json:"updatedAt"`
	ArchivedAt          int64             `json:"archivedAt,omitempty"`
	Turns               []MessageTurn     `json:"turns,omitempty"`
	Revert              *SessionRevert    `json:"revert,omitempty"`
	RevertedTurns       []MessageTurn     `json:"revertedTurns,omitempty"`
	RevertSnapshot      string            `json:"revertSnapshot,omitempty"`
	Snapshots           map[string]string `json:"snapshots,omitempty"`
	OpenCodeIDs         []string          `json:"openCodeIDs,omitempty"`
	OpenCodeDirectories map[string]string `json:"openCodeDirectories,omitempty"`
	ContextPending      bool              `json:"contextPending,omitempty"`
	ContextTurnCount    int               `json:"contextTurnCount,omitempty"`
}

func (r SessionRecord) ToSessionInfo() SessionInfo {
	dir := r.WorktreePath
	if dir == "" {
		dir = r.ProjectDir
	}
	caps := RuntimeCapabilities(r.Runtime, r.Subagent)
	return SessionInfo{
		ID:             r.ID,
		ParentID:       r.ParentID,
		Managed:        true,
		Slug:           r.ID,
		ProjectID:      r.ProjectDir,
		Directory:      dir,
		Title:          r.Title,
		Time:           SessionTime{Created: r.CreatedAt, Updated: r.UpdatedAt, Archived: r.ArchivedAt},
		Runtime:        r.Runtime,
		ModelID:        r.ModelID,
		ProviderID:     r.ProviderID,
		ExternalID:     r.ExternalID,
		SandboxMode:    r.sandboxMode(),
		CommandSandbox: r.CommandSandbox,
		Branch:         r.Branch,
		Revert:         r.Revert,
		Capabilities:   &caps,
	}
}

package runner

import "encoding/json"

type RuntimeType string

const (
	RuntimeAntigravity RuntimeType = "antigravity"
	RuntimeClaudeCode  RuntimeType = "claude-code"
)

func IsValidRuntime(rt string) bool {
	return rt == string(RuntimeAntigravity) || rt == string(RuntimeClaudeCode)
}

type SessionTime struct {
	Created  int64 `json:"created"`
	Updated  int64 `json:"updated"`
	Archived int64 `json:"archived,omitempty"`
}

type SessionInfo struct {
	ID          string      `json:"id"`
	Slug        string      `json:"slug,omitempty"`
	ProjectID   string      `json:"projectID,omitempty"`
	Directory   string      `json:"directory"`
	ParentID    string      `json:"parentID,omitempty"`
	Title       string      `json:"title"`
	Version     string      `json:"version,omitempty"`
	Time        SessionTime `json:"time"`
	Runtime     string      `json:"runtime"`
	ModelID     string      `json:"modelID,omitempty"`
	ProviderID  string      `json:"providerID,omitempty"`
	ExternalID  string      `json:"externalID,omitempty"`
	SandboxMode string      `json:"sandboxMode,omitempty"`
	Branch      string      `json:"branch,omitempty"`
	Managed     bool        `json:"managed,omitempty"`
}

type SessionStatus struct {
	Type    string `json:"type"`
	Attempt int    `json:"attempt,omitempty"`
	Message string `json:"message,omitempty"`
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

type RunnerModelItem struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ProviderID string `json:"providerID"`
	Runtime    string `json:"runtime"`
	Reasoning  bool   `json:"reasoning,omitempty"`
}

type SessionRecord struct {
	ID                  string            `json:"id"`
	ParentID            string            `json:"parentID,omitempty"`
	Runtime             string            `json:"runtime"`
	ExternalID          string            `json:"externalID,omitempty"`
	ProjectDir          string            `json:"projectDir"`
	WorktreePath        string            `json:"worktreePath"`
	Branch              string            `json:"branch,omitempty"`
	BaseCommit          string            `json:"baseCommit,omitempty"`
	SandboxMode         string            `json:"sandboxMode,omitempty"`
	RetainedBranch      string            `json:"retainedBranch,omitempty"`
	ModelID             string            `json:"modelID"`
	ProviderID          string            `json:"providerID"`
	Mode                string            `json:"mode,omitempty"`
	Title               string            `json:"title"`
	CreatedAt           int64             `json:"createdAt"`
	UpdatedAt           int64             `json:"updatedAt"`
	ArchivedAt          int64             `json:"archivedAt,omitempty"`
	Turns               []MessageTurn     `json:"turns,omitempty"`
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
	return SessionInfo{
		ID:          r.ID,
		ParentID:    r.ParentID,
		Managed:     true,
		Slug:        r.ID,
		ProjectID:   r.ProjectDir,
		Directory:   dir,
		Title:       r.Title,
		Time:        SessionTime{Created: r.CreatedAt, Updated: r.UpdatedAt, Archived: r.ArchivedAt},
		Runtime:     r.Runtime,
		ModelID:     r.ModelID,
		ProviderID:  r.ProviderID,
		ExternalID:  r.ExternalID,
		SandboxMode: r.sandboxMode(),
		Branch:      r.Branch,
	}
}

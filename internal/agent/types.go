package agent

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/hoxiai/hoxi/internal/llm"
)

// Role represents a conversation participant.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// ContentType identifies a structured message block.
type ContentType string

const (
	ContentText       ContentType = "text"
	ContentMedia      ContentType = "media"
	ContentThinking   ContentType = "thinking"
	ContentToolUse    ContentType = "tool_use"
	ContentToolResult ContentType = "tool_result"
)

// ContentBlock is a provider-agnostic message block preserved across turns.
type ContentBlock struct {
	Type      ContentType     `json:"type"`
	Text      string          `json:"text,omitempty"`
	MimeType  string          `json:"mime_type,omitempty"`
	Data      string          `json:"data,omitempty"`
	Filename  string          `json:"filename,omitempty"`
	Signature string          `json:"signature,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// Message is one canonical conversation message.
type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

// ToolCall represents a single model-requested tool invocation.
type ToolCall struct {
	ID        string
	Name      string
	Input     json.RawMessage
	Signature string
}

// ToolSpec describes a tool exposed to the LLM.
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// TokenUsage captures disjoint per-step token buckets: Input excludes cache
// reads/writes and Output excludes Reasoning. Summing all five gives the total.
type TokenUsage = llm.TokenUsage

// StreamEventKind enumerates incremental events emitted by a ModelDriver.
type StreamEventKind string

const (
	EventTextDelta        StreamEventKind = "text_delta"
	EventThinkingDelta    StreamEventKind = "thinking_delta"
	EventThinkingEnd      StreamEventKind = "thinking_end"
	EventRedactedThinking StreamEventKind = "redacted_thinking"
	EventSignatureDelta   StreamEventKind = "signature_delta"
	EventToolCallReady    StreamEventKind = "tool_call_ready"
	EventTruncatedTool    StreamEventKind = "truncated_tool"
	EventUsage            StreamEventKind = "usage"
	EventFinish           StreamEventKind = "finish"
)

// StreamEvent is emitted by a ModelDriver while parsing an upstream SSE stream.
type StreamEvent struct {
	Kind       StreamEventKind
	BlockIndex int
	Delta      string
	ToolCall   *ToolCall
	Usage      *TokenUsage
	Finish     string
}

// ModelRequest is the canonical input to ModelDriver.BuildRequest.
type ModelRequest struct {
	SessionID    string
	CacheKey     string
	Target       llm.StreamTarget
	System       string
	SystemSuffix string
	Messages     []Message
	Tools        []ToolSpec
	MaxTokens    int
	Effort       string
	FastMode     bool
}

func effectiveModelCacheKey(req ModelRequest) string {
	if k := strings.TrimSpace(req.CacheKey); k != "" {
		return k
	}
	return strings.TrimSpace(req.SessionID)
}

// ModelDriver abstracts wire-protocol differences across model families.
type ModelDriver interface {
	Protocol() llm.StreamProtocol
	SystemPromptPatch() string
	BuildRequest(req ModelRequest) (llm.StreamHTTPRequest, error)
	ParseSSE(ctx context.Context, body io.Reader, emit func(StreamEvent)) error
}

// DriverFactory creates a ModelDriver for a given StreamProtocol.
type DriverFactory interface {
	Create(proto llm.StreamProtocol) (ModelDriver, error)
}

// ToolRegistry exposes mode-filtered tool specs and executes tool calls.
type ToolRegistry interface {
	SpecsForMode(mode string) []ToolSpec
	Get(name string) (Tool, bool)
	Execute(ctx context.Context, tc ToolContext, call ToolCall, permHook PermissionHook) (ToolOutput, error)
}

type turnScopedRegistry interface {
	SpecsForTurn(mode string, subtaskDepth int) []ToolSpec
}

// CompactFunc prunes or compacts messages before an upstream LLM call.
type CompactFunc func(msgs []Message, system string, contextLimit int) []Message

// EventSink receives streaming engine events.
type EventSink interface {
	Emit(evt EngineEvent)
}

// EventSinkFunc adapts a function into an EventSink.
type EventSinkFunc func(evt EngineEvent)

// Emit implements EventSink.
func (f EventSinkFunc) Emit(evt EngineEvent) { f(evt) }

// ToolContext provides execution context and injected host capabilities to tools.
type ToolContext struct {
	commandLogs        *turnCommandLogs
	SessionID          string
	CallID             string
	WorktreeDir        string
	Mode               string
	SubtaskDepth       int
	PermissionVetted   bool
	VisibleMessages    []Message
	Steer              SteerSource
	RulesTracker       *SubdirRuleTracker
	FileRevisions      *FileRevisionTracker
	CoordinateFileEdit FileEditCoordinatorHook
	RecordFileEdit     FileEditRecordedHook
	RunCommand         CommandRunner
	StreamRunCommand   StreamingCommandRunner
	BackgroundCommands BackgroundCommandManager
	OnToolOutput       func(partialOutput string)
	Diagnostics        DiagnosticHook
	RepoMap            RepoMapHook
	CodeSearch         CodeSearchHook
	SymbolInspect      SymbolInspectHook
	ImpactInspect      ImpactInspectHook
	AskQuestion        QuestionAsker
	MCPConfig          MCPConfigSource
	projectConfig      map[string]any
	RunSubtask         SubtaskRunner
	HostInvoke         HostInvokeHook
}

// FileEditCoordination reports cross-session lease and peer-modification state for a file mutation.
type FileEditCoordination struct {
	Release        func()
	Waited         bool
	ModifiedByPeer map[string]bool
}

// FileEditCoordinatorHook coordinates cross-session active file leases and atomic per-path locks.
type FileEditCoordinatorHook func(ctx context.Context, paths []string, onWait func(waitedPath string)) (FileEditCoordination, error)

// FileEditRecordedHook notifies the host coordinator after file mutations land on disk.
type FileEditRecordedHook func(paths []string)

// HostToolResult carries text, metadata, and optional media returned by a Hoxi host capability.
type HostToolResult struct {
	Title    string
	Output   string
	Metadata map[string]any
	Media    []ContentBlock
}

// HostInvokeHook invokes Hoxi host capabilities (browser, computer, image, tools gateway, skills, remotes) in-process.
type HostInvokeHook func(ctx context.Context, tc ToolContext, toolName string, input json.RawMessage) (HostToolResult, error)

// CommandRunner executes a shell command inside a workspace (optionally sandboxed).
type CommandRunner func(ctx context.Context, dir, command string) (output string, exitCode int, err error)

// StreamingCommandRunner executes a shell command and emits throttled partial output snapshots while running.
type StreamingCommandRunner func(ctx context.Context, dir, command string, capture io.Writer, onOutput func(partial string)) (output string, exitCode int, err error)

// DiagnosticHook inspects workspace files after edits and returns compiler/LSP diagnostics.
type DiagnosticHook func(ctx context.Context, dir, filePath string) string

// CodeSearchHit is one ranked code chunk returned by CodeSearchHook.
type CodeSearchHit struct {
	Path      string
	Symbol    string
	StartLine int
	EndLine   int
	Score     float64
	Snippet   string
}

// CodeSearchHook queries the workspace AST/BM25/semantic code index in-process.
type CodeSearchHook func(ctx context.Context, dir, query, pathPrefix string, limit int) ([]CodeSearchHit, error)

// SymbolInspectHook resolves a symbol's definition body and optional caller references.
type SymbolInspectHook func(ctx context.Context, dir, relPath, symbol string, includeCallers bool) (string, error)

// RepoMapHook computes a PageRank-ordered architectural map of hub files and key symbols.
type RepoMapHook func(ctx context.Context, dir, pathPrefix, query string, limit int) (string, error)

// ImpactInspectHook computes direct/transitive callers, callees, tests, and affected files.
type ImpactInspectHook func(ctx context.Context, dir, relPath, symbol string, depth, limit int) (string, error)

// QuestionAsker presents structured clarifying questions to the user and returns answers.
type QuestionAsker func(ctx context.Context, callID, rawJSON string) (output string, metadata map[string]any, err error)

// MCPConfigSource returns the workspace/user MCP configuration map ("mcp" section).
type MCPConfigSource func() map[string]any

// SubtaskRequest carries parameters for an isolated in-process subagent turn.
type SubtaskRequest struct {
	ParentCallID string
	SubagentType string
	Description  string
	Prompt       string
	Model        string
	ResumeID     string
}

// SubtaskResult carries the final summary and resolved execution metadata.
type SubtaskResult struct {
	SubtaskID         string
	Summary           string
	SubagentType      string
	ProviderID        string
	ModelID           string
	Routing           string
	ReadOnly          bool
	Mutated           bool // Includes attempted side effects for safe retry decisions.
	CompletedMutation bool // A mutating tool completed before the child ended.
}

// SubtaskRunner executes an isolated exploration or general subagent turn in-process.
type SubtaskRunner func(ctx context.Context, req SubtaskRequest) (SubtaskResult, error)

// ToolOutput is the structured result returned by a tool execution.
type ToolOutput struct {
	Title    string
	Output   string
	Metadata map[string]any
	Media    []ContentBlock
	IsError  bool
}

// Tool defines a capability invocable by the agent engine.
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	IsReadOnly() bool
	Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error)
}

// PermissionRequest is passed to PermissionHook before any tool runs.
type PermissionRequest struct {
	SessionID string
	CallID    string
	ToolName  string
	Input     json.RawMessage
	IsRead    bool
	ForceAsk  bool
}

// PermissionHook gates tool execution (PreToolUse approval + doom-loop).
type PermissionHook func(ctx context.Context, req PermissionRequest) error

// EngineEventKind enumerates lifecycle events emitted by Engine.RunTurn.
type EngineEventKind string

const (
	EngineTextStart              EngineEventKind = "text_start"
	EngineTextDelta              EngineEventKind = "text_delta"
	EngineTextEnd                EngineEventKind = "text_end"
	EngineThinkingStart          EngineEventKind = "thinking_start"
	EngineThinkingDelta          EngineEventKind = "thinking_delta"
	EngineThinkingEnd            EngineEventKind = "thinking_end"
	EngineToolPending            EngineEventKind = "tool_pending"
	EngineToolRunning            EngineEventKind = "tool_running"
	EngineToolCompleted          EngineEventKind = "tool_completed"
	EngineToolError              EngineEventKind = "tool_error"
	EngineSummaryUsage           EngineEventKind = "summary_usage"
	EngineTurnUsage              EngineEventKind = "turn_usage"
	EngineTurnFinish             EngineEventKind = "turn_finish"
	EngineContextCompacted       EngineEventKind = "context_compacted"
	EngineRulesBudget            EngineEventKind = "rules_budget"
	EngineModePromoted           EngineEventKind = "mode_promoted"
	EngineStepRetry              EngineEventKind = "step_retry"
	EngineStepRetryDone          EngineEventKind = "step_retry_done"
	EngineThinkingSignatureReset EngineEventKind = "thinking_signature_reset"
	EngineSubtaskStart           EngineEventKind = "subtask_start"
	EngineSubtaskEnd             EngineEventKind = "subtask_end"
)

// EngineEvent is emitted to the host runtime (e.g. runner SSE translator).
type EngineEvent struct {
	Kind         EngineEventKind
	ParentCallID string
	Step         int
	PartID       string
	CallID       string
	ToolName     string
	Text         string
	Signature    string
	Title        string
	Finish       string
	Input        map[string]any
	Metadata     map[string]any
	Media        []ContentBlock
	ErrorText    string
	Usage        *TokenUsage
}

// SteerSource yields queued user steering instructions between tool steps.
type SteerSource interface {
	Drain() []string
}

// SteerBlockSource optionally yields full structured user steering messages (text + media).
type SteerBlockSource interface {
	DrainMessages() []Message
}

// SteerWaitSource optionally notifies long-running foreground tools when user steering arrives.
type SteerWaitSource interface {
	HasPending() bool
	NotifyCh() <-chan struct{}
}

// BackgroundPromotedError signals that a foreground command was seamlessly promoted
// into a session-owned background job so newly queued user steering can run immediately.
type BackgroundPromotedError struct {
	Status BackgroundProcStatus
}

func (e *BackgroundPromotedError) Error() string {
	if e == nil {
		return "foreground command promoted to background"
	}
	return "foreground command promoted to background " + e.Status.ProcID
}

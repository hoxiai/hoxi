package intake

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const (
	TokenPrefix = "hqa_"

	DefaultMaxAttachmentBytes = 15 * 1024 * 1024

	StatusStaged         = "staged"
	StatusRunning        = "running"
	StatusSucceeded      = "succeeded"
	StatusAwaitingRetest = "awaiting_retest"
	StatusReopened       = "reopened"
	StatusResolved       = "resolved"
	StatusFailed         = "failed"
	StatusRejected       = "rejected"

	MaxAttachmentsPerSubmit     = 6
	MaxOrphanAttachmentsPerLink = 18
	MaxTotalAttachmentsPerLink  = 240
	OrphanAttachmentTTL         = 2 * time.Hour
	MaxUploadsPerLinkWindow     = 24
	UploadRateWindow            = 5 * time.Minute
	MaxPendingTasksPerLink      = 50
	MaxTasksPerProject          = 200
	MaxTitleBytes               = 240
	MaxBodyBytes                = 16000
)

var validPortalTokenRe = regexp.MustCompile(`^hqa_[0-9a-f]{32,128}$`)

// IsValidPortalTokenFormat reports whether raw has the syntactic shape of an intake portal token.
func IsValidPortalTokenFormat(raw string) bool {
	return validPortalTokenRe.MatchString(strings.TrimSpace(raw))
}

// EndpointInfo provides public/LAN base URLs so desktop users can copy ready-to-share links.
type EndpointInfo struct {
	LocalBaseURL string `json:"localBaseUrl,omitempty"`
	LANBaseURL   string `json:"lanBaseUrl,omitempty"`
	RelayBaseURL string `json:"relayBaseUrl,omitempty"`
}

// Link persists a single-project scoped QA / task intake token on disk (0600).
type Link struct {
	ID          string `json:"id"`
	Token       string `json:"token"`
	Directory   string `json:"directory"`
	ProjectName string `json:"projectName"`
	Label       string `json:"label"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
	ExpiresAt   int64  `json:"expiresAt,omitempty"`
}

// PublicLink is returned to the desktop owner in the Tasks Link Manager modal.
type PublicLink struct {
	ID          string `json:"id"`
	Directory   string `json:"directory"`
	ProjectName string `json:"projectName"`
	Label       string `json:"label"`
	Enabled     bool   `json:"enabled"`
	PortalPath  string `json:"portalPath"`
	LocalURL    string `json:"localUrl,omitempty"`
	LANURL      string `json:"lanUrl,omitempty"`
	RelayURL    string `json:"relayUrl,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
	ExpiresAt   int64  `json:"expiresAt,omitempty"`
}

// AttachmentRef references an uploaded screenshot/log stored via the host attachment store.
type AttachmentRef struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Mime     string `json:"mime"`
	Size     int64  `json:"size"`
	Kind     string `json:"kind"`
	URL      string `json:"url,omitempty"`
}

// Attachments decouples intake from the attachment domain package; gateway injects the implementation.
type Attachments interface {
	MaxBytes() int
	Save(directory, filename, mime string, data []byte) (AttachmentRef, error)
	Get(id string) (AttachmentRef, error)
	ReadBytes(id string) (AttachmentRef, []byte, error)
	Delete(id string) error
}

// TaskComment records a tester follow-up, retest feedback, or developer note.
type TaskComment struct {
	ID          string          `json:"id"`
	AuthorRole  string          `json:"authorRole"` // "tester" | "developer"
	AuthorLabel string          `json:"authorLabel,omitempty"`
	Action      string          `json:"action,omitempty"` // "submit" | "comment" | "reopen" | "pass" | "retest" | "reject"
	Text        string          `json:"text"`
	Attachments []AttachmentRef `json:"attachments,omitempty"`
	CreatedAt   int64           `json:"createdAt"`
}

// TaskItem is the desktop-visible task record stored atomically on disk.
type TaskItem struct {
	ID             string          `json:"id"`
	LinkID         string          `json:"linkId"`
	SubmitterLabel string          `json:"submitterLabel"`
	Directory      string          `json:"directory"`
	ProjectName    string          `json:"projectName"`
	Title          string          `json:"title"`
	Description    string          `json:"description"`
	Steps          string          `json:"steps,omitempty"`
	Attachments    []AttachmentRef `json:"attachments,omitempty"`
	Comments       []TaskComment   `json:"comments,omitempty"`
	Status         string          `json:"status"`
	SessionID      string          `json:"sessionID,omitempty"`
	DeveloperNote  string          `json:"developerNote,omitempty"`
	Summary        string          `json:"summary,omitempty"`
	Error          string          `json:"error,omitempty"`
	CreatedAt      int64           `json:"createdAt"`
	UpdatedAt      int64           `json:"updatedAt"`
	ApprovedAt     int64           `json:"approvedAt,omitempty"`
	CompletedAt    int64           `json:"completedAt,omitempty"`
}

// PortalTask is the sanitized task representation exposed to a part-time tester
// (never contains local filesystem Directory paths or raw session internals).
type PortalTask struct {
	ID             string          `json:"id"`
	SubmitterLabel string          `json:"submitterLabel"`
	ProjectName    string          `json:"projectName"`
	Title          string          `json:"title"`
	Description    string          `json:"description"`
	Steps          string          `json:"steps,omitempty"`
	Attachments    []AttachmentRef `json:"attachments,omitempty"`
	Comments       []TaskComment   `json:"comments,omitempty"`
	Status         string          `json:"status"`
	HasTimeline    bool            `json:"hasTimeline"`
	DeveloperNote  string          `json:"developerNote,omitempty"`
	Summary        string          `json:"summary,omitempty"`
	Error          string          `json:"error,omitempty"`
	CreatedAt      int64           `json:"createdAt"`
	UpdatedAt      int64           `json:"updatedAt"`
	ApprovedAt     int64           `json:"approvedAt,omitempty"`
	CompletedAt    int64           `json:"completedAt,omitempty"`
}

// SanitizedTodo represents a safe checklist item without source code details.
type SanitizedTodo struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

// SanitizedFileChange reports a code-free filename (basename only) and line-change counts for an edited file.
type SanitizedFileChange struct {
	File      string `json:"file"`
	Additions int    `json:"additions,omitempty"`
	Deletions int    `json:"deletions,omitempty"`
}

// SanitizedStep is a non-expandable execution progress step shown to testers.
// Raw tool inputs, file contents, diffs, shell commands, and local paths are omitted.
type SanitizedStep struct {
	ID        string                `json:"id"`
	Kind      string                `json:"kind"` // "tool" | "text" | "todo" | "notice"
	Category  string                `json:"category,omitempty"`
	Label     string                `json:"label,omitempty"`
	Text      string                `json:"text,omitempty"`
	Status    string                `json:"status,omitempty"` // "pending" | "running" | "completed" | "error"
	Todos     []SanitizedTodo       `json:"todos,omitempty"`
	Additions int                   `json:"additions,omitempty"`
	Deletions int                   `json:"deletions,omitempty"`
	Files     []SanitizedFileChange `json:"files,omitempty"`
	StartTime int64                 `json:"startTime,omitempty"`
	EndTime   int64                 `json:"endTime,omitempty"`
}

// SessionEventKind describes runner lifecycle or progress events for linked QA sessions.
type SessionEventKind string

const (
	SessionEventBusy     SessionEventKind = "busy"
	SessionEventProgress SessionEventKind = "progress"
	SessionEventIdle     SessionEventKind = "idle"
	SessionEventError    SessionEventKind = "error"
)

// SessionEvent carries an observed runner lifecycle or transcript update for a session.
type SessionEvent struct {
	SessionID string
	Kind      SessionEventKind
	Summary   string
	Error     string
	Completed bool
	Aborted   bool
}

// SanitizedTurn groups one assistant execution cycle for the tester view.
type SanitizedTurn struct {
	ID          string          `json:"id"`
	CreatedAt   int64           `json:"createdAt"`
	CompletedAt int64           `json:"completedAt,omitempty"`
	Steps       []SanitizedStep `json:"steps"`
}

// SanitizedTimeline is the full code-free execution view of a task session.
type SanitizedTimeline struct {
	TaskID string          `json:"taskId"`
	Busy   bool            `json:"busy"`
	Turns  []SanitizedTurn `json:"turns"`
}

// SanitizedStreamEvent notifies portal SSE listeners that a session or task changed.
type SanitizedStreamEvent struct {
	Type   string `json:"type"` // "task.updated" | "timeline.updated"
	TaskID string `json:"taskId,omitempty"`
}

// RunSessionRequest asks the runner host to create or continue an isolated fix session.
type RunSessionRequest struct {
	Directory   string
	SessionID   string
	Title       string
	System      string
	Prompt      string
	Attachments []AttachmentRef
	Mode        string
	Provider    string
	Model       string
}

// Sessions bridges intake task execution and sanitized timeline projection onto runner.Service.
type Sessions interface {
	EnsureSession(ctx context.Context, req RunSessionRequest) (string, error)
	RunTurn(ctx context.Context, sessionID string, req RunSessionRequest) (string, error)
	SanitizedTimeline(sessionID string) (SanitizedTimeline, bool)
	SubscribeSession(sessionID string) (<-chan struct{}, func())
	Abort(sessionID string)
}

func randomID(prefix string, numBytes int) string {
	buf := make([]byte, numBytes)
	_, _ = rand.Read(buf[:])
	return prefix + strconv.FormatInt(time.Now().UnixMilli(), 36) + "_" + hex.EncodeToString(buf)
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return TokenPrefix + hex.EncodeToString(buf), nil
}

func canonicalProjectDirectory(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", os.ErrInvalid
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return "", os.ErrNotExist
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil && resolved != "" {
		abs = resolved
	}
	if root := hoxipaths.ProjectRootForWorktree(abs); root != "" {
		if rst, rErr := os.Stat(root); rErr == nil && rst.IsDir() {
			if resolved, err := filepath.EvalSymlinks(root); err == nil && resolved != "" {
				abs = resolved
			} else {
				abs = root
			}
		}
	}
	return filepath.Clean(abs), nil
}

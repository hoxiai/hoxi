package statusbar

import (
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	MenuActionWorkspace       = "workspace"
	MenuActionAttention       = "attention"
	MenuActionRunning         = "running"
	MenuActionNewThread       = "new-thread"
	MenuActionHandoff         = "handoff"
	MenuActionPolicy          = "policy"
	MenuActionSettings        = "settings"
	MenuActionToggleKeepAwake = "toggle-keep-awake"

	maxMenuTargets    = 5
	maxMenuTitleRunes = 34
)

// SessionTarget identifies a session surfaced in the macOS status bar menu so
// clicking a pending approval or running task can jump straight to it.
type SessionTarget struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title,omitempty"`
	Directory string `json:"directory,omitempty"`
}

// MenuAction describes a status bar menu item chosen by the user.
type MenuAction struct {
	Kind      string
	SessionID string
}

var (
	actionHandlerMu sync.RWMutex
	actionHandler   func(MenuAction)
)

// SetActionHandler registers the callback invoked when a status bar menu item is clicked.
func SetActionHandler(fn func(MenuAction)) {
	actionHandlerMu.Lock()
	actionHandler = fn
	actionHandlerMu.Unlock()
}

func dispatchMenuAction(kind, sessionID string) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return
	}
	actionHandlerMu.RLock()
	fn := actionHandler
	actionHandlerMu.RUnlock()
	if fn != nil {
		fn(MenuAction{
			Kind:      kind,
			SessionID: strings.TrimSpace(sessionID),
		})
	}
}

// FormatMenuTargetLabel formats a single-line menu label for a pending or
// running session, stripping worktree suffixes so the project name stays readable.
func FormatMenuTargetLabel(target SessionTarget) string {
	title := collapseSingleLine(target.Title)
	if title == "" || title == "New Session" || strings.HasPrefix(title, "New session - ") {
		title = collapseSingleLine(target.SessionID)
	}
	if title == "" {
		title = "未命名任务"
	}
	title = truncateRunes(title, maxMenuTitleRunes)

	project := projectDisplayName(target.Directory)
	if project != "" && !strings.EqualFold(project, title) {
		return "  ↳ " + title + " · " + project
	}
	return "  ↳ " + title
}

func encodeMenuTargets(targets []SessionTarget) string {
	if len(targets) == 0 {
		return ""
	}
	lines := make([]string, 0, len(targets))
	for _, target := range targets {
		sid := collapseSingleLine(target.SessionID)
		if sid == "" {
			continue
		}
		lines = append(lines, sid+"\t"+FormatMenuTargetLabel(target))
	}
	return strings.Join(lines, "\n")
}

func projectDisplayName(directory string) string {
	cleaned := strings.TrimSpace(strings.ReplaceAll(directory, "\\", "/"))
	cleaned = strings.TrimRight(cleaned, "/")
	if cleaned == "" {
		return ""
	}
	if idx := strings.Index(cleaned, "/.hoxi/worktrees/"); idx > 0 {
		cleaned = cleaned[:idx]
	}
	base := filepath.Base(cleaned)
	if base == "." || base == "/" || base == "" {
		return ""
	}
	return truncateRunes(collapseSingleLine(base), 20)
}

func collapseSingleLine(value string) string {
	fields := strings.Fields(value)
	return strings.Join(fields, " ")
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "…"
}

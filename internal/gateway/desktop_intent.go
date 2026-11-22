package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// OpenDirectoryOptions carries optional UI hints for the native desktop directory picker.
type OpenDirectoryOptions struct {
	Title            string
	DefaultDirectory string
}

// desktopBridgeState groups host window callbacks (directory picker, window
// focus, native notifications, and one-shot navigation intents).
type desktopBridgeState struct {
	dialogMu              sync.RWMutex
	onOpenDirectoryDialog func(OpenDirectoryOptions) (string, error)
	dialogPicking         atomic.Bool
	onFocusDesktop        func(directory, sessionID string)
	onAppConnectRedirect  func(context.Context, string) error

	notifyMu              sync.RWMutex
	onDesktopNotify       func(DesktopNotification) error
	onDesktopNotifyStatus func() DesktopNotificationPermission
	onDesktopNotifyAsk    func() DesktopNotificationPermission

	intentMu        sync.Mutex
	pendingIntent   *DesktopIntent
	intentExpiresAt time.Time
	onDesktopIntent func()

	companionMu             sync.RWMutex
	onDesktopCompanionSync  func(DesktopCompanionState) DesktopCompanionSyncStatus
	onDesktopCompanionReset func()

	windowMu             sync.RWMutex
	onDesktopWindowClose func()
}

const (
	maxDesktopEntityIDLength = 96
	desktopSessionIDPrefix   = "ses_"
	desktopMessageIDPrefix   = "msg_"

	DesktopIntentActionAttention          = "attention"
	DesktopIntentActionRunning            = "running"
	DesktopIntentActionNewThread          = "new-thread"
	DesktopIntentActionHandoff            = "handoff"
	DesktopIntentActionPolicy             = "policy"
	DesktopIntentActionSettings           = "settings"
	DesktopIntentActionSyncKeepAwake      = "sync-keep-awake"
	DesktopIntentActionCompanionTap       = "companion-tap"
	DesktopIntentActionCompanionToggleMin = "companion-toggle-min"
	DesktopIntentActionCompanionDismiss   = "companion-dismiss"
	DesktopIntentActionConnectApp         = "connect-app"
	DesktopIntentActionOpenApp            = "open-app"
	DesktopIntentActionRunDraft           = "run-draft"
	DesktopIntentActionOpenBrowser        = "open-browser"
	DesktopIntentActionPreviewDoc         = "preview-doc"
)

// DesktopIntent is a one-shot navigation request produced by a deep link, the
// CLI, or the native status bar menu. The desktop UI consumes it and focuses
// the referenced session or view.
type DesktopIntent struct {
	Action    string            `json:"action,omitempty"`
	SessionID string            `json:"sessionID,omitempty"`
	MessageID string            `json:"messageID,omitempty"`
	Directory string            `json:"directory,omitempty"`
	AppID     string            `json:"appID,omitempty"`
	Tab       string            `json:"tab,omitempty"`
	RequestID string            `json:"requestID,omitempty"`
	Params    map[string]string `json:"params,omitempty"`
}

// SetDesktopIntentNotifier registers a wake-up hook for the desktop webview. It
// carries no payload: the UI fetches the pending intent through the gateway so
// an early event emitted before the frontend subscribes can never be lost.
func (g *Gateway) SetDesktopIntentNotifier(fn func()) {
	g.intentMu.Lock()
	g.onDesktopIntent = fn
	g.intentMu.Unlock()
}

// QueueDesktopIntent replaces any pending intent and notifies the desktop UI.
func (g *Gateway) QueueDesktopIntent(intent DesktopIntent) {
	intent.Action = strings.TrimSpace(intent.Action)
	if intent.Action != "" && !validDesktopIntentAction(intent.Action) {
		return
	}
	intent.SessionID = strings.TrimSpace(intent.SessionID)
	if intent.SessionID != "" && !validDesktopEntityID(intent.SessionID, desktopSessionIDPrefix) {
		return
	}
	if intent.Action == "" && intent.SessionID == "" {
		return
	}
	intent.MessageID = strings.TrimSpace(intent.MessageID)
	if intent.MessageID != "" && !validDesktopEntityID(intent.MessageID, desktopMessageIDPrefix) {
		return
	}
	dir, ok := sanitizeDesktopDirectory(intent.Directory)
	if !ok {
		return
	}
	intent.Directory = dir
	intent.AppID = strings.TrimSpace(intent.AppID)
	intent.Tab = strings.TrimSpace(intent.Tab)
	intent.RequestID = strings.TrimSpace(intent.RequestID)
	if len(intent.Params) > 0 {
		cleaned := make(map[string]string, len(intent.Params))
		for k, v := range intent.Params {
			k = strings.TrimSpace(k)
			if k != "" && len(k) <= 64 && len(v) <= 4096 {
				cleaned[k] = v
			}
		}
		intent.Params = cleaned
	}
	g.intentMu.Lock()
	g.pendingIntent = &intent
	g.intentExpiresAt = time.Now().Add(15 * time.Second)
	notify := g.onDesktopIntent
	g.intentMu.Unlock()
	if notify != nil {
		notify()
	}
}

func validDesktopIntentAction(action string) bool {
	switch action {
	case DesktopIntentActionAttention,
		DesktopIntentActionRunning,
		DesktopIntentActionNewThread,
		DesktopIntentActionHandoff,
		DesktopIntentActionPolicy,
		DesktopIntentActionSettings,
		DesktopIntentActionSyncKeepAwake,
		DesktopIntentActionCompanionTap,
		DesktopIntentActionCompanionToggleMin,
		DesktopIntentActionCompanionDismiss,
		DesktopIntentActionConnectApp,
		DesktopIntentActionOpenApp,
		DesktopIntentActionRunDraft,
		DesktopIntentActionOpenBrowser,
		DesktopIntentActionPreviewDoc:
		return true
	default:
		return false
	}
}

func validDesktopEntityID(value, prefix string) bool {
	if value == "" || len(value) <= len(prefix) || len(value) > maxDesktopEntityIDLength {
		return false
	}
	if prefix != "" && !strings.HasPrefix(value, prefix) {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

func sanitizeDesktopDirectory(dir string) (string, bool) {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return "", true
	}
	for _, r := range trimmed {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	if !filepath.IsAbs(trimmed) {
		return "", false
	}
	return filepath.Clean(trimmed), true
}

// ConsumeDesktopIntent returns and clears the pending intent.
func (g *Gateway) ConsumeDesktopIntent() (DesktopIntent, bool) {
	g.intentMu.Lock()
	defer g.intentMu.Unlock()
	if g.pendingIntent == nil {
		return DesktopIntent{}, false
	}
	return g.takeDesktopIntentLocked()
}

func (g *Gateway) takeDesktopIntentLocked() (DesktopIntent, bool) {
	intent := g.pendingIntent
	g.pendingIntent = nil
	if intent == nil || time.Now().After(g.intentExpiresAt) {
		return DesktopIntent{}, false
	}
	return *intent, true
}

// Protocol intents can carry drafts or local paths. Only the in-process
// workspace may consume them; ordinary CLI focus intents keep their behavior.
func (g *Gateway) consumeDesktopIntentForRequest(r *http.Request) (DesktopIntent, bool) {
	g.intentMu.Lock()
	defer g.intentMu.Unlock()
	if g.pendingIntent == nil {
		return DesktopIntent{}, false
	}
	private := false
	switch g.pendingIntent.Action {
	case DesktopIntentActionConnectApp, DesktopIntentActionOpenApp, DesktopIntentActionRunDraft, DesktopIntentActionOpenBrowser, DesktopIntentActionPreviewDoc:
		private = true
	case DesktopIntentActionSettings:
		private = g.pendingIntent.Params["section"] != ""
	}
	desktop, _ := r.Context().Value(appDesktopRequestKey{}).(bool)
	if private && (!desktop || isRemoteRequest(r)) {
		return DesktopIntent{}, false
	}
	return g.takeDesktopIntentLocked()
}

func (g *Gateway) handleOpenDirectoryDialog(w http.ResponseWriter, r *http.Request) {
	if isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "directory picker is desktop-only")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET or POST required")
		return
	}

	var opts OpenDirectoryOptions
	var restrictSensitive bool
	if r.Method == http.MethodPost && r.Body != nil {
		var body struct {
			Title             string `json:"title"`
			DefaultDir        string `json:"defaultDir"`
			RestrictSensitive bool   `json:"restrictSensitive"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeJSONError(w, http.StatusBadRequest, "invalid directory dialog request")
			return
		}
		opts.Title = sanitizeDialogTitle(body.Title)
		opts.DefaultDirectory = sanitizeExistingDialogDir(body.DefaultDir)
		restrictSensitive = body.RestrictSensitive
	}

	g.dialogMu.RLock()
	openDialog := g.onOpenDirectoryDialog
	g.dialogMu.RUnlock()
	if openDialog == nil {
		writeJSON(w, http.StatusOK, map[string]any{"path": "", "canceled": true})
		return
	}
	if !g.dialogPicking.CompareAndSwap(false, true) {
		writeJSONError(w, http.StatusConflict, "directory picker is already open")
		return
	}
	defer g.dialogPicking.Store(false)

	dirPath, err := openDialog(opts)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"path": "", "error": err.Error()})
		return
	}
	cleaned := strings.TrimSpace(dirPath)
	if cleaned == "" {
		writeJSON(w, http.StatusOK, map[string]any{"path": "", "canceled": true})
		return
	}
	if restrictSensitive && isProtectedPickedDirectory(cleaned) {
		writeJSONError(w, http.StatusForbidden, "selected directory is protected and cannot be bound by a Work app")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": cleaned, "canceled": false})
}

func sanitizeDialogTitle(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var b strings.Builder
	count := 0
	for _, r := range trimmed {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		count++
		if count >= 160 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

func sanitizeExistingDialogDir(raw string) string {
	clean, ok := sanitizeDesktopDirectory(raw)
	if !ok || clean == "" {
		return ""
	}
	info, err := os.Stat(clean)
	if err != nil || !info.IsDir() {
		return ""
	}
	return clean
}

func isProtectedPickedDirectory(raw string) bool {
	clean, ok := sanitizeDesktopDirectory(raw)
	if !ok || clean == "" {
		return true
	}
	candidates := []string{clean}
	if resolved, err := filepath.EvalSymlinks(clean); err == nil && resolved != "" {
		candidates = append(candidates, filepath.Clean(resolved))
	}
	home, _ := os.UserHomeDir()
	hoxiHome := hoxipaths.Home()
	protectedRoots := []string{}
	if hoxiHome != "" {
		protectedRoots = append(protectedRoots, filepath.Clean(hoxiHome))
		if resolved, err := filepath.EvalSymlinks(hoxiHome); err == nil && resolved != "" {
			protectedRoots = append(protectedRoots, filepath.Clean(resolved))
		}
	}
	if home != "" {
		for _, sub := range []string{".hoxi", ".ssh", ".aws", ".gnupg", ".kube"} {
			p := filepath.Join(home, sub)
			protectedRoots = append(protectedRoots, filepath.Clean(p))
			if resolved, err := filepath.EvalSymlinks(p); err == nil && resolved != "" {
				protectedRoots = append(protectedRoots, filepath.Clean(resolved))
			}
		}
	}
	for _, cand := range candidates {
		if filepath.Dir(cand) == cand {
			return true
		}
		for _, root := range protectedRoots {
			if cand == root || strings.HasPrefix(cand, root+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

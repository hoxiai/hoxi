package app

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/appbridge"
	"github.com/hoxiai/hoxi/internal/gateway"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/wailsapp/wails/v2/pkg/options"
)

// desktopIntentEvent wakes the webview when a deep link or CLI request queues a
// navigation intent. The event carries no payload on purpose.
const desktopIntentEvent = "hoxi:desktop-intent"

const (
	maxDeepLinkIDLength    = 96
	maxDeepLinkURLLength   = 8192
	maxDeepLinkPromptBytes = 4096
	sessionIDPrefix        = "ses_"
	messageIDPrefix        = "msg_"
)

// ParseDeepLink extracts a desktop navigation or protocol intent from hoxi://
// links such as hoxi://session/ses_123?message=msg_456, hoxi://app/<id>,
// hoxi://run?..., hoxi://browser/open?..., hoxi://doc/preview?..., or
// hoxi://settings/<section>. It intentionally rejects unknown shapes so the URL
// can still fall through to the auth callback handler.
func ParseDeepLink(raw string) (gateway.DesktopIntent, bool) {
	if intent, ok := parseSessionDeepLink(raw); ok {
		return intent, true
	}
	return parseProtocolDeepLink(raw)
}

func parseSessionDeepLink(raw string) (gateway.DesktopIntent, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > maxDeepLinkURLLength {
		return gateway.DesktopIntent{}, false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || !strings.EqualFold(parsed.Scheme, "hoxi") {
		return gateway.DesktopIntent{}, false
	}
	host := strings.ToLower(parsed.Host)
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")

	sessionID := ""
	switch host {
	case "session", "sessions":
		if len(segments) > 0 {
			sessionID = segments[0]
		}
	case "", "open":
		if len(segments) > 1 && (segments[0] == "session" || segments[0] == "sessions") {
			sessionID = segments[1]
		} else {
			sessionID = parsed.Query().Get("session")
		}
	}
	sessionID = strings.TrimSpace(sessionID)
	if !validDeepLinkID(sessionID, sessionIDPrefix) {
		return gateway.DesktopIntent{}, false
	}

	messageID := strings.TrimSpace(parsed.Query().Get("message"))
	if messageID != "" && !validDeepLinkID(messageID, messageIDPrefix) {
		messageID = ""
	}
	return gateway.DesktopIntent{SessionID: sessionID, MessageID: messageID}, true
}

func parseProtocolDeepLink(raw string) (gateway.DesktopIntent, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > maxDeepLinkURLLength {
		return gateway.DesktopIntent{}, false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || !strings.EqualFold(parsed.Scheme, "hoxi") {
		return gateway.DesktopIntent{}, false
	}
	if parsed.User != nil || parsed.Port() != "" || parsed.Fragment != "" {
		return gateway.DesktopIntent{}, false
	}
	host := strings.ToLower(parsed.Host)
	segments := nonEmptySegments(parsed.Path)
	q, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return gateway.DesktopIntent{}, false
	}

	switch host {
	case "connect":
		if len(segments) != 0 {
			return gateway.DesktopIntent{}, false
		}
		reqID := strings.TrimSpace(q.Get("requestId"))
		if reqID != "" && validDeepLinkID(reqID, "con_") {
			return gateway.DesktopIntent{
				Action:    gateway.DesktopIntentActionConnectApp,
				RequestID: reqID,
			}, true
		}
		return gateway.DesktopIntent{}, false

	case "app":
		if len(segments) == 0 || len(segments) > 2 || !appbridge.ValidAppID(segments[0]) {
			return gateway.DesktopIntent{}, false
		}
		tab := ""
		if len(segments) > 1 {
			if !appbridge.ValidAppID(segments[1]) {
				return gateway.DesktopIntent{}, false
			}
			tab = segments[1]
		}
		params := map[string]string{}
		if store := strings.TrimSpace(q.Get("store")); store != "" && len(store) <= 96 {
			params["store"] = store
		}
		if item := strings.TrimSpace(q.Get("item")); item != "" && len(item) <= 128 {
			params["item"] = item
		}
		return gateway.DesktopIntent{
			Action: gateway.DesktopIntentActionOpenApp,
			AppID:  segments[0],
			Tab:    tab,
			Params: params,
		}, true

	case "run":
		if len(segments) != 0 {
			return gateway.DesktopIntent{}, false
		}
		mode := strings.ToLower(strings.TrimSpace(q.Get("mode")))
		if mode == "" {
			mode = "draft"
		}
		if mode != "draft" && mode != "confirm" {
			return gateway.DesktopIntent{}, false
		}
		prompt := strings.TrimSpace(q.Get("prompt"))
		skill := strings.TrimSpace(q.Get("skill"))
		appID := strings.TrimSpace(q.Get("app"))
		dir := strings.TrimSpace(q.Get("directory"))
		if len(prompt) > maxDeepLinkPromptBytes || (prompt == "" && skill == "") || len(skill) > 96 || hasControlChars(skill) {
			return gateway.DesktopIntent{}, false
		}
		if appID != "" && !appbridge.ValidAppID(appID) {
			return gateway.DesktopIntent{}, false
		}
		if dir != "" {
			if !filepath.IsAbs(dir) || hasControlChars(dir) || hoxipaths.IsDeniedDocOrSpreadsheetPath(dir) {
				return gateway.DesktopIntent{}, false
			}
			dir = filepath.Clean(dir)
		}
		params := map[string]string{"mode": mode}
		if prompt != "" {
			params["prompt"] = prompt
		}
		if skill != "" && len(skill) <= 96 {
			params["skill"] = skill
		}
		return gateway.DesktopIntent{
			Action:    gateway.DesktopIntentActionRunDraft,
			AppID:     appID,
			Directory: dir,
			Params:    params,
		}, true

	case "browser":
		if len(segments) != 1 || segments[0] != "open" {
			return gateway.DesktopIntent{}, false
		}
		targetURL := strings.TrimSpace(q.Get("url"))
		parsedTarget, targetErr := url.Parse(targetURL)
		if targetErr != nil || len(targetURL) > 4096 || (parsedTarget.Scheme != "http" && parsedTarget.Scheme != "https") || parsedTarget.Host == "" || parsedTarget.User != nil {
			return gateway.DesktopIntent{}, false
		}
		profile := strings.TrimSpace(q.Get("profile"))
		if profile != "" && !appbridge.ValidAppID(profile) {
			return gateway.DesktopIntent{}, false
		}
		mode := strings.ToLower(strings.TrimSpace(q.Get("mode")))
		if mode == "" {
			mode = "readonly"
		}
		if mode != "readonly" && mode != "interactive" {
			return gateway.DesktopIntent{}, false
		}
		return gateway.DesktopIntent{
			Action: gateway.DesktopIntentActionOpenBrowser,
			AppID:  profile,
			Params: map[string]string{"url": targetURL, "mode": mode},
		}, true

	case "doc":
		if len(segments) != 1 || segments[0] != "preview" {
			return gateway.DesktopIntent{}, false
		}
		docPath := strings.TrimSpace(q.Get("path"))
		if docPath == "" || len(docPath) > 4096 || !filepath.IsAbs(docPath) || hasControlChars(docPath) || !validDocDeepLinkPath(docPath) {
			return gateway.DesktopIntent{}, false
		}
		return gateway.DesktopIntent{
			Action: gateway.DesktopIntentActionPreviewDoc,
			Params: map[string]string{"path": filepath.Clean(docPath)},
		}, true

	case "settings":
		if len(segments) != 1 {
			return gateway.DesktopIntent{}, false
		}
		section := strings.ToLower(segments[0])
		switch section {
		case "providers", "media-roles", "app-grants", "approval", "general":
			return gateway.DesktopIntent{
				Action: gateway.DesktopIntentActionSettings,
				Params: map[string]string{"section": section},
			}, true
		default:
			return gateway.DesktopIntent{}, false
		}
	}
	return gateway.DesktopIntent{}, false
}

func validDocDeepLinkPath(path string) bool {
	denied := func(p string) bool {
		return hoxipaths.IsDeniedDocOrSpreadsheetPath(p) || hoxipaths.IsProtectedStateFile(filepath.Base(p)) || hoxipaths.IsProtectedWorkStatePath(p)
	}
	if denied(path) {
		return false
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return !denied(real)
	} else if !os.IsNotExist(err) {
		return false
	}
	return true // preview still goes through the existing file access gate
}

// ParseConnectDeepLink extracts a redirect-mode hoxi://connect?... handshake.
func ParseConnectDeepLink(raw string) (appbridge.ConnectInput, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > maxDeepLinkURLLength {
		return appbridge.ConnectInput{}, false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || !strings.EqualFold(parsed.Scheme, "hoxi") || !strings.EqualFold(parsed.Host, "connect") {
		return appbridge.ConnectInput{}, false
	}
	if parsed.User != nil || parsed.Port() != "" || parsed.Fragment != "" {
		return appbridge.ConnectInput{}, false
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return appbridge.ConnectInput{}, false
	}
	q, err := url.ParseQuery(parsed.RawQuery)
	if err != nil || q.Has("requestId") {
		return appbridge.ConnectInput{}, false
	}
	appID := strings.TrimSpace(q.Get("appId"))
	if !appbridge.ValidAppID(appID) {
		return appbridge.ConnectInput{}, false
	}
	var scopes []string
	for _, s := range strings.Split(strings.TrimSpace(q.Get("scopes")), ",") {
		if trimmedScope := strings.TrimSpace(s); trimmedScope != "" {
			scopes = append(scopes, trimmedScope)
		}
	}
	return appbridge.ConnectInput{
		AppID:               appID,
		Name:                strings.TrimSpace(q.Get("name")),
		Scopes:              scopes,
		RedirectURI:         strings.TrimSpace(q.Get("redirectUri")),
		CodeChallenge:       strings.TrimSpace(q.Get("codeChallenge")),
		CodeChallengeMethod: strings.TrimSpace(q.Get("codeChallengeMethod")),
		State:               q.Get("state"),
		Mode:                appbridge.ConnectModeRedirect,
	}, true
}

func nonEmptySegments(p string) []string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	out := make([]string, 0, len(parts))
	for _, seg := range parts {
		if seg = strings.TrimSpace(seg); seg != "" {
			out = append(out, seg)
		}
	}
	return out
}

func hasControlChars(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// launchURLs returns the hoxi:// links among process arguments. Windows opens a
// protocol link as `Hoxi.exe "hoxi://…"`; macOS delivers links through
// OnUrlOpen instead, so there the list is empty.
func launchURLs(args []string) []string {
	var urls []string
	for _, arg := range args {
		if isAppURLArg(arg) {
			urls = append(urls, strings.TrimSpace(arg))
		}
	}
	return urls
}

func isAppURLArg(arg string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(arg)), "hoxi://")
}

// onSecondInstance runs in the live instance when Hoxi is launched again,
// including when Windows opens a hoxi:// link (login callback, session link).
func (a *DesktopApp) onSecondInstance(data options.SecondInstanceData) {
	if a.desktopContext() != nil {
		a.showWindow()
	}
	for _, link := range launchURLs(data.Args) {
		a.onURLOpen(link)
	}
}

// isAuthCallbackDeepLink reports whether the URL is the account authorization
// callback handled by the auth service.
func isAuthCallbackDeepLink(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return false
	}
	if !strings.EqualFold(parsed.Scheme, "hoxi") {
		return false
	}
	return strings.EqualFold(parsed.Host, "auth-callback")
}

func validDeepLinkID(value, prefix string) bool {
	if value == "" || len(value) <= len(prefix) || len(value) > maxDeepLinkIDLength {
		return false
	}
	if prefix != "" && !strings.HasPrefix(value, prefix) {
		return false
	}
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case char == '_' || char == '-':
		default:
			return false
		}
	}
	return true
}

func (a *DesktopApp) handleStatusBarMenuAction(action statusbar.MenuAction) {
	switch strings.TrimSpace(action.Kind) {
	case statusbar.MenuActionToggleKeepAwake:
		if a.status != nil {
			a.status.SetKeepAwakeEnabled(!a.status.KeepAwakeEnabled())
		}
		if a.gateway != nil {
			a.gateway.QueueDesktopIntent(gateway.DesktopIntent{
				Action: gateway.DesktopIntentActionSyncKeepAwake,
			})
		}
		return
	case statusbar.MenuActionWorkspace:
		a.showWindow()
		return
	case statusbar.MenuActionAttention:
		a.showWindow()
		if a.gateway == nil {
			return
		}
		sessionID := strings.TrimSpace(action.SessionID)
		directory := ""
		if a.status != nil {
			if sessionID != "" {
				target := a.status.ResolveTarget(sessionID)
				sessionID = target.SessionID
				directory = target.Directory
			} else if target, ok := a.status.FirstAttentionTarget(); ok {
				sessionID = target.SessionID
				directory = target.Directory
			}
		}
		a.gateway.QueueDesktopIntent(gateway.DesktopIntent{
			Action:    gateway.DesktopIntentActionAttention,
			SessionID: sessionID,
			Directory: directory,
		})
	case statusbar.MenuActionRunning:
		a.showWindow()
		if a.gateway == nil {
			return
		}
		sessionID := strings.TrimSpace(action.SessionID)
		directory := ""
		if a.status != nil {
			if sessionID != "" {
				target := a.status.ResolveTarget(sessionID)
				sessionID = target.SessionID
				directory = target.Directory
			} else if target, ok := a.status.FirstRunningTarget(); ok {
				sessionID = target.SessionID
				directory = target.Directory
			}
		}
		a.gateway.QueueDesktopIntent(gateway.DesktopIntent{
			Action:    gateway.DesktopIntentActionRunning,
			SessionID: sessionID,
			Directory: directory,
		})
	case statusbar.MenuActionNewThread:
		a.showWindow()
		if a.gateway != nil {
			a.gateway.QueueDesktopIntent(gateway.DesktopIntent{
				Action: gateway.DesktopIntentActionNewThread,
			})
		}
	case statusbar.MenuActionHandoff:
		a.showWindow()
		if a.gateway != nil {
			a.gateway.QueueDesktopIntent(gateway.DesktopIntent{
				Action: gateway.DesktopIntentActionHandoff,
			})
		}
	case statusbar.MenuActionPolicy:
		a.showWindow()
		if a.gateway != nil {
			a.gateway.QueueDesktopIntent(gateway.DesktopIntent{
				Action: gateway.DesktopIntentActionPolicy,
			})
		}
	case statusbar.MenuActionSettings:
		a.showWindow()
		if a.gateway != nil {
			a.gateway.QueueDesktopIntent(gateway.DesktopIntent{
				Action: gateway.DesktopIntentActionSettings,
			})
		}
	case statusbar.MenuActionCompanionTap:
		if a.gateway != nil {
			a.gateway.QueueDesktopIntent(gateway.DesktopIntent{
				Action: gateway.DesktopIntentActionCompanionTap,
			})
		}
	case statusbar.MenuActionCompanionToggleMin:
		if a.gateway != nil {
			a.gateway.QueueDesktopIntent(gateway.DesktopIntent{
				Action: gateway.DesktopIntentActionCompanionToggleMin,
			})
		}
	case statusbar.MenuActionCompanionDismiss:
		if a.gateway != nil {
			a.gateway.QueueDesktopIntent(gateway.DesktopIntent{
				Action: gateway.DesktopIntentActionCompanionDismiss,
			})
		}
	}
}

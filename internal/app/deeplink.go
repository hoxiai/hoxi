package app

import (
	"net/url"
	"strings"

	"github.com/hoxiai/hoxi/internal/gateway"
)

// desktopIntentEvent wakes the webview when a deep link or CLI request queues a
// navigation intent. The event carries no payload on purpose.
const desktopIntentEvent = "hoxi:desktop-intent"

const (
	maxDeepLinkIDLength = 96
	sessionIDPrefix     = "ses_"
	messageIDPrefix     = "msg_"
)

// ParseDeepLink extracts a session navigation intent from links such as
// hoxi://session/ses_123?message=msg_456. It intentionally rejects unknown
// shapes so the URL can still fall through to the auth callback handler.
func ParseDeepLink(raw string) (gateway.DesktopIntent, bool) {
	return parseSessionDeepLink(raw)
}

func parseSessionDeepLink(raw string) (gateway.DesktopIntent, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
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

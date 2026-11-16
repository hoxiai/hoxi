package gateway

import (
	"context"
	"net/http"
	"strings"
)

// FileAccessConfirmation describes one desktop confirmation raised before Hoxi
// reads or opens a credential/protected path. Action is "read" or "open".
// Remote controllers and headless mode never reach it.
type FileAccessConfirmation struct {
	Path   string
	Action string
	Locale string
}

// SetFileAccessConfirmation installs the native dialog every desktop read or
// open of a sensitive credential path must pass. Without a dialog (headless
// mode) nothing is granted.
func (g *Gateway) SetFileAccessConfirmation(confirm func(context.Context, FileAccessConfirmation) (bool, error)) {
	g.originMu.Lock()
	g.confirmFileAccess = confirm
	g.originMu.Unlock()
}

// confirmSensitiveFileAccess asks the desktop user before reading or opening a
// path the credential policy protects. It fails closed: no dialog, a remote
// request, a declined answer, an error or a cancelled context all return false.
// The answer is never remembered and prompts never stack.
func (g *Gateway) confirmSensitiveFileAccess(r *http.Request, targetPath, action, locale string) bool {
	if isRemoteRequest(r) || strings.TrimSpace(targetPath) == "" {
		return false
	}
	g.originMu.RLock()
	confirm := g.confirmFileAccess
	g.originMu.RUnlock()
	if confirm == nil {
		return false
	}
	if !g.fileAccessConfirming.CompareAndSwap(false, true) {
		return false
	}
	defer g.fileAccessConfirming.Store(false)
	if r.Context().Err() != nil {
		return false
	}
	accepted, err := confirm(r.Context(), FileAccessConfirmation{Path: targetPath, Action: action, Locale: strings.TrimSpace(locale)})
	return err == nil && accepted && r.Context().Err() == nil
}

// fileAccessLocale reads the optional ?locale= hint the desktop UI can send so
// the native dialog matches the user's UI language.
func fileAccessLocale(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("locale"))
}

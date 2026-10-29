package browser

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// SetProfileRoot configures the base directory for persistent desktop Chrome
// profiles (for example ~/.hoxi/browser-profile). An empty path disables
// persistent profiles and falls back to temporary profiles.
func (s *Service) SetProfileRoot(dir string) {
	dir = strings.TrimSpace(dir)
	var persistedToken string
	if dir != "" {
		tokenPath := filepath.Join(dir, ".hoxi-extension-token")
		if raw, err := os.ReadFile(tokenPath); err == nil {
			candidate := strings.TrimSpace(string(raw))
			if len(candidate) == 64 && isHexToken(candidate) {
				persistedToken = candidate
			}
		}
	}
	s.mu.Lock()
	s.profileRoot = dir
	if persistedToken != "" {
		s.linkToken = persistedToken
	}
	s.mu.Unlock()
}

func (s *Service) persistLinkToken() {
	s.mu.Lock()
	dir := s.profileRoot
	token := s.linkToken
	s.mu.Unlock()
	if dir == "" || len(token) != 64 {
		return
	}
	tokenPath := filepath.Join(dir, ".hoxi-extension-token")
	if _, err := os.Stat(tokenPath); err == nil {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err == nil {
		_ = os.WriteFile(tokenPath, []byte(token+"\n"), 0o600)
	}
}

func isHexToken(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return len(s) > 0
}

// SetDefaultWindowMode controls whether new scopes default to launching a
// visible desktop Chrome window with a persistent profile instead of a
// headless temporary browser. Setting HOXI_BROWSER_HEADLESS=1 forces headless.
func (s *Service) SetDefaultWindowMode(window bool) {
	if os.Getenv("HOXI_BROWSER_HEADLESS") == "1" {
		window = false
	}
	s.mu.Lock()
	s.defaultWindow = window
	s.mu.Unlock()
}

func (s *Service) defaultModeWindow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.defaultWindow
}

// acquirePersistentProfile selects and prepares the persistent profile
// directory for sc. When another idle scope is holding the primary profile,
// its Chrome is closed first so the user's primary login state can be reused
// without a Chrome SingletonLock conflict.
func (s *Service) acquirePersistentProfile(sc *scope) (string, error) {
	s.mu.Lock()
	root := s.profileRoot
	var others []*scope
	for _, other := range s.scopes {
		if other != sc {
			others = append(others, other)
		}
	}
	s.mu.Unlock()
	if root == "" {
		return "", nil
	}
	root = filepath.Clean(root)
	busyPrimary := false
	for _, other := range others {
		select {
		case other.gate <- struct{}{}:
			if !other.closed && other.browser != nil && other.profile == root {
				if other.manual {
					busyPrimary = true
				} else {
					other.resetBrowser()
				}
			}
			<-other.gate
		default:
			// Another scope is actively executing an operation; do not block
			// on its gate (which could deadlock) or steal its profile.
			busyPrimary = true
		}
	}
	profile := root
	if busyPrimary {
		session := sc.sessionID
		if session == "" {
			session = "default"
		}
		profile = filepath.Join(root+"-sessions", session)
	}
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return "", err
	}
	cleanStaleSingletonLocks(profile)
	return profile, nil
}

// cleanStaleSingletonLocks removes leftover Chromium Singleton* lock files
// before starting a fresh Chrome process on profile.
func cleanStaleSingletonLocks(profile string) {
	for _, name := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		_ = os.Remove(filepath.Join(profile, name))
	}
}

// resetBrowser tears down a scope's Chrome process while keeping the scope's
// mode and manual control settings intact. Persistent profile directories are
// preserved on disk.
func (sc *scope) resetBrowser() {
	if sc.stream != nil {
		sc.stream.cancel()
		sc.stream = nil
	}
	if sc.pointerState.timer != nil {
		sc.pointerState.timer.Stop()
	}
	for _, n := range sc.navigations {
		n.stop()
	}
	clear(sc.navigations)
	for _, cancel := range sc.tabCancels {
		cancel()
	}
	if sc.browserCancel != nil {
		sc.browserCancel()
		sc.browserCancel = nil
	}
	clear(sc.tabs)
	clear(sc.tabCancels)
	clear(sc.viewports)
	clear(sc.diagnostics)
	if sc.allocatorCancel != nil {
		sc.allocatorCancel()
		sc.allocatorCancel = nil
	}
	sc.browser = nil
	sc.tabID = ""
	if sc.persistentProfile {
		sc.ledger.Forget(sc.ledgerID)
		sc.ledgerID = ""
		sc.profile = ""
		sc.persistentProfile = false
	} else if sc.profile != "" {
		if os.RemoveAll(sc.profile) == nil {
			sc.ledger.Forget(sc.ledgerID)
			sc.ledgerID = ""
		}
		sc.profile = ""
	}
	if sc.workflows != nil && sc.workflows.root != "" {
		if os.RemoveAll(sc.workflows.root) == nil {
			sc.ledger.Forget(sc.transferLedgerID)
			sc.transferLedgerID = ""
		}
		sc.workflows.mu.Lock()
		sc.workflows.root = ""
		clear(sc.workflows.uploads)
		clear(sc.workflows.dialogs)
		clear(sc.workflows.tabs)
		sc.workflows.mu.Unlock()
	}
}

// focusWindow activates the selected tab and brings the desktop Chrome window
// to the foreground.
func (sc *scope) focusWindow(ctx context.Context) error {
	if sc.tabID != "" {
		if err := sc.withTab(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			return target.ActivateTarget(target.ID(sc.tabID)).Do(ctx)
		})); err != nil {
			return err
		}
	}
	if !sc.window || sc.browser == nil {
		return nil
	}
	proc := chromedp.FromContext(sc.browser).Browser.Process()
	if proc == nil || proc.Pid <= 0 {
		return nil
	}
	focusOSProcess(proc.Pid)
	return nil
}

func focusOSProcess(pid int) {
	if runtime.GOOS != "darwin" || pid <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	script := fmt.Sprintf(`tell application "System Events" to set frontmost of first process whose unix id is %d to true`, pid)
	_ = exec.CommandContext(ctx, "osascript", "-e", script).Run()
}

package browser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/subproc"
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
	_ = hoxipaths.WriteFileAtomic(tokenPath, []byte(token+"\n"), 0o600)
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

// allocatorOptions builds Chrome launch flags. Desktop window mode avoids
// chromedp's headless test flags (--enable-automation, --disable-extensions,
// --use-mock-keychain, --disable-features=site-per-process, etc.) so persistent
// profiles behave like a normal desktop Chrome window.
func (sc *scope) allocatorOptions(profile string) []chromedp.ExecAllocatorOption {
	var opts []chromedp.ExecAllocatorOption
	if sc.window {
		opts = []chromedp.ExecAllocatorOption{
			chromedp.NoFirstRun,
			chromedp.NoDefaultBrowserCheck,
			chromedp.Flag("disable-blink-features", "AutomationControlled"),
		}
	} else {
		opts = append(opts, chromedp.DefaultExecAllocatorOptions[:]...)
	}
	opts = append(opts,
		chromedp.UserDataDir(profile),
		chromedp.WindowSize(sc.width, sc.height),
		// Back/forward-cache restores fire no document load, so chromedp's
		// history actions would wait until the request deadline.
		chromedp.Flag("disable-back-forward-cache", true),
		chromedp.Flag("disk-cache-size", strconv.Itoa(diskCacheBytes)),
	)
	if sc.proxyBridge != nil && sc.proxyBridge.Addr() != "" {
		opts = append(opts,
			chromedp.Flag("proxy-server", "http://"+sc.proxyBridge.Addr()),
			chromedp.Flag("force-webrtc-ip-handling-policy", "disable_non_proxied_udp"),
			chromedp.Flag("webrtc-ip-handling-policy", "disable_non_proxied_udp"),
		)
	}
	if loc := strings.TrimSpace(sc.env.Locale); loc != "" {
		opts = append(opts, chromedp.Flag("lang", loc))
	}
	if tz := strings.TrimSpace(sc.env.Timezone); tz != "" {
		opts = append(opts, chromedp.Env("TZ="+tz))
	}
	if execPath := resolveBrowserExecPath(); execPath != "" {
		opts = append(opts, chromedp.ExecPath(execPath))
	}
	return opts
}

func (s *Service) desiredProfileRoot(sc *scope) string {
	s.mu.Lock()
	root := s.profileRoot
	resolveProfile := s.resolveProfile
	s.mu.Unlock()
	if root == "" {
		return ""
	}
	root = filepath.Clean(root)
	if resolveProfile != nil {
		if subKey := sanitizeProfileSubKey(resolveProfile(sc.sessionID, sc.directory)); subKey != "" && subKey != "default" {
			root = filepath.Join(root+"-stores", subKey)
		}
	}
	return root
}

// acquirePersistentProfile selects and prepares the persistent profile
// directory for sc. When another idle scope is holding the primary profile,
// its Chrome is closed first so the user's primary login state can be reused
// without a Chrome SingletonLock conflict.
func (s *Service) acquirePersistentProfile(sc *scope) (string, error) {
	root := s.desiredProfileRoot(sc)
	if root == "" {
		return "", nil
	}
	s.mu.Lock()
	var others []*scope
	for _, other := range s.scopes {
		if other != sc {
			others = append(others, other)
		}
	}
	s.mu.Unlock()
	busyPrimary := false
	usedFallback := make(map[string]bool)
	for _, other := range others {
		if s.desiredProfileRoot(other) != root {
			continue
		}
		select {
		case other.gate <- struct{}{}:
			if !other.closed && other.browser != nil {
				if other.profile == root {
					if other.manual {
						busyPrimary = true
					} else {
						other.resetBrowser()
					}
				} else if other.profile != "" {
					usedFallback[other.profile] = true
				}
			}
			<-other.gate
		default:
			// Another scope sharing this profile root is actively executing an
			// operation; do not block on its gate (which could deadlock) or steal
			// its profile.
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
		for suffix := 2; usedFallback[profile]; suffix++ {
			profile = filepath.Join(root+"-sessions", fmt.Sprintf("%s-%d", session, suffix))
		}
	}
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return "", err
	}
	sc.profileRootUsed = root
	cleanStaleSingletonLocks(profile)
	return profile, nil
}

func sanitizeProfileSubKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 96 || strings.Contains(raw, "..") {
		return ""
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		isAlphaNum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if i == 0 && !isAlphaNum {
			return ""
		}
		if !isAlphaNum && c != '_' && c != '-' && c != '.' {
			return ""
		}
	}
	return raw
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
	clear(sc.sessionTabs)
	clear(sc.tabOwners)
	clear(sc.tabCancels)
	clear(sc.viewports)
	clear(sc.diagnostics)
	if sc.allocatorCancel != nil {
		sc.allocatorCancel()
		sc.allocatorCancel = nil
	}
	sc.browser = nil
	sc.tabID = ""
	sc.profileRootUsed = ""
	if sc.proxyBridge != nil {
		sc.proxyBridge.Close()
		sc.proxyBridge = nil
	}
	sc.env = StoreEnvConfig{}
	sc.envFingerprint = ""
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
	if pid <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`tell application "System Events" to set frontmost of first process whose unix id is %d to true`, pid)
		_ = subproc.CommandContext(ctx, "osascript", "-e", script).Run()
	case "windows":
		script := fmt.Sprintf(`(New-Object -ComObject WScript.Shell).AppActivate(%d) | Out-Null`, pid)
		_ = subproc.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script).Run()
	}
}

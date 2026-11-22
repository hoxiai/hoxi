package runner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/sandbox"
)

// confineRuntimeCommand wraps a native runtime subprocess in the OS sandbox.
// The runtime itself still needs outbound network access for model calls, so
// only the filesystem boundary is enforced here.
func (s *Service) confineRuntimeCommand(rec SessionRecord, bin string, args []string) (string, []string, error) {
	if !rec.CommandSandbox {
		return bin, args, nil
	}
	if !sandbox.Available() {
		return "", nil, errors.New(sandbox.UnavailableReason())
	}
	extraWritable := []string{writableGitDir(rec.ProjectDir)}
	if rec.Runtime == string(RuntimeGeminiCLI) {
		extraWritable = append(extraWritable, geminiSandboxWritableDirs()...)
	}
	profile, err := sandbox.DefaultProfile(rec.WorktreePath, sandbox.NetworkAllow, extraWritable...)
	if err != nil {
		return "", nil, err
	}
	if bundle, bErr := s.ResolveSessionBundle(rec.ID, rec.WorktreePath); bErr == nil {
		if len(bundle.AppIDs) > 0 {
			if withApps, aErr := sandbox.WithWorkAppPackageAccess(profile, bundle.AppIDs...); aErr == nil {
				profile = withApps
			}
		}
		if len(bundle.ReadOnlyExecRoots) > 0 {
			if withRoots, rErr := sandbox.WithReadOnlyExecRoots(profile, bundle.ReadOnlyExecRoots...); rErr == nil {
				profile = withRoots
			}
		}
	}
	return sandbox.WrapExec(profile, bin, args)
}

// confineNativeCommandWithOnDemand wraps one native bash command in an approved
// on-demand Seatbelt profile when present (replacing any session-wide profile
// so Seatbelt is never nested), or falls back to confineRuntimeCommand.
func (s *Service) confineNativeCommandWithOnDemand(rec SessionRecord, onDemandProfile, bin string, args []string) (string, []string, error) {
	return s.confineNativeCommandWithOnDemandIn(rec, rec.WorktreePath, onDemandProfile, bin, args)
}

func (s *Service) confineNativeCommandWithOnDemandIn(rec SessionRecord, rootDir, onDemandProfile, bin string, args []string) (string, []string, error) {
	if strings.TrimSpace(onDemandProfile) != "" {
		if !sandbox.Available() {
			return "", nil, errors.New(sandbox.UnavailableReason())
		}
		trustedRoot := firstNonEmptyStr(strings.TrimSpace(rootDir), rec.WorktreePath)
		if !sandbox.NewManager("").OnDemandTrusted(onDemandProfile, trustedRoot) {
			return "", nil, errors.New("on-demand sandbox profile is not trusted for this workspace")
		}
		wrapped := make([]string, 0, 4+len(args))
		wrapped = append(wrapped, "-f", onDemandProfile, "--", bin)
		wrapped = append(wrapped, args...)
		return sandboxExecPath, wrapped, nil
	}
	return s.confineRuntimeCommand(rec, bin, args)
}

func geminiSandboxWritableDirs() []string {
	var dirs []string
	if hoxi := strings.TrimSpace(hoxipaths.Home()); hoxi != "" {
		sandboxDir := filepath.Join(hoxi, "sandbox")
		_ = os.MkdirAll(sandboxDir, 0o700)
		dirs = append(dirs, sandboxDir)
	}
	if gRoot := strings.TrimSpace(geminiHomeDir()); gRoot != "" {
		_ = os.MkdirAll(gRoot, 0o700)
		dirs = append(dirs, gRoot)
		for _, sub := range []string{"tmp", "history"} {
			p := filepath.Join(gRoot, sub)
			_ = os.MkdirAll(p, 0o700)
			dirs = append(dirs, p)
		}
	}
	return dirs
}

// syncOpenCodeCommandSandbox publishes the session's sandbox choice to the
// OpenCode sandbox plugin, which looks policies up by the daemon's own session
// ID rather than Hoxi's conversation ID.
func syncOpenCodeCommandSandbox(rec SessionRecord) error {
	if rec.ExternalID == "" {
		return nil
	}
	manager := sandbox.NewManager("")
	if !rec.CommandSandbox {
		return manager.Remove(rec.ExternalID)
	}
	if !sandbox.Available() {
		return errors.New(sandbox.UnavailableReason())
	}
	_, err := manager.Set(rec.ExternalID, rec.WorktreePath, true, sandbox.NetworkAllow)
	return err
}

// writableGitDir keeps git metadata writable for worktree sessions, whose
// actual git directory lives under the main project instead of the worktree.
func writableGitDir(projectDir string) string {
	dir := filepath.Join(projectDir, ".git")
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return dir
	}
	return ""
}

package gateway

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/skills"
)

func (g *Gateway) allowedWorkspaceRoots() []string {
	var roots []string
	seen := make(map[string]bool)
	add := func(p string) {
		c, ok := canonicalDirectory(p)
		if !ok || c == "" || seen[c] {
			return
		}
		seen[c] = true
		roots = append(roots, c)
	}
	if g.rootDir != "" {
		add(g.rootDir)
	}
	if g.workspaces != nil {
		for _, ws := range g.workspaces.List() {
			for _, f := range ws.Folders {
				add(f.Path)
			}
		}
	}
	if def := hoxipaths.DefaultWorkspaceDir(); def != "" {
		add(def)
	}
	return roots
}

func (g *Gateway) isRegisteredWorkspaceOrSandbox(dir string, allowedRoots []string) (canonical string, projectRoot string, ok bool) {
	cDir, valid := canonicalDirectory(dir)
	if !valid || cDir == "" {
		return "", "", false
	}
	for _, root := range allowedRoots {
		if cDir == root {
			return cDir, root, true
		}
		// Allow .hoxi/worktrees/<session> under a registered workspace root.
		wtRoot := filepath.Join(root, ".hoxi", "worktrees")
		if isPathWithinDir(wtRoot, cDir) {
			return cDir, root, true
		}
	}
	// Allow Scheme C per-app default workspace subdirectories directly under ~/.hoxi/workspaces/default/<appId>.
	if defDir, ok := canonicalDirectory(hoxipaths.DefaultWorkspaceDir()); ok && defDir != "" {
		if rel, err := filepath.Rel(defDir, cDir); err == nil && rel != "." && rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator)) &&
			!strings.Contains(filepath.ToSlash(rel), "/") {
			return cDir, defDir, true
		}
	}
	return "", "", false
}

// skillSurface reports "work" when dirs belong to a Work workspace (a saved
// profile marked work, or the default Work directory); otherwise "code".
func (g *Gateway) skillSurface(dirs ...string) string {
	if g.workspaces != nil {
		for _, ws := range g.workspaces.List() {
			if ws.Surface != "work" {
				continue
			}
			for _, f := range ws.Folders {
				for _, d := range dirs {
					if d != "" && sameDirectory(f.Path, d) {
						return "work"
					}
				}
			}
		}
	}
	for _, d := range dirs {
		if d != "" && runner.NormalizeSessionSurface("", d) == "work" {
			return "work"
		}
	}
	return "code"
}

func (g *Gateway) resolveSkillContext(ctx context.Context, directory, sessionID, requestedRuntime string) (skills.RequestContext, error) {
	allowedRoots := g.allowedWorkspaceRoots()
	sessionID = strings.TrimSpace(sessionID)
	directory = strings.TrimSpace(directory)

	if sessionID != "" {
		if g.runner != nil {
			rt, effDir, projDir, found, err := g.runner.ResolveSessionContext(ctx, sessionID, directory)
			if err != nil {
				return skills.RequestContext{}, fmt.Errorf("%w: %s", skills.ErrForbidden, err.Error())
			}
			if found {
				if reqRT := strings.TrimSpace(requestedRuntime); reqRT != "" && (reqRT == "opencode" || runner.IsValidRuntime(reqRT)) {
					rt = reqRT
				}
				var extraSkillDirs []string
				if bundle, bErr := g.runner.ResolveSessionBundle(sessionID, effDir); bErr == nil && len(bundle.ExtraSkillDirs) > 0 {
					extraSkillDirs = bundle.ExtraSkillDirs
				}
				var extraMCPServers []string
				if dynMCP := g.extensionSessionMCPServers(sessionID, effDir); len(dynMCP) > 0 {
					extraMCPServers = make([]string, 0, len(dynMCP))
					for name := range dynMCP {
						extraMCPServers = append(extraMCPServers, name)
					}
					sort.Strings(extraMCPServers)
				}
				surface := g.skillSurface(projDir, effDir)
				if g.runner.IsAppDevSession(sessionID) {
					surface = "code"
				}
				return skills.RequestContext{
					SessionID:       sessionID,
					Runtime:         rt,
					Directory:       effDir,
					ProjectRoot:     projDir,
					AllowedRoots:    allowedRoots,
					ExtraSkillDirs:  extraSkillDirs,
					ExtraMCPServers: extraMCPServers,
					Surface:         surface,
				}, nil
			}
		}
		if g.worktrees != nil {
			binding, known, err := g.worktrees.BindingForSession(sessionID)
			if err != nil {
				return skills.RequestContext{}, fmt.Errorf("%w: %s", skills.ErrForbidden, err.Error())
			}
			if known && binding.Directory != "" {
				if directory != "" && !sameDirectory(directory, binding.Directory) {
					return skills.RequestContext{}, fmt.Errorf("%w: session directory does not match its sandbox", skills.ErrForbidden)
				}
				proj := g.worktrees.ForSession(sessionID).RootDir()
				if proj == "" {
					proj = binding.Directory
				}
				return skills.RequestContext{
					SessionID:    sessionID,
					Runtime:      g.defaultSkillRuntime(),
					Directory:    binding.Directory,
					ProjectRoot:  proj,
					AllowedRoots: allowedRoots,
					Surface:      g.skillSurface(proj, binding.Directory),
				}, nil
			}
		}
		return skills.RequestContext{}, fmt.Errorf("%w: session %q not found", skills.ErrNotFound, sessionID)
	}

	targetDir := directory
	if targetDir == "" {
		targetDir = g.rootDir
	}
	if targetDir == "" && len(allowedRoots) > 0 {
		targetDir = allowedRoots[0]
	}
	if targetDir == "" {
		return skills.RequestContext{}, fmt.Errorf("%w: workspace directory is required", skills.ErrBadRequest)
	}

	cDir, projRoot, ok := g.isRegisteredWorkspaceOrSandbox(targetDir, allowedRoots)
	if !ok {
		return skills.RequestContext{}, fmt.Errorf("%w: directory is not a registered workspace", skills.ErrForbidden)
	}

	rt := g.defaultSkillRuntime()
	if reqRT := strings.TrimSpace(requestedRuntime); reqRT != "" && (reqRT == "opencode" || runner.IsValidRuntime(reqRT)) {
		rt = reqRT
	}

	surface := g.skillSurface(projRoot, cDir)
	var extraSkillDirs []string
	if surface == "work" && g.runner != nil {
		if bundle, bErr := g.runner.ResolveSessionBundle("", cDir); bErr == nil && len(bundle.ExtraSkillDirs) > 0 {
			extraSkillDirs = bundle.ExtraSkillDirs
		}
	}

	return skills.RequestContext{
		Runtime:        rt,
		Directory:      cDir,
		ProjectRoot:    projRoot,
		AllowedRoots:   allowedRoots,
		ExtraSkillDirs: extraSkillDirs,
		Surface:        surface,
	}, nil
}

func (g *Gateway) defaultSkillRuntime() string {
	if g != nil && g.openCode != nil && g.openCode.Available() {
		return "opencode"
	}
	return string(runner.RuntimeNative)
}

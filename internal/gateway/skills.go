package gateway

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

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
	return "", "", false
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
				return skills.RequestContext{
					SessionID:    sessionID,
					Runtime:      rt,
					Directory:    effDir,
					ProjectRoot:  projDir,
					AllowedRoots: allowedRoots,
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
					Runtime:      "opencode",
					Directory:    binding.Directory,
					ProjectRoot:  proj,
					AllowedRoots: allowedRoots,
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

	rt := "opencode"
	if reqRT := strings.TrimSpace(requestedRuntime); reqRT != "" && reqRT != "opencode" {
		rt = reqRT
	}

	return skills.RequestContext{
		Runtime:      rt,
		Directory:    cDir,
		ProjectRoot:  projRoot,
		AllowedRoots: allowedRoots,
	}, nil
}

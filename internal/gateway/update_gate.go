package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func updateMutation(r *http.Request) bool {
	return r.Method != http.MethodGet && r.Method != http.MethodOptions &&
		r.URL.Path != "/api/hoxi/updater" && !strings.HasPrefix(r.URL.Path, "/api/hoxi/updater/")
}

func (g *Gateway) admitUpdateRequest(w http.ResponseWriter, r *http.Request) bool {
	g.updateMu.Lock()
	if !updateMutation(r) {
		g.updateMu.Unlock()
		return true
	}
	if g.updateDraining {
		g.updateMu.Unlock()
		writeJSONError(w, http.StatusServiceUnavailable, "update in progress")
		return false
	}
	g.updateMutations++
	g.updateMu.Unlock()
	return true
}

func (g *Gateway) finishUpdateRequest() {
	g.updateMu.Lock()
	g.updateMutations--
	g.updateMu.Unlock()
}

func (g *Gateway) rememberUpdateDirectory(r *http.Request) {
	if g.updateDirectories == nil {
		g.updateDirectories = make(map[string]bool)
	}
	dir, present, err := directoryQuery(r.URL.RawQuery)
	if err != nil {
		return
	}
	header := r.Header.Get(sessionDirectoryHeader)
	if present && header != "" && !sameDirectory(dir, header) {
		return
	}
	if !present {
		dir = header
	}
	if directorySelectorValid(dir) {
		g.updateDirectories[filepath.Clean(dir)] = true
	}
}

func (g *Gateway) observeUpdateDirectory(r *http.Request) {
	g.updateMu.Lock()
	g.rememberUpdateDirectory(r)
	g.updateMu.Unlock()
}

func (g *Gateway) updateLocallyIdle() bool {
	if g.status == nil {
		return false
	}
	st := g.status.Snapshot()
	return st.Available && st.Running == 0 && st.Attention == 0 &&
		(g.runner == nil || g.runner.ActiveProcessCount() == 0)
}

func (g *Gateway) isAnySessionBusy() bool {
	if g.runner != nil && g.runner.ActiveProcessCount() > 0 {
		return true
	}
	if g.status != nil {
		st := g.status.Snapshot()
		if st.Running > 0 || st.Attention > 0 {
			return true
		}
	}
	if g.openCodePort <= 0 {
		return false
	}
	g.updateMu.Lock()
	dirs := make([]string, 0, len(g.updateDirectories)+1)
	if strings.TrimSpace(g.rootDir) != "" {
		dirs = append(dirs, filepath.Clean(g.rootDir))
	}
	for dir := range g.updateDirectories {
		if dir != "" && dir != filepath.Clean(g.rootDir) {
			dirs = append(dirs, dir)
		}
	}
	g.updateMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	var rt http.RoundTripper
	if g.proxy != nil {
		rt = g.proxy.Transport
	}
	client := &http.Client{
		Timeout:   1500 * time.Millisecond,
		Transport: rt,
	}
	for _, dir := range dirs {
		if updateDirectoryGone(dir) {
			continue
		}
		if g.openCodeDirectoryHasActiveSession(ctx, client, dir) {
			return true
		}
	}
	return false
}

func (g *Gateway) openCodeDirectoryHasActiveSession(ctx context.Context, client *http.Client, dir string) bool {
	endpoint := "http://127.0.0.1:" + strconv.Itoa(g.openCodePort) + "/session/status?" + url.Values{"directory": {dir}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}
	req.SetBasicAuth("opencode", g.password)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	const maxBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		return false
	}
	var statuses map[string]struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(body, &statuses) != nil || statuses == nil {
		return false
	}
	for _, status := range statuses {
		if status.Type != "" && status.Type != "idle" {
			return true
		}
	}
	return false
}

var updateStatusTimeout = 2 * time.Second

// BeginUpdate closes mutation admission before consulting authoritative runtime
// state. Repeated calls revalidate, preserving an already-held drain on failure.
func (g *Gateway) BeginUpdate() bool {
	g.updateCheckMu.Lock()
	defer g.updateCheckMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), updateStatusTimeout)
	defer cancel()
	g.updateMu.Lock()
	wasDraining := g.updateDraining
	if g.updateMutations != 0 || !g.updateLocallyIdle() {
		g.updateMu.Unlock()
		return false
	}
	g.updateDraining = true
	g.updateMu.Unlock()
	ok := false
	defer func() {
		if !ok && !wasDraining {
			g.updateMu.Lock()
			g.updateDraining = false
			g.updateMu.Unlock()
		}
	}()

	roots := []string{g.rootDir}
	if g.workspaces != nil {
		for _, ws := range g.workspaces.List() {
			for _, folder := range ws.Folders {
				roots = append(roots, folder.Path)
			}
		}
	}
	if g.worktrees != nil {
		var err error
		roots, err = g.worktrees.UpdateDirectories(roots)
		if err != nil {
			return false
		}
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	checked := make(map[string]bool)
	for {
		g.updateMu.Lock()
		for dir := range g.updateDirectories {
			if updateDirectoryGone(dir) {
				delete(g.updateDirectories, dir)
				continue
			}
			roots = append(roots, dir)
		}
		g.updateMu.Unlock()
		for _, dir := range roots {
			if dir == "" || checked[dir] {
				continue
			}
			if updateDirectoryGone(dir) {
				checked[dir] = true
				continue
			}
			if !g.updateDirectoryIdle(ctx, client, dir) {
				if updateDirectoryGone(dir) {
					checked[dir] = true
					continue
				}
				return false
			}
			checked[dir] = true
		}
		g.updateMu.Lock()
		complete := true
		for dir := range g.updateDirectories {
			if !checked[dir] {
				complete = false
				break
			}
		}
		if complete {
			ok = len(checked) > 0 && ctx.Err() == nil && g.updateMutations == 0 && g.updateLocallyIdle()
		}
		g.updateMu.Unlock()
		if complete || ctx.Err() != nil {
			return ok
		}
	}
}

// BeginUpdateForced closes mutation admission even when agent sessions are
// still running. The caller must have confirmed the interruption with the user.
// Existing in-flight mutations may finish, but new mutations are rejected.
func (g *Gateway) BeginUpdateForced() bool {
	g.updateCheckMu.Lock()
	defer g.updateCheckMu.Unlock()
	g.updateMu.Lock()
	g.updateDraining = true
	g.updateMu.Unlock()
	return true
}

func updateDirectoryGone(dir string) bool {
	info, err := os.Stat(dir)
	return os.IsNotExist(err) || (err == nil && !info.IsDir())
}

func (g *Gateway) updateDirectoryIdle(ctx context.Context, client *http.Client, dir string) bool {
	endpoint := "http://127.0.0.1:" + strconv.Itoa(g.openCodePort) + "/session/status?" + url.Values{"directory": {dir}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}
	req.SetBasicAuth("opencode", g.password)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	const maxBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		return false
	}
	var statuses map[string]struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(body, &statuses) != nil || statuses == nil {
		return false
	}
	for _, status := range statuses {
		if status.Type != "idle" {
			return false
		}
	}
	return true
}

func (g *Gateway) ReleaseUpdate() {
	g.updateCheckMu.Lock()
	defer g.updateCheckMu.Unlock()
	g.updateMu.Lock()
	g.updateDraining = false
	g.updateMu.Unlock()
}

func (g *Gateway) UpdateDraining() bool {
	g.updateMu.Lock()
	defer g.updateMu.Unlock()
	return g.updateDraining
}

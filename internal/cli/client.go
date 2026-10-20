package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/handoff"
	"github.com/hoxiai/hoxi/internal/runner"
)

// FileDiff represents a file diff item returned by /api/hoxi/worktree/diff.
type FileDiff struct {
	File      string `json:"file"`
	Patch     string `json:"patch,omitempty"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Status    string `json:"status,omitempty"`
}

// SandboxStatus represents the status returned by /api/hoxi/worktree/status.
type SandboxStatus struct {
	SessionID    string `json:"sessionID"`
	Isolated     bool   `json:"isolated"`
	Directory    string `json:"directory"`
	Branch       string `json:"branch,omitempty"`
	ChangedFiles int    `json:"changedFiles"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
}

// Client communicates with a local Hoxi Gateway over loopback HTTP/SSE.
type Client struct {
	baseURL  string
	attached bool
	token    string
	http     *http.Client
}

// NewClient creates a loopback gateway client that bypasses system HTTP_PROXY.
func NewClient(baseURL string, attached bool) *Client {
	u := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "http://" + u
	}
	return &Client{
		baseURL:  u,
		attached: attached,
		http: &http.Client{
			Transport: &http.Transport{
				Proxy: nil,
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				IdleConnTimeout: 90 * time.Second,
			},
		},
	}
}

func (c *Client) BaseURL() string  { return c.baseURL }
func (c *Client) IsAttached() bool { return c.attached }

// ConnectOrSpawn attaches to an already-running local Hoxi Gateway if reachable,
// or boots an in-process headless gateway when none is running.
func ConnectOrSpawn(
	ctx context.Context,
	preferredAddr string,
	rootDir string,
	bootHeadless HeadlessBootFunc,
) (*Client, func(), error) {
	candidates := []string{}
	if strings.TrimSpace(preferredAddr) != "" {
		candidates = append(candidates, strings.TrimSpace(preferredAddr))
	}
	for _, fallback := range []string{"127.0.0.1:5410", "127.0.0.1:5420"} {
		if fallback != strings.TrimSpace(preferredAddr) {
			candidates = append(candidates, fallback)
		}
	}

	for _, addr := range candidates {
		c := NewClient(addr, true)
		if c.Healthy(ctx) {
			c.ensureGatewayToken(ctx)
			return c, func() {}, nil
		}
	}

	if bootHeadless == nil {
		return nil, nil, fmt.Errorf("no running Hoxi gateway found at %s and headless boot is unavailable", preferredAddr)
	}
	baseURL, cleanup, err := bootHeadless(ctx, rootDir)
	if err != nil {
		return nil, nil, err
	}
	c := NewClient(baseURL, false)
	c.ensureGatewayToken(ctx)
	return c, cleanup, nil
}

// Healthy checks whether the gateway responds 200 OK on /healthz.
func (c *Client) Healthy(ctx context.Context) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 600*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ensureGatewayToken captures the per-boot gateway token advertised by
// /api/hoxi/bootstrap when the gateway requires one.
func (c *Client) ensureGatewayToken(ctx context.Context) {
	var boot struct {
		GatewayToken string `json:"gatewayToken"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/hoxi/bootstrap", nil, &boot); err == nil && strings.TrimSpace(boot.GatewayToken) != "" {
		c.token = strings.TrimSpace(boot.GatewayToken)
	}
}

// CLIModelItem describes an available model across OpenCode or a native CLI runtime.
type CLIModelItem struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
	Name       string `json:"name"`
	Default    bool   `json:"default"`
}

func (c *Client) ListModels(ctx context.Context, directory, runtime string) ([]CLIModelItem, string, string, error) {
	rt := NormalizeRuntime(runtime)
	if rt != "opencode" {
		var resp struct {
			Default string `json:"default"`
			Models  []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := c.doJSON(ctx, http.MethodGet, "/api/hoxi/runner/"+url.PathEscape(rt)+"/models", nil, &resp); err != nil {
			return nil, "", "", err
		}
		items := make([]CLIModelItem, 0, len(resp.Models))
		defModel := strings.TrimSpace(resp.Default)
		for _, m := range resp.Models {
			if strings.TrimSpace(m.ID) == "" {
				continue
			}
			if defModel == "" {
				defModel = m.ID
			}
			items = append(items, CLIModelItem{
				ProviderID: rt,
				ModelID:    m.ID,
				Name:       firstNonEmpty(m.Name, m.ID),
				Default:    m.ID == defModel,
			})
		}
		return items, rt, defModel, nil
	}

	path := "/config/providers"
	if strings.TrimSpace(directory) != "" {
		path += "?directory=" + url.QueryEscape(directory)
	}
	var resp struct {
		Providers []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Source string `json:"source"`
			Key    string `json:"key"`
			Models map[string]struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"models"`
		} `json:"providers"`
		Default map[string]string `json:"default"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, "", "", err
	}

	defProvider := ""
	defModel := ""
	for _, p := range resp.Providers {
		if p.ID == "opencode" && p.Source != "config" && p.Key == "" {
			continue
		}
		if d := strings.TrimSpace(resp.Default[p.ID]); d != "" {
			if _, ok := p.Models[d]; ok || len(p.Models) == 0 {
				defProvider = p.ID
				defModel = d
				break
			}
		}
		for mID := range p.Models {
			defProvider = p.ID
			defModel = mID
			break
		}
		if defProvider != "" {
			break
		}
	}
	if defProvider == "" {
		for _, p := range resp.Providers {
			if d := strings.TrimSpace(resp.Default[p.ID]); d != "" {
				defProvider = p.ID
				defModel = d
				break
			}
		}
	}

	var items []CLIModelItem
	for _, p := range resp.Providers {
		for mID, m := range p.Models {
			items = append(items, CLIModelItem{
				ProviderID: p.ID,
				ModelID:    mID,
				Name:       firstNonEmpty(m.Name, mID),
				Default:    p.ID == defProvider && mID == defModel,
			})
		}
	}
	return items, defProvider, defModel, nil
}

func (c *Client) ResolveModel(ctx context.Context, directory, runtime, providerID, modelID string) (string, string) {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" && strings.Contains(modelID, "/") {
		parts := strings.SplitN(modelID, "/", 2)
		if strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" {
			providerID = strings.TrimSpace(parts[0])
			modelID = strings.TrimSpace(parts[1])
		}
	}
	if NormalizeRuntime(runtime) != "opencode" {
		if modelID != "" {
			return providerID, modelID
		}
		_, _, defModel, err := c.ListModels(ctx, directory, runtime)
		if err == nil && defModel != "" {
			return providerID, defModel
		}
		return providerID, modelID
	}
	if providerID != "" && modelID != "" {
		return providerID, modelID
	}
	items, defProv, defMod, err := c.ListModels(ctx, directory, "opencode")
	if err != nil {
		return providerID, modelID
	}
	if modelID != "" && providerID == "" {
		for _, it := range items {
			if it.ModelID == modelID {
				return it.ProviderID, modelID
			}
		}
		return defProv, modelID
	}
	if providerID != "" && modelID == "" {
		for _, it := range items {
			if it.ProviderID == providerID && it.Default {
				return providerID, it.ModelID
			}
		}
		for _, it := range items {
			if it.ProviderID == providerID {
				return providerID, it.ModelID
			}
		}
		return providerID, modelID
	}
	return defProv, defMod
}

func (c *Client) CreateSession(ctx context.Context, opts Options) (*runner.SessionInfo, error) {
	provID, modID := c.ResolveModel(ctx, opts.Directory, opts.Runtime, opts.ProviderID, opts.ModelID)
	payload := map[string]interface{}{
		"directory":   opts.Directory,
		"runtime":     opts.Runtime,
		"modelID":     modID,
		"providerID":  provID,
		"mode":        opts.Mode,
		"sandboxMode": opts.SandboxMode,
	}
	var info runner.SessionInfo
	if err := c.doJSON(ctx, http.MethodPost, "/api/hoxi/sessions?directory="+url.QueryEscape(opts.Directory), payload, &info); err != nil {
		return nil, err
	}
	if info.ModelID == "" {
		info.ModelID = modID
	}
	if info.ProviderID == "" {
		info.ProviderID = provID
	}
	return &info, nil
}

func (c *Client) GetSession(ctx context.Context, sessionID, directory string) (*runner.SessionInfo, error) {
	var info runner.SessionInfo
	path := "/api/hoxi/sessions/" + url.PathEscape(sessionID)
	if strings.TrimSpace(directory) != "" {
		path += "?directory=" + url.QueryEscape(directory)
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (c *Client) ListSessions(ctx context.Context, directory string) ([]runner.SessionInfo, error) {
	var list []runner.SessionInfo
	path := "/api/hoxi/sessions"
	if strings.TrimSpace(directory) != "" {
		path += "?directory=" + url.QueryEscape(directory)
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func (c *Client) SwitchRuntime(ctx context.Context, sessionID, runtime, modelID, providerID string) (*runner.SessionInfo, error) {
	payload := map[string]interface{}{
		"runtime":    NormalizeRuntime(runtime),
		"modelID":    modelID,
		"providerID": providerID,
	}
	var info runner.SessionInfo
	if err := c.doJSON(ctx, http.MethodPost, "/api/hoxi/sessions/"+url.PathEscape(sessionID)+"/runtime", payload, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (c *Client) SwitchSandbox(ctx context.Context, sessionID, sandboxMode string, force bool) (*runner.SessionInfo, error) {
	payload := map[string]interface{}{
		"sandboxMode": sandboxMode,
		"force":       force,
	}
	var info runner.SessionInfo
	if err := c.doJSON(ctx, http.MethodPatch, "/api/hoxi/sessions/"+url.PathEscape(sessionID), payload, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (c *Client) AbortSession(ctx context.Context, sessionID, directory string) error {
	path := "/api/hoxi/sessions/" + url.PathEscape(sessionID) + "/abort"
	if strings.TrimSpace(directory) != "" {
		path += "?directory=" + url.QueryEscape(directory)
	}
	return c.doJSON(ctx, http.MethodPost, path, map[string]interface{}{}, nil)
}

func (c *Client) ReplyPermission(ctx context.Context, requestID, directory, reply string) error {
	path := "/api/hoxi/permission/" + url.PathEscape(requestID) + "/reply"
	if strings.TrimSpace(directory) != "" {
		path += "?directory=" + url.QueryEscape(directory)
	}
	return c.doJSON(ctx, http.MethodPost, path, map[string]interface{}{"reply": reply}, nil)
}

func (c *Client) SessionDiff(ctx context.Context, sessionID, directory string) ([]FileDiff, error) {
	path := "/api/hoxi/worktree/diff?sessionID=" + url.QueryEscape(sessionID)
	if strings.TrimSpace(directory) != "" {
		path += "&directory=" + url.QueryEscape(directory)
	}
	var diffs []FileDiff
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &diffs); err != nil {
		return nil, err
	}
	return diffs, nil
}

func (c *Client) WorktreeStatus(ctx context.Context, sessionID, directory string) (*SandboxStatus, error) {
	path := "/api/hoxi/worktree/status?sessionID=" + url.QueryEscape(sessionID)
	if strings.TrimSpace(directory) != "" {
		path += "&directory=" + url.QueryEscape(directory)
	}
	var st SandboxStatus
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (c *Client) WorktreeApply(ctx context.Context, sessionID, directory, mode string) error {
	path := "/api/hoxi/worktree/apply"
	if strings.TrimSpace(directory) != "" {
		path += "?directory=" + url.QueryEscape(directory)
	}
	return c.doJSON(ctx, http.MethodPost, path, map[string]interface{}{
		"sessionID": sessionID,
		"mode":      mode,
	}, nil)
}

func (c *Client) ListHandoffSessions(ctx context.Context, directory, source string, onlyCurrent bool) (*handoff.HandoffSessionsResponse, error) {
	q := url.Values{}
	if strings.TrimSpace(directory) != "" {
		q.Set("directory", directory)
	}
	if strings.TrimSpace(source) != "" {
		q.Set("source", source)
	}
	if onlyCurrent {
		q.Set("onlyCurrent", "1")
	}
	var res handoff.HandoffSessionsResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/hoxi/handoff/sessions?"+q.Encode(), nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *Client) ImportHandoffSession(ctx context.Context, req handoff.HandoffImportRequest) (*handoff.HandoffImportResponse, error) {
	var res handoff.HandoffImportResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/hoxi/handoff/import", req, &res); err != nil {
		return nil, err
	}
	if !res.OK && res.Error != "" {
		return &res, fmt.Errorf("%s", res.Error)
	}
	return &res, nil
}

func (c *Client) FocusDesktop(ctx context.Context, directory, sessionID string) error {
	return c.doJSON(ctx, http.MethodPost, "/api/hoxi/desktop/focus", map[string]interface{}{
		"directory": directory,
		"sessionID": sessionID,
	}, nil)
}

// StreamTurn subscribes to the unified SSE stream, dispatches the prompt, and
// invokes onEvent for each event belonging to session until the turn completes
// or ctx is cancelled.
func (c *Client) StreamTurn(
	ctx context.Context,
	session *runner.SessionInfo,
	prompt string,
	mode string,
	modelID string,
	providerID string,
	onEvent func(runner.SSEEvent),
) error {
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	sseURL := c.baseURL + "/api/hoxi/event"
	if strings.TrimSpace(session.Directory) != "" {
		sseURL += "?directory=" + url.QueryEscape(session.Directory)
	}
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, sseURL, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("X-Hoxi-Token", c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("SSE stream returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	provID := firstNonEmpty(providerID, session.ProviderID)
	modID := firstNonEmpty(modelID, session.ModelID)
	provID, modID = c.ResolveModel(ctx, session.Directory, session.Runtime, provID, modID)

	promptPayload := map[string]interface{}{
		"text":  prompt,
		"agent": firstNonEmpty(mode, "build"),
	}
	if modID != "" || provID != "" {
		promptPayload["model"] = map[string]string{
			"modelID":    modID,
			"providerID": provID,
		}
	}
	promptPath := "/api/hoxi/sessions/" + url.PathEscape(session.ID) + "/prompt"
	if session.Directory != "" {
		promptPath += "?directory=" + url.QueryEscape(session.Directory)
	}
	if err := c.doJSON(ctx, http.MethodPost, promptPath, promptPayload, nil); err != nil {
		return err
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	sawBusy := false
	askedPerms := make(map[string]bool)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		rawJSON := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if rawJSON == "" {
			continue
		}
		evt, ok := decodeSSEEvent(rawJSON)
		if !ok {
			continue
		}
		if evt.Type == "server.heartbeat" {
			continue
		}
		if evt.Type == "hoxi.stream.resync" {
			done, err := c.resyncTurn(ctx, session, askedPerms, onEvent)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			sawBusy = true
			continue
		}
		if sseEventSessionID(evt) != session.ID {
			continue
		}
		if evt.Type == "permission.asked" {
			if reqID, _ := evt.Properties["id"].(string); reqID != "" {
				if askedPerms[reqID] {
					continue
				}
				askedPerms[reqID] = true
			}
		}
		if onEvent != nil {
			onEvent(evt)
		}
		switch evt.Type {
		case "session.status":
			stMap, _ := evt.Properties["status"].(map[string]interface{})
			stType, _ := stMap["type"].(string)
			if stType == "busy" {
				sawBusy = true
			} else if stType == "idle" && sawBusy {
				return nil
			} else if stType == "idle" && !sawBusy {
				// Quick turns may emit message updates before the status transition.
				return nil
			}
		case "session.idle":
			return nil
		case "session.error":
			if errObj, ok := evt.Properties["error"].(map[string]interface{}); ok {
				if dataObj, ok := errObj["data"].(map[string]interface{}); ok {
					if msg, _ := dataObj["message"].(string); msg != "" {
						return fmt.Errorf("%s", msg)
					}
				}
				if name, _ := errObj["name"].(string); name != "" {
					return fmt.Errorf("%s", name)
				}
			}
			return fmt.Errorf("session encountered an error")
		}
	}
	return scanner.Err()
}

func decodeSSEEvent(rawJSON string) (runner.SSEEvent, bool) {
	var wrapper struct {
		runner.SSEEvent
		Payload *runner.SSEEvent `json:"payload"`
	}
	if json.Unmarshal([]byte(rawJSON), &wrapper) != nil {
		return runner.SSEEvent{}, false
	}
	if wrapper.Payload != nil && wrapper.Payload.Type != "" {
		return *wrapper.Payload, true
	}
	return wrapper.SSEEvent, wrapper.Type != ""
}

func sseEventSessionID(evt runner.SSEEvent) string {
	if sid, _ := evt.Properties["sessionID"].(string); strings.TrimSpace(sid) != "" {
		return strings.TrimSpace(sid)
	}
	switch info := evt.Properties["info"].(type) {
	case map[string]interface{}:
		if sid, _ := info["sessionID"].(string); strings.TrimSpace(sid) != "" {
			return strings.TrimSpace(sid)
		}
		if strings.HasPrefix(evt.Type, "session.") {
			if id, _ := info["id"].(string); strings.TrimSpace(id) != "" {
				return strings.TrimSpace(id)
			}
		}
	case runner.MessageInfo:
		return strings.TrimSpace(info.SessionID)
	case runner.SessionInfo:
		return strings.TrimSpace(info.ID)
	}
	switch part := evt.Properties["part"].(type) {
	case map[string]interface{}:
		if sid, _ := part["sessionID"].(string); strings.TrimSpace(sid) != "" {
			return strings.TrimSpace(sid)
		}
	case runner.MessagePart:
		return strings.TrimSpace(part.SessionID)
	}
	return ""
}

func (c *Client) resyncTurn(
	ctx context.Context,
	session *runner.SessionInfo,
	askedPerms map[string]bool,
	onEvent func(runner.SSEEvent),
) (done bool, turnErr error) {
	dirQuery := ""
	if strings.TrimSpace(session.Directory) != "" {
		dirQuery = "?directory=" + url.QueryEscape(session.Directory)
	}

	var turns []runner.MessageTurn
	msgPath := "/api/hoxi/sessions/" + url.PathEscape(session.ID) + "/message" + dirQuery
	if err := c.doJSON(ctx, http.MethodGet, msgPath, nil, &turns); err == nil {
		for _, turn := range currentTurnSlice(turns) {
			if onEvent != nil && turn.Info.ID != "" {
				if infoMap := toMap(turn.Info); infoMap != nil {
					onEvent(runner.SSEEvent{
						Type:       "message.updated",
						Properties: map[string]interface{}{"sessionID": session.ID, "info": infoMap},
					})
				}
			}
			for _, part := range turn.Parts {
				if onEvent != nil && part.ID != "" {
					if partMap := toMap(part); partMap != nil {
						onEvent(runner.SSEEvent{
							Type:       "message.part.updated",
							Properties: map[string]interface{}{"sessionID": session.ID, "part": partMap},
						})
					}
				}
			}
			if turn.Info.Role == "assistant" && turn.Info.Error != nil {
				msg := strings.TrimSpace(turn.Info.Error.Name)
				if turn.Info.Error.Data != nil && strings.TrimSpace(turn.Info.Error.Data.Message) != "" {
					msg = strings.TrimSpace(turn.Info.Error.Data.Message)
				}
				turnErr = fmt.Errorf("%s", firstNonEmpty(msg, "session encountered an error"))
			}
		}
	}

	var perms []map[string]interface{}
	if err := c.doJSON(ctx, http.MethodGet, "/api/hoxi/permission"+dirQuery, nil, &perms); err == nil {
		for _, perm := range perms {
			if sid, _ := perm["sessionID"].(string); strings.TrimSpace(sid) != session.ID {
				continue
			}
			if reqID, _ := perm["id"].(string); reqID != "" {
				if askedPerms[reqID] {
					continue
				}
				askedPerms[reqID] = true
			}
			if onEvent != nil {
				onEvent(runner.SSEEvent{Type: "permission.asked", Properties: perm})
			}
		}
	}
	if turnErr != nil {
		return true, turnErr
	}

	var statuses map[string]runner.SessionStatus
	if err := c.doJSON(ctx, http.MethodGet, "/api/hoxi/sessions/status"+dirQuery, nil, &statuses); err == nil {
		st, ok := statuses[session.ID]
		if !ok || st.Type == "" || st.Type == "idle" {
			return true, nil
		}
	}
	return false, nil
}

func currentTurnSlice(turns []runner.MessageTurn) []runner.MessageTurn {
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Info.Role == "user" {
			return turns[i:]
		}
	}
	return turns
}

func toMap(v interface{}) map[string]interface{} {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]interface{}
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

func (c *Client) doJSON(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("X-Hoxi-Token", c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errDoc struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &errDoc) == nil && strings.TrimSpace(errDoc.Error) != "" {
			return fmt.Errorf("%s", errDoc.Error)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

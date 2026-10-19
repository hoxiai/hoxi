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
	"github.com/hoxiai/hoxi/internal/privacy"
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
			_ = c.EnsurePrivacyConsent(ctx)
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
	_ = c.EnsurePrivacyConsent(ctx)
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

// EnsurePrivacyConsent records AI execution consent for explicit CLI invocations.
func (c *Client) EnsurePrivacyConsent(ctx context.Context) error {
	var st privacy.Status
	if err := c.doJSON(ctx, http.MethodGet, "/api/hoxi/privacy", nil, &st); err == nil && st.Accepted && st.Version == privacy.Version {
		return nil
	}
	payload := map[string]interface{}{
		"version":  privacy.Version,
		"accepted": true,
	}
	return c.doJSON(ctx, http.MethodPut, "/api/hoxi/privacy", payload, nil)
}

func (c *Client) CreateSession(ctx context.Context, opts Options) (*runner.SessionInfo, error) {
	payload := map[string]interface{}{
		"directory":   opts.Directory,
		"runtime":     opts.Runtime,
		"modelID":     opts.ModelID,
		"providerID":  opts.ProviderID,
		"mode":        opts.Mode,
		"sandboxMode": opts.SandboxMode,
	}
	var info runner.SessionInfo
	if err := c.doJSON(ctx, http.MethodPost, "/api/hoxi/sessions?directory="+url.QueryEscape(opts.Directory), payload, &info); err != nil {
		return nil, err
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

// StreamTurn subscribes to the session SSE stream, dispatches the prompt, and
// invokes onEvent for each SSE event until the turn completes or ctx is cancelled.
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

	sseURL := c.baseURL + "/api/hoxi/runner/sessions/" + url.PathEscape(session.ID) + "/event"
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, sseURL, nil)
	if err != nil {
		return err
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

	promptPayload := map[string]interface{}{
		"text":  prompt,
		"agent": firstNonEmpty(mode, "build"),
	}
	if modelID != "" || providerID != "" {
		promptPayload["model"] = map[string]string{
			"modelID":    modelID,
			"providerID": providerID,
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
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		rawJSON := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if rawJSON == "" {
			continue
		}
		var evt runner.SSEEvent
		if json.Unmarshal([]byte(rawJSON), &evt) != nil {
			continue
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

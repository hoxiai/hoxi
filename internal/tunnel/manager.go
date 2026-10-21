package tunnel

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	StateDisabled   = "disabled"
	StateConnecting = "connecting"
	StateOnline     = "online"
	StateOffline    = "offline"
)

// Options wires the manager to the gateway without importing it.
type Options struct {
	// Handler serves every tunnelled request in-process. The gateway passes a
	// wrapper that enforces the remote route allowlist.
	Handler    http.Handler
	Store      *Store
	Version    string
	TargetPort int
	Hostname   func() string
	Dialer     *websocket.Dialer
	HTTPClient *http.Client
}

// Status is safe to return to the UI: it never includes the tunnel token.
type Status struct {
	Enabled       bool       `json:"enabled"`
	State         string     `json:"state"`
	ControllerURL string     `json:"controllerUrl,omitempty"`
	NodeID        uint       `json:"nodeId,omitempty"`
	Name          string     `json:"name,omitempty"`
	Message       string     `json:"message,omitempty"`
	ConnectedAt   *time.Time `json:"connectedAt,omitempty"`
}

type Manager struct {
	opts      Options
	restartMu sync.Mutex
	mu        sync.Mutex
	cancel    context.CancelFunc
	done      chan struct{}
	status    Status
	peer      *peer
}

// ErrOffline is returned by Notify when no tunnel is connected.
var ErrOffline = errors.New("remote tunnel is not connected")

// Notify sends an attention notice to GoPanel for phone push. Notices are not
// queued: one raised while offline is dropped, and the phone still sees the
// pending item when it next opens the node.
func (m *Manager) Notify(notice any) error {
	m.mu.Lock()
	p := m.peer
	m.mu.Unlock()
	if p == nil {
		return ErrOffline
	}
	f, err := jsonFrame(frameNotify, 0, notice)
	if err != nil {
		return err
	}
	return p.send(f)
}

func NewManager(opts Options) *Manager {
	if opts.Store == nil {
		opts.Store = NewStore("")
	}
	if opts.Hostname == nil {
		opts.Hostname = func() string {
			name, _ := os.Hostname()
			return strings.TrimSpace(name)
		}
	}
	if opts.Dialer == nil {
		opts.Dialer = &websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: 15 * time.Second}
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &Manager{opts: opts, status: Status{State: StateDisabled}}
}

// Configure sets where the credential lives and the loopback bridge port
// reported to GoPanel. The port is informational: requests are served
// in-process, not dialled to it. Without a store path pairing is refused,
// which keeps ephemeral headless gateways from claiming the desktop's node.
func (m *Manager) Configure(store *Store, targetPort int) {
	m.mu.Lock()
	if store != nil {
		m.opts.Store = store
	}
	m.opts.TargetPort = targetPort
	m.mu.Unlock()
}

func (m *Manager) store() *Store {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.opts.Store
}

// Start connects in the background when a paired credential exists.
func (m *Manager) Start() {
	cfg, err := m.store().Load()
	if err != nil {
		m.restartMu.Lock()
		defer m.restartMu.Unlock()
		m.stopLocked()
		status := Status{State: StateDisabled}
		if !errors.Is(err, os.ErrNotExist) {
			status.Message = err.Error()
		}
		m.setStatus(status)
		return
	}
	m.restart(cfg)
}

func (m *Manager) Stop() {
	m.restartMu.Lock()
	defer m.restartMu.Unlock()
	m.stopLocked()
}

func (m *Manager) stopLocked() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel, m.done = nil, nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	m.mu.Lock()
	if m.status.Enabled {
		m.status.State = StateOffline
		m.status.ConnectedAt = nil
	} else {
		m.status = Status{State: StateDisabled}
	}
	m.mu.Unlock()
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *Manager) setStatus(status Status) {
	m.mu.Lock()
	m.status = status
	m.mu.Unlock()
}

func (m *Manager) restart(cfg Config) {
	m.restartMu.Lock()
	defer m.restartMu.Unlock()
	m.stopLocked()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.mu.Lock()
	m.cancel, m.done = cancel, done
	m.status = Status{Enabled: true, State: StateConnecting, ControllerURL: cfg.ControllerURL, NodeID: cfg.NodeID, Name: cfg.Name}
	m.mu.Unlock()
	go func() {
		defer close(done)
		m.run(ctx, cfg)
	}()
}

func (m *Manager) run(ctx context.Context, cfg Config) {
	backoff := time.Second
	for {
		connected, err := m.connect(ctx, cfg)
		if ctx.Err() != nil {
			return
		}
		if connected {
			backoff = time.Second
		}
		message := "tunnel disconnected"
		if err != nil {
			message = err.Error()
		}
		m.setStatus(Status{Enabled: true, State: StateOffline, ControllerURL: cfg.ControllerURL, NodeID: cfg.NodeID, Name: cfg.Name, Message: message})
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (m *Manager) connect(ctx context.Context, cfg Config) (bool, error) {
	endpoint, err := connectURL(cfg.ControllerURL)
	if err != nil {
		return false, err
	}
	header := http.Header{}
	header.Set("X-GoPanel-Tunnel-Client", cfg.ClientID)
	header.Set("X-GoPanel-Tunnel-Token", cfg.Token)
	conn, response, err := m.opts.Dialer.DialContext(ctx, endpoint, header)
	if err != nil {
		if response != nil {
			return false, fmt.Errorf("GoPanel rejected the tunnel (HTTP %d)", response.StatusCode)
		}
		return false, fmt.Errorf("connect GoPanel: %w", err)
	}
	p := newPeer(conn, m.opts.Handler)
	defer func() {
		m.mu.Lock()
		if m.peer == p {
			m.peer = nil
		}
		m.mu.Unlock()
		p.close()
	}()
	m.mu.Lock()
	targetPort := m.opts.TargetPort
	m.mu.Unlock()
	if err := p.sendServiceSync(m.opts.Hostname(), m.opts.Version, targetPort); err != nil {
		return false, err
	}
	now := time.Now()
	m.mu.Lock()
	m.peer = p
	m.mu.Unlock()
	m.setStatus(Status{Enabled: true, State: StateOnline, ControllerURL: cfg.ControllerURL, NodeID: cfg.NodeID, Name: cfg.Name, ConnectedAt: &now})
	go p.heartbeat(ctx)
	return true, p.readLoop(ctx)
}

// Pair redeems a one-time GoPanel pairing code, stores the credential and
// connects. GoPanel creates a tunnel node for this desktop on success.
func (m *Manager) Pair(ctx context.Context, controllerURL, code, name string) error {
	// Check before redeeming: a consumed code with nowhere to store the
	// credential would leave an orphan node on GoPanel.
	if m.store().path == "" {
		return errors.New("remote tunnel is not available in this Hoxi process")
	}
	controllerURL, err := normalizeControllerURL(controllerURL)
	if err != nil {
		return err
	}
	code = strings.TrimSpace(code)
	if decoded, err := hex.DecodeString(code); err != nil || len(decoded) != 16 {
		return errors.New("pairing code must be the 32-character code issued by GoPanel")
	}
	name = strings.TrimSpace(name)
	hostname := m.opts.Hostname()
	if name == "" {
		name = hostname
	}
	payload, err := json.Marshal(map[string]string{"code": code, "name": name, "hostname": hostname, "version": m.opts.Version})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, controllerURL+"/api/tunnel/pair/exchange", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := *m.opts.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connect GoPanel: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	// GoPanel wraps results as {code, msg, data}; code 0 means success.
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			NodeID   uint   `json:"nodeId"`
			ClientID string `json:"clientId"`
			Token    string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("GoPanel returned an unrecognized pairing response (HTTP %d)", response.StatusCode)
	}
	if response.StatusCode != http.StatusOK || result.Code != 0 || result.Data.ClientID == "" || result.Data.Token == "" {
		message := strings.TrimSpace(result.Msg)
		if message == "" {
			message = fmt.Sprintf("HTTP %d", response.StatusCode)
		}
		return fmt.Errorf("GoPanel rejected pairing: %s", message)
	}
	cfg := Config{ControllerURL: controllerURL, NodeID: result.Data.NodeID, ClientID: result.Data.ClientID, Token: result.Data.Token, Name: name}
	if err := m.store().Save(cfg); err != nil {
		return fmt.Errorf("save tunnel credential: %w", err)
	}
	m.restart(cfg)
	return nil
}

// Unpair disconnects and deletes the local credential. The GoPanel node stays
// until an administrator deletes it there.
func (m *Manager) Unpair() error {
	m.Stop()
	if err := m.store().Delete(); err != nil {
		return err
	}
	m.setStatus(Status{State: StateDisabled})
	return nil
}

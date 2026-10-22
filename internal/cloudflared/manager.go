package cloudflared

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	StateDisabled = "disabled"
	StateMissing  = "missing"
	StateStarting = "starting"
	StateOnline   = "online"
	StateOffline  = "offline"
)

// onlineMarker is the log line cloudflared prints once a tunnel connection is
// registered with the edge.
const onlineMarker = "Registered tunnel connection"

// Status is safe to return to the UI: it never includes the connector token.
type Status struct {
	Enabled    bool   `json:"enabled"`
	State      string `json:"state"`
	Hostname   string `json:"hostname,omitempty"`
	Binary     string `json:"binary,omitempty"`
	HasToken   bool   `json:"hasToken"`
	IngressURL string `json:"ingressUrl,omitempty"`
	Message    string `json:"message,omitempty"`
}

// Options wires the manager. Only Store and IngressURL are required in
// production; Resolve and Run are injectable for tests.
type Options struct {
	Store      *Store
	IngressURL string
	Resolve    func() string
	Run        runFunc
	Logger     *slog.Logger
}

// Manager supervises a single cloudflared connector process.
type Manager struct {
	lifecycle sync.Mutex

	mu       sync.Mutex
	store    *Store
	resolve  func() string
	run      runFunc
	logger   *slog.Logger
	status   Status
	enabled  bool
	token    string
	hostname string
	binary   string
	ingress  string

	cancel context.CancelFunc
	done   chan struct{}
}

func NewManager(opts Options) *Manager {
	if opts.Store == nil {
		opts.Store = NewStore("")
	}
	if opts.Resolve == nil {
		opts.Resolve = ResolveBinary
	}
	if opts.Run == nil {
		opts.Run = execRun
	}
	return &Manager{
		store:   opts.Store,
		resolve: opts.Resolve,
		run:     opts.Run,
		logger:  opts.Logger,
		ingress: opts.IngressURL,
		status:  Status{State: StateDisabled},
	}
}

// Configure swaps the store, ingress URL and logger before Start. It mirrors
// tunnel.Manager.Configure so the host can inject the real ~/.hoxi path.
func (m *Manager) Configure(store *Store, ingressURL string, logger *slog.Logger) {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	m.mu.Lock()
	if store != nil {
		m.store = store
	}
	m.ingress = ingressURL
	if logger != nil {
		m.logger = logger
	}
	m.mu.Unlock()
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// Hostname returns the configured Cloudflare public hostname, or "".
func (m *Manager) Hostname() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hostname
}

// Start loads the stored settings and launches the supervisor when enabled.
func (m *Manager) Start() {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	cfg, err := m.store.Load()
	if err != nil {
		m.mu.Lock()
		m.enabled = false
		m.mu.Unlock()
		m.publish(StateDisabled, "")
		return
	}
	m.mu.Lock()
	m.enabled = cfg.Enabled
	m.token = cfg.Token
	m.hostname = cfg.Hostname
	m.mu.Unlock()
	m.restartLocked()
}

// Stop shuts the supervisor down; it is safe to call during shutdown.
func (m *Manager) Stop() {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	m.stopLocked()
}

// Enable persists the connector settings and (re)starts the connector.
func (m *Manager) Enable(token, hostname string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("a Cloudflare connector token is required")
	}
	cfg := Config{Enabled: true, Token: token, Hostname: strings.TrimSpace(hostname)}
	if err := m.store.Save(cfg); err != nil {
		return err
	}
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	m.mu.Lock()
	m.enabled = true
	m.token = token
	m.hostname = cfg.Hostname
	m.mu.Unlock()
	m.restartLocked()
	return nil
}

// Disable stops the connector and persists the disabled state. The token is
// retained so the user can re-enable without pasting it again.
func (m *Manager) Disable() error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	m.mu.Lock()
	cfg := Config{Enabled: false, Token: m.token, Hostname: m.hostname}
	m.enabled = false
	m.mu.Unlock()
	if err := m.store.Save(cfg); err != nil {
		return err
	}
	m.restartLocked()
	return nil
}

// restartLocked stops any running supervisor and starts a new one when enabled.
// The caller holds lifecycle.
func (m *Manager) restartLocked() {
	m.stopLocked()
	m.mu.Lock()
	enabled := m.enabled
	m.mu.Unlock()
	if !enabled {
		m.publish(StateDisabled, "")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.mu.Lock()
	m.cancel = cancel
	m.done = done
	m.mu.Unlock()
	go func() {
		defer close(done)
		m.supervise(ctx)
	}()
}

// stopLocked cancels the supervisor and waits for it to exit. The caller holds
// lifecycle.
func (m *Manager) stopLocked() {
	m.mu.Lock()
	cancel := m.cancel
	done := m.done
	m.cancel = nil
	m.done = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// supervise resolves the binary, runs the connector and restarts it with
// backoff until the context is cancelled.
func (m *Manager) supervise(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		bin := m.resolve()
		m.mu.Lock()
		m.binary = bin
		token := m.token
		m.mu.Unlock()
		if bin == "" {
			m.publish(StateMissing, "cloudflared is not installed")
			return
		}
		args := []string{"--no-autoupdate", "tunnel", "run"}
		env := []string{"TUNNEL_TOKEN=" + token}
		m.publish(StateStarting, "")
		h, err := m.run(ctx, bin, env, args, m.onLine)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			m.logf("cloudflared failed to start", "error", err)
			m.publish(StateOffline, err.Error())
		} else {
			waitErr := h.Wait()
			if ctx.Err() != nil {
				return
			}
			message := "cloudflared exited"
			if waitErr != nil {
				message = waitErr.Error()
			}
			m.publish(StateOffline, message)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (m *Manager) onLine(line string) {
	if strings.Contains(line, onlineMarker) {
		m.publish(StateOnline, "connected")
	}
}

func (m *Manager) publish(state, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = Status{
		Enabled:    m.enabled,
		State:      state,
		Hostname:   m.hostname,
		Binary:     m.binary,
		HasToken:   m.token != "",
		IngressURL: m.ingress,
		Message:    message,
	}
}

func (m *Manager) logf(message string, args ...any) {
	m.mu.Lock()
	logger := m.logger
	m.mu.Unlock()
	if logger != nil {
		logger.Info(message, args...)
	}
}

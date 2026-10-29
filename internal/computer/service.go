// Package computer provides explicitly granted, session-scoped desktop control.
package computer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Scope struct {
	SessionID string `json:"sessionID"`
	Directory string `json:"directory"`
}
type Permissions struct {
	Supported       bool   `json:"supported"`
	Platform        string `json:"platform"`
	Accessibility   bool   `json:"accessibility"`
	ScreenRecording bool   `json:"screenRecording"`
	ForegroundOnly  bool   `json:"foregroundOnly"`
	AppName         string `json:"appName,omitempty"`
	BundleID        string `json:"bundleID,omitempty"`
	AppPath         string `json:"appPath,omitempty"`
}
type App struct {
	ID       string `json:"id"`
	PID      int    `json:"pid"`
	Name     string `json:"name"`
	BundleID string `json:"bundleID"`
	Path     string `json:"path"`
	Started  string `json:"started"`
	Blocked  bool   `json:"blocked"`
}
type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}
type Window struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Bounds Rect   `json:"bounds"`
}
type Element struct {
	ID           int    `json:"id"`
	Role         string `json:"role"`
	Name         string `json:"name"`
	Value        string `json:"value,omitempty"`
	Bounds       Rect   `json:"bounds"`
	WindowBounds *Rect  `json:"windowBounds,omitempty"`
	Secure       bool   `json:"secure,omitempty"`
}
type Snapshot struct {
	App           App       `json:"app"`
	Windows       []Window  `json:"windows"`
	Elements      []Element `json:"elements"`
	WindowID      string    `json:"windowID"`
	ObservationID string    `json:"observationID"`
	Image         string    `json:"image,omitempty"`
	Width         int       `json:"width"`
	Height        int       `json:"height"`
}
type Action struct {
	Action        string  `json:"action"`
	ObservationID string  `json:"observationID"`
	WindowID      string  `json:"windowID"`
	ElementID     int     `json:"elementID,omitempty"`
	X             float64 `json:"x,omitempty"`
	Y             float64 `json:"y,omitempty"`
	EndX          float64 `json:"endX,omitempty"`
	EndY          float64 `json:"endY,omitempty"`
	DeltaX        int     `json:"deltaX,omitempty"`
	DeltaY        int     `json:"deltaY,omitempty"`
	Button        string  `json:"button,omitempty"`
	ClickCount    int     `json:"clickCount,omitempty"`
	Text          string  `json:"text,omitempty"`
	Key           string  `json:"key,omitempty"`
}
type Grant struct {
	App          App    `json:"app"`
	Scope        Scope  `json:"scope"`
	Paused       bool   `json:"paused"`
	Active       bool   `json:"active"`
	LastAction   string `json:"lastAction,omitempty"`
	LastActionAt int64  `json:"lastActionAt,omitempty"`
}
type Backend interface {
	Permissions(context.Context, string) (Permissions, error)
	Apps(context.Context) ([]App, error)
	Inspect(context.Context, App, string, bool) (Snapshot, error)
	Input(context.Context, App, Window, Action) error
}
type observation struct {
	at       time.Time
	windows  []Window
	elements []Element
}
type sessionGrant struct {
	Grant
	generation   uint64
	observations map[string]observation
	cancel       context.CancelFunc
}
type Service struct {
	mu         sync.Mutex
	operation  chan struct{}
	backend    Backend
	grants     map[Scope]*sessionGrant
	generation uint64
	now        func() time.Time
}

func New() *Service { return newService(nativeBackend{}) }
func newService(backend Backend) *Service {
	return &Service{backend: backend, grants: make(map[Scope]*sessionGrant), operation: make(chan struct{}, 1), now: time.Now}
}
func opaqueID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw[:])
}
func BlockedApp(app App) bool {
	id := strings.ToLower(app.BundleID)
	name := strings.ToLower(app.Name)
	file := strings.ToLower(filepath.Base(app.Path))
	return app.PID == os.Getpid() || file == "hoxi.exe" || file == "hoxi dev.exe" || file == "consent.exe" || file == "logonui.exe" || file == "credentialuibroker.exe" || file == "systemsettings.exe" || strings.HasPrefix(id, "ai.hoxi.") || id == "com.apple.systempreferences" || id == "com.apple.securityagent" || id == "com.apple.loginwindow" ||
		id == "com.apple.keychainaccess" || strings.Contains(id, "1password") || strings.Contains(id, "bitwarden") ||
		name == "hoxi" || name == "hoxi dev" || strings.Contains(strings.ToLower(app.Path), "/hoxi.app/")
}
func (s *Service) Permissions(ctx context.Context, prompt string) (Permissions, error) {
	return s.backend.Permissions(ctx, prompt)
}
func (s *Service) Apps(ctx context.Context) ([]App, error) {
	apps, err := s.backend.Apps(ctx)
	if len(apps) > 256 {
		apps = apps[:256]
	}
	for i := range apps {
		apps[i].Blocked = BlockedApp(apps[i])
	}
	return apps, err
}
func (s *Service) Current(scope Scope) *Grant {
	s.mu.Lock()
	defer s.mu.Unlock()
	if grant := s.grants[scope]; grant != nil {
		copy := grant.Grant
		return &copy
	}
	return nil
}

func (s *Service) AuthorizationVersion() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}
func (s *Service) Authorize(ctx context.Context, scope Scope, appID string) (*Grant, error) {
	return s.authorize(ctx, scope, appID, nil)
}

// AuthorizeConfirmed atomically rejects confirmations superseded by a stop,
// revoke, pause or another grant, including while native permission checks ran.
func (s *Service) AuthorizeConfirmed(ctx context.Context, scope Scope, appID string, version uint64) (*Grant, error) {
	return s.authorize(ctx, scope, appID, &version)
}
func (s *Service) authorize(ctx context.Context, scope Scope, appID string, version *uint64) (*Grant, error) {
	apps, err := s.Apps(ctx)
	if err != nil {
		return nil, err
	}
	var target *App
	for i := range apps {
		if apps[i].ID == appID {
			target = &apps[i]
			break
		}
	}
	if target == nil || target.Blocked {
		return nil, errors.New("target_app_not_allowed")
	}
	permission, err := s.Permissions(ctx, "")
	if err != nil {
		return nil, err
	}
	if !permission.Supported {
		return nil, errors.New("system_permissions_required")
	}
	if !permission.Accessibility {
		_, _ = s.Permissions(ctx, "accessibility")
	} else if !permission.ScreenRecording {
		_, _ = s.Permissions(ctx, "screen")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if version != nil && s.generation != *version {
		return nil, errors.New("app_permission_not_granted")
	}
	if s.grants[scope] == nil && len(s.grants) >= 32 {
		return nil, errors.New("computer_grant_limit_reached")
	}
	for owner, existing := range s.grants {
		if owner != scope && existing.App.ID == target.ID {
			return nil, errors.New("app_owned_by_another_session")
		}
	}
	if previous := s.grants[scope]; previous != nil && previous.cancel != nil {
		previous.cancel()
	}
	s.generation++
	grant := &sessionGrant{Grant: Grant{App: *target, Scope: scope, Active: true}, generation: s.generation, observations: make(map[string]observation)}
	s.grants[scope] = grant
	copy := grant.Grant
	return &copy, nil
}
func (s *Service) Pause(scope Scope, paused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	if grant := s.grants[scope]; grant != nil {
		grant.generation = s.generation
		grant.Paused = paused
		clear(grant.observations)
		if grant.cancel != nil {
			grant.cancel()
		}
	}
}
func (s *Service) Revoke(scope Scope) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	if grant := s.grants[scope]; grant != nil && grant.cancel != nil {
		grant.cancel()
	}
	delete(s.grants, scope)
}
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	for scope, grant := range s.grants {
		if grant.cancel != nil {
			grant.cancel()
		}
		delete(s.grants, scope)
	}
}
func (s *Service) alive(ctx context.Context, app App) error {
	apps, err := s.backend.Apps(ctx)
	if err != nil {
		return err
	}
	for _, current := range apps {
		if current.ID == app.ID && current.PID == app.PID && current.Started == app.Started && current.Path == app.Path && !BlockedApp(current) {
			return nil
		}
	}
	return errors.New("target_app_changed")
}
func (s *Service) Observe(ctx context.Context, scope Scope, window string, image bool) (Snapshot, error) {
	select {
	case s.operation <- struct{}{}:
		defer func() { <-s.operation }()
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	}
	s.mu.Lock()
	grant := s.grants[scope]
	if grant == nil || grant.Paused {
		s.mu.Unlock()
		return Snapshot{}, errors.New("app_permission_required_or_paused")
	}
	target, generation := grant.App, grant.generation
	s.mu.Unlock()
	if err := s.alive(ctx, target); err != nil {
		return Snapshot{}, err
	}
	snapshot, err := s.backend.Inspect(ctx, target, window, image)
	if err != nil {
		return Snapshot{}, err
	}
	if len(snapshot.Windows) > 64 {
		snapshot.Windows = snapshot.Windows[:64]
	}
	if len(snapshot.Elements) > 300 {
		snapshot.Elements = snapshot.Elements[:300]
	}
	var selectedWindow *Window
	for i := range snapshot.Windows {
		if snapshot.Windows[i].ID == snapshot.WindowID {
			selectedWindow = &snapshot.Windows[i]
			break
		}
	}
	if selectedWindow == nil && len(snapshot.Windows) > 0 {
		selectedWindow = &snapshot.Windows[0]
	}
	for i := range snapshot.Elements {
		if snapshot.Elements[i].Secure {
			snapshot.Elements[i].Value = ""
			snapshot.Elements[i].Name = ""
		}
		if selectedWindow != nil && snapshot.Elements[i].Bounds.Width > 0 && snapshot.Elements[i].Bounds.Height > 0 {
			snapshot.Elements[i].WindowBounds = &Rect{
				X:      snapshot.Elements[i].Bounds.X - selectedWindow.Bounds.X,
				Y:      snapshot.Elements[i].Bounds.Y - selectedWindow.Bounds.Y,
				Width:  snapshot.Elements[i].Bounds.Width,
				Height: snapshot.Elements[i].Bounds.Height,
			}
		}
	}
	if len(snapshot.Image) > 20<<20 {
		return Snapshot{}, errors.New("capture_exceeds_budget")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.grants[scope] != grant || grant.Paused || grant.generation != generation {
		return Snapshot{}, errors.New("app_permission_changed")
	}
	token := opaqueID()
	for id, old := range grant.observations {
		if s.now().Sub(old.at) > 5*time.Minute {
			delete(grant.observations, id)
		}
	}
	if len(grant.observations) >= 32 {
		var oldest string
		var stamp time.Time
		for id, old := range grant.observations {
			if oldest == "" || old.at.Before(stamp) {
				oldest = id
				stamp = old.at
			}
		}
		delete(grant.observations, oldest)
	}
	grant.observations[token] = observation{at: s.now(), windows: snapshot.Windows, elements: snapshot.Elements}
	snapshot.ObservationID = token
	snapshot.App = target
	return snapshot, nil
}
func ValidateAction(a Action) error {
	if len(a.ObservationID) != 32 || a.WindowID == "" || len(a.WindowID) > 64 {
		return errors.New("fresh_observation_and_window_required")
	}
	for _, n := range []float64{a.X, a.Y, a.EndX, a.EndY} {
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 16384 {
			return errors.New("invalid_coordinates")
		}
	}
	if len(a.Text) > 8192 || len(a.Key) > 128 || a.ClickCount < 0 || a.ClickCount > 3 || a.ElementID < 0 || a.ElementID > 300 {
		return errors.New("input_exceeds_budget")
	}
	switch a.Action {
	case "click":
		if a.Button != "" && a.Button != "left" && a.Button != "right" && a.Button != "middle" {
			return errors.New("invalid_button")
		}
	case "drag":
		if a.Button != "" && a.Button != "left" {
			return errors.New("invalid_drag_button")
		}
	case "scroll":
		if a.DeltaX < -2000 || a.DeltaX > 2000 || a.DeltaY < -2000 || a.DeltaY > 2000 {
			return errors.New("invalid_scroll")
		}
	case "type":
		if a.Text == "" {
			return errors.New("text_required")
		}
	case "key":
		if _, err := ParseKey(a.Key); err != nil {
			return err
		}
	case "focus", "move":
	default:
		return errors.New("unsupported_computer_action")
	}
	return nil
}
func (s *Service) Execute(ctx context.Context, scope Scope, a Action) error {
	if err := ValidateAction(a); err != nil {
		return err
	}
	select {
	case s.operation <- struct{}{}:
		defer func() { <-s.operation }()
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	grant := s.grants[scope]
	if grant == nil || grant.Paused {
		s.mu.Unlock()
		return errors.New("observation_expired_or_permission_changed")
	}
	observation, ok := grant.observations[a.ObservationID]
	if !ok || s.now().Sub(observation.at) > 5*time.Minute {
		s.mu.Unlock()
		return errors.New("observation_expired_or_permission_changed")
	}
	var window *Window
	for i := range observation.windows {
		if observation.windows[i].ID == a.WindowID {
			copy := observation.windows[i]
			window = &copy
			break
		}
	}
	if window == nil {
		s.mu.Unlock()
		return errors.New("window_not_in_observation")
	}
	if a.ElementID > 0 {
		var elem *Element
		for i := range observation.elements {
			if observation.elements[i].ID == a.ElementID {
				copy := observation.elements[i]
				elem = &copy
				break
			}
		}
		if elem == nil {
			s.mu.Unlock()
			return errors.New("element_not_in_observation")
		}
		if elem.Secure {
			s.mu.Unlock()
			return errors.New("secure_field_requires_manual_input")
		}
		cx := elem.Bounds.X + elem.Bounds.Width/2 - window.Bounds.X
		cy := elem.Bounds.Y + elem.Bounds.Height/2 - window.Bounds.Y
		if cx < 0 || cy < 0 || cx >= window.Bounds.Width || cy >= window.Bounds.Height {
			s.mu.Unlock()
			return errors.New("input_outside_window")
		}
		switch a.Action {
		case "click", "move", "type":
			a.X, a.Y = cx, cy
		case "scroll":
			if a.X == 0 && a.Y == 0 {
				a.X, a.Y = cx, cy
			}
		}
	}
	if a.X >= window.Bounds.Width || a.Y >= window.Bounds.Height || (a.Action == "drag" && (a.EndX >= window.Bounds.Width || a.EndY >= window.Bounds.Height)) {
		s.mu.Unlock()
		return errors.New("input_outside_window")
	}
	op, cancel := context.WithCancel(ctx)
	grant.cancel = cancel
	clear(grant.observations)
	target := grant.App
	s.mu.Unlock()
	defer cancel()
	if err := s.alive(op, target); err != nil {
		return err
	}
	latest, err := s.backend.Inspect(op, target, a.WindowID, false)
	if err != nil {
		return err
	}
	found := false
	for _, actual := range latest.Windows {
		if actual.ID == a.WindowID && actual.Bounds.Width == window.Bounds.Width && actual.Bounds.Height == window.Bounds.Height {
			*window = actual
			found = true
			break
		}
	}
	if !found {
		return errors.New("window_resized_or_closed")
	}
	if err := op.Err(); err != nil {
		return err
	}
	if a.Action == "type" && a.ElementID > 0 {
		clickAction := Action{
			Action:        "click",
			ObservationID: a.ObservationID,
			WindowID:      a.WindowID,
			X:             a.X,
			Y:             a.Y,
			Button:        "left",
			ClickCount:    1,
		}
		if err := s.backend.Input(op, target, *window, clickAction); err != nil {
			return fmt.Errorf("computer_input: %w", err)
		}
	}
	if err := s.backend.Input(op, target, *window, a); err != nil {
		return fmt.Errorf("computer_input: %w", err)
	}
	s.mu.Lock()
	if s.grants[scope] == grant {
		grant.LastAction = a.Action
		grant.LastActionAt = s.now().UnixMilli()
	}
	s.mu.Unlock()
	return nil
}

package notify

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	maxTitleRunes = 120
	maxBodyRunes  = 400
)

// ErrUnavailable means the host cannot deliver system notifications.
var ErrUnavailable = errors.New("desktop notifications are unavailable")

type Request struct {
	Title     string
	Body      string
	SessionID string
}

// Service delivers desktop notifications. Policy (when to notify, focus
// suppression and dedupe) stays with the workspace UI; this layer only owns
// transport and the click callback.
type Service struct {
	mu      sync.RWMutex
	onClick func(sessionID string)
	deliver func(Request) error
}

var active atomic.Pointer[Service]

func NewService() *Service {
	return &Service{deliver: platformShow}
}

func (s *Service) Start() {
	if s == nil {
		return
	}
	active.Store(s)
	platformStart()
}

func (s *Service) SetOnClick(fn func(sessionID string)) {
	s.mu.Lock()
	s.onClick = fn
	s.mu.Unlock()
}

func (s *Service) Show(req Request) error {
	if s == nil {
		return ErrUnavailable
	}
	req.Title = truncateRunes(strings.TrimSpace(req.Title), maxTitleRunes)
	req.Body = truncateRunes(strings.TrimSpace(req.Body), maxBodyRunes)
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.Title == "" {
		req.Title = "Hoxi"
	}
	s.mu.RLock()
	deliver := s.deliver
	s.mu.RUnlock()
	if deliver == nil {
		return ErrUnavailable
	}
	return deliver(req)
}

func (s *Service) handleClick(sessionID string) {
	s.mu.RLock()
	fn := s.onClick
	s.mu.RUnlock()
	if fn != nil {
		fn(strings.TrimSpace(sessionID))
	}
}

// dispatchClick is called from the native notification delegate, which can run
// on any thread; the registered handler must reveal the window itself.
func dispatchClick(sessionID string) {
	if s := active.Load(); s != nil {
		s.handleClick(sessionID)
	}
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	count := 0
	for index := range value {
		if count == limit {
			return value[:index]
		}
		count++
	}
	return value
}

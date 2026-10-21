package gateway

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/recall"
	"github.com/hoxiai/hoxi/internal/runner"
)

const (
	recallMaxSessions   = 40
	recallMaxTurns      = 120
	recallMaxTurnChars  = 4000
	recallUpstreamLimit = 40
)

// recallUpstreamTimeout bounds each engine read made while indexing.
const recallUpstreamTimeout = 8 * time.Second

type gatewayRecallSource struct {
	g *Gateway
}

// Sessions merges Hoxi-managed runner sessions with upstream OpenCode sessions
// in the same workspace.
func (s *gatewayRecallSource) Sessions(ctx context.Context, directory string) ([]recall.Session, error) {
	sessions := s.runnerSessions(directory)
	if s.g != nil {
		sessions = append(sessions, s.openCodeSessions(ctx, directory)...)
	}
	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].UpdatedAt > sessions[j].UpdatedAt })
	if len(sessions) > recallMaxSessions {
		sessions = sessions[:recallMaxSessions]
	}
	return sessions, nil
}

func (s *gatewayRecallSource) runnerSessions(directory string) []recall.Session {
	if s.g == nil || s.g.runner == nil {
		return nil
	}
	projectID := ""
	if s.g.workspaces != nil {
		projectID, _ = s.g.workspaces.ProjectIDForDirectory(directory)
	}
	infos := s.g.runner.Store().ListProject(projectID, directory)
	sessions := make([]recall.Session, 0, len(infos))
	for _, info := range infos {
		if info.ParentID != "" || info.Time.Archived > 0 {
			continue
		}
		rec, ok := s.g.runner.Store().Get(info.ID)
		if !ok || rec.Subagent {
			continue
		}
		session := recall.Session{
			ID:        rec.ID,
			Title:     rec.Title,
			Directory: rec.WorktreePath,
			Runtime:   rec.Runtime,
			UpdatedAt: rec.UpdatedAt,
		}
		for _, turn := range rec.Turns {
			if text := turnText(turn); text != "" {
				session.Turns = append(session.Turns, recall.Turn{
					Role:    turn.Info.Role,
					Text:    text,
					Created: turn.Info.Time.Created,
				})
			}
			if len(session.Turns) >= recallMaxTurns {
				break
			}
		}
		sessions = append(sessions, session)
	}
	return sessions
}

func turnText(turn runner.MessageTurn) string {
	if turn.Info.Role != "user" && turn.Info.Role != "assistant" {
		return ""
	}
	var b strings.Builder
	for _, part := range turn.Parts {
		if part.Type != "text" || strings.TrimSpace(part.Text) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(part.Text)
		if b.Len() >= recallMaxTurnChars {
			break
		}
	}
	return clipRunes(b.String(), recallMaxTurnChars)
}

// clipRunes truncates by characters so multi-byte text (e.g. Chinese) is not
// split into an invalid UTF-8 sequence.
func clipRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

type upstreamSession struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	ParentID string `json:"parentID"`
	Time     struct {
		Created  int64 `json:"created"`
		Updated  int64 `json:"updated"`
		Archived int64 `json:"archived"`
	} `json:"time"`
}

type upstreamTurn struct {
	Info struct {
		Role string `json:"role"`
		Time struct {
			Created int64 `json:"created"`
		} `json:"time"`
	} `json:"info"`
	Parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"parts"`
}

func (s *gatewayRecallSource) openCodeSessions(ctx context.Context, directory string) []recall.Session {
	if s.g == nil || !s.g.openCode.Available() {
		return nil
	}
	var listed []upstreamSession
	if err := s.getJSON(ctx, "/session", directory, &listed); err != nil {
		return nil
	}
	sort.SliceStable(listed, func(i, j int) bool { return listed[i].Time.Updated > listed[j].Time.Updated })
	if len(listed) > recallUpstreamLimit {
		listed = listed[:recallUpstreamLimit]
	}
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		sessions []recall.Session
		permits  = make(chan struct{}, 4)
	)
	for _, item := range listed {
		if item.ID == "" || item.ParentID != "" || item.Time.Archived > 0 {
			continue
		}
		// Engine handles of runner conversations are already recalled, with
		// their persisted turns, under the conversation's own ID.
		if s.g.runner != nil && s.g.runner.IsManagedOpenCodeSession(item.ID) {
			continue
		}
		wg.Add(1)
		go func(item upstreamSession) {
			defer wg.Done()
			permits <- struct{}{}
			defer func() { <-permits }()
			var turns []upstreamTurn
			if err := s.getJSON(ctx, "/session/"+url.PathEscape(item.ID)+"/message", directory, &turns); err != nil {
				return
			}
			session := recall.Session{
				ID:        item.ID,
				Title:     item.Title,
				Directory: directory,
				UpdatedAt: item.Time.Updated,
			}
			for _, turn := range turns {
				if turn.Info.Role != "user" && turn.Info.Role != "assistant" {
					continue
				}
				var b strings.Builder
				for _, part := range turn.Parts {
					if part.Type != "text" || strings.TrimSpace(part.Text) == "" {
						continue
					}
					if b.Len() > 0 {
						b.WriteString("\n")
					}
					b.WriteString(part.Text)
					if b.Len() >= recallMaxTurnChars {
						break
					}
				}
				text := clipRunes(b.String(), recallMaxTurnChars)
				if text != "" {
					session.Turns = append(session.Turns, recall.Turn{Role: turn.Info.Role, Text: text, Created: turn.Info.Time.Created})
				}
				if len(session.Turns) >= recallMaxTurns {
					break
				}
			}
			mu.Lock()
			sessions = append(sessions, session)
			mu.Unlock()
		}(item)
	}
	wg.Wait()
	return sessions
}

func (s *gatewayRecallSource) getJSON(ctx context.Context, path, directory string, out interface{}) error {
	ctx, cancel := context.WithTimeout(ctx, recallUpstreamTimeout)
	defer cancel()
	return s.g.openCode.JSON(ctx, http.MethodGet, path, url.Values{"directory": {directory}}, nil, out)
}

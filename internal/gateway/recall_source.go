package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/hoxiai/hoxi/internal/recall"
	"github.com/hoxiai/hoxi/internal/runner"
)

const (
	recallMaxSessions   = 40
	recallMaxTurns      = 120
	recallMaxTurnChars  = 4000
	recallUpstreamLimit = 40
)

type gatewayRecallSource struct {
	g      *Gateway
	client *http.Client
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
	infos := s.g.runner.Store().List(directory)
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
	if s.g == nil || s.g.openCodePort <= 0 {
		return nil
	}
	base := "http://127.0.0.1:" + strconv.Itoa(s.g.openCodePort)
	client := s.client
	var listed []upstreamSession
	if err := s.getJSON(ctx, client, base+"/session?"+directoryQueryParam(directory), &listed); err != nil {
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
		wg.Add(1)
		go func(item upstreamSession) {
			defer wg.Done()
			permits <- struct{}{}
			defer func() { <-permits }()
			var turns []upstreamTurn
			endpoint := base + "/session/" + url.PathEscape(item.ID) + "/message?" + directoryQueryParam(directory)
			if err := s.getJSON(ctx, client, endpoint, &turns); err != nil {
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

func (s *gatewayRecallSource) getJSON(ctx context.Context, client *http.Client, endpoint string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth("opencode", s.g.password)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upstream HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

func directoryQueryParam(directory string) string {
	return "directory=" + url.QueryEscape(directory)
}

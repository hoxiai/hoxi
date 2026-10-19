package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (s *Service) openCodeRequest(ctx context.Context, method, path, directory string, body interface{}) (*http.Response, error) {
	if s.openCodeClient == nil {
		return nil, fmt.Errorf("OpenCode connection is unavailable")
	}
	var data io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		data = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.openCodeURL+path+"?directory="+url.QueryEscape(directory), data)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", s.openCodePassword)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.openCodeClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("OpenCode HTTP %d: %s", resp.StatusCode, raw)
	}
	return resp, nil
}

func (s *Service) openCodeJSON(ctx context.Context, method, path, directory string, body, out interface{}) error {
	resp, err := s.openCodeRequest(ctx, method, path, directory, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (s *Service) startOpenCodeTurn(rec SessionRecord, mode, modelID, effort, system string, user MessageInfo, parts []MessagePart) error {
	setup, cancelSetup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelSetup()
	if rec.ExternalID == "" {
		var info SessionInfo
		if err := s.openCodeJSON(setup, "POST", "/session", rec.WorktreePath, map[string]interface{}{"title": rec.Title, "metadata": map[string]string{"hoxiConversation": rec.ID}}, &info); err != nil {
			return err
		}
		rec.ExternalID = info.ID
		if rec.ExternalID == "" {
			return fmt.Errorf("OpenCode returned an empty session ID")
		}
		var err error
		rec, _, err = s.store.Update(rec.ID, func(r *SessionRecord) {
			r.ExternalID = info.ID
			r.OpenCodeIDs = append(r.OpenCodeIDs, info.ID)
			if r.OpenCodeDirectories == nil {
				r.OpenCodeDirectories = make(map[string]string)
			}
			r.OpenCodeDirectories[info.ID] = rec.WorktreePath
		})
		if err != nil {
			return err
		}
		s.managedOpenCode.Store(info.ID, true)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := s.openCodeRequest(ctx, "GET", "/event", rec.WorktreePath, nil)
	if err != nil {
		cancel()
		return err
	}
	ap := &activeProcess{sessionID: rec.ID, runtime: "opencode", done: make(chan struct{})}
	ap.cancel = func() {
		cancel()
		abortCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = s.openCodeJSON(abortCtx, "POST", "/session/"+rec.ExternalID+"/abort", rec.WorktreePath, map[string]interface{}{}, nil)
	}
	s.mu.Lock()
	s.procs[rec.ID] = ap
	s.mu.Unlock()
	if s.status != nil {
		s.status.SetSessionBusy(rec.ID, true)
	}
	s.broadcast(rec.ID, SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": rec.ID, "status": SessionStatus{Type: "busy"}}})
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		scanner := bufio.NewScanner(stream.Body)
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				s.relayOpenCodeEvent(rec, []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))))
			}
		}
	}()
	go func() {
		defer func() {
			cancel()
			_ = stream.Body.Close()
			<-streamDone
			s.store.Flush()
			s.mu.Lock()
			if s.procs[rec.ID] == ap {
				delete(s.procs, rec.ID)
			}
			s.mu.Unlock()
			close(ap.done)
			if s.status != nil {
				s.status.SetSessionBusy(rec.ID, false)
			}
			s.broadcast(rec.ID, SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": rec.ID, "status": SessionStatus{Type: "idle"}}})
		}()
		inputParts := make([]map[string]interface{}, 0, len(parts))
		for _, p := range parts {
			if p.Type == "file" {
				inputParts = append(inputParts, map[string]interface{}{"type": "file", "mime": p.Mime, "url": p.URL, "filename": p.Filename})
			} else if p.Type == "text" {
				inputParts = append(inputParts, map[string]interface{}{"type": "text", "text": p.Text})
			}
		}
		payload := map[string]interface{}{"messageID": user.ID, "agent": mode, "model": map[string]string{"providerID": rec.ProviderID, "modelID": modelID}, "parts": inputParts}
		if effort != "" {
			payload["variant"] = effort
		}
		if contextText := runtimeContext(rec); contextText != "" {
			system = system + "\n\n" + contextText
		}
		if system != "" {
			payload["system"] = system
		}
		err := s.openCodeJSON(ctx, "POST", "/session/"+rec.ExternalID+"/message", rec.WorktreePath, payload, nil)
		// Stop the stream before authoritative snapshots, so a delayed delta cannot
		// append to an already complete snapshot.
		_ = stream.Body.Close()
		<-streamDone
		if err == nil {
			s.markContextDelivered(rec.ID)
			refreshCtx, done := context.WithTimeout(context.Background(), 15*time.Second)
			defer done()
			var turns []MessageTurn
			if fetchErr := s.openCodeJSON(refreshCtx, "GET", "/session/"+rec.ExternalID+"/message", rec.WorktreePath, nil, &turns); fetchErr == nil {
				for _, turn := range turns {
					turn.Info.SessionID = rec.ID
					s.broadcast(rec.ID, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": turn.Info}})
					for _, part := range turn.Parts {
						part.SessionID = rec.ID
						s.broadcast(rec.ID, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": part}})
					}
				}
			} else {
				err = fetchErr
			}
		}
		if err != nil && !ap.wasAborted() {
			s.broadcast(rec.ID, SSEEvent{Type: "session.error", Properties: map[string]interface{}{"sessionID": rec.ID, "error": &MessageError{Name: "OpenCodeError", Data: &MessageErrorData{Message: err.Error()}}}})
		}
	}()
	return nil
}

func (s *Service) relayOpenCodeEvent(rec SessionRecord, raw []byte) {
	var evt SSEEvent
	if json.Unmarshal(raw, &evt) != nil {
		return
	}
	if evt.Type == "session.updated" {
		data, _ := json.Marshal(evt.Properties["info"])
		var info SessionInfo
		if json.Unmarshal(data, &info) == nil && info.ID == rec.ExternalID && strings.TrimSpace(info.Title) != "" {
			if updated, ok, err := s.store.Update(rec.ID, func(r *SessionRecord) {
				if r.Title == "" || r.Title == "New Session" || strings.HasPrefix(r.Title, "New session - ") {
					r.Title = strings.TrimSpace(info.Title)
				}
			}); err == nil && ok {
				s.broadcast(rec.ID, SSEEvent{Type: "session.updated", Properties: map[string]interface{}{"info": updated.ToSessionInfo()}})
			}
		}
		return
	}
	// Rewriting only sessionID leaves message/part/request IDs intact, so
	// permissions and questions can use the daemon's existing reply endpoints.
	var rewrite func(interface{})
	matched := false
	rewrite = func(value interface{}) {
		switch v := value.(type) {
		case map[string]interface{}:
			for key, item := range v {
				if key == "sessionID" && item == rec.ExternalID {
					v[key] = rec.ID
					matched = true
				} else {
					rewrite(item)
				}
			}
		case []interface{}:
			for _, item := range v {
				rewrite(item)
			}
		}
	}
	rewrite(evt.Properties)
	if !matched {
		return
	}
	if evt.Type == "session.status" || evt.Type == "session.idle" {
		return
	}
	switch evt.Type {
	case "message.updated":
		data, _ := json.Marshal(evt.Properties["info"])
		var info MessageInfo
		if json.Unmarshal(data, &info) != nil {
			return
		}
		evt.Properties["info"] = info
	case "message.part.updated":
		data, _ := json.Marshal(evt.Properties["part"])
		var part MessagePart
		if json.Unmarshal(data, &part) != nil {
			return
		}
		evt.Properties["part"] = part
	}
	if s.status != nil {
		data, _ := json.Marshal(evt)
		s.status.InspectSSELine("data: " + string(data))
	}
	s.broadcast(rec.ID, evt)
}

// Keep OpenCode's live steering on the backing session; its existing event
// subscription continues to relay all subsequent tool steps to the same chat.
func (s *Service) steerOpenCodeTurn(ctx context.Context, rec SessionRecord, mode, modelID, providerID, effort, system, text string, parts []MessagePart) error {
	inputParts := []map[string]interface{}{{"type": "text", "text": text}}
	for _, part := range parts {
		if part.Type == "file" {
			inputParts = append(inputParts, map[string]interface{}{"type": "file", "mime": part.Mime, "filename": part.Filename, "url": part.URL})
		}
	}
	s.mu.Lock()
	s.seqSeed++
	seq := s.seqSeed
	s.mu.Unlock()
	payload := map[string]interface{}{"messageID": fmt.Sprintf("msg_u_%d_%d", time.Now().UnixMilli(), seq), "agent": mode, "model": map[string]string{"providerID": providerID, "modelID": modelID}, "parts": inputParts}
	if effort != "" {
		payload["variant"] = effort
	}
	if contextText := runtimeContext(rec); contextText != "" {
		system += "\n\n" + contextText
	}
	if system != "" {
		payload["system"] = system
	}
	return s.openCodeJSON(ctx, "POST", "/session/"+rec.ExternalID+"/prompt_async", rec.WorktreePath, payload, nil)
}

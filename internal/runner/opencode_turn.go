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

	"github.com/hoxiai/hoxi/internal/attachment"
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

func (s *Service) resolveOpenCodeModel(ctx context.Context, directory, providerID, modelID string) (string, string) {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" && strings.Contains(modelID, "/") {
		parts := strings.SplitN(modelID, "/", 2)
		if strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" {
			providerID = strings.TrimSpace(parts[0])
			modelID = strings.TrimSpace(parts[1])
		}
	}
	if providerID != "" && modelID != "" {
		return providerID, modelID
	}

	qCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var resp struct {
		Providers []struct {
			ID     string                 `json:"id"`
			Source string                 `json:"source"`
			Key    string                 `json:"key"`
			Models map[string]interface{} `json:"models"`
		} `json:"providers"`
		Default map[string]string `json:"default"`
	}
	if err := s.openCodeJSON(qCtx, http.MethodGet, "/config/providers", directory, nil, &resp); err != nil {
		return providerID, modelID
	}

	if modelID != "" && providerID == "" {
		for _, p := range resp.Providers {
			if _, ok := p.Models[modelID]; ok {
				return p.ID, modelID
			}
		}
		return providerID, modelID
	}

	if providerID != "" && modelID == "" {
		if def := strings.TrimSpace(resp.Default[providerID]); def != "" {
			return providerID, def
		}
		for _, p := range resp.Providers {
			if p.ID == providerID {
				for mID := range p.Models {
					return providerID, mID
				}
			}
		}
		return providerID, modelID
	}

	for _, p := range resp.Providers {
		if p.ID == "opencode" && p.Source != "config" && p.Key == "" {
			continue
		}
		if def := strings.TrimSpace(resp.Default[p.ID]); def != "" {
			if _, ok := p.Models[def]; ok || len(p.Models) == 0 {
				return p.ID, def
			}
		}
		for mID := range p.Models {
			return p.ID, mID
		}
	}
	for _, p := range resp.Providers {
		if def := strings.TrimSpace(resp.Default[p.ID]); def != "" {
			return p.ID, def
		}
	}
	return providerID, modelID
}

func (s *Service) resolveOpenCodeVariant(ctx context.Context, directory, providerID, modelID, effort string) string {
	e := normalizeEffortName(effort)
	if e == "" {
		return ""
	}
	if s.openCodeClient == nil || providerID == "" || modelID == "" {
		return e
	}
	qCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var resp struct {
		Providers []struct {
			ID     string                 `json:"id"`
			Models map[string]interface{} `json:"models"`
		} `json:"providers"`
	}
	if err := s.openCodeJSON(qCtx, http.MethodGet, "/config/providers", directory, nil, &resp); err != nil {
		return e
	}
	for _, p := range resp.Providers {
		if p.ID != providerID {
			continue
		}
		mObj, ok := p.Models[modelID].(map[string]interface{})
		if !ok {
			return e
		}
		vMap, hasVariants := mObj["variants"].(map[string]interface{})
		if !hasVariants {
			return e
		}
		if len(vMap) == 0 {
			return ""
		}
		if _, exact := vMap[e]; exact {
			return e
		}
		keys := make([]string, 0, len(vMap))
		for k := range vMap {
			keys = append(keys, k)
		}
		return clampEffortToSupported(e, keys, "")
	}
	return e
}

func (s *Service) normalizeOpenCodePart(rec SessionRecord, part *MessagePart, fileURLByMsgAndName map[string]string) bool {
	if part.Type == "file" && strings.HasPrefix(strings.TrimSpace(part.URL), "data:") {
		key := part.MessageID + "\x00" + part.Filename
		if mapped, ok := fileURLByMsgAndName[key]; ok && mapped != "" {
			part.URL = mapped
			return true
		}
		s.mu.Lock()
		ap := s.procs[rec.ID]
		s.mu.Unlock()
		if ap != nil {
			if v, ok := ap.fileURLs.Load(key); ok {
				if mapped, _ := v.(string); mapped != "" {
					part.URL = mapped
					return true
				}
			}
		}
		if s.attachments != nil {
			if meta, err := s.attachments.Save(attachment.SaveRequest{
				SessionID: rec.ID,
				Directory: rec.WorktreePath,
				Filename:  part.Filename,
				Mime:      part.Mime,
				DataURL:   part.URL,
			}); err == nil {
				part.URL = meta.URL
				part.Mime = meta.Mime
				part.Filename = meta.Filename
				if fileURLByMsgAndName != nil {
					fileURLByMsgAndName[key] = meta.URL
				}
			}
		}
		return true
	}
	if part.Type == "text" {
		const prefix = "Please analyze the attached file(s):\n\n<attached_file "
		if strings.HasPrefix(part.Text, prefix) {
			part.Text = ""
			return false
		}
		if idx := strings.Index(part.Text, "\n\n<attached_file name="); idx >= 0 {
			part.Text = part.Text[:idx]
		}
		s.applyUserSkillOverride(rec.ID, part)
	}
	return true
}

func (s *Service) startOpenCodeTurn(rec SessionRecord, mode, modelID, effort, system string, user MessageInfo, storedParts []MessagePart, deliveryParts []MessagePart) error {
	setup, cancelSetup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelSetup()
	resolvedProvider, resolvedModel := s.resolveOpenCodeModel(setup, rec.WorktreePath, rec.ProviderID, modelID)
	if resolvedProvider != "" && resolvedProvider != rec.ProviderID || resolvedModel != "" && resolvedModel != modelID {
		rec.ProviderID = firstNonEmptyStr(resolvedProvider, rec.ProviderID)
		modelID = firstNonEmptyStr(resolvedModel, modelID)
		_, _, _ = s.store.Update(rec.ID, func(r *SessionRecord) {
			r.ProviderID = rec.ProviderID
			r.ModelID = modelID
		})
	}
	effort = s.resolveOpenCodeVariant(setup, rec.WorktreePath, rec.ProviderID, modelID, effort)
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
	if err := syncOpenCodeCommandSandbox(rec); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := s.openCodeRequest(ctx, "GET", "/event", rec.WorktreePath, nil)
	if err != nil {
		cancel()
		return err
	}
	ap := &activeProcess{sessionID: rec.ID, runtime: "opencode", done: make(chan struct{}), idleCh: make(chan struct{}, 1)}
	ap.cancel = func() {
		abortCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		s.rejectOpenCodeSessionPermissions(abortCtx, rec)
		_ = s.openCodeJSON(abortCtx, "POST", "/session/"+rec.ExternalID+"/abort", rec.WorktreePath, map[string]interface{}{}, nil)
		cancel()
	}
	ap.stall.touch(time.Now())
	s.mu.Lock()
	s.procs[rec.ID] = ap
	s.mu.Unlock()
	if s.status != nil {
		s.status.SetSessionBusy(rec.ID, true)
	}
	s.broadcast(rec.ID, SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": rec.ID, "status": SessionStatus{Type: "busy"}}})
	go s.watchStall(ap)
	fileURLByMsgAndName := make(map[string]string)
	var docOnlyFileParts []MessagePart
	deliveryFileIDs := make(map[string]bool)
	for _, dp := range deliveryParts {
		if dp.Type == "file" {
			deliveryFileIDs[dp.ID] = true
		}
	}
	for _, sp := range storedParts {
		if sp.Type == "file" {
			fileURLByMsgAndName[user.ID+"\x00"+sp.Filename] = sp.URL
			ap.fileURLs.Store(user.ID+"\x00"+sp.Filename, sp.URL)
			if !deliveryFileIDs[sp.ID] {
				docOnlyFileParts = append(docOnlyFileParts, sp)
			}
		}
	}
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		scanner := bufio.NewScanner(stream.Body)
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				s.relayOpenCodeEventWithFiles(rec, []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), fileURLByMsgAndName)
			}
		}
	}()
	go func() {
		defer func() {
			cancel()
			_ = stream.Body.Close()
			<-streamDone
			s.abandonOpenCodeInFlightParts(rec.ID, time.Now().UnixMilli())
			s.store.FlushSession(rec.ID)
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
		if len(docOnlyFileParts) > 0 {
			s.broadcast(rec.ID, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": user}})
			for _, fp := range docOnlyFileParts {
				s.broadcast(rec.ID, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": fp}})
			}
		}
		inputParts := make([]map[string]interface{}, 0, len(deliveryParts))
		for _, p := range deliveryParts {
			if p.Type == "file" {
				inputParts = append(inputParts, map[string]interface{}{"type": "file", "mime": p.Mime, "url": p.URL, "filename": p.Filename})
			} else if p.Type == "text" && (strings.TrimSpace(p.Text) != "" || len(deliveryParts) == 1) {
				inputParts = append(inputParts, map[string]interface{}{"type": "text", "text": p.Text})
			}
		}
		payload := map[string]interface{}{"messageID": user.ID, "agent": mode, "parts": inputParts}
		if rec.ProviderID != "" && modelID != "" {
			payload["model"] = map[string]string{"providerID": rec.ProviderID, "modelID": modelID}
		}
		if effort != "" {
			payload["variant"] = effort
		}
		baseSystem := system
		if contextText := runtimeContext(rec); contextText != "" {
			system = strings.TrimSpace(baseSystem + "\n\n" + contextText)
		}
		if system != "" {
			payload["system"] = system
		}
		err := s.openCodeJSON(ctx, "POST", "/session/"+rec.ExternalID+"/message", rec.WorktreePath, payload, nil)
		if err != nil && (strings.Contains(err.Error(), "OpenCode HTTP 404") || strings.Contains(err.Error(), "NotFoundError") || strings.Contains(err.Error(), "Session not found")) {
			var info SessionInfo
			if createErr := s.openCodeJSON(ctx, "POST", "/session", rec.WorktreePath, map[string]interface{}{"title": rec.Title, "metadata": map[string]string{"hoxiConversation": rec.ID}}, &info); createErr == nil && info.ID != "" {
				rec, _, _ = s.store.Update(rec.ID, func(r *SessionRecord) {
					r.ExternalID = info.ID
					r.OpenCodeIDs = append(r.OpenCodeIDs, info.ID)
					if r.OpenCodeDirectories == nil {
						r.OpenCodeDirectories = make(map[string]string)
					}
					r.OpenCodeDirectories[info.ID] = rec.WorktreePath
					if len(r.Turns) > 0 {
						r.ContextPending = true
						r.ContextTurnCount = len(r.Turns)
					}
				})
				s.managedOpenCode.Store(info.ID, true)
				if contextText := runtimeContext(rec); contextText != "" {
					payload["system"] = strings.TrimSpace(baseSystem + "\n\n" + contextText)
				}
				err = s.openCodeJSON(ctx, "POST", "/session/"+rec.ExternalID+"/message", rec.WorktreePath, payload, nil)
			}
		}
		if err == nil && ap.wasSteered() && !ap.wasAborted() {
			s.waitForOpenCodeIdle(ctx, rec, ap)
		}
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
				existingInfoByID := make(map[string]MessageInfo)
				existingPartByKey := make(map[partKey]MessagePart)
				if current, ok := s.store.Get(rec.ID); ok {
					for _, t := range current.Turns {
						existingInfoByID[t.Info.ID] = t.Info
						for _, p := range t.Parts {
							existingPartByKey[partKey{messageID: p.MessageID, partID: p.ID}] = p
						}
					}
				}
				for _, turn := range turns {
					turn.Info.SessionID = rec.ID
					if prevInfo, exists := existingInfoByID[turn.Info.ID]; !exists || !equalMessageInfo(prevInfo, turn.Info) {
						s.broadcast(rec.ID, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": turn.Info}})
					}
					for _, part := range turn.Parts {
						part.SessionID = rec.ID
						if !s.normalizeOpenCodePart(rec, &part, fileURLByMsgAndName) {
							continue
						}
						if prevPart, exists := existingPartByKey[partKey{messageID: part.MessageID, partID: part.ID}]; !exists || !equalMessagePart(prevPart, part) {
							s.broadcast(rec.ID, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": part}})
						}
					}
				}
				if _, hasSkill := s.userSkillByMsg.Load(user.ID); hasSkill {
					hasUserTurn := false
					if current, ok := s.store.Get(rec.ID); ok {
						for _, t := range current.Turns {
							if t.Info.ID == user.ID {
								hasUserTurn = true
								break
							}
						}
					}
					if !hasUserTurn {
						s.broadcast(rec.ID, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": user}})
						for _, sp := range storedParts {
							s.broadcast(rec.ID, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": sp}})
						}
					}
				}
				s.syncSessionMemoryFile(rec.ID)
			} else {
				err = fetchErr
			}
		}
		if stallErr := ap.stall.abortError(); stallErr != nil {
			s.broadcast(rec.ID, SSEEvent{Type: "session.error", Properties: map[string]interface{}{"sessionID": rec.ID, "error": stallErr}})
		} else if err != nil && !ap.wasAborted() {
			s.broadcast(rec.ID, SSEEvent{Type: "session.error", Properties: map[string]interface{}{"sessionID": rec.ID, "error": &MessageError{Name: "OpenCodeError", Data: &MessageErrorData{Message: err.Error()}}}})
		}
	}()
	return nil
}

func (s *Service) rejectOpenCodeSessionPermissions(ctx context.Context, rec SessionRecord) {
	status, raw, err := s.openCodeRaw(ctx, http.MethodGet, "/permission", directoryQuery(rec.WorktreePath, nil), nil)
	var perms []map[string]interface{}
	if err != nil || status != http.StatusOK || json.Unmarshal(raw, &perms) != nil {
		return
	}
	replyBody := []byte(`{"reply":"reject","message":"the session was stopped"}`)
	for _, item := range perms {
		sid, _ := item["sessionID"].(string)
		pid, _ := item["id"].(string)
		if pid == "" || (sid != rec.ExternalID && sid != rec.ID) {
			continue
		}
		_, _, _ = s.openCodeRaw(ctx, http.MethodPost, "/permission/"+url.PathEscape(pid)+"/reply", directoryQuery(rec.WorktreePath, nil), replyBody)
		repliedEvt := SSEEvent{
			Type:       "permission.replied",
			Properties: map[string]interface{}{"sessionID": rec.ID, "requestID": pid, "reply": "reject"},
		}
		if s.status != nil {
			if data, mErr := json.Marshal(repliedEvt); mErr == nil {
				s.status.InspectSSELine("data: " + string(data))
			}
		}
		s.broadcast(rec.ID, repliedEvt)
	}
}

func (s *Service) abandonOpenCodeInFlightParts(sessionID string, now int64) {
	rec, ok := s.store.Get(sessionID)
	if !ok || len(rec.Turns) == 0 {
		return
	}
	for i := range rec.Turns {
		for j := range rec.Turns[i].Parts {
			p := rec.Turns[i].Parts[j]
			if p.Type == "tool" && p.Tool != "task" && p.State != nil &&
				(p.State.Status == "pending" || p.State.Status == "running") {
				start := now
				if p.State.Time != nil && p.State.Time.Start > 0 {
					start = p.State.Time.Start
				}
				p.State.Status = "error"
				if strings.TrimSpace(p.State.Error) == "" {
					p.State.Error = "Interrupted before completion"
				}
				p.State.Time = &PartTime{Start: start, End: now}
				s.broadcast(sessionID, SSEEvent{
					Type:       "message.part.updated",
					Properties: map[string]interface{}{"part": p},
				})
			} else if p.Type == "reasoning" && (p.Time == nil || p.Time.End == 0) {
				start := now
				if p.Time != nil && p.Time.Start > 0 {
					start = p.Time.Start
				}
				p.Time = &PartTime{Start: start, End: now}
				s.broadcast(sessionID, SSEEvent{
					Type:       "message.part.updated",
					Properties: map[string]interface{}{"part": p},
				})
			}
		}
	}
}

func (s *Service) relayOpenCodeEvent(rec SessionRecord, raw []byte) {
	s.relayOpenCodeEventWithFiles(rec, raw, nil)
}

func (s *Service) relayOpenCodeEventWithFiles(rec SessionRecord, raw []byte, fileURLByMsgAndName map[string]string) {
	var evt SSEEvent
	if json.Unmarshal(raw, &evt) != nil {
		return
	}
	if s.noteOpenCodeChildEvent(rec, evt) {
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
	s.noteOpenCodeActivity(rec.ID, evt)
	if evt.Type == "session.status" || evt.Type == "session.idle" {
		if evt.Type == "session.idle" || isOpenCodeIdleStatus(evt.Properties["status"]) {
			s.mu.Lock()
			ap := s.procs[rec.ID]
			s.mu.Unlock()
			if ap != nil {
				ap.notifyIdle()
			}
		}
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
		if !s.normalizeOpenCodePart(rec, &part, fileURLByMsgAndName) {
			return
		}
		evt.Properties["part"] = part
		s.noteToolOutcome(rec.ID, part)
	case "session.diff":
		evt.Properties["diff"] = s.filterLocalSessionDiffEvent(rec.ID, evt.Properties["diff"])
	}
	if s.status != nil {
		data, _ := json.Marshal(evt)
		s.status.InspectSSELine("data: " + string(data))
	}
	s.broadcast(rec.ID, evt)
}

func isOpenCodeIdleStatus(raw interface{}) bool {
	switch v := raw.(type) {
	case SessionStatus:
		return v.Type == "idle"
	case map[string]interface{}:
		t, _ := v["type"].(string)
		return t == "idle"
	}
	return false
}

func (s *Service) waitForOpenCodeIdle(ctx context.Context, rec SessionRecord, ap *activeProcess) {
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		qCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		var statuses map[string]SessionStatus
		err := s.openCodeJSON(qCtx, http.MethodGet, "/session/status", rec.WorktreePath, nil, &statuses)
		cancel()
		if err != nil {
			return
		}
		st := statuses[rec.ExternalID]
		if st.Type == "" || st.Type == "idle" {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ap.idleCh:
		case <-ticker.C:
		}
	}
}

func (s *Service) resolveUnmanagedSessionDir(sessionID, requestedDir string) string {
	if binding, bound, err := s.worktrees.BindingForSession(sessionID); err == nil && bound && strings.TrimSpace(binding.Directory) != "" {
		return strings.TrimSpace(binding.Directory)
	}
	if strings.TrimSpace(requestedDir) != "" {
		return strings.TrimSpace(requestedDir)
	}
	return s.rootDir
}

// Keep OpenCode's live steering on the backing session; its existing event
// subscription continues to relay all subsequent tool steps to the same chat.
func (s *Service) steerOpenCodeTurn(ctx context.Context, rec SessionRecord, mode, modelID, providerID, effort, system, text string, parts []MessagePart, skillSelection *SkillSelection) error {
	providerID, modelID = s.resolveOpenCodeModel(ctx, rec.WorktreePath, providerID, modelID)
	effort = s.resolveOpenCodeVariant(ctx, rec.WorktreePath, providerID, modelID, effort)
	s.mu.Lock()
	s.seqSeed++
	seq := s.seqSeed
	ap := s.procs[rec.ID]
	s.mu.Unlock()
	now := time.Now().UnixMilli()
	steerMsgID := fmt.Sprintf("msg_u_%d_%d", now, seq)
	var fileParts []MessagePart
	for _, p := range parts {
		if p.Type == "file" && strings.TrimSpace(p.URL) != "" {
			fileParts = append(fileParts, p)
		}
	}
	storedParts, deliveryParts, _ := s.prepareTurnAttachments(rec, now, seq, steerMsgID, text, fileParts)
	if skillSelection != nil {
		s.recordUserSkillOverride(steerMsgID, text, skillSelection)
		storedParts, deliveryParts = applySkillSelectionToTurnParts(
			rec.ID,
			steerMsgID,
			now,
			seq,
			text,
			skillSelection,
			storedParts,
			deliveryParts,
		)
	}
	if rec.ID != "" && s.OwnsSession(rec.ID) {
		s.recordTurnSnapshot(rec.ID, steerMsgID)
	}
	inputParts := make([]map[string]interface{}, 0, len(deliveryParts))
	for _, part := range deliveryParts {
		if part.Type == "file" {
			inputParts = append(inputParts, map[string]interface{}{"type": "file", "mime": part.Mime, "filename": part.Filename, "url": part.URL})
		} else if part.Type == "text" && (strings.TrimSpace(part.Text) != "" || len(deliveryParts) == 1) {
			inputParts = append(inputParts, map[string]interface{}{"type": "text", "text": part.Text})
		}
	}
	payload := map[string]interface{}{"messageID": steerMsgID, "agent": mode, "parts": inputParts}
	if providerID != "" && modelID != "" {
		payload["model"] = map[string]string{"providerID": providerID, "modelID": modelID}
	}
	if effort != "" {
		payload["variant"] = effort
	}
	if contextText := runtimeContext(rec); contextText != "" {
		system += "\n\n" + contextText
	}
	if system != "" {
		payload["system"] = system
	}
	if ap != nil {
		ap.markSteered()
		ap.stall.touch(time.Now())
	}
	if err := s.openCodeJSON(ctx, "POST", "/session/"+rec.ExternalID+"/prompt_async", rec.WorktreePath, payload, nil); err != nil {
		return err
	}
	// Documents reach OpenCode inlined into the text, which the relay strips
	// from the echoed message; show their file chips like a normal turn does.
	deliveredFiles := make(map[string]bool)
	for _, dp := range deliveryParts {
		if dp.Type == "file" {
			deliveredFiles[dp.ID] = true
		}
	}
	var docParts []MessagePart
	for _, sp := range storedParts {
		if sp.Type == "file" {
			if ap != nil {
				ap.fileURLs.Store(steerMsgID+"\x00"+sp.Filename, sp.URL)
			}
			if !deliveredFiles[sp.ID] {
				docParts = append(docParts, sp)
			}
		}
	}
	if len(docParts) > 0 && rec.ID != "" && s.OwnsSession(rec.ID) {
		user := MessageInfo{ID: steerMsgID, SessionID: rec.ID, Role: "user", Time: MessageTime{Created: now}, Agent: mode, ModelID: modelID, ProviderID: providerID}
		s.broadcast(rec.ID, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": user}})
		for _, fp := range docParts {
			s.broadcast(rec.ID, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": fp}})
		}
	}
	return nil
}

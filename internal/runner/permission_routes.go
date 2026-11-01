package runner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type cachedPermissionReply struct {
	reply     string
	rawBody   []byte
	expiresAt time.Time
}

func (s *Service) lookupPermissionReply(permissionID, clientRequestID, reply string) ([]byte, bool) {
	clientRequestID = strings.TrimSpace(clientRequestID)
	if clientRequestID == "" {
		return nil, false
	}
	key := permissionID + "\x00" + clientRequestID
	raw, ok := s.permissionReplies.Load(key)
	if !ok {
		return nil, false
	}
	entry, ok := raw.(cachedPermissionReply)
	if !ok || time.Now().After(entry.expiresAt) {
		s.permissionReplies.Delete(key)
		return nil, false
	}
	if entry.reply != reply {
		return nil, false
	}
	return entry.rawBody, true
}

func (s *Service) storePermissionReply(permissionID, clientRequestID, reply string, rawBody []byte) {
	clientRequestID = strings.TrimSpace(clientRequestID)
	if clientRequestID == "" {
		return
	}
	now := time.Now()
	s.permissionReplies.Range(func(k, v interface{}) bool {
		if entry, ok := v.(cachedPermissionReply); !ok || now.After(entry.expiresAt) {
			s.permissionReplies.Delete(k)
		}
		return true
	})
	s.permissionReplies.Store(permissionID+"\x00"+clientRequestID, cachedPermissionReply{
		reply:     reply,
		rawBody:   append([]byte(nil), rawBody...),
		expiresAt: now.Add(idempotencyReceiptTTL),
	})
}

func (s *Service) handlePermissionRoutes(w http.ResponseWriter, r *http.Request) {
	sub := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/hoxi/runner/permission"), "/")
	switch sub {
	case "":
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
			return
		}
		writeJSON(w, http.StatusOK, s.listPendingApprovals(r.URL.Query().Get("directory")))
		return
	case "audit":
		s.handleApprovalAudit(w, r)
		return
	case "grants":
		s.handleApprovalGrants(w, r)
		return
	}
	id, action, _ := strings.Cut(sub, "/")
	if action != "reply" || r.Method != http.MethodPost {
		writeJSONError(w, http.StatusNotFound, "unknown permission endpoint")
		return
	}
	s.replyRunnerPermission(w, r, id)
}

func (s *Service) handleUnifiedPermissionRoutes(w http.ResponseWriter, r *http.Request) {
	sub := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/hoxi/permission"), "/")
	switch sub {
	case "":
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
			return
		}
		writeJSON(w, http.StatusOK, s.listUnifiedPermissions(r.Context(), r.URL.Query().Get("directory")))
		return
	case "audit":
		s.handleApprovalAudit(w, r)
		return
	case "grants":
		s.handleApprovalGrants(w, r)
		return
	case "policy":
		s.handleApprovalPolicy(w, r)
		return
	case "deferred":
		s.handleDeferred(w, r)
		return
	}
	id, action, _ := strings.Cut(sub, "/")
	if action != "reply" || r.Method != http.MethodPost {
		writeJSONError(w, http.StatusNotFound, "unknown permission endpoint")
		return
	}
	if strings.HasPrefix(id, runnerPermissionID) {
		s.replyRunnerPermission(w, r, id)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid reply")
		return
	}
	var parsedReply struct {
		Reply           string `json:"reply"`
		Message         string `json:"message"`
		GrantClass      bool   `json:"grantClass"`
		ClientRequestID string `json:"clientRequestId"`
	}
	if err := json.Unmarshal(raw, &parsedReply); err != nil ||
		(parsedReply.Reply != "once" && parsedReply.Reply != "always" && parsedReply.Reply != "reject") {
		writeJSONError(w, http.StatusBadRequest, "reply must be once, always or reject")
		return
	}
	clientReqID := strings.TrimSpace(parsedReply.ClientRequestID)
	if clientReqID == "" {
		clientReqID = strings.TrimSpace(r.Header.Get("X-Idempotency-Key"))
	}
	source := requestApprovalSource(r)
	if isRemoteApprovalSource(source) && parsedReply.Reply == "always" {
		writeJSONError(w, http.StatusForbidden, "always approval is not allowed over the remote tunnel")
		return
	}
	if cachedRaw, ok := s.lookupPermissionReply(id, clientReqID, parsedReply.Reply); ok {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(cachedRaw)
		return
	}
	release, claimed := s.claimOpenCodeAnswer(id)
	if !claimed {
		writeJSONError(w, http.StatusConflict, "permission request already answered")
		return
	}
	dir := r.URL.Query().Get("directory")
	auditReply := parsedReply.Reply
	var granted []string
	var ask openCodeAsk
	if parsedReply.GrantClass {
		var known bool
		ask, known = s.gate.openCodeAskFor(id)
		if !known {
			release()
			writeJSONError(w, http.StatusBadRequest, "this request cannot be allowed for the whole session")
			return
		}
		classes, problem := grantClassesForReply(source, parsedReply.Reply, ask.item, ask.dir)
		if problem != "" {
			release()
			status := http.StatusBadRequest
			if isRemoteApprovalSource(source) {
				status = http.StatusForbidden
			}
			writeJSONError(w, status, problem)
			return
		}
		granted = classes
		auditReply = "always"
		// OpenCode only ever gets a one-time approval: its own "always" would
		// store a pattern such as `rm *` and stop asking Hoxi.
		raw = []byte(`{"reply":"once"}`)
	} else if parsedReply.ClientRequestID != "" {
		upstreamPayload := map[string]string{"reply": parsedReply.Reply}
		if parsedReply.Message != "" {
			upstreamPayload["message"] = parsedReply.Message
		}
		if cleaned, marshalErr := json.Marshal(upstreamPayload); marshalErr == nil {
			raw = cleaned
		}
	}
	status, respRaw, err := s.replyOpenCodePermission(r.Context(), id, dir, raw)
	if err != nil {
		release()
		writeJSONError(w, http.StatusBadGateway, "opencode service unavailable: "+err.Error())
		return
	}
	switch {
	case status == http.StatusNotFound:
		s.settleOpenCodePermission(id)
	case status < 200 || status >= 300:
		release()
	}
	if status >= 200 && status < 300 {
		if s.status != nil {
			s.status.SetPendingApproval(id, "", false)
		}
		if len(granted) > 0 {
			s.gate.addClassGrants(ask.sessionID, granted, source)
			go s.reconcilePendingApprovals()
		}
		s.recordOpenCodeApprovalAudit(id, dir, auditReply, parsedReply.Message, source)
		s.storePermissionReply(id, clientReqID, parsedReply.Reply, respRaw)
		s.settleOpenCodePermission(id)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(respRaw)
}

func (s *Service) replyRunnerPermission(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Reply           string `json:"reply"`
		Message         string `json:"message"`
		GrantClass      bool   `json:"grantClass"`
		ClientRequestID string `json:"clientRequestId"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid reply")
		return
	}
	if body.Reply != "once" && body.Reply != "always" && body.Reply != "reject" {
		writeJSONError(w, http.StatusBadRequest, "reply must be once, always or reject")
		return
	}
	clientReqID := strings.TrimSpace(body.ClientRequestID)
	if clientReqID == "" {
		clientReqID = strings.TrimSpace(r.Header.Get("X-Idempotency-Key"))
	}
	source := requestApprovalSource(r)
	if isRemoteApprovalSource(source) && body.Reply == "always" {
		writeJSONError(w, http.StatusForbidden, "always approval is not allowed over the remote tunnel")
		return
	}
	if cachedRaw, ok := s.lookupPermissionReply(id, clientReqID, body.Reply); ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(cachedRaw)
		return
	}
	s.gate.mu.Lock()
	pa := s.gate.pending[id]
	s.gate.mu.Unlock()
	if pa == nil {
		writeJSONError(w, http.StatusNotFound, "permission request not found")
		return
	}
	if permissionName(pa.req) == "hoxi_computer" && (body.Reply == "always" || body.GrantClass || isRemoteApprovalSource(source)) {
		writeJSONError(w, http.StatusForbidden, "computer input requires one-time approval on the desktop")
		return
	}
	reply := approvalReply{Reply: body.Reply, Message: body.Message, Source: source}
	var granted []string
	if body.GrantClass {
		classes, problem := grantClassesForReply(source, body.Reply, pa.req, s.sessionWorktree(pa.sid))
		if problem != "" {
			status := http.StatusBadRequest
			if isRemoteApprovalSource(source) {
				status = http.StatusForbidden
			}
			writeJSONError(w, status, problem)
			return
		}
		granted = classes
		// "always" on a destructive command adds no exact-command grant; the
		// class grant below is what covers later requests.
		reply.Reply = "always"
	}
	select {
	case pa.reply <- reply:
		if len(granted) > 0 {
			s.gate.addClassGrants(pa.sid, granted, source)
			go s.reconcilePendingApprovals()
		}
		s.storePermissionReply(id, clientReqID, body.Reply, []byte("true\n"))
		writeJSON(w, http.StatusOK, true)
	default:
		writeJSONError(w, http.StatusConflict, "permission request already answered")
	}
}

func (s *Service) openCodeEngineSessionMap() (map[string]string, map[string]string, map[string]bool) {
	engineToHoxi := make(map[string]string)
	sessionDirByID := make(map[string]string)
	archivedHoxi := make(map[string]bool)
	for _, info := range s.store.List("") {
		rec, ok := s.store.Meta(info.ID)
		if !ok {
			continue
		}
		if rec.ArchivedAt > 0 {
			archivedHoxi[rec.ID] = true
		}
		recDir := firstNonEmptyStr(strings.TrimSpace(rec.WorktreePath), strings.TrimSpace(rec.ProjectDir))
		if recDir != "" {
			sessionDirByID[rec.ID] = recDir
		}
		if rec.ExternalID != "" {
			engineToHoxi[rec.ExternalID] = rec.ID
			if recDir != "" {
				sessionDirByID[rec.ExternalID] = recDir
			}
		}
		for _, ocID := range rec.OpenCodeIDs {
			if ocID != "" {
				engineToHoxi[ocID] = rec.ID
				if ocDir := strings.TrimSpace(rec.OpenCodeDirectories[ocID]); ocDir != "" {
					sessionDirByID[ocID] = ocDir
				} else if recDir != "" {
					sessionDirByID[ocID] = recDir
				}
			}
		}
	}
	s.openCodeChildParent.Range(func(key, value interface{}) bool {
		childID, _ := key.(string)
		parentID, _ := value.(string)
		if childID == "" || parentID == "" {
			return true
		}
		if _, ok := s.store.Meta(parentID); !ok {
			return true
		}
		engineToHoxi[childID] = parentID
		if hoxiDir := sessionDirByID[parentID]; hoxiDir != "" {
			sessionDirByID[childID] = hoxiDir
		}
		return true
	})
	s.mu.Lock()
	for hoxiID, ap := range s.procs {
		if ap == nil {
			continue
		}
		ap.openCodeChildren.Range(func(key, _ interface{}) bool {
			if childID, ok := key.(string); ok && childID != "" {
				engineToHoxi[childID] = hoxiID
				if hoxiDir := sessionDirByID[hoxiID]; hoxiDir != "" {
					sessionDirByID[childID] = hoxiDir
				}
			}
			return true
		})
	}
	s.mu.Unlock()
	return engineToHoxi, sessionDirByID, archivedHoxi
}

func (s *Service) isSessionBusy(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	s.mu.Lock()
	ap := s.procs[sessionID]
	childBusy := s.childBusy[sessionID]
	s.mu.Unlock()
	if childBusy {
		return true
	}
	return ap != nil && !ap.hasPendingError() && !ap.wasAborted()
}

func (s *Service) isOrphanedOpenCodeSession(rawSid, hoxiID string, archivedHoxi map[string]bool) bool {
	if hoxiID != "" {
		if archivedHoxi[hoxiID] {
			return true
		}
		if rawSid != "" && rawSid != hoxiID {
			if _, isChild := s.openCodeChildParent.Load(rawSid); isChild && !s.isSessionBusy(hoxiID) {
				return true
			}
		}
		return false
	}
	if rawSid == "" {
		return false
	}
	if s.IsManagedOpenCodeSession(rawSid) || s.isManagedOpenCodeChild(rawSid) {
		return true
	}
	if IsRunnerSessionID(rawSid) {
		rec, ok := s.store.Meta(rawSid)
		return !ok || rec.ArchivedAt > 0
	}
	return false
}

func (s *Service) rejectStaleOpenCodePermission(pid, dir string) {
	if pid == "" || s.openCode == nil {
		return
	}
	release, claimed := s.claimOpenCodeAnswer(pid)
	if !claimed {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	replyBody := []byte(`{"reply":"reject","message":"the session was stopped"}`)
	rStatus, _, err := s.replyOpenCodePermission(ctx, pid, dir, replyBody)
	if err == nil && (rStatus == http.StatusNotFound || (rStatus >= 200 && rStatus < 300)) {
		s.settleOpenCodePermission(pid)
		return
	}
	release()
}

func (s *Service) rejectStaleOpenCodeQuestion(qid, dir string) {
	if qid == "" || s.openCode == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, _ = s.openCodeRaw(ctx, http.MethodPost, "/question/"+url.PathEscape(qid)+"/reject", directoryQuery(dir, nil), nil)
}

func (s *Service) listUnifiedPermissions(ctx context.Context, directory string) []map[string]interface{} {
	engineToHoxi, sessionDirByID, archivedHoxi := s.openCodeEngineSessionMap()
	out := make([]map[string]interface{}, 0)
	seenIDs := make(map[string]bool)
	for _, dir := range s.openCodeCandidateDirs(directory) {
		status, raw, err := s.openCodeRaw(ctx, http.MethodGet, "/permission", directoryQuery(dir, nil), nil)
		var enginePerms []map[string]interface{}
		if err == nil && status == http.StatusOK && json.Unmarshal(raw, &enginePerms) == nil {
			for _, item := range enginePerms {
				pid, _ := item["id"].(string)
				if pid != "" {
					if seenIDs[pid] {
						continue
					}
					seenIDs[pid] = true
				}
				rawSid, _ := item["sessionID"].(string)
				hoxiID := engineToHoxi[rawSid]
				if hoxiID != "" {
					item["sessionID"] = hoxiID
				}
				sessionID, _ := item["sessionID"].(string)
				meta, _ := item["metadata"].(map[string]interface{})
				itemDir := firstNonEmptyStr(
					sessionDirByID[rawSid],
					sessionDirByID[sessionID],
					firstString(meta["directory"], meta["cwd"]),
					dir,
				)
				if s.isOrphanedOpenCodeSession(rawSid, hoxiID, archivedHoxi) {
					go s.rejectStaleOpenCodePermission(pid, itemDir)
					continue
				}
				if itemDir != "" {
					if meta == nil {
						meta = make(map[string]interface{})
						item["metadata"] = meta
					}
					if firstString(meta["directory"]) == "" {
						meta["directory"] = itemDir
					}
				}
				sanitizeOpenCodePermissionItem(item)
				if s.audit != nil {
					s.audit.rememberOpenCodePerm(item, itemDir)
				}
				if s.claimOpenCodePermission(item, sessionID, itemDir, nil) {
					continue
				}
				out = append(out, item)
			}
		}
	}
	out = append(out, s.listPendingApprovals(directory)...)
	return out
}

// FilterOpenCodeQuestions rewrites engine/subagent session IDs to Hoxi
// conversation IDs and drops questions left behind by finished, archived, or
// deleted Hoxi turns.
func (s *Service) FilterOpenCodeQuestions(raw []byte, directory string) []byte {
	var questions []map[string]interface{}
	if json.Unmarshal(raw, &questions) != nil {
		return raw
	}
	engineToHoxi, sessionDirByID, archivedHoxi := s.openCodeEngineSessionMap()
	kept := make([]map[string]interface{}, 0, len(questions))
	for _, item := range questions {
		qid, _ := item["id"].(string)
		rawSid, _ := item["sessionID"].(string)
		hoxiID := engineToHoxi[rawSid]
		if hoxiID == "" && IsRunnerSessionID(rawSid) {
			if rec, ok := s.store.Meta(rawSid); ok {
				hoxiID = rec.ID
			}
		}
		if s.isOrphanedOpenCodeSession(rawSid, hoxiID, archivedHoxi) || (hoxiID != "" && !s.isSessionBusy(hoxiID)) {
			qDir := firstNonEmptyStr(sessionDirByID[rawSid], sessionDirByID[hoxiID], directory)
			go s.rejectStaleOpenCodeQuestion(qid, qDir)
			continue
		}
		if hoxiID != "" {
			item["sessionID"] = hoxiID
		}
		kept = append(kept, item)
	}
	encoded, err := json.Marshal(kept)
	if err != nil {
		return raw
	}
	return encoded
}

func (s *Service) rejectOpenCodeSessionPermissions(ctx context.Context, rec SessionRecord) {
	if s.openCode == nil {
		return
	}
	s.mu.Lock()
	ap := s.procs[rec.ID]
	s.mu.Unlock()
	matchesSession := func(sid string) bool {
		if sid == "" {
			return false
		}
		if sid == rec.ID || sid == rec.ExternalID {
			return true
		}
		for _, ocID := range rec.OpenCodeIDs {
			if ocID != "" && sid == ocID {
				return true
			}
		}
		if ap != nil && ap.isOpenCodeChild(sid) {
			return true
		}
		if parentID, ok := s.openCodeChildParent.Load(sid); ok && parentID == rec.ID {
			return true
		}
		return false
	}
	var dirs []string
	seenDirs := make(map[string]bool)
	addDir := func(d string) {
		d = strings.TrimSpace(d)
		key := normalizeComparePath(d)
		if key == "" {
			key = d
		}
		if seenDirs[key] {
			return
		}
		seenDirs[key] = true
		dirs = append(dirs, d)
	}
	if rec.WorktreePath != "" {
		addDir(rec.WorktreePath)
	}
	if rec.ProjectDir != "" {
		addDir(rec.ProjectDir)
	}
	for _, ocDir := range rec.OpenCodeDirectories {
		if ocDir != "" {
			addDir(ocDir)
		}
	}
	if len(dirs) == 0 {
		dirs = []string{""}
	}

	replyBody := []byte(`{"reply":"reject","message":"the session was stopped"}`)
	for _, dir := range dirs {
		status, raw, err := s.openCodeRaw(ctx, http.MethodGet, "/permission", directoryQuery(dir, nil), nil)
		var perms []map[string]interface{}
		if err == nil && status == http.StatusOK && json.Unmarshal(raw, &perms) == nil {
			for _, item := range perms {
				sid, _ := item["sessionID"].(string)
				pid, _ := item["id"].(string)
				if pid == "" || !matchesSession(sid) {
					continue
				}
				release, claimed := s.claimOpenCodeAnswer(pid)
				if !claimed {
					continue
				}
				rStatus, _, replyErr := s.replyOpenCodePermission(ctx, pid, dir, replyBody)
				if replyErr == nil && rStatus == http.StatusNotFound {
					s.settleOpenCodePermission(pid)
					continue
				}
				if replyErr != nil || rStatus < 200 || rStatus >= 300 {
					release()
					continue
				}
				// Hoxi rejected this itself because the session stopped, so record it
				// like any other decision (source "system", not a user choice).
				s.recordOpenCodeAudit(pid, rec.ID, dir, "reject", "the session was stopped", "system")
				s.settleOpenCodePermission(pid)
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

		qStatus, qRaw, qErr := s.openCodeRaw(ctx, http.MethodGet, "/question", directoryQuery(dir, nil), nil)
		var questions []map[string]interface{}
		if qErr == nil && qStatus == http.StatusOK && json.Unmarshal(qRaw, &questions) == nil {
			for _, item := range questions {
				sid, _ := item["sessionID"].(string)
				qid, _ := item["id"].(string)
				if qid == "" || !matchesSession(sid) {
					continue
				}
				rStatus, _, rErr := s.openCodeRaw(ctx, http.MethodPost, "/question/"+url.PathEscape(qid)+"/reject", directoryQuery(dir, nil), nil)
				if rErr != nil || (rStatus != http.StatusNotFound && (rStatus < 200 || rStatus >= 300)) {
					continue
				}
				rejectedEvt := SSEEvent{
					Type:       "question.rejected",
					Properties: map[string]interface{}{"sessionID": rec.ID, "requestID": qid},
				}
				if s.status != nil {
					if data, mErr := json.Marshal(rejectedEvt); mErr == nil {
						s.status.InspectSSELine("data: " + string(data))
					}
				}
				s.broadcast(rec.ID, rejectedEvt)
			}
		}
	}
}

func (s *Service) listPendingApprovals(directory string) []map[string]interface{} {
	dir := normalizeComparePath(directory)
	s.gate.mu.Lock()
	all := make([]*pendingApproval, 0, len(s.gate.pending))
	for _, p := range s.gate.pending {
		all = append(all, p)
	}
	s.gate.mu.Unlock()
	out := make([]map[string]interface{}, 0, len(all))
	for _, p := range all {
		rec, ok := s.store.Meta(p.sid)
		if !ok || rec.ArchivedAt > 0 {
			continue
		}
		if dir != "" {
			if normalizeComparePath(rec.ProjectDir) != dir && normalizeComparePath(rec.WorktreePath) != dir {
				continue
			}
		}
		out = append(out, p.req)
	}
	return out
}

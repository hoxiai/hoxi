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
	if clientRequestID == "" || s.ledger == nil {
		return nil, false
	}
	out, found := s.ledger.peekRequest(RequestKindPermissionReply, permissionID, clientRequestID, "", map[string]string{"reply": reply})
	if !found || out.State != RequestAdmissionDone {
		return nil, false
	}
	return out.ResponseBody, true
}

func (s *Service) storePermissionReply(permissionID, sessionID, clientRequestID, reply string, rawBody []byte) {
	clientRequestID = strings.TrimSpace(clientRequestID)
	if clientRequestID == "" || s.ledger == nil {
		return
	}
	s.ledger.completeRequest(RequestKindPermissionReply, permissionID, sessionID, clientRequestID, "", http.StatusOK, rawBody, map[string]string{"reply": reply})
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
	if s.ledger != nil && s.ledger.loadErr != nil {
		writeCodedRunnerError(w, http.StatusServiceUnavailable, ErrCodeRequestStorageUnavailable, "request ledger could not be loaded; restore it and restart before replying", "")
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
	permDigest := ""
	permMeta := map[string]string{"reply": parsedReply.Reply}
	if clientReqID != "" && s.ledger != nil {
		permDigest = PermissionReplyDigest(id, parsedReply.Reply, parsedReply.Message, parsedReply.GrantClass)
		if prev, found := s.ledger.peekRequest(RequestKindPermissionReply, id, clientReqID, permDigest, permMeta); found {
			if prev.State == RequestAdmissionDone {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("X-Hoxi-Deduplicated", "true")
				w.WriteHeader(prev.HTTPStatus)
				_, _ = w.Write(prev.ResponseBody)
				return
			}
			writeCodedRunnerError(w, prev.HTTPStatus, prev.ErrorCode, prev.ErrorMessage, clientReqID)
			return
		}
	}
	release, claimed := s.claimOpenCodeAnswer(id)
	if !claimed {
		if clientReqID != "" && s.ledger != nil {
			if prev, found := s.ledger.peekRequest(RequestKindPermissionReply, id, clientReqID, permDigest, permMeta); found {
				if prev.State == RequestAdmissionDone {
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.Header().Set("X-Hoxi-Deduplicated", "true")
					w.WriteHeader(prev.HTTPStatus)
					_, _ = w.Write(prev.ResponseBody)
					return
				}
				writeCodedRunnerError(w, prev.HTTPStatus, prev.ErrorCode, prev.ErrorMessage, clientReqID)
				return
			}
		}
		writeCodedRunnerError(w, http.StatusConflict, ErrCodeAlreadyAnswered, "permission request already answered", clientReqID)
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
	var alwaysSession, alwaysPerm string
	var alwaysPatterns []string
	if parsedReply.Reply == "always" && !parsedReply.GrantClass {
		// OpenCode would keep the `always` patterns for its whole instance,
		// ahead of its config (approval_opencode_always.go). It runs this
		// request once; Hoxi keeps the patterns for this session, unless the
		// request is one no session grant may cover.
		raw = []byte(`{"reply":"once"}`)
		auditReply = "once"
		if shown, known := s.gate.openCodeAskFor(id); known && strings.TrimSpace(shown.sessionID) != "" {
			if perm, patterns, ok := openCodeAlwaysGrant(shown.item); ok {
				alwaysSession, alwaysPerm, alwaysPatterns = shown.sessionID, perm, patterns
				auditReply = "always"
			}
		}
	}
	permSessionID := ask.sessionID
	if permSessionID == "" {
		if shown, ok := s.gate.openCodeAskFor(id); ok {
			permSessionID = shown.sessionID
		}
	}
	if clientReqID != "" && s.ledger != nil {
		if adm := s.ledger.admitRequest(RequestKindPermissionReply, id, permSessionID, clientReqID, permDigest, permMeta); adm.State != RequestAdmissionNew {
			release()
			if adm.State == RequestAdmissionDone {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("X-Hoxi-Deduplicated", "true")
				w.WriteHeader(adm.HTTPStatus)
				_, _ = w.Write(adm.ResponseBody)
				return
			}
			writeCodedRunnerError(w, adm.HTTPStatus, adm.ErrorCode, adm.ErrorMessage, clientReqID)
			return
		}
	}
	status, respRaw, err := s.replyOpenCodePermission(r.Context(), id, dir, raw)
	if err != nil {
		if clientReqID != "" && s.ledger != nil {
			s.ledger.markRequestUnknown(RequestKindPermissionReply, id, clientReqID, "opencode service unavailable: "+err.Error())
		}
		release()
		writeCodedRunnerError(w, http.StatusBadGateway, ErrCodeOutcomeUnknown, "opencode service unavailable: "+err.Error(), clientReqID)
		return
	}
	switch {
	case status == http.StatusNotFound:
		if clientReqID != "" && s.ledger != nil {
			s.ledger.abortUncommittedRequest(RequestKindPermissionReply, id, clientReqID)
		}
		s.settleOpenCodePermission(id)
	case status < 200 || status >= 300:
		if clientReqID != "" && s.ledger != nil {
			s.ledger.abortUncommittedRequest(RequestKindPermissionReply, id, clientReqID)
		}
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
		if alwaysPerm != "" {
			s.gate.addOpenCodeAlwaysGrants(alwaysSession, alwaysPerm, alwaysPatterns, source)
			go s.reconcilePendingApprovals()
		}
		s.recordOpenCodeApprovalAudit(id, dir, auditReply, parsedReply.Message, source)
		if clientReqID != "" && s.ledger != nil {
			s.ledger.completeRequest(RequestKindPermissionReply, id, permSessionID, clientReqID, permDigest, status, respRaw, permMeta)
		}
		s.settleOpenCodePermission(id)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(respRaw)
}

func (s *Service) replyRunnerPermission(w http.ResponseWriter, r *http.Request, id string) {
	if s.ledger != nil && s.ledger.loadErr != nil {
		writeCodedRunnerError(w, http.StatusServiceUnavailable, ErrCodeRequestStorageUnavailable, "request ledger could not be loaded; restore it and restart before replying", "")
		return
	}
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
	permDigest := ""
	permMeta := map[string]string{"reply": body.Reply}
	if clientReqID != "" && s.ledger != nil {
		permDigest = PermissionReplyDigest(id, body.Reply, body.Message, body.GrantClass)
		if prev, found := s.ledger.peekRequest(RequestKindPermissionReply, id, clientReqID, permDigest, permMeta); found {
			if prev.State == RequestAdmissionDone {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Hoxi-Deduplicated", "true")
				w.WriteHeader(prev.HTTPStatus)
				_, _ = w.Write(prev.ResponseBody)
				return
			}
			writeCodedRunnerError(w, prev.HTTPStatus, prev.ErrorCode, prev.ErrorMessage, clientReqID)
			return
		}
	}
	s.gate.mu.Lock()
	pa := s.gate.pending[id]
	s.gate.mu.Unlock()
	if pa == nil {
		writeJSONError(w, http.StatusNotFound, "permission request not found")
		return
	}
	if !s.matchesQuestionOrApprovalDirectory(pa.sid, firstString(pa.req["directory"]), r.URL.Query().Get("directory")) {
		writeJSONError(w, http.StatusForbidden, "session directory does not match its sandbox")
		return
	}
	if permissionName(pa.req) == "hoxi_computer" && (body.Reply == "always" || body.GrantClass || isRemoteApprovalSource(source)) {
		writeJSONError(w, http.StatusForbidden, "computer input requires one-time approval on the desktop")
		return
	}
	if permissionName(pa.req) == "hoxi_work_publish" && (body.Reply == "always" || body.GrantClass || isRemoteApprovalSource(source)) {
		writeJSONError(w, http.StatusForbidden, "store publication requires one-time approval on the desktop")
		return
	}
	if permissionName(pa.req) == "hoxi_schedule" && (body.Reply == "always" || body.GrantClass || isRemoteApprovalSource(source)) {
		writeJSONError(w, http.StatusForbidden, "scheduled task changes require one-time approval on the desktop")
		return
	}
	reply := approvalReply{Reply: body.Reply, Message: body.Message, Source: source}
	var granted []string
	if body.GrantClass {
		grantDir := s.sessionWorktree(pa.sid)
		if reqCwd := strings.TrimSpace(firstString(itemMetadata(pa.req)["cwd"])); reqCwd != "" {
			if rec, ok := s.store.Meta(pa.sid); ok {
				_, writeRoots := s.sessionWorkspaceRoots(rec)
				if matched, okRoot := matchSessionRoot(writeRoots, reqCwd); okRoot {
					grantDir = matched
				}
			}
		}
		classes, problem := grantClassesForReply(source, body.Reply, pa.req, grantDir)
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
	if clientReqID != "" && s.ledger != nil {
		if adm := s.ledger.admitRequest(RequestKindPermissionReply, id, pa.sid, clientReqID, permDigest, permMeta); adm.State != RequestAdmissionNew {
			if adm.State == RequestAdmissionDone {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Hoxi-Deduplicated", "true")
				w.WriteHeader(adm.HTTPStatus)
				_, _ = w.Write(adm.ResponseBody)
				return
			}
			writeCodedRunnerError(w, adm.HTTPStatus, adm.ErrorCode, adm.ErrorMessage, clientReqID)
			return
		}
	}
	select {
	case pa.reply <- reply:
		if len(granted) > 0 {
			s.gate.addClassGrants(pa.sid, granted, source)
			go s.reconcilePendingApprovals()
		}
		if clientReqID != "" && s.ledger != nil {
			s.ledger.completeRequest(RequestKindPermissionReply, id, pa.sid, clientReqID, permDigest, http.StatusOK, []byte("true\n"), permMeta)
		}
		writeJSON(w, http.StatusOK, true)
	default:
		if clientReqID != "" && s.ledger != nil {
			s.ledger.abortUncommittedRequest(RequestKindPermissionReply, id, clientReqID)
		}
		writeCodedRunnerError(w, http.StatusConflict, ErrCodeAlreadyAnswered, "permission request already answered", clientReqID)
	}
}

func (s *Service) isSessionBusy(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	if s.hasPendingOrActiveResume(sessionID) {
		return true
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
	if err == nil && rStatus > 0 && rStatus < 500 {
		s.settleOpenCodePermission(pid)
		return
	}
	release()
}

func (s *Service) rejectOpenCodeQuestion(ctx context.Context, qid, dir string) (int, []byte, error) {
	endpoint := "/question/" + url.PathEscape(qid) + "/reject"
	status, raw, err := s.openCodeRaw(ctx, http.MethodPost, endpoint, directoryQuery(dir, nil), nil)
	if err != nil || status != http.StatusNotFound {
		return status, raw, err
	}
	tried := map[string]bool{normalizeComparePath(dir): true}
	for _, candidate := range s.openCodeCandidateDirs("") {
		key := normalizeComparePath(candidate)
		if tried[key] {
			continue
		}
		tried[key] = true
		cStatus, cRaw, cErr := s.openCodeRaw(ctx, http.MethodPost, endpoint, directoryQuery(candidate, nil), nil)
		if cErr == nil && cStatus != http.StatusNotFound {
			return cStatus, cRaw, nil
		}
	}
	return status, raw, nil
}

func (s *Service) rejectStaleOpenCodeQuestion(qid, dir string) {
	if qid == "" || s.openCode == nil {
		return
	}
	key := "question:" + qid
	release, claimed := s.claimOpenCodeAnswer(key)
	if !claimed {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rStatus, _, err := s.rejectOpenCodeQuestion(ctx, qid, dir)
	if err == nil && rStatus > 0 && rStatus < 500 {
		s.settleOpenCodePermission(key)
		return
	}
	release()
}

func (s *Service) listUnifiedPermissions(ctx context.Context, directory string) []map[string]interface{} {
	if s.openCode == nil {
		return s.listPendingApprovals(directory)
	}
	engineToHoxi, sessionDirByID, archivedHoxi := s.openCodeEngineSessionMap()
	out := make([]map[string]interface{}, 0)
	seenIDs := make(map[string]bool)
	plan := s.openCodePollCandidateDirs(directory, &s.cleanIdlePermDirs)
	batches, okList := s.fetchOpenCodeListByDirs(ctx, "/permission", plan.dirs)
	for i, dir := range plan.dirs {
		if !okList[i] {
			continue
		}
		enginePerms := batches[i]
		s.recordOpenCodeDirPollOutcome(&s.cleanIdlePermDirs, plan, dir, len(enginePerms) == 0)
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
			isChild := rawSid != "" && hoxiID != "" && rawSid != hoxiID && s.isManagedOpenCodeChild(rawSid)
			if hoxiID != "" {
				item["sessionID"] = hoxiID
			}
			if isChild {
				item["subagentSessionID"] = rawSid
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
			if itemDir != "" || isChild {
				if meta == nil {
					meta = make(map[string]interface{})
					item["metadata"] = meta
				}
				if itemDir != "" && firstString(meta["directory"]) == "" {
					meta["directory"] = itemDir
				}
				if isChild && firstString(meta["subagentSessionID"]) == "" {
					meta["subagentSessionID"] = rawSid
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
	out = append(out, s.listPendingApprovals(directory)...)
	return out
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
		for _, childID := range rec.OpenCodeChildIDs {
			if childID != "" && sid == childID {
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
				s.settleOpenCodePermission("question:" + qid)
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

package runner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
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

func (s *Service) listUnifiedPermissions(ctx context.Context, directory string) []map[string]interface{} {
	engineToHoxi := make(map[string]string)
	sessionDirByID := make(map[string]string)
	for _, info := range s.store.List("") {
		rec, ok := s.store.Meta(info.ID)
		if !ok {
			continue
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
				if rawSid != "" {
					if hoxiID := engineToHoxi[rawSid]; hoxiID != "" {
						item["sessionID"] = hoxiID
					}
				}
				sessionID, _ := item["sessionID"].(string)
				meta, _ := item["metadata"].(map[string]interface{})
				itemDir := firstNonEmptyStr(
					sessionDirByID[rawSid],
					sessionDirByID[sessionID],
					firstString(meta["directory"], meta["cwd"]),
					dir,
				)
				if itemDir != "" {
					if meta == nil {
						meta = make(map[string]interface{})
						item["metadata"] = meta
					}
					if firstString(meta["directory"], meta["cwd"]) == "" {
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
		if dir != "" {
			rec, ok := s.store.Get(p.sid)
			if !ok || (normalizeComparePath(rec.ProjectDir) != dir && normalizeComparePath(rec.WorktreePath) != dir) {
				continue
			}
		}
		out = append(out, p.req)
	}
	return out
}

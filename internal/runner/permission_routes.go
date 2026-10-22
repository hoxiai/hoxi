package runner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

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
		Reply      string `json:"reply"`
		Message    string `json:"message"`
		GrantClass bool   `json:"grantClass"`
	}
	if err := json.Unmarshal(raw, &parsedReply); err != nil ||
		(parsedReply.Reply != "once" && parsedReply.Reply != "always" && parsedReply.Reply != "reject") {
		writeJSONError(w, http.StatusBadRequest, "reply must be once, always or reject")
		return
	}
	source := requestApprovalSource(r)
	if isRemoteApprovalSource(source) && parsedReply.Reply == "always" {
		writeJSONError(w, http.StatusForbidden, "always approval is not allowed over the remote tunnel")
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
			writeJSONError(w, http.StatusBadRequest, "this request cannot be allowed for the whole session")
			return
		}
		classes, problem := grantClassesForReply(source, parsedReply.Reply, ask.item, ask.dir)
		if problem != "" {
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
	}
	status, respRaw, err := s.replyOpenCodePermission(r.Context(), id, dir, raw)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "opencode service unavailable: "+err.Error())
		return
	}
	if status >= 200 && status < 300 {
		s.gate.forgetOpenCodeAsk(id)
		if len(granted) > 0 {
			s.gate.addClassGrants(ask.sessionID, granted, source)
			go s.reconcilePendingApprovals()
		}
		s.recordOpenCodeApprovalAudit(id, dir, auditReply, parsedReply.Message, source)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(respRaw)
}

func (s *Service) replyRunnerPermission(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Reply      string `json:"reply"`
		Message    string `json:"message"`
		GrantClass bool   `json:"grantClass"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid reply")
		return
	}
	if body.Reply != "once" && body.Reply != "always" && body.Reply != "reject" {
		writeJSONError(w, http.StatusBadRequest, "reply must be once, always or reject")
		return
	}
	source := requestApprovalSource(r)
	if isRemoteApprovalSource(source) && body.Reply == "always" {
		writeJSONError(w, http.StatusForbidden, "always approval is not allowed over the remote tunnel")
		return
	}
	s.gate.mu.Lock()
	pa := s.gate.pending[id]
	s.gate.mu.Unlock()
	if pa == nil {
		writeJSONError(w, http.StatusNotFound, "permission request not found")
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
		writeJSON(w, http.StatusOK, true)
	default:
		writeJSONError(w, http.StatusConflict, "permission request already answered")
	}
}

func (s *Service) listUnifiedPermissions(ctx context.Context, directory string) []map[string]interface{} {
	engineToHoxi := make(map[string]string)
	for _, info := range s.store.List("") {
		rec, ok := s.store.Meta(info.ID)
		if !ok {
			continue
		}
		if rec.ExternalID != "" {
			engineToHoxi[rec.ExternalID] = rec.ID
		}
		for _, ocID := range rec.OpenCodeIDs {
			if ocID != "" {
				engineToHoxi[ocID] = rec.ID
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
				if sid, ok := item["sessionID"].(string); ok {
					if hoxiID := engineToHoxi[sid]; hoxiID != "" {
						item["sessionID"] = hoxiID
					}
				}
				if dir != "" {
					meta, _ := item["metadata"].(map[string]interface{})
					if meta == nil {
						meta = make(map[string]interface{})
						item["metadata"] = meta
					}
					if firstString(meta["directory"], meta["cwd"]) == "" {
						meta["directory"] = dir
					}
				}
				sanitizeOpenCodePermissionItem(item)
				if s.audit != nil {
					s.audit.rememberOpenCodePerm(item, dir)
				}
				sessionID, _ := item["sessionID"].(string)
				if s.claimOpenCodePermission(item, sessionID, dir, nil) {
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

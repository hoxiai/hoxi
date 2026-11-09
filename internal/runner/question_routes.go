package runner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

func (s *Service) listPendingQuestions(directory string) []map[string]interface{} {
	dir := normalizeComparePath(directory)
	s.gate.mu.Lock()
	all := make([]*pendingQuestion, 0, len(s.gate.questions))
	for _, q := range s.gate.questions {
		all = append(all, q)
	}
	s.gate.mu.Unlock()
	out := make([]map[string]interface{}, 0, len(all))
	for _, q := range all {
		rec, ok := s.store.Meta(q.sid)
		if !ok || rec.ArchivedAt > 0 {
			continue
		}
		if dir != "" {
			if normalizeComparePath(rec.ProjectDir) != dir && normalizeComparePath(rec.WorktreePath) != dir {
				continue
			}
		}
		out = append(out, q.req)
	}
	return out
}

// FilterOpenCodeQuestions rewrites engine/subagent session IDs to Hoxi
// conversation IDs, drops questions left behind by finished, archived, or
// deleted Hoxi turns, and appends Runner-gated pending questions.
func (s *Service) FilterOpenCodeQuestions(raw []byte, directory string) []byte {
	var questions []map[string]interface{}
	if json.Unmarshal(raw, &questions) != nil {
		return raw
	}
	if s.openCode != nil {
		reqKey := normalizeComparePath(directory)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		for _, dir := range s.openCodeCandidateDirs(directory) {
			if normalizeComparePath(dir) == reqKey {
				continue
			}
			qStatus, qRaw, qErr := s.openCodeRaw(ctx, http.MethodGet, "/question", directoryQuery(dir, nil), nil)
			var extra []map[string]interface{}
			if qErr == nil && qStatus == http.StatusOK && json.Unmarshal(qRaw, &extra) == nil {
				for _, item := range extra {
					if dir != "" && firstString(item["directory"]) == "" {
						item["directory"] = dir
					}
					questions = append(questions, item)
				}
			}
		}
		cancel()
	}
	kept := s.filterAndMergeQuestions(questions, directory)
	encoded, err := json.Marshal(kept)
	if err != nil {
		return raw
	}
	return encoded
}

func (s *Service) filterAndMergeQuestions(questions []map[string]interface{}, directory string) []map[string]interface{} {
	engineToHoxi, sessionDirByID, archivedHoxi := s.openCodeEngineSessionMap()
	kept := make([]map[string]interface{}, 0, len(questions))
	seenIDs := make(map[string]bool, len(questions))
	for _, item := range questions {
		qid, _ := item["id"].(string)
		if qid != "" {
			if seenIDs[qid] {
				continue
			}
			seenIDs[qid] = true
		}
		rawSid, _ := item["sessionID"].(string)
		hoxiID := engineToHoxi[rawSid]
		if hoxiID == "" && IsRunnerSessionID(rawSid) {
			if rec, ok := s.store.Meta(rawSid); ok {
				hoxiID = rec.ID
			}
		}
		qDir := firstNonEmptyStr(firstString(item["directory"]), sessionDirByID[rawSid], sessionDirByID[hoxiID], directory)
		if s.isOrphanedOpenCodeSession(rawSid, hoxiID, archivedHoxi) || (hoxiID != "" && !s.isSessionBusy(hoxiID)) {
			go s.rejectStaleOpenCodeQuestion(qid, qDir)
			continue
		}
		if qDir != "" && firstString(item["directory"]) == "" {
			item["directory"] = qDir
		}
		if rawSid != "" && hoxiID != "" && rawSid != hoxiID && s.isManagedOpenCodeChild(rawSid) {
			item["subagentSessionID"] = rawSid
		}
		if hoxiID != "" {
			item["sessionID"] = hoxiID
		}
		kept = append(kept, item)
	}
	for _, rq := range s.listPendingQuestions(directory) {
		qid, _ := rq["id"].(string)
		if qid != "" && seenIDs[qid] {
			continue
		}
		if qid != "" {
			seenIDs[qid] = true
		}
		kept = append(kept, rq)
	}
	return kept
}

func (s *Service) listUnifiedQuestions(ctx context.Context, directory string) []map[string]interface{} {
	if s.openCode == nil {
		return s.listPendingQuestions(directory)
	}
	var questions []map[string]interface{}
	qCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for _, dir := range s.openCodeCandidateDirs(directory) {
		qStatus, qRaw, qErr := s.openCodeRaw(qCtx, http.MethodGet, "/question", directoryQuery(dir, nil), nil)
		var batch []map[string]interface{}
		if qErr == nil && qStatus == http.StatusOK && json.Unmarshal(qRaw, &batch) == nil {
			for _, item := range batch {
				if dir != "" && firstString(item["directory"]) == "" {
					item["directory"] = dir
				}
				questions = append(questions, item)
			}
		}
	}
	return s.filterAndMergeQuestions(questions, directory)
}

func (s *Service) handleUnifiedQuestionRoutes(w http.ResponseWriter, r *http.Request) {
	sub := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/hoxi/question"), "/")
	if sub == "" {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
			return
		}
		writeJSON(w, http.StatusOK, s.listUnifiedQuestions(r.Context(), r.URL.Query().Get("directory")))
		return
	}
	id, action, _ := strings.Cut(sub, "/")
	id = strings.TrimSpace(id)
	if id == "" || r.Method != http.MethodPost || (action != "reply" && action != "reject") {
		writeJSONError(w, http.StatusNotFound, "unknown question endpoint")
		return
	}
	if strings.HasPrefix(id, runnerQuestionID) {
		if action == "reply" {
			s.replyRunnerQuestion(w, r, id)
		} else {
			s.rejectRunnerQuestion(w, r, id)
		}
		return
	}
	s.forwardOpenCodeQuestionAction(w, r, id, action)
}

func (s *Service) matchesQuestionOrApprovalDirectory(sid, fallbackDir, requestedDir string) bool {
	normReq := normalizeComparePath(requestedDir)
	if normReq == "" {
		return true
	}
	if rec, ok := s.store.Meta(sid); ok {
		if sessionMatchesDirectory(rec, normReq) {
			return true
		}
	}
	if wt := normalizeComparePath(s.sessionWorktree(sid)); wt != "" && wt == normReq {
		return true
	}
	if fb := normalizeComparePath(fallbackDir); fb != "" && fb == normReq {
		return true
	}
	return false
}

func (s *Service) replyRunnerQuestion(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Answers [][]string `json:"answers"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil || body.Answers == nil {
		writeJSONError(w, http.StatusBadRequest, "answers array is required")
		return
	}
	s.gate.mu.Lock()
	pq := s.gate.questions[id]
	s.gate.mu.Unlock()
	if pq == nil {
		writeJSONError(w, http.StatusNotFound, "QuestionNotFound")
		return
	}
	if !s.matchesQuestionOrApprovalDirectory(pq.sid, firstString(pq.req["directory"]), r.URL.Query().Get("directory")) {
		writeJSONError(w, http.StatusForbidden, "session directory does not match its sandbox")
		return
	}
	select {
	case pq.reply <- questionReply{
		Answers:  normalizeAnswerMatrix(body.Answers, len(pq.questions)),
		Rejected: false,
		Source:   requestApprovalSource(r),
	}:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeJSONError(w, http.StatusConflict, "question already answered")
	}
}

func (s *Service) rejectRunnerQuestion(w http.ResponseWriter, r *http.Request, id string) {
	s.gate.mu.Lock()
	pq := s.gate.questions[id]
	s.gate.mu.Unlock()
	if pq == nil {
		writeJSONError(w, http.StatusNotFound, "QuestionNotFound")
		return
	}
	if !s.matchesQuestionOrApprovalDirectory(pq.sid, firstString(pq.req["directory"]), r.URL.Query().Get("directory")) {
		writeJSONError(w, http.StatusForbidden, "session directory does not match its sandbox")
		return
	}
	select {
	case pq.reply <- questionReply{
		Rejected: true,
		Message:  questionDismissedReason,
		Source:   requestApprovalSource(r),
	}:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeJSONError(w, http.StatusConflict, "question already answered")
	}
}

func (s *Service) forwardOpenCodeQuestionAction(w http.ResponseWriter, r *http.Request, id, action string) {
	if s.openCode == nil {
		writeJSONError(w, http.StatusNotFound, "QuestionNotFound")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if action == "reply" {
		var body struct {
			Answers [][]string `json:"answers"`
		}
		if json.Unmarshal(raw, &body) != nil || body.Answers == nil {
			writeJSONError(w, http.StatusBadRequest, "answers array is required")
			return
		}
	}
	dir := r.URL.Query().Get("directory")
	var lastStatus int
	var lastRaw []byte
	var lastErr error
	for _, candidateDir := range s.openCodeCandidateDirs(dir) {
		lastStatus, lastRaw, lastErr = s.openCodeRaw(
			r.Context(),
			http.MethodPost,
			"/question/"+id+"/"+action,
			directoryQuery(candidateDir, nil),
			raw,
		)
		if lastErr == nil && lastStatus >= 200 && lastStatus < 300 {
			break
		}
	}
	if lastErr != nil {
		writeJSONError(w, http.StatusBadGateway, "opencode service unavailable: "+lastErr.Error())
		return
	}
	if (lastStatus >= 200 && lastStatus < 300) || lastStatus == http.StatusNotFound {
		if s.status != nil {
			s.status.SetPendingQuestion(id, "", false)
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(lastStatus)
	_, _ = w.Write(lastRaw)
}

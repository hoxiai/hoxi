package runner

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	runnerQuestionID        = "que_rnr_"
	questionAnsweredPrefix  = "User has answered your questions: "
	questionDismissedReason = "The user dismissed this question."
)

type questionReply struct {
	Answers  [][]string
	Rejected bool
	Message  string
	Source   string
}

type pendingQuestion struct {
	req       map[string]interface{}
	sid       string
	msgID     string
	callID    string
	callKey   string
	questions []map[string]interface{}
	reply     chan questionReply
	done      chan struct{}
	result    questionReply
}

type resolvedQuestion struct {
	questions []map[string]interface{}
	answers   [][]string
	output    string
	reason    string
	rejected  bool
	resolved  bool
}

func isQuestionToolName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "question", "ask_question", "askuserquestion", "ask_user_question", "ask_user":
		return true
	default:
		return false
	}
}

func parseBoolFlag(v interface{}) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return strings.EqualFold(strings.TrimSpace(b), "true")
	default:
		return false
	}
}

func normalizeRunnerQuestionOption(raw interface{}) (map[string]interface{}, bool) {
	switch v := raw.(type) {
	case string:
		label := strings.TrimSpace(v)
		if label == "" {
			return nil, false
		}
		return map[string]interface{}{
			"label":       label,
			"description": "",
		}, true
	case map[string]interface{}:
		label := strings.TrimSpace(firstString(v["label"], v["value"], v["title"], v["text"], v["name"]))
		if label == "" {
			return nil, false
		}
		desc := strings.TrimSpace(firstString(v["description"], v["detail"], v["hint"]))
		return map[string]interface{}{
			"label":       label,
			"description": desc,
		}, true
	default:
		return nil, false
	}
}

func normalizeRunnerQuestionItem(raw map[string]interface{}) (map[string]interface{}, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	decoded := decodeAgyToolArgs(raw)
	question := strings.TrimSpace(firstString(decoded["question"], decoded["prompt"], decoded["text"], decoded["title"]))
	header := strings.TrimSpace(firstString(decoded["header"]))
	if question == "" && header != "" {
		question = header
	}

	rawOpts := decoded["options"]
	if s, ok := rawOpts.(string); ok {
		var parsed interface{}
		if json.Unmarshal([]byte(strings.TrimSpace(s)), &parsed) == nil {
			rawOpts = parsed
		}
	}
	options := make([]map[string]interface{}, 0)
	switch list := rawOpts.(type) {
	case []interface{}:
		for _, item := range list {
			if opt, ok := normalizeRunnerQuestionOption(item); ok {
				options = append(options, opt)
			}
		}
	case []string:
		for _, item := range list {
			if opt, ok := normalizeRunnerQuestionOption(item); ok {
				options = append(options, opt)
			}
		}
	case []map[string]interface{}:
		for _, item := range list {
			if opt, ok := normalizeRunnerQuestionOption(item); ok {
				options = append(options, opt)
			}
		}
	}

	if question == "" && len(options) == 0 {
		return nil, false
	}
	out := map[string]interface{}{
		"question": question,
		"options":  options,
	}
	if header != "" {
		out["header"] = header
	}
	if parseBoolFlag(decoded["multiple"]) || parseBoolFlag(decoded["multiSelect"]) || parseBoolFlag(decoded["is_multi_select"]) {
		out["multiple"] = true
	}
	if customVal, hasCustom := decoded["custom"]; hasCustom && !parseBoolFlag(customVal) {
		out["custom"] = false
	}
	return out, true
}

func normalizeRunnerQuestions(rawArgs map[string]interface{}) []map[string]interface{} {
	if len(rawArgs) == 0 {
		return nil
	}
	decoded := decodeAgyToolArgs(rawArgs)
	rawQuestions := decoded["questions"]
	if s, ok := rawQuestions.(string); ok {
		var parsed interface{}
		if json.Unmarshal([]byte(strings.TrimSpace(s)), &parsed) == nil {
			rawQuestions = parsed
		}
	}

	var out []map[string]interface{}
	switch list := rawQuestions.(type) {
	case []interface{}:
		for _, item := range list {
			if m, ok := item.(map[string]interface{}); ok {
				if q, ok := normalizeRunnerQuestionItem(m); ok {
					out = append(out, q)
				}
			}
		}
	case []map[string]interface{}:
		for _, m := range list {
			if q, ok := normalizeRunnerQuestionItem(m); ok {
				out = append(out, q)
			}
		}
	}

	if len(out) == 0 {
		if single, ok := normalizeRunnerQuestionItem(decoded); ok {
			out = append(out, single)
		}
	}
	return out
}

func formatQuestionAnswers(questions []map[string]interface{}, answers [][]string) string {
	parts := make([]string, 0, len(questions))
	for i, q := range questions {
		qText := strings.TrimSpace(firstString(q["question"], q["header"]))
		var picked []string
		if i < len(answers) {
			for _, a := range answers[i] {
				if trimmed := strings.TrimSpace(a); trimmed != "" {
					picked = append(picked, trimmed)
				}
			}
		}
		ansText := "Unanswered"
		if len(picked) > 0 {
			ansText = strings.Join(picked, ", ")
		}
		parts = append(parts, fmt.Sprintf("%q=%q", qText, ansText))
	}
	return questionAnsweredPrefix + strings.Join(parts, ", ") + ". You can now continue with the user's answers in mind."
}

func normalizeAnswerMatrix(raw [][]string, questionCount int) [][]string {
	out := make([][]string, 0, max(len(raw), questionCount))
	for _, group := range raw {
		clean := make([]string, 0, len(group))
		for _, item := range group {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				clean = append(clean, trimmed)
			}
		}
		out = append(out, clean)
	}
	for len(out) < questionCount {
		out = append(out, []string{})
	}
	return out
}

func (s *Service) gateQuestion(r *http.Request, cred hookCredential, rec SessionRecord, toolName string, rawArgs map[string]interface{}, callID string) (string, string) {
	questions := normalizeRunnerQuestions(rawArgs)
	if len(questions) == 0 {
		return "allow", ""
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		callID = "call_" + firstNonEmptyStr(cred.messageID, "msg") + "_question"
	}
	callKey := cred.sessionID + "\x00" + callID

	s.gate.mu.Lock()
	if existing := s.gate.questionsByCallKey[callKey]; existing != nil {
		s.gate.mu.Unlock()
		select {
		case <-existing.done:
			return questionHookDecision(existing.questions, existing.result)
		case <-r.Context().Done():
			return hookDecisionDeny, questionDismissedReason
		}
	}

	id := runnerQuestionID + randomHex(12)
	limit := s.approvalWaitLimit()
	nowMs := time.Now().UnixMilli()
	req := map[string]interface{}{
		"id":        id,
		"sessionID": cred.sessionID,
		"directory": rec.WorktreePath,
		"questions": questions,
		"createdAt": nowMs,
		"tool": map[string]interface{}{
			"messageID": cred.messageID,
			"callID":    callID,
		},
	}
	if limit > 0 {
		req["expiresAt"] = nowMs + limit.Milliseconds()
	}
	pq := &pendingQuestion{
		req:       req,
		sid:       cred.sessionID,
		msgID:     cred.messageID,
		callID:    callID,
		callKey:   callKey,
		questions: questions,
		reply:     make(chan questionReply, 1),
		done:      make(chan struct{}),
	}
	s.gate.questions[id] = pq
	s.gate.questionsByCallKey[callKey] = pq
	s.gate.mu.Unlock()

	s.seedQuestionToolPart(cred, callID, questions, nowMs)
	if s.status != nil {
		s.status.SetPendingQuestion(id, cred.sessionID, true)
	}
	s.broadcast(cred.sessionID, SSEEvent{Type: "question.asked", Properties: req})

	policy := s.ApprovalPolicy()
	timeoutReason := approvalTimeoutReason(policy)
	if policy.Mode == ApprovalModeUnattended {
		timeoutReason = unattendedQuestionTimeoutReason()
	}
	result := questionReply{
		Rejected: true,
		Message:  timeoutReason,
		Source:   approvalSourceTimeout,
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case result = <-pq.reply:
	case <-r.Context().Done():
		result = questionReply{
			Rejected: true,
			Message:  questionDismissedReason,
			Source:   "system",
		}
	case <-timer.C:
		s.rememberDeferred(cred.sessionID, id, "timeout", "question")
		s.noteTurn(cred.sessionID, "timeout")
	}

	decision, reason := questionHookDecision(questions, result)
	resolved := resolvedQuestion{
		questions: questions,
		answers:   result.Answers,
		output:    reason,
		reason:    reason,
		rejected:  result.Rejected,
		resolved:  true,
	}

	s.gate.mu.Lock()
	pq.result = result
	close(pq.done)
	delete(s.gate.questions, id)
	if s.gate.questionsByCallKey[callKey] == pq {
		delete(s.gate.questionsByCallKey, callKey)
	}
	s.gate.resolvedQuestions[callKey] = resolved
	s.gate.resolvedQuestions[cred.sessionID+"\x00latest"] = resolved
	s.gate.mu.Unlock()

	if s.status != nil {
		s.status.SetPendingQuestion(id, cred.sessionID, false)
	}
	s.finalizeQuestionToolPart(cred, callID, resolved, time.Now().UnixMilli())
	if result.Rejected {
		s.broadcast(cred.sessionID, SSEEvent{
			Type: "question.rejected",
			Properties: map[string]interface{}{
				"sessionID": cred.sessionID,
				"requestID": id,
			},
		})
	} else {
		s.broadcast(cred.sessionID, SSEEvent{
			Type: "question.replied",
			Properties: map[string]interface{}{
				"sessionID": cred.sessionID,
				"requestID": id,
				"answers":   result.Answers,
			},
		})
	}
	return decision, reason
}

func questionHookDecision(questions []map[string]interface{}, result questionReply) (string, string) {
	if result.Rejected {
		reason := strings.TrimSpace(result.Message)
		if reason == "" {
			reason = questionDismissedReason
		}
		return hookDecisionDeny, reason
	}
	return hookDecisionDeny, formatQuestionAnswers(questions, result.Answers)
}

func findTurnAndQuestionPart(turns []MessageTurn, msgID, callID string) (hasMsg bool, existing *MessagePart) {
	for i := len(turns) - 1; i >= 0; i-- {
		if msgID != "" && turns[i].Info.ID == msgID {
			hasMsg = true
		}
		for j := len(turns[i].Parts) - 1; j >= 0; j-- {
			p := turns[i].Parts[j]
			if p.Type == "tool" && callID != "" && p.CallID == callID {
				cp := clonePart(p)
				return true, &cp
			}
		}
	}
	return hasMsg, nil
}

func agyToolPartIDFromCallID(msgID, callID string) string {
	prefix := "call_" + msgID + "_"
	if msgID == "" || !strings.HasPrefix(callID, prefix) {
		return ""
	}
	stepSuffix := strings.TrimPrefix(callID, prefix)
	if stepSuffix == "" || stepSuffix == "0" {
		return ""
	}
	for i := 0; i < len(stepSuffix); i++ {
		if stepSuffix[i] < '0' || stepSuffix[i] > '9' {
			return ""
		}
	}
	return "prt_" + msgID + "_tool_" + stepSuffix
}

func (s *Service) seedQuestionToolPart(cred hookCredential, callID string, questions []map[string]interface{}, nowMs int64) {
	rec, ok := s.store.Get(cred.sessionID)
	if !ok {
		return
	}
	hasMsg, existing := findTurnAndQuestionPart(rec.Turns, cred.messageID, callID)
	if existing != nil {
		if existing.State == nil {
			existing.State = &ToolState{Status: "running", Time: &PartTime{Start: nowMs}}
		}
		if existing.State.Input == nil {
			existing.State.Input = make(map[string]interface{})
		}
		if !hasQuestionsInput(existing.State.Input) {
			existing.State.Input["questions"] = questions
			existing.Tool = "question"
			s.broadcast(cred.sessionID, SSEEvent{
				Type:       "message.part.updated",
				Properties: map[string]interface{}{"part": *existing},
			})
		}
		return
	}
	if !hasMsg || cred.runtime != string(RuntimeAntigravity) {
		return
	}
	partID := agyToolPartIDFromCallID(cred.messageID, callID)
	if partID == "" {
		return
	}
	part := MessagePart{
		ID:        partID,
		SessionID: cred.sessionID,
		MessageID: cred.messageID,
		Type:      "tool",
		Tool:      "question",
		CallID:    callID,
		State: &ToolState{
			Status: "running",
			Title:  "question",
			Input:  map[string]interface{}{"questions": questions},
			Time:   &PartTime{Start: nowMs},
		},
	}
	s.broadcast(cred.sessionID, SSEEvent{
		Type:       "message.part.updated",
		Properties: map[string]interface{}{"part": part},
	})
}

func (s *Service) finalizeQuestionToolPart(cred hookCredential, callID string, resolved resolvedQuestion, endMs int64) {
	rec, ok := s.store.Get(cred.sessionID)
	if !ok {
		return
	}
	_, existing := findTurnAndQuestionPart(rec.Turns, cred.messageID, callID)
	if existing == nil {
		return
	}
	startMs := endMs
	if existing.State != nil && existing.State.Time != nil && existing.State.Time.Start > 0 {
		startMs = existing.State.Time.Start
	}
	input := map[string]interface{}{"questions": resolved.questions}
	if existing.State != nil && len(existing.State.Input) > 0 {
		for k, v := range existing.State.Input {
			input[k] = v
		}
		input["questions"] = resolved.questions
	}
	meta := make(map[string]interface{})
	if existing.State != nil && len(existing.State.Metadata) > 0 {
		for k, v := range existing.State.Metadata {
			meta[k] = v
		}
	}
	status := "completed"
	output := resolved.output
	errMsg := ""
	if resolved.rejected {
		status = "error"
		output = ""
		errMsg = firstNonEmptyStr(resolved.reason, questionDismissedReason)
		meta["rejected"] = true
	} else {
		meta["answers"] = resolved.answers
	}
	existing.Tool = "question"
	existing.State = &ToolState{
		Status:   status,
		Title:    firstNonEmptyStr(existing.State.Title, "question"),
		Input:    input,
		Output:   output,
		Error:    errMsg,
		Metadata: meta,
		Time:     &PartTime{Start: startMs, End: max(endMs, startMs)},
	}
	s.broadcast(cred.sessionID, SSEEvent{
		Type:       "message.part.updated",
		Properties: map[string]interface{}{"part": *existing},
	})
}

func hasQuestionsInput(input map[string]interface{}) bool {
	if len(input) == 0 {
		return false
	}
	switch v := input["questions"].(type) {
	case []interface{}:
		return len(v) > 0
	case []map[string]interface{}:
		return len(v) > 0
	default:
		return false
	}
}

func extractAnsweredQuestionText(raw string) (string, bool) {
	idx := strings.Index(raw, questionAnsweredPrefix)
	if idx < 0 {
		return "", false
	}
	return strings.TrimSpace(raw[idx:]), true
}

func normalizeQuestionToolResult(normTool string, status *string, output, errMsg *string) {
	if normTool != "question" || status == nil || output == nil || errMsg == nil {
		return
	}
	if ans, ok := extractAnsweredQuestionText(*errMsg); ok {
		*status = "completed"
		*output = ans
		*errMsg = ""
		return
	}
	if ans, ok := extractAnsweredQuestionText(*output); ok {
		*status = "completed"
		*output = ans
		*errMsg = ""
	}
}

func (s *Service) lookupSessionQuestion(sessionID, callID string) (resolvedQuestion, bool) {
	sessionID = strings.TrimSpace(sessionID)
	callID = strings.TrimSpace(callID)
	if sessionID == "" {
		return resolvedQuestion{}, false
	}
	callKey := sessionID + "\x00" + callID
	s.gate.mu.Lock()
	defer s.gate.mu.Unlock()
	if callID != "" {
		if r, ok := s.gate.resolvedQuestions[callKey]; ok {
			return r, true
		}
		if p := s.gate.questionsByCallKey[callKey]; p != nil {
			return resolvedQuestion{questions: p.questions}, true
		}
	}
	if r, ok := s.gate.resolvedQuestions[sessionID+"\x00latest"]; ok {
		return r, true
	}
	for _, p := range s.gate.questions {
		if p.sid == sessionID {
			return resolvedQuestion{questions: p.questions}, true
		}
	}
	return resolvedQuestion{}, false
}

func (s *Service) decorateQuestionToolEvent(sessionID string, evt *SSEEvent) {
	if evt == nil || evt.Type != "message.part.updated" || evt.Properties == nil {
		return
	}
	part, ok := evt.Properties["part"].(MessagePart)
	if !ok || part.Type != "tool" || part.Tool != "question" || part.State == nil {
		return
	}
	q, found := s.lookupSessionQuestion(sessionID, part.CallID)
	if !found {
		return
	}
	part = clonePart(part)
	if len(q.questions) > 0 && !hasQuestionsInput(part.State.Input) {
		if part.State.Input == nil {
			part.State.Input = make(map[string]interface{})
		}
		part.State.Input["questions"] = q.questions
	}
	if q.resolved && (part.State.Status == "completed" || part.State.Status == "error") {
		if part.State.Metadata == nil {
			part.State.Metadata = make(map[string]interface{})
		}
		if q.rejected {
			part.State.Status = "error"
			part.State.Output = ""
			part.State.Error = firstNonEmptyStr(q.reason, questionDismissedReason)
			part.State.Metadata["rejected"] = true
		} else {
			part.State.Status = "completed"
			part.State.Output = q.output
			part.State.Error = ""
			part.State.Metadata["answers"] = q.answers
		}
	}
	evt.Properties["part"] = part
}

func (s *Service) clearSessionQuestionState(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	prefix := sessionID + "\x00"
	s.gate.mu.Lock()
	for k := range s.gate.resolvedQuestions {
		if strings.HasPrefix(k, prefix) {
			delete(s.gate.resolvedQuestions, k)
		}
	}
	s.gate.mu.Unlock()
}

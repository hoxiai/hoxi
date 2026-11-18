package turnstream

import (
	"encoding/json"
	"strings"
)

const QuestionAnsweredPrefix = "User has answered your questions: "

func IsQuestionToolName(name string) bool { return isQuestionToolName(name) }
func HasQuestionsInput(input map[string]interface{}) bool {
	return hasQuestionsInput(input)
}
func NormalizeRunnerQuestions(rawArgs map[string]interface{}) []map[string]interface{} {
	return normalizeRunnerQuestions(rawArgs)
}
func NormalizeQuestionToolResult(normTool string, status *string, output, errMsg *string) {
	normalizeQuestionToolResult(normTool, status, output, errMsg)
}

func isQuestionToolName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "question", "plan_consent", "ask_question", "askuserquestion", "ask_user_question", "ask_user":
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
	idx := strings.Index(raw, QuestionAnsweredPrefix)
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

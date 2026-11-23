// Package canonical defines Hoxi's provider-agnostic canonical message,
// content-block, tool-outcome, and history-projection invariants shared across
// session storage and agent execution layers.
package canonical

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/hoxiai/hoxi/internal/dataprofiles"
)

const (
	ToolOutcomeCompleted   = "completed"
	ToolOutcomeFailed      = "failed"
	ToolOutcomeCancelled   = "cancelled"
	ToolOutcomeInterrupted = "interrupted"
	ToolOutcomeUnknown     = "outcome_unknown"

	TurnFinishInterrupted = "interrupted"
	InterruptedToolError  = "Tool execution was interrupted before completion."
)

// TodoItem represents a persisted session checklist item across turn boundaries.
type TodoItem struct {
	ID       string `json:"id"`
	Content  string `json:"content"`
	Status   string `json:"status"`
	Priority string `json:"priority,omitempty"`
}

// ContentBlock represents one provider-neutral block inside a canonical Message.
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	MimeType  string          `json:"mime_type,omitempty"`
	Data      string          `json:"data,omitempty"`
	Filename  string          `json:"filename,omitempty"`
	Signature string          `json:"signature,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// Message is one chronological user or assistant exchange in canonical history.
type Message struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// AppendMessage merges consecutive same-role blocks or appends a new Message.
func AppendMessage(msgs []Message, role string, blocks []ContentBlock) []Message {
	if len(blocks) == 0 {
		return msgs
	}
	if len(msgs) > 0 && msgs[len(msgs)-1].Role == role {
		msgs[len(msgs)-1].Content = append(msgs[len(msgs)-1].Content, blocks...)
		return msgs
	}
	copied := make([]ContentBlock, len(blocks))
	copy(copied, blocks)
	return append(msgs, Message{Role: role, Content: copied})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstMapString(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// StringMeta reads a trimmed string metadata entry.
func StringMeta(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	s, _ := meta[key].(string)
	return strings.TrimSpace(s)
}

// IntMeta reads a positive/signed integer metadata entry across numeric JSON representations.
func IntMeta(meta map[string]any, key string) int {
	if meta == nil {
		return 0
	}
	switch v := meta[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	default:
		return 0
	}
}

// IsMutatingToolCall reports whether a tool invocation may mutate workspace
// files, processes, or external state so interrupted turns never blindly replay.
func IsMutatingToolCall(toolName string, input map[string]any) bool {
	lower := strings.ToLower(strings.TrimSpace(toolName))
	switch lower {
	case "", "read", "glob", "grep", "hoxi_code_index", "code_search", "symbol_inspect", "impact_analysis",
		"todoread", "todowrite", "question", "plan_consent", "webfetch", "websearch", "mcp_list_tools", "load_host_tool",
		"skill", "goal_report", "task":
		return false
	case "bash":
		act := strings.ToLower(firstMapString(input["action"]))
		return act != "output" && act != "list"
	case "hoxi_browser":
		act := strings.ToLower(firstMapString(input["action"]))
		return act != "snapshot" && act != "screenshot" && act != "diagnostics" && act != "wait"
	case "hoxi_computer":
		act := strings.ToLower(firstMapString(input["action"]))
		if act == "status" || act == "apps" {
			return false
		}
		if (act == "state" || act == "screenshot" || act == "wait") && firstMapString(input["app"]) == "" {
			return false
		}
		return true
	case "hoxi_tools":
		act := strings.ToLower(firstMapString(input["action"]))
		return act != "discover" && act != "inspect"
	case "hoxi_remote":
		act := strings.ToLower(firstMapString(input["action"]))
		switch act {
		case "list", "sql_query":
			return false
		case "http_request":
			m := strings.ToUpper(firstMapString(input["method"]))
			return m != "" && m != "GET" && m != "HEAD"
		default:
			return true
		}
	case "hoxi_data":
		act := strings.ToLower(firstMapString(input["action"]))
		switch act {
		case "list", "test":
			return false
		case "http_request":
			m := strings.ToUpper(firstMapString(input["method"]))
			return m != "" && m != "GET" && m != "HEAD"
		case "sql_query":
			ro, _ := dataprofiles.IsReadOnlySQL(firstMapString(input["query"]))
			return !ro
		case "ssh_exec":
			ro, _ := dataprofiles.IsReadOnlySSHCommand(firstMapString(input["command"]))
			return !ro
		default:
			return true
		}
	case "write", "edit", "multiedit", "apply_patch", "shell", "create_file",
		"mcp_call_tool", "hoxi_image", "hoxi_video", "hoxi_speech":
		return true
	default:
		if strings.HasSuffix(lower, "_read") || strings.HasPrefix(lower, "list_") || strings.HasPrefix(lower, "get_") {
			return false
		}
		return true
	}
}

// SameProducerModel reports whether a turn's (providerID, modelID) matches the outgoing target.
func SameProducerModel(turnProviderID, turnModelID, targetProviderID, targetModelID string) bool {
	if strings.TrimSpace(turnProviderID) == "" || strings.TrimSpace(turnModelID) == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(turnProviderID), strings.TrimSpace(targetProviderID)) &&
		strings.EqualFold(strings.TrimSpace(turnModelID), strings.TrimSpace(targetModelID))
}

// KeepReasoningAffinity checks whether private thinking/signature blocks should
// replay to (targetProviderID, targetModelID). Switching models strips private
// thinking while keeping canonical text and tool history intact.
func KeepReasoningAffinity(
	turnProviderID, turnModelID string,
	partText string,
	partMeta map[string]any,
	targetProviderID, targetModelID string,
) (sig string, keep bool) {
	if partMeta != nil {
		if inv, ok := partMeta["signatureInvalidated"].(bool); ok && inv {
			return "", false
		}
	}
	sig = StringMeta(partMeta, "signature")
	redacted := StringMeta(partMeta, "redactedData")
	srcProv := firstNonEmpty(StringMeta(partMeta, "providerID"), turnProviderID)
	srcModel := firstNonEmpty(StringMeta(partMeta, "modelID"), turnModelID)
	targetProv := strings.TrimSpace(targetProviderID)
	targetMod := strings.TrimSpace(targetModelID)
	if srcProv == "" || srcModel == "" || targetProv == "" || targetMod == "" {
		return "", false
	}
	if !strings.EqualFold(srcProv, targetProv) || !strings.EqualFold(srcModel, targetMod) {
		return "", false
	}
	if strings.TrimSpace(partText) == "" && sig == "" && redacted == "" {
		return "", false
	}
	return sig, true
}

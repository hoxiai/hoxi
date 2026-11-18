package turnstream

import (
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/agentcli"
	"github.com/hoxiai/hoxi/internal/llm"
	"github.com/hoxiai/hoxi/internal/runner/sessionstore"
)

type (
	SessionRecord    = sessionstore.SessionRecord
	SessionInfo      = sessionstore.SessionInfo
	SessionStatus    = sessionstore.SessionStatus
	MessageTurn      = sessionstore.MessageTurn
	MessageInfo      = sessionstore.MessageInfo
	MessageTime      = sessionstore.MessageTime
	MessageTokens    = sessionstore.MessageTokens
	MessageError     = sessionstore.MessageError
	MessageErrorData = sessionstore.MessageErrorData
	MessagePart      = sessionstore.MessagePart
	PartTime         = sessionstore.PartTime
	ToolState        = sessionstore.ToolState
	SSEEvent         = sessionstore.SSEEvent
)

const (
	RuntimeOpenCode    = sessionstore.RuntimeOpenCode
	RuntimeCodex       = sessionstore.RuntimeCodex
	RuntimeAntigravity = sessionstore.RuntimeAntigravity
	RuntimeClaudeCode  = sessionstore.RuntimeClaudeCode
	RuntimeGeminiCLI   = sessionstore.RuntimeGeminiCLI
	RuntimeCodeBuddy   = sessionstore.RuntimeCodeBuddy
	RuntimeQoder       = sessionstore.RuntimeQoder
	RuntimeNative      = sessionstore.RuntimeNative

	AgyHookName          = "hoxi-approval-gate"
	InterruptedToolError = "Interrupted before completion"

	agyHookName          = AgyHookName
	interruptedToolError = InterruptedToolError
)

func IsClaudeProtocolRuntime(rt string) bool     { return sessionstore.IsClaudeProtocolRuntime(rt) }
func clonePart(p MessagePart) MessagePart        { return sessionstore.ClonePart(p) }
func cloneMessageInfo(i MessageInfo) MessageInfo { return sessionstore.CloneMessageInfo(i) }
func validStoreID(id string) bool                { return sessionstore.ValidStoreID(id) }
func geminiHomeDir() string                      { return agentcli.GeminiHomeDir() }
func isRetryableTurnError(errText string) bool   { return llm.IsTransientTurnError(errText) }

func IsInFlightToolPart(p MessagePart) bool { return isInFlightToolPart(p) }

func isInFlightToolPart(p MessagePart) bool {
	return p.Type == "tool" && p.Tool != "task" && p.State != nil &&
		(p.State.Status == "pending" || p.State.Status == "running")
}

func NewClassifiedMessageError(name, message string, status int) *MessageError {
	return newClassifiedMessageError(name, message, status)
}

func newClassifiedMessageError(name, message string, status int) *MessageError {
	fault := llm.ClassifyFault(name, message, status, time.Now())
	return &MessageError{
		Name: name,
		Data: &MessageErrorData{
			Message:      message,
			StatusCode:   status,
			IsRetryable:  fault.IsRetryable,
			Kind:         string(fault.Kind),
			RetryAfterMs: fault.RetryAfterMs,
		},
	}
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func stringList(v interface{}) []string {
	switch val := v.(type) {
	case string:
		if strings.TrimSpace(val) != "" {
			return []string{val}
		}
	case []string:
		var out []string
		for _, s := range val {
			if strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	case []interface{}:
		var out []string
		for _, item := range val {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func FirstString(vals ...interface{}) string            { return firstString(vals...) }
func IntFromMap(m map[string]interface{}, k string) int { return intFromMap(m, k) }
func AgyBrainRoots() []string                           { return agyBrainRoots() }
func AgyTranscriptLogDir(root, externalID string) string {
	return agyTranscriptLogDir(root, externalID)
}
func DecodeAgyToolArgs(raw map[string]interface{}) map[string]interface{} {
	return decodeAgyToolArgs(raw)
}
func EventSessionID(evt SSEEvent) string { return eventSessionID(evt) }
func GeminiErrorText(raw []byte) string  { return geminiErrorText(raw) }
func HasAgySplitBrainConversation(externalID string) bool {
	return hasAgySplitBrainConversation(externalID)
}
func HydrateAgyTranscriptEditParts(externalID string, turns []MessageTurn) bool {
	return hydrateAgyTranscriptEditParts(externalID, turns)
}
func HydrateAgyTranscriptTurnTokens(externalID string, turns []MessageTurn) bool {
	return hydrateAgyTranscriptTurnTokens(externalID, turns)
}
func LookupAgyTranscriptThinking(externalID string, stepIndex int) string {
	return lookupAgyTranscriptThinking(externalID, stepIndex)
}
func PartUpdated(part MessagePart) SSEEvent { return partUpdated(part) }
func PreserveToolEditInput(incoming *MessagePart, existing MessagePart) {
	preserveToolEditInput(incoming, existing)
}
func QuarantineEmptyAgySQLiteStore(externalID string) bool {
	return quarantineEmptyAgySQLiteStore(externalID)
}

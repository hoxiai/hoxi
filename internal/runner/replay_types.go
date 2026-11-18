package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var replayHex64Pattern = regexp.MustCompile(`\b[0-9a-fA-F]{64}\b`)

// ReplayFixture defines a deterministic offline acceptance scenario exercising
// runner turns, approvals, subagents, SSE catch-up, and the 3-state request ledger.
type ReplayFixture struct {
	Name          string               `json:"name"`
	Description   string               `json:"description"`
	EngineCalls   []ReplayEngineTurn   `json:"engineCalls"`
	ClientActions []ReplayClientAction `json:"clientActions"`
	Expect        ReplayExpectation    `json:"expect"`
}

// ReplayEngineTurn scripts one invocation of NativeEngine.RunTurn.
type ReplayEngineTurn struct {
	Steps []ReplayEngineStep `json:"steps"`
}

// ReplayEngineStep scripts one deterministic step inside NativeEngine.RunTurn.
type ReplayEngineStep struct {
	Op           string         `json:"op"`
	Gate         string         `json:"gate,omitempty"`
	Event        *NativeEvent   `json:"event,omitempty"`
	CallID       string         `json:"callId,omitempty"`
	ToolName     string         `json:"toolName,omitempty"`
	Input        map[string]any `json:"input,omitempty"`
	IsRead       bool           `json:"isRead,omitempty"`
	ForceAsk     bool           `json:"forceAsk,omitempty"`
	ExpectSteers int            `json:"expectSteers,omitempty"`
}

// ReplayClientAction scripts one desktop or mobile client operation during a replay run.
type ReplayClientAction struct {
	Name            string `json:"name"`
	Op              string `json:"op"`
	Gate            string `json:"gate,omitempty"`
	Text            string `json:"text,omitempty"`
	Reply           string `json:"reply,omitempty"`
	Message         string `json:"message,omitempty"`
	ClientRequestID string `json:"clientRequestId,omitempty"`
	ExpectHTTP      int    `json:"expectHttp,omitempty"`
	ExpectCode      string `json:"expectCode,omitempty"`
	ExpectDedup     bool   `json:"expectDedup,omitempty"`
}

// ReplayExpectation declares post-run invariants verified against the final store & SSE stream.
type ReplayExpectation struct {
	UserTurnCount        int      `json:"userTurnCount,omitempty"`
	MinTotalTurns        int      `json:"minTotalTurns,omitempty"`
	NoRunningTools       bool     `json:"noRunningTools,omitempty"`
	ChildSessionsCount   int      `json:"childSessionsCount,omitempty"`
	ChildSessionsSettled bool     `json:"childSessionsSettled,omitempty"`
	CatchupContainsTypes []string `json:"catchupContainsTypes,omitempty"`
	NoResyncOnCatchup    bool     `json:"noResyncOnCatchup,omitempty"`
}

// ReplayActionOutcome records the observed HTTP status and response metadata of one client action.
type ReplayActionOutcome struct {
	Name         string `json:"name"`
	HTTPStatus   int    `json:"httpStatus,omitempty"`
	ErrorCode    string `json:"errorCode,omitempty"`
	Deduplicated bool   `json:"deduplicated,omitempty"`
}

// ReplayRunReport summarizes the deterministic execution of a ReplayFixture.
type ReplayRunReport struct {
	SessionID      string                `json:"sessionId"`
	FinalSeq       uint64                `json:"finalSeq"`
	ActionOutcomes []ReplayActionOutcome `json:"actionOutcomes"`
	CatchupEvents  []SSEEvent            `json:"catchupEvents,omitempty"`
	Session        SessionRecord         `json:"session"`
	Children       []SessionRecord       `json:"children,omitempty"`
}

// ScrubReplayText replaces caller-provided path/value mappings and 64-char hex digests.
func ScrubReplayText(raw string, replacements map[string]string) string {
	out := raw
	for k, v := range replacements {
		if strings.TrimSpace(k) != "" {
			out = strings.ReplaceAll(out, k, v)
		}
	}
	return replayHex64Pattern.ReplaceAllString(out, "<HEX64_REDACTED>")
}

// LoadReplayFixture reads and parses a JSON ReplayFixture from disk.
func LoadReplayFixture(path string) (ReplayFixture, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ReplayFixture{}, err
	}
	var fx ReplayFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		return ReplayFixture{}, fmt.Errorf("decode replay fixture %s: %w", filepath.Base(path), err)
	}
	return fx, nil
}

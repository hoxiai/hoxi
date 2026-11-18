package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

type scriptedConsentStream struct {
	stepRequests []ModelRequest
}

func (s *scriptedConsentStream) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	return llm.StreamTarget{
		ProviderID:   providerID,
		ModelID:      modelID,
		BaseURL:      "https://api.example.invalid",
		Protocol:     llm.ProtocolOpenAI,
		HasEndpoint:  true,
		ContextLimit: 128000,
		OutputLimit:  8192,
	}
}

func (s *scriptedConsentStream) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(string(req.Body))),
	}, nil
}

type scriptedConsentDriver struct {
	stream   *scriptedConsentStream
	stepFunc func(step int, req ModelRequest) []StreamEvent
	stepSeq  int
}

func (d *scriptedConsentDriver) Create(_ llm.StreamProtocol) (ModelDriver, error) {
	return d, nil
}

func (d *scriptedConsentDriver) Protocol() llm.StreamProtocol { return llm.ProtocolOpenAI }
func (d *scriptedConsentDriver) SystemPromptPatch() string    { return "" }

func (d *scriptedConsentDriver) BuildRequest(req ModelRequest) (llm.StreamHTTPRequest, error) {
	d.stream.stepRequests = append(d.stream.stepRequests, req)
	return llm.StreamHTTPRequest{
		Target: req.Target,
		Body:   []byte("{}"),
	}, nil
}

func (d *scriptedConsentDriver) ParseSSE(_ context.Context, _ io.Reader, emit func(StreamEvent)) error {
	d.stepSeq++
	idx := d.stepSeq
	req := d.stream.stepRequests[len(d.stream.stepRequests)-1]
	for _, ev := range d.stepFunc(idx, req) {
		emit(ev)
	}
	return nil
}

func hasToolSpec(specs []ToolSpec, want string) bool {
	for _, sp := range specs {
		if sp.Name == want {
			return true
		}
	}
	return false
}

func TestPlanConsentApproveAndBuildPromotesSameTurn(t *testing.T) {
	workDir := t.TempDir()
	stream := &scriptedConsentStream{}
	drv := &scriptedConsentDriver{
		stream: stream,
		stepFunc: func(step int, req ModelRequest) []StreamEvent {
			switch step {
			case 1:
				if hasToolSpec(req.Tools, "write") {
					t.Errorf("step 1 in plan mode must NOT expose write tool")
				}
				if !hasToolSpec(req.Tools, "plan_consent") {
					t.Errorf("step 1 in plan mode MUST expose plan_consent tool")
				}
				args, _ := json.Marshal(map[string]any{
					"title":   "Add release notes",
					"summary": "Write summary file in workspace",
					"steps":   []string{"Create summary.md", "Verify output"},
					"files":   []string{"summary.md"},
				})
				return []StreamEvent{
					{
						Kind: EventToolCallReady,
						ToolCall: &ToolCall{
							ID:    "call_consent_1",
							Name:  "plan_consent",
							Input: args,
						},
					},
					{Kind: EventFinish, Finish: "tool_calls"},
				}
			case 2:
				if !hasToolSpec(req.Tools, "write") {
					t.Errorf("step 2 after Approve & Build MUST expose write tool in the same turn")
				}
				writeArgs, _ := json.Marshal(map[string]any{
					"filePath": "summary.md",
					"content":  "# Approved\n",
				})
				return []StreamEvent{
					{
						Kind: EventToolCallReady,
						ToolCall: &ToolCall{
							ID:    "call_write_2",
							Name:  "write",
							Input: writeArgs,
						},
					},
					{Kind: EventFinish, Finish: "tool_calls"},
				}
			default:
				return []StreamEvent{
					{Kind: EventTextDelta, Delta: "Implemented approved plan."},
					{Kind: EventFinish, Finish: "stop"},
				}
			}
		},
	}

	eng := NewEngine(stream, drv, newToolRegistry(), nil)
	var promotedEvents []EngineEvent
	err := eng.RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_consent_1",
		WorktreeDir: workDir,
		Mode:        "plan",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Plan and write summary.md",
		AskQuestion: func(_ context.Context, _ string, rawJSON string) (string, map[string]any, error) {
			var payload map[string]any
			if err := json.Unmarshal([]byte(rawJSON), &payload); err != nil {
				return "", nil, err
			}
			if payload["plan"] == nil {
				t.Errorf("expected structured plan metadata in AskQuestion payload")
			}
			return "Answered Approve & Build", map[string]any{
				"answers": [][]string{{PlanConsentApproveAndBuild}},
			}, nil
		},
		Sink: EventSinkFunc(func(evt EngineEvent) {
			if evt.Kind == EngineModePromoted {
				promotedEvents = append(promotedEvents, evt)
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if len(promotedEvents) != 1 || promotedEvents[0].Text != "build" {
		t.Fatalf("expected 1 EngineModePromoted('build') event, got %+v", promotedEvents)
	}
	gotBytes, err := os.ReadFile(filepath.Join(workDir, "summary.md"))
	if err != nil {
		t.Fatalf("expected summary.md written in step 2 after promotion: %v", err)
	}
	if strings.TrimSpace(string(gotBytes)) != "# Approved" {
		t.Fatalf("unexpected file content: %q", string(gotBytes))
	}
}

func TestPlanConsentKeepPlanOnlyStaysReadOnly(t *testing.T) {
	workDir := t.TempDir()
	stream := &scriptedConsentStream{}
	drv := &scriptedConsentDriver{
		stream: stream,
		stepFunc: func(step int, req ModelRequest) []StreamEvent {
			if step == 1 {
				args, _ := json.Marshal(map[string]any{
					"title":   "Draft plan",
					"summary": "Stay in plan mode",
					"steps":   []string{"Inspect", "Summarize"},
				})
				return []StreamEvent{
					{Kind: EventToolCallReady, ToolCall: &ToolCall{ID: "call_1", Name: "plan_consent", Input: args}},
					{Kind: EventFinish, Finish: "tool_calls"},
				}
			}
			if hasToolSpec(req.Tools, "write") {
				t.Errorf("expected write tool to remain locked when user picks Keep in Plan Mode")
			}
			return []StreamEvent{
				{Kind: EventTextDelta, Delta: "Plan finalized in read-only mode."},
				{Kind: EventFinish, Finish: "stop"},
			}
		},
	}

	eng := NewEngine(stream, drv, newToolRegistry(), nil)
	err := eng.RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_consent_2",
		WorktreeDir: workDir,
		Mode:        "plan",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Propose a plan",
		AskQuestion: func(_ context.Context, _ string, _ string) (string, map[string]any, error) {
			return "Answered", map[string]any{
				"answers": [][]string{{PlanConsentKeepPlanOnly}},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
}

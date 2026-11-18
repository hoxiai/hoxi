package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

type replayGateHub struct {
	mu    sync.Mutex
	gates map[string]chan struct{}
}

func newReplayGateHub() *replayGateHub {
	return &replayGateHub{gates: make(map[string]chan struct{})}
}

func (h *replayGateHub) ch(name string) chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.gates[name]
	if !ok {
		c = make(chan struct{})
		h.gates[name] = c
	}
	return c
}

func (h *replayGateHub) signal(name string) {
	c := h.ch(name)
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case <-c:
	default:
		close(c)
	}
}

func (h *replayGateHub) wait(ctx context.Context, name string) error {
	select {
	case <-h.ch(name):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type replayScriptedEngine struct {
	mu      sync.Mutex
	calls   []ReplayEngineTurn
	callIdx int
	gates   *replayGateHub
}

func (e *replayScriptedEngine) Summarize(_ context.Context, _, _, transcript string) (string, error) {
	return "Summary: " + transcript, nil
}

func (e *replayScriptedEngine) RunTurn(ctx context.Context, req NativeTurnRequest) error {
	e.mu.Lock()
	idx := e.callIdx
	e.callIdx++
	var turn ReplayEngineTurn
	if idx < len(e.calls) {
		turn = e.calls[idx]
	}
	e.mu.Unlock()

	for _, step := range turn.Steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch step.Op {
		case "emit":
			if step.Event != nil && req.Emit != nil {
				req.Emit(*step.Event)
			}
		case "signal_gate":
			e.gates.signal(step.Gate)
		case "wait_gate":
			if err := e.gates.wait(ctx, step.Gate); err != nil {
				return err
			}
		case "wait_abort":
			<-ctx.Done()
			return ctx.Err()
		case "drain_steer":
			var drained int
			if req.SteerDrainItems != nil {
				drained = len(req.SteerDrainItems())
			} else if req.SteerDrain != nil {
				drained = len(req.SteerDrain())
			}
			if step.ExpectSteers > 0 && drained != step.ExpectSteers {
				return fmt.Errorf("expected %d steered items, drained %d", step.ExpectSteers, drained)
			}
		case "request_permission":
			if req.PermissionHook != nil {
				rawIn, _ := json.Marshal(step.Input)
				callID := firstNonEmptyStr(step.CallID, "call_perm_1")
				toolName := firstNonEmptyStr(step.ToolName, "bash")
				err := req.PermissionHook(ctx, NativePermissionRequest{
					SessionID: req.SessionID,
					CallID:    callID,
					ToolName:  toolName,
					Input:     rawIn,
					IsRead:    step.IsRead,
					ForceAsk:  step.ForceAsk,
				})
				if err != nil && ctx.Err() != nil {
					return ctx.Err()
				}
			}
		default:
			return fmt.Errorf("unknown replay engine step op %q", step.Op)
		}
	}
	return ctx.Err()
}

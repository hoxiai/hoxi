package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
)

func checkReplayHTTP(act ReplayClientAction, rec *httptest.ResponseRecorder) (ReplayActionOutcome, error) {
	out := ReplayActionOutcome{
		Name:         act.Name,
		HTTPStatus:   rec.Code,
		Deduplicated: rec.Header().Get("X-Hoxi-Deduplicated") == "true",
	}
	var payload map[string]any
	if json.Unmarshal(rec.Body.Bytes(), &payload) == nil {
		if code, ok := payload["code"].(string); ok {
			out.ErrorCode = code
		}
		if dedup, ok := payload["deduplicated"].(bool); ok && dedup {
			out.Deduplicated = true
		}
	}
	if act.ExpectHTTP > 0 && out.HTTPStatus != act.ExpectHTTP {
		return out, fmt.Errorf("action %q HTTP=%d want %d (%s)", act.Name, out.HTTPStatus, act.ExpectHTTP, rec.Body.String())
	}
	if act.ExpectCode != "" && out.ErrorCode != act.ExpectCode {
		return out, fmt.Errorf("action %q code=%q want %q (%s)", act.Name, out.ErrorCode, act.ExpectCode, rec.Body.String())
	}
	if act.ExpectDedup && !out.Deduplicated {
		return out, fmt.Errorf("action %q expected deduplicated=true (%s)", act.Name, rec.Body.String())
	}
	return out, nil
}

func assertReplayReport(exp ReplayExpectation, report *ReplayRunReport, svc *Service) error {
	userTurns := 0
	for _, t := range report.Session.Turns {
		if t.Info.Role == "user" {
			userTurns++
		}
		if exp.NoRunningTools {
			for _, p := range t.Parts {
				if p.Type == "tool" && p.State != nil && (p.State.Status == "running" || p.State.Status == "pending") {
					return fmt.Errorf("unsettled parent tool %s (%s)", p.ID, p.State.Status)
				}
			}
		}
	}
	if exp.UserTurnCount > 0 && userTurns != exp.UserTurnCount {
		return fmt.Errorf("userTurns=%d want %d", userTurns, exp.UserTurnCount)
	}
	if exp.MinTotalTurns > 0 && len(report.Session.Turns) < exp.MinTotalTurns {
		return fmt.Errorf("totalTurns=%d want >= %d", len(report.Session.Turns), exp.MinTotalTurns)
	}
	if exp.ChildSessionsCount > 0 && len(report.Children) != exp.ChildSessionsCount {
		return fmt.Errorf("children=%d want %d", len(report.Children), exp.ChildSessionsCount)
	}
	if exp.ChildSessionsSettled {
		for _, child := range report.Children {
			if svc.isSessionBusy(child.ID) {
				return fmt.Errorf("child %s still busy", child.ID)
			}
			for _, ct := range child.Turns {
				for _, p := range ct.Parts {
					if p.Type == "tool" && p.State != nil && (p.State.Status == "running" || p.State.Status == "pending") {
						return fmt.Errorf("unsettled child tool %s (%s)", p.ID, p.State.Status)
					}
				}
			}
		}
	}
	if exp.NoResyncOnCatchup {
		for _, evt := range report.CatchupEvents {
			if evt.Type == resyncEventType {
				return errors.New("unexpected resync event during bounded catch-up")
			}
		}
	}
	if len(exp.CatchupContainsTypes) > 0 {
		seen := make(map[string]bool)
		var prevSeq uint64
		for _, evt := range report.CatchupEvents {
			if evt.Seq <= prevSeq {
				return fmt.Errorf("non-monotonic seq %d after %d", evt.Seq, prevSeq)
			}
			prevSeq = evt.Seq
			seen[evt.Type] = true
		}
		for _, want := range exp.CatchupContainsTypes {
			if !seen[want] {
				return fmt.Errorf("missing catchup event type %q", want)
			}
		}
	}
	return nil
}

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/worktree"
)

// RunReplayFixture executes a deterministic offline ReplayFixture in-process
// without real LLM tokens or external network sockets, verifying turn state,
// subagent cleanup, SSE replay catch-up, and 3-state request ledger invariants.
func RunReplayFixture(ctx context.Context, fx ReplayFixture, repoDir, storeDir string) (*ReplayRunReport, error) {
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	wtMgr := worktree.NewManager(repoDir)
	svc := NewService(repoDir, wtMgr, statusbar.NewTracker(), storeDir)
	defer svc.CloseAll()

	gates := newReplayGateHub()
	eng := &replayScriptedEngine{
		calls: fx.EngineCalls,
		gates: gates,
	}
	svc.SetNativeEngine(eng)

	sessionInfo, _, err := svc.CreateSession(CreateSessionRequest{
		Title:       fx.Name,
		Directory:   repoDir,
		Runtime:     string(RuntimeNative),
		ModelID:     "replay-scripted-v1",
		ProviderID:  "replay",
		Mode:        "build",
		SandboxMode: "local",
	}, repoDir)
	if err != nil {
		return nil, fmt.Errorf("create replay session: %w", err)
	}

	report := &ReplayRunReport{
		SessionID: sessionInfo.ID,
	}
	var markedSeq uint64
	var activeDone <-chan struct{}

	for _, act := range fx.ClientActions {
		switch act.Op {
		case "prompt":
			status, receipt, pErr := svc.startPromptWithReceipt(runCtx, sessionInfo.ID, repoDir, PromptRequest{
				Text:            act.Text,
				Runtime:         string(RuntimeNative),
				ClientRequestID: act.ClientRequestID,
			})
			svc.mu.Lock()
			if cur := svc.procs[sessionInfo.ID]; cur != nil {
				activeDone = cur.done
			}
			svc.mu.Unlock()

			out := ReplayActionOutcome{
				Name:         act.Name,
				HTTPStatus:   status,
				Deduplicated: receipt.Deduplicated,
			}
			var coded *CodedRequestError
			if errors.As(pErr, &coded) && coded != nil {
				out.ErrorCode = coded.Code
				if coded.HTTPStatus > 0 {
					out.HTTPStatus = coded.HTTPStatus
				}
			}
			if err := verifyReplayOutcome(act, out, pErr); err != nil {
				return nil, err
			}
			report.ActionOutcomes = append(report.ActionOutcomes, out)

		case "abort":
			svc.AbortSession(sessionInfo.ID)
			out := ReplayActionOutcome{Name: act.Name, HTTPStatus: http.StatusOK}
			if err := verifyReplayOutcome(act, out, nil); err != nil {
				return nil, err
			}
			report.ActionOutcomes = append(report.ActionOutcomes, out)

		case "wait_gate":
			if err := gates.wait(runCtx, act.Gate); err != nil {
				return nil, fmt.Errorf("action %q wait_gate(%s): %w", act.Name, act.Gate, err)
			}

		case "signal_gate":
			gates.signal(act.Gate)

		case "wait_idle":
			if activeDone != nil {
				select {
				case <-activeDone:
				case <-runCtx.Done():
					return nil, fmt.Errorf("action %q timed out waiting for turn exit: %w", act.Name, runCtx.Err())
				}
			}
			for svc.isSessionBusy(sessionInfo.ID) {
				select {
				case <-runCtx.Done():
					return nil, fmt.Errorf("action %q timed out waiting for session idle: %w", act.Name, runCtx.Err())
				case <-time.After(5 * time.Millisecond):
				}
			}

		case "write_workspace_file":
			target := filepath.Join(repoDir, "notes.txt")
			if err := os.WriteFile(target, []byte(act.Text), 0o644); err != nil {
				return nil, fmt.Errorf("action %q write file: %w", act.Name, err)
			}

		case "worktree_commit":
			out, cErr := runReplayWorktreeCommit(svc, wtMgr, sessionInfo.ID, repoDir, act)
			if err := verifyReplayOutcome(act, out, cErr); err != nil {
				return nil, err
			}
			report.ActionOutcomes = append(report.ActionOutcomes, out)

		case "mark_sse_seq":
			markedSeq = svc.CurrentEventSeq()

		case "sse_catchup":
			ch, unsub := svc.SubscribeSince(sessionInfo.ID, markedSeq)
			var caught []SSEEvent
		drainLoop:
			for {
				select {
				case evt := <-ch:
					caught = append(caught, evt)
				default:
					break drainLoop
				}
			}
			unsub()
			report.CatchupEvents = caught

		default:
			return nil, fmt.Errorf("unknown replay client action op %q", act.Op)
		}
	}

	rec, ok := svc.store.Get(sessionInfo.ID)
	if !ok {
		return nil, fmt.Errorf("replay session %s missing from store", sessionInfo.ID)
	}
	report.Session = rec
	report.FinalSeq = svc.CurrentEventSeq()

	childIDs, _ := svc.store.ChildSessionIDs(sessionInfo.ID)
	for _, cid := range childIDs {
		if childRec, exists := svc.store.Get(cid); exists {
			report.Children = append(report.Children, childRec)
		}
	}

	if err := assertReplayReport(fx.Expect, report, svc); err != nil {
		return report, err
	}
	return report, nil
}

func runReplayWorktreeCommit(svc *Service, wtMgr *worktree.Manager, sessionID, repoDir string, act ReplayClientAction) (ReplayActionOutcome, error) {
	scope := worktree.CommitRequestScope(sessionID, repoDir)
	digest := worktree.CommitRequestDigest(scope, act.Message, false, nil)
	preHead := worktree.ObserveGitHead(repoDir)
	dec := svc.AdmitCommit(scope, sessionID, act.ClientRequestID, digest, map[string]string{
		"workDir": repoDir,
		"preHead": preHead,
	})

	out := ReplayActionOutcome{Name: act.Name}
	switch dec.State {
	case worktree.CommitAdmissionDone:
		out.HTTPStatus = dec.HTTPStatus
		if out.HTTPStatus <= 0 {
			out.HTTPStatus = http.StatusOK
		}
		out.Deduplicated = true
		return out, nil
	case worktree.CommitAdmissionConflict, worktree.CommitAdmissionUnknown, worktree.CommitAdmissionInFlight:
		out.HTTPStatus = dec.HTTPStatus
		if out.HTTPStatus <= 0 {
			out.HTTPStatus = http.StatusConflict
		}
		out.ErrorCode = dec.ErrorCode
		return out, nil
	}

	res, err := wtMgr.ForDirectory(repoDir).CommitAndPushWorktree(sessionID, act.Message, false)
	if err != nil {
		postHead := worktree.ObserveGitHead(repoDir)
		if res.Commit != "" || (preHead != "" && postHead != "" && postHead != preHead) {
			svc.MarkCommitUnknown(scope, act.ClientRequestID, err.Error())
		} else {
			svc.AbortUncommittedCommit(scope, act.ClientRequestID)
		}
		out.HTTPStatus = http.StatusBadRequest
		return out, err
	}
	res.ClientRequestID = act.ClientRequestID
	if rawResp, mErr := json.Marshal(res); mErr == nil {
		svc.CompleteCommit(scope, act.ClientRequestID, http.StatusOK, append(rawResp, '\n'))
	}
	out.HTTPStatus = http.StatusOK
	return out, nil
}

func verifyReplayOutcome(act ReplayClientAction, out ReplayActionOutcome, rawErr error) error {
	if act.ExpectHTTP > 0 && out.HTTPStatus != act.ExpectHTTP {
		return fmt.Errorf("action %q HTTP=%d want %d (err=%v)", act.Name, out.HTTPStatus, act.ExpectHTTP, rawErr)
	}
	if act.ExpectCode != "" && out.ErrorCode != act.ExpectCode {
		return fmt.Errorf("action %q code=%q want %q (err=%v)", act.Name, out.ErrorCode, act.ExpectCode, rawErr)
	}
	if act.ExpectDedup && !out.Deduplicated {
		return fmt.Errorf("action %q expected deduplicated=true", act.Name)
	}
	return nil
}

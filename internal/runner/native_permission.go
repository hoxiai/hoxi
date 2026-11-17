package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func (s *Service) nativePermissionHook(rec SessionRecord, asstMsgID string) func(context.Context, NativePermissionRequest) error {
	return func(ctx context.Context, req NativePermissionRequest) error {
		fakeHTTP, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
		cred := hookCredential{runtime: string(RuntimeNative), sessionID: rec.ID, messageID: asstMsgID}
		lowerTool := strings.ToLower(strings.TrimSpace(req.ToolName))

		if !(req.ForceAsk && lowerTool == "task") {
			if ok, reason := s.gateDoomLoopKeyed(fakeHTTP, cred, rec.ID, req.ToolName, req.Input, req.CallID); !ok {
				return fmt.Errorf("%s", reason)
			}
		}

		var args map[string]interface{}
		_ = json.Unmarshal(req.Input, &args)
		if args == nil {
			args = map[string]interface{}{}
		}

		if req.ForceAsk {
			approved, reason := s.gateExplicitAskToolCall(fakeHTTP, cred, req.ToolName, args, req.CallID)
			if !approved {
				return fmt.Errorf("%s", reason)
			}
			return nil
		}

		if isQuestionToolName(lowerTool) || lowerTool == "todowrite" || lowerTool == "todoread" ||
			lowerTool == "skill" || lowerTool == "goal_report" || lowerTool == "hoxi_computer" {
			return nil
		}
		if lowerTool == "hoxi_browser" {
			act := strings.ToLower(strings.TrimSpace(firstString(args["action"])))
			if act == "snapshot" || act == "screenshot" {
				return nil
			}
			approved, reason := s.gateToolCall(fakeHTTP, cred, req.ToolName, args, req.CallID)
			if !approved {
				return fmt.Errorf("%s", reason)
			}
			return nil
		}
		if lowerTool == "hoxi_tools" {
			act := strings.ToLower(strings.TrimSpace(firstString(args["action"])))
			if act == "discover" || act == "inspect" {
				return nil
			}
			approved, reason := s.gateToolCall(fakeHTTP, cred, req.ToolName, args, req.CallID)
			if !approved {
				return fmt.Errorf("%s", reason)
			}
			return nil
		}
		if lowerTool == "hoxi_remote" {
			act := strings.ToLower(strings.TrimSpace(firstString(args["action"])))
			if act == "list" {
				return nil
			}
			approved, reason := s.gateToolCall(fakeHTTP, cred, req.ToolName, args, req.CallID)
			if !approved {
				return fmt.Errorf("%s", reason)
			}
			return nil
		}
		if lowerTool == "hoxi_image" || lowerTool == "webfetch" || lowerTool == "mcp_call_tool" || lowerTool == "mcp_list_tools" {
			approved, reason := s.gateToolCall(fakeHTTP, cred, req.ToolName, args, req.CallID)
			if !approved {
				return fmt.Errorf("%s", reason)
			}
			return nil
		}
		if lowerTool == "bash" {
			cmd, _ := args["command"].(string)
			approved, reason := s.gateCommand(fakeHTTP, cred, rec, approvalAsk{
				command: cmd, cwd: rec.WorktreePath, callID: req.CallID, source: string(RuntimeNative),
			})
			if !approved {
				return fmt.Errorf("%s", reason)
			}
			return nil
		}
		if lowerTool == "apply_patch" {
			targets := extractNativePatchTargets(rec.WorktreePath, args)
			if len(targets) > 0 {
				allApproved := true
				for _, t := range targets {
					if !pathWithin(rec.WorktreePath, t) || isProtectedWorktreePath(rec.WorktreePath, t) ||
						isSecretFilePath(t) || !s.policyApprovesWorktreeEdit(rec, t) {
						allApproved = false
						break
					}
				}
				if allApproved {
					return nil
				}
			}
		} else if req.IsRead {
			target := resolveHookToolPath(rec.WorktreePath, args)
			if !hasEscapingSearchPattern(args) &&
				(target == "" || (pathWithin(rec.WorktreePath, target) && !isSensitiveExternalReadPath(target))) &&
				!isSecretFilePath(target) {
				return nil
			}
		} else if target := resolveHookToolPath(rec.WorktreePath, args); target != "" {
			if pathWithin(rec.WorktreePath, target) && !isProtectedWorktreePath(rec.WorktreePath, target) &&
				!isSecretFilePath(target) && s.policyApprovesWorktreeEdit(rec, target) {
				return nil
			}
		}
		approved, reason := s.gateToolCall(fakeHTTP, cred, req.ToolName, args, req.CallID)
		if !approved {
			return fmt.Errorf("%s", reason)
		}
		return nil
	}
}

// gateExplicitAskToolCall enforces an explicit per-tool or per-agent "ask"
// policy (such as permission.task=ask) so general smart/auto/trusted modes
// cannot silently auto-approve it without user confirmation.
func (s *Service) gateExplicitAskToolCall(r *http.Request, cred hookCredential, tool string, args map[string]interface{}, callID string) (bool, string) {
	key := hookToolGrantKey(tool, args)
	targetPath, worktree, destructive, externalDir := s.inspectHookToolRiskIn(cred.sessionID, tool, "", args)
	if !destructive && s.gate.hasToolAlways(cred.sessionID, key) {
		return true, ""
	}
	id := runnerPermissionID + randomHex(12)
	pretty, _ := json.MarshalIndent(args, "", "  ")
	meta := map[string]interface{}{
		"tool":        tool,
		"args":        string(pretty),
		"source":      firstNonEmptyStr(cred.runtime, string(RuntimeNative)),
		"explicitAsk": true,
	}
	if worktree != "" {
		meta["cwd"] = worktree
	}
	if targetPath != "" {
		meta["filepath"] = targetPath
	}
	if destructive {
		meta["destructive"] = true
	}
	if externalDir {
		meta["externalDirectory"] = true
	}
	if desc := firstString(args["toolSummary"], args["description"], args["Action"]); desc != "" {
		meta["description"] = desc
	}
	if rec, ok := s.store.Meta(cred.sessionID); ok && strings.TrimSpace(rec.ExternalID) != "" {
		meta["externalID"] = rec.ExternalID
	}
	permReq := map[string]interface{}{
		"id":         id,
		"sessionID":  cred.sessionID,
		"permission": "tool",
		"patterns":   []string{tool},
		"metadata":   meta,
		"always":     []string{tool},
		"tool": map[string]interface{}{
			"messageID": cred.messageID,
			"callID":    callID,
		},
	}
	if s.ApprovalPolicy().Mode == ApprovalModeUnattended {
		s.recordUnattendedDeny(cred, permReq, unattendedCategoryTool)
		return false, unattendedDenyReason(unattendedCategoryTool)
	}
	answer := s.awaitReply(r, cred, id, permReq)
	switch answer.Reply {
	case "always":
		if !destructive {
			s.gate.addToolAlways(cred.sessionID, key, tool, summarizeToolGrantPattern(tool, args), answer.Source)
		}
		return true, ""
	case "once":
		return true, ""
	}
	reason := "The user rejected the " + tool + " call in Hoxi"
	if msg := strings.TrimSpace(answer.Message); msg != "" {
		reason += ": " + msg
	}
	return false, reason
}

func extractNativePatchTargets(worktree string, args map[string]interface{}) []string {
	raw := firstString(args["patch"], args["diff"])
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		var rel string
		switch {
		case strings.HasPrefix(trimmed, "*** Add File:"):
			rel = strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Add File:"))
		case strings.HasPrefix(trimmed, "*** Update File:"):
			rel = strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Update File:"))
		case strings.HasPrefix(trimmed, "*** Delete File:"):
			rel = strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Delete File:"))
		case strings.HasPrefix(trimmed, "*** Move to:"):
			rel = strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Move to:"))
		case strings.HasPrefix(trimmed, "+++ ") && trimmed != "+++ /dev/null":
			cand := strings.TrimSpace(strings.TrimPrefix(trimmed, "+++ "))
			if idx := strings.IndexByte(cand, '\t'); idx >= 0 {
				cand = strings.TrimSpace(cand[:idx])
			}
			cand = strings.TrimPrefix(strings.TrimPrefix(cand, "b/"), "a/")
			rel = cand
		}
		if rel == "" {
			continue
		}
		resolved := resolveHookToolPath(worktree, map[string]interface{}{"path": rel})
		if resolved == "" {
			return nil
		}
		out = append(out, resolved)
	}
	return out
}

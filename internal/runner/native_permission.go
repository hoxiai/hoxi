package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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

		effRec := rec
		if latest, ok := s.effectiveSessionRecord(rec.ID); ok {
			effRec = latest
		}
		if reason, denied := sessionDeniedToolReason(effRec, req.ToolName, args); denied {
			return fmt.Errorf("%s", reason)
		}
		if reason, denied := sessionToolWriteViolation(effRec, effRec.WorktreePath, req.ToolName, args); denied {
			return fmt.Errorf("%s", reason)
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
			if act == "snapshot" || act == "screenshot" || act == "diagnostics" || act == "wait" {
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
		if lowerTool == "hoxi_schedule" {
			act := strings.ToLower(strings.TrimSpace(firstString(args["action"])))
			if act == "list" || act == "get" {
				if s.ApprovalPolicy().Mode != ApprovalModeManual {
					return nil
				}
				approved, reason := s.gateToolCall(fakeHTTP, cred, req.ToolName, args, req.CallID)
				if !approved {
					return fmt.Errorf("%s", reason)
				}
				return nil
			}
			if strings.TrimSpace(rec.ScheduleID) != "" || s.isScheduledTurnActive(rec.ID) {
				return fmt.Errorf("scheduled runs cannot create, modify, or delete scheduled tasks")
			}
			if s.ApprovalPolicy().Mode == ApprovalModeUnattended {
				return fmt.Errorf("scheduled task changes require desktop confirmation and are unavailable in unattended mode")
			}
			approved, reason := s.gateScheduleMutation(fakeHTTP, cred, rec, args, req.CallID)
			if !approved {
				return fmt.Errorf("%s", reason)
			}
			return nil
		}
		if lowerTool == "hoxi_remote" || lowerTool == "hoxi_data" {
			act := strings.ToLower(strings.TrimSpace(firstString(args["action"])))
			if act == "list" || act == "test" {
				return nil
			}
			mode := s.ApprovalPolicy().Mode
			if lowerTool == "hoxi_data" && (mode == ApprovalModeAuto || mode == ApprovalModeTrusted) {
				switch act {
				case "http_request":
					m := strings.ToUpper(strings.TrimSpace(firstString(args["method"])))
					if m == "" || m == http.MethodGet || m == http.MethodHead {
						return nil
					}
				case "sql_query":
					if ro, _ := isReadOnlyDataSQLQuery(firstString(args["query"])); ro {
						return nil
					}
				case "ssh_exec":
					if s.isSafeReadOnlySSHProfileCall(firstString(args["profile"]), firstString(args["command"])) {
						return nil
					}
				}
			}
			approved, reason := s.gateToolCall(fakeHTTP, cred, req.ToolName, args, req.CallID)
			if !approved {
				return fmt.Errorf("%s", reason)
			}
			return nil
		}
		if lowerTool == "hoxi_image" || lowerTool == "hoxi_video" || lowerTool == "hoxi_speech" {
			if s.nativeImageCallAutoAllowed(rec, args) {
				return nil
			}
			approved, reason := s.gateToolCall(fakeHTTP, cred, req.ToolName, args, req.CallID)
			if !approved {
				return fmt.Errorf("%s", reason)
			}
			return nil
		}
		if lowerTool == "mcp_list_tools" {
			if s.ApprovalPolicy().Mode != ApprovalModeManual {
				return nil
			}
			approved, reason := s.gateToolCall(fakeHTTP, cred, req.ToolName, args, req.CallID)
			if !approved {
				return fmt.Errorf("%s", reason)
			}
			return nil
		}
		if lowerTool == "webfetch" || lowerTool == "websearch" || lowerTool == "mcp_call_tool" {
			approved, reason := s.gateToolCall(fakeHTTP, cred, req.ToolName, args, req.CallID)
			if !approved {
				return fmt.Errorf("%s", reason)
			}
			return nil
		}
		if lowerTool == "bash" {
			act := ""
			if rawAct, exists := args["action"]; exists && rawAct != nil {
				sAct, ok := rawAct.(string)
				if !ok {
					return fmt.Errorf("invalid bash action type")
				}
				act = strings.ToLower(strings.TrimSpace(sAct))
			}
			switch act {
			case "", "run":
				cmd, _ := args["command"].(string)
				cwd := strings.TrimSpace(firstString(args["workdir"], args["cwd"], rec.WorktreePath))
				approved, reason := s.gateCommand(fakeHTTP, cred, effRec, approvalAsk{
					command: cmd, cwd: cwd, callID: req.CallID, source: string(RuntimeNative),
				})
				if !approved {
					return fmt.Errorf("%s", reason)
				}
				return nil
			case "output", "list":
				return nil
			case "stop":
				procID := strings.TrimSpace(firstString(args["procId"]))
				pol, _ := s.policy.get()
				if pol.Mode != ApprovalModeManual && s.bgProcs != nil && s.bgProcs.owns(rec.ID, procID) {
					s.recordRunnerApprovalAudit(cred, map[string]interface{}{
						"permission": "bash",
						"metadata":   map[string]interface{}{"tool": "bash", "command": "stop " + procID, "cwd": rec.WorktreePath},
					}, "once", approvalReply{Reply: "once", Source: approvalSourceOwnProcess})
					return nil
				}
				approved, reason := s.gateExplicitAskToolCall(fakeHTTP, cred, req.ToolName, args, req.CallID)
				if !approved {
					return fmt.Errorf("%s", reason)
				}
				return nil
			default:
				return fmt.Errorf("unsupported bash action %q", act)
			}
		}
		readRoots, writeRoots := s.sessionWorkspaceRoots(rec)
		matchedToolRoot := rec.WorktreePath
		if lowerTool == "apply_patch" {
			targets := extractNativePatchTargetsInRoots(writeRoots, rec.WorktreePath, args)
			if len(targets) > 0 {
				allApproved := true
				for _, t := range targets {
					root, inRoot := matchSessionRoot(writeRoots, t)
					if inRoot {
						matchedToolRoot = root
					}
					if !inRoot || isProtectedWorktreePath(root, t) ||
						isSecretFilePath(t) || !s.policyApprovesRootEdit(root, t) {
						allApproved = false
						break
					}
				}
				if allApproved {
					return nil
				}
			}
		} else if req.IsRead {
			rawTarget := rawHookToolPath(args)
			target := resolveHookToolPath(rec.WorktreePath, args)
			root, inReadRoot := matchSessionRoot(readRoots, target)
			if inReadRoot {
				matchedToolRoot = root
			}
			protectedCodeIdx := lowerTool == "hoxi_code_index" && target != "" && (!inReadRoot || isProtectedWorktreePath(root, target))
			if !protectedCodeIdx && !hasRiskySearchSelector(lowerTool, args) &&
				((rawTarget == "" && target == "") || (target != "" && inReadRoot && !isSensitiveExternalReadPath(target))) &&
				!isSecretFilePath(target) && !isSecretFilePath(rawTarget) {
				return nil
			}
		} else if target := resolveHookToolPath(rec.WorktreePath, args); target != "" {
			if root, inWriteRoot := matchSessionRoot(writeRoots, target); inWriteRoot {
				matchedToolRoot = root
				if !isProtectedWorktreePath(root, target) && !isSecretFilePath(target) && s.policyApprovesRootEdit(root, target) {
					return nil
				}
			}
		}
		approved, reason := s.gateNativeToolCall(fakeHTTP, cred, rec, req.ToolName, args, req.CallID, matchedToolRoot)
		if !approved {
			return fmt.Errorf("%s", reason)
		}
		return nil
	}
}

func (s *Service) gateNativeToolCall(
	r *http.Request,
	cred hookCredential,
	rec SessionRecord,
	tool string,
	args map[string]interface{},
	callID, matchedRoot string,
) (bool, string) {
	baseRoot := firstNonEmptyStr(strings.TrimSpace(matchedRoot), rec.WorktreePath)
	key := hookToolGrantKey(tool, args)
	targetPath, _, destructive, externalDir := s.inspectHookToolRiskIn(cred.sessionID, tool, baseRoot, args)
	if targetPath != "" {
		readRoots, writeRoots := s.sessionWorkspaceRoots(rec)
		roots := writeRoots
		lowerTool := strings.ToLower(strings.TrimSpace(tool))
		if readToolNames[lowerTool] && !editToolNames[lowerTool] {
			roots = readRoots
		}
		if root, ok := matchSessionRoot(roots, targetPath); ok {
			baseRoot = root
			externalDir = false
			if editToolNames[lowerTool] && isProtectedWorktreePath(root, targetPath) {
				destructive = true
			}
		}
	}
	if !destructive && s.gate.hasToolAlways(cred.sessionID, key) {
		return true, ""
	}
	id := runnerPermissionID + randomHex(12)
	pretty, _ := json.MarshalIndent(args, "", "  ")
	meta := map[string]interface{}{
		"tool":   tool,
		"args":   string(pretty),
		"source": string(RuntimeNative),
	}
	if baseRoot != "" {
		meta["cwd"] = baseRoot
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
	if desc := firstNonEmptyStr(firstString(args["toolSummary"], args["description"], args["Action"]), summarizeHookToolAction(tool, args)); desc != "" {
		meta["description"] = desc
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
		if allow, category := unattendedDecision(permReq, baseRoot); !allow && category != "" {
			s.recordUnattendedDeny(cred, permReq, category)
			return false, unattendedDenyReason(category)
		}
		if unattendedIsolated(rec) && s.policyApprovesIsolated(permReq, baseRoot, true) {
			s.recordPolicyApproval(cred, permReq)
			s.noteTurn(cred.sessionID, "allow")
			return true, ""
		}
	}
	lowerToolName := strings.ToLower(strings.TrimSpace(tool))
	isExternalEdit := externalDir && editToolNames[lowerToolName]
	if !isExternalEdit && s.policyApproves(permReq, baseRoot) {
		s.recordPolicyApproval(cred, permReq)
		return true, ""
	}
	if s.classGrantApproves(permReq, cred.sessionID, baseRoot) {
		s.recordRunnerApprovalAudit(cred, permReq, "once", approvalReply{Reply: "once", Source: grantSourceSession})
		return true, ""
	}
	annotateGrantClasses(permReq, baseRoot)
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

func (s *Service) gateScheduleMutation(
	r *http.Request,
	cred hookCredential,
	rec SessionRecord,
	args map[string]interface{},
	callID string,
) (bool, string) {
	id := runnerPermissionID + randomHex(12)
	pretty, _ := json.MarshalIndent(args, "", "  ")
	act := strings.ToLower(strings.TrimSpace(firstString(args["action"])))
	if act == "" {
		act = "mutate"
	}
	name := strings.TrimSpace(firstString(args["name"], args["id"]))
	summary := "hoxi_schedule " + act
	if name != "" {
		summary += ": " + name
	}
	if cronExpr := strings.TrimSpace(firstString(args["cron"])); cronExpr != "" {
		summary += " (" + cronExpr + ")"
	} else if iv, ok := args["intervalMinutes"].(float64); ok && iv > 0 {
		summary += fmt.Sprintf(" (every %dm)", int(iv))
	}
	meta := map[string]interface{}{
		"tool":        "hoxi_schedule",
		"action":      act,
		"args":        string(pretty),
		"source":      string(RuntimeNative),
		"destructive": true,
		"desktopOnly": true,
		"description": summary,
	}
	if rec.WorktreePath != "" {
		meta["cwd"] = rec.WorktreePath
	}
	permReq := map[string]interface{}{
		"id":         id,
		"sessionID":  cred.sessionID,
		"permission": "hoxi_schedule",
		"patterns":   []string{summary},
		"always":     []string{},
		"metadata":   meta,
		"tool": map[string]interface{}{
			"messageID": cred.messageID,
			"callID":    callID,
		},
	}
	answer := s.awaitReply(r, cred, id, permReq)
	if answer.Reply == "once" {
		return true, ""
	}
	reason := "The user rejected the scheduled task change in Hoxi"
	if msg := strings.TrimSpace(answer.Message); msg != "" {
		reason += ": " + msg
	}
	return false, reason
}

func extractNativePatchTargetsInRoots(writeRoots []string, defaultRoot string, args map[string]interface{}) []string {
	targets := extractNativePatchTargets(defaultRoot, args)
	if len(targets) == 0 || len(writeRoots) <= 1 {
		return targets
	}
	return targets
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

func (s *Service) nativeImageCallAutoAllowed(rec SessionRecord, args map[string]interface{}) bool {
	if strings.TrimSpace(rec.WorktreePath) == "" {
		return false
	}
	for _, inPath := range stringList(args["inputPaths"]) {
		resolvedIn := resolveHookToolPath(rec.WorktreePath, map[string]interface{}{"path": inPath})
		if resolvedIn == "" || !pathWithin(rec.WorktreePath, resolvedIn) ||
			isProtectedWorktreePath(rec.WorktreePath, resolvedIn) ||
			isSecretFilePath(resolvedIn) || isSensitiveExternalReadPath(resolvedIn) {
			return false
		}
	}
	outPath := strings.TrimSpace(firstString(args["outputPath"]))
	if outPath == "" {
		return s.ApprovalPolicy().Mode != ApprovalModeManual
	}
	resolvedOut := resolveHookToolPath(rec.WorktreePath, map[string]interface{}{"path": outPath})
	if resolvedOut == "" || !pathWithin(rec.WorktreePath, resolvedOut) ||
		isProtectedWorktreePath(rec.WorktreePath, resolvedOut) || isSecretFilePath(resolvedOut) {
		return false
	}
	return s.policyApprovesWorktreeEdit(rec, resolvedOut)
}

// sessionWorkspaceRoots returns the directories a session may read and write
// without an external-directory boundary. In local mode all folders configured
// on the workspace project are writable; in isolated worktree mode writes stay
// strictly inside rec.WorktreePath while sibling workspace folders remain readable.
func (s *Service) sessionWorkspaceRoots(rec SessionRecord) (readRoots, writeRoots []string) {
	seenRead := make(map[string]bool)
	seenWrite := make(map[string]bool)
	addRead := func(dir string) {
		dir = filepath.Clean(strings.TrimSpace(dir))
		if dir == "" || dir == "." {
			return
		}
		key := normalizeComparePath(dir)
		if key == "" || seenRead[key] {
			return
		}
		seenRead[key] = true
		readRoots = append(readRoots, dir)
	}
	addWrite := func(dir string) {
		dir = filepath.Clean(strings.TrimSpace(dir))
		if dir == "" || dir == "." {
			return
		}
		key := normalizeComparePath(dir)
		if key == "" || seenWrite[key] {
			return
		}
		seenWrite[key] = true
		writeRoots = append(writeRoots, dir)
		addRead(dir)
	}

	if strings.TrimSpace(rec.WorktreePath) != "" {
		addWrite(rec.WorktreePath)
	}
	if s != nil && strings.TrimSpace(rec.ID) != "" {
		if bundle, bErr := s.ResolveSessionBundle(rec.ID, rec.WorktreePath); bErr == nil {
			for _, dir := range bundle.ReadOnlyExecRoots {
				addRead(dir)
			}
			for _, dir := range bundle.ExtraSkillDirs {
				addRead(dir)
			}
		}
	}
	if s == nil || s.projectResolver == nil || strings.TrimSpace(rec.ProjectID) == "" {
		return readRoots, writeRoots
	}
	projID := strings.TrimSpace(rec.ProjectID)
	if live, ok := s.canonicalProjectID(projID); ok && live != "" {
		projID = live
	}
	isolated := rec.SandboxModeValue() == "worktree" || isIsolatedWorktree(rec.WorktreePath)
	projDirKey := normalizeComparePath(rec.ProjectDir)
	for _, rawRoot := range s.projectResolver.ProjectRoots(projID) {
		root := strings.TrimSpace(rawRoot)
		if root == "" {
			continue
		}
		st, err := os.Stat(root)
		if err != nil || !st.IsDir() {
			continue
		}
		if isolated {
			if projDirKey != "" && normalizeComparePath(root) == projDirKey {
				continue
			}
			addRead(root)
		} else {
			addWrite(root)
		}
	}
	return readRoots, writeRoots
}

func matchSessionRoot(roots []string, target string) (string, bool) {
	if strings.TrimSpace(target) == "" {
		return "", false
	}
	best := ""
	for _, root := range roots {
		if pathWithin(root, target) && len(root) > len(best) {
			best = root
		}
	}
	return best, best != ""
}

func (s *Service) policyApprovesRootEdit(rootDir, absTarget string) bool {
	target := absTarget
	if rel, err := filepath.Rel(rootDir, absTarget); err == nil && rootDir != "" {
		target = filepath.ToSlash(rel)
	}
	return s.policyApproves(map[string]interface{}{"permission": "edit", "patterns": []string{target}}, rootDir)
}

// grantableRunnerToolEditFiles enables "本会话内允许改此文件" (file:<abs>) on
// runner/native structured edit requests targeting grantable protected files
// (such as .agents/skills/*, .hoxi/skills/*, .vscode/settings.json) while
// refusing secrets, .git, .opencode, .claude, .gemini, hoxi.json, hooks, and /etc.
func grantableRunnerToolEditFiles(item map[string]interface{}, dir string) []string {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if !filepath.IsAbs(dir) || permissionName(item) != "tool" {
		return nil
	}
	meta := itemMetadata(item)
	if explicit, _ := meta["explicitAsk"].(bool); explicit {
		return nil
	}
	if ext, _ := meta["externalDirectory"].(bool); ext {
		return nil
	}
	toolObj, _ := item["tool"].(map[string]interface{})
	toolName := strings.ToLower(strings.TrimSpace(firstString(meta["tool"], toolObj["name"])))
	if !editToolNames[toolName] || !isDestructivePermissionRequest(item) {
		return nil
	}
	var raw []string
	for _, key := range []string{"filepath", "filePath", "path", "target"} {
		raw = append(raw, stringList(meta[key])...)
	}
	var parsedArgs map[string]interface{}
	switch a := meta["args"].(type) {
	case string:
		_ = json.Unmarshal([]byte(a), &parsedArgs)
	case map[string]interface{}:
		parsedArgs = a
	}
	if parsedArgs != nil {
		if p := rawHookToolPath(parsedArgs); p != "" {
			raw = append(raw, p)
		}
		raw = append(raw, stringList(parsedArgs["paths"])...)
		if toolName == "apply_patch" {
			for _, t := range extractNativePatchTargets(dir, parsedArgs) {
				raw = append(raw, t)
			}
		}
	}
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	hasProtected := false
	for _, p := range raw {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" || strings.HasPrefix(trimmed, "~") || strings.ContainsAny(trimmed, "*?[\x00\n") {
			return nil
		}
		joined := trimmed
		if !filepath.IsAbs(joined) {
			joined = filepath.Join(dir, joined)
		}
		joined = filepath.Clean(joined)
		resolved, ok := resolvePathWithSymlinks(joined, 0)
		if !ok || !filepath.IsAbs(resolved) || !pathWithin(dir, resolved) {
			return nil
		}
		for _, cand := range []string{trimmed, joined, resolved} {
			if fileGrantRefused(cand) {
				return nil
			}
		}
		if isProtectedWorktreePath(dir, resolved) {
			hasProtected = true
		}
		if cls := fileGrantPrefix + resolved; !seen[cls] {
			seen[cls] = true
			out = append(out, cls)
		}
	}
	if !hasProtected {
		return nil
	}
	sort.Strings(out)
	return out
}

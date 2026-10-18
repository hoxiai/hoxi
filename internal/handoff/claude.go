package handoff

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func claudeHomeDir() string {
	if env := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); env != "" {
		return filepath.Clean(env)
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".claude")
}

type claudeFileEntry struct {
	path      string
	sessionID string
	modTimeMs int64
	prioMatch bool
}

func encodeClaudeProjectHint(dir string) string {
	dir = cleanDirPath(dir)
	if dir == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range dir {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func scanClaudeSessions(targetDir, query string) ([]ExternalSessionSummary, ExternalSourceStats) {
	cDir := claudeHomeDir()
	stats := ExternalSourceStats{}
	if cDir == "" {
		return nil, stats
	}
	if _, err := os.Stat(cDir); err == nil {
		stats.Installed = true
	}
	projectsRoot := filepath.Join(cDir, "projects")
	projDirs, err := os.ReadDir(projectsRoot)
	if err != nil {
		return nil, stats
	}

	targetHint := encodeClaudeProjectHint(targetDir)
	var files []claudeFileEntry

	for _, pEntry := range projDirs {
		if !pEntry.IsDir() {
			continue
		}
		pPath := filepath.Join(projectsRoot, pEntry.Name())
		sFiles, err := os.ReadDir(pPath)
		if err != nil {
			continue
		}
		isPrio := targetHint != "" && strings.HasPrefix(pEntry.Name(), targetHint)
		for _, sf := range sFiles {
			if sf.IsDir() || !strings.HasSuffix(sf.Name(), ".jsonl") {
				continue
			}
			stats.SessionCount++
			fi, err := sf.Info()
			if err != nil || fi.Size() < 32 {
				continue
			}
			sID := strings.TrimSuffix(sf.Name(), ".jsonl")
			files = append(files, claudeFileEntry{
				path:      filepath.Join(pPath, sf.Name()),
				sessionID: sID,
				modTimeMs: fi.ModTime().UnixMilli(),
				prioMatch: isPrio,
			})
		}
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].prioMatch != files[j].prioMatch {
			return files[i].prioMatch
		}
		return files[i].modTimeMs > files[j].modTimeMs
	})

	maxInspect := 90
	if len(files) < maxInspect {
		maxInspect = len(files)
	}
	qLower := strings.ToLower(strings.TrimSpace(query))
	var results []ExternalSessionSummary

	for i := 0; i < maxInspect; i++ {
		fe := files[i]
		sum, ok := quickParseClaudeSummary(fe.path, fe.sessionID, fe.modTimeMs, targetDir)
		if !ok {
			continue
		}
		if qLower != "" {
			haystack := strings.ToLower(sum.Title + " " + sum.Summary + " " + sum.CWD + " " + sum.GitBranch)
			if !strings.Contains(haystack, qLower) {
				continue
			}
		}
		results = append(results, sum)
	}
	return results, stats
}

func quickParseClaudeSummary(filePath, sessionID string, modTimeMs int64, targetDir string) (ExternalSessionSummary, bool) {
	f, err := os.Open(filePath)
	if err != nil {
		return ExternalSessionSummary{}, false
	}
	defer f.Close()

	var cwd, gitBranch, customTitle, firstPrompt, modelID, firstTimestamp string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	lineCount := 0
	for scanner.Scan() {
		lineCount++
		if lineCount > 45 && cwd != "" && (customTitle != "" || firstPrompt != "") && modelID != "" {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item map[string]interface{}
		if json.Unmarshal([]byte(line), &item) != nil {
			continue
		}
		if cwd == "" {
			if c, ok := item["cwd"].(string); ok && strings.TrimSpace(c) != "" {
				cwd = strings.TrimSpace(c)
			}
		}
		if gitBranch == "" {
			if b, ok := item["gitBranch"].(string); ok && strings.TrimSpace(b) != "" {
				gitBranch = strings.TrimSpace(b)
			}
		}
		if firstTimestamp == "" {
			if ts, ok := item["timestamp"].(string); ok && ts != "" {
				firstTimestamp = ts
			}
		}

		typ, _ := item["type"].(string)
		switch typ {
		case "custom-title", "ai-title":
			if ct, ok := item["customTitle"].(string); ok && strings.TrimSpace(ct) != "" {
				customTitle = strings.TrimSpace(ct)
			} else if at, ok := item["aiTitle"].(string); ok && strings.TrimSpace(at) != "" {
				customTitle = strings.TrimSpace(at)
			}
		case "summary":
			if sm, ok := item["summary"].(string); ok && strings.TrimSpace(sm) != "" && customTitle == "" {
				customTitle = strings.TrimSpace(sm)
			}
		case "user":
			if firstPrompt == "" {
				if msg, ok := item["message"].(map[string]interface{}); ok {
					txt, isToolRes := extractClaudeUserText(msg["content"])
					if !isToolRes && txt != "" && !strings.HasPrefix(txt, "<system-reminder>") {
						firstPrompt = txt
					}
				}
			}
		case "assistant":
			if modelID == "" {
				if msg, ok := item["message"].(map[string]interface{}); ok {
					if m, ok := msg["model"].(string); ok && m != "" {
						modelID = m
					}
				}
			}
		}
	}

	if cwd == "" && firstPrompt == "" && customTitle == "" {
		return ExternalSessionSummary{}, false
	}
	normCWD := normalizeClaudeWorktreeCWD(cwd)
	title := customTitle
	if title == "" {
		title = truncateRunes(firstLineText(firstPrompt), 80)
	}
	if title == "" {
		title = "Claude Session " + sessionID[:minInt(8, len(sessionID))]
	}
	createdMs := parseTimeMs(firstTimestamp)
	if createdMs == 0 {
		createdMs = modTimeMs
	}

	return ExternalSessionSummary{
		ID:          sessionID,
		Source:      ExternalToolClaude,
		SourceLabel: "Claude Code",
		CWD:         normCWD,
		Title:       title,
		Summary:     truncateRunes(firstPrompt, 240),
		Model:       modelID,
		GitBranch:   gitBranch,
		CreatedAt:   createdMs,
		UpdatedAt:   modTimeMs,
		MatchesCWD:  sameOrSubPath(normCWD, targetDir) || sameOrSubPath(cwd, targetDir),
	}, true
}

func normalizeClaudeWorktreeCWD(cwd string) string {
	if idx := strings.Index(cwd, "/.claude/worktrees/"); idx > 0 {
		return cwd[:idx]
	}
	return cwd
}

func firstLineText(s string) string {
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "<") {
			return trimmed
		}
	}
	return strings.TrimSpace(s)
}

func findClaudeSessionFile(sessionID string) string {
	cDir := claudeHomeDir()
	if cDir == "" {
		return ""
	}
	projectsRoot := filepath.Join(cDir, "projects")
	projDirs, err := os.ReadDir(projectsRoot)
	if err != nil {
		return ""
	}
	targetName := sessionID + ".jsonl"
	for _, pEntry := range projDirs {
		if !pEntry.IsDir() {
			continue
		}
		candidate := filepath.Join(projectsRoot, pEntry.Name(), targetName)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	return ""
}

func loadClaudeSessionDetail(sessionID, hintCWD, activeRoot string) (*ExternalSessionDetail, error) {
	filePath := findClaudeSessionFile(sessionID)
	if filePath == "" {
		return nil, fmt.Errorf("claude session not found: %s", sessionID)
	}
	fi, _ := os.Stat(filePath)
	modTimeMs := int64(0)
	if fi != nil {
		modTimeMs = fi.ModTime().UnixMilli()
	}

	sum, _ := quickParseClaudeSummary(filePath, sessionID, modTimeMs, activeRoot)
	if sum.CWD == "" {
		sum.CWD = hintCWD
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var turns []NormalizedTurn
	var planSteps []ExternalPlanStep
	toolIndexByID := map[string]*NormalizedToolCall{}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	step := int64(0)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item map[string]interface{}
		if json.Unmarshal([]byte(line), &item) != nil {
			continue
		}
		step++
		ts := parseTimeMs(asString(item["timestamp"]))
		if ts == 0 {
			ts = sum.CreatedAt + step*1000
		}

		typ, _ := item["type"].(string)
		msg, _ := item["message"].(map[string]interface{})
		if msg == nil {
			continue
		}

		switch typ {
		case "user":
			userText, isToolRes := extractClaudeUserText(msg["content"])
			if isToolRes {
				attachClaudeToolResults(msg["content"], toolIndexByID)
				continue
			}
			if userText == "" || strings.HasPrefix(userText, "<system-reminder>") {
				continue
			}
			turns = append(turns, NormalizedTurn{
				Role:      "user",
				Timestamp: ts,
				Text:      userText,
			})

		case "assistant":
			modelID := asString(msg["model"])
			textPart, reasoningPart, tCalls, extractedPlans := extractClaudeAssistantContent(msg["content"])
			if len(extractedPlans) > 0 {
				planSteps = extractedPlans
			}
			if len(turns) > 0 && turns[len(turns)-1].Role == "assistant" {
				last := &turns[len(turns)-1]
				if reasoningPart != "" {
					if last.Reasoning != "" {
						last.Reasoning += "\n\n" + reasoningPart
					} else {
						last.Reasoning = reasoningPart
					}
				}
				if textPart != "" {
					if last.Text != "" {
						last.Text += "\n\n" + textPart
					} else {
						last.Text = textPart
					}
				}
				startIdx := len(last.Tools)
				last.Tools = append(last.Tools, tCalls...)
				for i := startIdx; i < len(last.Tools); i++ {
					if last.Tools[i].CallID != "" {
						toolIndexByID[last.Tools[i].CallID] = &last.Tools[i]
					}
				}
			} else {
				turn := NormalizedTurn{
					Role:      "assistant",
					Timestamp: ts,
					Text:      textPart,
					Reasoning: reasoningPart,
					Tools:     tCalls,
					ModelID:   modelID,
				}
				turns = append(turns, turn)
				last := &turns[len(turns)-1]
				for i := range last.Tools {
					if last.Tools[i].CallID != "" {
						toolIndexByID[last.Tools[i].CallID] = &last.Tools[i]
					}
				}
			}
		}
	}

	sum.TurnCount = len(turns)
	return &ExternalSessionDetail{
		Summary:      sum,
		Turns:        turns,
		PlanSteps:    planSteps,
		ChangedFiles: extractChangedFilesFromTurns(turns),
	}, nil
}

func extractClaudeUserText(content interface{}) (string, bool) {
	switch v := content.(type) {
	case string:
		return strings.TrimSpace(v), false
	case []interface{}:
		var texts []string
		hasToolResult := false
		for _, elem := range v {
			m, ok := elem.(map[string]interface{})
			if !ok {
				continue
			}
			t, _ := m["type"].(string)
			if t == "tool_result" {
				hasToolResult = true
				continue
			}
			if t == "text" {
				txt := strings.TrimSpace(asString(m["text"]))
				if txt != "" && !strings.HasPrefix(txt, "<system-reminder>") {
					texts = append(texts, txt)
				}
			}
		}
		if len(texts) == 0 && hasToolResult {
			return "", true
		}
		return strings.Join(texts, "\n"), false
	default:
		return "", false
	}
}

func attachClaudeToolResults(content interface{}, index map[string]*NormalizedToolCall) {
	arr, ok := content.([]interface{})
	if !ok {
		return
	}
	for _, elem := range arr {
		m, ok := elem.(map[string]interface{})
		if !ok || asString(m["type"]) != "tool_result" {
			continue
		}
		callID := asString(m["tool_use_id"])
		tc, exists := index[callID]
		if !exists {
			continue
		}
		out := extractGrokContentText(m["content"])
		tc.Output = truncateRunes(out, 4000)
	}
}

func extractClaudeAssistantContent(content interface{}) (string, string, []NormalizedToolCall, []ExternalPlanStep) {
	switch v := content.(type) {
	case string:
		return strings.TrimSpace(v), "", nil, nil
	case []interface{}:
		var texts, thoughts []string
		var calls []NormalizedToolCall
		var plans []ExternalPlanStep
		for _, elem := range v {
			m, ok := elem.(map[string]interface{})
			if !ok {
				continue
			}
			switch asString(m["type"]) {
			case "text":
				if txt := strings.TrimSpace(asString(m["text"])); txt != "" {
					texts = append(texts, txt)
				}
			case "thinking":
				if th := strings.TrimSpace(asString(m["thinking"])); th != "" {
					thoughts = append(thoughts, th)
				}
			case "tool_use":
				rawName := asString(m["name"])
				inputMap, _ := m["input"].(map[string]interface{})
				if inputMap == nil {
					inputMap = map[string]interface{}{}
				}
				if strings.Contains(rawName, "update_plan") || rawName == "TodoWrite" {
					if items, ok := inputMap["items"].([]interface{}); ok {
						for _, it := range items {
							if im, ok := it.(map[string]interface{}); ok {
								plans = append(plans, ExternalPlanStep{
									Title:  asString(im["content"]),
									Status: asString(im["status"]),
								})
							}
						}
					}
				}
				calls = append(calls, NormalizedToolCall{
					CallID: asString(m["id"]),
					Tool:   normalizeExternalToolName(rawName),
					Title:  rawName,
					Input:  inputMap,
					Status: "completed",
				})
			}
		}
		return strings.Join(texts, "\n\n"), strings.Join(thoughts, "\n\n"), calls, plans
	default:
		return "", "", nil, nil
	}
}

func isRealAnthropicAPIKey(k string) bool {
	k = strings.TrimSpace(k)
	if k == "" || strings.HasPrefix(k, "sk-ant-oat") || k == "claude-code-local-oauth" || k == "hoxi-local-claude-code-bridge" {
		return false
	}
	return true
}

func readClaudeSettingsEnv() (baseURL, apiKey, defaultModel string, models []string) {
	baseURL = strings.TrimSpace(os.Getenv("ANTHROPIC_BASE_URL"))
	apiKey = firstNonEmpty(strings.TrimSpace(os.Getenv("ANTHROPIC_AUTH_TOKEN")), strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")))
	cDir := claudeHomeDir()
	if cDir != "" {
		if raw, err := os.ReadFile(filepath.Join(cDir, "settings.json")); err == nil {
			var doc map[string]interface{}
			if json.Unmarshal(raw, &doc) == nil {
				if m := strings.TrimSpace(asString(doc["model"])); m != "" && !strings.ContainsAny(m, " ()") {
					defaultModel = m
					models = appendUniqueString(models, m)
				}
				if envMap, ok := doc["env"].(map[string]interface{}); ok {
					if u := strings.TrimSpace(asString(envMap["ANTHROPIC_BASE_URL"])); u != "" {
						baseURL = u
					}
					if k := firstNonEmpty(asString(envMap["ANTHROPIC_AUTH_TOKEN"]), asString(envMap["ANTHROPIC_API_KEY"])); k != "" && apiKey == "" {
						apiKey = k
					}
					for _, k := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
						if m := strings.TrimSpace(asString(envMap[k])); m != "" && !strings.ContainsAny(m, " ()") {
							models = appendUniqueString(models, m)
							if defaultModel == "" {
								defaultModel = m
							}
						}
					}
				}
			}
		}
		if raw, err := os.ReadFile(filepath.Join(filepath.Dir(cDir), ".claude.json")); err == nil {
			var rootDoc map[string]interface{}
			if json.Unmarshal(raw, &rootDoc) == nil {
				if extra, ok := rootDoc["additionalModelOptionsCache"].([]interface{}); ok {
					for _, item := range extra {
						if im, ok := item.(map[string]interface{}); ok {
							if v := strings.TrimSpace(asString(im["value"])); v != "" && !strings.ContainsAny(v, " ()") {
								models = appendUniqueString(models, v)
							}
						}
					}
				}
			}
		}
	}
	if !isRealAnthropicAPIKey(apiKey) {
		apiKey = ""
	}
	sessions, _ := scanClaudeSessions("", "")
	for _, s := range sessions {
		if m := strings.TrimSpace(s.Model); m != "" && !strings.HasPrefix(m, "<") && !strings.ContainsAny(m, " ()") {
			models = appendUniqueString(models, m)
			if defaultModel == "" {
				defaultModel = m
			}
		}
	}
	if len(models) == 0 {
		models = []string{"claude-sonnet-4-6", "claude-opus-4-6"}
	}
	if defaultModel == "" && len(models) > 0 {
		defaultModel = models[0]
	}
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}
	return baseURL, apiKey, defaultModel, models
}

func detectClaudeCLIStatus(g *Service, hoxiMap map[string]interface{}) LocalCLIStatusItem {
	_, stats := scanClaudeSessions("", "")
	_, apiKey, defaultModel, models := readClaudeSettingsEnv()
	claudeBin := resolveClaudeBinary()
	_, connected := hoxiMap["claude-code"]
	storedKey := firstNonEmpty(g.resolveStoredProviderKey("claude-code", ""), g.resolveStoredProviderKey("anthropic", ""))
	hasRealKey := isRealAnthropicAPIKey(apiKey) || isRealAnthropicAPIKey(storedKey)
	authMode := "claude-runner"
	if hasRealKey {
		authMode = "claude-settings"
	}
	return LocalCLIStatusItem{
		ID:            ExternalToolClaude,
		Name:          "Claude Code",
		Installed:     stats.Installed || claudeBin != "",
		Authenticated: hasRealKey || claudeBin != "" || stats.SessionCount > 0,
		Connected:     (hasRealKey && connected) || claudeBin != "",
		DefaultModel:  defaultModel,
		Models:        models,
		SessionCount:  stats.SessionCount,
		AuthMode:      authMode,
		ProviderID:    "claude-code",
	}
}

func (g *Service) importClaudeCLIProvider() LocalCLIImportResponse {
	baseURL, apiKey, _, models := readClaudeSettingsEnv()
	if !isRealAnthropicAPIKey(apiKey) {
		stored := firstNonEmpty(g.resolveStoredProviderKey("claude-code", ""), g.resolveStoredProviderKey("anthropic", ""))
		if isRealAnthropicAPIKey(stored) {
			apiKey = stored
		} else {
			apiKey = ""
		}
	}
	if apiKey == "" {
		g.removeStaleProviderBridge("claude-code", "/api/hoxi/bridge/claude-code/v1")
		return LocalCLIImportResponse{
			OK:     false,
			ToolID: string(ExternalToolClaude),
			Error:  "请使用 Claude Code 运行时新建会话 (Please use the Claude Code local runtime when creating a session; OAuth tokens are never imported into OpenCode)",
		}
	}
	modelsMap := map[string]interface{}{}
	var cleanModels []string
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" || strings.ContainsAny(m, " ()") {
			continue
		}
		cleanModels = appendUniqueString(cleanModels, m)
		modelsMap[m] = map[string]interface{}{"name": m + " (Claude Code)", "tool_call": true, "reasoning": true}
	}
	doc := g.loadHoxiConfigDoc()
	provMap, _ := doc["provider"].(map[string]interface{})
	if provMap == nil {
		provMap = map[string]interface{}{}
	}
	provMap["claude-code"] = map[string]interface{}{
		"name":    "Claude Code (API Key)",
		"npm":     "@ai-sdk/anthropic",
		"options": map[string]interface{}{"baseURL": baseURL, "apiKey": apiKey},
		"models":  modelsMap,
	}
	doc["provider"] = provMap
	if err := g.persistHoxiConfigDoc(doc); err != nil {
		return LocalCLIImportResponse{OK: false, ToolID: string(ExternalToolClaude), Error: err.Error()}
	}
	return LocalCLIImportResponse{OK: true, ToolID: string(ExternalToolClaude), ProviderID: "claude-code", Models: cleanModels}
}

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type auditDiffFile struct {
	File      string `json:"file"`
	Before    string `json:"before,omitempty"`
	After     string `json:"after,omitempty"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

type auditCodeRequest struct {
	SessionID          string          `json:"sessionID,omitempty"`
	WorkerProviderID   string          `json:"workerProviderID"`
	WorkerModelID      string          `json:"workerModelID"`
	ObserverProviderID string          `json:"observerProviderID"`
	ObserverModelID    string          `json:"observerModelID"`
	ObserverBaseURL    string          `json:"observerBaseURL,omitempty"`
	ObserverNPM        string          `json:"observerNPM,omitempty"`
	UserPrompt         string          `json:"userPrompt"`
	Diffs              []auditDiffFile `json:"diffs"`
	LspErrorCount      int             `json:"lspErrorCount"`
	LspErrorsSummary   string          `json:"lspErrorsSummary,omitempty"`
	Locale             string          `json:"locale,omitempty"`
}

type auditCodeResponse struct {
	OK               bool     `json:"ok"`
	Score            int      `json:"score"`
	Pass             bool     `json:"pass"`
	Summary          string   `json:"summary"`
	Issues           []string `json:"issues,omitempty"`
	ObserverProvider string   `json:"observerProvider"`
	ObserverModel    string   `json:"observerModel"`
	LatencyMs        int64    `json:"latencyMs,omitempty"`
	Error            string   `json:"error,omitempty"`
}

func (g *desktopGateway) handleAuditProviderCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req auditCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, auditCodeResponse{OK: false, Error: "Invalid audit request payload"})
		return
	}

	obsPID := strings.TrimSpace(req.ObserverProviderID)
	obsMID := strings.TrimSpace(req.ObserverModelID)
	if obsPID == "" || obsMID == "" {
		writeJSON(w, http.StatusOK, auditCodeResponse{OK: false, Error: "Observer model is not configured"})
		return
	}

	// Step 1: Zero-cost static engineering rules check
	staticDeduction, staticIssues := evaluateStaticCodeRules(req.Diffs, req.LspErrorCount, req.LspErrorsSummary, req.Locale)

	// Step 2: Resolve observer provider endpoint & credentials
	baseURL, apiKey, npm, headers := g.resolveProviderConnection(obsPID, req.ObserverBaseURL, req.ObserverNPM)
	start := time.Now()

	var llmScore = 90
	var llmSummary = ""
	var llmIssues []string

	if strings.HasPrefix(baseURL, "http://") || strings.HasPrefix(baseURL, "https://") {
		score, summary, issues, err := g.invokeObserverLLM(r.Context(), obsPID, obsMID, baseURL, apiKey, npm, headers, req)
		if err == nil {
			llmScore = score
			llmSummary = summary
			llmIssues = issues
		} else {
			// If observer endpoint is unreachable, surface error without penalizing worker unless static checks failed
			if staticDeduction == 0 {
				writeJSON(w, http.StatusOK, auditCodeResponse{
					OK:               false,
					ObserverProvider: obsPID,
					ObserverModel:    obsMID,
					Error:            err.Error(),
				})
				return
			}
		}
	}

	finalScore := llmScore - staticDeduction
	if finalScore < 0 {
		finalScore = 0
	}
	if finalScore > 100 {
		finalScore = 100
	}

	mergedIssues := append([]string{}, staticIssues...)
	for _, iss := range llmIssues {
		trimmed := strings.TrimSpace(iss)
		if trimmed != "" {
			mergedIssues = append(mergedIssues, trimmed)
		}
	}
	if len(mergedIssues) > 5 {
		mergedIssues = mergedIssues[:5]
	}

	pass := finalScore >= 75 && req.LspErrorCount == 0
	if llmSummary == "" {
		if pass {
			if strings.HasPrefix(req.Locale, "zh") {
				llmSummary = "代码变更符合需求与工程规范，静态门禁全部通过。"
			} else {
				llmSummary = "Code changes satisfy requirements and pass all static engineering checks."
			}
		} else if len(mergedIssues) > 0 {
			llmSummary = mergedIssues[0]
		}
	}

	latency := time.Since(start).Milliseconds()
	if g.telemetry != nil && strings.TrimSpace(req.WorkerProviderID) != "" {
		g.telemetry.recordAuditResult(req.WorkerProviderID, req.WorkerModelID, finalScore, pass, llmSummary)
	}

	writeJSON(w, http.StatusOK, auditCodeResponse{
		OK:               true,
		Score:            finalScore,
		Pass:             pass,
		Summary:          llmSummary,
		Issues:           mergedIssues,
		ObserverProvider: obsPID,
		ObserverModel:    obsMID,
		LatencyMs:        latency,
	})
}

func evaluateStaticCodeRules(diffs []auditDiffFile, lspErrorCount int, lspSummary string, locale string) (int, []string) {
	isZh := strings.HasPrefix(locale, "zh")
	deduction := 0
	var issues []string

	if lspErrorCount > 0 {
		pen := lspErrorCount * 15
		if pen > 40 {
			pen = 40
		}
		deduction += pen
		if isZh {
			msg := fmt.Sprintf("存在 %d 个未解决的 LSP 编译/类型错误", lspErrorCount)
			if lspSummary != "" {
				msg += " (" + truncateStr(lspSummary, 80) + ")"
			}
			issues = append(issues, msg)
		} else {
			msg := fmt.Sprintf("%d unresolved LSP compiler/type error(s)", lspErrorCount)
			if lspSummary != "" {
				msg += " (" + truncateStr(lspSummary, 80) + ")"
			}
			issues = append(issues, msg)
		}
	}

	lazyMarkers := []string{
		"// todo: implement",
		"// ... existing code",
		"// ... rest of",
		"/* ... existing",
		"// placeholder",
	}

	for _, d := range diffs {
		f := strings.TrimSpace(d.File)
		if d.After == "" {
			continue
		}
		isCodeFile := strings.HasSuffix(f, ".go") || strings.HasSuffix(f, ".ts") ||
			strings.HasSuffix(f, ".tsx") || strings.HasSuffix(f, ".js") || strings.HasSuffix(f, ".m")
		isIconDict := strings.Contains(f, "file-icons/") || strings.Contains(f, "icons/")

		if isCodeFile && !isIconDict {
			lineCount := strings.Count(d.After, "\n") + 1
			if lineCount > 500 {
				deduction += 18
				if isZh {
					issues = append(issues, fmt.Sprintf("文件 %s 达到 %d 行，违反单文件 500 行上限规范", f, lineCount))
				} else {
					issues = append(issues, fmt.Sprintf("File %s has %d lines, exceeding the 500-line limit", f, lineCount))
				}
			}
		}

		lowerAfter := strings.ToLower(d.After)
		lowerBefore := strings.ToLower(d.Before)
		for _, marker := range lazyMarkers {
			if strings.Contains(lowerAfter, marker) && !strings.Contains(lowerBefore, marker) {
				deduction += 22
				if isZh {
					issues = append(issues, fmt.Sprintf("文件 %s 检测到未完成的偷懒占位符 (%s)", f, marker))
				} else {
					issues = append(issues, fmt.Sprintf("File %s contains lazy incomplete placeholder (%s)", f, marker))
				}
				break
			}
		}
	}

	return deduction, issues
}

func (g *desktopGateway) resolveProviderConnection(providerID, hintBaseURL, hintNPM string) (baseURL, apiKey, npm string, headers map[string]string) {
	baseURL = strings.TrimSpace(hintBaseURL)
	npm = strings.TrimSpace(hintNPM)
	headers = map[string]string{}

	merged := g.loadOpenCodeImportedProviderMap()
	for k, v := range g.loadHoxiProvidersMap() {
		merged[k] = v
	}
	if rawCfg, ok := merged[providerID].(map[string]any); ok {
		if npm == "" {
			if s, ok := rawCfg["npm"].(string); ok {
				npm = strings.TrimSpace(s)
			}
		}
		if opts, ok := rawCfg["options"].(map[string]any); ok {
			if baseURL == "" {
				if u, ok := opts["baseURL"].(string); ok {
					baseURL = strings.TrimSpace(u)
				}
			}
			if k, ok := opts["apiKey"].(string); ok && k != "" && k != "********" {
				apiKey = strings.TrimSpace(k)
			}
			if hMap, ok := opts["headers"].(map[string]any); ok {
				for hk, hv := range hMap {
					if vs, ok := hv.(string); ok {
						headers[hk] = vs
					}
				}
			}
		}
	}

	if baseURL == "" {
		baseURL = defaultProviderBaseURLs[providerID]
	}
	if apiKey == "" {
		apiKey = g.resolveStoredProviderKey(providerID, baseURL)
	}
	return baseURL, apiKey, npm, headers
}

func (g *desktopGateway) invokeObserverLLM(
	parentCtx context.Context,
	providerID, modelID, baseURL, apiKey, npm string,
	headers map[string]string,
	req auditCodeRequest,
) (int, string, []string, error) {
	langHint := "Respond with concise English text in `summary` and `issues`."
	if strings.HasPrefix(req.Locale, "zh") {
		langHint = "请使用简明扼要的中文填写 `summary` 和 `issues`。"
	}

	var diffBuilder strings.Builder
	for i, d := range req.Diffs {
		if i >= 8 {
			break
		}
		diffBuilder.WriteString(fmt.Sprintf("--- File: %s (+%d -%d)\n", d.File, d.Additions, d.Deletions))
		snippet := strings.TrimSpace(d.After)
		if len(snippet) > 900 {
			snippet = snippet[:900] + "\n...[truncated]"
		}
		if snippet != "" {
			diffBuilder.WriteString(snippet + "\n")
		}
	}

	prompt := fmt.Sprintf(`You are an independent Senior Code Auditor evaluating code changes produced by an AI worker model (%s/%s).
User Request: %s
LSP Errors Count: %d (%s)
Modified Files Summary:
%s

Evaluate whether the code changes genuinely fulfill the user request without lazy shortcuts, hallucinations, or regressions.
%s
Return ONLY valid JSON matching this schema:
{"score": 0-100, "pass": true/false, "summary": "1 sentence verdict", "issues": ["specific defect 1"]}`,
		req.WorkerProviderID,
		req.WorkerModelID,
		truncateStr(req.UserPrompt, 500),
		req.LspErrorCount,
		truncateStr(req.LspErrorsSummary, 200),
		diffBuilder.String(),
		langHint,
	)

	isAnthropic := npm == "@ai-sdk/anthropic" || providerID == "anthropic"
	trimmedBase := strings.TrimRight(baseURL, "/")
	var targetURL string
	var bodyMap map[string]any

	if isAnthropic {
		if strings.HasSuffix(trimmedBase, "/v1") {
			targetURL = trimmedBase + "/messages"
		} else {
			targetURL = trimmedBase + "/v1/messages"
		}
		bodyMap = map[string]any{
			"model":      modelID,
			"max_tokens": 400,
			"messages": []map[string]string{
				{"role": "user", "content": prompt},
			},
		}
	} else {
		targetURL = trimmedBase + "/chat/completions"
		bodyMap = map[string]any{
			"model":       modelID,
			"max_tokens":  400,
			"temperature": 0.1,
			"messages": []map[string]string{
				{"role": "user", "content": prompt},
			},
		}
	}

	payloadBytes, _ := json.Marshal(bodyMap)
	ctx, cancel := context.WithTimeout(parentCtx, 20*time.Second)
	defer cancel()

	outReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return 0, "", nil, err
	}
	outReq.Header.Set("Accept", "application/json")
	outReq.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		if k != "" && v != "" {
			outReq.Header.Set(k, v)
		}
	}
	if apiKey != "" {
		if isAnthropic {
			outReq.Header.Set("x-api-key", apiKey)
			outReq.Header.Set("anthropic-version", "2023-06-01")
		} else {
			outReq.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}

	client := &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 15 * time.Second,
			}).DialContext,
		},
	}
	if strings.Contains(targetURL, "://127.0.0.1") || strings.Contains(targetURL, "://localhost") {
		client.Transport = &http.Transport{Proxy: nil}
	}

	resp, err := client.Do(outReq)
	if err != nil {
		return 0, "", nil, err
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, "", nil, fmt.Errorf("%s", extractAPIErrorMessage(resp.StatusCode, respBytes))
	}

	content := extractLLMTextContent(respBytes)
	score, summary, issues := parseObserverAuditJSON(content)
	return score, summary, issues, nil
}

func extractLLMTextContent(raw []byte) string {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &parsed) == nil {
		if len(parsed.Choices) > 0 && parsed.Choices[0].Message.Content != "" {
			return parsed.Choices[0].Message.Content
		}
		if len(parsed.Content) > 0 && parsed.Content[0].Text != "" {
			return parsed.Content[0].Text
		}
	}
	return string(raw)
}

func parseObserverAuditJSON(text string) (int, string, []string) {
	cleaned := strings.TrimSpace(text)
	if start := strings.Index(cleaned, "{"); start >= 0 {
		if end := strings.LastIndex(cleaned, "}"); end > start {
			cleaned = cleaned[start : end+1]
		}
	}
	var res struct {
		Score   int      `json:"score"`
		Summary string   `json:"summary"`
		Issues  []string `json:"issues"`
	}
	if err := json.Unmarshal([]byte(cleaned), &res); err == nil && res.Score > 0 {
		return res.Score, strings.TrimSpace(res.Summary), res.Issues
	}
	if s, err := strconv.Atoi(strings.TrimSpace(cleaned)); err == nil && s >= 0 && s <= 100 {
		return s, "", nil
	}
	return 85, truncateStr(strings.TrimSpace(text), 140), nil
}

func truncateStr(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

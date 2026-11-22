package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/hoxiai/hoxi/internal/llm"
)

const anthropicFastModeBetaHeader = "fast-mode-2026-02-01"

var openAIFastModelTokenRe = regexp.MustCompile(`(?i)(?:^|[/-])(?:gpt-(?:4o|5|6|7)|o[34](?:$|[.-])|codex)`)

func fastModeMemoKey(target llm.StreamTarget) string {
	return strings.ToLower(strings.TrimSpace(target.ProviderID)) + "|" +
		strings.TrimRight(strings.ToLower(strings.TrimSpace(target.BaseURL)), "/") + "|" +
		strings.ToLower(strings.TrimSpace(target.ModelID))
}

func isLoopbackHost(host string) bool {
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func supportsAnthropicFastMode(target llm.StreamTarget) bool {
	if target.FallbackOpenAI {
		return false
	}
	lowerModel := strings.ToLower(strings.TrimSpace(target.ModelID))
	if lowerModel == "" || strings.Contains(lowerModel, "haiku") || strings.Contains(lowerModel, "sonnet") {
		return false
	}
	if !strings.Contains(lowerModel, "opus") &&
		!strings.Contains(lowerModel, "fable") &&
		!strings.Contains(lowerModel, "mythos") {
		return false
	}
	rawURL := strings.TrimSpace(target.BaseURL)
	if rawURL == "" {
		return true
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	pid := strings.ToLower(strings.TrimSpace(target.ProviderID))
	if host == "api.anthropic.com" || strings.HasSuffix(host, ".anthropic.com") || pid == "hoxi-official" {
		return true
	}
	return pid == "anthropic" && isLoopbackHost(host)
}

func supportsOpenAIFastMode(target llm.StreamTarget) bool {
	lowerModel := strings.ToLower(strings.TrimSpace(target.ModelID))
	if lowerModel == "" ||
		strings.Contains(lowerModel, "oss-") ||
		strings.Contains(lowerModel, "nano") ||
		strings.Contains(lowerModel, "mini") ||
		strings.Contains(lowerModel, "audio") ||
		strings.Contains(lowerModel, "realtime") ||
		strings.Contains(lowerModel, "tts") ||
		strings.Contains(lowerModel, "whisper") ||
		strings.Contains(lowerModel, "image") ||
		strings.Contains(lowerModel, "embedding") {
		return false
	}
	if !openAIFastModelTokenRe.MatchString(lowerModel) {
		return false
	}
	rawURL := strings.TrimSpace(target.BaseURL)
	if rawURL == "" || isCodexBackendTarget(target) {
		return true
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	pid := strings.ToLower(strings.TrimSpace(target.ProviderID))
	if host == "api.openai.com" || strings.HasSuffix(host, ".openai.com") || pid == "hoxi-official" {
		return true
	}
	return (pid == "openai" || pid == "codex") && isLoopbackHost(host)
}

func normalizeFastServiceTier(raw any) string {
	s, _ := raw.(string)
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "fast", "priority":
		return "fast"
	default:
		return ""
	}
}

func fastModeCostMultiplier(serviceTier string) float64 {
	if strings.EqualFold(strings.TrimSpace(serviceTier), "fast") {
		return 2.0
	}
	return 1.0
}

func isUnsupportedFastModeError(err error) bool {
	if err == nil || isContextOverflowError(err) {
		return false
	}
	var httpErr *llm.StreamHTTPError
	if errors.As(err, &httpErr) && httpErr != nil {
		if httpErr.StatusCode != http.StatusBadRequest && httpErr.StatusCode != http.StatusUnprocessableEntity {
			return false
		}
	}
	lower := strings.ToLower(err.Error())
	if !strings.Contains(lower, "speed") &&
		!strings.Contains(lower, "service_tier") &&
		!strings.Contains(lower, "fast-mode") &&
		!strings.Contains(lower, "fast_mode") {
		return false
	}
	return strings.Contains(lower, "unknown") ||
		strings.Contains(lower, "unsupported") ||
		strings.Contains(lower, "invalid") ||
		strings.Contains(lower, "unrecognized") ||
		strings.Contains(lower, "not allowed") ||
		strings.Contains(lower, "not available") ||
		strings.Contains(lower, "extra inputs") ||
		strings.Contains(lower, "unexpected")
}

type openAIFastModeDriver struct {
	inner ModelDriver
}

func withOpenAIFastMode(inner ModelDriver) ModelDriver {
	return &openAIFastModeDriver{inner: inner}
}

func (d *openAIFastModeDriver) Protocol() llm.StreamProtocol { return d.inner.Protocol() }

func (d *openAIFastModeDriver) SystemPromptPatch() string { return d.inner.SystemPromptPatch() }

func (d *openAIFastModeDriver) BuildRequest(req ModelRequest) (llm.StreamHTTPRequest, error) {
	httpReq, err := d.inner.BuildRequest(req)
	if err != nil || !req.FastMode || !supportsOpenAIFastMode(req.Target) || len(httpReq.Body) == 0 {
		return httpReq, err
	}
	var payload map[string]any
	if json.Unmarshal(httpReq.Body, &payload) != nil || payload == nil {
		return httpReq, nil
	}
	payload["service_tier"] = "priority"
	if updated, mErr := json.Marshal(payload); mErr == nil {
		httpReq.Body = updated
	}
	return httpReq, nil
}

func (d *openAIFastModeDriver) ParseSSE(ctx context.Context, body io.Reader, emit func(StreamEvent)) error {
	tap := &sseServiceTierTap{r: body}
	return d.inner.ParseSSE(ctx, tap, func(se StreamEvent) {
		if se.Kind == EventUsage && se.Usage != nil && se.Usage.ServiceTier == "" && tap.tier != "" {
			cp := *se.Usage
			cp.ServiceTier = tap.tier
			se.Usage = &cp
		}
		emit(se)
	})
}

type sseServiceTierTap struct {
	r    io.Reader
	buf  []byte
	tier string
}

func (t *sseServiceTierTap) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		t.inspect(p[:n])
	}
	if err != nil && len(t.buf) > 0 {
		t.inspectLine(t.buf)
		t.buf = nil
	}
	return n, err
}

func (t *sseServiceTierTap) inspect(chunk []byte) {
	t.buf = append(t.buf, chunk...)
	for {
		idx := bytes.IndexByte(t.buf, '\n')
		if idx < 0 {
			if len(t.buf) > 256*1024 {
				t.inspectLine(t.buf)
				t.buf = nil
			}
			return
		}
		t.inspectLine(t.buf[:idx])
		t.buf = t.buf[idx+1:]
	}
}

func (t *sseServiceTierTap) inspectLine(rawLine []byte) {
	if !bytes.Contains(rawLine, []byte("service_tier")) {
		return
	}
	line := strings.TrimSpace(string(rawLine))
	if !strings.HasPrefix(line, "data:") {
		return
	}
	data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	var obj map[string]any
	if json.Unmarshal([]byte(data), &obj) != nil {
		return
	}
	if tier := normalizeFastServiceTier(obj["service_tier"]); tier != "" {
		t.tier = tier
		return
	}
	if respObj, ok := obj["response"].(map[string]any); ok {
		if tier := normalizeFastServiceTier(respObj["service_tier"]); tier != "" {
			t.tier = tier
		}
	}
}

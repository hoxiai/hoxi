package provider

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/diagnostics"
)

const (
	relayChatPath   = "/v1/chat/completions"
	relayModelsPath = "/v1/models"
	maxRelayBody    = 16 << 20
	// relayAutoAttempts bounds auto-pool rotation when candidates are not
	// OpenAI-compatible, so one misconfigured pool cannot spin forever.
	relayAutoAttempts = 8
)

// relayModel is one selectable entry served by GET /v1/models. IDs are
// namespaced as "<provider>/<model>" so a client can pick unambiguously;
// hoxi-auto entries expose the auto pools.
type relayModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}

// IsRelayPath reports whether a request belongs to the local OpenAI-compatible
// relay plane. It is checked before the browser origin guard because desktop
// clients (VS Code extensions, Electron apps, curl) send no usable Origin.
func IsRelayPath(path string) bool {
	return path == relayChatPath || path == relayModelsPath
}

// ServeRelay handles /v1/models and /v1/chat/completions. Authentication is a
// hk-* relay key; the gateway token is deliberately not accepted here.
func (s *Service) ServeRelay(w http.ResponseWriter, r *http.Request) {
	// Relay keys travel in the Authorization header, never in cookies, so a
	// wildcard origin cannot leak a session. Browser-based AI clients (Open
	// WebUI, chat UIs) need this preflight to work at all.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Api-Key")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Max-Age", "600")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !s.authorizeRelay(w, r) {
		return
	}
	switch r.URL.Path {
	case relayModelsPath:
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
			return
		}
		s.serveRelayModels(w)
	case relayChatPath:
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		s.serveRelayChat(w, r)
	default:
		writeJSONError(w, http.StatusNotFound, "unknown relay endpoint")
	}
}

func (s *Service) authorizeRelay(w http.ResponseWriter, r *http.Request) bool {
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		token = strings.TrimSpace(r.Header.Get("X-Api-Key"))
	}
	if token == "" || !s.verifyRelayToken(token) {
		// OpenAI-compatible clients surface the JSON error text; keep it
		// actionable and mention where keys are issued.
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]any{
				"message": "invalid or missing relay key; create one in Hoxi Settings → Providers → Local Relay",
				"type":    "invalid_request_error",
				"code":    "invalid_api_key",
			},
		})
		return false
	}
	return true
}

// bearerToken accepts any capitalization of the "Bearer" scheme, which
// OpenAI-compatible clients are inconsistent about.
func bearerToken(header string) string {
	header = strings.TrimSpace(header)
	if len(header) < 7 || !strings.EqualFold(header[:6], "bearer") || header[6] != ' ' {
		return ""
	}
	return strings.TrimSpace(header[7:])
}

func (s *Service) serveRelayModels(w http.ResponseWriter) {
	models := s.relayModelList()
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": models})
}

// relayModelList merges Hoxi-managed and imported providers with the auto
// pools. Only ids and names leave this function.
func (s *Service) relayModelList() []relayModel {
	merged := s.loadOpenCodeImportedProviderMap()
	for k, v := range s.loadHoxiProvidersMap() {
		merged[k] = v
	}
	out := []relayModel{
		{ID: AutoProviderID, Object: "model", OwnedBy: "hoxi"},
	}
	seen := map[string]bool{AutoProviderID: true}
	for _, pid := range sortedKeys(merged) {
		if pid == "opencode" || pid == "opencode-go" || pid == AutoProviderID {
			continue
		}
		pObj, _ := merged[pid].(map[string]any)
		models, _ := pObj["models"].(map[string]any)
		for _, mid := range sortedKeys(models) {
			if strings.TrimSpace(mid) == "" {
				continue
			}
			if relayProviderIsAnthropicNative(pid, pObj) {
				continue
			}
			id := pid + "/" + mid
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, relayModel{ID: id, Object: "model", OwnedBy: pid})
		}
	}
	for _, poolID := range sortedKeys(s.loadAutoPoolsMap()) {
		id := AutoProviderID + "/" + poolID
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, relayModel{ID: id, Object: "model", OwnedBy: "hoxi"})
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// relayCandidateCompatible is the cheap auto-pool filter: config files only,
// no engine round-trip per candidate, so a large pool cannot stall a request.
func (s *Service) relayCandidateCompatible(providerID string) bool {
	pObj, ok := s.relayProviderConfig(providerID)
	if !ok || relayProviderIsAnthropicNative(providerID, pObj) {
		return false
	}
	npm, _ := pObj["npm"].(string)
	if strings.Contains(strings.TrimSpace(npm), "anthropic") {
		return false
	}
	baseURL := strings.TrimSpace(defaultProviderBaseURLs[providerID])
	if opts, ok := pObj["options"].(map[string]any); ok {
		if u, ok := opts["baseURL"].(string); ok && strings.TrimSpace(u) != "" {
			baseURL = strings.TrimSpace(u)
		}
	}
	return isHTTPEndpoint(baseURL)
}

func relayProviderIsAnthropicNative(pid string, pObj map[string]any) bool {
	if pid == "anthropic" {
		return true
	}
	npm, _ := pObj["npm"].(string)
	return strings.Contains(strings.TrimSpace(npm), "anthropic")
}

// relayResolveModel maps a client-supplied model id onto a concrete provider
// and model. "hoxi-auto" and "hoxi-auto/<pool>" go through the auto router;
// "provider/model" is explicit; a bare model id is accepted only when exactly
// one configured provider exposes it, so a typo never silently calls the wrong
// vendor.
func (s *Service) relayResolveModel(raw string) (providerID, modelID string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", errors.New("model is required")
	}
	if raw == AutoProviderID || strings.HasPrefix(raw, AutoProviderID+"/") {
		poolID := strings.TrimPrefix(raw, AutoProviderID+"/")
		if poolID == raw {
			poolID = ""
		}
		// The auto pool may contain Anthropic-native or CLI-only entries that
		// this OpenAI-compatible plane cannot serve. Rotate past them instead of
		// failing the whole request on the first unlucky pick.
		exclude := map[string]bool{}
		for attempt := 0; attempt < relayAutoAttempts; attempt++ {
			pid, mid := s.selectAutoCandidate(poolID, exclude)
			if pid == AutoProviderID || strings.TrimSpace(mid) == "" {
				break
			}
			if s.relayCandidateCompatible(pid) {
				return pid, mid, nil
			}
			exclude[breakerKey(pid, mid)] = true
		}
		return "", "", errors.New("no OpenAI-compatible model is available in the auto pool " + strings.TrimSpace(poolID))
	}

	merged := s.loadOpenCodeImportedProviderMap()
	for k, v := range s.loadHoxiProvidersMap() {
		merged[k] = v
	}
	var unknownProvider string
	if pid, mid, ok := strings.Cut(raw, "/"); ok {
		pid, mid = strings.TrimSpace(pid), strings.TrimSpace(mid)
		if pid == "" || mid == "" {
			return "", "", errors.New("model must be \"provider/model\" or a configured model id")
		}
		if pid == "opencode" || pid == "opencode-go" {
			return "", "", errors.New("provider " + pid + " is not supported on the local relay")
		}
		pObj, _ := merged[pid].(map[string]any)
		if pObj != nil {
			if !relayProviderHasModel(pObj, mid) {
				return "", "", errors.New("provider " + pid + " does not expose model " + mid)
			}
			return pid, mid, nil
		}
		unknownProvider = pid
	}

	type modelMatch struct {
		pid string
		mid string
	}
	var matches []modelMatch
	for _, pid := range sortedKeys(merged) {
		if pid == AutoProviderID || pid == "opencode" || pid == "opencode-go" {
			continue
		}
		pObj, _ := merged[pid].(map[string]any)
		if relayProviderHasModel(pObj, raw) && !relayProviderIsAnthropicNative(pid, pObj) {
			matches = append(matches, modelMatch{pid: pid, mid: raw})
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].pid, matches[0].mid, nil
	case 0:
		if unknownProvider != "" {
			return "", "", errors.New("unknown provider " + unknownProvider)
		}
		return "", "", errors.New("unknown model " + raw + "; call GET /v1/models for the available ids")
	default:
		qualified := make([]string, 0, len(matches))
		for _, m := range matches {
			qualified = append(qualified, m.pid+"/"+m.mid)
		}
		return "", "", errors.New("model " + raw + " is ambiguous; use one of: " + strings.Join(qualified, ", "))
	}
}

func relayProviderHasModel(pObj map[string]any, modelID string) bool {
	models, _ := pObj["models"].(map[string]any)
	if len(models) == 0 {
		return false
	}
	_, ok := models[modelID]
	return ok
}

func (s *Service) serveRelayChat(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxRelayBody+1))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "could not read request body")
		return
	}
	if len(raw) > maxRelayBody {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "request body is too large")
		return
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil || payload == nil {
		writeJSONError(w, http.StatusBadRequest, "request body must be a JSON object")
		return
	}
	requested, _ := payload["model"].(string)
	providerID, modelID, err := s.relayResolveModel(requested)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"message": err.Error(), "type": "invalid_request_error"},
		})
		return
	}
	pObj, _ := s.relayProviderConfig(providerID)
	if relayProviderIsAnthropicNative(providerID, pObj) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{
				"message": "provider " + providerID + " speaks the Anthropic protocol; the local relay only forwards OpenAI-compatible providers",
				"type":    "invalid_request_error",
			},
		})
		return
	}

	baseURL, apiKey, npm, headers := s.resolveProviderConnection(providerID, "", "")
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if !isHTTPEndpoint(baseURL) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"message": "provider " + providerID + " has no HTTP endpoint configured", "type": "invalid_request_error"},
		})
		return
	}
	if strings.Contains(strings.TrimSpace(npm), "anthropic") {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"message": "provider " + providerID + " is configured with the Anthropic SDK; use an OpenAI-compatible endpoint", "type": "invalid_request_error"},
		})
		return
	}

	// Rewrite only the model field: tools, tool_choice, images and every other
	// OpenAI extension pass through untouched, which is what local coding
	// clients depend on.
	payload["model"] = modelID
	body, err := json.Marshal(payload)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "could not encode request body")
		return
	}

	ctx := r.Context()

	outReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", strings.NewReader(string(body)))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	outReq.Header.Set("Content-Type", "application/json")
	outReq.Header.Set("Accept", "application/json")
	if stream, _ := payload["stream"].(bool); stream {
		outReq.Header.Set("Accept", "text/event-stream")
	}
	for k, v := range headers {
		if k != "" && v != "" {
			outReq.Header.Set(k, v)
		}
	}
	if apiKey != "" {
		outReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	transport := providerTransport
	if isLoopbackURL(baseURL) {
		transport = localTransport
	}
	client := &http.Client{Transport: transport}
	started := time.Now()
	elapsedMs := func() int64 { return time.Since(started).Milliseconds() }
	resp, err := client.Do(outReq)
	if err != nil {
		s.recordBreakerFailure(providerID, modelID, classifyCandidateFailure(0, err, nil), 0, elapsedMs(), err.Error())
		recordRelayFailure(providerID, modelID, 0, started)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]any{"message": "upstream request failed: " + err.Error(), "type": "upstream_error"},
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rawErr, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		s.recordBreakerFailure(providerID, modelID, classifyCandidateFailure(resp.StatusCode, nil, rawErr), resp.StatusCode, elapsedMs(), string(rawErr))
		recordRelayFailure(providerID, modelID, resp.StatusCode, started)
		writeRelayUpstreamError(w, resp.StatusCode, rawErr)
		return
	}

	copyRelayHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if err := streamRelayBody(w, resp.Body); err != nil {
		s.recordBreakerFailure(providerID, modelID, classifyCandidateFailure(resp.StatusCode, err, nil), resp.StatusCode, elapsedMs(), err.Error())
		recordRelayFailure(providerID, modelID, resp.StatusCode, started)
		return
	}
	s.recordBreakerSuccess(providerID, modelID, elapsedMs())
}

func (s *Service) relayProviderConfig(providerID string) (map[string]any, bool) {
	merged := s.loadOpenCodeImportedProviderMap()
	for k, v := range s.loadHoxiProvidersMap() {
		merged[k] = v
	}
	pObj, ok := merged[providerID].(map[string]any)
	return pObj, ok
}

func writeRelayUpstreamError(w http.ResponseWriter, status int, raw []byte) {
	trimmed := strings.TrimSpace(string(raw))
	var parsed map[string]any
	if json.Unmarshal(raw, &parsed) == nil && parsed != nil {
		if _, ok := parsed["error"]; ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(parsed)
			return
		}
	}
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"message": "upstream returned HTTP " + http.StatusText(status) + ": " + trimmed,
			"type":    "upstream_error",
		},
	})
}

var relayHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
	"Set-Cookie":          true,
	"Content-Length":      true,
}

func copyRelayHeaders(dst, src http.Header) {
	for k, values := range src {
		if relayHopHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		for _, v := range values {
			dst.Add(k, v)
		}
	}
}

// streamRelayBody forwards bytes as they arrive and flushes after every chunk
// so SSE deltas reach the client immediately.
func streamRelayBody(w http.ResponseWriter, body io.Reader) error {
	controller := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				return err
			}
			_ = controller.Flush()
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

func recordRelayFailure(providerID, modelID string, status int, started time.Time) {
	diagnostics.Record(diagnostics.Entry{
		Kind:       "request",
		Level:      "error",
		Source:     "relay",
		Message:    "local relay request failed",
		ProviderID: providerID,
		ModelID:    modelID,
		Status:     status,
		DurationMs: time.Since(started).Milliseconds(),
	})
}

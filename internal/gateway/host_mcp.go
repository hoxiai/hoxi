package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/hoxiai/hoxi/internal/appbridge"
	"github.com/hoxiai/hoxi/internal/buildinfo"
)

const hostMCPVersion = "2025-03-26"
const hostMCPDiscoveryURI = "hoxi://protocol/discovery"

type hostMCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type hostMCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func mcpResponse(id json.RawMessage, result any, rpcErr *hostMCPError) map[string]any {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	out := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcErr != nil {
		out["error"] = rpcErr
	} else {
		out["result"] = result
	}
	return out
}

func writeMCPResponse(w http.ResponseWriter, acceptJSON bool, out map[string]any) error {
	if acceptJSON {
		writeJSON(w, http.StatusOK, out)
		return nil
	}
	payload, err := json.Marshal(out)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, err = w.Write(append(append([]byte("event: message\ndata: "), payload...), '\n', '\n'))
	return err
}

func mcpAccepts(accept, wanted string) bool {
	if accept == "" {
		return true
	}
	for _, field := range strings.Split(accept, ",") {
		kind, params, err := mime.ParseMediaType(strings.TrimSpace(field))
		q, qErr := strconv.ParseFloat(params["q"], 64)
		if err == nil && (params["q"] == "" || (qErr == nil && q > 0 && q <= 1)) && (kind == wanted || kind == "*/*" || kind == strings.Split(wanted, "/")[0]+"/*") {
			return true
		}
	}
	return false
}

func (g *Gateway) serveHostMCP(w http.ResponseWriter, r *http.Request, grant appbridge.GrantInfo) {
	accept := strings.Join(r.Header.Values("Accept"), ",")
	w.Header().Add("Vary", "Accept")
	acceptJSON := mcpAccepts(accept, "application/json")
	if !acceptJSON && !mcpAccepts(accept, "text/event-stream") {
		writeAppError(w, http.StatusNotAcceptable, "INVALID_REQUEST", "Accept must allow JSON or SSE")
		return
	}
	if v := r.Header.Get("MCP-Protocol-Version"); v != "" && v != hostMCPVersion {
		writeAppError(w, http.StatusBadRequest, "INVALID_REQUEST", "unsupported MCP protocol version")
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeAppError(w, http.StatusUnsupportedMediaType, "INVALID_REQUEST", "Content-Type must be application/json")
		return
	}
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2*maxLocalMediaUploadBytes))
	if err != nil {
		writeAppError(w, http.StatusRequestEntityTooLarge, "INVALID_REQUEST", "could not read MCP payload within size limit")
		return
	}
	payload = bytes.TrimSpace(payload)
	if !json.Valid(payload) {
		_ = writeMCPResponse(w, acceptJSON, mcpResponse(nil, nil, &hostMCPError{-32700, "Parse error"}))
		return
	}
	if payload[0] == '[' {
		g.serveMCPBatch(w, r, grant, payload)
		return
	}
	out := g.handleMCPMessage(r, grant, payload)
	if out == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	_ = writeMCPResponse(w, acceptJSON, out)
}

func (g *Gateway) serveMCPBatch(w http.ResponseWriter, r *http.Request, grant appbridge.GrantInfo, payload []byte) {
	// The 2025-03-26 transport supports batches; send one SSE message for
	// each response and do not buffer all tool results in memory at once.
	var batch []json.RawMessage
	if json.Unmarshal(payload, &batch) != nil || len(batch) == 0 || len(batch) > 16 {
		writeAppError(w, http.StatusBadRequest, "INVALID_REQUEST", "MCP batch must contain 1 to 16 messages")
		return
	}
	if !mcpAccepts(strings.Join(r.Header.Values("Accept"), ","), "text/event-stream") {
		writeAppError(w, http.StatusNotAcceptable, "INVALID_REQUEST", "MCP batches require Accept: text/event-stream")
		return
	}
	wrote := false
	for _, message := range batch {
		if r.Context().Err() != nil {
			return
		}
		if out := g.handleMCPMessage(r, grant, message); out != nil {
			if err := writeMCPResponse(w, false, out); err != nil {
				return
			}
			if err := http.NewResponseController(w).Flush(); err != nil {
				return
			}
			wrote = true
		}
	}
	if !wrote {
		w.WriteHeader(http.StatusAccepted)
	}
}

func (g *Gateway) handleMCPMessage(r *http.Request, grant appbridge.GrantInfo, payload []byte) map[string]any {
	var req hostMCPRequest
	if json.Unmarshal(payload, &req) != nil || req.JSONRPC != "2.0" || req.Method == "" || !validMCPID(req.ID) {
		return mcpResponse(nil, nil, &hostMCPError{-32600, "Invalid Request"})
	}
	if len(req.ID) == 0 {
		return nil // notifications never execute tools or mutate state
	}
	result, rpcErr := g.dispatchMCP(r, grant, req)
	return mcpResponse(req.ID, result, rpcErr)
}

func validMCPID(id json.RawMessage) bool {
	if len(id) == 0 {
		return true
	}
	return id[0] == '"' || id[0] == '-' || (id[0] >= '0' && id[0] <= '9')
}

func (g *Gateway) dispatchMCP(r *http.Request, grant appbridge.GrantInfo, req hostMCPRequest) (any, *hostMCPError) {
	switch req.Method {
	case "initialize":
		return map[string]any{"protocolVersion": hostMCPVersion, "serverInfo": map[string]string{"name": "hoxi-host", "version": buildinfo.Version},
			"capabilities": map[string]any{"tools": map[string]any{}, "resources": map[string]any{}}}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		tools := make([]map[string]any, 0)
		for _, cap := range appCapabilities {
			if cap.tool != "" && appbridge.HasScope(grant.Scopes, cap.scope) {
				tools = append(tools, map[string]any{"name": cap.tool, "description": cap.description, "inputSchema": json.RawMessage(cap.schema)})
			}
		}
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(req.Params, &params) != nil || params.Name == "" {
			return nil, &hostMCPError{-32602, "Invalid tool parameters"}
		}
		return g.callMCPTool(r, grant, params.Name, params.Arguments)
	case "resources/list":
		return map[string]any{"resources": []map[string]string{{"uri": hostMCPDiscoveryURI, "name": "hoxi-discovery", "mimeType": "application/json"}}}, nil
	case "resources/read":
		var params struct {
			URI string `json:"uri"`
		}
		if json.Unmarshal(req.Params, &params) != nil || params.URI != hostMCPDiscoveryURI {
			return nil, &hostMCPError{-32002, "Resource not found"}
		}
		data, _ := json.Marshal(g.appDiscovery())
		return map[string]any{"contents": []map[string]string{{"uri": params.URI, "mimeType": "application/json", "text": string(data)}}}, nil
	default:
		return nil, &hostMCPError{-32601, "Method not found"}
	}
}

func mcpToolResult(text string, failed bool) map[string]any {
	return map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "isError": failed}
}

func (g *Gateway) callMCPTool(r *http.Request, grant appbridge.GrantInfo, name string, args json.RawMessage) (any, *hostMCPError) {
	// A batch may outlive its original authorization. Recheck before each
	// side effect, so revoking or narrowing a grant affects later calls.
	_, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	verified, err := g.appBridge.VerifyToken(token, grant.AppID)
	if err != nil || verified.ID != grant.ID {
		return mcpToolResult("INVALID_TOKEN: application grant is no longer active", true), nil
	}
	grant = verified
	for _, cap := range appCapabilities {
		if cap.tool == "" || name != cap.tool {
			continue
		}
		if !appbridge.HasScope(grant.Scopes, cap.scope) {
			return mcpToolResult("SCOPE_MISSING: "+cap.scope, true), nil
		}
		var fields map[string]json.RawMessage
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		if json.Unmarshal(args, &fields) != nil || fields == nil || (cap.method == http.MethodGet && len(fields) != 0) {
			return nil, &hostMCPError{-32602, "Invalid tool arguments"}
		}
		if name == "llm_chat" {
			fields["stream"] = json.RawMessage("false") // MCP results are finite JSON-RPC messages
			args, _ = json.Marshal(fields)
		}
		call := r.Clone(r.Context())
		call.Method, call.URL = cap.method, &url.URL{Path: appCapabilityPath + cap.path}
		call.Body = http.NoBody
		if cap.method == http.MethodPost {
			call.Body = io.NopCloser(bytes.NewReader(args))
		}
		call.Header.Set("Content-Type", "application/json")
		out := &mcpCapabilityResponse{header: make(http.Header)}
		g.serveAppCapability(out, call, cap, grant)
		if out.overflow {
			return mcpToolResult("RESPONSE_TOO_LARGE: use the capability HTTP endpoint", true), nil
		}
		var meta struct {
			OK *bool `json:"ok"`
		}
		failed := out.status >= 400 || !json.Valid(out.body.Bytes())
		_ = json.Unmarshal(out.body.Bytes(), &meta)
		if meta.OK != nil && !*meta.OK {
			failed = true
		}
		return mcpToolResult(out.body.String(), failed), nil
	}
	return nil, &hostMCPError{-32602, "Unknown tool"}
}

type mcpCapabilityResponse struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	overflow bool
}

func (w *mcpCapabilityResponse) Header() http.Header { return w.header }
func (w *mcpCapabilityResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *mcpCapabilityResponse) Write(p []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	if w.overflow || len(p) > (8<<20)-w.body.Len() {
		w.overflow = true
		return len(p), nil
	}
	return w.body.Write(p)
}

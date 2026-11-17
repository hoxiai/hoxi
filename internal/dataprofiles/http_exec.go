package dataprofiles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (s *Service) invokeHTTPRequest(ctx context.Context, prof Profile, req InvokeRequest) (map[string]any, error) {
	if prof.Kind != KindHTTPAPI {
		return nil, fmt.Errorf("profile %q is kind %q, not http_api", prof.ID, prof.Kind)
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	switch method {
	case http.MethodGet, http.MethodHead:
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		if prof.ReadOnly {
			return nil, fmt.Errorf("profile %q is configured as read-only (only GET/HEAD permitted)", prof.ID)
		}
	default:
		return nil, fmt.Errorf("unsupported HTTP method %q", method)
	}

	ep := strings.TrimSpace(req.Endpoint)
	if ep == "" {
		ep = "/"
	}
	if !strings.HasPrefix(ep, "/") || strings.HasPrefix(ep, "//") || strings.Contains(ep, "://") {
		return nil, errors.New("endpoint must be a relative path starting with '/'")
	}

	targetURL := prof.BaseURL + ep
	var bodyReader io.Reader
	if req.Body != nil && method != http.MethodGet && method != http.MethodHead {
		encoded, err := json.Marshal(req.Body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(encoded)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "application/json, text/plain;q=0.9")
	if bodyReader != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	for k, v := range prof.Headers {
		if strings.TrimSpace(k) != "" {
			httpReq.Header.Set(k, v)
		}
	}
	if tok := strings.TrimSpace(prof.BearerToken); tok != "" {
		httpReq.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http_request on %s failed: %s", prof.ID, redactSecret(err.Error(), prof.BearerToken))
	}
	defer resp.Body.Close()

	rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxHTTPResponseLen+1))
	truncated := false
	if len(rawBody) > maxHTTPResponseLen {
		rawBody = rawBody[:maxHTTPResponseLen]
		truncated = true
	}
	bodyStr := redactSecret(string(rawBody), prof.BearerToken)
	out := map[string]any{
		"profile":    prof.ID,
		"statusCode": resp.StatusCode,
		"truncated":  truncated,
	}
	var parsed any
	if json.Unmarshal([]byte(bodyStr), &parsed) == nil {
		out["data"] = parsed
	} else {
		out["body"] = bodyStr
	}
	if resp.StatusCode >= 400 {
		return out, fmt.Errorf("profile %s returned HTTP %d", prof.ID, resp.StatusCode)
	}
	return out, nil
}

func redactSecret(text, secret string) string {
	trimmed := strings.TrimSpace(secret)
	if trimmed == "" || len(trimmed) < 4 {
		return text
	}
	return strings.ReplaceAll(text, trimmed, "[REDACTED]")
}

// Package ocapi is the one HTTP client Hoxi uses to talk to its supervised,
// pristine `opencode serve` daemon on 127.0.0.1.
//
// It owns the long-lived loopback transport (Proxy: nil, so system proxies
// such as Clash/Surge never turn loopback calls into 502s), injects HTTP Basic
// Auth (opencode:<password>) and bounds every buffered read with
// io.LimitReader. It is a foundation package (standard library only) so any
// domain package may depend on it without importing internal/opencode.
//
// Calls are deadline-driven: pass a context with a timeout; the client itself
// sets none so SSE and long prompts can stream.
package ocapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Username is the fixed Basic Auth user of `opencode serve`.
const Username = "opencode"

// MaxResponseBytes bounds every buffered engine response.
const MaxResponseBytes = 32 << 20

const maxErrorBodyBytes = 4 << 10

// ErrUnavailable means no engine endpoint is configured.
var ErrUnavailable = errors.New("OpenCode connection is unavailable")

var sharedTransport = &http.Transport{
	Proxy: nil,
	DialContext: (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	IdleConnTimeout:     90 * time.Second,
	MaxIdleConns:        100,
	MaxIdleConnsPerHost: 32,
}

// Transport returns the shared loopback transport, for the gateway's reverse
// proxy to reuse the same connection pool.
func Transport() *http.Transport { return sharedTransport }

// HTTPError is a non-2xx engine response.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("OpenCode HTTP %d: %s", e.Status, e.Body)
}

// Client addresses one engine instance. A nil or zero Client is valid and
// reports ErrUnavailable from every call.
type Client struct {
	baseURL  string
	password string
	http     *http.Client
}

// New returns a client for baseURL (e.g. http://127.0.0.1:4096).
func New(baseURL, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		password: password,
		http: &http.Client{
			Transport: sharedTransport,
			// The engine never redirects; refusing keeps credentials on loopback.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// NewForPort returns a client for the engine on 127.0.0.1:port; port <= 0
// yields an unavailable client.
func NewForPort(port int, password string) *Client {
	if port <= 0 {
		return New("", password)
	}
	return New("http://127.0.0.1:"+strconv.Itoa(port), password)
}

// Available reports whether an engine endpoint is configured.
func (c *Client) Available() bool { return c != nil && c.baseURL != "" }

// BaseURL returns the engine origin without a trailing slash.
func (c *Client) BaseURL() string {
	if c == nil {
		return ""
	}
	return c.baseURL
}

// URL joins path (which may carry its own query) and query onto the origin.
func (c *Client) URL(path string, query url.Values) string {
	target := c.BaseURL() + path
	if len(query) > 0 {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		target += sep + query.Encode()
	}
	return target
}

// Authorize adds the engine's Basic Auth credentials to req.
func (c *Client) Authorize(req *http.Request) {
	if c != nil && c.password != "" {
		req.SetBasicAuth(Username, c.password)
	}
}

// NewRequest builds an authorized request; a non-nil body is sent as JSON.
func (c *Client) NewRequest(ctx context.Context, method, path string, query url.Values, body io.Reader) (*http.Request, error) {
	if !c.Available() {
		return nil, ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL(path, query), body)
	if err != nil {
		return nil, err
	}
	c.Authorize(req)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// Do sends req over the shared transport. The caller owns resp.Body, which
// may be a stream; wrap reads with io.LimitReader unless it is SSE.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if !c.Available() {
		return nil, ErrUnavailable
	}
	return c.http.Do(req)
}

// Stream sends a request and returns the open response when it is 2xx, or
// an *HTTPError otherwise.
func (c *Client) Stream(ctx context.Context, method, path string, query url.Values, in any) (*http.Response, error) {
	body, err := encodeBody(in)
	if err != nil {
		return nil, err
	}
	req, err := c.NewRequest(ctx, method, path, query, body)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return nil, &HTTPError{Status: resp.StatusCode, Body: strings.TrimSpace(string(raw))}
	}
	return resp, nil
}

// Raw performs one request and returns the status and bounded body, whatever
// the status code.
func (c *Client) Raw(ctx context.Context, method, path string, query url.Values, body []byte) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := c.NewRequest(ctx, method, path, query, reader)
	if err != nil {
		return 0, nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes))
	return resp.StatusCode, raw, err
}

// JSON sends in (nil for no body) and decodes a 2xx answer into out (nil to
// discard it). Non-2xx answers return an *HTTPError.
func (c *Client) JSON(ctx context.Context, method, path string, query url.Values, in, out any) error {
	resp, err := c.Stream(ctx, method, path, query, in)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, MaxResponseBytes)
	if out == nil {
		_, err = io.Copy(io.Discard, limited)
		return err
	}
	return json.NewDecoder(limited).Decode(out)
}

func encodeBody(in any) (io.Reader, error) {
	switch v := in.(type) {
	case nil:
		return nil, nil
	case []byte:
		return bytes.NewReader(v), nil
	case json.RawMessage:
		return bytes.NewReader(v), nil
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(raw), nil
}

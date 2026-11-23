package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	maxWebFetchBodyBytes   = 2 << 20 // 2 MiB raw download cap
	maxWebFetchOutputBytes = 64 * 1024
)

var (
	reStripBlocks = regexp.MustCompile(`(?is)<(script|style|noscript|svg|iframe)[^>]*>.*?</(script|style|noscript|svg|iframe)>`)
	reHeadingTag  = regexp.MustCompile(`(?is)<h([1-6])[^>]*>(.*?)</h[1-6]>`)
	reLinkTag     = regexp.MustCompile(`(?is)<a\s+[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	reListItemTag = regexp.MustCompile(`(?is)<li[^>]*>(.*?)</li>`)
	reBreakTag    = regexp.MustCompile(`(?is)<(br|hr)\s*/?>`)
	reBlockEndTag = regexp.MustCompile(`(?is)</(p|div|section|article|header|footer|main|tr|pre|blockquote|ul|ol|table)>`)
	reAnyHTMLTag  = regexp.MustCompile(`(?s)<[^>]+>`)
	reMultiBlank  = regexp.MustCompile(`\n{3,}`)
)

type webFetchTool struct {
	allowLoopbackForTest bool
}

func (t *webFetchTool) Name() string     { return "webfetch" }
func (t *webFetchTool) IsReadOnly() bool { return true }
func (t *webFetchTool) Description() string {
	return "Fetch a public HTTP/HTTPS URL and extract readable Markdown, plain text, or raw HTML."
}
func (t *webFetchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url":     map[string]any{"type": "string", "description": "HTTP or HTTPS URL to fetch"},
			"format":  map[string]any{"type": "string", "enum": []string{"markdown", "text", "html"}, "description": "Output format (default markdown)"},
			"timeout": map[string]any{"type": "integer", "description": "Timeout in seconds (default 20, max 60)"},
		},
		"required": []string{"url"},
	}
}

func (t *webFetchTool) Execute(ctx context.Context, _ ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		URL     string `json:"url"`
		Format  string `json:"format"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "webfetch", IsError: true}, err
	}
	rawURL := strings.TrimSpace(args.URL)
	parsed, err := validateFetchURL(rawURL, t.allowLoopbackForTest)
	if err != nil {
		return ToolOutput{Title: rawURL, IsError: true}, err
	}
	timeoutSec := args.Timeout
	if timeoutSec <= 0 {
		timeoutSec = 20
	} else if timeoutSec > 60 {
		timeoutSec = 60
	}
	format := strings.ToLower(strings.TrimSpace(args.Format))
	if format == "" {
		format = "markdown"
	}

	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	client := newSSRFSafeHTTPClient(time.Duration(timeoutSec)*time.Second, t.allowLoopbackForTest)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return ToolOutput{Title: rawURL, IsError: true}, err
	}
	req.Header.Set("User-Agent", "HoxiNativeAgent/1.0 (+https://hoxi.ai)")
	req.Header.Set("Accept", "text/markdown, text/html, application/xhtml+xml, application/json, text/plain;q=0.9, */*;q=0.5")

	resp, err := client.Do(req)
	if err != nil {
		return ToolOutput{Title: rawURL, IsError: true}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return ToolOutput{Title: rawURL, IsError: true}, fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, rawURL)
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxWebFetchBodyBytes))
	if err != nil {
		return ToolOutput{Title: rawURL, IsError: true}, err
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	out := string(bodyBytes)
	if format != "html" && (strings.Contains(contentType, "html") || strings.HasPrefix(strings.TrimSpace(out), "<!") || strings.Contains(out, "<body")) {
		out = htmlToReadable(out, format == "markdown")
	}
	out = truncateToolOutput(strings.TrimSpace(out), maxWebFetchOutputBytes)
	return ToolOutput{
		Title:    parsed.Host + parsed.Path,
		Output:   out,
		Metadata: map[string]any{"url": parsed.String(), "status": resp.StatusCode, "contentType": contentType},
	}, nil
}

func validateFetchURL(raw string, allowLoopback bool) (*url.URL, error) {
	if raw == "" {
		return nil, fmt.Errorf("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("unsupported url scheme %q: only http and https are allowed", u.Scheme)
	}
	host := strings.TrimSpace(u.Hostname())
	lowerHost := strings.TrimRight(strings.ToLower(host), ".")
	if lowerHost == "" {
		return nil, fmt.Errorf("url host is required")
	}
	if !allowLoopback && (lowerHost == "localhost" || strings.HasSuffix(lowerHost, ".localhost") || strings.HasSuffix(lowerHost, ".local") || lowerHost == "metadata.google.internal" || lowerHost == "metadata.goog") {
		return nil, fmt.Errorf("blocked internal host %q", host)
	}
	if ip := net.ParseIP(host); ip != nil && isBlockedFetchIP(ip, allowLoopback) {
		return nil, fmt.Errorf("blocked non-public IP %s", ip.String())
	}
	return u, nil
}

func isBlockedFetchIP(ip net.IP, allowLoopback bool) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() {
		return !allowLoopback
	}
	if ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		// CGNAT 100.64.0.0/10 and cloud metadata 169.254.0.0/16
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return true
		}
		if v4[0] == 169 && v4[1] == 254 {
			return true
		}
	}
	return false
}

type ssrfSafeRoundTripper struct {
	allowLoopback bool
	direct        *http.Transport
	proxied       *http.Transport
}

// lookupFetchHostIPs resolves hostnames for webfetch SSRF validation; unit tests may override it.
var lookupFetchHostIPs = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

func resolveSafeFetchIP(ctx context.Context, host string, allowLoopback bool) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedFetchIP(ip, allowLoopback) {
			return nil, fmt.Errorf("blocked connection to non-public IP %s for host %s", ip.String(), host)
		}
		return ip, nil
	}
	ips, err := lookupFetchHostIPs(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no IP addresses resolved for %s", host)
	}
	for _, ipAddr := range ips {
		if isBlockedFetchIP(ipAddr.IP, allowLoopback) {
			return nil, fmt.Errorf("blocked connection to non-public IP %s for host %s", ipAddr.IP.String(), host)
		}
	}
	return ips[0].IP, nil
}

func (rt *ssrfSafeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if _, err := validateFetchURL(req.URL.String(), rt.allowLoopback); err != nil {
		return nil, err
	}
	if !rt.allowLoopback {
		if proxyURL, _ := http.ProxyFromEnvironment(req); proxyURL != nil {
			// When routing via an HTTP proxy, DialContext dials the proxy rather
			// than the target host, so validate resolved target IPs explicitly
			// before forwarding to prevent SSRF via a proxy that reaches internal nets.
			if _, err := resolveSafeFetchIP(req.Context(), req.URL.Hostname(), false); err != nil {
				return nil, err
			}
			return rt.proxied.RoundTrip(req)
		}
	}
	return rt.direct.RoundTrip(req)
}

func newSSRFSafeHTTPClient(timeout time.Duration, allowLoopback bool) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	direct := &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			safeIP, err := resolveSafeFetchIP(ctx, host, allowLoopback)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(safeIP.String(), port))
		},
	}
	proxied := &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		DisableKeepAlives: true,
		DialContext:       dialer.DialContext,
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &ssrfSafeRoundTripper{
			allowLoopback: allowLoopback,
			direct:        direct,
			proxied:       proxied,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("stopped after 5 redirects")
			}
			nextURL, err := validateFetchURL(req.URL.String(), allowLoopback)
			if err != nil {
				return err
			}
			if len(via) > 0 && (!strings.EqualFold(via[0].URL.Scheme, nextURL.Scheme) || !strings.EqualFold(via[0].URL.Host, nextURL.Host)) {
				req.Header.Del("Authorization")
				req.Header.Del("Proxy-Authorization")
				req.Header.Del("Cookie")
				req.Header.Del("X-Api-Key")
				req.Header.Del("X-Subscription-Token")
			}
			return nil
		},
	}
}

func htmlToReadable(src string, markdown bool) string {
	s := reStripBlocks.ReplaceAllString(src, "")
	if markdown {
		s = reHeadingTag.ReplaceAllStringFunc(s, func(m string) string {
			sub := reHeadingTag.FindStringSubmatch(m)
			if len(sub) < 3 {
				return m
			}
			level := int(sub[1][0] - '0')
			text := strings.TrimSpace(reAnyHTMLTag.ReplaceAllString(sub[2], ""))
			if text == "" {
				return ""
			}
			return "\n\n" + strings.Repeat("#", level) + " " + text + "\n\n"
		})
		s = reLinkTag.ReplaceAllStringFunc(s, func(m string) string {
			sub := reLinkTag.FindStringSubmatch(m)
			if len(sub) < 3 {
				return m
			}
			href := strings.TrimSpace(sub[1])
			label := strings.TrimSpace(reAnyHTMLTag.ReplaceAllString(sub[2], ""))
			if label == "" || strings.HasPrefix(strings.ToLower(href), "javascript:") {
				return label
			}
			return "[" + label + "](" + href + ")"
		})
		s = reListItemTag.ReplaceAllStringFunc(s, func(m string) string {
			sub := reListItemTag.FindStringSubmatch(m)
			if len(sub) < 2 {
				return m
			}
			return "\n- " + strings.TrimSpace(reAnyHTMLTag.ReplaceAllString(sub[1], ""))
		})
	}
	s = reBreakTag.ReplaceAllString(s, "\n")
	s = reBlockEndTag.ReplaceAllString(s, "\n\n")
	s = reAnyHTMLTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)

	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.Join(strings.Fields(l), " ")
	}
	s = strings.Join(lines, "\n")
	return strings.TrimSpace(reMultiBlank.ReplaceAllString(s, "\n\n"))
}

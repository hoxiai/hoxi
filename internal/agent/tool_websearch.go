package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	defaultWebSearchMaxResults = 5
	maxWebSearchMaxResults     = 10
	defaultWebSearchTimeoutSec = 15
	maxWebSearchTimeoutSec     = 45
	maxWebSearchSnippetRunes   = 360
)

var (
	reDDGResultLink    = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	reDDGResultSnippet = regexp.MustCompile(`(?is)<(?:a|div|span)[^>]*class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</(?:a|div|span)>`)
)

type webSearchResult struct {
	Title   string
	URL     string
	Snippet string
}

type webSearchTool struct {
	httpClientOverride *http.Client
	tavilyEndpoint     string
	exaEndpoint        string
	braveEndpoint      string
	ddgEndpoint        string
}

func (t *webSearchTool) Name() string     { return "websearch" }
func (t *webSearchTool) IsReadOnly() bool { return true }
func (t *webSearchTool) Description() string {
	return "Search the public web for up-to-date documentation, release notes, error messages, or references, returning titles, URLs, and snippets. Prefer websearch before webfetch when you do not yet know the exact URL."
}
func (t *webSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query string",
			},
			"maxResults": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     maxWebSearchMaxResults,
				"description": "Maximum number of results to return (default 5, max 10)",
			},
			"domains": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional domain filter (e.g. [\"go.dev\", \"pkg.go.dev\"])",
			},
			"provider": map[string]any{
				"type":        "string",
				"enum":        []string{"auto", "tavily", "exa", "brave", "duckduckgo"},
				"description": "Optional search backend override (default auto: uses configured Tavily/Exa/Brave API key if available, otherwise DuckDuckGo)",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in seconds (default 15, max 45)",
			},
		},
		"required": []string{"query"},
	}
}

func (t *webSearchTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		Query      string   `json:"query"`
		MaxResults int      `json:"maxResults"`
		Domains    []string `json:"domains"`
		Provider   string   `json:"provider"`
		Timeout    int      `json:"timeout"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "websearch", IsError: true}, err
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return ToolOutput{Title: "websearch", IsError: true}, fmt.Errorf("query is required")
	}
	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = defaultWebSearchMaxResults
	} else if maxResults > maxWebSearchMaxResults {
		maxResults = maxWebSearchMaxResults
	}
	timeoutSec := args.Timeout
	if timeoutSec <= 0 {
		timeoutSec = defaultWebSearchTimeoutSec
	} else if timeoutSec > maxWebSearchTimeoutSec {
		timeoutSec = maxWebSearchTimeoutSec
	}

	domains := normalizeSearchDomains(args.Domains)
	provider, apiKey := resolveWebSearchBackend(args.Provider, tc.projectConfig)

	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	client := t.httpClientOverride
	if client == nil {
		client = newSSRFSafeHTTPClient(time.Duration(timeoutSec)*time.Second, false)
	}

	var results []webSearchResult
	var err error
	switch provider {
	case "tavily":
		results, err = t.searchTavily(reqCtx, client, apiKey, query, maxResults, domains)
	case "exa":
		results, err = t.searchExa(reqCtx, client, apiKey, query, maxResults, domains)
	case "brave":
		results, err = t.searchBrave(reqCtx, client, apiKey, query, maxResults, domains)
	case "duckduckgo":
		results, err = t.searchDuckDuckGo(reqCtx, client, query, maxResults, domains)
	default:
		return ToolOutput{Title: query, IsError: true}, fmt.Errorf("unsupported websearch provider %q", provider)
	}
	if err != nil {
		return ToolOutput{Title: query, IsError: true}, err
	}
	if len(results) > maxResults {
		results = results[:maxResults]
	}

	out := formatWebSearchResults(query, provider, results)
	return ToolOutput{
		Title:  query,
		Output: out,
		Metadata: map[string]any{
			"query":       query,
			"provider":    provider,
			"resultCount": len(results),
		},
	}, nil
}

func normalizeSearchDomains(raw []string) []string {
	var out []string
	seen := make(map[string]bool, len(raw))
	for _, d := range raw {
		clean := strings.ToLower(strings.TrimSpace(d))
		clean = strings.TrimPrefix(clean, "https://")
		clean = strings.TrimPrefix(clean, "http://")
		clean = strings.TrimRight(clean, "/")
		if idx := strings.IndexByte(clean, '/'); idx > 0 {
			clean = clean[:idx]
		}
		if clean == "" || seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	return out
}

func resolveWebSearchBackend(requested string, cfg map[string]any) (provider, apiKey string) {
	wsCfg, _ := cfg["websearch"].(map[string]any)
	strCfg := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := wsCfg[k].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}
	tavilyKey := firstNonEmptySearchStr(strCfg("tavilyApiKey", "tavily_api_key"), os.Getenv("TAVILY_API_KEY"))
	exaKey := firstNonEmptySearchStr(strCfg("exaApiKey", "exa_api_key"), os.Getenv("EXA_API_KEY"))
	braveKey := firstNonEmptySearchStr(strCfg("braveApiKey", "brave_api_key"), os.Getenv("BRAVE_API_KEY"), os.Getenv("BRAVE_SEARCH_API_KEY"))
	genericKey := strCfg("apiKey", "api_key")

	choice := strings.ToLower(strings.TrimSpace(requested))
	if choice == "" || choice == "auto" {
		choice = strings.ToLower(strings.TrimSpace(strCfg("provider")))
	}
	switch choice {
	case "tavily":
		if k := firstNonEmptySearchStr(tavilyKey, genericKey); k != "" {
			return "tavily", k
		}
		return "duckduckgo", ""
	case "exa":
		if k := firstNonEmptySearchStr(exaKey, genericKey); k != "" {
			return "exa", k
		}
		return "duckduckgo", ""
	case "brave":
		if k := firstNonEmptySearchStr(braveKey, genericKey); k != "" {
			return "brave", k
		}
		return "duckduckgo", ""
	case "duckduckgo":
		return "duckduckgo", ""
	}
	if tavilyKey != "" {
		return "tavily", tavilyKey
	}
	if exaKey != "" {
		return "exa", exaKey
	}
	if braveKey != "" {
		return "brave", braveKey
	}
	return "duckduckgo", ""
}

func firstNonEmptySearchStr(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func appendDomainSiteQuery(query string, domains []string) string {
	if len(domains) == 0 {
		return query
	}
	parts := make([]string, 0, len(domains))
	for _, d := range domains {
		parts = append(parts, "site:"+d)
	}
	if len(parts) == 1 {
		return query + " " + parts[0]
	}
	return query + " (" + strings.Join(parts, " OR ") + ")"
}

func (t *webSearchTool) searchTavily(ctx context.Context, client *http.Client, apiKey, query string, maxResults int, domains []string) ([]webSearchResult, error) {
	endpoint := strings.TrimSpace(t.tavilyEndpoint)
	if endpoint == "" {
		endpoint = "https://api.tavily.com/search"
	}
	bodyMap := map[string]any{
		"api_key":      apiKey,
		"query":        query,
		"max_results":  maxResults,
		"search_depth": "basic",
	}
	if len(domains) > 0 {
		bodyMap["include_domains"] = domains
	}
	rawBody, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(rawBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "HoxiNativeAgent/1.0 (+https://hoxi.ai)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxWebFetchBodyBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tavily search HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var parsed struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse tavily response: %w", err)
	}
	out := make([]webSearchResult, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		out = append(out, webSearchResult{
			Title:   cleanSearchText(r.Title),
			URL:     strings.TrimSpace(r.URL),
			Snippet: cleanSearchSnippet(r.Content),
		})
	}
	return out, nil
}

func (t *webSearchTool) searchExa(ctx context.Context, client *http.Client, apiKey, query string, maxResults int, domains []string) ([]webSearchResult, error) {
	endpoint := strings.TrimSpace(t.exaEndpoint)
	if endpoint == "" {
		endpoint = "https://api.exa.ai/search"
	}
	bodyMap := map[string]any{
		"query":      query,
		"numResults": maxResults,
		"contents": map[string]any{
			"highlights": map[string]any{"numSentences": 2},
		},
	}
	if len(domains) > 0 {
		bodyMap["includeDomains"] = domains
	}
	rawBody, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(rawBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("User-Agent", "HoxiNativeAgent/1.0 (+https://hoxi.ai)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxWebFetchBodyBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("exa search HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var parsed struct {
		Results []struct {
			Title      string   `json:"title"`
			URL        string   `json:"url"`
			Highlights []string `json:"highlights"`
			Text       string   `json:"text"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse exa response: %w", err)
	}
	out := make([]webSearchResult, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		snip := strings.Join(r.Highlights, " ... ")
		if strings.TrimSpace(snip) == "" {
			snip = r.Text
		}
		out = append(out, webSearchResult{
			Title:   cleanSearchText(r.Title),
			URL:     strings.TrimSpace(r.URL),
			Snippet: cleanSearchSnippet(snip),
		})
	}
	return out, nil
}

func (t *webSearchTool) searchBrave(ctx context.Context, client *http.Client, apiKey, query string, maxResults int, domains []string) ([]webSearchResult, error) {
	endpoint := strings.TrimSpace(t.braveEndpoint)
	if endpoint == "" {
		endpoint = "https://api.search.brave.com/res/v1/web/search"
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("q", appendDomainSiteQuery(query, domains))
	q.Set("count", fmt.Sprintf("%d", maxResults))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", apiKey)
	req.Header.Set("User-Agent", "HoxiNativeAgent/1.0 (+https://hoxi.ai)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxWebFetchBodyBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("brave search HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var parsed struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse brave response: %w", err)
	}
	out := make([]webSearchResult, 0, len(parsed.Web.Results))
	for _, r := range parsed.Web.Results {
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		out = append(out, webSearchResult{
			Title:   cleanSearchText(r.Title),
			URL:     strings.TrimSpace(r.URL),
			Snippet: cleanSearchSnippet(r.Description),
		})
	}
	return out, nil
}

func (t *webSearchTool) searchDuckDuckGo(ctx context.Context, client *http.Client, query string, maxResults int, domains []string) ([]webSearchResult, error) {
	endpoint := strings.TrimSpace(t.ddgEndpoint)
	if endpoint == "" {
		endpoint = "https://html.duckduckgo.com/html/"
	}
	form := url.Values{}
	form.Set("q", appendDomainSiteQuery(query, domains))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; HoxiNativeAgent/1.0; +https://hoxi.ai)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxWebFetchBodyBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("duckduckgo search HTTP %d", resp.StatusCode)
	}
	return parseDuckDuckGoHTML(string(data), maxResults), nil
}

func parseDuckDuckGoHTML(doc string, maxResults int) []webSearchResult {
	linkMatches := reDDGResultLink.FindAllStringSubmatchIndex(doc, -1)
	if len(linkMatches) == 0 {
		return nil
	}
	results := make([]webSearchResult, 0, min(len(linkMatches), maxResults))
	seenURL := make(map[string]bool, maxResults)
	for i, loc := range linkMatches {
		if len(results) >= maxResults {
			break
		}
		rawHref := html.UnescapeString(strings.TrimSpace(doc[loc[2]:loc[3]]))
		resolvedURL := unwrapDuckDuckGoURL(rawHref)
		if resolvedURL == "" || seenURL[resolvedURL] {
			continue
		}
		title := cleanSearchText(doc[loc[4]:loc[5]])
		if title == "" {
			title = resolvedURL
		}
		windowEnd := len(doc)
		if i+1 < len(linkMatches) {
			windowEnd = linkMatches[i+1][0]
		}
		snippet := ""
		if snipSub := reDDGResultSnippet.FindStringSubmatch(doc[loc[1]:windowEnd]); len(snipSub) >= 2 {
			snippet = cleanSearchSnippet(snipSub[1])
		}
		seenURL[resolvedURL] = true
		results = append(results, webSearchResult{
			Title:   title,
			URL:     resolvedURL,
			Snippet: snippet,
		})
	}
	return results
}

func unwrapDuckDuckGoURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "//") {
		s = "https:" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	if strings.Contains(strings.ToLower(u.Host), "duckduckgo.com") && strings.HasPrefix(u.Path, "/l/") {
		if target := strings.TrimSpace(u.Query().Get("uddg")); target != "" {
			if decoded, err := url.QueryUnescape(target); err == nil && decoded != "" {
				target = decoded
			}
			u, err = url.Parse(target)
			if err != nil {
				return ""
			}
		}
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" || strings.TrimSpace(u.Host) == "" {
		return ""
	}
	return u.String()
}

func cleanSearchText(raw string) string {
	stripped := reAnyHTMLTag.ReplaceAllString(raw, " ")
	return strings.Join(strings.Fields(html.UnescapeString(stripped)), " ")
}

func cleanSearchSnippet(raw string) string {
	s := cleanSearchText(raw)
	runes := []rune(s)
	if len(runes) > maxWebSearchSnippetRunes {
		return string(runes[:maxWebSearchSnippetRunes-3]) + "..."
	}
	return s
}

func formatWebSearchResults(query, provider string, results []webSearchResult) string {
	if len(results) == 0 {
		return fmt.Sprintf("No web search results found for %q (provider: %s).", query, provider)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Web search results for %q (provider: %s, %d results):\n\n", query, provider, len(results))
	for i, r := range results {
		title := r.Title
		if title == "" {
			title = r.URL
		}
		fmt.Fprintf(&b, "%d. [%s](%s)\n", i+1, title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "   %s\n", r.Snippet)
		}
	}
	b.WriteString("\nUse `webfetch` on a specific URL above if you need the full page content.")
	return strings.TrimSpace(b.String())
}

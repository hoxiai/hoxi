package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

const geminiCacheTTL = "720s"
const geminiCacheMinTokens = 32 * 1024
const maxGeminiCaches = 128

var cachedContentName = regexp.MustCompile(`^cachedContents/[A-Za-z0-9_-]+$`)

type geminiCacheEntry struct {
	name          string
	count         int
	digest        [32]byte
	pendingCount  int
	pendingDigest [32]byte
	expires       time.Time
	busy          bool
}

type geminiContextCache struct {
	mu      sync.Mutex
	entries map[string]geminiCacheEntry
}

func allowGeminiContextCache(req llm.StreamHTTPRequest, base string) bool {
	if req.Target.Protocol != llm.ProtocolGoogle || req.Target.DisableGeminiExplicitCache || strings.TrimSpace(req.SessionID) == "" {
		return false
	}
	model := strings.TrimPrefix(req.Target.ModelID, "models/")
	if !strings.HasPrefix(model, "gemini-2.5-") && !strings.HasPrefix(model, "gemini-3-") && !strings.HasPrefix(model, "gemini-3.") {
		return false
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "generativelanguage.googleapis.com") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	switch strings.TrimSuffix(u.EscapedPath(), "/") {
	case "", "/v1beta", "/v1beta/models":
		return true
	}
	return false
}

func geminiCacheIdentity(req llm.StreamHTTPRequest, base, key string, headers map[string]string) string {
	b, _ := json.Marshal([]any{req.Target.ProviderID, req.Target.ModelID, base, req.SessionID, key, headers, req.ExtraHeaders})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type geminiCacheSend func(context.Context, string, string, []byte) (*http.Response, error)

func (c *geminiContextCache) prepare(ctx context.Context, key string, req llm.StreamHTTPRequest, now time.Time, send geminiCacheSend) ([]byte, string) {
	if len(req.Body) < 4*geminiCacheMinTokens {
		return req.Body, ""
	}
	var fields map[string]json.RawMessage
	var contents []json.RawMessage
	if json.Unmarshal(req.Body, &fields) != nil || json.Unmarshal(fields["contents"], &contents) != nil || len(contents) == 0 || fields["cachedContent"] != nil {
		return req.Body, ""
	}
	c.mu.Lock()
	entry, exists := c.entries[key]
	if exists && !entry.expires.After(now) && !entry.busy {
		delete(c.entries, key)
		exists = false
		entry = geminiCacheEntry{}
	}
	if entry.busy || (exists && entry.name == "") {
		c.mu.Unlock()
		return req.Body, ""
	}
	c.mu.Unlock()
	// Hashing a large prompt must not stall unrelated sessions.
	n := len(contents) - 1
	rebuild := false
	var digest [32]byte
	var eligible bool
	if exists {
		matchedExisting := false
		if entry.count < len(contents) {
			digest, eligible = geminiPrefix(fields, contents[:entry.count])
			matchedExisting = eligible && digest == entry.digest
		}
		if matchedExisting {
			n = entry.count
		} else {
			// Prefix diverged (e.g. after pruning or /compact). Confirm stability
			// across two consecutive requests on the new prefix before replacing
			// the live cache so one-off variations neither thrash nor evict it.
			confirmed := false
			if entry.pendingDigest != ([32]byte{}) && entry.pendingCount < len(contents) {
				pd, ok := geminiPrefix(fields, contents[:entry.pendingCount])
				confirmed = ok && pd == entry.pendingDigest
			}
			newDigest, newEligible := geminiPrefix(fields, contents[:n])
			if !newEligible {
				return req.Body, ""
			}
			if !confirmed {
				c.mu.Lock()
				if cur, present := c.entries[key]; present && cur == entry {
					cur.pendingCount = n
					cur.pendingDigest = newDigest
					c.entries[key] = cur
				}
				c.mu.Unlock()
				return req.Body, ""
			}
			digest = newDigest
			eligible = true
			rebuild = true
		}
	} else {
		digest, eligible = geminiPrefix(fields, contents[:n])
		if !eligible {
			return req.Body, ""
		}
	}
	c.mu.Lock()
	if current, present := c.entries[key]; present != exists || current != entry {
		c.mu.Unlock()
		return req.Body, ""
	}
	if !exists || rebuild || entry.expires.Sub(now) < 2*time.Minute {
		if c.entries == nil {
			c.entries = make(map[string]geminiCacheEntry)
		}
		if !exists && len(c.entries) >= maxGeminiCaches {
			for k, v := range c.entries {
				if !v.busy && !v.expires.After(now) {
					delete(c.entries, k)
				}
			}
			if len(c.entries) >= maxGeminiCaches {
				c.mu.Unlock()
				return req.Body, ""
			}
		}
		entry.busy = true
		c.entries[key] = entry
		c.mu.Unlock()
		// Resource bodies never leave the selected official API. No prompt bodies
		// or credentials are retained in the local cache.
		method, path := http.MethodPost, "cachedContents"
		body := map[string]any{"ttl": geminiCacheTTL}
		renewing := exists && !rebuild
		if renewing {
			method, path = http.MethodPatch, entry.name+"?updateMask=ttl"
		} else {
			body["model"] = "models/" + strings.TrimPrefix(req.Target.ModelID, "models/")
			if n > 0 {
				body["contents"] = contents[:n]
			}
			for _, k := range []string{"systemInstruction", "tools", "toolConfig"} {
				if fields[k] != nil {
					body[k] = fields[k]
				}
			}
		}
		raw, _ := json.Marshal(body)
		callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		resp, err := send(callCtx, method, path, raw)
		var resource struct {
			Name       string
			ExpireTime time.Time
		}
		if err == nil && resp != nil {
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				err = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&resource)
			}
			resp.Body.Close()
		}
		cancel()
		c.mu.Lock()
		if err == nil && cachedContentName.MatchString(resource.Name) && resource.ExpireTime.After(now) && (!renewing || resource.Name == entry.name) {
			entry = geminiCacheEntry{name: resource.Name, count: n, digest: digest, expires: resource.ExpireTime}
		} else {
			entry = geminiCacheEntry{expires: now.Add(2 * time.Minute)}
		}
		c.entries[key] = entry
	}
	c.mu.Unlock()
	if entry.name == "" {
		return req.Body, ""
	}
	for _, k := range []string{"systemInstruction", "tools", "toolConfig"} {
		delete(fields, k)
	}
	fields["cachedContent"], _ = json.Marshal(entry.name)
	fields["contents"], _ = json.Marshal(contents[n:])
	body, err := json.Marshal(fields)
	if err != nil {
		return req.Body, ""
	}
	return body, entry.name
}

func geminiPrefix(fields map[string]json.RawMessage, contents []json.RawMessage) ([32]byte, bool) {
	hash := sha256.New()
	size := 0
	add := func(raw []byte) {
		hash.Write(raw)
		hash.Write([]byte{0})
		if !bytes.Contains(raw, []byte(`"inlineData"`)) {
			size += len(raw)
		}
	}
	for _, k := range []string{"systemInstruction", "tools", "toolConfig"} {
		add(fields[k])
	}
	for _, raw := range contents {
		add(raw)
	}
	var sum [32]byte
	copy(sum[:], hash.Sum(nil))
	return sum, size/4 >= geminiCacheMinTokens
}

func geminiCacheRejected(resp *http.Response) bool {
	switch resp.StatusCode {
	case http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound:
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		message := strings.ToLower(string(raw))
		return strings.Contains(message, "cachedcontent") || strings.Contains(message, "cached_content") || strings.Contains(message, "cached content")
	}
	return false
}

func (c *geminiContextCache) reject(key, name string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[key]; e.name == name && !e.busy {
		c.entries[key] = geminiCacheEntry{expires: now.Add(2 * time.Minute)}
	}
}

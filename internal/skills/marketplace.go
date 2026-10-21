package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// The hoxi.ai skills catalog publishes metadata only. Skill bodies are
// downloaded on explicit user action and written to ~/.hoxi/skills/<slug>/,
// after the SHA-256 digest from the catalog has been verified. Nothing is
// installed automatically and no catalog entry can execute code on its own.

const (
	marketplacePath     = "/api/hoxi/skills"
	marketplaceCacheTTL = 5 * time.Minute
	maxMarketplaceBytes = 1 << 20
	maxSkillBodyBytes   = 512 << 10
	marketplaceTimeout  = 10 * time.Second
)

var (
	ErrMarketplaceUnavailable = errors.New("skills marketplace unavailable")
	ErrInvalidSkillSlug       = errors.New("invalid skill slug")
	ErrSkillAlreadyInstalled  = errors.New("skill already installed")
	ErrSkillDigestMismatch    = errors.New("skill content digest mismatch")
	ErrSkillContentInvalid    = errors.New("skill content is not a valid SKILL.md")
)

// marketplaceSkillSlug mirrors the Agent Skills spec: lowercase alphanumerics
// and single hyphens, at most 64 characters.
var marketplaceSkillSlug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// extraMarketplaceHosts are the only non-catalog hosts a skill body may be
// fetched from. The catalog host itself is always allowed; everything else is
// rejected so a compromised entry cannot turn install into an SSRF primitive.
var extraMarketplaceHosts = map[string]bool{
	"github.com":               true,
	"raw.githubusercontent.com": true,
	"cdn.jsdelivr.net":         true,
}

type MarketplaceSkill struct {
	Slug            string   `json:"slug"`
	Name            string   `json:"name"`
	Description     string   `json:"description,omitempty"`
	Category        string   `json:"category,omitempty"`
	CategoryLabel   string   `json:"categoryLabel,omitempty"`
	Author          string   `json:"author,omitempty"`
	Version         string   `json:"version,omitempty"`
	Runtime         string   `json:"runtime,omitempty"`
	ContentURL      string   `json:"contentUrl"`
	ContentSHA256   string   `json:"contentSha256"`
	HomepageURL     string   `json:"homepageUrl,omitempty"`
	RepoURL         string   `json:"repoUrl,omitempty"`
	Badge           string   `json:"badge,omitempty"`
	BadgeTone       string   `json:"badgeTone,omitempty"`
	Icon            string   `json:"icon,omitempty"`
	Compatibility   string   `json:"compatibility,omitempty"`
	Highlights      []string `json:"highlights,omitempty"`
	Order           int      `json:"order,omitempty"`
	Installed       bool     `json:"installed"`
	InstalledDigest string   `json:"installedDigest,omitempty"`
	UpdateAvailable bool     `json:"updateAvailable,omitempty"`
}

type MarketplaceResponse struct {
	Skills    []MarketplaceSkill `json:"skills"`
	Source    string             `json:"source"`
	CacheHint string             `json:"cacheHint,omitempty"`
}

type marketplacePayload struct {
	Data struct {
		Items []MarketplaceSkill `json:"items"`
	} `json:"data"`
}

type marketplaceCache struct {
	fetchedAt time.Time
	skills    []MarketplaceSkill
}

// MarketplaceService lists and installs skills published by the Hoxi server.
type MarketplaceService struct {
	baseURL string
	client  *http.Client
	root    func() string

	mu    sync.Mutex
	cache marketplaceCache
}

func NewMarketplaceService(baseURL string, client *http.Client) *MarketplaceService {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = hoxipaths.ServerBaseURL()
	}
	if client == nil {
		client = &http.Client{Timeout: marketplaceTimeout}
	}
	return &MarketplaceService{
		baseURL: baseURL,
		client:  client,
		root:    DefaultUserDir,
	}
}

// List returns the remote catalog annotated with local install state.
func (s *MarketplaceService) List(ctx context.Context, forceRefresh bool) (MarketplaceResponse, error) {
	skills, err := s.catalog(ctx, forceRefresh)
	if err != nil {
		return MarketplaceResponse{}, err
	}
	installed := installedSkillDigests(s.root())
	out := make([]MarketplaceSkill, 0, len(skills))
	for _, item := range skills {
		entry := item
		if digest, ok := installed[item.Slug]; ok {
			entry.Installed = true
			entry.InstalledDigest = digest
			entry.UpdateAvailable = !strings.EqualFold(digest, item.ContentSHA256)
		}
		out = append(out, entry)
	}
	return MarketplaceResponse{
		Skills: out,
		Source: s.baseURL,
		CacheHint: "The catalog is metadata published by hoxi.ai. " +
			"Installing downloads SKILL.md, verifies its SHA-256 digest and writes it to ~/.hoxi/skills.",
	}, nil
}

// Install downloads the catalog entry for slug, verifies the digest and writes
// it under ~/.hoxi/skills/<slug>/SKILL.md. overwrite must be set explicitly to
// replace an existing file that differs from the published digest.
func (s *MarketplaceService) Install(ctx context.Context, slug string, overwrite bool) (MarketplaceSkill, error) {
	slug = strings.TrimSpace(slug)
	if !marketplaceSkillSlug.MatchString(slug) || len(slug) > 64 {
		return MarketplaceSkill{}, fmt.Errorf("%w: %q", ErrInvalidSkillSlug, slug)
	}
	skills, err := s.catalog(ctx, false)
	if err != nil {
		return MarketplaceSkill{}, err
	}
	var entry *MarketplaceSkill
	for i := range skills {
		if skills[i].Slug == slug {
			entry = &skills[i]
			break
		}
	}
	if entry == nil {
		return MarketplaceSkill{}, fmt.Errorf("%w: skill %q is not in the catalog", ErrNotFound, slug)
	}

	root := s.root()
	if root == "" {
		return MarketplaceSkill{}, fmt.Errorf("%w: ~/.hoxi is unavailable", ErrBadRequest)
	}
	target := filepath.Join(root, slug, "SKILL.md")
	if existing, err := os.ReadFile(target); err == nil {
		sum := sha256.Sum256(existing)
		if strings.EqualFold(hex.EncodeToString(sum[:]), entry.ContentSHA256) {
			return *entry, nil
		}
		if !overwrite {
			return MarketplaceSkill{}, fmt.Errorf("%w: %s exists with different content", ErrSkillAlreadyInstalled, slug)
		}
	} else if !os.IsNotExist(err) {
		return MarketplaceSkill{}, err
	}

	body, err := s.download(ctx, entry.ContentURL)
	if err != nil {
		return MarketplaceSkill{}, err
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	if !strings.EqualFold(digest, entry.ContentSHA256) {
		return MarketplaceSkill{}, fmt.Errorf("%w: expected %s, got %s", ErrSkillDigestMismatch, entry.ContentSHA256, digest)
	}
	if name := frontmatterName(body); name != slug {
		return MarketplaceSkill{}, fmt.Errorf("%w: frontmatter name %q does not match %q", ErrSkillContentInvalid, name, slug)
	}
	if err := writeSkillFile(target, body); err != nil {
		return MarketplaceSkill{}, err
	}

	entry.Installed = true
	entry.InstalledDigest = digest
	entry.UpdateAvailable = false
	return *entry, nil
}

func (s *MarketplaceService) catalog(ctx context.Context, forceRefresh bool) ([]MarketplaceSkill, error) {
	s.mu.Lock()
	cached := s.cache
	s.mu.Unlock()
	if !forceRefresh && len(cached.skills) > 0 && time.Since(cached.fetchedAt) < marketplaceCacheTTL {
		return cached.skills, nil
	}

	endpoint := s.baseURL + marketplacePath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrMarketplaceUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "HoxiDesktop/1.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrMarketplaceUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP %d", ErrMarketplaceUnavailable, resp.StatusCode)
	}

	var payload marketplacePayload
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxMarketplaceBytes)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrMarketplaceUnavailable, err)
	}

	skills := make([]MarketplaceSkill, 0, len(payload.Data.Items))
	for _, item := range payload.Data.Items {
		clean, err := sanitizeMarketplaceSkill(item)
		if err != nil {
			continue
		}
		skills = append(skills, clean)
	}

	s.mu.Lock()
	s.cache = marketplaceCache{fetchedAt: time.Now(), skills: skills}
	s.mu.Unlock()
	return skills, nil
}

// sanitizeMarketplaceSkill drops entries whose slug or URL would let the
// catalog write outside ~/.hoxi/skills or fetch from an unapproved host.
func sanitizeMarketplaceSkill(item MarketplaceSkill) (MarketplaceSkill, error) {
	slug := strings.TrimSpace(item.Slug)
	if !marketplaceSkillSlug.MatchString(slug) || len(slug) > 64 {
		return MarketplaceSkill{}, fmt.Errorf("%w: %q", ErrInvalidSkillSlug, slug)
	}
	raw := strings.TrimSpace(item.ContentURL)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return MarketplaceSkill{}, fmt.Errorf("%w: invalid contentUrl", ErrBadRequest)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return MarketplaceSkill{}, fmt.Errorf("%w: unsupported scheme %q", ErrBadRequest, parsed.Scheme)
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return MarketplaceSkill{}, fmt.Errorf("%w: plain http is only allowed for loopback", ErrBadRequest)
	}
	if !marketplaceHostAllowed(parsed.Hostname()) {
		return MarketplaceSkill{}, fmt.Errorf("%w: host %q is not approved for skill downloads", ErrBadRequest, parsed.Hostname())
	}
	if len(item.ContentSHA256) != 64 {
		return MarketplaceSkill{}, fmt.Errorf("%w: invalid contentSha256", ErrBadRequest)
	}
	if _, err := hex.DecodeString(item.ContentSHA256); err != nil {
		return MarketplaceSkill{}, fmt.Errorf("%w: invalid contentSha256", ErrBadRequest)
	}
	item.Slug = slug
	item.Name = strings.TrimSpace(item.Name)
	if item.Name == "" {
		item.Name = slug
	}
	item.ContentURL = parsed.String()
	item.ContentSHA256 = strings.ToLower(item.ContentSHA256)
	return item, nil
}

func marketplaceHostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if extraMarketplaceHosts[host] {
		return true
	}
	if isLoopbackHost(host) {
		return true
	}
	base, err := url.Parse(hoxipaths.ServerBaseURL())
	if err != nil {
		return false
	}
	return strings.EqualFold(base.Hostname(), host)
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

func (s *MarketplaceService) download(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrMarketplaceUnavailable, err)
	}
	req.Header.Set("Accept", "text/markdown, text/plain")
	req.Header.Set("User-Agent", "HoxiDesktop/1.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrMarketplaceUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP %d", ErrMarketplaceUnavailable, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSkillBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrMarketplaceUnavailable, err)
	}
	if len(body) > maxSkillBodyBytes {
		return nil, fmt.Errorf("%w: skill body exceeds %d bytes", ErrSkillContentInvalid, maxSkillBodyBytes)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("%w: empty skill body", ErrSkillContentInvalid)
	}
	return body, nil
}

func installedSkillDigests(root string) map[string]string {
	out := make(map[string]string)
	if root == "" {
		return out
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if !entry.IsDir() || !marketplaceSkillSlug.MatchString(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name(), "SKILL.md"))
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		out[entry.Name()] = hex.EncodeToString(sum[:])
	}
	return out
}

func frontmatterName(body []byte) string {
	text := string(body)
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return ""
	}
	rest := text[strings.Index(text, "\n")+1:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return ""
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if !strings.HasPrefix(line, "name:") {
			continue
		}
		return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "name:")), `"'`)
	}
	return ""
}

// writeSkillFile replaces target atomically so an interrupted install cannot
// leave a half-written SKILL.md that the engine would then load.
func writeSkillFile(target string, data []byte) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".SKILL.md.tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, target)
}

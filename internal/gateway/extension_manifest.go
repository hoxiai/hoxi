package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const (
	ExtensionSchemaVersion       = "hoxi.extension/v1"
	DefaultToolStartupBudgetMs   = 5000
	MinToolStartupBudgetMs       = 250
	MaxToolStartupBudgetMs       = 30000
	MaxDeclarativeToolsPerPlugin = 32
	MaxDeclarativePanels         = 8
)

var (
	manifestIDRe       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
	manifestSemverRe   = regexp.MustCompile(`^v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$`)
	manifestToolNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{2,63}$`)
)

// ExtensionManifest defines a schema-validated declarative plugin descriptor
// (stored under ~/.hoxi/extensions/<id>/manifest.json or <workspace>/.hoxi/extensions/<id>/manifest.json).
type ExtensionManifest struct {
	SchemaVersion string                  `json:"schemaVersion"`
	ID            string                  `json:"id"`
	Name          string                  `json:"name"`
	Version       string                  `json:"version"`
	Description   string                  `json:"description,omitempty"`
	Permissions   []string                `json:"permissions,omitempty"`
	Tools         []ExtensionToolManifest `json:"tools,omitempty"`
	Panels        []ExtensionPanelSpec    `json:"panels,omitempty"`
}

// ExtensionToolManifest declares a JSON-Schema-validated tool exposed by a manifest.
type ExtensionToolManifest struct {
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	ReadOnly        bool           `json:"readOnly,omitempty"`
	StartupBudgetMs int            `json:"startupBudgetMs,omitempty"`
	Endpoint        string         `json:"endpoint,omitempty"`
	Parameters      map[string]any `json:"parameters"`
}

// ExtensionPanelSpec declares a UI panel slot contributed by a declarative extension.
type ExtensionPanelSpec struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Slot  string `json:"slot"` // "inspector" | "settings"
	Entry string `json:"entry"`
}

var allowedManifestPermissions = map[string]bool{
	"workspace:read":  true,
	"workspace:write": true,
	"network:local":   true,
	"network:https":   true,
	"tools:register":  true,
	"panels:register": true,
}

var reservedManifestToolNames = map[string]bool{
	"read": true, "write": true, "edit": true, "multiedit": true, "apply_patch": true,
	"glob": true, "grep": true, "bash": true, "task": true, "question": true,
	"plan_consent": true, "todoread": true, "todowrite": true, "webfetch": true,
	"mcp_list_tools": true, "mcp_call_tool": true, "hoxi_browser": true,
	"hoxi_computer": true, "hoxi_image": true, "hoxi_tools": true, "hoxi_data": true,
	"hoxi_code_index": true, "code_search": true, "symbol_inspect": true,
	"impact_analysis": true, "skill": true, "goal_report": true,
}

// ExtensionManifestJSONSchema returns the canonical JSON Schema document for
// hoxi.extension/v1 so plugin authors and runtime tests validate against one SSOT.
func ExtensionManifestJSONSchema() map[string]any {
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"title":                "Hoxi Extension Manifest v1",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"schemaVersion", "id", "name", "version"},
		"properties": map[string]any{
			"schemaVersion": map[string]any{"const": ExtensionSchemaVersion},
			"id":            map[string]any{"type": "string", "pattern": manifestIDRe.String()},
			"name":          map[string]any{"type": "string", "minLength": 1, "maxLength": 80},
			"version":       map[string]any{"type": "string", "pattern": manifestSemverRe.String()},
			"description":   map[string]any{"type": "string", "maxLength": 400},
			"permissions":   map[string]any{"type": "array"},
			"tools":         map[string]any{"type": "array", "maxItems": MaxDeclarativeToolsPerPlugin},
			"panels":        map[string]any{"type": "array", "maxItems": MaxDeclarativePanels},
		},
	}
}

// ValidateExtensionManifest strictly decodes and validates raw JSON against the
// hoxi.extension/v1 contract, rejecting unknown fields, reserved tool names,
// malformed JSON Schema parameter definitions, or out-of-bounds startup budgets.
func ValidateExtensionManifest(raw []byte) (ExtensionManifest, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ExtensionManifest{}, errors.New("extension manifest is empty")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var m ExtensionManifest
	if err := dec.Decode(&m); err != nil {
		return ExtensionManifest{}, fmt.Errorf("invalid extension manifest JSON: %w", err)
	}
	if dec.More() {
		return ExtensionManifest{}, errors.New("trailing data after extension manifest JSON")
	}
	if strings.TrimSpace(m.SchemaVersion) != ExtensionSchemaVersion {
		return ExtensionManifest{}, fmt.Errorf("unsupported schemaVersion %q (expected %q)", m.SchemaVersion, ExtensionSchemaVersion)
	}
	m.ID = strings.TrimSpace(m.ID)
	if !manifestIDRe.MatchString(m.ID) {
		return ExtensionManifest{}, fmt.Errorf("invalid extension id %q", m.ID)
	}
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" || len(m.Name) > 80 {
		return ExtensionManifest{}, errors.New("extension name must be 1..80 characters")
	}
	m.Version = strings.TrimSpace(m.Version)
	if !manifestSemverRe.MatchString(m.Version) {
		return ExtensionManifest{}, fmt.Errorf("invalid semver version %q", m.Version)
	}
	if len(m.Description) > 400 {
		return ExtensionManifest{}, errors.New("extension description exceeds 400 characters")
	}

	perms := make(map[string]bool, len(m.Permissions))
	for _, p := range m.Permissions {
		p = strings.TrimSpace(p)
		if !allowedManifestPermissions[p] {
			return ExtensionManifest{}, fmt.Errorf("unsupported or forbidden extension permission %q", p)
		}
		perms[p] = true
	}

	if len(m.Tools) > MaxDeclarativeToolsPerPlugin {
		return ExtensionManifest{}, fmt.Errorf("extension declares %d tools (max %d)", len(m.Tools), MaxDeclarativeToolsPerPlugin)
	}
	if len(m.Tools) > 0 && !perms["tools:register"] {
		return ExtensionManifest{}, errors.New("extension declaring tools must request \"tools:register\" permission")
	}
	seenTools := make(map[string]bool, len(m.Tools))
	for i := range m.Tools {
		if err := validateExtensionTool(&m.Tools[i], perms, seenTools); err != nil {
			return ExtensionManifest{}, fmt.Errorf("tools[%d]: %w", i, err)
		}
	}

	if len(m.Panels) > MaxDeclarativePanels {
		return ExtensionManifest{}, fmt.Errorf("extension declares %d panels (max %d)", len(m.Panels), MaxDeclarativePanels)
	}
	if len(m.Panels) > 0 && !perms["panels:register"] {
		return ExtensionManifest{}, errors.New("extension declaring panels must request \"panels:register\" permission")
	}
	seenPanels := make(map[string]bool, len(m.Panels))
	for i := range m.Panels {
		if err := validateExtensionPanel(&m.Panels[i], seenPanels); err != nil {
			return ExtensionManifest{}, fmt.Errorf("panels[%d]: %w", i, err)
		}
	}
	return m, nil
}

func validateExtensionTool(t *ExtensionToolManifest, perms map[string]bool, seen map[string]bool) error {
	t.Name = strings.TrimSpace(t.Name)
	if !manifestToolNameRe.MatchString(t.Name) {
		return fmt.Errorf("invalid tool name %q", t.Name)
	}
	if reservedManifestToolNames[t.Name] || strings.HasPrefix(t.Name, "hoxi_") {
		return fmt.Errorf("tool name %q is reserved by Hoxi core", t.Name)
	}
	if seen[t.Name] {
		return fmt.Errorf("duplicate tool name %q", t.Name)
	}
	seen[t.Name] = true
	t.Description = strings.TrimSpace(t.Description)
	if t.Description == "" || len(t.Description) > 1024 {
		return fmt.Errorf("tool %q description must be 1..1024 characters", t.Name)
	}
	if t.StartupBudgetMs <= 0 {
		t.StartupBudgetMs = DefaultToolStartupBudgetMs
	} else if t.StartupBudgetMs < MinToolStartupBudgetMs || t.StartupBudgetMs > MaxToolStartupBudgetMs {
		return fmt.Errorf("tool %q startupBudgetMs %d outside %d..%d", t.Name, t.StartupBudgetMs, MinToolStartupBudgetMs, MaxToolStartupBudgetMs)
	}
	ep := strings.TrimSpace(t.Endpoint)
	u, err := url.Parse(ep)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("tool %q requires a valid http(s) endpoint URL", t.Name)
	}
	isLocal := u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"
	if isLocal && !perms["network:local"] {
		return fmt.Errorf("tool %q targeting loopback requires \"network:local\" permission", t.Name)
	}
	if !isLocal {
		if u.Scheme != "https" {
			return fmt.Errorf("external tool %q must use https://", t.Name)
		}
		if !perms["network:https"] {
			return fmt.Errorf("external tool %q requires \"network:https\" permission", t.Name)
		}
	}
	t.Endpoint = ep
	return validateJSONObjectSchema(t.Parameters)
}

func validateJSONObjectSchema(schema map[string]any) error {
	if len(schema) == 0 {
		return errors.New("parameters JSON Schema is required")
	}
	if typ, _ := schema["type"].(string); typ != "object" {
		return fmt.Errorf("root parameters schema type must be \"object\", got %q", typ)
	}
	propsRaw, hasProps := schema["properties"]
	if !hasProps {
		return errors.New("parameters schema must declare \"properties\"")
	}
	propMap, ok := propsRaw.(map[string]any)
	if !ok {
		return errors.New("\"properties\" must be a JSON object")
	}
	for k, v := range propMap {
		if strings.TrimSpace(k) == "" {
			return errors.New("property name must not be empty")
		}
		def, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("property %q schema must be an object", k)
		}
		pType, _ := def["type"].(string)
		switch pType {
		case "string", "number", "integer", "boolean", "array", "object":
		default:
			return fmt.Errorf("property %q has unsupported JSON Schema type %q", k, pType)
		}
	}
	if reqRaw, hasReq := schema["required"]; hasReq {
		switch reqList := reqRaw.(type) {
		case []any:
			for _, item := range reqList {
				s, ok := item.(string)
				if !ok || strings.TrimSpace(s) == "" {
					return errors.New("\"required\" entries must be non-empty strings")
				}
				if _, exists := propMap[s]; !exists {
					return fmt.Errorf("required property %q is missing from properties", s)
				}
			}
		case []string:
			for _, s := range reqList {
				if _, exists := propMap[s]; !exists {
					return fmt.Errorf("required property %q is missing from properties", s)
				}
			}
		default:
			return errors.New("\"required\" must be an array of property names")
		}
	}
	return nil
}

func validateExtensionPanel(p *ExtensionPanelSpec, seen map[string]bool) error {
	p.ID = strings.TrimSpace(p.ID)
	if !manifestIDRe.MatchString(p.ID) {
		return fmt.Errorf("invalid panel id %q", p.ID)
	}
	if seen[p.ID] {
		return fmt.Errorf("duplicate panel id %q", p.ID)
	}
	seen[p.ID] = true
	p.Title = strings.TrimSpace(p.Title)
	if p.Title == "" || len(p.Title) > 60 {
		return fmt.Errorf("panel %q title must be 1..60 characters", p.ID)
	}
	p.Slot = strings.ToLower(strings.TrimSpace(p.Slot))
	if p.Slot != "inspector" && p.Slot != "settings" {
		return fmt.Errorf("panel %q slot must be \"inspector\" or \"settings\"", p.ID)
	}
	p.Entry = filepath.ToSlash(strings.TrimSpace(p.Entry))
	if p.Entry == "" || strings.HasPrefix(p.Entry, "/") || strings.Contains(p.Entry, "..") || strings.Contains(p.Entry, "://") {
		return fmt.Errorf("panel %q entry %q must be a safe relative path", p.ID, p.Entry)
	}
	return nil
}

// DiscoverExtensionManifests scans ~/.hoxi/extensions/*/manifest.json and
// <directory>/.hoxi/extensions/*/manifest.json, returning validated manifests
// alongside structured validation diagnostics for malformed packages.
func DiscoverExtensionManifests(directory string) ([]ExtensionManifest, []map[string]string) {
	var roots []string
	if home := strings.TrimSpace(hoxipaths.Home()); home != "" {
		roots = append(roots, filepath.Join(home, "extensions"))
	}
	if dir := strings.TrimSpace(directory); dir != "" {
		roots = append(roots, filepath.Join(dir, ".hoxi", "extensions"))
	}

	byID := make(map[string]ExtensionManifest)
	var diags []map[string]string
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			mPath := filepath.Join(root, entry.Name(), "manifest.json")
			raw, rErr := os.ReadFile(mPath)
			if rErr != nil {
				continue
			}
			m, vErr := ValidateExtensionManifest(raw)
			if vErr != nil {
				diags = append(diags, map[string]string{
					"path":  mPath,
					"error": vErr.Error(),
				})
				continue
			}
			byID[m.ID] = m
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]ExtensionManifest, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out, diags
}

func (g *Gateway) registerDeclarativeExtensionRoutes() {
	if g == nil || g.mux == nil {
		return
	}
	g.mux.HandleFunc("/api/hoxi/extensions/schema", func(w http.ResponseWriter, r *http.Request) {
		if isRemoteRequest(r) {
			writeJSONError(w, http.StatusForbidden, "extension schema is desktop-only")
			return
		}
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
			return
		}
		writeJSON(w, http.StatusOK, ExtensionManifestJSONSchema())
	})
	g.mux.HandleFunc("/api/hoxi/extensions/manifests", func(w http.ResponseWriter, r *http.Request) {
		if isRemoteRequest(r) {
			writeJSONError(w, http.StatusForbidden, "extensions management is desktop-only")
			return
		}
		switch r.Method {
		case http.MethodGet:
			dir := strings.TrimSpace(r.URL.Query().Get("directory"))
			if dir == "" {
				dir = g.rootDir
			}
			manifests, errs := DiscoverExtensionManifests(dir)
			writeJSON(w, http.StatusOK, map[string]any{
				"schemaVersion": ExtensionSchemaVersion,
				"manifests":     manifests,
				"errors":        errs,
			})
		case http.MethodPost:
			raw, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "failed to read manifest body")
				return
			}
			m, vErr := ValidateExtensionManifest(raw)
			if vErr != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{
					"valid": false,
					"error": vErr.Error(),
				})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"valid":    true,
				"manifest": m,
			})
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "GET or POST required")
		}
	})
}

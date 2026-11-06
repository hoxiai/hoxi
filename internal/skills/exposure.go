package skills

import (
	"context"
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
)

// Exposure says how a discovered skill reaches the model. The skills list
// route, explicit selection and the OpenCode plugin's catalog rewrite all read
// this one value, so the UI, the model's catalog and execution checks agree.
const (
	// ExposureAuto lists the skill with its description in the model catalog.
	ExposureAuto = "auto"
	// ExposureOnDemand lists the skill by name only; its description and body
	// load when the user names or selects it.
	ExposureOnDemand = "on-demand"
	// ExposureUnavailable hides the skill from the model and refuses loading.
	ExposureUnavailable = "unavailable"
)

// Exposure reason codes; the web UI translates them.
const (
	reasonHoxiManaged  = "hoxi-managed"
	reasonClientSynced = "client-synced"
	reasonMissingTools = "missing-tools"
	reasonConfigDenied = "config-denied"
	reasonConfigPinned = "config-pinned"
)

// generalHoxiSkills are bundled Hoxi skills that apply to any project. Every
// other skill in the Hoxi-managed directory (bundled trading skills,
// marketplace and Work extension skills) serves one business workflow.
var generalHoxiSkills = map[string]bool{"skill-authoring": true}

// mcpToolRefRe matches MCP tool names in the mcp__<server>__<tool> form that
// some clients use. A skill that calls such tools depends on that server.
var mcpToolRefRe = regexp.MustCompile(`\bmcp__([A-Za-z0-9-]+(?:_[A-Za-z0-9-]+)*?)__`)

type skillRule struct {
	pattern string
	action  string
}

// engineSkillConfig is the part of the effective engine config that decides
// skill exposure: configured MCP servers and permission.skill rules in order.
type engineSkillConfig struct {
	mcpServers map[string]bool
	rules      []skillRule
}

type exposureDecision struct {
	exposure     string
	reason       string
	missingTools []string
}

// classifyExposure decides a skill's exposure from, in order: a permission
// rule that denies it, an exact-name permission rule that keeps it, MCP tools
// it needs but the runtime lacks, then where it was installed. Skills with no
// signal stay in the automatic catalog, so nothing is dropped silently.
func classifyExposure(cfg engineSkillConfig, hoxiDir, surface, name, location, content string) exposureDecision {
	action, pinned := cfg.ruleFor(name)
	if action == "deny" {
		return exposureDecision{exposure: ExposureUnavailable, reason: reasonConfigDenied}
	}
	if pinned {
		return exposureDecision{exposure: ExposureAuto, reason: reasonConfigPinned}
	}
	if missing := missingMCPTools(content, cfg.mcpServers); len(missing) > 0 {
		return exposureDecision{exposure: ExposureUnavailable, reason: reasonMissingTools, missingTools: missing}
	}
	if surface != "work" && location != "" && hoxiDir != "" && isWithinDir(hoxiDir, location) && !generalHoxiSkills[name] {
		return exposureDecision{exposure: ExposureOnDemand, reason: reasonHoxiManaged}
	}
	if isClientSyncedSkill(location, name) {
		return exposureDecision{exposure: ExposureOnDemand, reason: reasonClientSynced}
	}
	return exposureDecision{exposure: ExposureAuto}
}

// ruleFor evaluates permission.skill like the engine: the last matching rule
// wins. pinned reports an exact-name allow/ask rule, the user's way to keep an
// on-demand skill in the automatic catalog.
func (c engineSkillConfig) ruleFor(name string) (action string, pinned bool) {
	for _, rule := range c.rules {
		if wildcardMatch(rule.pattern, name) {
			action = rule.action
			pinned = rule.pattern == name && rule.action != "deny"
		}
	}
	return action, pinned
}

// wildcardMatch implements the engine's glob subset for rule patterns:
// "*" matches any run of characters and "?" one character.
func wildcardMatch(pattern, value string) bool {
	if pattern == "*" {
		return true
	}
	var b strings.Builder
	b.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	return err == nil && re.MatchString(value)
}

// missingMCPTools lists the mcp__<server>__* tool families a skill body calls
// whose server is not configured in the runtime.
func missingMCPTools(content string, servers map[string]bool) []string {
	seen := make(map[string]bool)
	var missing []string
	for _, m := range mcpToolRefRe.FindAllStringSubmatch(content, -1) {
		server := m[1]
		key := strings.ToLower(server)
		if seen[key] || servers[key] {
			continue
		}
		seen[key] = true
		missing = append(missing, "mcp__"+server+"__*")
	}
	sort.Strings(missing)
	return missing
}

// isClientSyncedSkill reports a skill that another client's account sync
// placed under <root>/skills/synced/<bucket>/<skill>/SKILL.md and lists in
// the bucket's manifest.json. Skills the user put in the same root directly
// are not matched.
func isClientSyncedSkill(location, name string) bool {
	if location == "" || strings.HasPrefix(location, "<") {
		return false
	}
	bucket := filepath.Dir(filepath.Dir(location))
	if filepath.Base(filepath.Dir(bucket)) != "synced" {
		return false
	}
	file, err := os.Open(filepath.Join(bucket, "manifest.json"))
	if err != nil {
		return false
	}
	defer file.Close()
	var manifest struct {
		Skills []struct {
			Name    string `json:"name"`
			SkillID string `json:"skillId"`
		} `json:"skills"`
	}
	if json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&manifest) != nil {
		return false
	}
	for _, s := range manifest.Skills {
		if s.Name == name || s.SkillID == name {
			return true
		}
	}
	return false
}

// fetchEngineSkillConfig reads the effective engine config for directory.
// Any failure yields an empty config: no MCP servers and no rules.
func (s *Service) fetchEngineSkillConfig(ctx context.Context, directory string) engineSkillConfig {
	cfg := engineSkillConfig{mcpServers: map[string]bool{}}
	if !s.openCode.Available() {
		return cfg
	}
	ctx, cancel := context.WithTimeout(ctx, upstreamSkillTimeout)
	defer cancel()
	status, body, err := s.openCode.Raw(ctx, http.MethodGet, "/config", url.Values{"directory": {directory}}, nil)
	if err != nil || status < 200 || status >= 300 || len(body) > maxUpstreamSkillBytes {
		return cfg
	}
	parsed, err := parseEngineSkillConfig(body)
	if err != nil {
		return cfg
	}
	return parsed
}

func parseEngineSkillConfig(body []byte) (engineSkillConfig, error) {
	cfg := engineSkillConfig{mcpServers: map[string]bool{}}
	var doc struct {
		MCP        map[string]json.RawMessage `json:"mcp"`
		Permission json.RawMessage            `json:"permission"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return cfg, err
	}
	for name, raw := range doc.MCP {
		var entry struct {
			Enabled *bool `json:"enabled"`
		}
		if json.Unmarshal(raw, &entry) == nil && entry.Enabled != nil && !*entry.Enabled {
			continue
		}
		cfg.mcpServers[strings.ToLower(name)] = true
	}
	rules, err := parseSkillRules(doc.Permission)
	if err != nil {
		return cfg, err
	}
	cfg.rules = rules
	return cfg, nil
}

// parseSkillRules extracts permission.skill (and a top-level string or "*"
// rule that applies to it) in document order, which decides the engine's
// last-match-wins result.
func parseSkillRules(raw json.RawMessage) ([]skillRule, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var whole string
	if json.Unmarshal(raw, &whole) == nil {
		return []skillRule{{pattern: "*", action: whole}}, nil
	}
	keys, values, err := orderedObject(raw)
	if err != nil {
		return nil, err
	}
	var rules []skillRule
	for i, key := range keys {
		if key != "*" && key != "skill" {
			continue
		}
		var action string
		if json.Unmarshal(values[i], &action) == nil {
			rules = append(rules, skillRule{pattern: "*", action: action})
			continue
		}
		if key != "skill" {
			continue
		}
		patterns, actions, err := orderedObject(values[i])
		if err != nil {
			return nil, err
		}
		for j, pattern := range patterns {
			if json.Unmarshal(actions[j], &action) == nil {
				rules = append(rules, skillRule{pattern: pattern, action: action})
			}
		}
	}
	return rules, nil
}

// orderedObject decodes a JSON object's keys and raw values in document order.
func orderedObject(raw json.RawMessage) ([]string, []json.RawMessage, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, nil, errors.New("expected a JSON object")
	}
	var keys []string
	var values []json.RawMessage
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, nil, errors.New("expected an object key")
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, nil, err
		}
		keys = append(keys, key)
		values = append(values, value)
	}
	return keys, values, nil
}

// unavailableMessage is the error returned when a caller selects a skill the
// runtime cannot run.
func unavailableMessage(d SkillDescriptor) string {
	switch d.ExposureReason {
	case reasonMissingTools:
		return fmt.Sprintf("skill %q needs tools this runtime does not provide: %s", d.Name, strings.Join(d.MissingTools, ", "))
	case reasonConfigDenied:
		return fmt.Sprintf("skill %q is disabled by permission.skill in the engine configuration", d.Name)
	default:
		return fmt.Sprintf("skill %q is unavailable in this runtime", d.Name)
	}
}

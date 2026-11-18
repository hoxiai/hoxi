package hoxiconfig

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// Version 1 deliberately exposes only implemented native capabilities. In
// particular, providers, credentials, plugins and approval modes are not project
// settings. Unknown fields are errors rather than silently ignored typos.
func validate(root string, doc map[string]any) error {
	for key, value := range doc {
		switch key {
		case "$schema":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("$schema must be a string")
			}
		case "version":
			if number, ok := value.(float64); !ok || number != 1 {
				return fmt.Errorf("version must be 1")
			}
		case "permission":
			perm, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("permission must be an object")
			}
			for name, action := range perm {
				if !permissionNames[name] {
					return fmt.Errorf("unsupported permission %q", name)
				}
				if name == "skill" || name == "task" {
					if rules, ok := action.(map[string]any); ok {
						for _, rule := range rules {
							if !validAction(rule) {
								return fmt.Errorf("permission.%s rules must be allow, ask or deny", name)
							}
						}
						continue
					}
				}
				if !validAction(action) {
					return fmt.Errorf("permission.%s must be allow, ask or deny", name)
				}
			}
		case "skills":
			cfg, ok := value.(map[string]any)
			if !ok || len(cfg) != 1 {
				return fmt.Errorf("skills must contain only paths")
			}
			paths, ok := cfg["paths"].([]any)
			if !ok {
				return fmt.Errorf("skills.paths must be an array")
			}
			for _, raw := range paths {
				p, ok := raw.(string)
				if !ok || strings.TrimSpace(p) == "" {
					return fmt.Errorf("skills.paths entries must be non-empty strings")
				}
				if err := validateSkillPath(root, p); err != nil {
					return err
				}
			}
		case "mcp":
			servers, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("mcp must be an object")
			}
			seen := map[string]bool{}
			for name, raw := range servers {
				canonical := strings.ToLower(strings.TrimSpace(name))
				if name != strings.TrimSpace(name) || seen[canonical] {
					return fmt.Errorf("mcp server names must be trimmed and unique ignoring case")
				}
				seen[canonical] = true
				if err := validateMCP(name, raw); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unsupported field %q", key)
		}
	}
	return nil
}

var permissionNames = map[string]bool{
	"*": true, "read": true, "glob": true, "grep": true, "edit": true,
	"write": true, "multiedit": true, "apply_patch": true, "bash": true,
	"webfetch": true, "skill": true, "task": true, "question": true,
	"todoread": true, "todowrite": true, "goal_report": true,
	"code_search": true, "symbol_inspect": true, "impact_analysis": true,
	"hoxi_code_index": true, "mcp_list_tools": true, "mcp_call_tool": true,
	"hoxi_browser": true, "hoxi_computer": true, "hoxi_image": true,
	"hoxi_tools": true, "hoxi_data": true,
}

func validAction(v any) bool {
	s, ok := v.(string)
	return ok && (s == "allow" || s == "ask" || s == "deny")
}

func validateSkillPath(root, p string) error {
	if strings.ContainsAny(p, "\x00*?[]") || strings.HasPrefix(p, "~") {
		return fmt.Errorf("skills.paths must use literal project paths")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	if !within(root, filepath.Clean(p)) {
		return fmt.Errorf("skills.paths must stay within the project")
	}
	return nil
}

func validateMCP(name string, raw any) error {
	cfg, ok := raw.(map[string]any)
	if !ok || strings.TrimSpace(name) == "" {
		return fmt.Errorf("mcp server must have a non-empty name and object definition")
	}
	for key, value := range cfg {
		switch key {
		case "type":
			if typ, ok := value.(string); !ok || (typ != "local" && typ != "remote") {
				return fmt.Errorf("mcp.%s.type must be local or remote", name)
			}
		case "enabled":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("mcp.%s.enabled must be boolean", name)
			}
		case "command":
			args, ok := value.([]any)
			if !ok || len(args) == 0 {
				return fmt.Errorf("mcp.%s.command must be a non-empty string array", name)
			}
			for _, arg := range args {
				if s, ok := arg.(string); !ok || strings.ContainsRune(s, 0) {
					return fmt.Errorf("mcp.%s.command must contain strings without NUL", name)
				}
			}
			if strings.TrimSpace(args[0].(string)) == "" {
				return fmt.Errorf("mcp.%s.command executable must not be empty", name)
			}
		case "environment":
			env, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("mcp.%s.environment must be a string map", name)
			}
			for k, v := range env {
				if s, ok := v.(string); !ok || k == "" || strings.ContainsAny(k, "=\x00") || strings.ContainsRune(s, 0) {
					return fmt.Errorf("mcp.%s.environment contains an invalid entry", name)
				}
			}
		case "url":
			s, ok := value.(string)
			u, err := url.Parse(s)
			if !ok || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
				return fmt.Errorf("mcp.%s.url must be an HTTP(S) URL without credentials", name)
			}
		default:
			return fmt.Errorf("unsupported mcp.%s field %q (keep credentials in local settings)", name, key)
		}
	}
	if len(cfg) == 1 && cfg["enabled"] == false {
		return nil
	}
	switch cfg["type"] {
	case "local":
		if cfg["command"] == nil || cfg["url"] != nil {
			return fmt.Errorf("mcp.%s local server requires command and cannot have url", name)
		}
	case "remote":
		if cfg["url"] == nil || cfg["command"] != nil || cfg["environment"] != nil {
			return fmt.Errorf("mcp.%s remote server requires url and cannot have command/environment", name)
		}
	default:
		return fmt.Errorf("mcp.%s requires type", name)
	}
	return nil
}

package shellpolicy

import (
	"encoding/json"
	"strings"
)

// HasRiskySearchSelector checks filename selectors in structured tool calls.
// Grep's pattern matches file contents. Glob's pattern selects files.
func HasRiskySearchSelector(tool string, args map[string]interface{}) bool {
	keys := []string{"glob", "Glob", "include", "Include"}
	tool = strings.ToLower(strings.TrimSpace(tool))
	if tool != "grep" && tool != "search_file_content" {
		keys = append(keys, "pattern", "Pattern")
	}
	for _, key := range keys {
		for _, value := range stringList(args[key]) {
			if strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
				var decoded string
				if json.Unmarshal([]byte(value), &decoded) != nil {
					return true
				}
				value = strings.TrimSpace(decoded)
			}
			p := strings.ReplaceAll(value, `\`, "/")
			if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") || (len(p) >= 2 && p[1] == ':') ||
				strings.Contains("/"+p+"/", "/../") || isSecretFilePathLexical(p) {
				return true
			}
		}
	}
	return false
}

// Check original spellings before shared permission helpers trim or resolve
// them. Each narrow read exception supplies its own invalid-path predicate.
func hasAmbiguousPermissionPathInput(item map[string]interface{}, invalid func(string) bool) bool {
	meta := itemMetadata(item)
	var check func(interface{}) bool
	check = func(value interface{}) bool {
		switch v := value.(type) {
		case string:
			return invalid(v)
		case []string:
			for _, p := range v {
				if check(p) {
					return true
				}
			}
		case []interface{}:
			for _, p := range v {
				if check(p) {
					return true
				}
			}
		case map[string]interface{}:
			for _, key := range append([]string{"movePath"}, toolArgPathKeys...) {
				if check(v[key]) {
					return true
				}
			}
		}
		return false
	}
	if perm := permissionName(item); perm == "read" || perm == "external_directory" {
		if check(item["patterns"]) || check(item["pattern"]) || check(item["always"]) {
			return true
		}
	}
	for _, key := range []string{"filepath", "filePath", "path", "target", "parentDir", "dir", "directories", "files"} {
		if check(meta[key]) {
			return true
		}
	}
	args := meta["args"]
	if raw, ok := args.(string); ok {
		var parsed map[string]interface{}
		if json.Unmarshal([]byte(raw), &parsed) == nil {
			args = parsed
		}
	}
	return check(args)
}

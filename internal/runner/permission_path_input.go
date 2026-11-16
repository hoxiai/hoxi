package runner

import "encoding/json"

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

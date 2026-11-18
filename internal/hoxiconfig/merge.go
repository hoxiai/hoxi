package hoxiconfig

// Overlay returns an independent effective document. Project values win;
// objects merge recursively, arrays/scalars replace. Inputs are never modified
// and this function never persists the effective configuration.
func Overlay(base, project map[string]any) map[string]any {
	out := cloneMap(base)
	for key, value := range project {
		if key == "$schema" || key == "version" {
			continue
		}
		mergeValue(out, key, value)
	}
	return out
}

func mergeValue(out map[string]any, key string, value any) {
	if object, ok := value.(map[string]any); ok {
		previous, _ := out[key].(map[string]any)
		next := cloneMap(previous)
		for k, v := range object {
			mergeValue(next, k, v)
		}
		out[key] = next
	} else {
		out[key] = clone(value)
	}
}

func cloneMap(src map[string]any) map[string]any {
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = clone(v)
	}
	return out
}

func clone(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneMap(v)
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = clone(v[i])
		}
		return out
	default:
		return v
	}
}

// ToolAction is deliberately scalar-only (except task/skill, which their
// existing domain loaders evaluate). Project allow never grants approval: the
// host policy must still approve the operation. A project can only tighten it.
func ToolAction(doc map[string]any, name string) string {
	perm, _ := doc["permission"].(map[string]any)
	category := name
	switch name {
	case "write", "edit", "multiedit", "apply_patch":
		category = "edit"
	}
	action, _ := perm["*"].(string)
	if v, ok := perm[category].(string); ok {
		action = v
	}
	if v, ok := perm[name].(string); ok {
		action = v
	}
	return action
}

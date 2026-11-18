package sessionstore

import (
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

func EqualMessageInfo(a, b MessageInfo) bool { return reflect.DeepEqual(a, b) }
func EqualMessagePart(a, b MessagePart) bool { return reflect.DeepEqual(a, b) }

func cloneAnyValue(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		return CloneGenericMap(val)
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, item := range val {
			out[i] = cloneAnyValue(item)
		}
		return out
	case []string:
		out := make([]string, len(val))
		copy(out, val)
		return out
	default:
		return val
	}
}

func CloneGenericMap(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = cloneAnyValue(v)
	}
	return out
}

func CloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func CloneMessageInfo(info MessageInfo) MessageInfo {
	out := info
	if info.Tokens != nil {
		tok := *info.Tokens
		out.Tokens = &tok
	}
	if info.Error != nil {
		errCopy := *info.Error
		if info.Error.Data != nil {
			dataCopy := *info.Error.Data
			if dataCopy.Upstream != nil {
				upCopy := *dataCopy.Upstream
				upCopy.Headers = CloneStringMap(upCopy.Headers)
				dataCopy.Upstream = &upCopy
			}
			errCopy.Data = &dataCopy
		}
		out.Error = &errCopy
	}
	return out
}

func ClonePart(p MessagePart) MessagePart {
	out := p
	if p.Time != nil {
		t := *p.Time
		out.Time = &t
	}
	out.Metadata = CloneGenericMap(p.Metadata)
	if p.State != nil {
		st := *p.State
		st.Input = CloneGenericMap(p.State.Input)
		st.Metadata = CloneGenericMap(p.State.Metadata)
		if p.State.Time != nil {
			t := *p.State.Time
			st.Time = &t
		}
		out.State = &st
	}
	return out
}

func CloneTurns(turns []MessageTurn) []MessageTurn {
	if turns == nil {
		return nil
	}
	out := make([]MessageTurn, len(turns))
	for i, t := range turns {
		var parts []MessagePart
		if t.Parts != nil {
			parts = make([]MessagePart, len(t.Parts))
			for j, p := range t.Parts {
				parts[j] = ClonePart(p)
			}
		}
		out[i] = MessageTurn{
			Info:  CloneMessageInfo(t.Info),
			Parts: parts,
		}
	}
	return out
}

func CloneRecord(r SessionRecord) SessionRecord {
	out := r
	out.Turns = CloneTurns(r.Turns)
	out.RevertedTurns = CloneTurns(r.RevertedTurns)
	if r.Revert != nil {
		rev := *r.Revert
		out.Revert = &rev
	}
	out.Snapshots = CloneStringMap(r.Snapshots)
	if r.OpenCodeIDs != nil {
		out.OpenCodeIDs = make([]string, len(r.OpenCodeIDs))
		copy(out.OpenCodeIDs, r.OpenCodeIDs)
	}
	if r.OpenCodeChildIDs != nil {
		out.OpenCodeChildIDs = make([]string, len(r.OpenCodeChildIDs))
		copy(out.OpenCodeChildIDs, r.OpenCodeChildIDs)
	}
	out.OpenCodeDirectories = CloneStringMap(r.OpenCodeDirectories)
	return out
}

const maxNormalizedPathCacheEntries = 2048

var (
	normalizedComparePathCache     sync.Map
	normalizedComparePathCacheSize atomic.Int64
)

func NormalizeComparePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if cached, ok := normalizedComparePathCache.Load(p); ok {
		return cached.(string)
	}
	raw := p
	resolvedOK := false
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
		resolvedOK = true
	}
	cleaned := filepath.ToSlash(filepath.Clean(p))
	cleaned = strings.TrimRight(cleaned, "/")
	if runtime.GOOS == "windows" {
		cleaned = strings.ToLower(cleaned)
	}
	if resolvedOK {
		if _, loaded := normalizedComparePathCache.LoadOrStore(raw, cleaned); !loaded {
			if normalizedComparePathCacheSize.Add(1) > maxNormalizedPathCacheEntries {
				normalizedComparePathCache.Range(func(k, _ any) bool {
					normalizedComparePathCache.Delete(k)
					return true
				})
				normalizedComparePathCacheSize.Store(1)
				normalizedComparePathCache.Store(raw, cleaned)
			}
		}
	}
	return cleaned
}

func cloneRecord(r SessionRecord) SessionRecord  { return CloneRecord(r) }
func cloneTurns(t []MessageTurn) []MessageTurn   { return CloneTurns(t) }
func clonePart(p MessagePart) MessagePart        { return ClonePart(p) }
func cloneMessageInfo(i MessageInfo) MessageInfo { return CloneMessageInfo(i) }
func normalizeComparePath(p string) string       { return NormalizeComparePath(p) }

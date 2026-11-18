// Package hoxiconfig loads the repository-shared Hoxi configuration.
//
// hoxi.json is intentionally separate from .hoxi/models.json: the former is
// safe to commit and describes project behaviour, while the latter may contain
// provider credentials and machine-local state.
package hoxiconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	FileName       = "hoxi.json"
	MaxConfigBytes = 1 << 20
)

var ErrInvalidConfig = errors.New("invalid hoxi.json")

// Load reads <root>/hoxi.json. A missing file is not an error. JSONC comments
// and trailing commas are accepted so the file is convenient to edit by hand.
func Load(root string) (map[string]any, error) {
	root = cleanRoot(root)
	if root == "" {
		return nil, nil
	}
	path := filepath.Join(root, FileName)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Size() > MaxConfigBytes {
		return nil, fmt.Errorf("%w: %s must be a regular file of at most %d bytes", ErrInvalidConfig, path, MaxConfigBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if err != nil || len(raw) > MaxConfigBytes {
		return nil, fmt.Errorf("%w: cannot read %s within size limit", ErrInvalidConfig, path)
	}
	cleaned, err := sanitizeJSONC(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %s", ErrInvalidConfig, path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(cleaned, &doc); err != nil || doc == nil {
		return nil, fmt.Errorf("%w: %s: expected a JSON object", ErrInvalidConfig, path)
	}
	if err := validate(root, doc); err != nil {
		return nil, fmt.Errorf("%w: %s: %s", ErrInvalidConfig, path, err)
	}
	return doc, nil
}

// SkillPaths returns configured project skill roots. Relative paths are
// resolved against the project root; paths outside that root are ignored.
// Built-in .hoxi/skills discovery remains enabled independently of this list.
func SkillPaths(root string, doc map[string]any) []string {
	root = cleanRoot(root)
	if root == "" || doc == nil {
		return nil
	}
	skills, _ := doc["skills"].(map[string]any)
	paths, _ := skills["paths"].([]any)
	seen := make(map[string]bool)
	result := make([]string, 0, len(paths))
	for _, raw := range paths {
		value, ok := raw.(string)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		path := strings.TrimSpace(value)
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		path = cleanRoot(path)
		if path == "" || !within(root, path) || seen[path] {
			continue
		}
		seen[path] = true
		result = append(result, path)
	}
	return result
}

func cleanRoot(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	return filepath.Clean(abs)
}

func within(root, path string) bool {
	if root == path {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

// SanitizeJSONC removes comments and trailing commas outside string literals,
// returning an error if a block comment is left unterminated.
func SanitizeJSONC(raw []byte) ([]byte, error) {
	return sanitizeJSONC(raw)
}

// sanitizeJSONC removes comments and trailing commas outside string literals.
func sanitizeJSONC(raw []byte) ([]byte, error) {
	withoutComments := make([]byte, 0, len(raw))
	inString := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			withoutComments = append(withoutComments, c)
			if c == '\\' && i+1 < len(raw) {
				i++
				withoutComments = append(withoutComments, raw[i])
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			withoutComments = append(withoutComments, c)
			continue
		}
		if c == '/' && i+1 < len(raw) && raw[i+1] == '/' {
			i += 2
			for i < len(raw) && raw[i] != '\n' {
				i++
			}
			if i < len(raw) {
				withoutComments = append(withoutComments, '\n')
			}
			continue
		}
		if c == '/' && i+1 < len(raw) && raw[i+1] == '*' {
			withoutComments = append(withoutComments, ' ')
			i += 2
			for i+1 < len(raw) && !(raw[i] == '*' && raw[i+1] == '/') {
				i++
			}
			if i+1 < len(raw) {
				i++
			} else {
				return nil, errors.New("unterminated block comment")
			}
			continue
		}
		withoutComments = append(withoutComments, c)
	}

	result := make([]byte, 0, len(withoutComments))
	inString = false
	for i := 0; i < len(withoutComments); i++ {
		c := withoutComments[i]
		if inString {
			result = append(result, c)
			if c == '\\' && i+1 < len(withoutComments) {
				i++
				result = append(result, withoutComments[i])
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			result = append(result, c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(withoutComments) && strings.ContainsRune(" \t\r\n", rune(withoutComments[j])) {
				j++
			}
			if j < len(withoutComments) && (withoutComments[j] == '}' || withoutComments[j] == ']') {
				continue
			}
		}
		result = append(result, c)
	}
	return bytes.TrimSpace(result), nil
}

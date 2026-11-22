package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/llm"
)

// ErrInvalidMediaRole aliases llm.ErrInvalidMediaRole for callers in provider tests.
var ErrInvalidMediaRole = llm.ErrInvalidMediaRole

func (s *Service) mediaRolesFilePath() string {
	if p := hoxipaths.Join("media-roles.json"); p != "" {
		return p
	}
	if root := strings.TrimSpace(s.rootDir); root != "" {
		return filepath.Join(root, ".hoxi", "media-roles.json")
	}
	return ""
}

func validateAndNormalizeMediaRoleSpec(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.EqualFold(trimmed, "auto") {
		return "", nil
	}
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("%w: %q must use <providerID>/<modelID> format", ErrInvalidMediaRole, raw)
	}
	pid := strings.TrimSpace(parts[0])
	mid := strings.TrimSpace(parts[1])
	if pid == "" || mid == "" {
		return "", fmt.Errorf("%w: %q must include non-empty providerID and modelID", ErrInvalidMediaRole, raw)
	}
	if pid == AutoProviderID {
		return "", fmt.Errorf("%w: %s cannot be pinned as a media role model", ErrInvalidMediaRole, AutoProviderID)
	}
	if strings.ContainsAny(pid, " \t\r\n") || strings.ContainsAny(mid, " \t\r\n") {
		return "", fmt.Errorf("%w: %q must not contain whitespace", ErrInvalidMediaRole, raw)
	}
	return pid + "/" + mid, nil
}

func normalizeMediaRoleSpec(raw string) string {
	norm, err := validateAndNormalizeMediaRoleSpec(raw)
	if err != nil {
		return ""
	}
	return norm
}

func splitMediaRoleSpec(raw string) (string, string) {
	norm := normalizeMediaRoleSpec(raw)
	if norm == "" {
		return "", ""
	}
	parts := strings.SplitN(norm, "/", 2)
	return parts[0], parts[1]
}

// LoadMediaRolePrefs reads optional user-configured default media models from
// ~/.hoxi/media-roles.json, backing up corrupt files instead of silently clobbering.
func (s *Service) LoadMediaRolePrefs() llm.MediaRolePrefs {
	if s == nil {
		return llm.MediaRolePrefs{}
	}
	s.mediaRolesMu.Lock()
	defer s.mediaRolesMu.Unlock()
	return s.loadMediaRolePrefsLocked()
}

func (s *Service) loadMediaRolePrefsLocked() llm.MediaRolePrefs {
	path := s.mediaRolesFilePath()
	if path == "" {
		return llm.MediaRolePrefs{}
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		return llm.MediaRolePrefs{}
	}
	var prefs llm.MediaRolePrefs
	if err := json.Unmarshal(raw, &prefs); err != nil {
		_ = os.Rename(path, path+".corrupt-"+strconv.FormatInt(time.Now().UnixMilli(), 10))
		return llm.MediaRolePrefs{}
	}
	prefs.ImageModel = normalizeMediaRoleSpec(prefs.ImageModel)
	prefs.VideoModel = normalizeMediaRoleSpec(prefs.VideoModel)
	prefs.SpeechModel = normalizeMediaRoleSpec(prefs.SpeechModel)
	prefs.TranscribeModel = normalizeMediaRoleSpec(prefs.TranscribeModel)
	return prefs
}

// UpdateMediaRolePrefs merges non-nil role updates into ~/.hoxi/media-roles.json
// using atomic write-and-rename. Empty string clears a role back to auto-select;
// malformed specs or unknown keys return ErrInvalidMediaRole without mutating state.
func (s *Service) UpdateMediaRolePrefs(patch map[string]*string) (llm.MediaRolePrefs, error) {
	normalizedPatch := make(map[string]string, len(patch))
	for k, v := range patch {
		switch k {
		case "imageModel", "videoModel", "speechModel", "transcribeModel":
		default:
			return llm.MediaRolePrefs{}, fmt.Errorf("%w: unknown role field %q", ErrInvalidMediaRole, k)
		}
		if v == nil {
			continue
		}
		norm, err := validateAndNormalizeMediaRoleSpec(*v)
		if err != nil {
			return llm.MediaRolePrefs{}, err
		}
		normalizedPatch[k] = norm
	}

	s.mediaRolesMu.Lock()
	defer s.mediaRolesMu.Unlock()

	cur := s.loadMediaRolePrefsLocked()
	if v, ok := normalizedPatch["imageModel"]; ok {
		cur.ImageModel = v
	}
	if v, ok := normalizedPatch["videoModel"]; ok {
		cur.VideoModel = v
	}
	if v, ok := normalizedPatch["speechModel"]; ok {
		cur.SpeechModel = v
	}
	if v, ok := normalizedPatch["transcribeModel"]; ok {
		cur.TranscribeModel = v
	}

	path := s.mediaRolesFilePath()
	if path == "" {
		return cur, nil
	}
	if err := hoxipaths.WriteJSONAtomic(path, cur, 0o600); err != nil {
		return cur, err
	}
	return cur, nil
}

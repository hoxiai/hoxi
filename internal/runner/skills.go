package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type SkillSelection struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type userSkillOverride struct {
	originalText string
	selection    SkillSelection
}

func (s *Service) SetSkillValidator(fn func(ctx context.Context, directory, sessionID string, sel SkillSelection) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skillValidator = fn
}

// ResolveSessionContext returns the canonical runtime, working directory, and project root
// for a session ID (supporting both Hoxi-owned sessions and unadopted OpenCode sessions).
func (s *Service) ResolveSessionContext(ctx context.Context, sessionID, directory string) (runtime, effectiveDir, projectDir string, ok bool, err error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", "", "", false, nil
	}
	if rec, exists := s.store.Get(sessionID); exists {
		dir := rec.WorktreePath
		if dir == "" {
			dir = rec.ProjectDir
		}
		if directory != "" &&
			normalizeComparePath(directory) != normalizeComparePath(dir) &&
			normalizeComparePath(directory) != normalizeComparePath(rec.ProjectDir) {
			return "", "", "", true, errors.New("session directory does not match its sandbox")
		}
		rt := rec.Runtime
		if rt == "" {
			rt = "opencode"
		}
		return rt, dir, rec.ProjectDir, true, nil
	}

	// Check worktree binding for an unadopted OpenCode session.
	if s.worktrees != nil {
		if binding, bound, bErr := s.worktrees.BindingForSession(sessionID); bErr != nil {
			return "", "", "", true, bErr
		} else if bound && binding.Directory != "" {
			if directory != "" && normalizeComparePath(directory) != normalizeComparePath(binding.Directory) {
				return "", "", "", true, errors.New("session directory does not match its sandbox")
			}
			proj := s.worktrees.ForSession(sessionID).RootDir()
			if proj == "" {
				proj = binding.Directory
			}
			return "opencode", binding.Directory, proj, true, nil
		}
	}

	// Query OpenCode if available for an unadopted session.
	if s.openCode.Available() {
		var info SessionInfo
		if err := s.openCodeJSON(ctx, http.MethodGet, "/session/"+sessionID, directory, nil, &info); err == nil && info.ID == sessionID {
			dir := info.Directory
			if dir == "" {
				dir = directory
			}
			if dir == "" {
				dir = s.rootDir
			}
			if directory != "" && normalizeComparePath(directory) != normalizeComparePath(dir) {
				return "", "", "", true, errors.New("session directory does not match its sandbox")
			}
			return "opencode", dir, dir, true, nil
		}
	}

	return "", "", "", false, nil
}

func (s *Service) validateSkillSelection(ctx context.Context, directory, sessionID string, sel SkillSelection) error {
	key := strings.TrimSpace(sel.Key)
	name := strings.TrimSpace(sel.Name)
	if key == "" || name == "" || !strings.HasPrefix(key, "skl_") {
		return errors.New("invalid skillSelection: key and name are required")
	}
	s.mu.Lock()
	validator := s.skillValidator
	s.mu.Unlock()
	if validator != nil {
		return validator(ctx, directory, sessionID, SkillSelection{Key: key, Name: name})
	}
	return s.verifyOpenCodeSkillFallback(ctx, directory, SkillSelection{Key: key, Name: name})
}

func (s *Service) verifyOpenCodeSkillFallback(ctx context.Context, directory string, sel SkillSelection) error {
	var items []struct {
		Name     string `json:"name"`
		Location string `json:"location"`
	}
	if err := s.openCodeJSON(ctx, http.MethodGet, "/skill", directory, nil, &items); err != nil {
		return fmt.Errorf("failed to verify skill in OpenCode: %w", err)
	}
	cleanDir := canonicalizeRunnerPath(directory)
	foundName := false
	for _, item := range items {
		if strings.TrimSpace(item.Name) != sel.Name {
			continue
		}
		foundName = true
		loc := strings.TrimSpace(item.Location)
		if loc != "" && loc != "<built-in>" && !strings.HasPrefix(loc, "<") {
			cleaned := filepath.Clean(loc)
			if !filepath.IsAbs(cleaned) {
				cleaned = filepath.Join(cleanDir, cleaned)
			}
			if resolved := canonicalizeRunnerPath(cleaned); resolved != "" {
				loc = resolved
			}
		}
		sum := sha256.Sum256([]byte("opencode\x00" + cleanDir + "\x00" + sel.Name + "\x00" + loc))
		expectedKey := "skl_" + hex.EncodeToString(sum[:10])
		if expectedKey == sel.Key {
			return nil
		}
	}
	if foundName {
		return fmt.Errorf("skill %q source changed in the active directory; please re-select the skill", sel.Name)
	}
	return fmt.Errorf("skill %q is not available in the active directory", sel.Name)
}

func canonicalizeRunnerPath(p string) string {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(trimmed); err == nil {
		if abs, err := filepath.Abs(resolved); err == nil {
			return filepath.Clean(abs)
		}
		return filepath.Clean(resolved)
	}
	if abs, err := filepath.Abs(trimmed); err == nil {
		return filepath.Clean(abs)
	}
	if _, err := os.Stat(trimmed); err == nil {
		return filepath.Clean(trimmed)
	}
	return filepath.Clean(trimmed)
}

func formatSkillPromptInstruction(skillName, userText string) string {
	name := strings.TrimSpace(skillName)
	trimmed := strings.TrimSpace(userText)
	header := fmt.Sprintf(
		"Use the %q skill by calling the `skill` tool with `{\"name\": %q}` and follow its instructions for this request.",
		name,
		name,
	)
	if trimmed == "" {
		return header
	}
	return header + "\n\n" + trimmed
}

func (s *Service) recordUserSkillOverride(messageID, originalText string, sel *SkillSelection) {
	if sel == nil || strings.TrimSpace(messageID) == "" {
		return
	}
	s.userSkillByMsg.Store(messageID, userSkillOverride{
		originalText: originalText,
		selection: SkillSelection{
			Key:  strings.TrimSpace(sel.Key),
			Name: strings.TrimSpace(sel.Name),
		},
	})
}

func applySkillSelectionToTurnParts(
	sessionID string,
	userMsgID string,
	now int64,
	seq int64,
	userText string,
	sel *SkillSelection,
	storedParts []MessagePart,
	deliveryParts []MessagePart,
) ([]MessagePart, []MessagePart) {
	if sel == nil {
		return storedParts, deliveryParts
	}
	skillMeta := map[string]interface{}{
		"key":  strings.TrimSpace(sel.Key),
		"name": strings.TrimSpace(sel.Name),
	}

	// Ensure storedParts has a text part carrying the user's original text and hoxiSkill metadata.
	hasStoredText := false
	for i := range storedParts {
		if storedParts[i].Type == "text" {
			hasStoredText = true
			if storedParts[i].Metadata == nil {
				storedParts[i].Metadata = make(map[string]interface{})
			}
			storedParts[i].Metadata["hoxiSkill"] = skillMeta
			break
		}
	}
	if !hasStoredText {
		textPart := MessagePart{
			ID:        fmt.Sprintf("prt_u_%d_%d", now, seq),
			SessionID: sessionID,
			MessageID: userMsgID,
			Type:      "text",
			Text:      userText,
			Metadata: map[string]interface{}{
				"hoxiSkill": skillMeta,
			},
		}
		storedParts = append([]MessagePart{textPart}, storedParts...)
	}

	// Ensure deliveryParts has a text part formatted with the explicit native skill request.
	hasDeliveryText := false
	for i := range deliveryParts {
		if deliveryParts[i].Type == "text" {
			hasDeliveryText = true
			deliveryParts[i].Text = formatSkillPromptInstruction(sel.Name, deliveryParts[i].Text)
			break
		}
	}
	if !hasDeliveryText {
		deliveryPart := MessagePart{
			ID:        fmt.Sprintf("prt_u_%d_%d", now, seq),
			SessionID: sessionID,
			MessageID: userMsgID,
			Type:      "text",
			Text:      formatSkillPromptInstruction(sel.Name, userText),
		}
		deliveryParts = append([]MessagePart{deliveryPart}, deliveryParts...)
	}

	return storedParts, deliveryParts
}

func (s *Service) applyUserSkillOverride(sessionID string, part *MessagePart) {
	if part == nil || part.Type != "text" {
		return
	}
	if raw, ok := s.userSkillByMsg.Load(part.MessageID); ok {
		ov := raw.(userSkillOverride)
		part.Text = ov.originalText
		if part.Metadata == nil {
			part.Metadata = make(map[string]interface{})
		}
		part.Metadata["hoxiSkill"] = map[string]interface{}{
			"key":  ov.selection.Key,
			"name": ov.selection.Name,
		}
		return
	}
	// Also check if the store already recorded hoxiSkill metadata for this user message.
	if rec, ok := s.store.Get(sessionID); ok {
		for _, t := range rec.Turns {
			if t.Info.ID != part.MessageID {
				continue
			}
			for _, existing := range t.Parts {
				if existing.Type == "text" && existing.Metadata != nil && existing.Metadata["hoxiSkill"] != nil {
					if part.Metadata == nil {
						part.Metadata = make(map[string]interface{})
					}
					part.Metadata["hoxiSkill"] = existing.Metadata["hoxiSkill"]
					part.Text = existing.Text
					return
				}
			}
		}
	}
}

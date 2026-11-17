package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/opencode/ocstate"
	"github.com/hoxiai/hoxi/internal/sqlitecli"
	"github.com/hoxiai/hoxi/internal/subproc"
)

func legacyOpenCodeDBPaths() []string {
	var paths []string
	seen := make(map[string]bool)
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		clean := filepath.Clean(p)
		if seen[clean] {
			return
		}
		if fi, err := os.Stat(clean); err == nil && !fi.IsDir() && fi.Size() > 0 {
			seen[clean] = true
			paths = append(paths, clean)
		}
	}
	add(hoxipaths.DBPath())
	add(hoxipaths.EngineLegacyDBPath())
	for _, ext := range ocstate.ExternalDBPaths() {
		add(ext)
	}
	return paths
}

func (s *Service) migrateDefaultOpenCodeDBs() {
	for _, dbPath := range legacyOpenCodeDBPaths() {
		s.MigrateOpenCodeDB(dbPath)
	}
}

type ocLegacySessionMeta struct {
	ID          string `json:"id"`
	Directory   string `json:"directory"`
	Title       string `json:"title"`
	TimeCreated int64  `json:"timeCreated"`
	TimeUpdated int64  `json:"timeUpdated"`
	Model       string `json:"model"`
}

// MigrateOpenCodeDB imports unadopted legacy OpenCode SQLite sessions into runner.Store
// on the native Go runtime so historical conversations work seamlessly without `opencode serve`.
func (s *Service) MigrateOpenCodeDB(dbPath string) int {
	if s == nil || s.store == nil || strings.TrimSpace(dbPath) == "" {
		return 0
	}
	if fi, err := os.Stat(dbPath); err != nil || fi.IsDir() || fi.Size() == 0 {
		return 0
	}
	listSQL := `
SELECT COALESCE(json_group_array(json_object(
  'id', s.id,
  'directory', COALESCE(s.directory, ''),
  'title', COALESCE(s.title, ''),
  'timeCreated', COALESCE(s.time_created, 0),
  'timeUpdated', COALESCE(s.time_updated, 0),
  'model', COALESCE(s.model, '')
)), '[]')
FROM (
  SELECT id, directory, title, time_created, time_updated, model
  FROM session
  WHERE parent_id IS NULL
    AND (metadata IS NULL OR metadata NOT LIKE '%"hoxiConversation"%')
  ORDER BY time_updated DESC
  LIMIT 300
) s;`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := subproc.CommandContext(ctx, sqlitecli.Executable(), "-readonly", "-cmd", ".timeout 3000", dbPath, listSQL)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return 0
	}
	var rows []ocLegacySessionMeta
	if json.Unmarshal(bytes.TrimSpace(out), &rows) != nil || len(rows) == 0 {
		return 0
	}
	migrated := 0
	tombstones := s.loadDeletedOpenCodeIDs()
	for _, row := range rows {
		id := strings.TrimSpace(row.ID)
		if id == "" || tombstones[id] {
			continue
		}
		if _, exists := s.store.Meta(id); exists || s.IsManagedOpenCodeSession(id) {
			continue
		}
		if s.migrateOneOpenCodeSessionFromDB(dbPath, id) {
			migrated++
		}
	}
	return migrated
}

func (s *Service) tryMigrateSingleOpenCodeSession(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || !strings.HasPrefix(sessionID, "ses_") {
		return false
	}
	if s.loadDeletedOpenCodeIDs()[sessionID] {
		return false
	}
	for _, dbPath := range legacyOpenCodeDBPaths() {
		if s.migrateOneOpenCodeSessionFromDB(dbPath, sessionID) {
			return true
		}
	}
	return false
}

func (s *Service) deletedOpenCodeTombstonePath() string {
	if s == nil || s.store == nil || strings.TrimSpace(s.store.Dir()) == "" {
		return ""
	}
	return filepath.Join(s.store.Dir(), "_deleted_opencode_ids.json")
}

func (s *Service) loadDeletedOpenCodeIDs() map[string]bool {
	out := make(map[string]bool)
	p := s.deletedOpenCodeTombstonePath()
	if p == "" {
		return out
	}
	raw, err := os.ReadFile(p)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return out
	}
	var ids []string
	if json.Unmarshal(raw, &ids) != nil {
		return out
	}
	for _, id := range ids {
		if clean := strings.TrimSpace(id); clean != "" {
			out[clean] = true
		}
	}
	return out
}

func (s *Service) purgeDeletedOpenCodeState(ctx context.Context, id string, rec SessionRecord, childIDs []string) {
	seen := make(map[string]bool)
	var ids []string
	add := func(raw string) {
		if v := strings.TrimSpace(raw); v != "" && !seen[v] {
			seen[v] = true
			ids = append(ids, v)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(id), "ses_") {
		add(id)
	}
	if rec.Runtime == "opencode" || len(rec.OpenCodeIDs) > 0 {
		add(rec.ExternalID)
	}
	for _, ocID := range rec.OpenCodeIDs {
		add(ocID)
	}
	for _, childID := range childIDs {
		add(childID)
	}
	if len(ids) == 0 {
		return
	}
	if p := s.deletedOpenCodeTombstonePath(); p != "" {
		s.mu.Lock()
		merged := s.loadDeletedOpenCodeIDs()
		changed := false
		for _, item := range ids {
			if !merged[item] {
				merged[item] = true
				changed = true
			}
		}
		if changed {
			all := make([]string, 0, len(merged))
			for k := range merged {
				all = append(all, k)
			}
			if raw, err := json.Marshal(all); err == nil {
				_ = os.MkdirAll(filepath.Dir(p), 0o700)
				tmp := p + ".tmp"
				if os.WriteFile(tmp, raw, 0o600) == nil {
					_ = os.Rename(tmp, p)
				}
			}
		}
		s.mu.Unlock()
	}
	for _, dbPath := range []string{hoxipaths.DBPath(), hoxipaths.EngineLegacyDBPath()} {
		_ = ocstate.DeleteEngineSessions(ctx, dbPath, ids)
	}
}

func (s *Service) migrateOneOpenCodeSessionFromDB(dbPath, sessionID string) bool {
	escID := "'" + strings.ReplaceAll(sessionID, "'", "''") + "'"
	detailSQL := fmt.Sprintf(`
SELECT json_object(
  'session', (
    SELECT json_object(
      'id', s.id,
      'directory', COALESCE(s.directory, ''),
      'title', COALESCE(s.title, ''),
      'timeCreated', COALESCE(s.time_created, 0),
      'timeUpdated', COALESCE(s.time_updated, 0),
      'model', COALESCE(s.model, '')
    )
    FROM session s WHERE s.id = %s
  ),
  'messages', COALESCE((
    SELECT json_group_array(json_object('id', m.id, 'timeCreated', COALESCE(m.time_created, 0), 'data', m.data))
    FROM (SELECT id, time_created, data FROM message WHERE session_id = %s ORDER BY time_created ASC, id ASC) m
  ), json('[]')),
  'parts', COALESCE((
    SELECT json_group_array(json_object('id', p.id, 'messageID', p.message_id, 'data', p.data))
    FROM (SELECT id, message_id, data FROM part WHERE session_id = %s ORDER BY time_created ASC, id ASC) p
  ), json('[]'))
);`, escID, escID, escID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := subproc.CommandContext(ctx, sqlitecli.Executable(), "-readonly", "-cmd", ".timeout 3000", dbPath, detailSQL)
	cmd.WaitDelay = time.Second
	raw, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return false
	}

	var payload struct {
		Session  *ocLegacySessionMeta `json:"session"`
		Messages []struct {
			ID          string `json:"id"`
			TimeCreated int64  `json:"timeCreated"`
			Data        string `json:"data"`
		} `json:"messages"`
		Parts []struct {
			ID        string `json:"id"`
			MessageID string `json:"messageID"`
			Data      string `json:"data"`
		} `json:"parts"`
	}
	if json.Unmarshal(bytes.TrimSpace(raw), &payload) != nil || payload.Session == nil || payload.Session.ID == "" {
		return false
	}

	partsByMsg := make(map[string][]MessagePart)
	for _, p := range payload.Parts {
		var mp MessagePart
		if json.Unmarshal([]byte(p.Data), &mp) != nil {
			continue
		}
		if mp.ID == "" {
			mp.ID = p.ID
		}
		mp.SessionID = sessionID
		mp.MessageID = p.MessageID
		partsByMsg[p.MessageID] = append(partsByMsg[p.MessageID], mp)
	}

	var turns []MessageTurn
	var inferredProv, inferredMod string
	for _, m := range payload.Messages {
		var info MessageInfo
		if json.Unmarshal([]byte(m.Data), &info) != nil {
			continue
		}
		if info.ID == "" {
			info.ID = m.ID
		}
		info.SessionID = sessionID
		if info.Time.Created == 0 {
			info.Time.Created = m.TimeCreated
		}
		if inferredMod == "" && info.ModelID != "" {
			inferredMod = info.ModelID
			inferredProv = info.ProviderID
		}
		parts := partsByMsg[m.ID]
		if len(parts) == 0 && strings.ToLower(info.Role) == "" {
			continue
		}
		turns = append(turns, MessageTurn{Info: info, Parts: parts})
	}

	provID, modID := parseMigratedModelField(payload.Session.Model)
	if modID == "" {
		provID, modID = inferredProv, inferredMod
	}
	if provID == "" {
		provID = "auto"
	}

	dir := strings.TrimSpace(payload.Session.Directory)
	if dir == "" {
		dir = s.rootDir
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	projectID, _ := s.projectIDForDirectory(dir)
	createdAt := payload.Session.TimeCreated
	if createdAt <= 0 {
		createdAt = time.Now().UnixMilli()
	}
	updatedAt := payload.Session.TimeUpdated
	if updatedAt <= 0 {
		updatedAt = createdAt
	}
	title := strings.TrimSpace(payload.Session.Title)
	if title == "" {
		title = "Migrated Session"
	}

	rec := SessionRecord{
		ID:                  sessionID,
		ProjectID:           projectID,
		Surface:             NormalizeSessionSurface("", dir),
		Runtime:             string(RuntimeNative),
		ProjectDir:          dir,
		WorktreePath:        dir,
		SandboxMode:         "local",
		ProviderID:          provID,
		ModelID:             modID,
		Mode:                "build",
		Title:               title,
		CreatedAt:           createdAt,
		UpdatedAt:           updatedAt,
		Turns:               turns,
		OpenCodeIDs:         []string{sessionID},
		OpenCodeDirectories: map[string]string{sessionID: dir},
		ContextPending:      len(turns) > 0,
		ContextTurnCount:    len(turns),
	}
	if err := s.store.Put(rec); err != nil {
		return false
	}
	s.managedOpenCode.Store(sessionID, true)
	return true
}

func parseMigratedModelField(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if strings.HasPrefix(raw, "{") {
		var obj map[string]interface{}
		if json.Unmarshal([]byte(raw), &obj) == nil {
			p, _ := obj["providerID"].(string)
			m, _ := obj["id"].(string)
			if m == "" {
				m, _ = obj["modelID"].(string)
			}
			return strings.TrimSpace(p), strings.TrimSpace(m)
		}
	}
	if idx := strings.IndexByte(raw, '/'); idx > 0 && idx < len(raw)-1 {
		return strings.TrimSpace(raw[:idx]), strings.TrimSpace(raw[idx+1:])
	}
	return "", raw
}

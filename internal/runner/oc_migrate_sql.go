package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/attachment"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/sqlitecli"
)

type ocRawSessionRow struct {
	ID           string
	ParentID     string
	Directory    string
	Title        string
	TimeCreated  int64
	TimeUpdated  int64
	TimeArchived int64
	Model        string
	Metadata     string
	RevertRaw    string
	DataRaw      string
}

type ocRevertPayload struct {
	MessageID string `json:"messageID"`
	PartID    string `json:"partID"`
	Snapshot  string `json:"snapshot"`
}

type parsedOCTranscript struct {
	turns          []MessageTurn
	snapshots      map[string]string
	inferredProv   string
	inferredMod    string
	attachmentsCnt int
}

func readAllOpenCodeSessionRows(dbPath string) ([]ocRawSessionRow, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var out []ocRawSessionRow
	err := sqlitecli.WithReadOnly(ctx, dbPath, 5*time.Second, func(ctx context.Context, db *sql.DB) error {
		cols := make(map[string]bool)
		colRows, err := db.QueryContext(ctx, "PRAGMA table_info(session);")
		if err != nil {
			return err
		}
		for colRows.Next() {
			var cid, notNull, pk int
			var name, ctype string
			var dflt sql.NullString
			if scanErr := colRows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); scanErr == nil {
				cols[strings.ToLower(strings.TrimSpace(name))] = true
			}
		}
		_ = colRows.Close()
		if len(cols) == 0 {
			return nil
		}

		colExpr := func(col, fallback string) string {
			if cols[col] {
				return "COALESCE(" + col + ", " + fallback + ")"
			}
			return fallback
		}

		query := fmt.Sprintf(`SELECT %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s FROM session;`,
			colExpr("id", "''"),
			colExpr("parent_id", "''"),
			colExpr("directory", "''"),
			colExpr("title", "''"),
			colExpr("time_created", "0"),
			colExpr("time_updated", "0"),
			colExpr("time_archived", "0"),
			colExpr("model", "''"),
			colExpr("metadata", "''"),
			colExpr("revert", "''"),
			colExpr("data", "''"),
		)

		rset, err := db.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		defer rset.Close()
		for rset.Next() {
			var r ocRawSessionRow
			if err := rset.Scan(
				&r.ID, &r.ParentID, &r.Directory, &r.Title,
				&r.TimeCreated, &r.TimeUpdated, &r.TimeArchived,
				&r.Model, &r.Metadata, &r.RevertRaw, &r.DataRaw,
			); err != nil {
				return err
			}
			enrichOCRowFromDataJSON(&r)
			out = append(out, r)
		}
		return rset.Err()
	})
	return out, err
}

func enrichOCRowFromDataJSON(r *ocRawSessionRow) {
	if r == nil || strings.TrimSpace(r.DataRaw) == "" {
		return
	}
	var doc map[string]interface{}
	if json.Unmarshal([]byte(r.DataRaw), &doc) != nil || doc == nil {
		return
	}
	if r.ParentID == "" {
		if v, ok := doc["parentID"].(string); ok {
			r.ParentID = strings.TrimSpace(v)
		}
	}
	if r.Directory == "" {
		if v, ok := doc["directory"].(string); ok {
			r.Directory = strings.TrimSpace(v)
		}
	}
	if r.Title == "" {
		if v, ok := doc["title"].(string); ok {
			r.Title = strings.TrimSpace(v)
		}
	}
	if tMap, ok := doc["time"].(map[string]interface{}); ok {
		if r.TimeCreated <= 0 {
			r.TimeCreated = anyInt64(tMap["created"])
		}
		if r.TimeUpdated <= 0 {
			r.TimeUpdated = anyInt64(tMap["updated"])
		}
		if r.TimeArchived <= 0 {
			r.TimeArchived = anyInt64(tMap["archived"])
		}
	}
	if r.Metadata == "" {
		if mMap, ok := doc["metadata"].(map[string]interface{}); ok {
			if raw, err := json.Marshal(mMap); err == nil {
				r.Metadata = string(raw)
			}
		}
	}
	if r.RevertRaw == "" {
		if revMap, ok := doc["revert"].(map[string]interface{}); ok {
			if raw, err := json.Marshal(revMap); err == nil {
				r.RevertRaw = string(raw)
			}
		}
	}
}

func anyInt64(v interface{}) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		return 0
	}
}

func extractHoxiConversationMetaID(metaRaw string) string {
	metaRaw = strings.TrimSpace(metaRaw)
	if metaRaw == "" {
		return ""
	}
	var m map[string]interface{}
	if json.Unmarshal([]byte(metaRaw), &m) != nil {
		return ""
	}
	v, _ := m["hoxiConversation"].(string)
	return strings.TrimSpace(v)
}

func parseOCRevertRaw(raw string) *ocRevertPayload {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" || raw == "{}" {
		return nil
	}
	var rev ocRevertPayload
	if json.Unmarshal([]byte(raw), &rev) != nil || strings.TrimSpace(rev.MessageID) == "" {
		return nil
	}
	rev.MessageID = strings.TrimSpace(rev.MessageID)
	rev.PartID = strings.TrimSpace(rev.PartID)
	rev.Snapshot = strings.TrimSpace(rev.Snapshot)
	return &rev
}

func (s *Service) readOpenCodeTranscriptFromDB(dbPath, sourceSessionID, targetSessionID, worktreeDir string) (*parsedOCTranscript, error) {
	type msgRow struct {
		id          string
		timeCreated int64
		data        string
	}
	type partRow struct {
		id        string
		messageID string
		data      string
	}
	var msgs []msgRow
	var parts []partRow

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := sqlitecli.WithReadOnly(ctx, dbPath, 5*time.Second, func(ctx context.Context, db *sql.DB) error {
		mRows, err := db.QueryContext(ctx,
			"SELECT COALESCE(id, ''), COALESCE(time_created, 0), COALESCE(data, '') FROM message WHERE session_id = ? ORDER BY time_created ASC, id ASC;",
			sourceSessionID,
		)
		if err != nil {
			return err
		}
		for mRows.Next() {
			var m msgRow
			if err := mRows.Scan(&m.id, &m.timeCreated, &m.data); err != nil {
				_ = mRows.Close()
				return err
			}
			msgs = append(msgs, m)
		}
		if err := mRows.Close(); err != nil {
			return err
		}

		pRows, err := db.QueryContext(ctx,
			"SELECT COALESCE(id, ''), COALESCE(message_id, ''), COALESCE(data, '') FROM part WHERE session_id = ? ORDER BY time_created ASC, id ASC;",
			sourceSessionID,
		)
		if err != nil {
			return err
		}
		defer pRows.Close()
		for pRows.Next() {
			var p partRow
			if err := pRows.Scan(&p.id, &p.messageID, &p.data); err != nil {
				return err
			}
			parts = append(parts, p)
		}
		return pRows.Err()
	})
	if err != nil {
		return nil, err
	}

	res := &parsedOCTranscript{
		snapshots: make(map[string]string),
	}
	partsByMsg := make(map[string][]MessagePart)
	stepSnapshotsByMsg := make(map[string]string)

	for _, p := range parts {
		trimmed := strings.TrimSpace(p.data)
		if trimmed == "" {
			continue
		}
		var rawDoc map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &rawDoc); err != nil {
			return nil, fmt.Errorf("corrupt part %s JSON: %w", p.id, err)
		}
		pType, _ := rawDoc["type"].(string)
		switch strings.TrimSpace(pType) {
		case "step-start":
			if snap, _ := rawDoc["snapshot"].(string); strings.TrimSpace(snap) != "" && stepSnapshotsByMsg[p.messageID] == "" {
				stepSnapshotsByMsg[p.messageID] = strings.TrimSpace(snap)
			}
			continue
		case "step-finish":
			continue
		case "patch":
			if hash, _ := rawDoc["hash"].(string); strings.TrimSpace(hash) != "" && stepSnapshotsByMsg[p.messageID] == "" {
				stepSnapshotsByMsg[p.messageID] = strings.TrimSpace(hash)
			}
		}

		var mp MessagePart
		if err := json.Unmarshal([]byte(trimmed), &mp); err != nil {
			return nil, fmt.Errorf("decode part %s: %w", p.id, err)
		}
		if mp.ID == "" {
			mp.ID = p.id
		}
		mp.SessionID = targetSessionID
		mp.MessageID = p.messageID

		if mp.Type == "file" && strings.TrimSpace(mp.URL) != "" {
			res.attachmentsCnt++
			if strings.HasPrefix(strings.TrimSpace(mp.URL), "data:") && s.attachments != nil {
				fname := firstNonEmptyStr(mp.Filename, "attachment")
				if saved, saveErr := s.attachments.Save(attachment.SaveRequest{
					SessionID: targetSessionID,
					Directory: worktreeDir,
					Filename:  fname,
					Mime:      mp.Mime,
					DataURL:   strings.TrimSpace(mp.URL),
				}); saveErr == nil && saved.URL != "" {
					mp.URL = saved.URL
					mp.Mime = firstNonEmptyStr(saved.Mime, mp.Mime)
					mp.Filename = firstNonEmptyStr(saved.Filename, mp.Filename)
				}
			}
		}
		partsByMsg[p.messageID] = append(partsByMsg[p.messageID], mp)
	}

	lastUserMsgID := ""
	for _, m := range msgs {
		trimmed := strings.TrimSpace(m.data)
		if trimmed == "" {
			continue
		}
		var rawMsg map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &rawMsg); err != nil {
			return nil, fmt.Errorf("corrupt message %s JSON: %w", m.id, err)
		}
		var info MessageInfo
		if err := json.Unmarshal([]byte(trimmed), &info); err != nil {
			return nil, fmt.Errorf("decode message %s: %w", m.id, err)
		}
		if info.ID == "" {
			info.ID = m.id
		}
		info.SessionID = targetSessionID
		if info.Time.Created == 0 {
			info.Time.Created = m.timeCreated
		}
		if info.ProviderID == "" || info.ModelID == "" {
			if modObj, ok := rawMsg["model"].(map[string]interface{}); ok {
				if info.ProviderID == "" {
					if pv, ok := modObj["providerID"].(string); ok {
						info.ProviderID = strings.TrimSpace(pv)
					}
				}
				if info.ModelID == "" {
					if mv, ok := modObj["modelID"].(string); ok && strings.TrimSpace(mv) != "" {
						info.ModelID = strings.TrimSpace(mv)
					} else if idv, ok := modObj["id"].(string); ok {
						info.ModelID = strings.TrimSpace(idv)
					}
				}
			}
		}
		if res.inferredMod == "" && info.ModelID != "" {
			res.inferredMod = info.ModelID
			res.inferredProv = info.ProviderID
		}
		if strings.EqualFold(info.Role, "user") {
			lastUserMsgID = info.ID
		}
		if snap := stepSnapshotsByMsg[m.id]; snap != "" {
			anchorID := firstNonEmptyStr(info.ParentID, lastUserMsgID, info.ID)
			if res.snapshots[anchorID] == "" {
				res.snapshots[anchorID] = snap
			}
		}
		mParts := partsByMsg[m.id]
		if len(mParts) == 0 && strings.TrimSpace(info.Role) == "" {
			continue
		}
		res.turns = append(res.turns, MessageTurn{Info: info, Parts: mParts})
	}
	return res, nil
}

func (s *Service) resolveMigratedWorktree(targetID, sourceID, rawDir string, isSubagent bool) (projDir, wtDir, sandboxMode, branch, baseCommit string) {
	dir := strings.TrimSpace(rawDir)
	if dir == "" {
		dir = s.rootDir
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}

	if s.worktrees != nil {
		for _, candidateID := range []string{targetID, sourceID} {
			if candidateID == "" {
				continue
			}
			if binding, bound, err := s.worktrees.BindingForSession(candidateID); err == nil && bound && binding.Isolated {
				if fi, stErr := os.Stat(binding.Directory); stErr == nil && fi.IsDir() {
					mgr := s.worktrees.ForSession(candidateID)
					root := mgr.RootDir()
					if root == "" {
						root = hoxipaths.ProjectRootForWorktree(binding.Directory)
					}
					if root == "" {
						root = dir
					}
					st, _ := mgr.Status(candidateID)
					br := firstNonEmptyStr(st.Branch, "hoxi/"+candidateID)
					bc := st.BaseCommit
					if !isSubagent {
						_ = s.worktrees.ForDirectory(root).RestoreSession(targetID, binding.Directory, br, bc)
					}
					return root, binding.Directory, "worktree", br, bc
				}
			}
		}
	}

	if root := hoxipaths.ProjectRootForWorktree(dir); root != "" && filepath.Clean(root) != filepath.Clean(dir) {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			wtID := filepath.Base(dir)
			br := "hoxi/" + wtID
			var bc string
			if s.worktrees != nil {
				mgr := s.worktrees.ForDirectory(root)
				_ = mgr.RestoreSession(targetID, dir, br, "")
				if st, stErr := mgr.Status(targetID); stErr == nil {
					br = firstNonEmptyStr(st.Branch, br)
					bc = st.BaseCommit
					_ = mgr.RestoreSession(targetID, dir, br, bc)
				}
			}
			return root, dir, "worktree", br, bc
		}
		if fi, err := os.Stat(root); err == nil && fi.IsDir() {
			dir = root
		}
	}

	if !isSubagent && s.worktrees != nil {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			_ = s.worktrees.ForDirectory(dir).UseLocalDirectory(targetID)
		}
	}
	return dir, dir, "local", "", ""
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

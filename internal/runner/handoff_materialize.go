package runner

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"
)

// NativeHandoffPart describes one imported message part from an external CLI transcript.
type NativeHandoffPart struct {
	ID       string
	Type     string
	Text     string
	Tool     string
	CallID   string
	Title    string
	Status   string
	Input    map[string]interface{}
	Output   string
	StartMs  int64
	EndMs    int64
	Metadata map[string]interface{}
}

// NativeHandoffTokens describes imported token counts for an assistant turn.
type NativeHandoffTokens struct {
	Total      int
	Context    int
	Input      int
	Output     int
	Reasoning  int
	CacheRead  int
	CacheWrite int
}

// NativeHandoffTurn describes one imported user or assistant turn.
type NativeHandoffTurn struct {
	ID          string
	ParentID    string
	Role        string
	CreatedMs   int64
	CompletedMs int64
	ProviderID  string
	ModelID     string
	Mode        string
	Cost        float64
	Finish      string
	Tokens      *NativeHandoffTokens
	Parts       []NativeHandoffPart
}

// NativeHandoffRequest configures direct Hoxi native session creation from an external CLI handoff.
type NativeHandoffRequest struct {
	TargetDirectory string
	Title           string
	ProviderID      string
	ModelID         string
	CreatedAt       int64
	UpdatedAt       int64
	Turns           []NativeHandoffTurn
}

// MaterializeNativeHandoff creates a first-class Hoxi native session (`ses_nat_...`)
// populated with full normalized turns, tool records, and project attribution without OpenCode.
func (s *Service) MaterializeNativeHandoff(req NativeHandoffRequest) (map[string]interface{}, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("runner session store is not initialized")
	}
	projDir := strings.TrimSpace(req.TargetDirectory)
	if projDir == "" {
		projDir = s.rootDir
	}
	absDir, err := filepath.Abs(projDir)
	if err == nil {
		absDir, err = existingDirectory(absDir)
	}
	if err != nil {
		return nil, err
	}

	surface := NormalizeSessionSurface("", absDir)
	projectID, err := s.ensureProjectIDWithSurface(absDir, surface)
	if err != nil {
		return nil, err
	}

	sessionID := GenerateRunnerSessionID(string(RuntimeNative))
	nowMs := time.Now().UnixMilli()
	createdAt := req.CreatedAt
	if createdAt <= 0 {
		createdAt = nowMs
	}
	updatedAt := nowMs
	if req.UpdatedAt > updatedAt {
		updatedAt = req.UpdatedAt
	}

	provID := strings.TrimSpace(req.ProviderID)
	if provID == "" {
		provID = "auto"
	}
	modelID := strings.TrimSpace(req.ModelID)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "Imported Session"
	}

	turns := make([]MessageTurn, 0, len(req.Turns))
	for _, ht := range req.Turns {
		var tok *MessageTokens
		if ht.Tokens != nil {
			tok = &MessageTokens{
				Total:     ht.Tokens.Total,
				Context:   ht.Tokens.Context,
				Input:     ht.Tokens.Input,
				Output:    ht.Tokens.Output,
				Reasoning: ht.Tokens.Reasoning,
			}
			tok.Cache.Read = ht.Tokens.CacheRead
			tok.Cache.Write = ht.Tokens.CacheWrite
		}
		info := MessageInfo{
			ID:         ht.ID,
			SessionID:  sessionID,
			Role:       ht.Role,
			ParentID:   ht.ParentID,
			Time:       MessageTime{Created: ht.CreatedMs, Completed: ht.CompletedMs},
			ModelID:    firstNonEmptyStr(ht.ModelID, modelID),
			ProviderID: firstNonEmptyStr(ht.ProviderID, provID),
			Mode:       firstNonEmptyStr(ht.Mode, "build"),
			Agent:      firstNonEmptyStr(ht.Mode, "build"),
			Imported:   true,
			Cost:       ht.Cost,
			Tokens:     tok,
			Finish:     ht.Finish,
		}
		parts := make([]MessagePart, 0, len(ht.Parts))
		for _, hp := range ht.Parts {
			mp := MessagePart{
				ID:        hp.ID,
				SessionID: sessionID,
				MessageID: ht.ID,
				Type:      hp.Type,
				Text:      hp.Text,
				Metadata:  cloneGenericMap(hp.Metadata),
			}
			if hp.StartMs > 0 || hp.EndMs > 0 {
				mp.Time = &PartTime{Start: hp.StartMs, End: hp.EndMs}
			}
			if hp.Type == "tool" {
				mp.Tool = hp.Tool
				mp.CallID = hp.CallID
				inMap := cloneGenericMap(hp.Input)
				if inMap == nil {
					inMap = map[string]interface{}{}
				}
				var pt *PartTime
				if hp.StartMs > 0 || hp.EndMs > 0 {
					pt = &PartTime{Start: hp.StartMs, End: hp.EndMs}
				}
				st := ToolState{
					Status:   firstNonEmptyStr(hp.Status, "completed"),
					Input:    inMap,
					Output:   hp.Output,
					Title:    firstNonEmptyStr(hp.Title, hp.Tool),
					Metadata: cloneGenericMap(hp.Metadata),
					Time:     pt,
				}
				if st.Status == "error" && st.Error == "" {
					st.Error = hp.Output
				}
				mp.State = &st
				mp.Metadata = nil
			}
			parts = append(parts, mp)
		}
		turns = append(turns, MessageTurn{Info: info, Parts: parts})
	}

	rec := SessionRecord{
		ID:               sessionID,
		ProjectID:        projectID,
		Surface:          surface,
		Runtime:          string(RuntimeNative),
		ProjectDir:       absDir,
		WorktreePath:     absDir,
		SandboxMode:      "local",
		ProviderID:       provID,
		ModelID:          modelID,
		Mode:             "build",
		Title:            title,
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
		Turns:            turns,
		ContextPending:   len(turns) > 0,
		ContextTurnCount: len(turns),
	}
	if err := s.provisionSessionDirectory(&rec); err != nil {
		return nil, err
	}
	if err := s.store.Put(rec); err != nil {
		return nil, err
	}

	info := rec.ToSessionInfo()
	s.broadcast(sessionID, SSEEvent{
		Type:       "session.created",
		Properties: map[string]interface{}{"info": info},
	})

	raw, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}
	var sessionMap map[string]interface{}
	if err := json.Unmarshal(raw, &sessionMap); err != nil {
		return nil, err
	}
	return sessionMap, nil
}

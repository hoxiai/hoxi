package handoff

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/agentcli"
)

func codeBuddyDotDirs() []string {
	if env := strings.TrimSpace(os.Getenv("CODEBUDDY_HOME")); env != "" {
		return []string{filepath.Clean(env)}
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	var out []string
	for _, sub := range []string{".codebuddy", ".codebuddycn"} {
		p := filepath.Join(home, sub)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

func codeBuddyAppSupportDirs() []string {
	if env := strings.TrimSpace(os.Getenv("CODEBUDDY_DATA_DIR")); env != "" {
		return []string{filepath.Clean(env)}
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	candidates := []string{
		filepath.Join(home, "Library", "Application Support", "CodeBuddy CN"),
		filepath.Join(home, "Library", "Application Support", "CodeBuddy"),
		filepath.Join(home, "Library", "Application Support", "CodeBuddyExtension"),
		filepath.Join(home, ".config", "CodeBuddy CN"),
		filepath.Join(home, ".config", "CodeBuddy"),
		filepath.Join(home, ".config", "CodeBuddyExtension"),
	}
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		candidates = append(candidates,
			filepath.Join(appData, "CodeBuddy CN"),
			filepath.Join(appData, "CodeBuddy"),
			filepath.Join(appData, "CodeBuddyExtension"),
		)
	}
	var out []string
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

func decodeCodeBuddyBase64Dir(encoded string) string {
	s := strings.TrimSpace(encoded)
	if s == "" {
		return ""
	}
	s = strings.TrimRight(s, "_")
	if decoded, err := base64.RawURLEncoding.DecodeString(s); err == nil && len(decoded) > 0 {
		return cleanDirPath(string(decoded))
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(s); err == nil && len(decoded) > 0 {
		return cleanDirPath(string(decoded))
	}
	return ""
}

func md5HexString(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

type codeBuddySessionCandidate struct {
	summary    ExternalSessionSummary
	sessionDir string
	jsonlPath  string
	copilotDir string
}

func collectCodeBuddyCandidates(targetDir string) (map[string]*codeBuddySessionCandidate, bool) {
	dotDirs := codeBuddyDotDirs()
	appDirs := codeBuddyAppSupportDirs()
	installed := len(dotDirs) > 0 || len(appDirs) > 0
	byID := map[string]*codeBuddySessionCandidate{}
	if !installed {
		return byID, false
	}

	cwdBySessionID := map[string]string{}
	cwdByProjHash := map[string]string{}
	var primaryCopilotDir string

	// 1. Scan codebuddy-sessions.vscdb & genie-history in CodeBuddy / CodeBuddy CN app dirs
	for _, appDir := range appDirs {
		copDir := filepath.Join(appDir, "User", "globalStorage", "tencent-cloud.coding-copilot")
		if st, err := os.Stat(copDir); err == nil && st.IsDir() && primaryCopilotDir == "" {
			primaryCopilotDir = copDir
		}

		// genie-history/<base64-cwd> maps both projHash and sessionID to cwd
		genieRoot := filepath.Join(copDir, "genie-history")
		if gDirs, err := os.ReadDir(genieRoot); err == nil {
			for _, gd := range gDirs {
				if !gd.IsDir() {
					continue
				}
				cwd := decodeCodeBuddyBase64Dir(gd.Name())
				if cwd == "" {
					continue
				}
				cwdByProjHash[md5HexString(cwd)] = cwd
				curBytes, err := os.ReadFile(filepath.Join(genieRoot, gd.Name(), "current.json"))
				if err == nil {
					var curDoc struct {
						ConversationID string `json:"conversationId"`
					}
					if json.Unmarshal(curBytes, &curDoc) == nil && curDoc.ConversationID != "" {
						cwdBySessionID[curDoc.ConversationID] = cwd
					}
				}
				if cEntries, err := os.ReadDir(filepath.Join(genieRoot, gd.Name(), "conversations")); err == nil {
					for _, ce := range cEntries {
						if ce.IsDir() {
							cwdBySessionID[ce.Name()] = cwd
						}
					}
				}
			}
		}

		// codebuddy-sessions.vscdb
		vscdb := filepath.Join(appDir, "codebuddy-sessions.vscdb")
		if _, err := os.Stat(vscdb); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			cmd := exec.CommandContext(ctx, "sqlite3", "-readonly", "-cmd", ".timeout 3000", "-json", vscdb, "SELECT value FROM ItemTable WHERE key LIKE 'session:%' LIMIT 150;")
			cmd.WaitDelay = time.Second
			rawOut, err := cmd.Output()
			cancel()
			if err == nil && len(bytes.TrimSpace(rawOut)) > 0 {
				var rows []struct {
					Value string `json:"value"`
				}
				if json.Unmarshal(rawOut, &rows) == nil {
					for _, r := range rows {
						var item struct {
							ConversationID string `json:"conversationId"`
							CWD            string `json:"cwd"`
							Title          string `json:"title"`
							CreatedAt      int64  `json:"createdAt"`
							UpdatedAt      int64  `json:"updatedAt"`
							IsPlayground   bool   `json:"isPlayground"`
						}
						if json.Unmarshal([]byte(r.Value), &item) != nil || item.ConversationID == "" {
							continue
						}
						cwd := cleanDirPath(item.CWD)
						if cwd != "" {
							cwdBySessionID[item.ConversationID] = cwd
							cwdByProjHash[md5HexString(cwd)] = cwd
						}
						title := strings.TrimSpace(item.Title)
						if title == "" {
							title = "CodeBuddy Session " + item.ConversationID[:minInt(8, len(item.ConversationID))]
						}
						byID[item.ConversationID] = &codeBuddySessionCandidate{
							copilotDir: copDir,
							summary: ExternalSessionSummary{
								ID:          item.ConversationID,
								Source:      ExternalToolCodeBuddy,
								SourceLabel: "CodeBuddy",
								CWD:         cwd,
								Title:       title,
								CreatedAt:   item.CreatedAt,
								UpdatedAt:   item.UpdatedAt,
								MatchesCWD:  sameOrSubPath(cwd, targetDir),
							},
						}
					}
				}
			}
		}
	}

	// 2. Scan CodeBuddyExtension/Data/<uid>/CodeBuddyIDE/<uid>/history/<proj-hash>/
	for _, appDir := range appDirs {
		dataRoot := filepath.Join(appDir, "Data")
		uDirs, err := os.ReadDir(dataRoot)
		if err != nil {
			continue
		}
		for _, uDir := range uDirs {
			if !uDir.IsDir() {
				continue
			}
			ideRoot := filepath.Join(dataRoot, uDir.Name(), "CodeBuddyIDE")
			ideUsers, err := os.ReadDir(ideRoot)
			if err != nil {
				continue
			}
			for _, iu := range ideUsers {
				if !iu.IsDir() {
					continue
				}
				histRoot := filepath.Join(ideRoot, iu.Name(), "history")
				projDirs, err := os.ReadDir(histRoot)
				if err != nil {
					continue
				}
				for _, pd := range projDirs {
					if !pd.IsDir() {
						continue
					}
					projHash := pd.Name()
					projPath := filepath.Join(histRoot, projHash)
					idxBytes, err := os.ReadFile(filepath.Join(projPath, "index.json"))
					if err != nil {
						continue
					}
					var idxDoc struct {
						Conversations []struct {
							ID              string            `json:"id"`
							Name            string            `json:"name"`
							CreatedAt       string            `json:"createdAt"`
							LastMessageAt   string            `json:"lastMessageAt"`
							SelectedModelID string            `json:"selectedModelId"`
							ModelMap        map[string]string `json:"modelMap"`
						} `json:"conversations"`
					}
					if json.Unmarshal(idxBytes, &idxDoc) != nil {
						continue
					}
					for _, conv := range idxDoc.Conversations {
						sID := strings.TrimSpace(conv.ID)
						if sID == "" {
							continue
						}
						sessDir := filepath.Join(projPath, sID)
						hasMessages := false
						if st, err := os.Stat(filepath.Join(sessDir, "messages")); err == nil && st.IsDir() {
							hasMessages = true
						}
						name := strings.TrimSpace(conv.Name)
						if name == "" && !hasMessages {
							// Skip empty uninitialized stub conversations
							continue
						}
						model := strings.TrimSpace(conv.SelectedModelID)
						if model == "" && conv.ModelMap != nil {
							for _, mv := range conv.ModelMap {
								if strings.TrimSpace(mv) != "" {
									model = strings.TrimSpace(mv)
									break
								}
							}
						}
						cwd := firstNonEmpty(cwdBySessionID[sID], cwdByProjHash[projHash])
						if cwd == "" && hasMessages {
							cwd = sniffCodeBuddyWorkspaceFolder(sessDir)
							if cwd != "" {
								cwdByProjHash[projHash] = cwd
							}
						}
						createdMs := parseTimeMs(conv.CreatedAt)
						updatedMs := parseTimeMs(conv.LastMessageAt)
						if updatedMs == 0 {
							updatedMs = createdMs
						}

						if existing, ok := byID[sID]; ok {
							if hasMessages {
								existing.sessionDir = sessDir
							}
							if existing.copilotDir == "" {
								existing.copilotDir = primaryCopilotDir
							}
							if name != "" && strings.HasPrefix(existing.summary.Title, "CodeBuddy Session ") {
								existing.summary.Title = name
							}
							if existing.summary.CWD == "" && cwd != "" {
								existing.summary.CWD = cwd
								existing.summary.MatchesCWD = sameOrSubPath(cwd, targetDir)
							}
							if existing.summary.Model == "" && model != "" {
								existing.summary.Model = model
							}
							if updatedMs > existing.summary.UpdatedAt {
								existing.summary.UpdatedAt = updatedMs
							}
						} else {
							if name == "" {
								name = "CodeBuddy Session " + sID[:minInt(8, len(sID))]
							}
							var sDir string
							if hasMessages {
								sDir = sessDir
							}
							byID[sID] = &codeBuddySessionCandidate{
								sessionDir: sDir,
								copilotDir: primaryCopilotDir,
								summary: ExternalSessionSummary{
									ID:          sID,
									Source:      ExternalToolCodeBuddy,
									SourceLabel: "CodeBuddy",
									CWD:         cwd,
									Title:       name,
									Model:       model,
									CreatedAt:   createdMs,
									UpdatedAt:   updatedMs,
									MatchesCWD:  sameOrSubPath(cwd, targetDir),
								},
							}
						}
					}
				}
			}
		}
	}

	// 3. Scan CodeBuddy CLI ~/.codebuddy/projects/<slug>/<session-id>.jsonl
	for _, dotDir := range dotDirs {
		projRoot := filepath.Join(dotDir, "projects")
		projDirs, err := os.ReadDir(projRoot)
		if err != nil {
			continue
		}
		for _, pd := range projDirs {
			if !pd.IsDir() {
				continue
			}
			dirPath := filepath.Join(projRoot, pd.Name())
			decodedCWD := decodeHyphenatedProjectPath(pd.Name(), targetDir)
			files, err := os.ReadDir(dirPath)
			if err != nil {
				continue
			}
			for _, f := range files {
				if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
					continue
				}
				sID := strings.TrimSuffix(f.Name(), ".jsonl")
				fullPath := filepath.Join(dirPath, f.Name())
				info, err := f.Info()
				if err != nil || info.Size() == 0 {
					continue
				}
				cwd, title, model, createdMs, updatedMs, turnCount := sniffCodeBuddyCLIJSONL(fullPath, decodedCWD, info.ModTime().UnixMilli())
				if turnCount == 0 {
					continue
				}
				if title == "" {
					title = "CodeBuddy Session " + sID[:minInt(8, len(sID))]
				}
				if existing, ok := byID[sID]; ok {
					if existing.jsonlPath == "" {
						existing.jsonlPath = fullPath
					}
					if existing.summary.CWD == "" && cwd != "" {
						existing.summary.CWD = cwd
						existing.summary.MatchesCWD = sameOrSubPath(cwd, targetDir)
					}
					if existing.summary.Model == "" && model != "" {
						existing.summary.Model = model
					}
					if updatedMs > existing.summary.UpdatedAt {
						existing.summary.UpdatedAt = updatedMs
					}
				} else {
					byID[sID] = &codeBuddySessionCandidate{
						jsonlPath:  fullPath,
						copilotDir: primaryCopilotDir,
						summary: ExternalSessionSummary{
							ID:          sID,
							Source:      ExternalToolCodeBuddy,
							SourceLabel: "CodeBuddy",
							CWD:         cwd,
							Title:       title,
							Model:       model,
							TurnCount:   turnCount,
							CreatedAt:   createdMs,
							UpdatedAt:   updatedMs,
							MatchesCWD:  sameOrSubPath(cwd, targetDir),
						},
					}
				}
			}
		}
	}

	return byID, installed
}

func sniffCodeBuddyWorkspaceFolder(sessionDir string) string {
	idxBytes, err := os.ReadFile(filepath.Join(sessionDir, "index.json"))
	if err != nil {
		return ""
	}
	var idx struct {
		Messages []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"messages"`
	}
	if json.Unmarshal(idxBytes, &idx) != nil {
		return ""
	}
	for i, m := range idx.Messages {
		if i > 3 {
			break
		}
		if m.Role != "user" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(sessionDir, "messages", m.ID+".json"))
		if err != nil {
			continue
		}
		if cwd := extractWorkspaceFolderTag(string(raw)); cwd != "" {
			return cwd
		}
	}
	return ""
}

func extractWorkspaceFolderTag(s string) string {
	const marker = "Workspace Folder:"
	idx := strings.Index(s, marker)
	if idx < 0 {
		return ""
	}
	rest := s[idx+len(marker):]
	for _, sep := range []string{"\\n", "\n", "<", "\""} {
		if end := strings.Index(rest, sep); end >= 0 {
			rest = rest[:end]
		}
	}
	return cleanDirPath(strings.TrimSpace(rest))
}

func scanCodeBuddySessions(targetDir, query string) ([]ExternalSessionSummary, ExternalSourceStats) {
	byID, installed := collectCodeBuddyCandidates(targetDir)
	stats := ExternalSourceStats{
		Installed:    installed,
		SessionCount: len(byID),
	}
	if len(byID) == 0 {
		return nil, stats
	}

	qLower := strings.ToLower(strings.TrimSpace(query))
	results := make([]ExternalSessionSummary, 0, len(byID))
	for _, cand := range byID {
		sum := cand.summary
		if qLower != "" {
			haystack := strings.ToLower(sum.Title + " " + sum.Summary + " " + sum.CWD + " " + sum.Model)
			if !strings.Contains(haystack, qLower) {
				continue
			}
		}
		results = append(results, sum)
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].MatchesCWD != results[j].MatchesCWD {
			return results[i].MatchesCWD
		}
		return results[i].UpdatedAt > results[j].UpdatedAt
	})
	return results, stats
}

func detectCodeBuddyCLIStatus(hoxiMap map[string]interface{}) LocalCLIStatusItem {
	sessions, stats := scanCodeBuddySessions("", "")
	bin := agentcli.ResolveCodeBuddyBinary()
	var models []string
	var defModel string
	for _, m := range agentcli.DiscoverCodeBuddyModels() {
		if m.ID != "" {
			models = appendUniqueString(models, m.ID)
			if defModel == "" {
				defModel = m.ID
			}
		}
	}
	for _, s := range sessions {
		if s.Model != "" {
			models = appendUniqueString(models, s.Model)
			if defModel == "" {
				defModel = s.Model
			}
		}
	}
	_, customConn := hoxiMap["codebuddy"]
	authMode := "session-handoff"
	if bin != "" {
		authMode = "codebuddy-runner"
	} else if customConn {
		authMode = "hoxi-custom"
	}
	return LocalCLIStatusItem{
		ID:            ExternalToolCodeBuddy,
		Name:          "CodeBuddy",
		Installed:     stats.Installed || bin != "",
		Authenticated: bin != "" || customConn,
		Connected:     bin != "" || customConn,
		DefaultModel:  defModel,
		Models:        models,
		SessionCount:  stats.SessionCount,
		AuthMode:      authMode,
		ProviderID:    "codebuddy",
	}
}

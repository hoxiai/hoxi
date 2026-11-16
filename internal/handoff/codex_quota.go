package handoff

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/sqlitecli"
)

const (
	codexPrimaryDefaultWindowMin   = 300   // 5 hours
	codexSecondaryDefaultWindowMin = 10080 // 7 days (weekly)
	codexQuotaMaxInspectRollouts   = 12
)

type codexRolloutCandidate struct {
	path      string
	updatedMs int64
}

func parseCodexJWTPlanType(token string) string {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 || parts[1] == "" {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]interface{}
	if json.Unmarshal(payload, &claims) != nil || claims == nil {
		return ""
	}
	if p := strings.TrimSpace(asString(claims["chatgpt_plan_type"])); p != "" {
		return strings.ToLower(p)
	}
	if p := strings.TrimSpace(asString(claims["plan_type"])); p != "" {
		return strings.ToLower(p)
	}
	if authObj, ok := claims["https://api.openai.com/auth"].(map[string]interface{}); ok {
		if p := strings.TrimSpace(asString(authObj["chatgpt_plan_type"])); p != "" {
			return strings.ToLower(p)
		}
		if p := strings.TrimSpace(asString(authObj["plan_type"])); p != "" {
			return strings.ToLower(p)
		}
	}
	return ""
}

func parseCodexFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		if !math.IsNaN(n) && !math.IsInf(n, 0) {
			return n, true
		}
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f, true
		}
	}
	return 0, false
}

func roundOneDecimal(v float64) float64 {
	return math.Round(v*10) / 10
}

func parseCodexRateLimitWindow(raw map[string]interface{}, eventTsMs, nowMs int64, defaultWindowMin int) *LocalCLIQuotaWindow {
	if len(raw) == 0 {
		return nil
	}
	used, hasUsed := parseCodexFloat(raw["used_percent"])
	if !hasUsed {
		used, hasUsed = parseCodexFloat(raw["usedPercent"])
	}
	if !hasUsed {
		return nil
	}
	if used < 0 {
		used = 0
	}
	if used > 100 {
		used = 100
	}
	windowMin := int(asInt64(raw["window_minutes"]))
	if windowMin <= 0 {
		windowMin = int(asInt64(raw["windowMinutes"]))
	}
	if windowMin <= 0 {
		windowMin = defaultWindowMin
	}

	resetsAt := asInt64(raw["resets_at"])
	if resetsAt == 0 {
		resetsAt = asInt64(raw["resetsAt"])
	}
	if resetsAt > 0 && resetsAt < 10_000_000_000 {
		resetsAt *= 1000
	}
	if resetsAt == 0 {
		if inSec := asInt64(raw["resets_in_seconds"]); inSec > 0 && eventTsMs > 0 {
			resetsAt = eventTsMs + inSec*1000
		}
	}

	// If the upstream reset timestamp is already in the past, the quota window has rolled over.
	if resetsAt > 0 && nowMs > 0 && resetsAt <= nowMs {
		used = 0
		resetsAt = 0
	}
	usedRounded := roundOneDecimal(used)
	remRounded := roundOneDecimal(math.Max(0, 100-usedRounded))
	return &LocalCLIQuotaWindow{
		WindowMinutes:    windowMin,
		UsedPercent:      &usedRounded,
		RemainingPercent: &remRounded,
		ResetsAt:         resetsAt,
	}
}

func recentCodexRolloutCandidates(cDir string) []codexRolloutCandidate {
	seen := map[string]bool{}
	var out []codexRolloutCandidate

	if dbPath := findCodexStateDB(); dbPath != "" {
		sqlQuery := `SELECT rollout_path, updated_at FROM threads WHERE archived = 0 ORDER BY updated_at DESC LIMIT 15;`
		if rawJSON, err := codexCommandOutput(sqlitecli.Executable(), "-readonly", "-cmd", ".timeout 3000", "-json", dbPath, sqlQuery); err == nil && len(bytes.TrimSpace(rawJSON)) > 0 {
			var rows []struct {
				RolloutPath string `json:"rollout_path"`
				UpdatedAt   int64  `json:"updated_at"`
			}
			if json.Unmarshal(rawJSON, &rows) == nil {
				for _, r := range rows {
					p := strings.TrimSpace(r.RolloutPath)
					if p == "" || seen[p] {
						continue
					}
					if st, err := os.Stat(p); err == nil && !st.IsDir() {
						seen[p] = true
						uMs := r.UpdatedAt
						if uMs > 0 && uMs < 10_000_000_000 {
							uMs *= 1000
						}
						if fiMod := st.ModTime().UnixMilli(); fiMod > uMs {
							uMs = fiMod
						}
						out = append(out, codexRolloutCandidate{path: p, updatedMs: uMs})
					}
				}
			}
		}
	}

	sessRoot := filepath.Join(cDir, "sessions")
	if st, err := os.Stat(sessRoot); err == nil && st.IsDir() && len(out) < codexQuotaMaxInspectRollouts {
		_ = filepath.WalkDir(sessRoot, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".jsonl") && !strings.HasSuffix(name, ".jsonl.zst") {
				return nil
			}
			if seen[path] {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			seen[path] = true
			out = append(out, codexRolloutCandidate{path: path, updatedMs: fi.ModTime().UnixMilli()})
			return nil
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].updatedMs > out[j].updatedMs
	})
	if len(out) > codexQuotaMaxInspectRollouts {
		out = out[:codexQuotaMaxInspectRollouts]
	}
	return out
}

func scanCodexRolloutQuotaFile(path string, fallbackMs, cutoff7dMs int64) (rateLimits map[string]interface{}, rlTsMs int64, weeklyTokens int64, weeklyTurns int) {
	rc, err := openCodexRolloutReader(path)
	if err != nil {
		return nil, 0, 0, 0
	}
	defer rc.Close()

	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)

	var modernTokens int64
	var latestLegacyTotal int64
	var legacyTurnSum int64

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item map[string]interface{}
		if json.Unmarshal([]byte(line), &item) != nil {
			continue
		}
		ts := parseTimeMs(asString(item["timestamp"]))
		if ts <= 0 {
			ts = fallbackMs
		}
		itemType := asString(item["type"])
		payload, _ := item["payload"].(map[string]interface{})
		if payload == nil {
			continue
		}
		pType := asString(payload["type"])

		if itemType == "event_msg" && pType == "token_count" {
			if rl, ok := payload["rate_limits"].(map[string]interface{}); ok && len(rl) > 0 {
				rateLimits = rl
				rlTsMs = ts
			}
		}

		if ts >= cutoff7dMs {
			if itemType == "response_item" && pType == "message" && asString(payload["role"]) == "assistant" {
				if asString(payload["phase"]) != "commentary" {
					weeklyTurns++
				}
			}
			if itemType == "token_usage_record" {
				u := parseTurnUsage(payload["usage"])
				modernTokens += u.stepTokens()
			} else if itemType == "event_msg" && pType == "token_count" {
				info, _ := payload["info"].(map[string]interface{})
				if tot := parseTurnUsage(info["total_token_usage"]); !tot.empty() {
					if s := tot.stepTokens(); s > latestLegacyTotal {
						latestLegacyTotal = s
					}
				} else if u := codexItemUsage(item, payload); !u.empty() {
					legacyTurnSum += u.stepTokens()
				}
			}
		}
	}

	if modernTokens > 0 {
		weeklyTokens = modernTokens
	} else if latestLegacyTotal > 0 {
		weeklyTokens = latestLegacyTotal
	} else {
		weeklyTokens = legacyTurnSum
	}
	return rateLimits, rlTsMs, weeklyTokens, weeklyTurns
}

func collectCodexQuotaSnapshot(cDir string, now time.Time) *LocalCLIQuotaSnapshot {
	if cDir == "" {
		return nil
	}
	var planType string
	if raw, err := os.ReadFile(filepath.Join(cDir, "auth.json")); err == nil {
		var doc map[string]interface{}
		if json.Unmarshal(raw, &doc) == nil {
			if tokens, ok := doc["tokens"].(map[string]interface{}); ok {
				planType = firstNonEmpty(
					parseCodexJWTPlanType(asString(tokens["access_token"])),
					parseCodexJWTPlanType(asString(tokens["id_token"])),
				)
			}
		}
	}

	nowMs := now.UnixMilli()
	cutoff7dMs := now.Add(-7 * 24 * time.Hour).UnixMilli()
	candidates := recentCodexRolloutCandidates(cDir)

	var bestRL map[string]interface{}
	var bestRLTs int64
	var totalWeeklyTokens int64
	var totalWeeklyTurns int
	var latestUpdatedMs int64

	for _, cand := range candidates {
		if bestRL != nil && cand.updatedMs < cutoff7dMs {
			break
		}
		rl, rlTs, wTokens, wTurns := scanCodexRolloutQuotaFile(cand.path, cand.updatedMs, cutoff7dMs)
		if rl != nil && (bestRL == nil || rlTs > bestRLTs) {
			bestRL = rl
			bestRLTs = rlTs
		}
		if cand.updatedMs >= cutoff7dMs {
			totalWeeklyTokens += wTokens
			totalWeeklyTurns += wTurns
			if cand.updatedMs > latestUpdatedMs {
				latestUpdatedMs = cand.updatedMs
			}
		}
	}

	var primary, secondary *LocalCLIQuotaWindow
	if bestRL != nil {
		if pt := strings.ToLower(strings.TrimSpace(asString(bestRL["plan_type"]))); pt != "" {
			planType = pt
		} else if pt := strings.ToLower(strings.TrimSpace(asString(bestRL["planType"]))); pt != "" {
			planType = pt
		}
		if pMap, ok := bestRL["primary"].(map[string]interface{}); ok {
			primary = parseCodexRateLimitWindow(pMap, bestRLTs, nowMs, codexPrimaryDefaultWindowMin)
		}
		if sMap, ok := bestRL["secondary"].(map[string]interface{}); ok {
			secondary = parseCodexRateLimitWindow(sMap, bestRLTs, nowMs, codexSecondaryDefaultWindowMin)
		}
		if bestRLTs > latestUpdatedMs {
			latestUpdatedMs = bestRLTs
		}
	}

	if planType == "" && primary == nil && secondary == nil && totalWeeklyTokens == 0 && totalWeeklyTurns == 0 {
		return nil
	}
	return &LocalCLIQuotaSnapshot{
		PlanType:     planType,
		Primary:      primary,
		Secondary:    secondary,
		WeeklyTokens: totalWeeklyTokens,
		WeeklyTurns:  totalWeeklyTurns,
		UpdatedAt:    latestUpdatedMs,
	}
}

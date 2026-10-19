package handoff

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func loadCodeBuddyTodosAndBrain(copilotDir, sessionID string) ([]ExternalPlanStep, string) {
	if copilotDir == "" || sessionID == "" {
		return nil, ""
	}
	var plans []ExternalPlanStep
	todoBytes, err := os.ReadFile(filepath.Join(copilotDir, "todos", sessionID+".json"))
	if err == nil {
		var doc struct {
			Todos []struct {
				Content string `json:"content"`
				Status  string `json:"status"`
			} `json:"todos"`
		}
		if json.Unmarshal(todoBytes, &doc) == nil {
			for _, td := range doc.Todos {
				if strings.TrimSpace(td.Content) != "" {
					plans = append(plans, ExternalPlanStep{
						Title:  strings.TrimSpace(td.Content),
						Status: firstNonEmpty(td.Status, "pending"),
					})
				}
			}
		}
	}

	var brainDocs []string
	brainDir := filepath.Join(copilotDir, "brain", sessionID)
	if files, err := os.ReadDir(brainDir); err == nil {
		for _, f := range files {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") {
				if raw, err := os.ReadFile(filepath.Join(brainDir, f.Name())); err == nil {
					brainDocs = append(brainDocs, truncateRunes(strings.TrimSpace(string(raw)), 800))
				}
			}
		}
	}
	return plans, strings.Join(brainDocs, "\n\n")
}

func loadCodeBuddySessionDetail(sessionID, hintCWD, activeRoot string) (*ExternalSessionDetail, error) {
	targetDir := firstNonEmpty(hintCWD, activeRoot)
	byID, _ := collectCodeBuddyCandidates(targetDir)
	cand, ok := byID[sessionID]
	if !ok || cand == nil {
		return nil, fmt.Errorf("codebuddy session not found: %s", sessionID)
	}

	turns, msgPlans, msgCWD := parseCodeBuddySessionMessages(cand.sessionDir, cand.summary.CreatedAt, cand.summary.Model)
	todoPlans, brainRecap := loadCodeBuddyTodosAndBrain(cand.copilotDir, sessionID)
	if len(todoPlans) > 0 {
		msgPlans = todoPlans
	}
	if len(turns) == 0 {
		turns = []NormalizedTurn{
			{
				Role:      "user",
				Timestamp: cand.summary.CreatedAt,
				Text:      cand.summary.Title,
			},
			{
				Role:      "assistant",
				Timestamp: cand.summary.UpdatedAt,
				Text:      firstNonEmpty(brainRecap, cand.summary.Summary, "Imported CodeBuddy session: "+cand.summary.Title),
				ModelID:   cand.summary.Model,
			},
		}
	}

	sum := cand.summary
	if sum.CWD == "" {
		sum.CWD = firstNonEmpty(msgCWD, hintCWD)
	}
	if sum.Model == "" {
		for _, t := range turns {
			if t.ModelID != "" {
				sum.Model = t.ModelID
				break
			}
		}
	}
	var lastTurnSummary string
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == "assistant" && turns[i].Text != "" {
			lastTurnSummary = truncateRunes(turns[i].Text, 360)
			break
		}
	}
	if sum.Summary == "" {
		sum.Summary = truncateRunes(firstNonEmpty(lastTurnSummary, brainRecap), 240)
	}
	sum.TurnCount = len(turns)
	sum.MatchesCWD = sameOrSubPath(sum.CWD, activeRoot)

	return &ExternalSessionDetail{
		Summary:         sum,
		Turns:           turns,
		PlanSteps:       msgPlans,
		ChangedFiles:    extractChangedFilesFromTurns(turns),
		LastTurnSummary: lastTurnSummary,
		LastRecap:       brainRecap,
	}, nil
}

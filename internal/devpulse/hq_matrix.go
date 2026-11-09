package devpulse

import (
	"os"
	"path/filepath"
	"strings"
)

// HQMatrixSummary summarizes the federated workspace dashboard (DASHBOARD.md)
// and daily operations pulse (OPS_PULSE.md) when located in or above a
// workspace root containing .hq/topology.env.
type HQMatrixSummary struct {
	Enabled         bool     `json:"enabled"`
	RootDir         string   `json:"rootDir,omitempty"`
	UpdatedAt       string   `json:"updatedAt,omitempty"`
	TotalProjects   int      `json:"totalProjects"`
	IdleProjects    int      `json:"idleProjects"`
	ReadyProjects   int      `json:"readyProjects"`
	ActiveProjects  int      `json:"activeProjects"`
	BlockedProjects int      `json:"blockedProjects"`
	BlockedNames    []string `json:"blockedNames,omitempty"`
	ActiveSandboxes int      `json:"activeSandboxes"`
	OpsUpdatedAt    string   `json:"opsUpdatedAt,omitempty"`
	OpsHealthyCount int      `json:"opsHealthyCount"`
	OpsAnomalyCount int      `json:"opsAnomalyCount"`
}

func findHQWorkspaceRoot(candidates []string) string {
	seen := make(map[string]struct{})
	for _, raw := range candidates {
		curr := canonicalPulseDir(raw)
		for curr != "" && curr != "/" && curr != "." {
			if _, ok := seen[curr]; ok {
				break
			}
			seen[curr] = struct{}{}
			if info, err := os.Stat(filepath.Join(curr, ".hq", "topology.env")); err == nil && !info.IsDir() {
				return curr
			}
			if info, err := os.Stat(filepath.Join(curr, "DASHBOARD.md")); err == nil && !info.IsDir() {
				if _, hqErr := os.Stat(filepath.Join(curr, ".hq")); hqErr == nil {
					return curr
				}
			}
			parent := filepath.Dir(curr)
			if parent == curr {
				break
			}
			curr = parent
		}
	}
	return ""
}

// CollectHQMatrixSummary parses DASHBOARD.md and OPS_PULSE.md from the nearest
// .hq workspace root so Hoxi's Dev Pulse popover can surface fleet status.
func (s *Service) CollectHQMatrixSummary(targets []GitTarget) *HQMatrixSummary {
	var candidates []string
	for _, t := range targets {
		if t.RepoRoot != "" {
			candidates = append(candidates, t.RepoRoot)
		}
		if t.Dir != "" {
			candidates = append(candidates, t.Dir)
		}
	}
	if s.source != nil {
		candidates = append(candidates, s.source.CandidateDirectories()...)
	}
	root := findHQWorkspaceRoot(candidates)
	if root == "" {
		return nil
	}

	dashBytes, err := os.ReadFile(filepath.Join(root, "DASHBOARD.md"))
	if err != nil || len(dashBytes) == 0 {
		return nil
	}

	summary := &HQMatrixSummary{
		Enabled: true,
		RootDir: root,
	}

	inSandboxes := false
	inTable := false
	for _, rawLine := range strings.Split(string(dashBytes), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if summary.UpdatedAt == "" && strings.Contains(line, "**自动生成时间**：`") {
			if start := strings.Index(line, "**自动生成时间**：`"); start >= 0 {
				rest := line[start+len("**自动生成时间**：`"):]
				if end := strings.IndexByte(rest, '`'); end > 0 {
					summary.UpdatedAt = strings.TrimSpace(rest[:end])
				}
			}
		}
		if strings.HasPrefix(line, "## ") {
			inSandboxes = strings.Contains(line, "活跃并发沙箱池")
			inTable = strings.Contains(line, "全矩阵项目状态总览")
			continue
		}
		if inSandboxes && strings.HasPrefix(line, "- **`") {
			summary.ActiveSandboxes++
			continue
		}
		if inTable && strings.HasPrefix(line, "| [**") {
			cols := strings.Split(line, "|")
			if len(cols) < 5 {
				continue
			}
			projCell := strings.TrimSpace(cols[1])
			statusCell := strings.ToUpper(strings.TrimSpace(cols[3]))
			projName := extractMarkdownBoldLinkText(projCell)
			summary.TotalProjects++
			switch {
			case strings.Contains(statusCell, "BLOCKED"):
				summary.BlockedProjects++
				if projName != "" {
					summary.BlockedNames = append(summary.BlockedNames, projName)
				}
			case strings.Contains(statusCell, "IN_PROGRESS") || strings.Contains(statusCell, "RUNNING") || strings.Contains(statusCell, "ACTIVE"):
				summary.ActiveProjects++
			case strings.Contains(statusCell, "READY"):
				summary.ReadyProjects++
			default:
				summary.IdleProjects++
			}
		}
	}

	if opsBytes, err := os.ReadFile(filepath.Join(root, "OPS_PULSE.md")); err == nil && len(opsBytes) > 0 {
		inAnomalies := false
		for _, rawLine := range strings.Split(string(opsBytes), "\n") {
			line := strings.TrimSpace(rawLine)
			if line == "" {
				continue
			}
			if summary.OpsUpdatedAt == "" && strings.Contains(line, "**巡检时间**：`") {
				if start := strings.Index(line, "**巡检时间**：`"); start >= 0 {
					rest := line[start+len("**巡检时间**：`"):]
					if end := strings.IndexByte(rest, '`'); end > 0 {
						summary.OpsUpdatedAt = strings.TrimSpace(rest[:end])
					}
				}
			}
			if strings.HasPrefix(line, "## ") {
				inAnomalies = strings.Contains(line, "异常告警与自动派单记录")
				continue
			}
			if strings.HasPrefix(line, "| ✅ ") {
				summary.OpsHealthyCount++
			} else if strings.HasPrefix(line, "| 🚨 ") {
				summary.OpsAnomalyCount++
			} else if inAnomalies && strings.HasPrefix(line, "- 🚨 ") {
				summary.OpsAnomalyCount++
			}
		}
	}

	return summary
}

func extractMarkdownBoldLinkText(cell string) string {
	if start := strings.Index(cell, "[**"); start >= 0 {
		rest := cell[start+3:]
		if end := strings.Index(rest, "**]"); end > 0 {
			return strings.TrimSpace(rest[:end])
		}
	}
	return ""
}

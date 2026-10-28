package orchestration

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

type TaskSpec struct {
	ID        string   `json:"id"`
	Prompt    string   `json:"prompt"`
	DependsOn []string `json:"dependsOn,omitempty"`
}

type PlanRequest struct {
	Directory       string     `json:"directory"`
	ParentSessionID string     `json:"parentSessionID,omitempty"`
	Title           string     `json:"title,omitempty"`
	Mode            string     `json:"mode,omitempty"`
	Model           string     `json:"model,omitempty"`
	Provider        string     `json:"providerID,omitempty"`
	ProviderAlias   string     `json:"provider,omitempty"`
	Concurrency     int        `json:"concurrency"`
	Tasks           []TaskSpec `json:"tasks"`
}

type TaskState struct {
	ID        string   `json:"id"`
	Prompt    string   `json:"prompt"`
	DependsOn []string `json:"dependsOn,omitempty"`
	Status    string   `json:"status"`
	SessionID string   `json:"sessionID,omitempty"`
	Summary   string   `json:"summary,omitempty"`
	Error     string   `json:"error,omitempty"`
	StartedAt int64    `json:"startedAt,omitempty"`
	EndedAt   int64    `json:"endedAt,omitempty"`
}

type Plan struct {
	ID              string      `json:"id"`
	ParentSessionID string      `json:"parentSessionID,omitempty"`
	Title           string      `json:"title"`
	Directory       string      `json:"directory"`
	Mode            string      `json:"mode,omitempty"`
	Provider        string      `json:"provider,omitempty"`
	Model           string      `json:"model,omitempty"`
	Concurrency     int         `json:"concurrency"`
	Isolated        bool        `json:"isolated"` // every task runs in its own git worktree
	Status          string      `json:"status"`
	Tasks           []TaskState `json:"tasks"`
	CreatedAt       int64       `json:"createdAt"`
	UpdatedAt       int64       `json:"updatedAt"`
	MergedAt        int64       `json:"mergedAt,omitempty"`
	Error           string      `json:"error,omitempty"`
}

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusBlocked   = "blocked"
	StatusCancelled = "cancelled"
)

func normalizePlan(req PlanRequest) (PlanRequest, error) {
	req.ParentSessionID = strings.TrimSpace(req.ParentSessionID)
	req.Title = strings.TrimSpace(req.Title)
	req.Provider = strings.TrimSpace(req.Provider)
	if req.Provider == "" {
		req.Provider = strings.TrimSpace(req.ProviderAlias)
	}
	req.ProviderAlias = ""
	req.Model = strings.TrimSpace(req.Model)
	if req.Concurrency <= 0 {
		req.Concurrency = 3
	}
	if req.Concurrency > 8 {
		req.Concurrency = 8
	}
	if req.Mode == "" {
		req.Mode = "build"
	}
	if len(req.Tasks) == 0 {
		return req, errors.New("at least one task is required")
	}
	if len(req.Tasks) > 20 {
		return req, errors.New("a plan supports at most 20 tasks")
	}
	seen := make(map[string]bool, len(req.Tasks))
	prompts := 0
	for i := range req.Tasks {
		task := &req.Tasks[i]
		task.ID = strings.TrimSpace(task.ID)
		task.Prompt = strings.TrimSpace(task.Prompt)
		if task.ID == "" {
			return req, fmt.Errorf("task %d requires an id", i+1)
		}
		if !validID(task.ID) {
			return req, fmt.Errorf("task id %q may only contain letters, digits, dash and underscore", task.ID)
		}
		if seen[task.ID] {
			return req, fmt.Errorf("duplicate task id %q", task.ID)
		}
		if task.Prompt == "" {
			return req, fmt.Errorf("task %q requires a prompt", task.ID)
		}
		if len(task.Prompt) > 20000 {
			return req, fmt.Errorf("task %q prompt is too long", task.ID)
		}
		prompts += len(task.Prompt)
		seen[task.ID] = true
	}
	if prompts > 120000 {
		return req, errors.New("plan prompts exceed the total size limit")
	}
	for i := range req.Tasks {
		task := &req.Tasks[i]
		for j, dep := range task.DependsOn {
			dep = strings.TrimSpace(dep)
			task.DependsOn[j] = dep
			if dep == task.ID {
				return req, fmt.Errorf("task %q cannot depend on itself", task.ID)
			}
			if !seen[dep] {
				return req, fmt.Errorf("task %q depends on unknown task %q", task.ID, dep)
			}
		}
	}
	if err := validateAcyclic(req.Tasks); err != nil {
		return req, err
	}
	if req.Title == "" {
		req.Title = "Convoy " + time.Now().Format("2006-01-02 15:04")
	}
	return req, nil
}

func validID(value string) bool {
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return value != ""
}

func validateAcyclic(tasks []TaskSpec) error {
	indegree := make(map[string]int, len(tasks))
	edges := make(map[string][]string, len(tasks))
	for _, task := range tasks {
		indegree[task.ID] = len(task.DependsOn)
		for _, dep := range task.DependsOn {
			edges[dep] = append(edges[dep], task.ID)
		}
	}
	queue := make([]string, 0, len(tasks))
	for id, degree := range indegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, next := range edges[id] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if visited != len(tasks) {
		return errors.New("task dependencies contain a cycle")
	}
	return nil
}

func sortedReady(plan *Plan) []int {
	var ready []int
	pending := make(map[string]string, len(plan.Tasks))
	for _, task := range plan.Tasks {
		pending[task.ID] = task.Status
	}
	for i := range plan.Tasks {
		task := &plan.Tasks[i]
		if task.Status != StatusQueued {
			continue
		}
		blocked := false
		ok := true
		for _, dep := range task.DependsOn {
			status := pending[dep]
			if status == StatusFailed || status == StatusBlocked || status == StatusCancelled {
				blocked = true
				break
			}
			if status != StatusSucceeded {
				ok = false
			}
		}
		if blocked {
			task.Status = StatusBlocked
			continue
		}
		if ok {
			ready = append(ready, i)
		}
	}
	sort.Ints(ready)
	return ready
}

// leafSessionIDs returns the session IDs of tasks that no other task in the
// plan depends on (the DAG sinks), preserving plan task order. Each sink
// already includes merged changes from its transitive prerequisites.
func leafSessionIDs(plan Plan) []string {
	depended := make(map[string]bool, len(plan.Tasks))
	for _, task := range plan.Tasks {
		for _, dep := range task.DependsOn {
			depended[dep] = true
		}
	}
	out := make([]string, 0, len(plan.Tasks))
	for _, task := range plan.Tasks {
		if !depended[task.ID] && strings.TrimSpace(task.SessionID) != "" {
			out = append(out, strings.TrimSpace(task.SessionID))
		}
	}
	return out
}

func convoyPrompt(spec TaskSpec, isolated bool) string {
	var b strings.Builder
	b.WriteString("Hoxi convoy task `")
	b.WriteString(spec.ID)
	b.WriteString("`.\n")
	if len(spec.DependsOn) > 0 {
		b.WriteString("Completed prerequisites: ")
		b.WriteString(strings.Join(spec.DependsOn, ", "))
		b.WriteString(". Their changes are already in this workspace.\n")
	}
	b.WriteString("Work only within the workspace, report the files you changed, and end with a short hand-off summary.\n")
	if isolated {
		b.WriteString("Do not commit: leave your changes uncommitted so dependent tasks can build on them.\n")
	}
	b.WriteString("\n")
	b.WriteString(spec.Prompt)
	return b.String()
}

func canonicalDirectory(directory string) (string, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return "", errors.New("directory is required")
	}
	abs, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return "", errors.New("directory does not exist")
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if root := hoxipaths.ProjectRootForWorktree(abs); root != "" {
		if st, err := os.Stat(root); err == nil && st.IsDir() {
			if resolved, err := filepath.EvalSymlinks(root); err == nil {
				abs = resolved
			} else {
				abs = root
			}
		}
	}
	return abs, nil
}

func newPlanID() string {
	var buf [6]byte
	_, _ = rand.Read(buf[:])
	return "plan_" + strconv.FormatInt(time.Now().UnixMilli(), 36) + "_" + hex.EncodeToString(buf[:])
}

func clip(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "…"
}

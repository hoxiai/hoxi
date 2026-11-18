package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// TodoItem matches Hoxi's frontend RawTodoItem contract (web/src/store/task-progress.ts).
type TodoItem struct {
	ID       string `json:"id"`
	Content  string `json:"content"`
	Status   string `json:"status"`
	Priority string `json:"priority,omitempty"`
}

const maxTrackedTodoSessions = 256

var (
	sessionTodoMu    sync.Mutex
	sessionTodoMap   = make(map[string][]TodoItem)
	sessionTodoOrder []string
)

func normalizeSessionTodoKey(sessionID string) string {
	s := strings.TrimSpace(sessionID)
	if s == "" {
		return "_default"
	}
	return s
}

func storeSessionTodos(sessionID string, items []TodoItem) {
	key := normalizeSessionTodoKey(sessionID)
	copied := make([]TodoItem, len(items))
	copy(copied, items)

	sessionTodoMu.Lock()
	defer sessionTodoMu.Unlock()
	if _, exists := sessionTodoMap[key]; !exists {
		for len(sessionTodoOrder) >= maxTrackedTodoSessions {
			oldest := sessionTodoOrder[0]
			sessionTodoOrder = sessionTodoOrder[1:]
			delete(sessionTodoMap, oldest)
		}
		sessionTodoOrder = append(sessionTodoOrder, key)
	}
	sessionTodoMap[key] = copied
}

func loadSessionTodos(sessionID string) ([]TodoItem, bool) {
	key := normalizeSessionTodoKey(sessionID)
	sessionTodoMu.Lock()
	items, ok := sessionTodoMap[key]
	sessionTodoMu.Unlock()
	if !ok {
		return nil, false
	}
	copied := make([]TodoItem, len(items))
	copy(copied, items)
	return copied, true
}

// SeedSessionTodosIfAbsent populates the in-memory todo checklist for sessionID
// from persisted session history (e.g. after process restart) without overwriting
// any checklist already written during the current process lifetime.
func SeedSessionTodosIfAbsent(sessionID string, rawItems []TodoItem) {
	trimmedID := strings.TrimSpace(sessionID)
	if trimmedID == "" || len(rawItems) == 0 {
		return
	}
	normalized := make([]TodoItem, 0, len(rawItems))
	for i, item := range rawItems {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = fmt.Sprintf("%d", i+1)
		}
		normalized = append(normalized, TodoItem{
			ID:       id,
			Content:  content,
			Status:   normalizeTodoStatus(item.Status),
			Priority: strings.TrimSpace(item.Priority),
		})
	}
	if len(normalized) == 0 {
		return
	}
	sessionTodoMu.Lock()
	defer sessionTodoMu.Unlock()
	if _, exists := sessionTodoMap[trimmedID]; exists {
		return
	}
	for len(sessionTodoOrder) >= maxTrackedTodoSessions {
		oldest := sessionTodoOrder[0]
		sessionTodoOrder = sessionTodoOrder[1:]
		delete(sessionTodoMap, oldest)
	}
	sessionTodoOrder = append(sessionTodoOrder, trimmedID)
	sessionTodoMap[trimmedID] = normalized
}

// DeleteSessionTodos removes any cached todo checklist for a deleted session.
func DeleteSessionTodos(sessionID string) {
	trimmedID := strings.TrimSpace(sessionID)
	if trimmedID == "" {
		return
	}
	sessionTodoMu.Lock()
	defer sessionTodoMu.Unlock()
	if _, exists := sessionTodoMap[trimmedID]; !exists {
		return
	}
	delete(sessionTodoMap, trimmedID)
	for i, k := range sessionTodoOrder {
		if k == trimmedID {
			sessionTodoOrder = append(sessionTodoOrder[:i], sessionTodoOrder[i+1:]...)
			break
		}
	}
}

func normalizeTodoStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "in_progress", "in-progress", "active", "running":
		return "in_progress"
	case "completed", "done", "complete":
		return "completed"
	case "cancelled", "canceled":
		return "cancelled"
	default:
		return "pending"
	}
}

type todoWriteTool struct{}

func (t *todoWriteTool) Name() string     { return "todowrite" }
func (t *todoWriteTool) IsReadOnly() bool { return false }
func (t *todoWriteTool) Description() string {
	return "Create or update the structured task checklist for the current session so progress stays visible in the UI."
}
func (t *todoWriteTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"todos": map[string]any{
				"type":        "array",
				"description": "Complete ordered list of todos for the current task",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":       map[string]any{"type": "string", "description": "Stable identifier (e.g. '1', 'step-1')"},
						"content":  map[string]any{"type": "string", "description": "Concise task description"},
						"status":   map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed", "cancelled"}},
						"priority": map[string]any{"type": "string", "enum": []string{"high", "medium", "low"}},
					},
					"required": []string{"content", "status"},
				},
			},
		},
		"required": []string{"todos"},
	}
}

func (t *todoWriteTool) Execute(_ context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		Todos []TodoItem `json:"todos"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "todowrite", IsError: true}, err
	}
	normalized := make([]TodoItem, 0, len(args.Todos))
	completed := 0
	for i, item := range args.Todos {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = fmt.Sprintf("%d", i+1)
		}
		status := normalizeTodoStatus(item.Status)
		if status == "completed" {
			completed++
		}
		normalized = append(normalized, TodoItem{
			ID:       id,
			Content:  content,
			Status:   status,
			Priority: strings.TrimSpace(item.Priority),
		})
	}
	storeSessionTodos(tc.SessionID, normalized)

	encoded, _ := json.MarshalIndent(normalized, "", "  ")
	title := fmt.Sprintf("%d/%d todos completed", completed, len(normalized))
	return ToolOutput{
		Title:    title,
		Output:   string(encoded),
		Metadata: map[string]any{"todos": normalized},
	}, nil
}

type todoReadTool struct{}

func (t *todoReadTool) Name() string     { return "todoread" }
func (t *todoReadTool) IsReadOnly() bool { return true }
func (t *todoReadTool) Description() string {
	return "Read the current session's structured task checklist."
}
func (t *todoReadTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *todoReadTool) Execute(_ context.Context, tc ToolContext, _ json.RawMessage) (ToolOutput, error) {
	items, ok := loadSessionTodos(tc.SessionID)
	if !ok || items == nil {
		items = []TodoItem{}
	}
	encoded, _ := json.MarshalIndent(items, "", "  ")
	return ToolOutput{
		Title:    fmt.Sprintf("%d todos", len(items)),
		Output:   string(encoded),
		Metadata: map[string]any{"todos": items},
	}, nil
}

package orchestration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPlanRunsDependenciesInOrder(t *testing.T) {
	var (
		mu      sync.Mutex
		nextID  int
		prompts = make(map[string]string)
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == http.MethodPost && path == "/session":
			mu.Lock()
			nextID++
			id := fmt.Sprintf("ses_%d", nextID)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/message"):
			var body struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			prompts[strings.Split(path, "/")[2]] = body.Parts[0].Text
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "msg_1"})
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/message"):
			id := strings.Split(path, "/")[2]
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{
				"info":  map[string]string{"role": "assistant"},
				"parts": []map[string]string{{"type": "text", "text": "finished " + id}},
			}})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(server.Close)

	svc := NewService(0, "secret")
	svc.base = server.URL
	dir := t.TempDir()
	plan, err := svc.Create(PlanRequest{
		Directory:   dir,
		Concurrency: 2,
		Tasks: []TaskSpec{
			{ID: "a", Prompt: "alpha work"},
			{ID: "b", Prompt: "beta work", DependsOn: []string{"a"}},
			{ID: "c", Prompt: "gamma work"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := svc.Get(plan.ID)
		if ok && terminal(current.Status) {
			plan = current
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if plan.Status != StatusSucceeded {
		t.Fatalf("plan did not succeed: %#v", plan)
	}
	sessions := map[string]string{}
	for _, task := range plan.Tasks {
		if task.Status != StatusSucceeded || task.SessionID == "" || task.Summary == "" {
			t.Fatalf("task not completed: %#v", task)
		}
		sessions[task.ID] = task.SessionID
	}
	mu.Lock()
	beta := prompts[sessions["b"]]
	mu.Unlock()
	if !strings.Contains(beta, "Completed prerequisites: a") || !strings.Contains(beta, "beta work") {
		t.Fatalf("dependent prompt missing context: %q", beta)
	}
	if len(sessions) != 3 {
		t.Fatalf("expected three sessions, got %#v", sessions)
	}
}

func TestCancelMarksQueuedTasks(t *testing.T) {
	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/session" {
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "ses_blocked"})
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/message") {
			<-block
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(block) })

	svc := NewService(0, "secret")
	svc.base = server.URL
	plan, err := svc.Create(PlanRequest{
		Directory:   t.TempDir(),
		Concurrency: 1,
		Tasks: []TaskSpec{
			{ID: "a", Prompt: "alpha"},
			{ID: "b", Prompt: "beta", DependsOn: []string{"a"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := svc.Cancel(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != StatusCancelled {
		t.Fatalf("status = %q", cancelled.Status)
	}
	if cancelled.Tasks[1].Status != StatusCancelled {
		t.Fatalf("queued task should be cancelled: %#v", cancelled.Tasks[1])
	}
}

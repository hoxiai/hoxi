package orchestration

import (
	"strings"
	"testing"
)

func TestNormalizePlanValidatesDependencies(t *testing.T) {
	base := PlanRequest{
		Directory: "/repo",
		Tasks: []TaskSpec{
			{ID: "a", Prompt: "first"},
			{ID: "b", Prompt: "second", DependsOn: []string{"a"}},
		},
	}
	if _, err := normalizePlan(base); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}

	cases := []struct {
		name string
		req  PlanRequest
		want string
	}{
		{"empty", PlanRequest{}, "at least one task"},
		{"duplicate", PlanRequest{Tasks: []TaskSpec{{ID: "a", Prompt: "x"}, {ID: "a", Prompt: "y"}}}, "duplicate"},
		{"unknown dep", PlanRequest{Tasks: []TaskSpec{{ID: "a", Prompt: "x", DependsOn: []string{"missing"}}}}, "unknown task"},
		{"self dep", PlanRequest{Tasks: []TaskSpec{{ID: "a", Prompt: "x", DependsOn: []string{"a"}}}}, "itself"},
		{"cycle", PlanRequest{Tasks: []TaskSpec{
			{ID: "a", Prompt: "x", DependsOn: []string{"b"}},
			{ID: "b", Prompt: "y", DependsOn: []string{"a"}},
		}}, "cycle"},
		{"bad id", PlanRequest{Tasks: []TaskSpec{{ID: "a b", Prompt: "x"}}}, "may only contain"},
		{"empty prompt", PlanRequest{Tasks: []TaskSpec{{ID: "a", Prompt: "  "}}}, "requires a prompt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normalizePlan(tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestNormalizePlanClampsConcurrency(t *testing.T) {
	req, err := normalizePlan(PlanRequest{Tasks: []TaskSpec{{ID: "a", Prompt: "x"}}, Concurrency: 99})
	if err != nil {
		t.Fatal(err)
	}
	if req.Concurrency != 8 {
		t.Fatalf("concurrency = %d, want 8", req.Concurrency)
	}
	req, _ = normalizePlan(PlanRequest{Tasks: []TaskSpec{{ID: "a", Prompt: "x"}}})
	if req.Concurrency != 3 {
		t.Fatalf("default concurrency = %d, want 3", req.Concurrency)
	}
}

func TestSortedReadyBlocksDependentTasks(t *testing.T) {
	plan := Plan{Tasks: []TaskState{
		{ID: "a", Status: StatusFailed},
		{ID: "b", Status: StatusQueued, DependsOn: []string{"a"}},
		{ID: "c", Status: StatusQueued},
		{ID: "d", Status: StatusQueued, DependsOn: []string{"c"}},
	}}
	ready := sortedReady(&plan)
	if len(ready) != 1 || plan.Tasks[ready[0]].ID != "c" {
		t.Fatalf("unexpected ready tasks: %#v", ready)
	}
	if plan.Tasks[1].Status != StatusBlocked {
		t.Fatalf("dependent task should be blocked: %#v", plan.Tasks[1])
	}
}

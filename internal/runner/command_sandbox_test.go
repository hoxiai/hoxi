package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/sandbox"
	"github.com/hoxiai/hoxi/internal/worktree"
)

func TestConfineRuntimeCommandDisabled(t *testing.T) {
	t.Parallel()
	svc := &Service{}
	bin, args, err := svc.confineRuntimeCommand(SessionRecord{WorktreePath: t.TempDir()}, "/bin/echo", []string{"hi"})
	if err != nil {
		t.Fatal(err)
	}
	if bin != "/bin/echo" || len(args) != 1 || args[0] != "hi" {
		t.Fatalf("disabled sandbox must not wrap: %s %#v", bin, args)
	}
}

func TestConfineRuntimeCommandWrapsWorkspace(t *testing.T) {
	t.Parallel()
	if !sandbox.Available() {
		t.Skip("seatbelt backend unavailable")
	}
	workspace := t.TempDir()
	project := filepath.Dir(workspace)
	svc := &Service{}
	bin, args, err := svc.confineRuntimeCommand(
		SessionRecord{WorktreePath: workspace, ProjectDir: project, CommandSandbox: true},
		"/bin/echo",
		[]string{"hi"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if bin != "/usr/bin/sandbox-exec" {
		t.Fatalf("expected seatbelt binary, got %q", bin)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, workspace) {
		t.Fatalf("profile must include the workspace: %s", joined)
	}
	if !strings.Contains(joined, "(deny default)") {
		t.Fatalf("profile must deny by default: %s", joined)
	}
	if args[len(args)-2] != "/bin/echo" || args[len(args)-1] != "hi" {
		t.Fatalf("wrapped command lost its arguments: %#v", args)
	}
}

func TestReadableGitDir(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	if got := writableGitDir(project); got != "" {
		t.Fatalf("expected no git dir, got %q", got)
	}
	git := filepath.Join(project, ".git")
	if err := os.Mkdir(git, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := writableGitDir(project); got != git {
		t.Fatalf("expected %q, got %q", git, got)
	}
}

func TestOpenCodeConversationAndAdoptPreserveCommandSandbox(t *testing.T) {
	if !sandbox.Available() {
		t.Skip("seatbelt backend unavailable")
	}
	t.Setenv("HOXI_HOME", t.TempDir())
	project := t.TempDir()

	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/config/providers":
			_ = json.NewEncoder(w).Encode(map[string]any{"providers": []any{}, "default": map[string]string{}})
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			_ = json.NewEncoder(w).Encode(SessionInfo{ID: "ses_oc_created", Title: "Created", Directory: project})
		case r.Method == http.MethodGet && r.URL.Path == "/session/ses_oc_adopted":
			_ = json.NewEncoder(w).Encode(SessionInfo{ID: "ses_oc_adopted", Title: "Adopted", Directory: project})
		case r.Method == http.MethodGet && r.URL.Path == "/session/status":
			_ = json.NewEncoder(w).Encode(map[string]SessionStatus{})
		case r.Method == http.MethodGet && r.URL.Path == "/session/ses_oc_adopted/message":
			_ = json.NewEncoder(w).Encode([]MessageTurn{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer engine.Close()

	svc := NewService(project, worktree.NewManager(project), nil, filepath.Join(t.TempDir(), "sessions.json"))
	svc.ConfigureOpenCode(engine.URL, "secret")

	info, status, err := svc.CreateConversation(context.Background(), CreateSessionRequest{
		Runtime:        "opencode",
		Directory:      project,
		CommandSandbox: true,
	}, project)
	if err != nil || status != http.StatusOK {
		t.Fatalf("CreateConversation status=%d err=%v", status, err)
	}
	if !info.CommandSandbox {
		t.Fatalf("CreateConversation dropped CommandSandbox: %+v", info)
	}
	if _, enabled, err := sandbox.NewManager("").Get("ses_oc_created"); err != nil || !enabled {
		t.Fatalf("expected OpenCode sandbox manager entry for ses_oc_created, enabled=%v err=%v", enabled, err)
	}

	if _, err := sandbox.NewManager("").Set("ses_oc_adopted", project, true, sandbox.NetworkAllow); err != nil {
		t.Fatal(err)
	}
	adopted, err := svc.adoptOpenCodeSession(context.Background(), "ses_oc_adopted", project)
	if err != nil {
		t.Fatalf("adoptOpenCodeSession: %v", err)
	}
	if !adopted.CommandSandbox {
		t.Fatalf("adoptOpenCodeSession dropped CommandSandbox: %+v", adopted)
	}
}


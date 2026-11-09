package sandbox

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnDemandProfileRefusesBroadWorkspaces(t *testing.T) {
	home := resolvePathSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	for _, dir := range []string{
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".hoxi", "workspaces", "default"),
		filepath.Join(home, "Library", "Application Support"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, workspace := range []string{
		"/",
		home,
		filepath.Dir(home),
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".hoxi", "workspaces", "default"),
		filepath.Join(home, "Library", "Application Support"),
	} {
		if _, err := OnDemandProfile(workspace, nil); err == nil {
			t.Errorf("on-demand profile accepted workspace %s", workspace)
		}
	}
	project := filepath.Join(home, "code", "app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := OnDemandProfile(project, nil); err != nil {
		t.Fatalf("on-demand profile refused a project under home: %v", err)
	}
}

func TestOnDemandProfileTextIsLocalOnly(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:5488")
	t.Setenv("ALL_PROXY", "socks5://localhost:5486")
	t.Setenv("HTTP_PROXY", "http://proxy.example.com:8080")
	proxyPortCacheMu.Lock()
	proxyPortCache.at = proxyPortCache.at.AddDate(-1, 0, 0)
	proxyPortCacheMu.Unlock()
	profile, err := OnDemandProfile(t.TempDir(), []int{4242, 0, 4242})
	if err != nil {
		t.Fatal(err)
	}
	text, err := ProfileText(profile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`(allow network-outbound (remote ip "localhost:*"))`,
		`(remote ip "localhost:4242")`,
		`(remote ip "localhost:5488")`,
		`(remote ip "localhost:5486")`,
		`hoxi-opencode-plugin-`,
		`\.env(\.[^/]*)?$`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("on-demand profile is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "(allow network-outbound)\n") || strings.Contains(text, "localhost:8080") {
		t.Fatalf("on-demand profile must not open the internet or list a remote proxy:\n%s", text)
	}
	if strings.Count(text, `(remote ip "localhost:4242")`) != 1 {
		t.Fatal("deny ports must be deduplicated")
	}
	if strings.Index(text, "(remote unix-socket)") > strings.Index(text, "credential agent socket") ||
		strings.Index(text, "(remote unix-socket)") > strings.Index(text, "control or proxy port") {
		t.Fatal("the socket and port denies must follow the local allow so they win")
	}
}

// The profile is enforced, not only rendered: a command under it can write
// the workspace and reach a local server, and cannot touch .git, read .env
// or key files, write outside, or reach a denied control port.
func TestOnDemandProfileEnforced(t *testing.T) {
	if !Available() {
		t.Skip("seatbelt backend unavailable")
	}
	workspace := resolvePathSymlinks(t.TempDir())
	for _, dir := range []string{".git", "sub"} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{
		"readme.txt": "hello", ".env.fishing": "DB=secret", "sub/.env": "K=v", "account.key": "k",
	} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	open := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer open.Close()
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer control.Close()
	profile, err := OnDemandProfile(workspace, []int{PortOf(control.URL)})
	if err != nil {
		t.Fatal(err)
	}
	run := func(command string) error {
		bin, args, err := WrapShell(profile, command)
		if err != nil {
			t.Fatal(err)
		}
		return runCommand(bin, args...)
	}
	home, _ := os.UserHomeDir()
	outside := filepath.Join(home, "hoxi-on-demand-outside-test.txt")
	_ = os.Remove(outside)
	defer os.Remove(outside)
	allowed := []string{
		"echo ok > " + shellQuote(filepath.Join(workspace, "out.txt")),
		"cat " + shellQuote(filepath.Join(workspace, "readme.txt")),
		"/usr/bin/curl -sf --noproxy '*' " + shellQuote(open.URL),
	}
	for _, command := range allowed {
		if err := run(command); err != nil {
			t.Errorf("on-demand sandbox blocked %q: %v", command, err)
		}
	}
	refused := []string{
		"echo bad > " + shellQuote(filepath.Join(workspace, ".git", "config")),
		"cat " + shellQuote(filepath.Join(workspace, ".env.fishing")),
		"cat " + shellQuote(filepath.Join(workspace, "sub", ".env")),
		"cat " + shellQuote(filepath.Join(workspace, "account.key")),
		"echo bad > " + shellQuote(outside),
		"/usr/bin/curl -sf --noproxy '*' " + shellQuote(control.URL),
	}
	for _, command := range refused {
		if err := run(command); err == nil {
			t.Errorf("on-demand sandbox allowed %q", command)
		}
	}
}

func TestOnDemandTrustedOnlyForUnchangedOwnProfile(t *testing.T) {
	m := NewManager(t.TempDir())
	workspace := t.TempDir()
	other := t.TempDir()
	path, err := m.WriteOnDemand(workspace, []int{4242})
	if err != nil {
		t.Fatal(err)
	}
	if !m.OnDemandTrusted(path, workspace) {
		t.Fatal("freshly written on-demand profile is not trusted")
	}
	if m.OnDemandTrusted(path, other) {
		t.Fatal("a profile for one workspace must not cover another")
	}
	otherPath, _ := m.OnDemandProfilePath(other)
	if m.OnDemandTrusted(otherPath, other) {
		t.Fatal("a profile this process never wrote must not be trusted")
	}
	if err := os.WriteFile(path, []byte("(version 1)(allow default)"), 0o600); err != nil {
		t.Fatal(err)
	}
	if m.OnDemandTrusted(path, workspace) {
		t.Fatal("a tampered on-demand profile must not be trusted")
	}
	if again, err := m.WriteOnDemand(workspace, []int{4242}); err != nil || again != path || !m.OnDemandTrusted(path, workspace) {
		t.Fatalf("rewriting restores trust: %v %s", err, again)
	}
}

func TestLoopbackPort(t *testing.T) {
	for raw, want := range map[string]int{
		"http://127.0.0.1:5488":       5488,
		"socks5://localhost:5486":     5486,
		"127.0.0.1:7890":              7890,
		"http://[::1]:8080":           8080,
		"http://proxy.example.com:80": 0,
		"http://127.0.0.1":            0,
		"":                            0,
	} {
		if got := loopbackPort(raw); got != want {
			t.Errorf("loopbackPort(%q) = %d, want %d", raw, got, want)
		}
	}
}

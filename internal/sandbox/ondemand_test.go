package sandbox

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if strings.Contains(text, "(allow network-outbound)\n") || strings.Contains(text, `(remote ip "localhost:8080")`) {
		t.Fatalf("on-demand profile must not open the internet or list a remote proxy:\n%s", text)
	}
	if strings.Count(text, `(remote ip "localhost:4242")`) != 1 {
		t.Fatal("deny ports must be deduplicated")
	}
	if strings.Index(text, "(remote unix-socket)") > strings.Index(text, "credential agent socket") ||
		strings.Index(text, "(remote unix-socket)") > strings.Index(text, "did not open") {
		t.Fatal("the socket and port denies must follow the local allow so they win")
	}
	if strings.Contains(text, "(allow network-outbound (remote unix-socket))") {
		t.Fatalf("Unix sockets must be limited to the workspace:\n%s", text)
	}
	for _, want := range []string{
		filepath.Join(".codex", "auth.json"), filepath.Join(".claude", "ide"),
		filepath.Join(".local", "share", "opencode", "auth.json"), filepath.Join(".gemini", "oauth_creds.json"),
	} {
		if !strings.Contains(text, want) {
			t.Errorf("on-demand profile does not deny reading %s", want)
		}
	}
}

// unixConnect is a command that exits 0 only if it can connect to path.
func unixConnect(path string) string {
	return `/usr/bin/python3 -I -c 'import socket, sys; s = socket.socket(socket.AF_UNIX); s.settimeout(2); s.connect(sys.argv[1])' ` + shellQuote(path)
}

// forgetPortSnapshot makes the next profile list the ports in use now.
func forgetPortSnapshot() {
	listeningPortsMu.Lock()
	listeningPortsCache.at = time.Time{}
	listeningPortsMu.Unlock()
}

func TestParseLocalPorts(t *testing.T) {
	tcp := `Active Internet connections (including servers)
Proto Recv-Q Send-Q  Local Address          Foreign Address        (state)
tcp4       0      0  127.0.0.1.5488         *.*                    LISTEN
tcp46      0      0  *.7890                 *.*                    LISTEN
tcp6       0      0  ::1.49459              *.*                    LISTEN
tcp4       0      0  192.168.1.5.8080       *.*                    LISTEN
tcp4       0      0  127.0.0.1.5488         127.0.0.1.61000        ESTABLISHED
tcp4       0      0  127.0.0.1.61000        127.0.0.1.5488         ESTABLISHED
`
	if got := uniquePorts(parseLocalPorts(tcp, "tcp")); fmt.Sprint(got) != "[5488 7890 49459]" {
		t.Errorf("tcp ports = %v", got)
	}
	udp := `Active Internet connections (including servers)
Proto Recv-Q Send-Q  Local Address          Foreign Address        (state)
udp4       0      0  *.5353                 *.*
udp4       0      0  127.0.0.1.53           *.*
udp4       0      0  192.168.1.5.61234      1.1.1.1.53
udp6       0      0  ::1.1053               *.*
`
	if got := uniquePorts(parseLocalPorts(udp, "udp")); fmt.Sprint(got) != "[53 1053 5353]" {
		t.Errorf("udp ports = %v", got)
	}
}

// Without a list of the ports in use the profile cannot keep the command
// offline, so no profile is made and the command is asked about instead.
func TestOnDemandProfileFailsClosedWithoutPortSnapshot(t *testing.T) {
	saved := netstatPath
	netstatPath = filepath.Join(t.TempDir(), "missing-netstat")
	forgetPortSnapshot()
	defer func() { netstatPath = saved; forgetPortSnapshot() }()
	if _, err := OnDemandProfile(t.TempDir(), nil); err == nil {
		t.Fatal("on-demand profile was made without a port snapshot")
	}
}

// The profile is enforced, not only rendered: a command under it can write
// the workspace and reach a local server, and cannot touch .git, read .env
// or key files, write outside, or reach a denied control port.
func TestOnDemandProfileEnforced(t *testing.T) {
	if !Available() {
		t.Skip("seatbelt backend unavailable")
	}
	// A short workspace path keeps its Unix socket under the 104-byte limit.
	workspace, err := os.MkdirTemp("/private/tmp", "hxw")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspace)
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
	// Servers already running when the profile is written (a local proxy,
	// another agent's API) are unreachable, on IPv4 and IPv6 alike.
	running := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer running.Close()
	running6 := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	if l, err := net.Listen("tcp", "[::1]:0"); err == nil {
		running6.Listener = l
		running6.Start()
		defer running6.Close()
	}
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer control.Close()
	// A Unix socket server outside the workspace (an IDE or agent bridge)
	// is unreachable; one inside it is not. Socket paths stay under 104 bytes.
	sockDir, err := os.MkdirTemp("/private/tmp", "hxs")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	outsideSock := filepath.Join(sockDir, "o.sock")
	insideSock := filepath.Join(workspace, "i.sock")
	for _, path := range []string{outsideSock, insideSock} {
		l, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
	}
	forgetPortSnapshot()
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
		// A server the command starts itself stays reachable.
		`/usr/bin/python3 -I -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); s.listen(1); socket.create_connection(s.getsockname(), 2).close()'`,
		unixConnect(insideSock),
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
		"/usr/bin/curl -sf --noproxy '*' " + shellQuote(running.URL),
		unixConnect(outsideSock),
	}
	if running6.Listener != nil {
		refused = append(refused, "/usr/bin/curl -sfg --noproxy '*' "+shellQuote(running6.URL))
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

// Other agents' sign-in tokens and IDE bridge lock files are unreadable from
// the on-demand sandbox; an ordinary file next to them is not.
func TestOnDemandProfileHidesAgentCredentials(t *testing.T) {
	if !Available() {
		t.Skip("seatbelt backend unavailable")
	}
	home := resolvePathSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	workspace := filepath.Join(home, "code", "app")
	files := map[string]string{
		".codex/auth.json": "{}", ".claude/.credentials.json": "{}", ".claude.json": "{}", ".claude/ide/4242.lock": "{}",
		".local/share/opencode/auth.json": "{}", ".gemini/oauth_creds.json": "{}", ".config/github-copilot/apps.json": "{}",
		"notes.txt": "plain",
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		path := filepath.Join(home, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	profile, err := OnDemandProfile(workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name := range files {
		bin, args, err := WrapShell(profile, "cat "+shellQuote(filepath.Join(home, filepath.FromSlash(name)))+" > /dev/null")
		if err != nil {
			t.Fatal(err)
		}
		err = runCommand(bin, args...)
		if name == "notes.txt" {
			if err != nil {
				t.Errorf("on-demand sandbox blocked an ordinary file: %v", err)
			}
		} else if err == nil {
			t.Errorf("on-demand sandbox let a command read %s", name)
		}
	}
}

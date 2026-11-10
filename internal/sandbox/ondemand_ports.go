package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
)

// A loopback-only sandbox is not offline while other programs listen on
// loopback: a local proxy (any of its ports), another agent's server or an
// IDE bridge would carry the command off the machine or out of the sandbox.
// Port blocklists miss some of them, so the on-demand profile denies every
// TCP and UDP port already in use on loopback when it is written. Servers
// the confined command starts itself bind new ports and stay reachable.

const listeningPortsTTL = 3 * time.Second

var (
	listeningPortsCache struct {
		at       time.Time
		tcp, udp []int
	}
	listeningPortsMu sync.Mutex
	// netstatPath is a variable so tests can make the snapshot fail.
	netstatPath = "/usr/sbin/netstat"
)

// ListeningLocalPorts returns the TCP ports listening and the UDP ports bound
// on a loopback or wildcard address. An error means the snapshot could not
// be taken; callers must then refuse to confine instead of assuming none.
// They are kept apart so a server the command starts on a free TCP port is
// not refused because some UDP socket has the same number.
func ListeningLocalPorts() (tcp, udp []int, err error) {
	listeningPortsMu.Lock()
	defer listeningPortsMu.Unlock()
	if !listeningPortsCache.at.IsZero() && time.Since(listeningPortsCache.at) < listeningPortsTTL {
		return append([]int(nil), listeningPortsCache.tcp...), append([]int(nil), listeningPortsCache.udp...), nil
	}
	found := make(map[string][]int)
	for _, proto := range []string{"tcp", "udp"} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		out, err := subproc.CommandContext(ctx, netstatPath, "-an", "-p", proto).Output()
		cancel()
		if err != nil {
			return nil, nil, err
		}
		found[proto] = uniquePorts(parseLocalPorts(string(out), proto))
	}
	listeningPortsCache.at, listeningPortsCache.tcp, listeningPortsCache.udp = time.Now(), found["tcp"], found["udp"]
	return append([]int(nil), found["tcp"]...), append([]int(nil), found["udp"]...), nil
}

// parseLocalPorts reads `netstat -an -p <proto>` output from macOS, where an
// address is written host.port (`127.0.0.1.5488`, `::1.49459`, `*.5353`).
func parseLocalPorts(out, proto string) []int {
	var ports []int
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || !strings.HasPrefix(fields[0], proto) {
			continue
		}
		if proto == "tcp" && (len(fields) < 6 || fields[5] != "LISTEN") {
			continue
		}
		local := fields[3]
		dot := strings.LastIndex(local, ".")
		if dot <= 0 {
			continue
		}
		host, rawPort := local[:dot], local[dot+1:]
		if host != "*" && host != "::1" && host != "::" && !strings.HasPrefix(host, "127.") {
			continue
		}
		if port, err := strconv.Atoi(rawPort); err == nil && port > 0 && port <= 65535 {
			ports = append(ports, port)
		}
	}
	return ports
}

// agentCredentialRules are other agents' sign-in tokens and the lock files
// that hold local IDE bridge tokens. A session sandbox cannot deny them (its
// runtime reads its own), but a confined shell command never needs them.
func agentCredentialRules() []PathRule {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	home = resolvePathSymlinks(home)
	var rules []PathRule
	for _, file := range []string{
		filepath.Join(".codex", "auth.json"),
		filepath.Join(".claude", ".credentials.json"),
		".claude.json",
		filepath.Join(".local", "share", "opencode", "auth.json"),
		filepath.Join(".local", "share", "opencode", "mcp-auth.json"),
		filepath.Join(".gemini", "oauth_creds.json"),
	} {
		rules = append(rules, PathRule{Path: filepath.Join(home, file), Kind: PathLiteral})
	}
	for _, dir := range []string{filepath.Join(".claude", "ide"), filepath.Join(".config", "github-copilot")} {
		rules = append(rules, PathRule{Path: filepath.Join(home, dir), Kind: PathSubtree})
	}
	return rules
}

var errNoPortSnapshot = errors.New("on-demand sandbox could not list the local ports in use")

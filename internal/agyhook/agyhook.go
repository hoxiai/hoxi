// Package agyhook implements `hoxi agy-hook`, the Antigravity PreToolUse hook
// command that forwards tool calls to the Hoxi runner approval gate.
package agyhook

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// Subcommand is the argv[1] value that runs the hook instead of the app.
	Subcommand = "agy-hook"

	EnvSessionID = "HOXI_RUNNER_SESSION_ID"
	EnvHookURL   = "HOXI_HOOK_URL"
	EnvHookToken = "HOXI_HOOK_TOKEN"

	// HeaderSession carries the runner session ID next to the bearer token.
	HeaderSession = "X-Hoxi-Runner-Session"

	// TimeoutSeconds is the hooks.json timeout agy enforces before killing us.
	TimeoutSeconds = 600
	// ClientTimeout leaves agy enough slack to read our deny before its kill.
	ClientTimeout = (TimeoutSeconds - 15) * time.Second

	maxStdinBytes = 4 << 20
)

// Decision is the PreToolUse hook output understood by agy.
type Decision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

var validDecisions = map[string]bool{
	"allow":        true,
	"auto_approve": true,
	"deny":         true,
	"ask":          true,
	"force_ask":    true,
}

// Passthrough hands control back to agy's own policy. Used when agy was not
// started by Hoxi (terminal, IDE), so installing the hook changes nothing there.
var Passthrough = Decision{Decision: "allow"}

func denied(reason string) Decision {
	return Decision{Decision: "deny", Reason: reason}
}

// Run executes the hook. It always exits 0: agy treats a failing hook as a
// tool error, and a well-formed deny gives the model a readable reason.
func Run(stdin io.Reader, stdout io.Writer, getenv func(string) string) int {
	return RunWithClient(stdin, stdout, getenv, nil)
}

func RunWithClient(stdin io.Reader, stdout io.Writer, getenv func(string) string, client *http.Client) int {
	body, _ := io.ReadAll(io.LimitReader(stdin, maxStdinBytes))
	writeDecision(stdout, decide(body, getenv, client))
	return 0
}

func decide(body []byte, getenv func(string) string, client *http.Client) Decision {
	sessionID := strings.TrimSpace(getenv(EnvSessionID))
	hookURL := strings.TrimSpace(getenv(EnvHookURL))
	token := strings.TrimSpace(getenv(EnvHookToken))
	if sessionID == "" || hookURL == "" || token == "" {
		return Passthrough
	}
	if !isLoopbackURL(hookURL) {
		return denied("Hoxi approval gate URL is not a loopback address")
	}
	if client == nil {
		client = &http.Client{
			Timeout:   ClientTimeout,
			Transport: &http.Transport{Proxy: nil},
		}
	}
	req, err := http.NewRequest(http.MethodPost, hookURL, bytes.NewReader(body))
	if err != nil {
		return denied("Hoxi approval gate unavailable")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(HeaderSession, sessionID)
	resp, err := client.Do(req)
	if err != nil {
		return denied("Hoxi approval gate unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return denied("Hoxi approval gate rejected the request")
	}
	var out Decision
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil || !validDecisions[out.Decision] {
		return denied("Hoxi approval gate returned an invalid decision")
	}
	return out
}

func isLoopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeDecision(w io.Writer, d Decision) {
	raw, err := json.Marshal(d)
	if err != nil {
		raw = []byte(`{"decision":"deny","reason":"Hoxi approval gate encoding error"}`)
	}
	_, _ = w.Write(append(raw, '\n'))
}

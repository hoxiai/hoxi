package runner

import (
	"regexp"
	"strings"

	"github.com/hoxiai/hoxi/internal/agentcli"
)

// A confined runtime still has the network (it calls its model), so whatever
// credential sits in its environment can leave the machine through any shell
// command it runs, whatever the static approval rules make of that command.
// sandboxedRuntimeEnv therefore drops credentials before the runtime starts.
//
// It is a deny-list, not an allow-list, on purpose: the runtime and the
// project's toolchains depend on many non-secret variables nobody can list in
// advance (PATH, locale, proxy, JAVA_HOME, NVM_DIR, PYENV_ROOT, CC, ...), and
// dropping one breaks builds in ways that look unrelated. Secrets, in
// contrast, follow a few naming conventions (…_TOKEN, …_SECRET, …_API_KEY,
// …PASSWORD) and a few value shapes (ghp_…, AKIA…, user:pass@ URLs), which
// is what isCredentialEnv matches. Agent sockets (SSH_AUTH_SOCK, the GPG
// agent) are capabilities rather than strings and are dropped by name; the
// Seatbelt profile also refuses to connect to the default agent sockets.
//
// The only credentials kept are the ones the runtime itself needs to reach
// its model (runtimeModelEnv). A shell the runtime spawns can still read
// those; that residual is documented in AGENTS.md rule 6.

// credentialEnvNames are dropped by exact name: agent sockets, askpass
// helpers, config pointers that lead to credentials outside the sandbox's
// deny-list, and git config injected through the environment (it can carry
// http.extraHeader with a token).
var credentialEnvNames = setOf(
	"SSH_AUTH_SOCK", "SSH_AGENT_PID", "GPG_AGENT_INFO",
	"GIT_ASKPASS", "SSH_ASKPASS", "SUDO_ASKPASS",
	"KUBECONFIG", "DOCKER_CONFIG", "NETRC", "AWS_SHARED_CREDENTIALS_FILE", "AWS_CONFIG_FILE",
	"GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
)

// credentialNameFragments match anywhere in the upper-cased name
// (PGPASSWORD, GITHUB_TOKEN, AWS_SECRET_ACCESS_KEY, HOMEBREW_GITHUB_API_TOKEN).
var credentialNameFragments = []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "APIKEY", "API_KEY", "ACCESS_KEY", "PRIVATE_KEY"}

// credentialNameParts match one `_`-separated part, so SSH_AUTH_SOCK and
// NPM_CONFIG__AUTH match while GIT_AUTHOR_NAME does not.
var credentialNameParts = setOf("AUTH", "PAT", "AUTHTOKEN")

// credentialValueRe matches well-known token shapes stored under a name that
// does not look secret, and URLs that embed a password.
var credentialValueRe = regexp.MustCompile(`^(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_\w{20,}|glpat-[\w-]{20,}|npm_[A-Za-z0-9]{30,}|xox[abeprs]-[\w-]{10,}|sk-[\w-]{20,}|(?:AKIA|ASIA)[A-Z0-9]{16}|hf_[A-Za-z0-9]{30,})$|^[A-Za-z][A-Za-z0-9+.-]*://[^/@\s:]+:[^/@\s]+@`)

// proxyEnvNames carry the proxy the runtime needs for its own model calls,
// even when the proxy URL embeds a password.
var proxyEnvNames = setOf("HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy")

// isCredentialEnvName reports whether a variable name looks like it holds a
// credential. The unattended egress check uses the same rule for `$NAME`
// references inside a command.
func isCredentialEnvName(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	if upper == "" {
		return false
	}
	if credentialEnvNames[upper] || strings.HasPrefix(upper, "GIT_CONFIG_KEY_") || strings.HasPrefix(upper, "GIT_CONFIG_VALUE_") {
		return true
	}
	for _, fragment := range credentialNameFragments {
		if strings.Contains(upper, fragment) {
			return true
		}
	}
	for _, part := range strings.Split(upper, "_") {
		if credentialNameParts[part] {
			return true
		}
	}
	return strings.HasSuffix(upper, "_KEY")
}

// isCredentialEnv reports whether one NAME=value entry must be dropped.
func isCredentialEnv(name, value string) bool {
	if proxyEnvNames[name] {
		return false
	}
	return isCredentialEnvName(name) || credentialValueRe.MatchString(strings.TrimSpace(value))
}

// runtimeModelEnv lists the credentials a runtime reads to call its model.
// Cloud-provider keys are kept only when the runtime is configured to use
// that provider (Claude Code on Bedrock or Vertex).
func runtimeModelEnv(runtime string, lookup func(string) string) map[string]bool {
	keep := map[string]bool{}
	add := func(names ...string) {
		for _, n := range names {
			keep[n] = true
		}
	}
	switch runtime {
	case string(RuntimeClaudeCode):
		add("ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN")
		if envTruthy(lookup("CLAUDE_CODE_USE_BEDROCK")) {
			add("AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_BEARER_TOKEN_BEDROCK")
		}
		if envTruthy(lookup("CLAUDE_CODE_USE_VERTEX")) {
			add("GOOGLE_APPLICATION_CREDENTIALS")
		}
	case string(RuntimeAntigravity), string(RuntimeGeminiCLI):
		add("GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY")
		if envTruthy(lookup("GOOGLE_GENAI_USE_VERTEXAI")) {
			add("GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION")
		}
	case string(RuntimeQoder):
		add("QODER_PERSONAL_ACCESS_TOKEN")
	case string(RuntimeCodeBuddy):
		add("CODEBUDDY_API_KEY", "CODEBUDDY_AUTH_TOKEN")
	}
	return keep
}

func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// sandboxedRuntimeEnv returns env without credentials, keeping only the
// runtime's own model keys. env is a list of NAME=value entries.
func sandboxedRuntimeEnv(runtime string, env []string) []string {
	values := make(map[string]string, len(env))
	for _, kv := range env {
		if name, value, ok := strings.Cut(kv, "="); ok {
			values[name] = value
		}
	}
	keep := runtimeModelEnv(runtime, func(name string) string { return values[name] })
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		if !keep[name] && isCredentialEnv(name, value) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// runtimeProcessEnv is the environment of a native runtime turn process. A
// command-sandboxed session leaves the network open for model calls, so no
// credential beyond the runtime's own model key may ride along. hookEnv (the
// approval hook token) is appended after the scrub.
func runtimeProcessEnv(rec SessionRecord, base, hookEnv []string) []string {
	if rec.CommandSandbox {
		base = sandboxedRuntimeEnv(rec.Runtime, base)
	}
	return agentcli.WithUserBinPath(append(base, hookEnv...))
}

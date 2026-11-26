package runner

import (
	"regexp"
	"runtime"
	"strings"

	"github.com/hoxiai/hoxi/internal/agentcli"
	"github.com/hoxiai/hoxi/internal/subproc"
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

var credentialValueRe = regexp.MustCompile(`^(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_\w{20,}|glpat-[\w-]{20,}|npm_[A-Za-z0-9]{30,}|xox[abeprs]-[\w-]{10,}|sk-[\w-]{20,}|(?:AKIA|ASIA)[A-Z0-9]{16}|hf_[A-Za-z0-9]{30,})$|^[A-Za-z][A-Za-z0-9+.-]*://[^/@\s:]+:[^/@\s]+@`)

// proxyEnvNames carry the proxy the runtime needs for its own model calls,
// even when the proxy URL embeds a password.
var proxyEnvNames = setOf("HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy")

// blockedSandboxExecEnvNames are loader and shell/interpreter startup hook
// variables that silently execute code on child startup without appearing in
// the command line. Build-tuning flags such as NODE_OPTIONS stay untouched.
var blockedSandboxExecEnvNames = setOf(
	"LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "DYLD_FORCE_FLAT_NAMESPACE",
	"BASH_ENV", "ENV", "ZDOTDIR",
	"PYTHONSTARTUP", "PERL5OPT", "RUBYOPT",
)

// isCredentialEnv reports whether one NAME=value entry must be dropped.
func isCredentialEnv(name, value string) bool {
	if proxyEnvNames[name] || proxyEnvNames[strings.ToUpper(name)] {
		return false
	}
	if blockedSandboxExecEnvNames[strings.ToUpper(strings.TrimSpace(name))] {
		return true
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
func sandboxedRuntimeEnv(runtimeName string, env []string) []string {
	return sandboxedRuntimeEnvForOS(runtimeName, env, runtime.GOOS == "windows")
}

func sandboxedRuntimeEnvForOS(runtimeName string, env []string, caseInsensitive bool) []string {
	keyFor := func(name string) string {
		if caseInsensitive {
			return strings.ToUpper(name)
		}
		return name
	}
	values := make(map[string]string, len(env))
	for _, kv := range env {
		if name, value, ok := strings.Cut(kv, "="); ok {
			values[keyFor(name)] = value
		}
	}
	keep := runtimeModelEnv(runtimeName, func(name string) string { return values[keyFor(name)] })
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		if !keep[keyFor(name)] && isCredentialEnv(name, value) {
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
var applyRuntimeSystemProxy = agentcli.WithSystemProxy

func runtimeProcessEnv(rec SessionRecord, base, hookEnv []string) []string {
	if rec.Runtime != "" && rec.Runtime != "native" && applyRuntimeSystemProxy != nil {
		base = applyRuntimeSystemProxy(base)
	}
	if rec.CommandSandbox {
		base = sandboxedRuntimeEnv(rec.Runtime, base)
	}
	return subproc.WithUTF8Env(agentcli.WithUserBinPath(mergeProcessEnv(base, hookEnv)))
}

// mergeProcessEnv applies hookEnv overrides onto base in-place while merging
// comma-separated NO_PROXY / no_proxy lists so loopback hook exclusions never
// overwrite system/user proxy bypass entries.
func mergeProcessEnv(base, overlay []string) []string {
	return mergeProcessEnvForOS(base, overlay, runtime.GOOS == "windows")
}

func mergeProcessEnvForOS(base, overlay []string, caseInsensitive bool) []string {
	out := make([]string, 0, len(base)+len(overlay))
	idxByKey := make(map[string]int, len(base)+len(overlay))
	keyFor := func(name string) string {
		if caseInsensitive {
			return strings.ToUpper(name)
		}
		return name
	}
	apply := func(kv string) {
		name, val, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			return
		}
		k := keyFor(name)
		if prevIdx, exists := idxByKey[k]; exists {
			if (caseInsensitive && strings.EqualFold(name, "NO_PROXY")) || name == "NO_PROXY" || name == "no_proxy" {
				prevName, prevVal, _ := strings.Cut(out[prevIdx], "=")
				mergedName := name
				if caseInsensitive && prevName != "" {
					mergedName = prevName
				}
				out[prevIdx] = mergedName + "=" + mergeNoProxyCSV(prevVal, val)
				return
			}
			out[prevIdx] = kv
			return
		}
		idxByKey[k] = len(out)
		out = append(out, kv)
	}
	for _, kv := range base {
		apply(kv)
	}
	for _, kv := range overlay {
		apply(kv)
	}
	return out
}

func mergeNoProxyCSV(a, b string) string {
	seen := make(map[string]bool)
	var parts []string
	for _, list := range []string{a, b} {
		for _, raw := range strings.Split(list, ",") {
			tok := strings.TrimSpace(raw)
			if tok == "" {
				continue
			}
			lower := strings.ToLower(tok)
			if seen[lower] {
				continue
			}
			seen[lower] = true
			parts = append(parts, tok)
		}
	}
	return strings.Join(parts, ",")
}

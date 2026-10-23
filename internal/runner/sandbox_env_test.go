package runner

import (
	"strings"
	"testing"
)

func envNames(env []string) map[string]bool {
	out := map[string]bool{}
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		out[name] = true
	}
	return out
}

func TestSandboxedRuntimeEnvDropsCredentials(t *testing.T) {
	env := []string{
		"PATH=/usr/bin:/bin", "HOME=/Users/u", "LANG=en_US.UTF-8", "GIT_AUTHOR_NAME=U",
		"HTTPS_PROXY=http://user:pw@proxy:8080", "JAVA_HOME=/opt/jdk", "PWD=/w",
		"ANTHROPIC_API_KEY=sk-ant-keep", "ANTHROPIC_BASE_URL=https://api.anthropic.com",
		"SSH_AUTH_SOCK=/private/tmp/com.apple.launchd.x/Listeners", "SSH_AGENT_PID=1",
		"GH_TOKEN=a", "GITHUB_TOKEN=b", "NPM_TOKEN=c", "NODE_AUTH_TOKEN=d", "CARGO_REGISTRY_TOKEN=e",
		"AWS_ACCESS_KEY_ID=AKIA0000000000000000", "AWS_SECRET_ACCESS_KEY=f", "AWS_SESSION_TOKEN=g",
		"PGPASSWORD=h", "TWINE_PASSWORD=i", "OPENAI_API_KEY=j", "STRIPE_KEY=k", "GITHUB_PAT=l",
		"npm_config__auth=m", "DOCKER_AUTH_CONFIG=n", "GIT_ASKPASS=/bin/askpass", "KUBECONFIG=/k",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: x",
		"INNOCENT_NAME=ghp_abcdefghijklmnopqrstuvwxyz0123", "DATABASE_URL=postgres://app:pw@db/x",
	}
	got := envNames(sandboxedRuntimeEnv(string(RuntimeClaudeCode), env))
	for _, keep := range []string{"PATH", "HOME", "LANG", "GIT_AUTHOR_NAME", "HTTPS_PROXY", "JAVA_HOME", "PWD", "ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL"} {
		if !got[keep] {
			t.Errorf("%s was dropped", keep)
		}
	}
	for _, drop := range []string{
		"SSH_AUTH_SOCK", "SSH_AGENT_PID", "GH_TOKEN", "GITHUB_TOKEN", "NPM_TOKEN", "NODE_AUTH_TOKEN",
		"CARGO_REGISTRY_TOKEN", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"PGPASSWORD", "TWINE_PASSWORD", "OPENAI_API_KEY", "STRIPE_KEY", "GITHUB_PAT", "npm_config__auth",
		"DOCKER_AUTH_CONFIG", "GIT_ASKPASS", "KUBECONFIG", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0",
		"GIT_CONFIG_VALUE_0", "INNOCENT_NAME", "DATABASE_URL",
	} {
		if got[drop] {
			t.Errorf("%s reached the sandboxed runtime", drop)
		}
	}
}

func TestSandboxedRuntimeEnvKeepsOnlyOwnModelKeys(t *testing.T) {
	env := []string{"ANTHROPIC_API_KEY=a", "GEMINI_API_KEY=b", "AWS_SECRET_ACCESS_KEY=c"}
	if got := envNames(sandboxedRuntimeEnv(string(RuntimeAntigravity), env)); got["ANTHROPIC_API_KEY"] || !got["GEMINI_API_KEY"] || got["AWS_SECRET_ACCESS_KEY"] {
		t.Fatalf("antigravity env: %v", got)
	}
	if got := envNames(sandboxedRuntimeEnv(string(RuntimeClaudeCode), env)); got["AWS_SECRET_ACCESS_KEY"] || got["GEMINI_API_KEY"] {
		t.Fatalf("claude without bedrock kept cloud keys: %v", got)
	}
	bedrock := append([]string{"CLAUDE_CODE_USE_BEDROCK=1"}, env...)
	if got := envNames(sandboxedRuntimeEnv(string(RuntimeClaudeCode), bedrock)); !got["AWS_SECRET_ACCESS_KEY"] {
		t.Fatalf("claude on bedrock lost its AWS key: %v", got)
	}
}

func TestRuntimeProcessEnvScrubsOnlySandboxedSessions(t *testing.T) {
	base := []string{"PATH=/usr/bin", "GH_TOKEN=x", "SSH_AUTH_SOCK=/s"}
	hook := []string{claudeHookTokenEnv + "=hook"}
	sandboxed := envNames(runtimeProcessEnv(SessionRecord{Runtime: string(RuntimeClaudeCode), CommandSandbox: true}, base, hook))
	if sandboxed["GH_TOKEN"] || sandboxed["SSH_AUTH_SOCK"] || !sandboxed[claudeHookTokenEnv] || !sandboxed["PATH"] {
		t.Fatalf("sandboxed env: %v", sandboxed)
	}
	plain := envNames(runtimeProcessEnv(SessionRecord{Runtime: string(RuntimeClaudeCode)}, base, hook))
	if !plain["GH_TOKEN"] || !plain["SSH_AUTH_SOCK"] {
		t.Fatalf("an unsandboxed session must keep the user's environment: %v", plain)
	}
}

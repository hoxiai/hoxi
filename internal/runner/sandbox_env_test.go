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
		"NODE_OPTIONS=--max-old-space-size=4096",
		"ANTHROPIC_API_KEY=sk-ant-keep", "ANTHROPIC_BASE_URL=https://api.anthropic.com",
		"SSH_AUTH_SOCK=/private/tmp/com.apple.launchd.x/Listeners", "SSH_AGENT_PID=1",
		"GH_TOKEN=a", "GITHUB_TOKEN=b", "NPM_TOKEN=c", "NODE_AUTH_TOKEN=d", "CARGO_REGISTRY_TOKEN=e",
		"AWS_ACCESS_KEY_ID=AKIA0000000000000000", "AWS_SECRET_ACCESS_KEY=f", "AWS_SESSION_TOKEN=g",
		"PGPASSWORD=h", "TWINE_PASSWORD=i", "OPENAI_API_KEY=j", "STRIPE_KEY=k", "GITHUB_PAT=l",
		"npm_config__auth=m", "DOCKER_AUTH_CONFIG=n", "GIT_ASKPASS=/bin/askpass", "KUBECONFIG=/k",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: x",
		"INNOCENT_NAME=ghp_abcdefghijklmnopqrstuvwxyz0123", "DATABASE_URL=postgres://app:pw@db/x",
		"LD_PRELOAD=/tmp/evil.so", "DYLD_INSERT_LIBRARIES=/tmp/evil.dylib", "BASH_ENV=/tmp/hook.sh", "PYTHONSTARTUP=/tmp/p.py",
	}
	got := envNames(sandboxedRuntimeEnv(string(RuntimeClaudeCode), env))
	for _, keep := range []string{"PATH", "HOME", "LANG", "GIT_AUTHOR_NAME", "HTTPS_PROXY", "JAVA_HOME", "PWD", "NODE_OPTIONS", "ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL"} {
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
		"LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "BASH_ENV", "PYTHONSTARTUP",
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

func TestRuntimeProcessEnvMergesNoProxyWithoutDuplicates(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"HTTPS_PROXY=http://127.0.0.1:7890",
		"NO_PROXY=*.local,169.254.0.0/16,127.0.0.1",
	}
	hook := []string{
		claudeHookTokenEnv + "=hook",
		"NO_PROXY=127.0.0.1,localhost,::1",
	}
	got := runtimeProcessEnv(SessionRecord{Runtime: string(RuntimeAntigravity), CommandSandbox: true}, base, hook)
	var noProxyVals []string
	var httpsProxy string
	for _, kv := range got {
		name, val, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch name {
		case "NO_PROXY":
			noProxyVals = append(noProxyVals, val)
		case "HTTPS_PROXY":
			httpsProxy = val
		}
	}
	if httpsProxy != "http://127.0.0.1:7890" {
		t.Fatalf("HTTPS_PROXY = %q, want http://127.0.0.1:7890", httpsProxy)
	}
	if len(noProxyVals) != 1 {
		t.Fatalf("expected single deduplicated NO_PROXY entry, got %v", noProxyVals)
	}
	if want := "*.local,169.254.0.0/16,127.0.0.1,localhost,::1"; noProxyVals[0] != want {
		t.Fatalf("NO_PROXY = %q, want %q", noProxyVals[0], want)
	}
}

func TestRuntimeProcessEnvInjectsSystemProxyForExternalCLIsOnly(t *testing.T) {
	prev := applyRuntimeSystemProxy
	applyRuntimeSystemProxy = func(env []string) []string {
		return append(append([]string(nil), env...),
			"HTTPS_PROXY=http://127.0.0.1:7890",
			"NO_PROXY=127.0.0.1,localhost,::1",
		)
	}
	t.Cleanup(func() { applyRuntimeSystemProxy = prev })

	agyEnv := envNames(runtimeProcessEnv(
		SessionRecord{Runtime: string(RuntimeAntigravity), CommandSandbox: true},
		[]string{"PATH=/usr/bin", "GH_TOKEN=drop"},
		nil,
	))
	if !agyEnv["HTTPS_PROXY"] || !agyEnv["NO_PROXY"] || agyEnv["GH_TOKEN"] {
		t.Fatalf("expected system proxy preserved and GH_TOKEN scrubbed for antigravity, got %v", agyEnv)
	}

	nativeEnv := envNames(runtimeProcessEnv(
		SessionRecord{Runtime: "native", CommandSandbox: true},
		[]string{"PATH=/usr/bin"},
		nil,
	))
	if nativeEnv["HTTPS_PROXY"] {
		t.Fatalf("native bash env should not auto-inject CLI system proxy: %v", nativeEnv)
	}
}

func TestWindowsCaseInsensitiveEnvMergeAndScrub(t *testing.T) {
	merged := mergeProcessEnvForOS(
		[]string{
			"Path=C:\\Windows\\System32",
			"No_Proxy=corp.local,10.0.0.0/8",
		},
		[]string{
			"PATH=C:\\Custom\\Bin",
			"NO_PROXY=127.0.0.1,localhost",
			"no_proxy=::1,127.0.0.1",
		},
		true,
	)
	var pathCount, noProxyCount int
	var pathVal, noProxyVal string
	for _, kv := range merged {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch strings.ToUpper(k) {
		case "PATH":
			pathCount++
			pathVal = v
		case "NO_PROXY":
			noProxyCount++
			noProxyVal = v
		}
	}
	if pathCount != 1 || pathVal != `C:\Custom\Bin` {
		t.Fatalf("expected single overridden PATH, got count=%d val=%q env=%v", pathCount, pathVal, merged)
	}
	if noProxyCount != 1 || noProxyVal != "corp.local,10.0.0.0/8,127.0.0.1,localhost,::1" {
		t.Fatalf("expected merged case-insensitive NO_PROXY, got count=%d val=%q env=%v", noProxyCount, noProxyVal, merged)
	}

	scrubbed := sandboxedRuntimeEnvForOS(string(RuntimeClaudeCode), []string{
		"anthropic_api_key=sk-ant-keep",
		"Claude_Code_Use_Bedrock=1",
		"aws_access_key_id=AKIAIOSFODNN7EXAMPLE",
		"Http_Proxy=http://user:pass@127.0.0.1:7890",
		"github_token=ghp_123456789012345678901234567890",
	}, true)
	gotUpper := map[string]bool{}
	for _, kv := range scrubbed {
		if k, _, ok := strings.Cut(kv, "="); ok {
			gotUpper[strings.ToUpper(k)] = true
		}
	}
	if !gotUpper["ANTHROPIC_API_KEY"] || !gotUpper["AWS_ACCESS_KEY_ID"] || !gotUpper["HTTP_PROXY"] {
		t.Fatalf("expected mixed-case Claude/Bedrock/proxy env vars preserved, got %v", scrubbed)
	}
	if gotUpper["GITHUB_TOKEN"] {
		t.Fatalf("expected mixed-case GITHUB_TOKEN to be scrubbed, got %v", scrubbed)
	}
}

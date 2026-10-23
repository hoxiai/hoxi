package opencode

import (
	"os"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/testutil/execstub"
)

func TestParseVersionOutputStripsAnsiAndWarnings(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "1.19.3\n", "1.19.3"},
		{"trailing lines", "opencode 1.18.0\nextra line\n", "opencode 1.18.0"},
		{
			"ansi warning before version",
			"\x1b[0m\x1b[33mWarning\x1b[0m\x1b[2m:\x1b[0m \x1b[1mThe 'NO_COLOR' env is ignored\x1b[0m\n\x1b[0m      \x1b[2mat \x1b[0m\x1b[36minternal:tty\x1b[0m:33:24\n1.19.3\n",
			"1.19.3",
		},
		{"blank lines", "\n\n  \n1.20.0\n", "1.20.0"},
		{"empty", "\x1b[33mWarning\x1b[0m: something\n", ""},
	}
	for _, tc := range cases {
		if got := parseVersionOutput([]byte(tc.in)); got != tc.want {
			t.Errorf("%s: parseVersionOutput = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestEnvWithoutDropsColorForcingVars(t *testing.T) {
	env := []string{"PATH=/bin", "FORCE_COLOR=1", "CLICOLOR_FORCE=1", "CLICOLOR=1", "HOME=/tmp"}
	got := envWithout(env, "FORCE_COLOR", "CLICOLOR_FORCE", "CLICOLOR")
	if strings.Join(got, ",") != "PATH=/bin,HOME=/tmp" {
		t.Fatalf("envWithout = %v", got)
	}
}

func TestResolveVersionIgnoresInheritedForceColor(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	tmpDir := t.TempDir()
	bin := execstub.Write(t, tmpDir, "color-opencode", `
if [ "${FORCE_COLOR+x}" = "x" ]; then
  printf '\033[33mWarning\033[0m: The NO_COLOR env is ignored due to the FORCE_COLOR env being set.\n'
fi
echo "1.21.0"
`)
	if got := ResolveVersion(bin); got != "1.21.0" {
		t.Fatalf("ResolveVersion = %q, want %q", got, "1.21.0")
	}
	if raw := os.Getenv("FORCE_COLOR"); raw != "1" {
		t.Fatalf("test setup broken: FORCE_COLOR = %q", raw)
	}
}

package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

func TestHostSubcommandTokenPairURIAndStartHook(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	workDir := t.TempDir()

	if !ShouldHandle([]string{"hoxi", "host", "status"}, nil) {
		t.Fatalf("expected ShouldHandle=true for `hoxi host status`")
	}

	var out, errBuf bytes.Buffer
	code := Run(context.Background(), []string{"hoxi", "host", "token"}, strings.NewReader(""), &out, &errBuf, "127.0.0.1:5420", nil)
	if code != 0 {
		t.Fatalf("expected exit 0 for `hoxi host token`, got %d (stderr=%s)", code, errBuf.String())
	}
	tok := strings.TrimSpace(out.String())
	if !strings.HasPrefix(tok, "hxh_") {
		t.Fatalf("expected hxh_ token, got %q", tok)
	}

	out.Reset()
	errBuf.Reset()
	code = Run(context.Background(), []string{"hoxi", "host", "pair-uri", "--name", "wsl-ubuntu", "--public-url", "https://example.invalid:5430"}, strings.NewReader(""), &out, &errBuf, "127.0.0.1:5420", nil)
	if code != 0 {
		t.Fatalf("expected exit 0 for `hoxi host pair-uri`, got %d (stderr=%s)", code, errBuf.String())
	}
	uri := strings.TrimSpace(out.String())
	if !strings.HasPrefix(uri, "hoxi-host://pair?") || !strings.Contains(uri, "wsl-ubuntu") {
		t.Fatalf("unexpected pair-uri output: %q", uri)
	}

	var captured HostStartOptions
	SetHostDaemonRunner(func(_ context.Context, opts HostStartOptions, stdout, _ io.Writer) int {
		captured = opts
		_, _ = io.WriteString(stdout, "daemon started\n")
		return 0
	})
	t.Cleanup(func() { SetHostDaemonRunner(nil) })

	out.Reset()
	errBuf.Reset()
	code = Run(context.Background(), []string{"hoxi", "host", "start", "--listen", "127.0.0.1:5435", "--dir", workDir, "--name", "devbox"}, strings.NewReader(""), &out, &errBuf, "127.0.0.1:5420", nil)
	if code != 0 {
		t.Fatalf("expected exit 0 for `hoxi host start`, got %d (stderr=%s)", code, errBuf.String())
	}
	if captured.ListenAddr != "127.0.0.1:5435" || captured.Name != "devbox" || captured.RootDir != workDir {
		t.Fatalf("unexpected captured HostStartOptions: %+v", captured)
	}
}

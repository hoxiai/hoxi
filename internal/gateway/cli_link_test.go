package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

func TestSystemCLILinkEndpointAndTunnelGuard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink fixture")
	}

	tmp := t.TempDir()
	appExe := filepath.Join(tmp, "Hoxi.app", "Contents", "MacOS", "Hoxi")
	if err := os.MkdirAll(filepath.Dir(appExe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appExe, []byte("#!/bin/sh\necho hoxi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(tmp, "local-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	origExe := resolveCLILinkExecutable
	origBin := resolveCLILinkBinDir
	resolveCLILinkExecutable = func() (string, error) { return appExe, nil }
	resolveCLILinkBinDir = func() string { return binDir }
	defer func() {
		resolveCLILinkExecutable = origExe
		resolveCLILinkBinDir = origBin
	}()

	g := New(0, "test", tmp, nil, nil)
	const secretToken = "cli-link-test-token"
	g.SetGatewayToken(secretToken)

	// 1. Remote tunnel requests MUST be rejected with 403 across all HTTP methods.
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/hoxi/system/cli-link", nil)
		g.serveRemoteRequest(rec, req, remoteSourceTunnel)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("remote %s /api/hoxi/system/cli-link: status=%d, want 403 (%s)", method, rec.Code, rec.Body.String())
		}
	}

	// 2. Unauthenticated loopback HTTP call without gateway token is rejected with 403.
	unauthRec := httptest.NewRecorder()
	g.ServeHTTP(unauthRec, localRequest(http.MethodPost, "/api/hoxi/system/cli-link", nil))
	if unauthRec.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated POST /api/hoxi/system/cli-link: status=%d, want 403", unauthRec.Code)
	}

	// 3. Authenticated desktop GET -> POST -> DELETE lifecycle.
	authed := func(method string) hoxipaths.CLILinkStatus {
		t.Helper()
		req := localRequest(method, "/api/hoxi/system/cli-link", nil)
		req.Header.Set(gatewayTokenHeader, secretToken)
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s /api/hoxi/system/cli-link: status=%d body=%s", method, rec.Code, rec.Body.String())
		}
		var st hoxipaths.CLILinkStatus
		if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
			t.Fatal(err)
		}
		return st
	}

	initial := authed(http.MethodGet)
	if initial.LinkPath == filepath.Join(binDir, "hoxi") {
		t.Fatalf("the fixture link should not exist before installation: %+v", initial)
	}

	installed := authed(http.MethodPost)
	if !installed.Installed || !installed.Current || installed.LinkPath != filepath.Join(binDir, "hoxi") {
		t.Fatalf("expected installed+current status, got %+v", installed)
	}

	removed := authed(http.MethodDelete)
	// A real Hoxi installation on the host search path may still be visible.
	// Only the link installed in our test directory may change.
	if removed != initial {
		t.Fatalf("expected DELETE to restore original status: before=%+v after=%+v", initial, removed)
	}
}

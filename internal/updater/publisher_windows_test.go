//go:build windows

package updater

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsStageRequiresSameTrustedPublisherForAllBinaries(t *testing.T) {
	for _, tc := range []struct {
		name, expected, sidecar, sidecarKey, wantErr string
	}{
		{name: "app only", expected: "publisher-A"},
		{name: "same publisher sidecar", expected: "publisher-A", sidecar: "opencode.exe", sidecarKey: "publisher-A"},
		{name: "other publisher", expected: "publisher-A", sidecar: "sqlite3.exe", sidecarKey: "publisher-B", wantErr: "different publisher key"},
		{name: "unsigned", expected: "publisher-A", sidecar: "opencode.exe", wantErr: "signature verification failed"},
		{name: "unsigned current installation", wantErr: "no trusted publisher"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range []string{"Hoxi.exe", tc.sidecar} {
				if name != "" {
					if err := os.WriteFile(filepath.Join(dir, name), []byte("fixture"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			err := verifyWindowsStagePublisher(dir, tc.expected, func(path string) (string, error) {
				if filepath.Base(path) == "Hoxi.exe" {
					return "publisher-A", nil
				}
				if tc.sidecarKey == "" {
					return "", errors.New("signature verification failed")
				}
				return tc.sidecarKey, nil
			})
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("verification = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestWindowsPublisherRejectsUnsignedNativeExecutable(t *testing.T) {
	exe, err := os.Executable() // The Go-generated test binary is a real unsigned PE.
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyWindowsUpdateArchitecture(exe); err != nil {
		t.Fatal(err)
	}
	if windowsExecutableHasEmbeddedSignature(exe) {
		t.Fatal("unsigned installation must not offer in-app updates")
	}
	ps, err := windowsUpdatePowerShell()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ps); err != nil {
		t.Fatal(err)
	}
	key, err := verifiedWindowsPublisherKey(context.Background(), ps, exe)
	if key != "" || err == nil || !strings.Contains(err.Error(), "embedded Authenticode signature could not be verified") {
		t.Fatalf("unsigned executable did not return the signature error: %v", err)
	}
}

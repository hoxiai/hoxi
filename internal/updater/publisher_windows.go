//go:build windows

package updater

import (
	"context"
	"debug/pe"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
	"golang.org/x/sys/windows"
)

const windowsPublisherKeyPS = `
$ErrorActionPreference = 'Stop'
$sig = Get-AuthenticodeSignature -LiteralPath $env:HOXI_UPD_VERIFY_FILE
if ($sig.Status -ne 'Valid' -or $sig.SignatureType -ne 'Authenticode' -or !$sig.SignerCertificate) { exit 1 }
$key = $sig.SignerCertificate.PublicKey
if (!$key.EncodedKeyValue.RawData.Length) { exit 1 }
$parts = @($key.Oid.Value, [Convert]::ToBase64String($key.EncodedParameters.RawData), [Convert]::ToBase64String($key.EncodedKeyValue.RawData))
[Console]::WriteLine(($parts -join ':'))
`

func windowsUpdatePowerShell() (string, error) {
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "WindowsPowerShell", "v1.0", "powershell.exe"), nil
}

// This is a cheap availability check, not a signature verification. Actual
// Windows trust and signer matching remain mandatory on each update attempt.
func windowsExecutableHasEmbeddedSignature(path string) bool {
	f, err := pe.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if h, ok := f.OptionalHeader.(*pe.OptionalHeader64); ok && h.NumberOfRvaAndSizes > pe.IMAGE_DIRECTORY_ENTRY_SECURITY {
		entry := h.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_SECURITY]
		return entry.VirtualAddress != 0 && entry.Size != 0
	}
	return false
}

func verifiedWindowsPublisherKey(ctx context.Context, ps, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	cmd := subproc.CommandContext(ctx, ps, "-NoProfile", "-NonInteractive", "-Command", windowsPublisherKeyPS)
	cmd.Env = append(os.Environ(), "HOXI_UPD_VERIFY_FILE="+path)
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("embedded Authenticode signature could not be verified: %w", err)
	}
	key := strings.TrimSpace(string(out))
	if key == "" {
		return "", errors.New("Authenticode signer has no public key")
	}
	return key, nil
}

func verifyWindowsStagePublisher(stageDir, expectedKey string, signerKey func(string) (string, error)) error {
	if expectedKey == "" {
		return errors.New("no trusted publisher key for this installation")
	}
	for _, name := range []string{"Hoxi.exe", "opencode.exe", "sqlite3.exe"} {
		path := filepath.Join(stageDir, name)
		info, err := os.Lstat(path)
		if name != "Hoxi.exe" && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect staged %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("staged %s is not a regular file", name)
		}
		key, err := signerKey(path)
		if err != nil {
			return fmt.Errorf("verify staged %s: %w", name, err)
		}
		if key != expectedKey {
			return fmt.Errorf("staged %s has a different publisher key; download the official installer manually if the signing key has changed", name)
		}
	}
	return nil
}

func verifyWindowsUpdateArchitecture(path string) error {
	f, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("read staged Hoxi.exe architecture: %w", err)
	}
	defer f.Close()
	want := uint16(pe.IMAGE_FILE_MACHINE_AMD64)
	if runtime.GOARCH == "arm64" {
		want = pe.IMAGE_FILE_MACHINE_ARM64
	}
	if f.Machine != want {
		return fmt.Errorf("staged Hoxi.exe does not match current architecture %s", runtime.GOARCH)
	}
	return nil
}

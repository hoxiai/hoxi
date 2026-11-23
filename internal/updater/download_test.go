package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestResolveCandidatesHonorsSourcePreferenceAndMirrors(t *testing.T) {
	t.Parallel()

	arch := darwinReleaseArch()
	cdnDMG := "https://cdn.hoxi.dev/hoxi-desktop/v0.7.3/Hoxi-0.7.3-darwin-" + arch + ".dmg"
	cnDMG := "https://dl.example.cn/hoxi-desktop/v0.7.3/Hoxi-0.7.3-darwin-" + arch + ".dmg"
	ghDMG := "https://github.com/hoxiai/hoxi/releases/download/v0.7.3/Hoxi-0.7.3-darwin-" + arch + ".dmg"

	res := &UpdateCheckResult{
		LatestVersion: "0.7.3",
		DownloadURL:   cdnDMG,
		Mirrors: UpdateMirrors{
			CN:     cnDMG,
			CDN:    cdnDMG,
			Global: ghDMG,
		},
	}

	autoCands := resolveDarwinDMGCandidates(res, DownloadSourceAuto)
	if len(autoCands) != 3 || autoCands[0].URL != cnDMG || autoCands[1].URL != cdnDMG || autoCands[2].URL != ghDMG {
		t.Fatalf("auto candidates = %+v", autoCands)
	}

	globalCands := resolveDarwinDMGCandidates(res, DownloadSourceGlobal)
	if len(globalCands) != 3 || globalCands[0].URL != ghDMG || globalCands[1].URL != cdnDMG || globalCands[2].URL != cnDMG {
		t.Fatalf("global candidates = %+v", globalCands)
	}

	// Legacy GitHub-only API response: auto tries GitHub then derives cdn.hoxi.dev fallback,
	// while explicit CDN preference puts cdn.hoxi.dev first.
	legacyRes := &UpdateCheckResult{
		LatestVersion: "0.7.3",
		DownloadURL:   ghDMG,
		Mirrors:       UpdateMirrors{Global: ghDMG},
	}
	legacyAuto := resolveDarwinDMGCandidates(legacyRes, DownloadSourceAuto)
	if len(legacyAuto) != 2 || legacyAuto[0].URL != ghDMG || legacyAuto[1].URL != cdnDMG {
		t.Fatalf("legacy auto candidates = %+v", legacyAuto)
	}
	legacyCDN := resolveDarwinDMGCandidates(legacyRes, DownloadSourceCDN)
	if len(legacyCDN) != 2 || legacyCDN[0].URL != cdnDMG || legacyCDN[1].URL != ghDMG {
		t.Fatalf("legacy cdn candidates = %+v", legacyCDN)
	}

	// Windows setup.exe CDN URL converts to .zip on both CDN and GitHub mirrors.
	winArch := windowsReleaseArch()
	winRes := &UpdateCheckResult{
		LatestVersion: "0.7.3",
		DownloadURL:   "https://cdn.hoxi.dev/hoxi-desktop/v0.7.3/Hoxi-0.7.3-windows-amd64-setup.exe",
		Mirrors: UpdateMirrors{
			CDN:    "https://cdn.hoxi.dev/hoxi-desktop/v0.7.3/Hoxi-0.7.3-windows-amd64-setup.exe",
			Global: "https://github.com/hoxiai/hoxi/releases/download/v0.7.3/Hoxi-0.7.3-windows-amd64-setup.exe",
		},
	}
	winAuto := resolveWindowsArchiveCandidates(winRes, DownloadSourceAuto)
	wantWinCDN := "https://cdn.hoxi.dev/hoxi-desktop/v0.7.3/Hoxi-0.7.3-windows-" + winArch + ".zip"
	wantWinGH := "https://github.com/hoxiai/hoxi/releases/download/v0.7.3/Hoxi-0.7.3-windows-" + winArch + ".zip"
	if len(winAuto) != 2 || winAuto[0].URL != wantWinCDN || winAuto[1].URL != wantWinGH || !winAuto[0].Rewritten {
		t.Fatalf("windows auto candidates = %+v", winAuto)
	}
}

func TestDownloadFileWithFallbackSwitchesOnErrorAndCorruptMirror(t *testing.T) {
	payload := bytes.Repeat([]byte("hoxi-release-bytes-"), 2048)
	sum := sha256.Sum256(payload)
	wantSHA := hex.EncodeToString(sum[:])

	var badHits, corruptHits, goodHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mirror-503/Hoxi.dmg":
			badHits.Add(1)
			http.Error(w, "upstream down", http.StatusServiceUnavailable)
		case "/mirror-corrupt/Hoxi.dmg":
			corruptHits.Add(1)
			_, _ = w.Write([]byte("tampered-payload"))
		case "/mirror-good/Hoxi.dmg":
			goodHits.Add(1)
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dstPath := filepath.Join(t.TempDir(), "update.dmg")
	var lastSource string
	err := downloadFileWithFallback(
		context.Background(),
		srv.Client(),
		[]downloadCandidate{
			{URL: srv.URL + "/mirror-503/Hoxi.dmg", Source: "cn"},
			{URL: srv.URL + "/mirror-corrupt/Hoxi.dmg", Source: "cdn"},
			{URL: srv.URL + "/mirror-good/Hoxi.dmg", Source: "global"},
		},
		wantSHA,
		dstPath,
		func(p InstallProgress) {
			if p.Source != "" {
				lastSource = p.Source
			}
		},
	)
	if err != nil {
		t.Fatalf("expected failover to good mirror to succeed, got %v", err)
	}
	if badHits.Load() != 1 || corruptHits.Load() != 1 || goodHits.Load() != 1 {
		t.Fatalf("unexpected mirror hit counts: 503=%d corrupt=%d good=%d", badHits.Load(), corruptHits.Load(), goodHits.Load())
	}
	if lastSource != "global" {
		t.Fatalf("expected final progress source global, got %q", lastSource)
	}
	got, err := os.ReadFile(dstPath)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("downloaded file mismatch: err=%v len=%d", err, len(got))
	}
}

func TestDownloadFileResumesViaHTTPRangeAfterTruncation(t *testing.T) {
	payload := bytes.Repeat([]byte("0123456789abcdef"), 4096) // 64 KiB
	splitAt := len(payload) / 2
	sum := sha256.Sum256(payload)
	wantSHA := hex.EncodeToString(sum[:])

	var rangeRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Hoxi.dmg" {
			http.NotFound(w, r)
			return
		}
		rng := strings.TrimSpace(r.Header.Get("Range"))
		if rng == "" {
			// Send Content-Length for the full payload, then stop mid-stream to force resume.
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.Header().Set("Accept-Ranges", "bytes")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload[:splitAt])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			// Hijack and close abruptly so the client observes an unexpected EOF mid-body.
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
				}
			}
			return
		}
		rangeRequests.Add(1)
		prefix := "bytes="
		if !strings.HasPrefix(rng, prefix) || !strings.HasSuffix(rng, "-") {
			http.Error(w, "bad range", http.StatusBadRequest)
			return
		}
		offset, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rng, prefix), "-"))
		if err != nil || offset < 0 || offset >= len(payload) {
			http.Error(w, "invalid range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		chunk := payload[offset:]
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, len(payload)-1, len(payload)))
		w.Header().Set("Content-Length", strconv.Itoa(len(chunk)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(chunk)
	}))
	defer srv.Close()

	dstPath := filepath.Join(t.TempDir(), "resumed.dmg")
	err := downloadFileWithFallback(
		context.Background(),
		srv.Client(),
		[]downloadCandidate{{URL: srv.URL + "/Hoxi.dmg", Source: "cdn"}},
		wantSHA,
		dstPath,
		nil,
	)
	if err != nil {
		t.Fatalf("expected Range resume to succeed, got %v", err)
	}
	if rangeRequests.Load() != 1 {
		t.Fatalf("expected 1 Range resume request, got %d", rangeRequests.Load())
	}
	got, err := os.ReadFile(dstPath)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("resumed file mismatch: err=%v len=%d", err, len(got))
	}
}

func TestDownloadFileFailsOverWhenPrimaryMirrorIsTooSlow(t *testing.T) {
	oldWindow := downloadSlowFailoverWindow
	oldMinBPS := downloadSlowFailoverMinBPS
	downloadSlowFailoverWindow = 60 * time.Millisecond
	downloadSlowFailoverMinBPS = 1024 * 1024 // 1 MiB/s threshold for fast deterministic test
	defer func() {
		downloadSlowFailoverWindow = oldWindow
		downloadSlowFailoverMinBPS = oldMinBPS
	}()

	payload := bytes.Repeat([]byte("fast-mirror-"), 1024)
	sum := sha256.Sum256(payload)
	wantSHA := hex.EncodeToString(sum[:])

	var slowHits, fastHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slow/Hoxi.dmg":
			slowHits.Add(1)
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
			for i := 0; i < 20; i++ {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(25 * time.Millisecond):
					_, _ = w.Write([]byte("x"))
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
				}
			}
		case "/fast/Hoxi.dmg":
			fastHits.Add(1)
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dstPath := filepath.Join(t.TempDir(), "fast.dmg")
	err := downloadFileWithFallback(
		context.Background(),
		srv.Client(),
		[]downloadCandidate{
			{URL: srv.URL + "/slow/Hoxi.dmg", Source: "cdn"},
			{URL: srv.URL + "/fast/Hoxi.dmg", Source: "global"},
		},
		wantSHA,
		dstPath,
		nil,
	)
	if err != nil {
		t.Fatalf("expected slow mirror to fail over to fast mirror, got %v", err)
	}
	if slowHits.Load() != 1 || fastHits.Load() != 1 {
		t.Fatalf("unexpected hit counts slow=%d fast=%d", slowHits.Load(), fastHits.Load())
	}
}

func TestResolveCandidatesSHA256RejectsRewrittenFallbackButAllowsSameFilenameMirror(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	apiSHA := strings.Repeat("b", 64)

	// Rewritten filename (e.g., foreign-arch DMG aligned to host arch): must NOT reuse API SHA-256.
	rewrittenCands := []downloadCandidate{
		{URL: srv.URL + "/Hoxi-0.7.3-darwin-amd64.dmg", Source: "cdn", Rewritten: true},
	}
	if got, err := resolveCandidatesSHA256(context.Background(), srv.Client(), rewrittenCands, 0, apiSHA); err == nil {
		t.Fatalf("expected missing checksum error for rewritten candidate without sidecar, got %q", got)
	}

	// Same filename across CDN -> GitHub failover: !Rewritten reuses the official API SHA-256 directly.
	sameFileCands := []downloadCandidate{
		{URL: srv.URL + "/cdn/Hoxi-0.7.3-darwin-arm64.dmg", Source: "cdn", Rewritten: false},
		{URL: srv.URL + "/gh/Hoxi-0.7.3-darwin-arm64.dmg", Source: "global", Rewritten: false},
	}
	got, err := resolveCandidatesSHA256(context.Background(), srv.Client(), sameFileCands, 1, apiSHA)
	if err != nil || got != apiSHA {
		t.Fatalf("same-filename mirror failover SHA256 = (%q, %v), want %q", got, err, apiSHA)
	}
}

// A sibling mirror's checksum for a different file name (other arch, or the
// -setup.exe the API listed before the .zip rewrite) must not be cached as the
// expected digest for the active download.
func TestResolveCandidatesSHA256IgnoresOtherFilenameSidecars(t *testing.T) {
	t.Parallel()

	otherSHA := strings.Repeat("c", 64)
	sameSHA := strings.Repeat("d", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a/Hoxi-0.7.4-darwin-amd64.dmg.sha256":
			_, _ = w.Write([]byte(otherSHA + "  Hoxi-0.7.4-darwin-amd64.dmg\n"))
		case "/c/Hoxi-0.7.4-darwin-arm64.dmg.sha256":
			_, _ = w.Write([]byte(sameSHA + "  Hoxi-0.7.4-darwin-arm64.dmg\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cands := []downloadCandidate{
		{URL: srv.URL + "/a/Hoxi-0.7.4-darwin-amd64.dmg", Source: "cdn"},
		{URL: srv.URL + "/b/Hoxi-0.7.4-darwin-arm64.dmg", Source: "global", Rewritten: true},
		{URL: srv.URL + "/c/Hoxi-0.7.4-darwin-arm64.dmg", Source: "cn"},
	}
	got, err := resolveCandidatesSHA256(context.Background(), srv.Client(), cands, 1, "")
	if err != nil || got != sameSHA {
		t.Fatalf("SHA256 = (%q, %v), want same-file sidecar %q", got, err, sameSHA)
	}
	if _, err := resolveCandidatesSHA256(context.Background(), srv.Client(), cands[:2], 1, ""); err == nil {
		t.Fatal("a different file's sidecar must not satisfy the active download")
	}
}

func TestDownloadFileRejectsMismatchedContentRangeStartAndRecoversCleanly(t *testing.T) {
	payload := bytes.Repeat([]byte("range-guard-bytes"), 1024)
	splitAt := len(payload) / 2
	sum := sha256.Sum256(payload)
	wantSHA := hex.EncodeToString(sum[:])

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		switch n {
		case 1:
			// Truncate mid-stream to trigger Range resume.
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload[:splitAt])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
				}
			}
		case 2:
			// Misbehaving proxy returns 206 starting from byte 0 instead of splitAt!
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(payload)-1, len(payload)))
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload)
		default:
			// Subsequent full download without Range succeeds cleanly.
			if r.Header.Get("Range") != "" {
				http.Error(w, "unexpected Range after rejection", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
		}
	}))
	defer srv.Close()

	dstPath := filepath.Join(t.TempDir(), "recovered.dmg")
	err := downloadFileWithFallback(
		context.Background(),
		srv.Client(),
		[]downloadCandidate{{URL: srv.URL + "/Hoxi.dmg", Source: "cdn"}},
		wantSHA,
		dstPath,
		nil,
	)
	if err != nil {
		t.Fatalf("expected recovery after bogus Content-Range start, got %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("expected 3 attempts (truncated -> bad 206 -> clean full), got %d", calls.Load())
	}
	got, err := os.ReadFile(dstPath)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("recovered payload mismatch: err=%v len=%d", err, len(got))
	}
}

package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	githubReleaseDownloadBase = "https://github.com/hoxiai/hoxi/releases/download"
	downloadMaxResumeAttempts = 3
)

var (
	downloadFailoverStallTimeout = 20 * time.Second
	downloadSlowFailoverWindow   = 15 * time.Second
	downloadSlowFailoverMinBPS   = int64(64 * 1024)
)

type downloadCandidate struct {
	URL       string
	Source    string
	Rewritten bool
}

func classifyUpdateSource(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case host == "github.com" || strings.HasSuffix(host, ".githubusercontent.com"):
		return string(DownloadSourceGlobal)
	case host == "cdn.hoxi.dev":
		return string(DownloadSourceCDN)
	case strings.HasPrefix(host, "dl.") || strings.HasPrefix(host, "cdn."):
		return string(DownloadSourceCDN)
	default:
		return ""
	}
}

func isOfficialHoxiReleaseOrigin(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "github.com" {
		return strings.HasPrefix(strings.ToLower(u.Path), "/hoxiai/hoxi/releases/download/")
	}
	return host == "cdn.hoxi.dev" || host == "hoxi.ai" || host == "hoxi.dev"
}

func deriveOfficialMirrors(version, assetName string) (cdnURL, githubURL string) {
	ver := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if ver == "" || assetName == "" {
		return "", ""
	}
	cdnURL = fmt.Sprintf("%s/v%s/%s", strings.TrimRight(DefaultCDNBaseURL, "/"), ver, assetName)
	githubURL = fmt.Sprintf("%s/v%s/%s", githubReleaseDownloadBase, ver, assetName)
	return cdnURL, githubURL
}

func normalizeWindowsCandidateURL(raw, arch string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		aligned := alignWindowsArchiveArch(trimmed, arch)
		return aligned, aligned != trimmed
	case strings.HasSuffix(lower, "-setup.exe"):
		converted := trimmed[:len(trimmed)-len("-setup.exe")] + ".zip"
		return alignWindowsArchiveArch(converted, arch), true
	default:
		return "", false
	}
}

func normalizeDarwinCandidateURL(raw, arch string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	if !strings.HasSuffix(strings.ToLower(trimmed), ".dmg") {
		return "", false
	}
	aligned := alignDarwinDMGArch(trimmed, arch)
	return aligned, aligned != trimmed
}

func resolveArchiveCandidates(
	res *UpdateCheckResult,
	pref DownloadSource,
	assetName string,
	normalize func(raw string) (string, bool),
) []downloadCandidate {
	if res == nil {
		return nil
	}
	source := NormalizeDownloadSource(string(pref))
	var out []downloadCandidate
	seen := make(map[string]bool)
	add := func(raw, label string, forceRewritten bool) {
		norm, rewritten := normalize(raw)
		if norm == "" || seen[norm] {
			return
		}
		if label == "" {
			label = classifyUpdateSource(norm)
		}
		seen[norm] = true
		out = append(out, downloadCandidate{
			URL:       norm,
			Source:    label,
			Rewritten: rewritten || forceRewritten,
		})
	}

	rawPrimary := strings.TrimSpace(res.DownloadURL)
	primaryNorm, _ := normalize(rawPrimary)
	primarySource := classifyUpdateSource(primaryNorm)
	primaryFile := ""
	if rawPrimary != "" {
		if u, err := url.Parse(rawPrimary); err == nil {
			primaryFile = path.Base(u.Path)
		}
	}
	fileDiffersFromPrimary := func(candidateURL string) bool {
		if primaryFile == "" || candidateURL == "" {
			return false
		}
		u, err := url.Parse(candidateURL)
		if err != nil {
			return true
		}
		return path.Base(u.Path) != primaryFile
	}
	hasExplicitAccelerated := strings.TrimSpace(res.Mirrors.CN) != "" || strings.TrimSpace(res.Mirrors.CDN) != ""
	canDeriveOfficial := primaryNorm == "" || isOfficialHoxiReleaseOrigin(primaryNorm) ||
		isOfficialHoxiReleaseOrigin(res.Mirrors.Global) || isOfficialHoxiReleaseOrigin(res.Mirrors.CDN)
	derivedCDN, derivedGitHub := "", ""
	if canDeriveOfficial {
		derivedCDN, derivedGitHub = deriveOfficialMirrors(res.LatestVersion, assetName)
	}

	switch source {
	case DownloadSourceGlobal:
		add(res.Mirrors.Global, string(DownloadSourceGlobal), false)
		if primarySource == string(DownloadSourceGlobal) {
			add(rawPrimary, string(DownloadSourceGlobal), false)
		}
		add(derivedGitHub, string(DownloadSourceGlobal), fileDiffersFromPrimary(derivedGitHub))
		add(rawPrimary, primarySource, false)
		add(res.Mirrors.CDN, string(DownloadSourceCDN), false)
		add(res.Mirrors.CN, "cn", false)
		add(derivedCDN, string(DownloadSourceCDN), fileDiffersFromPrimary(derivedCDN))
	case DownloadSourceCDN:
		add(res.Mirrors.CN, "cn", false)
		add(res.Mirrors.CDN, string(DownloadSourceCDN), false)
		if primarySource != string(DownloadSourceGlobal) {
			add(rawPrimary, primarySource, false)
		}
		add(derivedCDN, string(DownloadSourceCDN), fileDiffersFromPrimary(derivedCDN))
		add(rawPrimary, primarySource, false)
		add(res.Mirrors.Global, string(DownloadSourceGlobal), false)
		add(derivedGitHub, string(DownloadSourceGlobal), fileDiffersFromPrimary(derivedGitHub))
	default: // DownloadSourceAuto
		if hasExplicitAccelerated {
			add(res.Mirrors.CN, "cn", false)
			add(res.Mirrors.CDN, string(DownloadSourceCDN), false)
			add(rawPrimary, primarySource, false)
			add(res.Mirrors.Global, string(DownloadSourceGlobal), false)
			add(derivedGitHub, string(DownloadSourceGlobal), fileDiffersFromPrimary(derivedGitHub))
		} else {
			// Respect the server's DownloadURL first (CDN once published to cdn.hoxi.dev,
			// or GitHub for legacy releases), and automatically add the complementary
			// official mirror as a failover candidate.
			add(rawPrimary, primarySource, false)
			if primarySource == string(DownloadSourceCDN) {
				add(res.Mirrors.Global, string(DownloadSourceGlobal), false)
				add(derivedGitHub, string(DownloadSourceGlobal), fileDiffersFromPrimary(derivedGitHub))
			} else {
				add(res.Mirrors.Global, string(DownloadSourceGlobal), false)
				add(derivedGitHub, string(DownloadSourceGlobal), fileDiffersFromPrimary(derivedGitHub))
				add(derivedCDN, string(DownloadSourceCDN), fileDiffersFromPrimary(derivedCDN))
			}
		}
	}

	if len(out) == 0 {
		for _, fallback := range []struct {
			raw   string
			label string
		}{
			{res.Mirrors.CN, "cn"},
			{res.Mirrors.CDN, string(DownloadSourceCDN)},
			{res.DownloadURL, ""},
			{res.Mirrors.Global, string(DownloadSourceGlobal)},
		} {
			trimmed := strings.TrimSpace(fallback.raw)
			if trimmed != "" && !seen[trimmed] {
				seen[trimmed] = true
				label := fallback.label
				if label == "" {
					label = classifyUpdateSource(trimmed)
				}
				out = append(out, downloadCandidate{URL: trimmed, Source: label})
			}
		}
	}
	return out
}

func resolveDarwinDMGCandidates(res *UpdateCheckResult, pref DownloadSource) []downloadCandidate {
	arch := darwinReleaseArch()
	ver := strings.TrimPrefix(strings.TrimSpace(res.LatestVersion), "v")
	assetName := ""
	if ver != "" {
		assetName = fmt.Sprintf("Hoxi-%s-darwin-%s.dmg", ver, arch)
	}
	return resolveArchiveCandidates(res, pref, assetName, func(raw string) (string, bool) {
		return normalizeDarwinCandidateURL(raw, arch)
	})
}

func resolveWindowsArchiveCandidates(res *UpdateCheckResult, pref DownloadSource) []downloadCandidate {
	arch := windowsReleaseArch()
	ver := strings.TrimPrefix(strings.TrimSpace(res.LatestVersion), "v")
	assetName := ""
	if ver != "" {
		assetName = fmt.Sprintf("Hoxi-%s-windows-%s.zip", ver, arch)
	}
	return resolveArchiveCandidates(res, pref, assetName, func(raw string) (string, bool) {
		return normalizeWindowsCandidateURL(raw, arch)
	})
}

func resolveCandidatesSHA256(
	ctx context.Context,
	client *http.Client,
	candidates []downloadCandidate,
	activeIdx int,
	fallbackSHA256 string,
) (string, error) {
	activeFile := ""
	if activeIdx >= 0 && activeIdx < len(candidates) {
		active := candidates[activeIdx]
		activeFile = candidateFileName(active.URL)
		// If the official upgrade API already returned a 64-char SHA-256 for this exact
		// unrewritten archive, trust that TLS-authenticated digest directly without an
		// extra roundtrip to a sidecar file.
		if !active.Rewritten {
			if sum := parseSHA256Text(fallbackSHA256); sum != "" {
				return sum, nil
			}
		}
		if sum, err := resolveArchiveSHA256(ctx, client, active.URL, ""); err == nil && sum != "" {
			return sum, nil
		}
	}
	for i, cand := range candidates {
		if i == activeIdx {
			continue
		}
		// Another mirror's checksum only describes the active download when it
		// names the same file; a different arch or a .zip rewritten from
		// -setup.exe would make every byte look corrupt.
		if activeFile != "" && candidateFileName(cand.URL) != activeFile {
			continue
		}
		if sum, err := resolveArchiveSHA256(ctx, client, cand.URL, ""); err == nil && sum != "" {
			return sum, nil
		}
	}
	return "", errors.New("missing SHA-256 checksum for update archive")
}

func candidateFileName(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Path == "" {
		return ""
	}
	return path.Base(u.Path)
}

type slowTransferReader struct {
	r                 io.Reader
	startedAt         time.Time
	alreadyDownloaded int64
	bytesRead         int64
	totalBytes        int64
	window            time.Duration
	minBPS            int64
	cancel            context.CancelCauseFunc
}

func (s *slowTransferReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.bytesRead += int64(n)
		if s.window > 0 && s.minBPS > 0 {
			elapsed := time.Since(s.startedAt)
			if elapsed >= s.window {
				avgBPS := int64(float64(s.bytesRead) / elapsed.Seconds())
				// Only fail over when meaningfully below threshold and not already mostly done.
				totalDownloaded := s.alreadyDownloaded + s.bytesRead
				mostlyDone := s.totalBytes > 0 && totalDownloaded*2 >= s.totalBytes
				if avgBPS < s.minBPS && !mostlyDone {
					cause := fmt.Errorf("update download too slow (%d B/s), switching mirror", avgBPS)
					s.cancel(cause)
					return n, cause
				}
			}
		}
	}
	return n, err
}

func parseContentRangeStartAndTotal(header string) (start, total int64, ok bool) {
	// Format: "bytes 100-199/1000"
	trimmed := strings.TrimSpace(header)
	if !strings.HasPrefix(strings.ToLower(trimmed), "bytes ") {
		return 0, 0, false
	}
	spec := strings.TrimSpace(trimmed[len("bytes "):])
	rangePart, totalPart, hasSlash := strings.Cut(spec, "/")
	if !hasSlash {
		return 0, 0, false
	}
	startPart, _, hasDash := strings.Cut(strings.TrimSpace(rangePart), "-")
	if !hasDash {
		return 0, 0, false
	}
	s, err := strconv.ParseInt(strings.TrimSpace(startPart), 10, 64)
	if err != nil || s < 0 {
		return 0, 0, false
	}
	var t int64
	if totalPart != "" && totalPart != "*" {
		parsedTotal, err := strconv.ParseInt(strings.TrimSpace(totalPart), 10, 64)
		if err == nil && parsedTotal > 0 {
			t = parsedTotal
		}
	}
	return s, t, true
}

func hashFileSHA256(filePath string) (string, int64, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", n, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func downloadFileWithFallback(
	ctx context.Context,
	client *http.Client,
	candidates []downloadCandidate,
	fallbackSHA256 string,
	dstPath string,
	report func(InstallProgress),
) error {
	if len(candidates) == 0 {
		return errors.New("update download URL is missing")
	}
	if client == nil {
		client = newUpdateHTTPClient()
	}
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o700); err != nil {
		return err
	}

	var lastErr error
	shaByFilename := make(map[string]string)
	for idx, cand := range candidates {
		if !isAllowedUpdateURL(cand.URL) {
			lastErr = errors.New("update download URL must use HTTPS")
			continue
		}
		hasNext := idx < len(candidates)-1
		err := downloadSingleCandidateWithResume(ctx, client, candidates, idx, fallbackSHA256, shaByFilename, dstPath, hasNext, report)
		if err == nil {
			return nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return err
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return errors.New("update download failed")
}

func downloadSingleCandidateWithResume(
	ctx context.Context,
	client *http.Client,
	candidates []downloadCandidate,
	idx int,
	fallbackSHA256 string,
	shaByFilename map[string]string,
	dstPath string,
	hasNextMirror bool,
	report func(InstallProgress),
) error {
	cand := candidates[idx]
	fileKey := ""
	if u, err := url.Parse(cand.URL); err == nil {
		fileKey = path.Base(u.Path)
	}
	expectedSHA := shaByFilename[fileKey]
	if expectedSHA == "" {
		resolved, err := resolveCandidatesSHA256(ctx, client, candidates, idx, fallbackSHA256)
		if err != nil {
			return err
		}
		expectedSHA = resolved
		if fileKey != "" {
			shaByFilename[fileKey] = expectedSHA
		}
	}

	partPath := dstPath + ".part"
	_ = os.Remove(partPath)
	defer func() { _ = os.Remove(partPath) }()

	stallLimit := downloadStallTimeout
	if hasNextMirror && downloadFailoverStallTimeout > 0 && downloadFailoverStallTimeout < stallLimit {
		stallLimit = downloadFailoverStallTimeout
	}

	var (
		offset        int64
		knownTotal    int64
		rangeRejected bool
	)

	for attempt := 0; attempt <= downloadMaxResumeAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		reqCtx, reqCancel := context.WithCancelCause(ctx)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, cand.URL, nil)
		if err != nil {
			reqCancel(nil)
			return err
		}
		sentRange := offset > 0 && !rangeRejected
		if sentRange {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}

		resp, err := client.Do(req)
		if err != nil {
			reqCancel(nil)
			if offset > 0 && !rangeRejected && attempt < downloadMaxResumeAttempts && !hasNextMirror {
				continue
			}
			return fmt.Errorf("download update archive (%s): %w", path.Base(cand.URL), err)
		}

		if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && sentRange {
			_ = resp.Body.Close()
			reqCancel(nil)
			if knownTotal > 0 && offset == knownTotal {
				break
			}
			offset = 0
			rangeRejected = true
			_ = os.Remove(partPath)
			continue
		}

		resuming := sentRange && resp.StatusCode == http.StatusPartialContent
		if resuming {
			rangeStart, rangeTotal, validRange := parseContentRangeStartAndTotal(resp.Header.Get("Content-Range"))
			if !validRange || rangeStart != offset {
				// Misbehaving proxy returned a 206 starting at a different offset; do not
				// append mismatched bytes into the .part file.
				_ = resp.Body.Close()
				reqCancel(nil)
				offset = 0
				rangeRejected = true
				_ = os.Remove(partPath)
				continue
			}
			if rangeTotal > knownTotal {
				knownTotal = rangeTotal
			}
		}
		if sentRange && resp.StatusCode == http.StatusOK {
			// Origin ignored Range header; do not loop endlessly re-downloading from 0.
			rangeRejected = true
		}
		if !resuming && resp.StatusCode != http.StatusOK {
			status := resp.StatusCode
			_ = resp.Body.Close()
			reqCancel(nil)
			return fmt.Errorf("update download returned HTTP %d", status)
		}

		var (
			openFlags int
			totalSize int64
		)
		if resuming {
			openFlags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
			if knownTotal > 0 {
				totalSize = knownTotal
			} else if resp.ContentLength > 0 {
				totalSize = offset + resp.ContentLength
			}
		} else {
			offset = 0
			openFlags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
			if resp.ContentLength > 0 {
				totalSize = resp.ContentLength
			}
		}
		if totalSize > knownTotal {
			knownTotal = totalSize
		}

		outFile, err := os.OpenFile(partPath, openFlags, 0o600)
		if err != nil {
			_ = resp.Body.Close()
			reqCancel(nil)
			return err
		}

		remainingLimit := maxUpdateArchiveBytes - offset + 1
		if remainingLimit <= 0 {
			_ = outFile.Close()
			_ = resp.Body.Close()
			reqCancel(nil)
			return errors.New("update archive exceeds maximum allowed size")
		}

		var reader io.Reader = &stallTimeoutReader{r: resp.Body, timeout: stallLimit, cancel: reqCancel}
		if hasNextMirror && downloadSlowFailoverWindow > 0 && downloadSlowFailoverMinBPS > 0 {
			reader = &slowTransferReader{
				r:                 reader,
				startedAt:         time.Now(),
				alreadyDownloaded: offset,
				bytesRead:         0,
				totalBytes:        knownTotal,
				window:            downloadSlowFailoverWindow,
				minBPS:            downloadSlowFailoverMinBPS,
				cancel:            reqCancel,
			}
		}

		written, copyErr := io.Copy(
			withDownloadProgressFrom(outFile, offset, knownTotal, cand.Source, report),
			io.LimitReader(reader, remainingLimit),
		)
		closeErr := outFile.Close()
		_ = resp.Body.Close()
		cause := context.Cause(reqCtx)
		reqCancel(nil)

		offset += written
		if offset > maxUpdateArchiveBytes {
			return errors.New("update archive exceeds maximum allowed size")
		}
		if closeErr != nil {
			return closeErr
		}
		if copyErr == nil {
			if knownTotal > 0 && offset < knownTotal && !rangeRejected && attempt < downloadMaxResumeAttempts {
				// Truncated transfer before reaching Content-Length: resume via Range.
				continue
			}
			break
		}

		// If failover aborted due to sustained slowness, immediately switch to the next mirror.
		if cause != nil && strings.Contains(cause.Error(), "switching mirror") {
			return cause
		}
		if cause != nil && errors.Is(copyErr, context.Canceled) {
			// If we already made progress and are on the final mirror, try resuming with Range.
			if offset > 0 && !rangeRejected && attempt < downloadMaxResumeAttempts && !hasNextMirror {
				continue
			}
			return cause
		}
		if offset > 0 && !rangeRejected && attempt < downloadMaxResumeAttempts {
			continue
		}
		return copyErr
	}

	if report != nil {
		report(InstallProgress{
			Phase:           "verifying",
			DownloadedBytes: offset,
			TotalBytes:      offset,
			Source:          cand.Source,
		})
	}
	actualSHA, _, err := hashFileSHA256(partPath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(actualSHA, expectedSHA) {
		return fmt.Errorf("update archive SHA-256 mismatch: got %s, want %s", actualSHA, expectedSHA)
	}
	if err := os.Rename(partPath, dstPath); err != nil {
		return err
	}
	return nil
}

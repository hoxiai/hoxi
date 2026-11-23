package updater

import (
	"io"
	"sync/atomic"
)

// InstallProgress describes the self-updater, which has no native progress UI.
// Snapshots are immutable so status polling never waits on download or staging.
type InstallProgress struct {
	Phase           string `json:"phase"`
	DownloadedBytes int64  `json:"downloadedBytes"`
	TotalBytes      int64  `json:"totalBytes"`
	Source          string `json:"source,omitempty"`
	Error           string `json:"error,omitempty"`
}

var installProgress atomic.Pointer[InstallProgress]

func setInstallPhase(phase string) {
	tracePhase(phase)
	installProgress.Store(&InstallProgress{Phase: phase})
}

func finishInstallProgress(err error) {
	if err != nil {
		tracePhase("failed")
		installProgress.Store(&InstallProgress{Phase: "failed", Error: err.Error()})
	}
}

type downloadProgressWriter struct {
	downloaded int64
	total      int64
	source     string
	report     func(InstallProgress)
}

func (w *downloadProgressWriter) Write(p []byte) (int, error) {
	w.downloaded += int64(len(p))
	w.report(InstallProgress{
		Phase:           "downloading",
		DownloadedBytes: w.downloaded,
		TotalBytes:      w.total,
		Source:          w.source,
	})
	return len(p), nil
}

func withDownloadProgress(dst io.Writer, total int64, report func(InstallProgress)) io.Writer {
	return withDownloadProgressFrom(dst, 0, total, "", report)
}

func withDownloadProgressFrom(dst io.Writer, initialDownloaded, total int64, source string, report func(InstallProgress)) io.Writer {
	if report == nil {
		return dst
	}
	if total < 0 {
		total = 0 // Chunked responses have no percentage; report bytes only.
	}
	if initialDownloaded < 0 {
		initialDownloaded = 0
	}
	report(InstallProgress{
		Phase:           "downloading",
		DownloadedBytes: initialDownloaded,
		TotalBytes:      total,
		Source:          source,
	})
	return io.MultiWriter(dst, &downloadProgressWriter{
		downloaded: initialDownloaded,
		total:      total,
		source:     source,
		report:     report,
	})
}

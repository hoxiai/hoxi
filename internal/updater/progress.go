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
	Error           string `json:"error,omitempty"`
}

var installProgress atomic.Pointer[InstallProgress]

func setInstallPhase(phase string) {
	installProgress.Store(&InstallProgress{Phase: phase})
}

func finishInstallProgress(err error) {
	if err != nil {
		installProgress.Store(&InstallProgress{Phase: "failed", Error: err.Error()})
	}
}

type downloadProgressWriter struct {
	downloaded int64
	total      int64
	report     func(InstallProgress)
}

func (w *downloadProgressWriter) Write(p []byte) (int, error) {
	w.downloaded += int64(len(p))
	w.report(InstallProgress{Phase: "downloading", DownloadedBytes: w.downloaded, TotalBytes: w.total})
	return len(p), nil
}

func withDownloadProgress(dst io.Writer, total int64, report func(InstallProgress)) io.Writer {
	if report == nil {
		return dst
	}
	if total < 0 {
		total = 0 // Chunked responses have no percentage; report bytes only.
	}
	report(InstallProgress{Phase: "downloading", TotalBytes: total})
	return io.MultiWriter(dst, &downloadProgressWriter{total: total, report: report})
}

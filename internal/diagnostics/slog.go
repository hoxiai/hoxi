package diagnostics

import (
	"context"
	"io"
	"log"
	"log/slog"
	"strings"
)

type logHandler struct {
	stderr  slog.Handler
	attrs   []slog.Attr
	grouped bool
}

// Exact static event names reviewed in internal/app/app.go. Never add dynamic
// strings or error text here; unrecognized messages retain the generic label.
var staticEvents = map[string]bool{
	"diagnostics storage unavailable":                                             true,
	"loopback server stopped unexpectedly":                                        true,
	"opencode daemon exited unexpectedly":                                         true,
	"started opencode daemon":                                                     true,
	"failed to allocate free port for opencode":                                   true,
	"failed to generate opencode password":                                        true,
	"bridge address must use a loopback IP":                                       true,
	"failed to bind preferred loopback address, falling back to random port":      true,
	"failed to bind loopback listener":                                            true,
	"failed to generate browser credential":                                       true,
	"failed to prepare browser tools":                                             true,
	"initial opencode start failed; will retry in background":                     true,
	"failed to resolve or install opencode":                                       true,
	"failed to start opencode daemon":                                             true,
	"hoxi headless server is ready":                                               true,
	"wails application exited with error":                                         true,
	"background opencode resolution failed":                                       true,
	"background opencode daemon start failed":                                     true,
	"native updates unavailable":                                                  true,
	"desktop authorization callback was not completed; polling remains available": true,
	"session title summarizer fell back to prompt":                                true,
	"http server error":                                                           true,
}

// NewHandler preserves normal stderr logging. Disk records accept only reviewed
// static messages and metadata; free-form messages and attributes are omitted.
func NewHandler(stderr io.Writer) slog.Handler {
	if stderr == nil {
		stderr = io.Discard
	}
	return &logHandler{stderr: slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo})}
}

func (h *logHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.stderr.Enabled(ctx, level)
}

func (h *logHandler) Handle(ctx context.Context, r slog.Record) error {
	recognized := staticEvents[r.Message]
	// Skip persisting routine unreviewed Info/Debug records as generic
	// "system log event" placeholders; warnings, errors, and reviewed static
	// events are always persisted.
	if !recognized && r.Level < slog.LevelWarn {
		return h.stderr.Handle(ctx, r)
	}
	e := Entry{Kind: "system", Level: r.Level.String(), Source: "slog", Message: "system log event"}
	if recognized {
		e.Message = r.Message
	}
	// Only metadata in the root group is accepted; no arbitrary attr values.
	apply := func(a slog.Attr) {
		if a.Key == "exitCode" && a.Value.Kind() == slog.KindInt64 {
			code := a.Value.Int64()
			e.ExitCode = &code
			return
		}
		if a.Value.Kind() != slog.KindString {
			return
		}
		switch a.Key {
		case "source":
			e.Source = a.Value.String()
		case "sessionID":
			e.SessionID = a.Value.String()
		case "providerID":
			e.ProviderID = a.Value.String()
		case "modelID":
			e.ModelID = a.Value.String()
		}
	}
	for _, a := range h.attrs {
		apply(a)
	}
	if !h.grouped {
		r.Attrs(func(a slog.Attr) bool { apply(a); return true })
	}
	Record(e)
	return h.stderr.Handle(ctx, r)
}

func (h *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	copy := *h
	copy.stderr = h.stderr.WithAttrs(attrs)
	if !h.grouped {
		copy.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	}
	return &copy
}

func (h *logHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	copy := *h
	copy.stderr = h.stderr.WithGroup(name)
	copy.grouped = true
	return &copy
}

type quietHTTPErrorWriter struct{}

func (quietHTTPErrorWriter) Write(p []byte) (int, error) {
	msg := strings.TrimSpace(string(p))
	if msg == "" {
		return len(p), nil
	}
	lower := strings.ToLower(msg)
	if lower == "eof" || strings.HasSuffix(lower, ": eof") || strings.HasSuffix(lower, " eof") {
		return len(p), nil
	}
	for _, benign := range []string{
		"context canceled",
		"broken pipe",
		"connection reset by peer",
		"use of closed network connection",
		"unexpected eof",
		"superfluous response.writeheader",
	} {
		if strings.Contains(lower, benign) {
			return len(p), nil
		}
	}
	slog.Warn("http server error", "source", "http", "error", RedactSecrets(msg))
	return len(p), nil
}

// NewQuietHTTPServerLogger returns a *log.Logger suitable for http.Server and
// httputil.ReverseProxy ErrorLog fields. Routine client-disconnect noise is
// suppressed while unexpected transport/server errors route through slog.
func NewQuietHTTPServerLogger() *log.Logger {
	return log.New(quietHTTPErrorWriter{}, "", 0)
}

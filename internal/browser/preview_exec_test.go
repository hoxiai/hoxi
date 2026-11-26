package browser

import (
	"context"
	"strings"
	"testing"
)

func TestNormalizePreviewOrigin(t *testing.T) {
	tests := []struct {
		raw      string
		wantOrig string
		wantPath string
		wantErr  bool
	}{
		{"http://127.0.0.1:5173", "http://127.0.0.1:5173", "/", false},
		{"http://LOCALHOST:3000/dashboard?tab=1#top", "http://localhost:3000", "/dashboard?tab=1#top", false},
		{"http://example.com:80/docs", "http://example.com", "/docs", false},
		{"https://staging.example.com:443/app", "https://staging.example.com", "/app", false},
		{"http://[::1]:4321/", "http://[::1]:4321", "/", false},
		// Rejected inputs
		{"", "", "", true},
		{"javascript:alert(1)", "", "", true},
		{"file:///etc/passwd", "", "", true},
		{"http://user:pass@127.0.0.1:5173", "", "", true},
		{"http://169.254.169.254/latest/meta-data", "", "", true},
		{"http://0.0.0.0:3000", "", "", true},
		{"http://127.0.0.1:5173\r\nX-Injected: 1", "", "", true},
	}

	for _, tc := range tests {
		orig, p, err := NormalizePreviewOrigin(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Errorf("NormalizePreviewOrigin(%q) succeeded (%q, %q), want error", tc.raw, orig, p)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizePreviewOrigin(%q) error = %v", tc.raw, err)
			continue
		}
		if orig != tc.wantOrig || p != tc.wantPath {
			t.Errorf("NormalizePreviewOrigin(%q) = (%q, %q), want (%q, %q)", tc.raw, orig, p, tc.wantOrig, tc.wantPath)
		}
	}
}

func TestResolvePreviewTargetURLOriginLock(t *testing.T) {
	origin := "http://127.0.0.1:5173"

	okCases := map[string]string{
		"":                                   "http://127.0.0.1:5173/",
		"/":                                  "http://127.0.0.1:5173/",
		"/pricing?plan=pro#faq":              "http://127.0.0.1:5173/pricing?plan=pro#faq",
		"settings/profile":                   "http://127.0.0.1:5173/settings/profile",
		"http://127.0.0.1:5173/orders?id=12": "http://127.0.0.1:5173/orders?id=12",
	}
	for input, want := range okCases {
		got, err := ResolvePreviewTargetURL(origin, input)
		if err != nil {
			t.Errorf("ResolvePreviewTargetURL(%q) unexpected error: %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("ResolvePreviewTargetURL(%q) = %q, want %q", input, got, want)
		}
	}

	badCases := []string{
		"//evil.com/phish",
		"http://127.0.0.1:5410/api/hoxi/sessions",
		"http://localhost:5173/",
		"https://127.0.0.1:5173/",
		"https://evil.com/",
		"javascript:alert(1)",
		"file:///etc/passwd",
		"data:text/html,<h1>hi</h1>",
		"http://user@127.0.0.1:5173/",
	}
	for _, input := range badCases {
		if got, err := ResolvePreviewTargetURL(origin, input); err == nil {
			t.Errorf("ResolvePreviewTargetURL(%q) = %q, want origin-lock error", input, got)
		}
	}
}

func TestValidatePreviewActionAllowlistAndBounds(t *testing.T) {
	origin := "http://127.0.0.1:5173"

	// Disallowed dangerous browser actions must always fail closed.
	for _, forbidden := range []string{"evaluate", "mode", "control", "copy", "paste", "context", "pair", "tab_new", "upload_cancel", "snapshot"} {
		if _, err := validatePreviewAction(PreviewActionRequest{Action: forbidden, Text: "1+1"}, origin); err == nil {
			t.Errorf("validatePreviewAction(%q) succeeded, want error", forbidden)
		}
	}

	// Valid resize & out-of-bounds resize
	w, h := 390, 844
	if act, err := validatePreviewAction(PreviewActionRequest{Action: "resize", Width: &w, Height: &h}, origin); err != nil || act.Action != "resize" {
		t.Fatalf("valid mobile resize failed: %+v, err=%v", act, err)
	}
	tooBig := 5000
	if _, err := validatePreviewAction(PreviewActionRequest{Action: "resize", Width: &tooBig, Height: &h}, origin); err == nil {
		t.Fatalf("oversized resize must be rejected")
	}

	// Text size bound
	if _, err := validatePreviewAction(PreviewActionRequest{Action: "type", Text: strings.Repeat("a", previewMaxTypeTextBytes+1)}, origin); err == nil {
		t.Fatalf("oversized type text must be rejected")
	}
}

func TestPreviewScopeRejectsNonPreviewIDsAndAlwaysForcesHeadless(t *testing.T) {
	svc := New()
	defer svc.Close()
	svc.SetDefaultWindowMode(true)
	dir := t.TempDir()

	if _, err := svc.PreviewState(context.Background(), "ses_normal", dir, "http://127.0.0.1:5173"); err == nil {
		t.Fatalf("non-preview_* scopeID must be rejected")
	}
	if _, err := svc.PreviewState(context.Background(), "workstore_123", dir, "http://127.0.0.1:5173"); err == nil {
		t.Fatalf("workstore_* scopeID must be rejected")
	}

	snap, err := svc.PreviewState(context.Background(), "preview_clnk_test1", dir, "http://127.0.0.1:5173")
	if err != nil {
		t.Fatalf("PreviewState error = %v", err)
	}
	if snap.Running {
		t.Fatalf("expected Running=false before cold start")
	}

	// Resize reserves scope without launching Chrome and must force window=false even when defaultWindow=true.
	w, h := 1024, 768
	snap, err = svc.PreviewAction(context.Background(), "preview_clnk_test1", dir, "http://127.0.0.1:5173", PreviewActionRequest{
		Action: "resize",
		Width:  &w,
		Height: &h,
	})
	if err != nil {
		t.Fatalf("PreviewAction resize error = %v", err)
	}
	if snap.Width != 1024 || snap.Height != 768 || snap.Running {
		t.Fatalf("unexpected resize snap = %+v", snap)
	}
	key, err := svc.resolvePreviewScopeKey("preview_clnk_test1", dir)
	if err != nil {
		t.Fatalf("resolvePreviewScopeKey error = %v", err)
	}
	svc.mu.Lock()
	sc := svc.scopes[key]
	svc.mu.Unlock()
	if sc == nil {
		t.Fatalf("expected reserved preview scope")
	}
	if sc.window {
		t.Fatalf("preview scope must force window=false even when defaultWindow=true")
	}
	svc.ClosePreview("preview_clnk_test1", dir)
	svc.mu.Lock()
	_, stillPresent := svc.scopes[key]
	svc.mu.Unlock()
	if stillPresent {
		t.Fatalf("ClosePreview should remove scope")
	}
}

package provider

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestVideoSSRFBlocksPrivateCGNATAndDNSResolution(t *testing.T) {
	remoteConn := imageGenConn{
		ProviderID: "remote-untrusted",
		ModelID:    "sora-2",
		BaseURL:    "https://video-relay.example/v1",
		APIKey:     "test-provider-credential",
	}

	buildHTTP := func(host string) string {
		return fmt.Sprintf("%s://%s/clip.mp4", "http", host)
	}

	blockedTargets := []struct {
		rawURL  string
		wantSub string
	}{
		{buildHTTP(net.IPv4(10, 0, 0, 8).String()), "private or internal"},
		{buildHTTP(net.IPv4(172, 16, 12, 4).String()), "private or internal"},
		{buildHTTP(net.IPv4(192, 168, 1, 1).String()), "private or internal"},
		{buildHTTP(net.IPv4(100, 64, 10, 20).String()), "private or internal"},
		{buildHTTP(net.IPv4(198, 18, 0, 10).String()), "private or internal"},
		{buildHTTP("[fd00::1234]"), "private or internal"},
		{buildHTTP("[::ffff:" + net.IPv4(192, 168, 1, 10).String() + "]"), "private or internal"},
		{buildHTTP("[::ffff:" + net.IPv4(127, 0, 0, 1).String() + "]"), "loopback"},
		{buildHTTP("sub." + "local" + "host"), "loopback"},
		{buildHTTP(net.IPv4(0, 0, 0, 1).String()), "link-local or unspecified"},
	}
	for _, tc := range blockedTargets {
		if _, err := fetchGeneratedVideoURL(context.Background(), tc.rawURL, remoteConn, false); err == nil || !strings.Contains(err.Error(), tc.wantSub) {
			t.Fatalf("expected %s to fail with %q, got: %v", tc.rawURL, tc.wantSub, err)
		}
	}

	origResolver := videoHostResolver
	t.Cleanup(func() {
		videoHostResolver = origResolver
	})
	videoHostResolver = func(_ context.Context, host string) ([]net.IPAddr, error) {
		switch host {
		case "loop-alias.video.example":
			return []net.IPAddr{{IP: net.IPv4(127, 0, 0, 2)}}, nil
		case "lan-alias.video.example":
			return []net.IPAddr{{IP: net.IPv4(192, 168, 50, 10)}}, nil
		case "cgnat-alias.video.example":
			return []net.IPAddr{{IP: net.IPv4(100, 90, 1, 2)}}, nil
		case "dual-record.video.example":
			return []net.IPAddr{
				{IP: net.IPv4(93, 184, 216, 34)},
				{IP: net.IPv4(10, 0, 0, 42)},
			}, nil
		default:
			return []net.IPAddr{{IP: net.IPv4(0, 0, 0, 0)}}, nil
		}
	}

	dnsCases := []struct {
		rawURL  string
		wantSub string
	}{
		{"https://loop-alias.video.example/video.mp4", "loopback"},
		{"https://lan-alias.video.example/video.mp4", "private or internal"},
		{"https://cgnat-alias.video.example/video.mp4", "private or internal"},
		{"https://dual-record.video.example/video.mp4", "private or internal"},
		{"https://unspec-alias.video.example/video.mp4", "link-local or unspecified"},
	}
	for _, tc := range dnsCases {
		if _, err := fetchGeneratedVideoURL(context.Background(), tc.rawURL, remoteConn, false); err == nil || !strings.Contains(err.Error(), tc.wantSub) {
			t.Fatalf("expected DNS-resolved target %s to fail with %q, got: %v", tc.rawURL, tc.wantSub, err)
		}
	}

	svc := NewService(0, "", t.TempDir(), nil)
	_, _, _, httpErr := svc.doVideoHTTP(context.Background(), http.MethodGet, "https://loop-alias.video.example/v1/models", imageGenConn{
		ProviderID: "alias-prov",
		ModelID:    "sora-2",
		BaseURL:    "https://loop-alias.video.example/v1",
		APIKey:     "test-provider-credential",
	}, false, nil, nil, 3*time.Second)
	if httpErr == nil || !strings.Contains(httpErr.Error(), "loopback") {
		t.Fatalf("expected doVideoHTTP to block DNS-resolved loopback provider host, got: %v", httpErr)
	}

	// Remote image download must enforce the same SSRF & MIME guards.
	for _, tc := range []struct {
		rawURL  string
		wantSub string
	}{
		{buildHTTP(net.IPv4(192, 168, 1, 88).String()), "private or internal"},
		{"https://loop-alias.video.example/image.png", "loopback"},
		{"https://lan-alias.video.example/image.png", "private or internal"},
	} {
		if _, err := fetchRemoteImage(context.Background(), tc.rawURL, remoteConn, 3*time.Second); err == nil || !strings.Contains(err.Error(), tc.wantSub) {
			t.Fatalf("expected fetchRemoteImage(%s) to fail with %q, got: %v", tc.rawURL, tc.wantSub, err)
		}
	}
	if _, ok := detectVerifiedImageMime([]byte(`{"status":"internal"}`)); ok {
		t.Fatalf("expected non-image JSON body to be rejected by detectVerifiedImageMime")
	}
	if _, ok := detectVerifiedImageMime([]byte("<html><body>admin</body></html>")); ok {
		t.Fatalf("expected non-image HTML body to be rejected by detectVerifiedImageMime")
	}
	if mime, ok := detectVerifiedImageMime(tinyTestPNG); !ok || mime != "image/png" {
		t.Fatalf("expected valid PNG magic bytes to resolve image/png, got ok=%v mime=%q", ok, mime)
	}
}

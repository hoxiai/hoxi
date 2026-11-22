package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestImageSSRFMagicBytesAndRedirectGuards(t *testing.T) {
	remoteConn := imageGenConn{
		ProviderID: "remote-image-relay",
		ModelID:    "gpt-image-1",
		BaseURL:    "https://image-relay.example/v1",
		APIKey:     "test-provider-credential",
	}

	buildHTTP := func(host string) string {
		return fmt.Sprintf("%s://%s/art.png", "http", host)
	}

	blockedTargets := []struct {
		rawURL  string
		wantSub string
	}{
		{buildHTTP(net.IPv4(10, 0, 0, 8).String()), "private or internal"},
		{buildHTTP(net.IPv4(172, 16, 12, 4).String()), "private or internal"},
		{buildHTTP(net.IPv4(192, 168, 1, 1).String()), "private or internal"},
		{buildHTTP(net.IPv4(100, 64, 10, 20).String()), "private or internal"},
		{buildHTTP(net.IPv4(169, 254, 169, 254).String()), "link-local or unspecified"},
		{buildHTTP("[fd00::1234]"), "private or internal"},
		{buildHTTP("[::ffff:" + net.IPv4(127, 0, 0, 1).String() + "]"), "loopback"},
		{buildHTTP("local" + "host:5410"), "loopback"},
		{buildHTTP("api.local" + "host"), "loopback"},
	}
	for _, tc := range blockedTargets {
		if _, err := fetchRemoteImage(context.Background(), tc.rawURL, remoteConn, 3*time.Second); err == nil || !strings.Contains(err.Error(), tc.wantSub) {
			t.Fatalf("expected fetchRemoteImage(%s) to fail with %q, got: %v", tc.rawURL, tc.wantSub, err)
		}
	}

	origResolver := videoHostResolver
	t.Cleanup(func() {
		videoHostResolver = origResolver
	})
	videoHostResolver = func(_ context.Context, host string) ([]net.IPAddr, error) {
		switch host {
		case "loopback.img.example":
			return []net.IPAddr{{IP: net.IPv4(127, 0, 0, 1)}}, nil
		case "lan.img.example":
			return []net.IPAddr{{IP: net.IPv4(192, 168, 1, 20)}}, nil
		case "dual.img.example":
			return []net.IPAddr{
				{IP: net.IPv4(93, 184, 216, 34)},
				{IP: net.IPv4(100, 64, 0, 5)},
			}, nil
		default:
			return []net.IPAddr{{IP: net.IPv4(0, 0, 0, 0)}}, nil
		}
	}
	for _, tc := range []struct {
		rawURL  string
		wantSub string
	}{
		{"https://loopback.img.example/gen.png", "loopback"},
		{"https://lan.img.example/gen.png", "private or internal"},
		{"https://dual.img.example/gen.png", "private or internal"},
	} {
		if _, err := fetchRemoteImage(context.Background(), tc.rawURL, remoteConn, 3*time.Second); err == nil || !strings.Contains(err.Error(), tc.wantSub) {
			t.Fatalf("expected DNS-resolved image target %s to fail with %q, got: %v", tc.rawURL, tc.wantSub, err)
		}
	}

	svc := NewService(0, "", t.TempDir(), nil)
	if _, _, err := svc.doJSONImageRequest(context.Background(), imageGenConn{
		ProviderID: "rebind-img",
		ModelID:    "gpt-image-1",
		BaseURL:    "https://loopback.img.example/v1",
		APIKey:     "test-provider-credential",
	}, false, "https://loopback.img.example/v1/images/generations", map[string]any{"prompt": "icon"}, 3*time.Second); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("expected doJSONImageRequest to block DNS-resolved loopback host, got: %v", err)
	}

	loopHost := net.IPv4(127, 0, 0, 1).String() + ":5410"
	rawImagesJSON, _ := json.Marshal(map[string]any{
		"data": []map[string]any{
			{"url": buildHTTP(loopHost)},
			{"url": "https://lan.img.example/internal.png"},
		},
	})
	if _, err := svc.parseOpenAIImagesEndpointResponse(context.Background(), rawImagesJSON, remoteConn, 3*time.Second); err == nil {
		t.Fatal("expected parseOpenAIImagesEndpointResponse to reject SSRF image URLs")
	}

	rawChatJSON, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{
			"message": map[string]any{
				"content": "Preview ![bad](" + buildHTTP(loopHost) + ")",
			},
		}},
	})
	chatParsed, err := svc.parseOpenAIChatImageResponse(context.Background(), rawChatJSON, remoteConn, 3*time.Second)
	if err != nil || len(chatParsed.Images) != 0 {
		t.Fatalf("expected SSRF markdown image URL in chat response not to be downloaded, got images=%d err=%v", len(chatParsed.Images), err)
	}

	validWebP := append([]byte("RIFF\x10\x00\x00\x00WEBP"), []byte("VP8 \x04\x00\x00\x00data")...)
	validJPEG := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}
	validGIF := []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;")
	validAVIF := []byte{0x00, 0x00, 0x00, 0x1c, 'f', 't', 'y', 'p', 'a', 'v', 'i', 'f', 0x00, 0x00, 0x00, 0x00}

	for _, tc := range []struct {
		data []byte
		want string
	}{
		{tinyTestPNG, "image/png"},
		{validJPEG, "image/jpeg"},
		{validGIF, "image/gif"},
		{validWebP, "image/webp"},
		{validAVIF, "image/avif"},
	} {
		got, ok := detectVerifiedImageMime(tc.data)
		if !ok || got != tc.want {
			t.Fatalf("detectVerifiedImageMime(%x) = (%q, %v), want (%q, true)", tc.data[:4], got, ok, tc.want)
		}
	}

	for _, nonRaster := range [][]byte{
		[]byte(`{"ok":false,"detail":"json-not-image"}`),
		[]byte("plain text response body"),
		{0x3c, 0x73, 0x76, 0x67, 0x3e},
	} {
		if mime, ok := detectVerifiedImageMime(nonRaster); ok {
			t.Fatalf("expected non-raster payload %x to fail magic-byte check, got %q", nonRaster, mime)
		}
	}

	var leakedCrossOriginAuth string
	baseOrigin := newMemPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		localBase := strings.Join([]string{"http:", "", net.IPv4(127, 0, 0, 1).String() + ":19090"}, "/")
		switch r.URL.Path {
		case "/v1/images/generations":
			var reqBody struct {
				Prompt string `json:"prompt"`
			}
			_ = json.NewDecoder(r.Body).Decode(&reqBody)
			switch reqBody.Prompt {
			case "spoof-json-as-png":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": []map[string]any{{"url": localBase + "/spoof.png"}},
				})
			case "redirect-to-private":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": []map[string]any{{"url": localBase + "/redir-private"}},
				})
			case "redirect-cross-port":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": []map[string]any{{"url": localBase + "/redir-cdn"}},
				})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": []map[string]any{{"url": localBase + "/valid.webp"}},
				})
			}
		case "/spoof.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte(`{"status":"spoofed-json-body"}`))
		case "/redir-private":
			http.Redirect(w, r, buildHTTP(net.IPv4(10, 0, 0, 8).String()), http.StatusFound)
		case "/redir-cdn":
			http.Redirect(w, r, strings.Join([]string{"http:", "", "local" + "host:19091", "cdn.png"}, "/"), http.StatusFound)
		case "/cdn.png":
			leakedCrossOriginAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(tinyTestPNG)
		case "/valid.webp":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write(validWebP)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	localConn := imageGenConn{
		ProviderID: "local-oai",
		ModelID:    "gpt-image-1",
		BaseURL:    baseOrigin + "/v1",
		APIKey:     "test-local-provider-credential",
	}

	if _, err := svc.openAIImagesGenerate(context.Background(), localConn, "spoof-json-as-png", nil, imageGenExecOptions{count: 1}, 5*time.Second); err == nil {
		t.Fatal("expected spoofed JSON payload with Content-Type: image/png to be rejected")
	}
	if _, err := svc.openAIImagesGenerate(context.Background(), localConn, "redirect-to-private", nil, imageGenExecOptions{count: 1}, 5*time.Second); err == nil {
		t.Fatal("expected HTTP redirect to private 10.0.0.8 address to be blocked")
	}

	redirOut, err := svc.openAIImagesGenerate(context.Background(), localConn, "redirect-cross-port", nil, imageGenExecOptions{count: 1}, 5*time.Second)
	if err != nil || len(redirOut.Images) != 1 || redirOut.Images[0].Mime != "image/png" {
		t.Fatalf("expected valid redirected PNG image, got out=%+v err=%v", redirOut, err)
	}
	if leakedCrossOriginAuth != "" {
		t.Fatalf("expected cross-origin redirect to scrub Authorization header, leaked %q", leakedCrossOriginAuth)
	}

	webpOut, err := svc.openAIImagesGenerate(context.Background(), localConn, "valid-webp", nil, imageGenExecOptions{count: 1}, 5*time.Second)
	if err != nil || len(webpOut.Images) != 1 || webpOut.Images[0].Mime != "image/webp" || !bytes.Equal(webpOut.Images[0].Data, validWebP) {
		t.Fatalf("expected verified image/webp output, got out=%+v err=%v", webpOut, err)
	}
}

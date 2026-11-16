package browser

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStoreEnvValidationFailClosedAndRedaction(t *testing.T) {
	svc := New()
	defer svc.Close()
	svc.SetAgentToken("agent-secret")

	dir := t.TempDir()
	secretPass := "Pr0xyP@ss99!"

	// 1. ProxyRequired without ProxyURL must fail closed before launching Chrome.
	svc.SetEnvironmentResolver(func(sessionID, directory string) (StoreEnvConfig, error) {
		return StoreEnvConfig{
			StoreID:       "amz-us-1",
			ProxyRequired: true,
			Timezone:      "America/Los_Angeles",
			Locale:        "en-US",
		}, nil
	})
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	actReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/browser/action?sessionID=ses_1&directory="+url.QueryEscape(dir), strings.NewReader(`{"action":"navigate","url":"https://example.com"}`))
	actReq.Host = "127.0.0.1:5400"
	actReq.Header.Set("Content-Type", "application/json")
	actReq.Header.Set("X-Hoxi-Agent", "true")
	actReq.Header.Set("Authorization", "Bearer agent-secret")
	actRec := httptest.NewRecorder()
	mux.ServeHTTP(actRec, actReq)
	if actRec.Code != http.StatusBadGateway || !strings.Contains(actRec.Body.String(), "requires an isolated proxy") {
		t.Fatalf("expected 502 fail-closed when proxyRequired lacks proxyURL, got %d: %s", actRec.Code, actRec.Body.String())
	}

	// 2. Idle state must surface redacted storeProxy and never leak credentials.
	svc.SetEnvironmentResolver(func(sessionID, directory string) (StoreEnvConfig, error) {
		return StoreEnvConfig{
			StoreID:       "amz-us-1",
			ProxyURL:      "socks5://seller:" + secretPass + "@198.51.100.44:1080",
			RedactedProxy: "socks5://***@198.51.100.44:1080",
			ProxyRequired: true,
			Timezone:      "America/Los_Angeles",
			Locale:        "en-US",
		}, nil
	})
	stReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/browser/state?sessionID=ses_1&directory="+url.QueryEscape(dir), nil)
	stReq.Host = "127.0.0.1:5400"
	stReq.Header.Set("X-Hoxi-Agent", "true")
	stReq.Header.Set("Authorization", "Bearer agent-secret")
	stRec := httptest.NewRecorder()
	mux.ServeHTTP(stRec, stReq)
	if stRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on state, got %d: %s", stRec.Code, stRec.Body.String())
	}
	if strings.Contains(stRec.Body.String(), secretPass) {
		t.Fatalf("CRITICAL: raw proxy password leaked in /api/hoxi/browser/state: %s", stRec.Body.String())
	}
	var st State
	if err := json.Unmarshal(stRec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if st.StoreID != "amz-us-1" || st.StoreProxy != "socks5://***@198.51.100.44:1080" || st.StoreTimezone != "America/Los_Angeles" || st.StoreLocale != "en-US" {
		t.Fatalf("unexpected store metadata in state: %#v", st)
	}

	// 3. Target SSRF guard rejects loopback, RFC1918 private IPs, and .local/.internal hostnames without local DNS leaks.
	for _, blocked := range []string{
		"localhost:8080", "sub.localhost:80", "printer.local:631", "svc.internal:8080",
		"127.0.0.1:5400", "[::1]:8080", "169.254.169.254:80", "10.0.0.5:80", "192.168.1.1:80", "0.0.0.0:80",
	} {
		if err := validateProxyTarget(blocked, false); err == nil {
			t.Fatalf("expected proxy target %q to be denied", blocked)
		}
	}
	for _, allowed := range []string{"sellercentral.amazon.com:443", "seller-us.tiktok.com:443", "[2606:4700:4700::1111]:443"} {
		if err := validateProxyTarget(allowed, false); err != nil {
			t.Fatalf("expected remote target %q to be allowed without local DNS lookup, got %v", allowed, err)
		}
	}

	// 4. Pairing and close responses preserve redacted store metadata even when RedactedProxy is omitted.
	svc.SetBridgeURL("ws://127.0.0.1:5420")
	svc.SetEnvironmentResolver(func(sessionID, directory string) (StoreEnvConfig, error) {
		return StoreEnvConfig{
			StoreID:       "amz-us-1",
			ProxyURL:      "socks5://seller:" + secretPass + "@198.51.100.44:1080",
			ProxyRequired: true,
			Timezone:      "America/Los_Angeles",
			Locale:        "en-US",
		}, nil
	})
	pairReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/browser/action?sessionID=ses_1&directory="+url.QueryEscape(dir), strings.NewReader(`{"action":"pair"}`))
	pairReq.Host = "localhost"
	pairReq.Header.Set("Origin", "wails://wails.localhost")
	pairReq.Header.Set("Content-Type", "application/json")
	pairRec := httptest.NewRecorder()
	mux.ServeHTTP(pairRec, pairReq)
	if pairRec.Code != http.StatusOK {
		t.Fatalf("pair status = %d: %s", pairRec.Code, pairRec.Body.String())
	}
	pairStateReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/browser/state?sessionID=ses_1&directory="+url.QueryEscape(dir), nil)
	pairStateReq.Host = "localhost"
	pairStateReq.Header.Set("Origin", "wails://wails.localhost")
	pairStateRec := httptest.NewRecorder()
	mux.ServeHTTP(pairStateRec, pairStateReq)
	if !strings.Contains(pairStateRec.Body.String(), `"storeProxy":"socks5://***@198.51.100.44:1080"`) || strings.Contains(pairStateRec.Body.String(), secretPass) {
		t.Fatalf("expected redacted storeProxy during pairing state, got %s", pairStateRec.Body.String())
	}
}

func TestLoopbackProxyBridgeSOCKS5AndHTTPConnect(t *testing.T) {
	// Target echo server pretending to be remote seller platform.
	destLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen dest: %v", err)
	}
	defer destLn.Close()
	go func() {
		for {
			c, err := destLn.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				line, _ := bufio.NewReader(conn).ReadString('\n')
				_, _ = io.WriteString(conn, "ECHO:"+line)
			}(c)
		}
	}()

	// Mock SOCKS5 server requiring seller:s3cr3t and forwarding ATYP=0x03 domain to destLn.
	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen socks5: %v", err)
	}
	defer socksLn.Close()
	var seenDomainMu sync.Mutex
	var seenDomain string
	go func() {
		for {
			c, err := socksLn.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				hdr := make([]byte, 2)
				if _, err := io.ReadFull(conn, hdr); err != nil || hdr[0] != 0x05 {
					return
				}
				methods := make([]byte, int(hdr[1]))
				_, _ = io.ReadFull(conn, methods)
				_, _ = conn.Write([]byte{0x05, 0x02})
				ah := make([]byte, 2)
				if _, err := io.ReadFull(conn, ah); err != nil {
					return
				}
				u := make([]byte, int(ah[1]))
				_, _ = io.ReadFull(conn, u)
				pl := make([]byte, 1)
				_, _ = io.ReadFull(conn, pl)
				p := make([]byte, int(pl[0]))
				_, _ = io.ReadFull(conn, p)
				if string(u) != "seller" || string(p) != "s3cr3t" {
					_, _ = conn.Write([]byte{0x01, 0x01})
					return
				}
				_, _ = conn.Write([]byte{0x01, 0x00})
				// Optional CONNECT request (absent during preflight probe).
				_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
				reqHdr := make([]byte, 4)
				if _, err := io.ReadFull(conn, reqHdr); err != nil {
					return
				}
				_ = conn.SetReadDeadline(time.Time{})
				if reqHdr[3] == 0x03 {
					dl := make([]byte, 1)
					_, _ = io.ReadFull(conn, dl)
					dom := make([]byte, int(dl[0]))
					_, _ = io.ReadFull(conn, dom)
					seenDomainMu.Lock()
					seenDomain = string(dom)
					seenDomainMu.Unlock()
					var portBytes [2]byte
					_, _ = io.ReadFull(conn, portBytes[:])
				}
				up, err := net.Dial("tcp", destLn.Addr().String())
				if err != nil {
					return
				}
				defer up.Close()
				_, _ = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0, 0})
				go func() { _, _ = io.Copy(up, conn); _ = up.Close() }()
				_, _ = io.Copy(conn, up)
			}(c)
		}
	}()

	// Wrong password fails closed at preflight without leaking password.
	badUpstream, _ := parseAndValidateUpstreamProxy("socks5://seller:badpass@" + socksLn.Addr().String())
	if _, err := startLoopbackProxyBridge(context.Background(), badUpstream, false); err == nil {
		t.Fatalf("expected startLoopbackProxyBridge to fail on invalid SOCKS5 credentials")
	} else if strings.Contains(err.Error(), "badpass") {
		t.Fatalf("password leaked in bridge error: %v", err)
	}

	// Valid SOCKS5 bridge tunnels CONNECT sellercentral.amazon.com:443 with remote domain ATYP=0x03.
	goodUpstream, err := parseAndValidateUpstreamProxy("socks5://seller:s3cr3t@" + socksLn.Addr().String())
	if err != nil {
		t.Fatalf("parse good upstream: %v", err)
	}
	bridge, err := startLoopbackProxyBridge(context.Background(), goodUpstream, false)
	if err != nil {
		t.Fatalf("startLoopbackProxyBridge failed: %v", err)
	}
	defer bridge.Close()

	clientConn, err := net.DialTimeout("tcp", bridge.Addr(), 3*time.Second)
	if err != nil {
		t.Fatalf("dial bridge: %v", err)
	}
	defer clientConn.Close()
	_, _ = io.WriteString(clientConn, "CONNECT sellercentral.amazon.com:443 HTTP/1.1\r\nHost: sellercentral.amazon.com:443\r\n\r\n")
	br := bufio.NewReader(clientConn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 Connection Established, got %v, err=%v", resp, err)
	}
	_, _ = io.WriteString(clientConn, "hello-amazon\n")
	echoLine, err := br.ReadString('\n')
	if err != nil || echoLine != "ECHO:hello-amazon\n" {
		t.Fatalf("unexpected tunnel response %q, err=%v", echoLine, err)
	}
	seenDomainMu.Lock()
	gotDomain := seenDomain
	seenDomainMu.Unlock()
	if gotDomain != "sellercentral.amazon.com" {
		t.Fatalf("expected remote SOCKS5 ATYP=0x03 domain sellercentral.amazon.com, got %q", gotDomain)
	}

	// Also verify HTTP CONNECT upstream with Proxy-Authorization Basic auth.
	httpProxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen http proxy: %v", err)
	}
	defer httpProxyLn.Close()
	expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("ttuser:ttpass"))
	go func() {
		for {
			c, err := httpProxyLn.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				r := bufio.NewReader(conn)
				req, err := http.ReadRequest(r)
				if err != nil {
					return
				}
				if req.Header.Get("Proxy-Authorization") != expectedAuth {
					_, _ = io.WriteString(conn, "HTTP/1.1 407 Proxy Authentication Required\r\n\r\n")
					return
				}
				up, err := net.Dial("tcp", destLn.Addr().String())
				if err != nil {
					return
				}
				defer up.Close()
				_, _ = io.WriteString(conn, fmt.Sprintf("HTTP/1.1 200 OK\r\n\r\n"))
				go func() { _, _ = io.Copy(up, r); _ = up.Close() }()
				_, _ = io.Copy(conn, up)
			}(c)
		}
	}()

	httpUpstream, _ := parseAndValidateUpstreamProxy("http://ttuser:ttpass@" + httpProxyLn.Addr().String())
	httpBridge, err := startLoopbackProxyBridge(context.Background(), httpUpstream, false)
	if err != nil {
		t.Fatalf("start http bridge: %v", err)
	}
	defer httpBridge.Close()

	hc, err := net.DialTimeout("tcp", httpBridge.Addr(), 3*time.Second)
	if err != nil {
		t.Fatalf("dial http bridge: %v", err)
	}
	defer hc.Close()
	_, _ = io.WriteString(hc, "CONNECT seller-us.tiktok.com:443 HTTP/1.1\r\nHost: seller-us.tiktok.com:443\r\n\r\n")
	hbr := bufio.NewReader(hc)
	hresp, err := http.ReadResponse(hbr, &http.Request{Method: http.MethodConnect})
	if err != nil || hresp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 via HTTP upstream proxy, got %v, err=%v", hresp, err)
	}
	_, _ = io.WriteString(hc, "hello-tiktok\n")
	hecho, err := hbr.ReadString('\n')
	if err != nil || hecho != "ECHO:hello-tiktok\n" {
		t.Fatalf("unexpected HTTP proxy tunnel echo %q, err=%v", hecho, err)
	}
}

func TestLoopbackProxyBridgeCloseReleasesTrackedConns(t *testing.T) {
	b := &loopbackProxyBridge{
		conns: make(map[net.Conn]struct{}),
	}
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	if !b.trackConn(c1) {
		t.Fatal("expected trackConn to accept active tunnel conn")
	}
	done := make(chan error, 1)
	go func() {
		var buf [8]byte
		_, err := c2.Read(buf[:])
		done <- err
	}()
	b.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected peer read to unblock on bridge Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bridge Close did not close tracked hijacked connection")
	}
	p1, p2 := net.Pipe()
	defer p1.Close()
	defer p2.Close()
	if b.trackConn(p1) {
		t.Fatal("expected trackConn after Close to reject and close new conn")
	}
}

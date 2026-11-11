package workapps

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"time"
)

const defaultProxyProbeTimeout = 5 * time.Second

// ProxyProbeResult summarizes a store proxy reachability & authentication check.
type ProxyProbeResult struct {
	OK            bool   `json:"ok"`
	Scheme        string `json:"scheme,omitempty"`
	Host          string `json:"host,omitempty"`
	RedactedProxy string `json:"redactedProxy,omitempty"`
	LatencyMs     int64  `json:"latencyMs"`
	Error         string `json:"error,omitempty"`
}

// ProbeProxy validates rawProxyURL and verifies TCP/TLS reachability plus
// SOCKS5 greeting/authentication handshake when scheme is socks5/socks5h.
// Credentials are never included in returned fields or error strings.
func ProbeProxy(ctx context.Context, rawProxyURL string) (ProxyProbeResult, error) {
	u, err := ValidateProxyURL(rawProxyURL)
	if err != nil {
		return ProxyProbeResult{OK: false, Error: err.Error()}, err
	}
	redacted := RedactProxyURL(rawProxyURL)
	probeCtx, cancel := context.WithTimeout(ctx, defaultProxyProbeTimeout)
	defer cancel()

	start := time.Now()
	dialer := &net.Dialer{Timeout: defaultProxyProbeTimeout}
	conn, err := dialer.DialContext(probeCtx, "tcp", u.Host)
	if err != nil {
		msg := fmt.Sprintf("cannot reach proxy %s: connection failed", redacted)
		return ProxyProbeResult{
			OK:            false,
			Scheme:        u.Scheme,
			Host:          u.Host,
			RedactedProxy: redacted,
			LatencyMs:     time.Since(start).Milliseconds(),
			Error:         msg,
		}, errors.New(msg)
	}
	defer conn.Close()

	if deadline, ok := probeCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	switch u.Scheme {
	case "https":
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName: u.Hostname(),
			MinVersion: tls.VersionTLS12,
		})
		if err := tlsConn.HandshakeContext(probeCtx); err != nil {
			msg := fmt.Sprintf("TLS handshake failed with proxy %s", redacted)
			return ProxyProbeResult{
				OK:            false,
				Scheme:        u.Scheme,
				Host:          u.Host,
				RedactedProxy: redacted,
				LatencyMs:     time.Since(start).Milliseconds(),
				Error:         msg,
			}, errors.New(msg)
		}
	case "socks5", "socks5h":
		if err := probeSOCKS5Auth(conn, u.User); err != nil {
			msg := fmt.Sprintf("SOCKS5 handshake failed with proxy %s: %v", redacted, err)
			return ProxyProbeResult{
				OK:            false,
				Scheme:        u.Scheme,
				Host:          u.Host,
				RedactedProxy: redacted,
				LatencyMs:     time.Since(start).Milliseconds(),
				Error:         msg,
			}, errors.New(msg)
		}
	}

	return ProxyProbeResult{
		OK:            true,
		Scheme:        u.Scheme,
		Host:          u.Host,
		RedactedProxy: redacted,
		LatencyMs:     time.Since(start).Milliseconds(),
	}, nil
}

func probeSOCKS5Auth(conn net.Conn, user *url.Userinfo) error {
	username := ""
	password := ""
	hasAuth := false
	if user != nil {
		username = user.Username()
		password, _ = user.Password()
		hasAuth = username != "" || password != ""
	}
	if hasAuth {
		if len(username) > 255 || len(password) > 255 {
			return errors.New("SOCKS5 credentials exceed 255 bytes")
		}
		if _, err := conn.Write([]byte{0x05, 0x02, 0x00, 0x02}); err != nil {
			return errors.New("write greeting failed")
		}
	} else {
		if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
			return errors.New("write greeting failed")
		}
	}
	var resp [2]byte
	if _, err := io.ReadFull(conn, resp[:]); err != nil {
		return errors.New("read greeting reply failed")
	}
	if resp[0] != 0x05 {
		return errors.New("invalid SOCKS5 version")
	}
	switch resp[1] {
	case 0x00:
		return nil
	case 0x02:
		if !hasAuth {
			return errors.New("proxy requires username/password authentication")
		}
		buf := make([]byte, 0, 3+len(username)+len(password))
		buf = append(buf, 0x01, byte(len(username)))
		buf = append(buf, username...)
		buf = append(buf, byte(len(password)))
		buf = append(buf, password...)
		if _, err := conn.Write(buf); err != nil {
			return errors.New("write auth request failed")
		}
		var authResp [2]byte
		if _, err := io.ReadFull(conn, authResp[:]); err != nil {
			return errors.New("read auth reply failed")
		}
		if authResp[1] != 0x00 {
			return errors.New("proxy rejected username/password credentials")
		}
		return nil
	case 0xFF:
		return errors.New("no acceptable authentication method")
	default:
		return fmt.Errorf("unsupported SOCKS5 auth method 0x%02x", resp[1])
	}
}

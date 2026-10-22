// Package pairing issues the local-network pairing QR the Hoxi mobile app
// scans to reach this desktop directly.
//
// The desktop gateway is loopback-only today, so the QR records the LAN
// address, port and token that the LAN listener will accept once it ships
// (see DefaultLANPairingPort). The token lives in ~/.hoxi/pairing.json (0600)
// and never leaves the machine except inside the QR the user chooses to show.
package pairing

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Payload is the scannable pairing record the mobile app reads. It mirrors the
// JSON shape the phone's QR scanner already understands:
// {"url","token","type","entrance"}.
type Payload struct {
	URL       string `json:"url"`
	Token     string `json:"token"`
	Type      string `json:"type"`
	Entrance  string `json:"entrance"`
	Relay     string `json:"relay,omitempty"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Hostname  string `json:"hostname"`
	Available bool   `json:"available"`
}

// Code is a Payload plus the rendered QR image.
type Code struct {
	Payload
	// QRPng is a data: URL holding the PNG QR code, or empty when no LAN
	// address was found.
	QRPng string `json:"qrPng"`
}

// Service builds pairing codes for this desktop.
type Service struct {
	tokenPath string
	port      int
	hostname  func() string
	lanHost   func() string
	relayURL  func() string

	mu     sync.Mutex
	cached string
}

// NewService returns a Service that stores its token at tokenPath and
// advertises port. An empty tokenPath keeps the token in memory only.
func NewService(tokenPath string, port int) *Service {
	return &Service{
		tokenPath: tokenPath,
		port:      port,
		hostname:  defaultHostname,
		lanHost:   primaryLANIPv4,
	}
}

// SetRelayURL registers a callback returning this desktop's off-LAN relay URL
// (for example a Cloudflare Tunnel public hostname), or "" when none is
// configured. It is embedded in the QR so the phone can fall back to it.
func (s *Service) SetRelayURL(fn func() string) {
	s.mu.Lock()
	s.relayURL = fn
	s.mu.Unlock()
}

// Code assembles the current pairing record. When the machine has no usable
// LAN address, Available is false and no QR image is produced; the caller
// should tell the user to join a network instead of showing a dead code.
func (s *Service) Code() (Code, error) {
	token, err := s.token()
	if err != nil {
		return Code{}, err
	}
	code := Code{Payload: Payload{
		Type:     "hoxi",
		Token:    token,
		Port:     s.port,
		Hostname: s.hostname(),
	}}
	if relay := s.relay(); relay != "" {
		code.Relay = relay
	}
	host := strings.TrimSpace(s.lanHost())
	if host == "" {
		return code, nil
	}
	code.Host = host
	code.URL = fmt.Sprintf("http://%s:%d", host, s.port)
	code.Available = true

	scannable := map[string]string{
		"url":      code.URL,
		"token":    code.Token,
		"type":     code.Type,
		"entrance": code.Entrance,
	}
	if code.Relay != "" {
		scannable["relay"] = code.Relay
	}
	encoded, err := json.Marshal(scannable)
	if err != nil {
		return Code{}, err
	}
	png, err := encodeQR(string(encoded))
	if err != nil {
		return Code{}, err
	}
	code.QRPng = "data:image/png;base64," + png
	return code, nil
}

func (s *Service) relay() string {
	s.mu.Lock()
	fn := s.relayURL
	s.mu.Unlock()
	if fn == nil {
		return ""
	}
	return strings.TrimSpace(fn())
}

// Token returns this desktop's stable pairing token, creating and persisting it
// on first use.
func (s *Service) Token() (string, error) {
	return s.token()
}

// Rotate replaces the pairing token, invalidating any previously scanned QR and
// every saved LAN connection, and returns the freshly generated code.
func (s *Service) Rotate() (Code, error) {
	if err := s.rotateToken(); err != nil {
		return Code{}, err
	}
	return s.Code()
}

func (s *Service) rotateToken() error {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	token := hex.EncodeToString(buf)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeToken(s.tokenPath, token); err != nil {
		return err
	}
	s.cached = token
	return nil
}

// token returns the persisted pairing token, creating and storing it on first
// use. A successful value is cached in memory so repeated calls stay stable.
func (s *Service) token() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != "" {
		return s.cached, nil
	}
	if token := readToken(s.tokenPath); token != "" {
		s.cached = token
		return token, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	if err := writeToken(s.tokenPath, token); err != nil {
		return "", err
	}
	s.cached = token
	return token, nil
}

func readToken(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var rec struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(data, &rec) != nil {
		return ""
	}
	return strings.TrimSpace(rec.Token)
}

func writeToken(path, token string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(struct {
		Token string `json:"token"`
	}{Token: token}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pairing-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func defaultHostname() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(name)
}

// primaryLANIPv4 returns the machine's most likely LAN address: a private
// IPv4 on an up, non-loopback interface, falling back to any global unicast
// IPv4. It returns "" when only loopback or link-local addresses exist.
func primaryLANIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	fallback := ""
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			v4 := ip.To4()
			if v4 == nil || v4.IsLoopback() || v4.IsLinkLocalUnicast() {
				continue
			}
			if v4.IsPrivate() {
				return v4.String()
			}
			if fallback == "" {
				fallback = v4.String()
			}
		}
	}
	return fallback
}

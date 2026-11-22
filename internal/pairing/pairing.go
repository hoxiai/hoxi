// Package pairing issues the local-network pairing QR the Hoxi mobile app
// scans to reach this desktop directly.
//
// The QR records the LAN address, port (DefaultLANPairingPort, served by
// internal/gateway/lan.go), optional relay URL, pairing tokens, and an
// end-to-end encryption master key (e2eeKey). Credentials live in
// ~/.hoxi/pairing.json (0600) and e2eeKey never transits third-party relays.
package pairing

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// Payload is the scannable pairing record the mobile app reads. It mirrors the
// JSON shape the phone's QR scanner already understands:
// {"url","token","type","entrance"}.
type Payload struct {
	URL        string   `json:"url"`
	Peers      []string `json:"peers,omitempty"`
	Token      string   `json:"token"`
	RelayToken string   `json:"relayToken,omitempty"`
	E2EEKey    string   `json:"e2eeKey,omitempty"`
	Type       string   `json:"type"`
	Entrance   string   `json:"entrance"`
	Relay      string   `json:"relay,omitempty"`
	Host       string   `json:"host"`
	Port       int      `json:"port"`
	Hostname   string   `json:"hostname"`
	Available  bool     `json:"available"`
}

// Code is a Payload plus the rendered QR image.
type Code struct {
	Payload
	// QRPng is a data: URL holding the PNG QR code, or empty when no LAN
	// address was found.
	QRPng string `json:"qrPng"`
	// ShortCodeRelayURL is the build/env default 6-digit pairing relay origin
	// (empty in -tags oss builds unless overridden via HOXI_PAIRING_RELAY_URL).
	// It is returned to the desktop UI only and never embedded in QRPng.
	ShortCodeRelayURL string `json:"shortCodeRelayUrl,omitempty"`
}

// Service builds pairing codes for this desktop.
type Service struct {
	tokenPath string
	port      int
	hostname  func() string
	lanHost   func() string
	lanPeers  func() []string
	relayURL  func() string

	mu                 sync.Mutex
	cached             string
	cachedRelay        string
	cachedE2EE         string
	cachedCipher       *E2EECipher
	activeShortCode    *PublishedShortCode
	shortCodeTransport http.RoundTripper
}

// NewService returns a Service that stores its token at tokenPath and
// advertises port. An empty tokenPath keeps the token in memory only.
func NewService(tokenPath string, port int) *Service {
	return &Service{
		tokenPath: tokenPath,
		port:      port,
		hostname:  defaultHostname,
		lanHost:   primaryLANIPv4,
		lanPeers:  allDirectHosts,
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

func formatDirectEndpoint(host string, port int) string {
	clean := strings.Trim(strings.TrimSpace(host), "[]")
	if clean == "" {
		return ""
	}
	return "http://" + net.JoinHostPort(clean, strconv.Itoa(port))
}

// Code assembles the current pairing record. When the machine has no usable
// LAN address, Available is false and no QR image is produced; the caller
// should tell the user to join a network instead of showing a dead code.
func (s *Service) Code() (Code, error) {
	lanToken, relayToken, e2eeKey, err := s.credentials()
	if err != nil {
		return Code{}, err
	}
	code := Code{
		Payload: Payload{
			Type:     "hoxi",
			Token:    lanToken,
			E2EEKey:  e2eeKey,
			Port:     s.port,
			Hostname: s.hostname(),
		},
		ShortCodeRelayURL: DefaultShortCodeRelayURL(),
	}
	if relay := s.relay(); relay != "" {
		code.Relay = relay
		code.RelayToken = relayToken
	}
	host := strings.TrimSpace(s.lanHost())
	if host == "" && code.Relay == "" {
		code.E2EEKey = ""
		return code, nil
	}
	if host != "" {
		code.Host = strings.Trim(host, "[]")
		code.URL = formatDirectEndpoint(host, s.port)
	} else {
		code.URL = code.Relay
	}
	code.Available = true

	if host != "" && s.lanPeers != nil {
		candidates := s.lanPeers()
		if containsHost(candidates, host) {
			seen := map[string]bool{strings.Trim(host, "[]"): true}
			for _, peer := range candidates {
				peer = strings.Trim(strings.TrimSpace(peer), "[]")
				if peer == "" || seen[peer] {
					continue
				}
				seen[peer] = true
				code.Peers = append(code.Peers, formatDirectEndpoint(peer, s.port))
			}
		}
	}

	scannable := map[string]any{
		"url":      code.URL,
		"token":    code.Token,
		"e2eeKey":  code.E2EEKey,
		"type":     code.Type,
		"entrance": code.Entrance,
	}
	if len(code.Peers) > 0 {
		scannable["peers"] = code.Peers
	}
	if code.Relay != "" {
		scannable["relay"] = code.Relay
		scannable["relayToken"] = code.RelayToken
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

// RelayURL returns this desktop's configured off-LAN relay URL, or "".
func (s *Service) RelayURL() string {
	return s.relay()
}

// LanEndpoints returns the primary LAN URL and any secondary peer LAN/Tailscale URLs.
func (s *Service) LanEndpoints() (string, []string) {
	host := strings.Trim(strings.TrimSpace(s.lanHost()), "[]")
	if host == "" {
		return "", nil
	}
	primary := formatDirectEndpoint(host, s.port)
	var peers []string
	if s.lanPeers != nil {
		candidates := s.lanPeers()
		if containsHost(candidates, host) {
			seen := map[string]bool{host: true}
			for _, peer := range candidates {
				peer = strings.Trim(strings.TrimSpace(peer), "[]")
				if peer == "" || seen[peer] {
					continue
				}
				seen[peer] = true
				peers = append(peers, formatDirectEndpoint(peer, s.port))
			}
		}
	}
	return primary, peers
}

// DirectEndpoints returns all currently reachable direct endpoints (primary LAN
// interface first, followed by secondary LAN and Tailscale/CGNAT addresses) so
// paired mobile clients can dynamically refresh changed DHCP or VPN addresses.
func (s *Service) DirectEndpoints() []string {
	primary, peers := s.LanEndpoints()
	if primary == "" {
		return nil
	}
	out := make([]string, 0, 1+len(peers))
	out = append(out, primary)
	out = append(out, peers...)
	return out
}

// Token returns this desktop's stable LAN pairing token, creating and
// persisting it on first use.
func (s *Service) Token() (string, error) {
	lanToken, _, _, err := s.credentials()
	return lanToken, err
}

// RelayToken returns this desktop's reverse-proxy / tunnel relay token, kept
// separate from the cleartext LAN pairing token so a LAN leak cannot authorize
// requests through the public relay ingress.
func (s *Service) RelayToken() (string, error) {
	_, relayToken, _, err := s.credentials()
	return relayToken, err
}

// E2EEKey returns this desktop's 256-bit hex end-to-end encryption master key.
func (s *Service) E2EEKey() (string, error) {
	_, _, e2eeKey, err := s.credentials()
	return e2eeKey, err
}

// Cipher returns the stateful E2EECipher bound to the current pairing E2EE key,
// preserving replay-detection state across requests until credentials rotate.
func (s *Service) Cipher() (*E2EECipher, error) {
	_, _, e2eeKey, err := s.credentials()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cachedCipher != nil && s.cachedCipher.keyHex == strings.ToLower(e2eeKey) {
		return s.cachedCipher, nil
	}
	c, err := NewE2EECipher(e2eeKey)
	if err != nil {
		return nil, err
	}
	s.cachedCipher = c
	return c, nil
}

// Rotate replaces the LAN token, relay token, and E2EE key together,
// invalidating any previously scanned QR and every saved connection, and
// returns the freshly generated code.
func (s *Service) Rotate() (Code, error) {
	if err := s.rotateToken(); err != nil {
		return Code{}, err
	}
	return s.Code()
}

func randomHexToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func isValidHexKey32(raw string) bool {
	decoded, err := hex.DecodeString(strings.TrimSpace(raw))
	return err == nil && len(decoded) == 32
}

func (s *Service) rotateToken() error {
	lanToken, err := randomHexToken()
	if err != nil {
		return err
	}
	relayToken, err := randomHexToken()
	if err != nil {
		return err
	}
	e2eeKey, err := randomHexToken()
	if err != nil {
		return err
	}
	cipherSuite, err := NewE2EECipher(e2eeKey)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeTokens(s.tokenPath, lanToken, relayToken, e2eeKey); err != nil {
		return err
	}
	s.cached = lanToken
	s.cachedRelay = relayToken
	s.cachedE2EE = e2eeKey
	s.cachedCipher = cipherSuite
	return nil
}

// credentials returns the persisted LAN token, relay token, and E2EE master key,
// creating and storing any missing value on first use while preserving existing ones.
func (s *Service) credentials() (string, string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != "" && s.cachedRelay != "" && s.cachedE2EE != "" {
		return s.cached, s.cachedRelay, s.cachedE2EE, nil
	}
	lanToken, relayToken, e2eeKey := readTokens(s.tokenPath)
	changed := false
	if lanToken == "" {
		var err error
		lanToken, err = randomHexToken()
		if err != nil {
			return "", "", "", err
		}
		changed = true
	}
	if relayToken == "" || relayToken == lanToken {
		var err error
		relayToken, err = randomHexToken()
		if err != nil {
			return "", "", "", err
		}
		changed = true
	}
	if !isValidHexKey32(e2eeKey) || e2eeKey == lanToken || e2eeKey == relayToken {
		var err error
		e2eeKey, err = randomHexToken()
		if err != nil {
			return "", "", "", err
		}
		changed = true
	}
	if changed {
		if err := writeTokens(s.tokenPath, lanToken, relayToken, e2eeKey); err != nil {
			return "", "", "", err
		}
	}
	s.cached = lanToken
	s.cachedRelay = relayToken
	s.cachedE2EE = e2eeKey
	return lanToken, relayToken, e2eeKey, nil
}

type tokenRecord struct {
	Token      string `json:"token"`
	RelayToken string `json:"relayToken,omitempty"`
	E2EEKey    string `json:"e2eeKey,omitempty"`
}

func readTokens(path string) (string, string, string) {
	if path == "" {
		return "", "", ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", ""
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return "", "", ""
	}
	var rec tokenRecord
	if json.Unmarshal(data, &rec) != nil {
		backupCorruptTokenFile(path)
		return "", "", ""
	}
	tok := strings.TrimSpace(rec.Token)
	if tok == "" {
		backupCorruptTokenFile(path)
		return "", "", ""
	}
	return tok, strings.TrimSpace(rec.RelayToken), strings.TrimSpace(rec.E2EEKey)
}

func backupCorruptTokenFile(path string) {
	if path == "" {
		return
	}
	backup := fmt.Sprintf("%s.corrupt-%d", path, time.Now().UTC().UnixNano())
	_ = os.Rename(path, backup)
}

func writeTokens(path, lanToken, relayToken, e2eeKey string) error {
	if path == "" {
		return nil
	}
	if data, err := os.ReadFile(path); err == nil && len(bytes.TrimSpace(data)) > 0 {
		var rec tokenRecord
		if json.Unmarshal(data, &rec) != nil || strings.TrimSpace(rec.Token) == "" {
			backupCorruptTokenFile(path)
		}
	}
	return hoxipaths.WriteJSONAtomic(path, tokenRecord{
		Token:      lanToken,
		RelayToken: relayToken,
		E2EEKey:    e2eeKey,
	}, 0o600)
}

func defaultHostname() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(name)
}

func containsHost(hosts []string, want string) bool {
	wantClean := strings.Trim(strings.TrimSpace(want), "[]")
	for _, h := range hosts {
		if strings.Trim(strings.TrimSpace(h), "[]") == wantClean {
			return true
		}
	}
	return false
}

func isCGNATIPv4(v4 net.IP) bool {
	return len(v4) == net.IPv4len && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}

// isTailscaleULAIPv6 reports whether ip lies in Tailscale's fd7a:115c:a1e0::/48 ULA prefix.
func isTailscaleULAIPv6(ip net.IP) bool {
	if len(ip) != net.IPv6len || ip.To4() != nil {
		return false
	}
	return ip[0] == 0xfd && ip[1] == 0x7a && ip[2] == 0x11 && ip[3] == 0x5c && ip[4] == 0xa1 && ip[5] == 0xe0
}

func lanIPv4Rank(v4 net.IP) int {
	if len(v4) != net.IPv4len || v4.IsLoopback() {
		return 0
	}
	if v4.IsPrivate() {
		switch v4[0] {
		case 192:
			// 192.168.64.0/24 and 192.168.65.0/24 are commonly macOS VM / Docker bridges.
			if v4[2] == 64 || v4[2] == 65 {
				return 35
			}
			return 50
		case 10:
			return 40
		case 172:
			return 30
		default:
			return 35
		}
	}
	if isCGNATIPv4(v4) {
		return 20
	}
	if v4.IsLinkLocalUnicast() {
		return 10
	}
	return 0
}

func isVirtualInterfaceName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, prefix := range []string{
		"utun", "tun", "tap", "ppp", "gif", "stf",
		"awdl", "llw", "anpi", "bridge", "veth",
		"docker", "br-", "vmnet", "vboxnet", "virbr",
		"wg", "tailscale", "zt", "cni", "flannel", "cali", "lo",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func interfacePriorityBonus(name string) int {
	if isVirtualInterfaceName(name) {
		return -25
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "en0" || lower == "wlan0" || lower == "eth0" {
		return 15
	}
	for _, prefix := range []string{"en", "eth", "wlan", "wl", "wi-fi", "ethernet"} {
		if strings.HasPrefix(lower, prefix) {
			return 10
		}
	}
	return 0
}

func selectLANIPv4(ips []net.IP) string {
	bestIP := ""
	bestRank := 0
	for _, ip := range ips {
		v4 := ip.To4()
		rank := lanIPv4Rank(v4)
		if rank > bestRank {
			bestRank = rank
			bestIP = v4.String()
		}
	}
	return bestIP
}

type scoredLANIP struct {
	ip    string
	score int
	order int
}

// allLANIPv4s returns all non-loopback, non-public IPv4 addresses on up
// interfaces, ordered with physical Wi-Fi/Ethernet RFC1918 addresses first,
// followed by Tailscale/CGNAT overlay addresses (100.64.0.0/10), ahead of
// local-only Docker/VM bridge and link-local addresses.
func allLANIPv4s() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var scored []scoredLANIP
	order := 0
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		bonus := interfacePriorityBonus(iface.Name)
		for _, addr := range addrs {
			var rawIP net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				rawIP = v.IP
			case *net.IPAddr:
				rawIP = v.IP
			}
			v4 := rawIP.To4()
			rank := lanIPv4Rank(v4)
			if rank == 0 {
				continue
			}
			effectiveBonus := bonus
			// Overlay VPNs (Tailscale/Headscale/ZeroTier) bind 100.64.0.0/10 on
			// utun/tailscale interfaces; do not penalize them below local VM/Docker bridges.
			if isCGNATIPv4(v4) && effectiveBonus < 0 {
				effectiveBonus = 0
			}
			scored = append(scored, scoredLANIP{
				ip:    v4.String(),
				score: rank + effectiveBonus,
				order: order,
			})
			order++
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].order < scored[j].order
	})
	seen := make(map[string]bool, len(scored))
	out := make([]string, 0, len(scored))
	for _, item := range scored {
		if !seen[item.ip] {
			seen[item.ip] = true
			out = append(out, item.ip)
		}
	}
	return out
}

// allDirectHosts returns ordered direct IPv4 addresses plus any Tailscale ULA
// IPv6 addresses (fd7a:115c:a1e0::/48) accepted by the LAN gateway listener.
func allDirectHosts() []string {
	out := allLANIPv4s()
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	seen := make(map[string]bool, len(out))
	for _, h := range out {
		seen[h] = true
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var rawIP net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				rawIP = v.IP
			case *net.IPAddr:
				rawIP = v.IP
			}
			if isTailscaleULAIPv6(rawIP) {
				s := rawIP.String()
				if !seen[s] {
					seen[s] = true
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// primaryLANIPv4 returns the machine's most likely LAN address: a private
// RFC1918 IPv4 on an up, non-loopback physical interface, falling back to
// virtual/CGNAT/Tailscale (100.64.0.0/10) or link-local (169.254.0.0/16).
// Public IPv4 addresses are never returned because the LAN listener refuses
// public clients.
func primaryLANIPv4() string {
	all := allLANIPv4s()
	if len(all) == 0 {
		return ""
	}
	return all[0]
}

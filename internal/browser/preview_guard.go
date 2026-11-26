package browser

import (
	"context"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

const (
	previewFetchActionTimeout  = 5 * time.Second
	previewDialogHandleTimeout = 3 * time.Second
	previewSubresourceHostTTL  = 30 * time.Second
)

var (
	nonPublicSubresourcePrefixes = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),       // "This" network (RFC 1122)
		netip.MustParsePrefix("100.64.0.0/10"),   // Carrier-Grade NAT / Tailscale (RFC 6598)
		netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
		netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
		netip.MustParsePrefix("198.18.0.0/15"),   // Benchmarking
		netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
		netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
		netip.MustParsePrefix("240.0.0.0/4"),     // Reserved / broadcast
		netip.MustParsePrefix("2001:db8::/32"),   // IPv6 documentation
	}

	nonPublicSubresourceHostSuffixes = []string{
		".local",
		".localhost",
		".internal",
		".lan",
		".home.arpa",
		".corp",
		".intranet",
		".arpa",
	}
)

// parseNonCanonicalIPv4 recognizes legacy inet_aton numeric IPv4 forms that
// browsers/OS resolvers accept (e.g. 127.1, 0177.0.0.1, 0x7f000001, 2130706433).
func parseNonCanonicalIPv4(host string) (netip.Addr, bool) {
	if host == "" {
		return netip.Addr{}, false
	}
	parts := strings.Split(host, ".")
	if len(parts) < 1 || len(parts) > 4 {
		return netip.Addr{}, false
	}
	vals := make([]uint64, len(parts))
	for i, part := range parts {
		if part == "" {
			return netip.Addr{}, false
		}
		v, err := strconv.ParseUint(part, 0, 64)
		if err != nil {
			return netip.Addr{}, false
		}
		vals[i] = v
	}
	var ip32 uint32
	switch len(vals) {
	case 1:
		if vals[0] > 0xffffffff {
			return netip.Addr{}, false
		}
		ip32 = uint32(vals[0])
	case 2:
		if vals[0] > 0xff || vals[1] > 0xffffff {
			return netip.Addr{}, false
		}
		ip32 = uint32(vals[0]<<24) | uint32(vals[1])
	case 3:
		if vals[0] > 0xff || vals[1] > 0xff || vals[2] > 0xffff {
			return netip.Addr{}, false
		}
		ip32 = uint32(vals[0]<<24) | uint32(vals[1]<<16) | uint32(vals[2])
	case 4:
		if vals[0] > 0xff || vals[1] > 0xff || vals[2] > 0xff || vals[3] > 0xff {
			return netip.Addr{}, false
		}
		ip32 = uint32(vals[0]<<24) | uint32(vals[1]<<16) | uint32(vals[2]<<8) | uint32(vals[3])
	}
	return netip.AddrFrom4([4]byte{byte(ip32 >> 24), byte(ip32 >> 16), byte(ip32 >> 8), byte(ip32)}), true
}

// extractWildcardDNSIP extracts an embedded IPv4 address from wildcard DNS
// hostnames such as 127.0.0.1.nip.io, app-127-0-0-1.sslip.io, or 7f000001.nip.io.
func extractWildcardDNSIP(host string) (netip.Addr, bool) {
	var prefix string
	for _, suffix := range []string{".nip.io", ".sslip.io", ".xip.io"} {
		if strings.HasSuffix(host, suffix) {
			prefix = strings.TrimSuffix(host, suffix)
			break
		}
	}
	if prefix == "" {
		return netip.Addr{}, false
	}
	labels := strings.Split(prefix, ".")
	if len(labels) >= 4 {
		cand := strings.Join(labels[len(labels)-4:], ".")
		if ip, err := netip.ParseAddr(cand); err == nil {
			return ip.Unmap(), true
		}
	}
	last := labels[len(labels)-1]
	if len(last) >= 8 {
		hexPart := last[len(last)-8:]
		if v, err := strconv.ParseUint(hexPart, 16, 32); err == nil {
			return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}), true
		}
	}
	dashParts := strings.Split(last, "-")
	if len(dashParts) >= 4 {
		cand := strings.Join(dashParts[len(dashParts)-4:], ".")
		if ip, err := netip.ParseAddr(cand); err == nil {
			return ip.Unmap(), true
		}
	}
	return netip.Addr{}, false
}

func isLoopbackPreviewHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.Trim(strings.TrimSpace(host), "[]"), "."))
	if h == "" {
		return false
	}
	if h == "localhost" || h == "localhost.localdomain" || h == "wails" ||
		h == "wails.localhost" || h == "app.wails.localhost" ||
		strings.HasSuffix(h, ".localhost") ||
		h == "localtest.me" || strings.HasSuffix(h, ".localtest.me") ||
		h == "lvh.me" || strings.HasSuffix(h, ".lvh.me") ||
		h == "vcap.me" || strings.HasSuffix(h, ".vcap.me") {
		return true
	}
	if ip, err := netip.ParseAddr(h); err == nil {
		return ip.Unmap().IsLoopback()
	}
	if ip, ok := parseNonCanonicalIPv4(h); ok {
		return ip.IsLoopback()
	}
	if ip, ok := extractWildcardDNSIP(h); ok {
		return ip.IsLoopback()
	}
	return false
}

func isBlockedPreviewWildcardHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.Trim(strings.TrimSpace(host), "[]"), "."))
	if ip, ok := parseNonCanonicalIPv4(h); ok && isBlockedPreviewIP(ip) {
		return true
	}
	if ip, ok := extractWildcardDNSIP(h); ok && isBlockedPreviewIP(ip) {
		return true
	}
	return false
}

// isPublicSubresourceIP returns true ONLY for strictly public global unicast
// internet IP addresses (never loopback, RFC1918 private, CGNAT, link-local,
// cloud metadata, ULA, multicast, or reserved ranges).
func isPublicSubresourceIP(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	ip := addr.Unmap()
	if isBlockedPreviewIP(ip) || ip.IsLoopback() || ip.IsPrivate() || !ip.IsGlobalUnicast() {
		return false
	}
	for _, pfx := range nonPublicSubresourcePrefixes {
		if pfx.Contains(ip) {
			return false
		}
	}
	return true
}

func isPublicSubresourceHostname(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.Trim(strings.TrimSpace(host), "[]"), "."))
	if h == "" || isLoopbackPreviewHost(h) || isBlockedPreviewWildcardHost(h) {
		return false
	}
	if ip, err := netip.ParseAddr(h); err == nil {
		return isPublicSubresourceIP(ip)
	}
	if ip, ok := parseNonCanonicalIPv4(h); ok {
		return isPublicSubresourceIP(ip)
	}
	if ip, ok := extractWildcardDNSIP(h); ok {
		return isPublicSubresourceIP(ip)
	}
	if !strings.Contains(h, ".") {
		return false
	}
	for _, suffix := range nonPublicSubresourceHostSuffixes {
		if strings.HasSuffix(h, suffix) {
			return false
		}
	}
	return true
}

// validatePublicSubresourceURL checks whether u targets a standard web port
// (http:80 or https:443) on a public internet hostname/IP eligible for passive
// cross-origin asset loading (images, fonts, CSS, scripts, CDN data).
func validatePublicSubresourceURL(u *url.URL) (host string, port int, ok bool) {
	if u == nil || u.User != nil || u.Opaque != "" {
		return "", 0, false
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "https" {
		return "", 0, false
	}
	host = strings.ToLower(strings.Trim(strings.TrimSpace(u.Hostname()), "[]"))
	if !isPublicSubresourceHostname(host) {
		return "", 0, false
	}
	port = 80
	if scheme == "https" {
		port = 443
	}
	if portStr := strings.TrimSpace(u.Port()); portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil || p != port {
			return "", 0, false
		}
	}
	return host, port, true
}

func (sc *scope) isPreviewScope() bool {
	return sc != nil && strings.HasPrefix(strings.TrimSpace(sc.sessionID), previewScopePrefix)
}

func (sc *scope) previewNavBlockedTotal() uint64 {
	if sc == nil {
		return 0
	}
	return sc.previewNavBlocked.Load() + sc.previewProxy.NavBlockedCount()
}

func (sc *scope) consumePreviewNavBlocked() bool {
	if sc == nil {
		return false
	}
	total := sc.previewNavBlockedTotal()
	if total > sc.previewNavBlockedAcked {
		sc.previewNavBlockedAcked = total
		return true
	}
	return false
}

func isAllowedCrossOriginPreviewResourceType(rt network.ResourceType) bool {
	switch rt {
	case network.ResourceTypeStylesheet,
		network.ResourceTypeImage,
		network.ResourceTypeMedia,
		network.ResourceTypeFont,
		network.ResourceTypeScript,
		network.ResourceTypeManifest,
		network.ResourceTypeXHR,
		network.ResourceTypeFetch,
		network.ResourceTypePreflight,
		network.ResourceTypeOther:
		return true
	default:
		return false
	}
}

// decidePreviewFetchAction decides whether Chrome may proceed with an HTTP(S)
// request inside an origin-locked preview tab.
//
// Invariants:
//   - Same-origin requests to allowedOrigin always proceed (and still go through
//     previewOriginProxy's dial-time DNS/IP check).
//   - Cross-origin Document requests (both top-level navigation and iframes) are
//     always aborted; top-level blocks flag navBlocked=true.
//   - Cross-origin non-document subresources are allowed ONLY for standard
//     public-internet HTTP(:80) / HTTPS(:443) hosts and NEVER for loopback,
//     private LAN, CGNAT, link-local, metadata, or internal hostnames.
func headerValueFold(headers network.Headers, name string) string {
	for k, v := range headers {
		if strings.EqualFold(k, name) {
			if s, ok := v.(string); ok {
				return strings.ToLower(strings.TrimSpace(s))
			}
		}
	}
	return ""
}

func isEmbeddedFrameDest(dest string) bool {
	switch dest {
	case "iframe", "frame", "fencedframe", "object", "embed":
		return true
	default:
		return false
	}
}

func decidePreviewFetchAction(
	allowedOrigin string,
	mainFrameID cdp.FrameID,
	rawURL string,
	resourceType network.ResourceType,
	frameID cdp.FrameID,
	secFetchDest string,
) (allow bool, navBlocked bool, subHost string, subPort int) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		if resourceType == network.ResourceTypeDocument && !isEmbeddedFrameDest(secFetchDest) && (frameID == "" || frameID == mainFrameID) {
			return false, true, "", 0
		}
		return false, false, "", 0
	}
	reqOrigin, oErr := canonicalPreviewOrigin(u)
	if oErr == nil && reqOrigin == allowedOrigin {
		return true, false, "", 0
	}
	if resourceType == network.ResourceTypeDocument {
		if isEmbeddedFrameDest(secFetchDest) {
			return false, false, "", 0
		}
		isMain := frameID == "" || mainFrameID == "" || frameID == mainFrameID
		return false, isMain, "", 0
	}
	if !isAllowedCrossOriginPreviewResourceType(resourceType) {
		return false, false, "", 0
	}
	host, port, ok := validatePublicSubresourceURL(u)
	if !ok {
		return false, false, "", 0
	}
	return true, false, host, port
}

func (sc *scope) installPreviewBrowserGuard(browserCtx context.Context) {
	if !sc.isPreviewScope() || browserCtx == nil {
		return
	}
	allowedOrigin := sc.previewOrigin
	chromedp.ListenBrowser(browserCtx, func(raw any) {
		ev, ok := raw.(*target.EventTargetCreated)
		if !ok || ev.TargetInfo == nil || ev.TargetInfo.Type != "page" {
			return
		}
		targetID := string(ev.TargetInfo.TargetID)
		sc.tabMu.Lock()
		mainTabID := sc.workflowTabID
		sc.tabMu.Unlock()
		if mainTabID == "" || targetID == mainTabID {
			return
		}
		rawURL := strings.TrimSpace(ev.TargetInfo.URL)
		if rawURL == "" || isBlankTabURL(rawURL) || isAllowedPreviewTabURL(allowedOrigin, rawURL) {
			return
		}
		// Immediately close stray cross-origin popup targets so foreign origins
		// cannot render even transiently in a background tab.
		sc.previewNavBlocked.Add(1)
		go func(id target.ID) {
			op, cancel := context.WithTimeout(browserCtx, previewFetchActionTimeout)
			defer cancel()
			if c := chromedp.FromContext(browserCtx); c != nil && c.Browser != nil {
				_ = target.CloseTarget(id).Do(cdp.WithExecutor(op, c.Browser))
			}
		}(ev.TargetInfo.TargetID)
	})
}

func (sc *scope) installPreviewTabGuard(tab context.Context, tabID string) error {
	if !sc.isPreviewScope() || tab == nil {
		return nil
	}
	allowedOrigin := sc.previewOrigin
	mainFrameID := cdp.FrameID(tabID)
	proxy := sc.previewProxy

	// Register the target event listener BEFORE enabling Fetch interception so
	// no early requestPaused or javascriptDialogOpening event can be missed.
	chromedp.ListenTarget(tab, func(raw any) {
		switch ev := raw.(type) {
		case *page.EventJavascriptDialogOpening:
			// Auto-dismiss alert/confirm/prompt dialogs (and accept beforeunload)
			// so a modal dialog in the previewed site never deadlocks CDP commands.
			accept := ev.Type == page.DialogTypeBeforeunload
			go func() {
				op, cancel := context.WithTimeout(tab, previewDialogHandleTimeout)
				defer cancel()
				_ = chromedp.Run(op, page.HandleJavaScriptDialog(accept))
			}()
		case *fetch.EventRequestPaused:
			if ev.Request == nil {
				go func(reqID fetch.RequestID) {
					op, cancel := context.WithTimeout(tab, previewFetchActionTimeout)
					defer cancel()
					_ = chromedp.Run(op, fetch.FailRequest(reqID, network.ErrorReasonBlockedByClient))
				}(ev.RequestID)
				return
			}
			allow, navBlocked, subHost, subPort := decidePreviewFetchAction(
				allowedOrigin,
				mainFrameID,
				ev.Request.URL,
				ev.ResourceType,
				ev.FrameID,
				headerValueFold(ev.Request.Headers, "Sec-Fetch-Dest"),
			)
			if allow && subHost != "" && proxy != nil {
				proxy.AllowSubresourceHost(subHost, subPort, previewSubresourceHostTTL)
			}
			if navBlocked {
				sc.previewNavBlocked.Add(1)
			}
			go func(reqID fetch.RequestID, ok bool) {
				op, cancel := context.WithTimeout(tab, previewFetchActionTimeout)
				defer cancel()
				if ok {
					_ = chromedp.Run(op, fetch.ContinueRequest(reqID))
				} else {
					_ = chromedp.Run(op, fetch.FailRequest(reqID, network.ErrorReasonBlockedByClient))
				}
			}(ev.RequestID, allow)
		}
	})

	enableCtx, cancel := context.WithTimeout(tab, previewFetchActionTimeout)
	defer cancel()
	return chromedp.Run(enableCtx, fetch.Enable().WithPatterns([]*fetch.RequestPattern{
		{URLPattern: "http://*", RequestStage: fetch.RequestStageRequest},
		{URLPattern: "https://*", RequestStage: fetch.RequestStageRequest},
	}))
}

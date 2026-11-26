package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/diagnostics"
	"github.com/hoxiai/hoxi/internal/llm"
)

var _ llm.VideoClient = (*Service)(nil)

const (
	defaultVideoGenTimeout   = 8 * time.Minute
	defaultVideoPollInterval = 2 * time.Second
	defaultVideoProbeTimeout = 15 * time.Second
	maxVideoResponseBytes    = 100 << 20
)

// videoPollStepHook allows unit tests to shorten poll intervals without global sleeps.
var videoPollIntervalOverride time.Duration

// videoHostResolver resolves hostnames for SSRF validation; unit tests may override it.
var videoHostResolver = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

type videoGenExecOptions struct {
	aspectRatio     string
	resolution      string
	durationSeconds int
}

func normalizeVideoResolution(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1080p", "1080":
		return "1080p"
	case "720p", "720":
		return "720p"
	case "480p", "480":
		return "480p"
	default:
		return ""
	}
}

func clampVideoDuration(sec int) int {
	if sec <= 0 {
		return 0
	}
	if sec < 2 {
		return 2
	}
	if sec > 20 {
		return 20
	}
	return sec
}

// GenerateVideo executes a text-to-video or image-to-video generation request
// against the configured provider (Google Veo, Alibaba DashScope Wan, Volcengine
// Ark Seedance, Z.AI CogVideo, or OpenAI-compatible /videos endpoints).
func (s *Service) GenerateVideo(ctx context.Context, req llm.VideoRequest) (resp llm.VideoResponse, resultErr error) {
	pid := strings.TrimSpace(req.ProviderID)
	mid := strings.TrimSpace(req.ModelID)
	if pid == "" || mid == "" {
		resolvedPID, resolvedMID, ok := s.ResolveVideoModel(pid, mid)
		if !ok {
			return llm.VideoResponse{}, llm.ErrUnavailable
		}
		pid, mid = resolvedPID, resolvedMID
	}
	conn, err := s.resolveImageConnection(pid, mid)
	if err != nil {
		return llm.VideoResponse{}, err
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultVideoGenTimeout
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		if len(req.InputImages) > 0 {
			prompt = "Animate the reference image with smooth, natural motion."
		} else {
			return llm.VideoResponse{}, errors.New("prompt text is required for video generation")
		}
	}

	opts := videoGenExecOptions{
		aspectRatio:     normalizeAspectRatio(req.AspectRatio),
		resolution:      normalizeVideoResolution(req.Resolution),
		durationSeconds: clampVideoDuration(req.DurationSeconds),
	}

	started := time.Now()
	defer func() {
		level, msg := "info", "video generation completed"
		if resultErr != nil {
			level, msg = "error", "video generation failed"
			if errors.Is(resultErr, context.Canceled) {
				level, msg = "info", "video generation canceled"
			}
		}
		diagnostics.Record(diagnostics.Entry{
			Kind:       "request",
			Level:      level,
			Source:     "provider",
			Message:    msg,
			ProviderID: conn.ProviderID,
			ModelID:    conn.ModelID,
			DurationMs: time.Since(started).Milliseconds(),
		})
		if s.stats != nil {
			s.stats.recordLLMRequest(conn.ProviderID, conn.ModelID, 1, resultErr)
		}
	}()

	jobCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp, resultErr = s.executeVideoRequest(jobCtx, conn, prompt, req.InputImages, opts)
	if resultErr == nil {
		resp.ProviderID = conn.ProviderID
		resp.ModelID = conn.ModelID
	}
	return resp, resultErr
}

func (s *Service) executeVideoRequest(
	ctx context.Context,
	conn imageGenConn,
	prompt string,
	inputImages []llm.ImageInput,
	opts videoGenExecOptions,
) (llm.VideoResponse, error) {
	proto := effectiveProviderProtocol(conn.ProviderID, conn.NPM, conn.BaseURL)
	if proto == "google" {
		return s.googleVeoGenerateVideo(ctx, conn, prompt, inputImages, opts)
	}
	if isDashScopeVideoEndpoint(conn.ProviderID, conn.BaseURL) {
		return s.dashScopeGenerateVideo(ctx, conn, prompt, inputImages, opts)
	}
	if isVolcArkVideoEndpoint(conn.ProviderID, conn.BaseURL, conn.ModelID) {
		return s.volcArkGenerateVideo(ctx, conn, prompt, inputImages, opts)
	}
	if isBigModelVideoEndpoint(conn.ProviderID, conn.BaseURL) {
		return s.bigModelGenerateVideo(ctx, conn, prompt, inputImages, opts)
	}
	return s.openAIVideosGenerate(ctx, conn, prompt, inputImages, opts)
}

func (s *Service) probeVideoModel(parentCtx context.Context, conn imageGenConn) error {
	proto := effectiveProviderProtocol(conn.ProviderID, conn.NPM, conn.BaseURL)
	trimmedBase := strings.TrimRight(strings.TrimSpace(conn.BaseURL), "/")
	targetURL := trimmedBase + "/models"
	isGoogle := proto == "google"
	if isGoogle {
		cleanModel := strings.TrimPrefix(strings.TrimSpace(conn.ModelID), "models/")
		targetURL = googleModelsBaseURL(conn.BaseURL) + "/" + url.PathEscape(cleanModel)
	}
	status, _, body, err := s.doVideoHTTP(parentCtx, http.MethodGet, targetURL, conn, isGoogle, nil, nil, defaultVideoProbeTimeout)
	if err != nil {
		return err
	}
	lowerBase := strings.ToLower(trimmedBase)
	if !isGoogle && status == http.StatusNotFound &&
		!strings.HasSuffix(lowerBase, "/v1") &&
		!strings.HasSuffix(lowerBase, "/models") {
		if rStatus, _, rBody, rErr := s.doVideoHTTP(parentCtx, http.MethodGet, trimmedBase+"/v1/models", conn, false, nil, nil, defaultVideoProbeTimeout); rErr == nil && rStatus >= 200 && rStatus < 300 {
			status, body = rStatus, rBody
		}
	}
	if status < 200 || status >= 300 {
		return errors.New(redactSecretInText(extractAPIErrorMessage(status, body), conn.APIKey))
	}
	if !isGoogle {
		want := strings.TrimPrefix(strings.TrimSpace(conn.ModelID), "models/")
		for _, item := range parseOpenAIModelsPayload(body) {
			if item.ID == want {
				return nil
			}
		}
		return fmt.Errorf("provider model list did not confirm model %q", want)
	}
	return nil
}

func (s *Service) doVideoHTTP(
	parentCtx context.Context,
	method, targetURL string,
	conn imageGenConn,
	isGoogle bool,
	extraHeaders map[string]string,
	payload any,
	reqTimeout time.Duration,
) (int, string, []byte, error) {
	var rawPayload []byte
	if payload != nil {
		var err error
		rawPayload, err = json.Marshal(payload)
		if err != nil {
			return 0, "", nil, err
		}
	}
	if reqTimeout <= 0 {
		reqTimeout = 90 * time.Second
	}

	send := func(reqURL string, includeBearer bool) (int, string, []byte, error) {
		if err := isBlockedMediaTargetURL(reqURL, conn.BaseURL); err != nil {
			return 0, "", nil, err
		}
		ctx, cancel := context.WithTimeout(parentCtx, reqTimeout)
		defer cancel()
		var bodyReader io.Reader
		if rawPayload != nil {
			bodyReader = bytes.NewReader(rawPayload)
		}
		outReq, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
		if err != nil {
			return 0, "", nil, err
		}
		outReq.Header.Set("Accept", "application/json, video/*;q=0.9, */*;q=0.8")
		if rawPayload != nil {
			outReq.Header.Set("Content-Type", "application/json")
		}
		applyImageAuthHeaders(outReq, conn, isGoogle, includeBearer)
		for k, v := range extraHeaders {
			if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
				outReq.Header.Set(k, v)
			}
		}
		client := newSafeVideoHTTPClient(reqTimeout, reqURL, conn)
		resp, err := client.Do(outReq)
		if err != nil {
			return 0, "", nil, errors.New(redactSecretInText(err.Error(), conn.APIKey))
		}
		defer resp.Body.Close()
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxVideoResponseBytes+1))
		if readErr != nil {
			return resp.StatusCode, "", nil, fmt.Errorf("read video response: %w", readErr)
		}
		if len(body) > maxVideoResponseBytes {
			return resp.StatusCode, "", nil, fmt.Errorf("generated video exceeds %d MB limit", maxVideoResponseBytes>>20)
		}
		ct := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
		return resp.StatusCode, ct, body, nil
	}

	status, ct, body, err := send(targetURL, false)
	if err != nil {
		return status, ct, nil, err
	}
	if isGoogle && conn.APIKey != "" && !isOfficialGoogleHost(targetURL) &&
		(status == http.StatusUnauthorized || status == http.StatusForbidden) {
		if rStatus, rCT, rBody, rErr := send(appendGoogleQueryKey(targetURL, conn.APIKey), shouldSendBearerOnGoogleRetry(conn.APIKey)); rErr == nil {
			status, ct, body = rStatus, rCT, rBody
		}
	}
	return status, ct, body, nil
}

var (
	cgnatPrefix      = netip.MustParsePrefix("100.64.0.0/10")
	benchmarkPrefix  = netip.MustParsePrefix("198.18.0.0/15")
	zeroNetPrefix    = netip.MustParsePrefix("0.0.0.0/8")
	reservedV4Prefix = netip.MustParsePrefix("240.0.0.0/4")
)

func isLoopbackHostname(host string) bool {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		if addr, ok := netip.AddrFromSlice(ip); ok {
			return addr.Unmap().IsLoopback()
		}
		return ip.IsLoopback()
	}
	return false
}

func mediaTargetPolicy(targetURL, providerBaseURL string) (allowLoopback bool, allowedProviderAddr netip.Addr) {
	uProv, err := url.Parse(strings.TrimSpace(providerBaseURL))
	if err != nil {
		return false, netip.Addr{}
	}
	provHost := uProv.Hostname()
	allowLoopback = isLoopbackHostname(provHost)
	if sameHostIgnoreCase(targetURL, providerBaseURL) {
		if ip := net.ParseIP(provHost); ip != nil {
			if addr, ok := netip.AddrFromSlice(ip); ok {
				allowedProviderAddr = addr.Unmap()
			}
		}
	}
	return allowLoopback, allowedProviderAddr
}

func validateMediaTargetIP(ip net.IP, allowLoopback bool, allowedProviderAddr netip.Addr) error {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return errors.New("invalid video target IP address")
	}
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsUnspecified() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || zeroNetPrefix.Contains(addr) || reservedV4Prefix.Contains(addr) {
		return errors.New("refusing link-local or unspecified video download URL")
	}
	if addr.IsLoopback() {
		if !allowLoopback {
			return errors.New("refusing loopback video download URL from remote provider")
		}
		return nil
	}
	if addr.IsPrivate() || cgnatPrefix.Contains(addr) || benchmarkPrefix.Contains(addr) {
		if allowedProviderAddr.IsValid() && addr == allowedProviderAddr {
			return nil
		}
		return errors.New("refusing private or internal network video download URL from remote provider")
	}
	return nil
}

func resolveAndValidateMediaHost(ctx context.Context, host string, allowLoopback bool, allowedProviderAddr netip.Addr) (net.IP, error) {
	trimmed := strings.TrimSuffix(strings.TrimSpace(host), ".")
	if ip := net.ParseIP(trimmed); ip != nil {
		if err := validateMediaTargetIP(ip, allowLoopback, allowedProviderAddr); err != nil {
			return nil, err
		}
		return ip, nil
	}
	if isLoopbackHostname(trimmed) && !allowLoopback {
		return nil, errors.New("refusing loopback video download URL from remote provider")
	}
	records, err := videoHostResolver(ctx, trimmed)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("no IP addresses resolved for %s", trimmed)
	}
	for _, rec := range records {
		if err := validateMediaTargetIP(rec.IP, allowLoopback, allowedProviderAddr); err != nil {
			return nil, err
		}
	}
	return records[0].IP, nil
}

func isBlockedMediaTargetURL(targetURL, providerBaseURL string) error {
	parsedTarget, err := url.Parse(strings.TrimSpace(targetURL))
	if err != nil || (parsedTarget.Scheme != "http" && parsedTarget.Scheme != "https") || parsedTarget.Hostname() == "" {
		return errors.New("invalid generated video URL")
	}
	host := parsedTarget.Hostname()
	allowLoopback, allowedProviderAddr := mediaTargetPolicy(targetURL, providerBaseURL)
	if ip := net.ParseIP(host); ip != nil {
		return validateMediaTargetIP(ip, allowLoopback, allowedProviderAddr)
	}
	if isLoopbackHostname(host) && !allowLoopback {
		return errors.New("refusing loopback video download URL from remote provider")
	}
	return nil
}

func scrubCrossOriginAuthHeaders(req *http.Request, conn imageGenConn) {
	req.Header.Del("Authorization")
	req.Header.Del("x-goog-api-key")
	req.Header.Del("x-api-key")
	for k := range conn.Headers {
		if trimmed := strings.TrimSpace(k); trimmed != "" {
			req.Header.Del(trimmed)
		}
	}
}

type safeVideoRoundTripper struct {
	providerBaseURL string
}

func (rt safeVideoRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	reqURL := req.URL.String()
	if err := isBlockedMediaTargetURL(reqURL, rt.providerBaseURL); err != nil {
		return nil, err
	}
	allowLoopback, allowedProviderAddr := mediaTargetPolicy(reqURL, rt.providerBaseURL)
	host := req.URL.Hostname()
	if isLoopbackHostname(host) && allowLoopback {
		return localTransport.RoundTrip(req)
	}
	if proxyURL, _ := http.ProxyFromEnvironment(req); proxyURL != nil {
		if _, err := resolveAndValidateMediaHost(req.Context(), host, allowLoopback, allowedProviderAddr); err != nil {
			return nil, err
		}
		return providerTransport.RoundTrip(req)
	}
	tr := providerTransport.Clone()
	tr.Proxy = nil
	tr.DisableKeepAlives = true
	tr.DialContext = func(dialCtx context.Context, network, addr string) (net.Conn, error) {
		dialHost, port, splitErr := net.SplitHostPort(addr)
		if splitErr != nil {
			return nil, splitErr
		}
		safeIP, valErr := resolveAndValidateMediaHost(dialCtx, dialHost, allowLoopback, allowedProviderAddr)
		if valErr != nil {
			return nil, valErr
		}
		dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second}
		return dialer.DialContext(dialCtx, network, net.JoinHostPort(safeIP.String(), port))
	}
	return tr.RoundTrip(req)
}

func newSafeVideoHTTPClient(timeout time.Duration, _ string, conn imageGenConn) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: safeVideoRoundTripper{providerBaseURL: conn.BaseURL},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if err := isBlockedMediaTargetURL(req.URL.String(), conn.BaseURL); err != nil {
				return err
			}
			if len(via) > 0 && !sameHostIgnoreCase(req.URL.String(), via[0].URL.String()) {
				scrubCrossOriginAuthHeaders(req, conn)
			}
			return nil
		},
	}
}

func fetchGeneratedVideoURL(
	parentCtx context.Context,
	rawURL string,
	conn imageGenConn,
	isGoogle bool,
) (llm.GeneratedVideo, error) {
	trimmed := strings.TrimSpace(rawURL)
	if vid, ok := decodeDataURIVideo(trimmed); ok {
		return vid, nil
	}
	if err := isBlockedMediaTargetURL(trimmed, conn.BaseURL); err != nil {
		return llm.GeneratedVideo{}, err
	}

	ctx, cancel := context.WithTimeout(parentCtx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, trimmed, nil)
	if err != nil {
		return llm.GeneratedVideo{}, err
	}
	if sameHostIgnoreCase(trimmed, conn.BaseURL) {
		applyImageAuthHeaders(req, conn, isGoogle, false)
	}
	client := newSafeVideoHTTPClient(2*time.Minute, trimmed, conn)
	resp, err := client.Do(req)
	if err != nil {
		return llm.GeneratedVideo{}, errors.New(redactSecretInText(err.Error(), conn.APIKey))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return llm.GeneratedVideo{}, fmt.Errorf("download generated video HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxVideoResponseBytes+1))
	if err != nil || len(data) == 0 {
		return llm.GeneratedVideo{}, errors.New("empty generated video body")
	}
	if len(data) > maxVideoResponseBytes {
		return llm.GeneratedVideo{}, fmt.Errorf("generated video exceeds %d MB limit", maxVideoResponseBytes>>20)
	}
	ct := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	return llm.GeneratedVideo{
		Mime: normalizeGeneratedVideoMime(ct, data),
		Data: data,
	}, nil
}

func sameHostIgnoreCase(rawA, rawB string) bool {
	uA, errA := url.Parse(strings.TrimSpace(rawA))
	uB, errB := url.Parse(strings.TrimSpace(rawB))
	if errA != nil || errB != nil || uA.Host == "" || uB.Host == "" {
		return false
	}
	return strings.EqualFold(uA.Host, uB.Host)
}

func decodeDataURIVideo(dataURI string) (llm.GeneratedVideo, bool) {
	trimmed := strings.TrimSpace(dataURI)
	if !strings.HasPrefix(trimmed, "data:") {
		return llm.GeneratedVideo{}, false
	}
	comma := strings.IndexByte(trimmed, ',')
	if comma < 0 {
		return llm.GeneratedVideo{}, false
	}
	header := trimmed[5:comma]
	mime := strings.TrimSpace(strings.Split(header, ";")[0])
	decoded, err := decodeBase64Flexible(trimmed[comma+1:])
	if err != nil || len(decoded) == 0 {
		return llm.GeneratedVideo{}, false
	}
	return llm.GeneratedVideo{
		Mime: normalizeGeneratedVideoMime(mime, decoded),
		Data: decoded,
	}, true
}

func encodeFirstFrameDataURI(inputs []llm.ImageInput) string {
	for _, img := range inputs {
		if len(img.Data) == 0 {
			continue
		}
		mime := normalizeGeneratedImageMime(img.Mime, img.Data)
		return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
	}
	return ""
}

func normalizeGeneratedVideoMime(mime string, data []byte) string {
	m := strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))
	if strings.HasPrefix(m, "video/") {
		return m
	}
	if len(data) >= 12 {
		if string(data[4:8]) == "ftyp" {
			return "video/mp4"
		}
		if data[0] == 0x1a && data[1] == 0x45 && data[2] == 0xdf && data[3] == 0xa3 {
			return "video/webm"
		}
	}
	return "video/mp4"
}

func waitVideoPoll(ctx context.Context) error {
	d := defaultVideoPollInterval
	if videoPollIntervalOverride > 0 {
		d = videoPollIntervalOverride
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

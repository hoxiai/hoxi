package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/hoxiai/hoxi/internal/pairing"
)

const maxE2EEEnvelopeBody = 24 << 20

// appendPairingBootstrap populates direct LAN/Tailscale candidate endpoints,
// relay metadata, optional X25519-encapsulated E2EE key material, and
// (exclusively on trusted LAN connections) the plaintext E2EE master key.
func (g *Gateway) appendPairingBootstrap(resp map[string]any, r *http.Request, kexClientPub string) {
	if g.pairing == nil {
		return
	}
	if direct := g.pairing.DirectEndpoints(); len(direct) > 0 {
		resp["peers"] = direct
	}
	resp["e2ee"] = true
	src := remoteSource(r)
	if src == remoteSourceLAN {
		if e2eeKey, err := g.pairing.E2EEKey(); err == nil && e2eeKey != "" {
			resp["e2eeKey"] = e2eeKey
		}
	} else {
		delete(resp, "e2eeKey")
	}
	if kexClientPub != "" {
		if bundle, err := g.pairing.SealE2EEKeyForClientPub(kexClientPub); err == nil && bundle != nil {
			resp["e2eeKex"] = bundle
		}
	}
	if relayURL := g.pairing.RelayURL(); relayURL != "" {
		resp["relay"] = relayURL
		resp["relayUrl"] = relayURL
		if src == remoteSourceLAN || src == remoteSourceRelay {
			if relayTok, err := g.pairing.RelayToken(); err == nil && relayTok != "" {
				resp["relayToken"] = relayTok
			}
		}
	}
}

type remoteBootstrapWriter struct {
	dst    http.ResponseWriter
	header http.Header
	status int
	buf    bytes.Buffer
}

func (w *remoteBootstrapWriter) Header() http.Header { return w.header }

func (w *remoteBootstrapWriter) WriteHeader(code int) {
	if w.status == 0 && code >= 200 {
		w.status = code
	}
}

func (w *remoteBootstrapWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.buf.Write(p)
}

func (g *Gateway) wrapRemoteBootstrap(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, func()) {
	if r.Method != http.MethodGet || r.URL.Path != "/api/hoxi/bootstrap" {
		return w, func() {}
	}
	kexClientPub := strings.TrimSpace(r.Header.Get(pairing.KEXClientPubHeader))
	r.Header.Del(pairing.KEXClientPubHeader)
	bw := &remoteBootstrapWriter{dst: w, header: make(http.Header)}
	return bw, func() {
		status := bw.status
		if status == 0 {
			status = http.StatusOK
		}
		outBytes := bw.buf.Bytes()
		if status == http.StatusOK {
			var payload map[string]any
			if err := json.Unmarshal(outBytes, &payload); err == nil && payload != nil {
				g.appendPairingBootstrap(payload, r, kexClientPub)
				if encoded, mErr := json.Marshal(payload); mErr == nil {
					outBytes = append(encoded, '\n')
				}
			}
		}
		copyHeaders(bw.dst.Header(), bw.header)
		bw.dst.Header().Del("Content-Length")
		bw.dst.WriteHeader(status)
		_, _ = bw.dst.Write(outBytes)
	}
}

// unwrapRemoteE2EERequest verifies X-Hoxi-E2EE when present, decrypts any
// enveloped request body in-place so downstream route/directory guards inspect
// real plaintext, and wraps w to encrypt the HTTP or SSE response.
func (g *Gateway) unwrapRemoteE2EERequest(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, func(), bool) {
	rawHeader := strings.TrimSpace(r.Header.Get(pairing.E2EEHeader))
	r.Header.Del(pairing.E2EEHeader)
	if rawHeader == "" {
		if r.Body != nil && r.Body != http.NoBody &&
			(r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch) {
			ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
			if ct == "" || strings.HasPrefix(ct, "application/json") {
				raw, ok := readTunnelRequestBody(r)
				if !ok {
					writeJSONError(w, http.StatusRequestEntityTooLarge, "request body too large")
					return w, func() {}, false
				}
				if looksLikeE2EEEnvelope(raw) {
					writeJSONError(w, http.StatusBadRequest, "encrypted E2EE request received without X-Hoxi-E2EE header; upgrade the GoPanel relay proxy")
					return w, func() {}, false
				}
			}
		}
		return w, func() {}, true
	}
	if g.pairing == nil {
		writeJSONError(w, http.StatusUnauthorized, "e2ee is not available")
		return w, func() {}, false
	}
	cipherSuite, err := g.pairing.Cipher()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "e2ee cipher unavailable")
		return w, func() {}, false
	}
	cleanPath := r.URL.Path
	if cleanPath == "" {
		cleanPath = "/"
	}
	meta, err := cipherSuite.VerifyRequestHeader(rawHeader, r.Method, cleanPath)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, err.Error())
		return w, func() {}, false
	}
	if meta.HasBody {
		if r.Body == nil || r.Body == http.NoBody {
			writeJSONError(w, http.StatusBadRequest, "missing e2ee request body")
			return w, func() {}, false
		}
		rawEnv, readErr := io.ReadAll(io.LimitReader(r.Body, maxE2EEEnvelopeBody+1))
		_ = r.Body.Close()
		if readErr != nil || len(rawEnv) > maxE2EEEnvelopeBody {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "e2ee request body too large")
			return w, func() {}, false
		}
		plain, innerCT, openErr := cipherSuite.OpenRequest(meta, r.Method, cleanPath, rawEnv)
		if openErr != nil {
			writeJSONError(w, http.StatusUnauthorized, "invalid e2ee request envelope")
			return w, func() {}, false
		}
		if len(plain) == 0 {
			r.Body = http.NoBody
			r.ContentLength = 0
		} else {
			r.Body = io.NopCloser(bytes.NewReader(plain))
			r.ContentLength = int64(len(plain))
		}
		if innerCT != "" {
			r.Header.Set("Content-Type", innerCT)
		} else if len(plain) > 0 {
			r.Header.Set("Content-Type", "application/json")
		} else {
			r.Header.Del("Content-Type")
		}
	} else if r.Body != nil && r.Body != http.NoBody {
		ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
		if !(tunnelMultipartBodyRoute(cleanPath) && strings.HasPrefix(ct, "multipart/")) {
			probe, readErr := io.ReadAll(io.LimitReader(r.Body, 1))
			_ = r.Body.Close()
			if readErr != nil || len(probe) > 0 {
				writeJSONError(w, http.StatusBadRequest, "unencrypted request body rejected on e2ee session")
				return w, func() {}, false
			}
			r.Body = http.NoBody
			r.ContentLength = 0
		}
	}

	rw := &e2eeResponseWriter{
		dst:       w,
		header:    make(http.Header),
		cipher:    cipherSuite,
		reqID:     meta.ID,
		method:    r.Method,
		cleanPath: cleanPath,
	}
	return rw, rw.finalize, true
}

type e2eeResponseWriter struct {
	dst         http.ResponseWriter
	header      http.Header
	cipher      *pairing.E2EECipher
	reqID       string
	method      string
	cleanPath   string
	status      int
	wroteHeader bool
	streaming   bool
	finalized   bool
	buf         bytes.Buffer
	ssePartial  bytes.Buffer
	sseSeq      uint64
}

func (w *e2eeResponseWriter) Header() http.Header {
	if w.streaming {
		return w.dst.Header()
	}
	return w.header
}

func (w *e2eeResponseWriter) WriteHeader(code int) {
	if w.wroteHeader || code < 200 {
		return
	}
	w.wroteHeader = true
	w.status = code
	if strings.Contains(strings.ToLower(w.header.Get("Content-Type")), "text/event-stream") {
		w.streaming = true
		copyHeaders(w.dst.Header(), w.header)
		w.dst.Header().Del("Content-Length")
		w.dst.Header().Set(pairing.E2EEHeader, pairing.E2EEVersionSSEHeader)
		w.dst.WriteHeader(code)
	}
}

func (w *e2eeResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		if w.header.Get("Content-Type") == "" && len(p) > 0 {
			w.header.Set("Content-Type", http.DetectContentType(p))
		}
		w.WriteHeader(http.StatusOK)
	}
	if w.streaming {
		origLen := len(p)
		_, _ = w.ssePartial.Write(p)
		if err := w.flushCompleteSSELines(); err != nil {
			return 0, err
		}
		return origLen, nil
	}
	return w.buf.Write(p)
}

func (w *e2eeResponseWriter) flushCompleteSSELines() error {
	for {
		data := w.ssePartial.Bytes()
		nl := bytes.IndexByte(data, '\n')
		if nl < 0 {
			return nil
		}
		line := string(bytes.TrimSuffix(data[:nl], []byte("\r")))
		w.ssePartial.Next(nl + 1)
		if err := w.writeSSELine(line); err != nil {
			return err
		}
	}
}

func (w *e2eeResponseWriter) writeSSELine(line string) error {
	if strings.HasPrefix(line, "data:") {
		payload := strings.TrimPrefix(line, "data:")
		payload = strings.TrimPrefix(payload, " ")
		w.sseSeq++
		sealed, err := w.cipher.SealSSEData(w.reqID, w.cleanPath, w.sseSeq, []byte(payload))
		if err != nil {
			return err
		}
		_, err = w.dst.Write(append(append([]byte("data: "), sealed...), '\n'))
		return err
	}
	_, err := io.WriteString(w.dst, line+"\n")
	return err
}

func (w *e2eeResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.streaming {
		_ = w.flushCompleteSSELines()
		if f, ok := w.dst.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func (w *e2eeResponseWriter) finalize() {
	if w.finalized {
		return
	}
	w.finalized = true
	if w.streaming {
		if w.ssePartial.Len() > 0 {
			rest := strings.TrimSuffix(w.ssePartial.String(), "\r")
			w.ssePartial.Reset()
			_ = w.writeSSELine(rest)
		}
		if f, ok := w.dst.(http.Flusher); ok {
			f.Flush()
		}
		return
	}
	status := w.status
	if !w.wroteHeader {
		status = http.StatusOK
	}
	copyHeaders(w.dst.Header(), w.header)
	w.dst.Header().Del("Content-Length")
	if status == http.StatusNoContent || status == http.StatusNotModified || w.method == http.MethodHead {
		w.dst.Header().Set(pairing.E2EEHeader, pairing.E2EEVersionHeader)
		w.dst.WriteHeader(status)
		return
	}
	origCT := w.header.Get("Content-Type")
	encoded, err := w.cipher.SealResponse(w.reqID, w.method, w.cleanPath, status, origCT, w.buf.Bytes())
	if err != nil {
		w.dst.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.dst.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w.dst, `{"error":"e2ee response seal failed"}`)
		return
	}
	w.dst.Header().Set(pairing.E2EEHeader, pairing.E2EEVersionHeader)
	w.dst.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.dst.WriteHeader(status)
	_, _ = w.dst.Write(encoded)
}

func looksLikeE2EEEnvelope(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '{' {
		return false
	}
	var probe struct {
		Version    int    `json:"_hoxi_e2ee"`
		Nonce      string `json:"n"`
		Ciphertext string `json:"c"`
	}
	if json.Unmarshal(trimmed, &probe) != nil {
		return false
	}
	return probe.Version == 1 && strings.TrimSpace(probe.Nonce) != "" && strings.TrimSpace(probe.Ciphertext) != ""
}

func copyHeaders(dst, src http.Header) {
	for k, vals := range src {
		dst.Del(k)
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
}

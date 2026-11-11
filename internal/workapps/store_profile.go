package workapps

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrInvalidStoreConfig marks client-supplied store validation errors (HTTP 400).
	ErrInvalidStoreConfig = errors.New("invalid store configuration")

	localeTagRe = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8}){0,2}$`)
	ianaTZRe    = regexp.MustCompile(`^(?:UTC|GMT|[A-Za-z]+(?:_[A-Za-z0-9+-]+)*(?:/[A-Za-z0-9_+-]+){1,2})$`)
)

// StoreProfile represents the structured operational, anti-association network,
// unit-economics, and brand compliance profile of a bound Work store.
type StoreProfile struct {
	ID                string   `json:"id"`
	AppID             string   `json:"appId,omitempty"`
	Name              string   `json:"name,omitempty"`
	Platform          string   `json:"platform,omitempty"`
	Marketplace       string   `json:"marketplace,omitempty"`
	Currency          string   `json:"currency,omitempty"`
	SellerDomain      string   `json:"sellerDomain,omitempty"`
	BrowserProfileKey string   `json:"browserProfileKey,omitempty"`

	// Anti-association browser network & fingerprint settings.
	ProxyURL      string `json:"proxyURL,omitempty"`
	RedactedProxy string `json:"redactedProxy,omitempty"`
	ProxyRequired bool   `json:"proxyRequired,omitempty"`
	Timezone      string `json:"timezone,omitempty"`
	Locale        string `json:"locale,omitempty"`

	// Unit-economics & compliance guardrails automatically injected into turns.
	BrandName          string   `json:"brandName,omitempty"`
	ReferralFeePct     float64  `json:"referralFeePct,omitempty"`
	TargetNetMarginPct float64  `json:"targetNetMarginPct,omitempty"`
	ShippingRatePerKg  float64  `json:"shippingRatePerKg,omitempty"`
	ShippingCurrency   string   `json:"shippingCurrency,omitempty"`
	FXRate             float64  `json:"fxRate,omitempty"`
	ReturnRatePct      float64  `json:"returnRatePct,omitempty"`
	TargetACOSPct      float64  `json:"targetACOSPct,omitempty"`
	TargetROAS         float64  `json:"targetROAS,omitempty"`
	ForbiddenTerms     []string `json:"forbiddenTerms,omitempty"`
	Notes              string   `json:"notes,omitempty"`
}

// BrowserEnvironment carries validated per-store network & fingerprint isolation
// settings consumed by internal/browser before launching Chrome.
type BrowserEnvironment struct {
	StoreID       string `json:"storeId,omitempty"`
	ProfileKey    string `json:"profileKey,omitempty"`
	ProxyURL      string `json:"proxyURL,omitempty"`
	RedactedProxy string `json:"redactedProxy,omitempty"`
	ProxyRequired bool   `json:"proxyRequired,omitempty"`
	Timezone      string `json:"timezone,omitempty"`
	Locale        string `json:"locale,omitempty"`
}

func hasIsolationSettings(m map[string]any) bool {
	if m == nil {
		return false
	}
	if strings.TrimSpace(stringField(m, "proxyURL", "proxyUrl", "proxy")) != "" {
		return true
	}
	if boolField(m, "proxyRequired") {
		return true
	}
	if strings.TrimSpace(stringField(m, "timezone", "timeZone")) != "" {
		return true
	}
	if strings.TrimSpace(stringField(m, "locale", "language")) != "" {
		return true
	}
	return false
}

// ValidateProxyURL validates an HTTP, HTTPS, SOCKS5, or SOCKS5H proxy URL and
// rejects any control characters, Chrome flag injection, or unsupported schemes.
func ValidateProxyURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("proxy URL is empty")
	}
	if strings.HasPrefix(trimmed, "-") || strings.ContainsAny(trimmed, " \t\r\n\"'`\\;") {
		return nil, errors.New("proxy URL contains disallowed characters")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	switch scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q (expected http, https, socks5, or socks5h)", u.Scheme)
	}
	if u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("proxy URL must not contain path, query, or fragment")
	}
	host := strings.TrimSpace(u.Hostname())
	portStr := strings.TrimSpace(u.Port())
	if host == "" || strings.HasPrefix(host, "-") || portStr == "" {
		return nil, errors.New("proxy URL must specify an explicit host:port")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("proxy URL port must be between 1 and 65535")
	}
	u.Scheme = scheme
	u.Host = net.JoinHostPort(host, strconv.Itoa(port))
	u.Path = ""
	return u, nil
}

// RedactProxyURL strips credentials from a proxy URL for logs, UI state, and prompts.
func RedactProxyURL(raw string) string {
	u, err := ValidateProxyURL(raw)
	if err != nil {
		return ""
	}
	if u.User != nil {
		return fmt.Sprintf("%s://***@%s", u.Scheme, u.Host)
	}
	return fmt.Sprintf("%s://%s", u.Scheme, u.Host)
}

// ValidateTimezone checks that tz is a clean IANA timezone identifier.
func ValidateTimezone(tz string) error {
	trimmed := strings.TrimSpace(tz)
	if trimmed == "" {
		return nil
	}
	if len(trimmed) > 64 || !ianaTZRe.MatchString(trimmed) {
		return fmt.Errorf("invalid timezone %q (expected IANA name such as America/Los_Angeles)", tz)
	}
	if _, err := time.LoadLocation(trimmed); err != nil {
		return fmt.Errorf("unknown IANA timezone %q", tz)
	}
	return nil
}

// ValidateLocale checks that locale is a safe BCP-47 language tag (e.g. en-US).
func ValidateLocale(locale string) error {
	trimmed := strings.TrimSpace(locale)
	if trimmed == "" {
		return nil
	}
	if len(trimmed) > 24 || !localeTagRe.MatchString(trimmed) {
		return fmt.Errorf("invalid locale %q (expected BCP-47 tag such as en-US or de-DE)", locale)
	}
	return nil
}

// isRedactedProxyPlaceholder reports whether raw is a "***@" redacted placeholder
// returned by sanitizeStateForClient rather than actual credentials.
func isRedactedProxyPlaceholder(raw string) bool {
	u, err := ValidateProxyURL(raw)
	if err != nil || u.User == nil {
		return false
	}
	_, hasPass := u.User.Password()
	return u.User.Username() == "***" && !hasPass
}

// ValidateStatePatch validates store definitions in a work-apps patch so
// malformed proxy URLs, timezones, or locales are rejected upfront rather than
// silently dropped or left to fail later.
func ValidateStatePatch(patch map[string]any) error {
	if patch == nil {
		return nil
	}
	if storesByApp, ok := patch["storesByApp"]; ok && storesByApp != nil {
		appMap, ok := storesByApp.(map[string]any)
		if !ok {
			return fmt.Errorf("%w: storesByApp must be an object", ErrInvalidStoreConfig)
		}
		for appID, rawList := range appMap {
			list, ok := rawList.([]any)
			if !ok {
				return fmt.Errorf("%w: storesByApp.%s must be an array", ErrInvalidStoreConfig, appID)
			}
			for i, rawItem := range list {
				item, ok := rawItem.(map[string]any)
				if !ok {
					return fmt.Errorf("%w: storesByApp.%s[%d] must be an object", ErrInvalidStoreConfig, appID, i)
				}
				if err := validateStoreMap(item); err != nil {
					return fmt.Errorf("%w: storesByApp.%s[%d]: %v", ErrInvalidStoreConfig, appID, i, err)
				}
			}
		}
	}
	if storeConfigs, ok := patch["storeConfigs"]; ok && storeConfigs != nil {
		cfgMap, ok := storeConfigs.(map[string]any)
		if !ok {
			return fmt.Errorf("%w: storeConfigs must be an object", ErrInvalidStoreConfig)
		}
		for storeID, rawCfg := range cfgMap {
			if !isSafeStoreProfileKey(strings.TrimSpace(storeID)) {
				return fmt.Errorf("%w: storeConfigs key %q is invalid", ErrInvalidStoreConfig, storeID)
			}
			cfg, ok := rawCfg.(map[string]any)
			if !ok {
				return fmt.Errorf("%w: storeConfigs.%s must be an object", ErrInvalidStoreConfig, storeID)
			}
			if err := validateStoreMap(cfg); err != nil {
				return fmt.Errorf("%w: storeConfigs.%s: %v", ErrInvalidStoreConfig, storeID, err)
			}
		}
	}
	return nil
}

func validateStoreMap(m map[string]any) error {
	if id := strings.TrimSpace(stringField(m, "id")); id != "" && !isSafeStoreProfileKey(id) {
		return fmt.Errorf("invalid store id %q", id)
	}
	if profKey := strings.TrimSpace(stringField(m, "browserProfileKey")); profKey != "" && !isSafeStoreProfileKey(profKey) {
		return fmt.Errorf("invalid browserProfileKey %q", profKey)
	}
	if rawProxy := strings.TrimSpace(stringField(m, "proxyURL", "proxyUrl", "proxy")); rawProxy != "" {
		if _, err := ValidateProxyURL(rawProxy); err != nil {
			return err
		}
	}
	if tz := strings.TrimSpace(stringField(m, "timezone", "timeZone")); tz != "" {
		if err := ValidateTimezone(tz); err != nil {
			return err
		}
	}
	if loc := strings.TrimSpace(stringField(m, "locale", "language")); loc != "" {
		if err := ValidateLocale(loc); err != nil {
			return err
		}
	}
	return nil
}

func parseStoreProfile(appID string, raw map[string]any, extra map[string]any) StoreProfile {
	merged := make(map[string]any, len(raw)+len(extra))
	for k, v := range raw {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	id := strings.TrimSpace(stringField(merged, "id"))
	platform := strings.ToLower(strings.TrimSpace(stringField(merged, "platform")))
	if platform == "" {
		platform = strings.ToLower(strings.TrimSpace(appID))
	}
	rawProxy := strings.TrimSpace(stringField(merged, "proxyURL", "proxyUrl", "proxy"))
	prof := StoreProfile{
		ID:                 id,
		AppID:              strings.TrimSpace(appID),
		Name:               clippedText(stringField(merged, "name", "title", "storeName"), 120),
		Platform:           clippedText(platform, 48),
		Marketplace:        strings.ToUpper(clippedText(stringField(merged, "marketplace", "region", "site", "country"), 24)),
		Currency:           strings.ToUpper(clippedText(stringField(merged, "currency"), 12)),
		SellerDomain:       clippedText(stringField(merged, "sellerDomain", "domain"), 120),
		BrowserProfileKey:  strings.TrimSpace(stringField(merged, "browserProfileKey")),
		ProxyURL:           rawProxy,
		RedactedProxy:      RedactProxyURL(rawProxy),
		ProxyRequired:      boolField(merged, "proxyRequired"),
		Timezone:           strings.TrimSpace(stringField(merged, "timezone", "timeZone")),
		Locale:             strings.TrimSpace(stringField(merged, "locale", "language")),
		BrandName:          clippedText(stringField(merged, "brandName", "brand"), 80),
		ReferralFeePct:     clampedFloat(merged, 0, 100, "referralFeePct", "commissionPct", "platformFeePct"),
		TargetNetMarginPct: clampedFloat(merged, -100, 100, "targetNetMarginPct", "targetMarginPct"),
		ShippingRatePerKg:  clampedFloat(merged, 0, 100000, "shippingRatePerKg", "firstLegRatePerKg"),
		ShippingCurrency:   strings.ToUpper(clippedText(stringField(merged, "shippingCurrency"), 12)),
		FXRate:             clampedFloat(merged, 0, 100000, "fxRate", "exchangeRate"),
		ReturnRatePct:      clampedFloat(merged, 0, 100, "returnRatePct", "refundRatePct"),
		TargetACOSPct:      clampedFloat(merged, 0, 200, "targetACOSPct", "targetAcosPct"),
		TargetROAS:         clampedFloat(merged, 0, 1000, "targetROAS", "targetRoas"),
		ForbiddenTerms:     stringSliceField(merged, 64, 80, "forbiddenTerms", "blockedKeywords", "prohibitedWords"),
		Notes:              clippedText(stringField(merged, "notes", "positioning"), 600),
	}
	return prof
}

// PublicCopy returns a credential-scrubbed copy suitable for UI/API listing.
func (p StoreProfile) PublicCopy() StoreProfile {
	out := p
	out.RedactedProxy = RedactProxyURL(p.ProxyURL)
	out.ProxyURL = ""
	return out
}

// FormatPromptBlock renders a deterministic, credential-free XML context block
// for the bound session's system prompt.
func (p *StoreProfile) FormatPromptBlock() string {
	if p == nil || strings.TrimSpace(p.ID) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("<hoxi_work_store_profile>\n")
	label := p.Name
	if label == "" {
		label = p.ID
	}
	b.WriteString(fmt.Sprintf("Bound Store: %s (ID: %s", label, p.ID))
	if p.Platform != "" {
		b.WriteString(", Platform: " + p.Platform)
	}
	if p.Marketplace != "" {
		b.WriteString(", Marketplace: " + p.Marketplace)
	}
	if p.Currency != "" {
		b.WriteString(", Currency: " + p.Currency)
	}
	b.WriteString(")\n")

	var isoParts []string
	if p.BrowserProfileKey != "" {
		isoParts = append(isoParts, "Profile="+p.BrowserProfileKey)
	} else {
		isoParts = append(isoParts, "Profile="+p.ID)
	}
	if p.RedactedProxy != "" {
		isoParts = append(isoParts, "Proxy=isolated ("+p.RedactedProxy+")")
	} else if p.ProxyRequired {
		isoParts = append(isoParts, "Proxy=REQUIRED (unconfigured — browser blocked until proxy is set)")
	}
	if p.Timezone != "" {
		isoParts = append(isoParts, "Timezone="+p.Timezone)
	}
	if p.Locale != "" {
		isoParts = append(isoParts, "Locale="+p.Locale)
	}
	if len(isoParts) > 0 {
		b.WriteString("Browser Isolation: " + strings.Join(isoParts, ", ") + "\n")
	}

	var econ []string
	if p.ReferralFeePct > 0 {
		econ = append(econ, fmt.Sprintf("Platform Fee %.2f%%", p.ReferralFeePct))
	}
	if p.ShippingRatePerKg > 0 {
		shipCur := p.ShippingCurrency
		if shipCur == "" {
			shipCur = "CNY"
		}
		econ = append(econ, fmt.Sprintf("First-Leg Shipping %.2f %s/kg", p.ShippingRatePerKg, shipCur))
	}
	if p.FXRate > 0 {
		econ = append(econ, fmt.Sprintf("FX Rate %.4f", p.FXRate))
	}
	if p.ReturnRatePct > 0 {
		econ = append(econ, fmt.Sprintf("Return Buffer %.2f%%", p.ReturnRatePct))
	}
	if p.TargetNetMarginPct != 0 {
		econ = append(econ, fmt.Sprintf("Target Net Margin >= %.2f%%", p.TargetNetMarginPct))
	}
	if p.TargetACOSPct > 0 {
		econ = append(econ, fmt.Sprintf("Target ACOS <= %.2f%%", p.TargetACOSPct))
	}
	if p.TargetROAS > 0 {
		econ = append(econ, fmt.Sprintf("Target ROAS >= %.2fx", p.TargetROAS))
	}
	if len(econ) > 0 {
		b.WriteString("Unit Economics Baseline: " + strings.Join(econ, ", ") + "\n")
	}
	if p.BrandName != "" || len(p.ForbiddenTerms) > 0 {
		b.WriteString("Brand & Compliance Guardrails:")
		if p.BrandName != "" {
			b.WriteString(fmt.Sprintf(" Brand=%q;", p.BrandName))
		}
		if len(p.ForbiddenTerms) > 0 {
			b.WriteString(" ForbiddenTerms=[" + strings.Join(p.ForbiddenTerms, ", ") + "]")
		}
		b.WriteString("\n")
	}
	if p.Notes != "" {
		b.WriteString("Store Notes: " + p.Notes + "\n")
	}
	b.WriteString(fmt.Sprintf("Deterministic E-Commerce Engine: Prefer 'hoxi ecom profit|ads-audit|listing-check|shopify-csv --store %s' over mental math.\n", p.ID))
	b.WriteString("</hoxi_work_store_profile>")
	return b.String()
}

func stringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func boolField(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if v, ok := m[k].(bool); ok {
			return v
		}
	}
	return false
}

func clampedFloat(m map[string]any, minVal, maxVal float64, keys ...string) float64 {
	for _, k := range keys {
		raw, ok := m[k]
		if !ok || raw == nil {
			continue
		}
		var val float64
		switch n := raw.(type) {
		case float64:
			val = n
		case int:
			val = float64(n)
		case int64:
			val = float64(n)
		case string:
			parsed, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
			if err != nil {
				continue
			}
			val = parsed
		default:
			continue
		}
		if math.IsNaN(val) || math.IsInf(val, 0) {
			continue
		}
		if val < minVal {
			return minVal
		}
		if val > maxVal {
			return maxVal
		}
		return val
	}
	return 0
}

func stringSliceField(m map[string]any, maxItems, maxItemLen int, keys ...string) []string {
	for _, k := range keys {
		raw, ok := m[k]
		if !ok || raw == nil {
			continue
		}
		var out []string
		switch v := raw.(type) {
		case []string:
			for _, item := range v {
				if s := clippedText(item, maxItemLen); s != "" {
					out = append(out, s)
					if len(out) >= maxItems {
						break
					}
				}
			}
		case []any:
			for _, item := range v {
				if str, ok := item.(string); ok {
					if s := clippedText(str, maxItemLen); s != "" {
						out = append(out, s)
						if len(out) >= maxItems {
							break
						}
					}
				}
			}
		case string:
			for _, part := range strings.Split(v, ",") {
				if s := clippedText(part, maxItemLen); s != "" {
					out = append(out, s)
					if len(out) >= maxItems {
						break
					}
				}
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func clippedText(raw string, maxRunes int) string {
	cleaned := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if cleaned == "" {
		return ""
	}
	runes := []rune(cleaned)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes])
	}
	return cleaned
}

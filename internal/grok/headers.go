package grok

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aurora-develop/grok2api/internal/config"
	"github.com/aurora-develop/grok2api/internal/grok/statsig"
	"github.com/aurora-develop/grok2api/internal/platform"
)

// resolveProxyProfile returns the effective user-agent and cf_clearance.
type proxyProfile struct {
	UserAgent   string
	CFCookies   string
	CFClearance string
}

func resolveProxyProfile() proxyProfile {
	cfg := config.Global()
	ua := strings.TrimSpace(cfg.GetStr("proxy.clearance.user_agent", DefaultUserAgent))
	if ua == "" {
		ua = DefaultUserAgent
	}
	cookies := cfg.GetStr("proxy.clearance.cf_cookies", "")
	clearance := strings.TrimSpace(extractCookieValue(cookies, "cf_clearance"))
	if clearance == "" {
		clearance = strings.TrimSpace(cfg.GetStr("proxy.clearance.cf_clearance", ""))
	}
	return proxyProfile{
		UserAgent:   ua,
		CFCookies:   cookies,
		CFClearance: clearance,
	}
}

var cookieValueRE = regexp.MustCompile(`(?:^|;\s*)` + regexp.QuoteMeta("cf_clearance") + `=([^;]*)`)

func extractCookieValue(cookieHeader, name string) string {
	if cookieHeader == "" {
		return ""
	}
	pattern := regexp.MustCompile(`(?:^|;\s*)` + regexp.QuoteMeta(name) + `=([^;]*)`)
	m := pattern.FindStringSubmatch(cookieHeader)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// BuildSSOCookie builds the Cookie header value for an SSO-authenticated request.
// Includes all cookies observed in browser: sso, sso-rw, grok_device_id, x-anonuserid,
// x-challenge, x-signature, x-userid, cf_clearance, __cf_bm, and other cf cookies.
func BuildSSOCookie(ssoToken string, profile proxyProfile) string {
	tok := platform.SanitizeToken(ssoToken)

	deviceID := strings.TrimSpace(config.Global().GetStr("proxy.clearance.device_id", ""))
	if deviceID == "" {
		deviceID = uuid.NewString()
	}

	cookie := "sso=" + tok + "; sso-rw=" + tok + "; grok_device_id=" + deviceID

	// Prepend x- cookies observed in browser request, read from config
	anonUserID := strings.TrimSpace(config.Global().GetStr("proxy.clearance.x_anonuserid", ""))
	if anonUserID != "" {
		cookie += "; x-anonuserid=" + anonUserID
	}
	xChallenge := strings.TrimSpace(config.Global().GetStr("proxy.clearance.x_challenge", ""))
	if xChallenge != "" {
		cookie += "; x-challenge=" + xChallenge
	}
	xSignature := strings.TrimSpace(config.Global().GetStr("proxy.clearance.x_signature", ""))
	if xSignature != "" {
		cookie += "; x-signature=" + xSignature
	}
	xUserID := strings.TrimSpace(config.Global().GetStr("proxy.clearance.x_userid", ""))
	if xUserID != "" {
		cookie += "; x-userid=" + xUserID
	}

	cfCookies := strings.TrimSpace(profile.CFCookies)
	clearance := strings.TrimSpace(profile.CFClearance)

	if clearance != "" && cfCookies != "" {
		if cookieValueRE.MatchString(cfCookies) {
			cfCookies = cookieValueRE.ReplaceAllString(cfCookies, "cf_clearance="+clearance)
		} else {
			cfCookies = strings.TrimRight(cfCookies, "; ") + "; cf_clearance=" + clearance
		}
	} else if clearance != "" {
		cfCookies = "cf_clearance=" + clearance
	}
	if cfCookies != "" {
		cookie += "; " + cfCookies
	}
	return cookie
}

// statsigID returns the x-statsig-id header value for a (pathname, method).
//
// Priority:
//  1. proxy.clearance.statsig_id — a full fixed value (manual override).
//  2. Dynamic pair from HTML: fetch grok.com via tls_client, extract seed from
//     <meta name="grok-site‑verification">, find SVG paths, compute HEX via
//     svgfingerprint. Refreshed periodically (see statsigRefreshInterval).
//  3. Config-provided pair (proxy.clearance.statsig_seed / _hex).
//  4. Embedded default pair in the statsig package.
//  5. Fallback to the legacy error-format fake if generation fails.
func statsigID(pathname, method string) string {
	cfg := config.Global()
	if sid := strings.TrimSpace(cfg.GetStr("proxy.clearance.statsig_id", "")); sid != "" {
		return sid
	}
	// Try dynamic pair from HTML (periodic refresh)
	applyStatsigPairFromHTML()
	// Config override takes precedence over dynamic
	applyStatsigPairFromConfig()
	if pathname == "" {
		pathname = "/rest/app-chat/conversations/new"
	}
	if method == "" {
		method = "POST"
	}
	if v, err := statsig.Generate(pathname, method, time.Now().Unix()); err == nil && v != "" {
		return v
	}
	return statsigFallback()
}

// statsigPairState caches the last (seed, hex) applied from config so SetPair is
// only called when the config actually changes.
var (
	statsigPairMu   sync.Mutex
	statsigLastSeed string
	statsigLastHEX  string
)

// applyStatsigPairFromConfig pushes a config-provided genuine (seed, HEX) pair
// into the statsig package when present and changed. No-op when unset (the
// package keeps its embedded default).
func applyStatsigPairFromConfig() {
	cfg := config.Global()
	seed := strings.TrimSpace(cfg.GetStr("proxy.clearance.statsig_seed", ""))
	hx := strings.TrimSpace(cfg.GetStr("proxy.clearance.statsig_hex", ""))
	if seed == "" || hx == "" {
		return
	}
	statsigPairMu.Lock()
	defer statsigPairMu.Unlock()
	if seed == statsigLastSeed && hx == statsigLastHEX {
		return
	}
	if err := statsig.SetPair(seed, hx); err == nil {
		statsigLastSeed, statsigLastHEX = seed, hx
	}
}

// statsigFallback is the legacy error-format value used only if real generation
// fails (kept so requests still carry a plausibly-shaped header).
func statsigFallback() string {
	var msg string
	if randInt(2) == 0 {
		r := randString(5, lowerAlphaDigits)
		msg = fmt.Sprintf("x1:TypeError: Cannot read properties of null (reading 'children[\\'%s\\']')", r)
	} else {
		r := randString(10, lowerAlpha)
		msg = fmt.Sprintf("x1:TypeError: Cannot read properties of undefined (reading '%s')", r)
	}
	return base64.StdEncoding.EncodeToString([]byte(msg))
}

const (
	lowerAlpha       = "abcdefghijklmnopqrstuvwxyz"
	lowerAlphaDigits = "abcdefghijklmnopqrstuvwxyz0123456789"
)

func randInt(n int) int {
	if n <= 0 {
		return 0
	}
	b, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(b.Int64())
}

func randString(n int, charset string) string {
	if n <= 0 || charset == "" {
		return ""
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = charset[randInt(len(charset))]
	}
	return string(out)
}

// clientHints returns Sec-Ch-Ua-* headers derived from the User-Agent.
// The version is extracted from UA, NOT from the browser config label,
// so Sec-Ch-Ua and User-Agent stay consistent.
func clientHints(_ string, ua string) map[string]string {
	u := strings.ToLower(ua)
	isChromium := strings.Contains(u, "chrome") || strings.Contains(u, "chromium") || strings.Contains(u, "edg")
	if !isChromium || strings.Contains(u, "firefox") ||
		(strings.Contains(u, "safari") && !strings.Contains(u, "chrome")) {
		return nil
	}
	ver := versionFromUA(u)
	if ver == "" {
		return nil
	}
	plat := platformFromUA(u)
	arch := archFromUA(u)
	mobile := "?0"
	if strings.Contains(u, "mobile") || plat == "Android" || plat == "iOS" {
		mobile = "?1"
	}
	verBucket := versionBucket(ver)

	// Build only the hints that are non-empty — order matches Python build order.
	hints := map[string]string{
		"Sec-Ch-Ua":                  fmt.Sprintf(`"Google Chrome";v="%s", "Chromium";v="%s", "Not/A)Brand";v="99"`, verBucket, verBucket),
		"Sec-Ch-Ua-Mobile":           mobile,
		"Sec-Ch-Ua-Model":            `""`,
		"Sec-Ch-Ua-Full-Version":     fmt.Sprintf(`"%s.0.0.0"`, verBucket),
		"Sec-Ch-Ua-Platform-Version": `"13.0.0"`,
	}
	if plat != "" {
		hints["Sec-Ch-Ua-Platform"] = fmt.Sprintf(`"%s"`, plat)
	}
	if arch != "" {
		hints["Sec-Ch-Ua-Arch"] = fmt.Sprintf(`"%s"`, arch)
		hints["Sec-Ch-Ua-Bitness"] = `"64"`
	}
	return hints
}

var versionRE = regexp.MustCompile(`[Cc]hrome/(\d{2,3})`)

func versionFromUA(ua string) string {
	m := versionRE.FindStringSubmatch(ua)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

// versionBucket returns the reduced Chrome version for Sec-Ch-Ua.
// Chrome ≥ 100 uses a bucket: 147→10, 126→12, etc.
func versionBucket(full string) string {
	if len(full) >= 3 {
		return full[:len(full)-1]
	}
	return full
}

func platformFromUA(u string) string {
	switch {
	case strings.Contains(u, "windows"):
		return "Windows"
	case strings.Contains(u, "mac os x") || strings.Contains(u, "macintosh"):
		return "macOS"
	case strings.Contains(u, "android"):
		return "Android"
	case strings.Contains(u, "iphone") || strings.Contains(u, "ipad"):
		return "iOS"
	case strings.Contains(u, "linux"):
		return "Linux"
	}
	return ""
}

func archFromUA(u string) string {
	switch {
	case strings.Contains(u, "aarch64") || strings.Contains(u, "arm"):
		return "arm"
	case strings.Contains(u, "x86_64") || strings.Contains(u, "x64") ||
		strings.Contains(u, "win64") || strings.Contains(u, "intel"):
		return "x86"
	}
	return ""
}

// BuildHTTPHeaders builds the standard reverse-proxy headers for a grok.com
// request. reqURL and method are used to compute the per-request x-statsig-id.
// Returns standard net/http.Header.
//
// Sends only the minimal headers that grok's anti-bot requires — Content-Type,
// User-Agent, Cookie (with sso + cf_clearance + anti-bot tokens), x-statsig-id.
// Extra browser-fingerprint headers (sentry-trace, Baggage, Sec-Ch-Ua*, etc.)
// are omitted because grok detects mismatches between these and a real browser
// session, causing code:7 anti-bot rejection. The minimal set was verified to
// pass anti-bot (HTTP 404 code:5 = "Model not found", confirming acceptance).
func BuildHTTPHeaders(ssoToken string, contentType, origin, referer, reqURL, method string, profile proxyProfile) http.Header {
	ua := profile.UserAgent
	if ua == "" {
		ua = DefaultUserAgent
	}
	if contentType == "" {
		contentType = "application/json"
	}
	h := http.Header{}
	h.Set("Content-Type", contentType)
	h.Set("User-Agent", ua)
	h.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	h.Set("x-statsig-id", statsigID(pathOf(reqURL), method))
	cookie := BuildSSOCookie(ssoToken, profile)
	h.Set("Cookie", cookie)

	return h
}

// BuildConsoleHeaders builds headers for console.x.ai/v1/responses requests.
func BuildConsoleHeaders(ssoToken string, contentType string, profile proxyProfile) http.Header {
	if contentType == "" {
		contentType = "application/json"
	}
	ua := profile.UserAgent
	if ua == "" {
		ua = DefaultUserAgent
	}
	tok := platform.SanitizeToken(ssoToken)
	cookie := "sso=" + tok + "; sso-rw=" + tok
	if profile.CFClearance != "" {
		cookie += "; cf_clearance=" + profile.CFClearance
	}
	h := http.Header{}
	h.Set("Accept", "*/*")
	h.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	h.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	h.Set("Authorization", "Bearer anonymous")
	h.Set("Content-Type", contentType)
	h.Set("Cookie", cookie)
	h.Set("Origin", "https://console.x.ai")
	h.Set("Priority", "u=1, i")
	h.Set("Referer", "https://console.x.ai/")
	h.Set("Sec-Fetch-Dest", "empty")
	h.Set("Sec-Fetch-Mode", "cors")
	h.Set("Sec-Fetch-Site", "same-origin")
	h.Set("User-Agent", ua)
	h.Set("x-cluster", "https://us-east-1.api.x.ai")
	for k, v := range clientHints("", profile.UserAgent) {
		h.Set(k, v)
	}
	return h
}

// BuildGRPCWebHeaders merges a base HTTP headers map with gRPC-Web specific headers.
func BuildGRPCWebHeaders(base http.Header) http.Header {
	base.Set("Content-Type", "application/grpc-web+proto")
	base.Set("Accept", "*/*")
	base.Set("x-grpc-web", "1")
	base.Set("x-user-agent", "connect-es/2.1.1")
	base.Set("Cache-Control", "no-cache")
	base.Set("Pragma", "no-cache")
	base.Set("Sec-Fetch-Dest", "empty")
	return base
}

// newTraceID generates a random 32-char hex trace ID.
func newTraceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// newSpanID generates a random 16-char hex span ID.
func newSpanID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// orDefault returns *v* if non-empty, else *def*.
func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func hostOf(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Host
}

// pathOf returns the URL path (statsig is computed over the pathname only).
func pathOf(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Path == "" {
		return ""
	}
	return u.Path
}

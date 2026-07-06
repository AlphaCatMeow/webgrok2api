package grok

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aurora-develop/grok2api/internal/config"
	"github.com/aurora-develop/grok2api/internal/grok/statsig"
	"github.com/aurora-develop/grok2api/internal/grok/statsig/svgfingerprint"
	tlsclient "github.com/aurora-develop/grok2api/internal/tlsclient"
)

const statsigRefreshInterval = 30 * time.Minute

var (
	statsigHTMLMu          sync.Mutex
	statsigHTMLLastAttempt time.Time
	statsigHTMLLastSeed    string
	statsigHTMLLastHEX     string
)

var (
	statsigMetaTagRE = regexp.MustCompile(`(?is)<meta\b[^>]*\bname=["']grok-site―verification["'][^>]*>`)
	contentAttrRE    = regexp.MustCompile(`(?is)\bcontent=["']([^"']+)["']`)
)

// applyStatsigPairFromHTML tries to refresh the active (seed, HEX) pair from
// grok.com's rendered HTML using tls_client. If it cannot find enough SVG path
// data (the real statsig SVG is often transient and absent from SSR HTML), it
// silently leaves the existing pair in place.
func applyStatsigPairFromHTML() {
	cfg := config.Global()
	if !cfg.GetBool("proxy.clearance.statsig_from_html", false) {
		return
	}

	statsigHTMLMu.Lock()
	defer statsigHTMLMu.Unlock()

	if !statsigHTMLLastAttempt.IsZero() && time.Since(statsigHTMLLastAttempt) < statsigRefreshInterval {
		return
	}
	statsigHTMLLastAttempt = time.Now()

	seed, hx, err := fetchStatsigPairFromHTML(context.Background())
	if err != nil || seed == "" || hx == "" {
		return
	}
	if seed == statsigHTMLLastSeed && hx == statsigHTMLLastHEX {
		return
	}
	if err := statsig.SetPair(seed, hx); err == nil {
		statsigHTMLLastSeed, statsigHTMLLastHEX = seed, hx
	}
}

func fetchStatsigPairFromHTML(ctx context.Context) (string, string, error) {
	cfg := config.Global()
	var opts []tlsclient.Option
	proxyURL := normalizeProxyURL(strings.TrimSpace(cfg.GetStr("proxy.egress.proxy_url", "")))
	if proxyURL != "" {
		opts = append(opts, tlsclient.WithProxy(proxyURL))
	}
	client := tlsclient.New(opts...)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://grok.com/", nil)
	if err != nil {
		return "", "", err
	}
	profile := resolveProxyProfile()
	ua := profile.UserAgent
	if ua == "" {
		ua = DefaultUserAgent
	}
	req.Header = http.Header{}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("User-Agent", ua)
	for k, v := range clientHints("", ua) {
		req.Header.Set(k, v)
	}
	if cookie := htmlCookieHeader(profile); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", "", err
	}

	seed := extractStatsigSeedFromHTML(body)
	if seed == "" {
		return "", "", nil
	}

	hx, err := svgfingerprint.ComputeHEXForSeedB64(seed)
	if err != nil {
		return seed, "", err
	}
	return seed, hx, nil
}

func htmlCookieHeader(profile proxyProfile) string {
	cookie := strings.TrimSpace(profile.CFCookies)
	if profile.CFClearance != "" && extractCookieValue(cookie, "cf_clearance") == "" {
		if cookie != "" {
			cookie += "; "
		}
		cookie += "cf_clearance=" + profile.CFClearance
	}
	return cookie
}

func extractStatsigSeedFromHTML(body []byte) string {
	tag := statsigMetaTagRE.Find(body)
	if len(tag) == 0 {
		return ""
	}
	m := contentAttrRE.FindSubmatch(tag)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(string(m[1]))
}

package grok

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	tlsclient "github.com/aurora-develop/grok2api/internal/tlsclient"

	"github.com/aurora-develop/grok2api/internal/config"
	"github.com/aurora-develop/grok2api/internal/platform"
)

// Transport is the HTTP client for upstream Grok requests.  It uses
// tlsclient.Client for TLS fingerprinting (Chrome_146) and converts
// everything to standard *http.Response so callers never see fhttp.
type Transport struct {
	mu           sync.Mutex
	client       *tlsclient.Client
	proxyURL     string
	resetOn      map[int]struct{}
	resetPending bool
	extraCookies []*http.Cookie

	dpopMu       sync.Mutex
	dpopSessions map[string]dpopSession
	dpopLoading  map[string]chan struct{}
}

// NewTransport builds a Transport from config.
func NewTransport() (*Transport, error) {
	cfg := config.Global()
	codes := cfg.GetList("retry.reset_session_status_codes", []string{"403"})
	reset := make(map[int]struct{}, len(codes))
	for _, c := range codes {
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(c), "%d", &n); err == nil {
			reset[n] = struct{}{}
		}
	}
	proxyURL := normalizeProxyURL(strings.TrimSpace(cfg.GetStr("proxy.egress.proxy_url", "")))
	if envProxy := strings.TrimSpace(os.Getenv("PROXY_HTTP")); envProxy != "" {
		proxyURL = normalizeProxyURL(envProxy)
	}
	return &Transport{
		proxyURL:     proxyURL,
		resetOn:      reset,
		dpopSessions: map[string]dpopSession{},
		dpopLoading:  map[string]chan struct{}{},
	}, nil
}

func (t *Transport) SetProxy(proxyURL string) {
	n := normalizeProxyURL(strings.TrimSpace(proxyURL))
	t.mu.Lock()
	defer t.mu.Unlock()
	if n == t.proxyURL {
		return
	}
	t.proxyURL = n
	t.resetPending = true
}

func (t *Transport) Reset() {
	t.mu.Lock()
	t.resetPending = true
	t.mu.Unlock()
}

func (t *Transport) ensureClient() (*tlsclient.Client, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.client != nil && !t.resetPending {
		return t.client, nil
	}
	var opts []tlsclient.Option
	if t.proxyURL != "" {
		opts = append(opts, tlsclient.WithProxy(t.proxyURL))
	}
	c := tlsclient.New(opts...)
	t.client = c
	t.resetPending = false
	return c, nil
}

func (t *Transport) markResetIfStatus(status int) {
	if _, ok := t.resetOn[status]; !ok {
		return
	}
	t.mu.Lock()
	t.resetPending = true
	t.mu.Unlock()
}

// --- request options -------------------------------------------------------

type reqOpts struct {
	timeout       time.Duration
	origin        string
	referer       string
	contentType   string
	consoleMode   bool
	extraHeaders  http.Header
	noContentType bool
}

type RequestOption func(*reqOpts)

func WithTimeout(d time.Duration) RequestOption    { return func(o *reqOpts) { o.timeout = d } }
func WithOrigin(v string) RequestOption            { return func(o *reqOpts) { o.origin = v } }
func WithReferer(v string) RequestOption           { return func(o *reqOpts) { o.referer = v } }
func WithContentType(v string) RequestOption       { return func(o *reqOpts) { o.contentType = v } }
func WithConsoleMode() RequestOption               { return func(o *reqOpts) { o.consoleMode = true } }
func WithExtraHeaders(h http.Header) RequestOption { return func(o *reqOpts) { o.extraHeaders = h } }
func WithoutContentType() RequestOption            { return func(o *reqOpts) { o.noContentType = true } }

func applyOpts(opts []RequestOption) reqOpts {
	o := reqOpts{
		timeout:     120 * time.Second,
		origin:      "https://grok.com",
		referer:     "https://grok.com/",
		contentType: "application/json",
	}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// --- HTTP verbs ------------------------------------------------------------

func (t *Transport) PostJSON(ctx context.Context, urlStr, token string, payload []byte, opts ...RequestOption) (map[string]any, error) {
	o := applyOpts(opts)
	resp, err := t.do(ctx, http.MethodPost, urlStr, token, bytes.NewReader(payload), o)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, platform.UpstreamError("read response body failed: "+err.Error(), resp.StatusCode, "")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.markResetIfStatus(resp.StatusCode)
		return nil, platform.UpstreamError(fmt.Sprintf("Upstream returned %d", resp.StatusCode), resp.StatusCode, truncBody(body, 400))
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, platform.UpstreamError("failed to decode JSON body: "+err.Error(), resp.StatusCode, truncBody(body, 400))
	}
	return out, nil
}

func (t *Transport) PostStream(ctx context.Context, urlStr, token string, payload []byte, opts ...RequestOption) (io.ReadCloser, error) {
	o := applyOpts(opts)
	resp, err := t.do(ctx, http.MethodPost, urlStr, token, bytes.NewReader(payload), o)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.markResetIfStatus(resp.StatusCode)
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		bt := truncBody(body, 400)
		msg := fmt.Sprintf("Upstream returned %d", resp.StatusCode)
		if bt != "" {
			msg += ": " + bt
		}
		return nil, platform.UpstreamError(msg, resp.StatusCode, bt)
	}
	return resp.Body, nil
}

func (t *Transport) GetJSON(ctx context.Context, urlStr, token string, opts ...RequestOption) (map[string]any, error) {
	o := applyOpts(opts)
	resp, err := t.do(ctx, http.MethodGet, urlStr, token, nil, o)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, platform.UpstreamError("read response body failed: "+err.Error(), resp.StatusCode, "")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.markResetIfStatus(resp.StatusCode)
		return nil, platform.UpstreamError(fmt.Sprintf("Upstream returned %d", resp.StatusCode), resp.StatusCode, truncBody(body, 400))
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, platform.UpstreamError("failed to decode JSON body: "+err.Error(), resp.StatusCode, truncBody(body, 400))
	}
	return out, nil
}

func (t *Transport) DeleteJSON(ctx context.Context, urlStr, token string, opts ...RequestOption) (map[string]any, error) {
	o := applyOpts(opts)
	resp, err := t.do(ctx, http.MethodDelete, urlStr, token, nil, o)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, platform.UpstreamError("read response body failed: "+err.Error(), resp.StatusCode, "")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.markResetIfStatus(resp.StatusCode)
		return nil, platform.UpstreamError(fmt.Sprintf("Upstream returned %d", resp.StatusCode), resp.StatusCode, truncBody(body, 400))
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, platform.UpstreamError("failed to decode JSON body: "+err.Error(), resp.StatusCode, truncBody(body, 400))
	}
	return out, nil
}

func (t *Transport) GetBytes(ctx context.Context, urlStr, token string, opts ...RequestOption) (io.ReadCloser, error) {
	o := applyOpts(opts)
	resp, err := t.do(ctx, http.MethodGet, urlStr, token, nil, o)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.markResetIfStatus(resp.StatusCode)
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, platform.UpstreamError(fmt.Sprintf("Upstream returned %d", resp.StatusCode), resp.StatusCode, truncBody(body, 400))
	}
	return resp.Body, nil
}

func (t *Transport) PostGRPCWeb(ctx context.Context, urlStr, token string, payload []byte, opts ...RequestOption) ([][]byte, map[string]string, error) {
	o := applyOpts(opts)
	o.contentType = "application/grpc-web+proto"
	resp, err := t.do(ctx, http.MethodPost, urlStr, token, bytes.NewReader(payload), o)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, platform.UpstreamError("read response body failed: "+err.Error(), resp.StatusCode, "")
	}
	if resp.StatusCode != 200 {
		t.markResetIfStatus(resp.StatusCode)
		return nil, nil, platform.UpstreamError(fmt.Sprintf("Upstream returned %d", resp.StatusCode), resp.StatusCode, truncBody(body, 300))
	}
	ct := resp.Header.Get("Content-Type")
	msgs, trailers := ParseGRPCWebResponse(body, ct, resp.Header)
	return msgs, trailers, nil
}

// do builds standard *http.Request with headers, then sends via tlsclient.
func (t *Transport) do(ctx context.Context, method, urlStr, token string, body io.Reader, o reqOpts) (*http.Response, error) {
	if o.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.timeout)
		defer cancel()
	}

	var payload []byte
	var err error
	if body != nil {
		payload, err = io.ReadAll(body)
		if err != nil {
			return nil, platform.UpstreamError("read request body failed: "+err.Error(), 500, "")
		}
	}

	attempts := 1
	if o.consoleMode {
		attempts = 2
	}
	for attempt := 0; attempt < attempts; attempt++ {
		resp, sessionToken, reqErr := t.doOnce(ctx, method, urlStr, token, payload, o)
		if reqErr != nil {
			return nil, reqErr
		}
		if !o.consoleMode || resp.StatusCode != http.StatusUnauthorized || attempt+1 >= attempts {
			return resp, nil
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		t.invalidateDPoPSession(token, consoleRequestBase(urlStr), sessionToken)
	}
	return nil, platform.UpstreamError("Console DPoP retry state invalid", 502, "")
}

func (t *Transport) doOnce(ctx context.Context, method, urlStr, token string, payload []byte, o reqOpts) (*http.Response, string, error) {
	profile := resolveProxyProfile()
	var headers http.Header
	var sessionToken string
	if o.consoleMode {
		headers = BuildConsoleHeaders(token, o.contentType, profile)
	} else {
		headers = BuildHTTPHeaders(token, o.contentType, o.origin, o.referer, urlStr, method, profile)
	}
	if o.extraHeaders != nil {
		for k, vs := range o.extraHeaders {
			for _, v := range vs {
				headers.Set(k, v)
			}
		}
	}
	if o.noContentType {
		headers.Del("Content-Type")
		headers.Del("Origin")
	}

	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return nil, "", platform.UpstreamError("build request failed: "+err.Error(), 502, "")
	}
	req.Header = headers
	if o.consoleMode {
		session, err := t.getDPoPSession(ctx, token, consoleRequestBase(urlStr), profile)
		if err != nil {
			return nil, "", err
		}
		if !strings.HasSuffix(req.URL.Path, "/responses") {
			req.Header.Del("x-cluster")
		}
		if err := applyDPoPAuthorization(req, session); err != nil {
			return nil, "", err
		}
		sessionToken = session.accessToken
	}

	client, err := t.ensureClient()
	if err != nil {
		return nil, "", platform.UpstreamError("transport init failed: "+err.Error(), 502, "")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.mu.Lock()
		t.resetPending = true
		t.mu.Unlock()
		return nil, "", platform.UpstreamError("transport request failed: "+err.Error(), 502, "")
	}
	return resp, sessionToken, nil
}

// --- helpers ---------------------------------------------------------------

func truncBody(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n])
}

func normalizeProxyURL(u string) string {
	if u == "" {
		return ""
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}
	switch strings.ToLower(parsed.Scheme) {
	case "socks":
		return "socks5h://" + strings.TrimPrefix(u, "socks://")
	case "socks5":
		return "socks5h://" + strings.TrimPrefix(u, "socks5://")
	case "socks4":
		return "socks4a://" + strings.TrimPrefix(u, "socks4://")
	}
	return u
}

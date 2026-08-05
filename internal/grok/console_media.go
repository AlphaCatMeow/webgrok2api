package grok

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ConsoleImageGeneration calls the standard Console image generation resource.
func (t *Transport) ConsoleImageGeneration(ctx context.Context, token string, payload map[string]any) (map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode Console image payload: %w", err)
	}
	return t.PostJSON(ctx, ConsoleImageGeneration, token, body, WithConsoleMode(), WithTimeout(5*time.Minute))
}

// ConsoleImageEdit calls the standard Console image edit resource.
func (t *Transport) ConsoleImageEdit(ctx context.Context, token string, payload map[string]any) (map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode Console image edit payload: %w", err)
	}
	return t.PostJSON(ctx, ConsoleImageEdits, token, body, WithConsoleMode(), WithTimeout(5*time.Minute))
}

// ConsoleVideoGeneration creates a standard Console video job.
func (t *Transport) ConsoleVideoGeneration(ctx context.Context, token string, payload map[string]any) (map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode Console video payload: %w", err)
	}
	return t.PostJSON(ctx, ConsoleVideoGeneration, token, body, WithConsoleMode(), WithTimeout(5*time.Minute))
}

// ConsoleVideoStatus polls a standard Console video job.
func (t *Transport) ConsoleVideoStatus(ctx context.Context, token, requestID string) (map[string]any, error) {
	path := ConsoleVideos + "/" + url.PathEscape(strings.TrimSpace(requestID))
	return t.GetJSON(ctx, path, token, WithConsoleMode(), WithTimeout(5*time.Minute))
}

// ConsoleVideoStatusAt polls a Console video job against a custom base URL.
// It is useful for protocol tests and keeps the DPoP binding tied to the URL.
func (t *Transport) ConsoleVideoStatusAt(ctx context.Context, endpoint, token string) (map[string]any, error) {
	return t.GetJSON(ctx, endpoint, token, WithConsoleMode(), WithTimeout(5*time.Minute))
}

// DownloadConsoleVideo downloads a completed video from the trusted vidgen.x.ai CDN.
func (t *Transport) DownloadConsoleVideo(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || !trustedConsoleVideoHost(parsed.Hostname()) {
		return nil, fmt.Errorf("untrusted Console video URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	profile := resolveProxyProfile()
	userAgent := profile.UserAgent
	if userAgent == "" {
		userAgent = DefaultUserAgent
	}
	req.Header.Set("Accept", "video/*,*/*;q=0.8")
	req.Header.Set("User-Agent", userAgent)
	client, err := t.ensureClient()
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.Request != nil && resp.Request.URL != nil && !trustedConsoleVideoHost(resp.Request.URL.Hostname()) {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("Console video redirected to an untrusted host")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("Console video download returned %d", resp.StatusCode)
	}
	return resp.Body, nil
}

func trustedConsoleVideoHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "vidgen.x.ai" || strings.HasSuffix(host, ".vidgen.x.ai")
}

// ConsoleMediaURL returns the upstream URL for a generated media response.
func ConsoleMediaURL(value any) string {
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// ConsoleVideoEndpoint builds the standard Console video status URL.
func ConsoleVideoEndpoint(requestID string) string {
	return ConsoleVideos + "/" + url.PathEscape(strings.TrimSpace(requestID))
}

package grok

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aurora-develop/grok2api/internal/platform"
)

const (
	dpopRefreshSkew      = 20 * time.Second
	maxDPoPTokenLifetime = time.Hour
	dpopTokenBodyLimit   = 1 << 20
)

type dpopJWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type dpopSession struct {
	accessToken string
	privateKey  *ecdsa.PrivateKey
	publicJWK   dpopJWK
	expiresAt   time.Time
}

func dpopSessionKey(baseURL, ssoToken string) string {
	digest := sha256.Sum256([]byte(platform.SanitizeToken(ssoToken)))
	return strings.TrimRight(baseURL, "/") + "|" + base64.RawURLEncoding.EncodeToString(digest[:])
}

func (t *Transport) getDPoPSession(ctx context.Context, ssoToken, baseURL string, profile proxyProfile) (dpopSession, error) {
	key := dpopSessionKey(baseURL, ssoToken)
	for {
		t.dpopMu.Lock()
		if t.dpopSessions == nil {
			t.dpopSessions = map[string]dpopSession{}
		}
		if t.dpopLoading == nil {
			t.dpopLoading = map[string]chan struct{}{}
		}
		if session, ok := t.dpopSessions[key]; ok && session.expiresAt.After(time.Now().UTC().Add(dpopRefreshSkew)) {
			t.dpopMu.Unlock()
			return session, nil
		}
		if done := t.dpopLoading[key]; done != nil {
			t.dpopMu.Unlock()
			select {
			case <-ctx.Done():
				return dpopSession{}, ctx.Err()
			case <-done:
				continue
			}
		}
		done := make(chan struct{})
		t.dpopLoading[key] = done
		t.dpopMu.Unlock()

		session, err := t.fetchDPoPSession(ctx, ssoToken, baseURL, profile)
		t.dpopMu.Lock()
		delete(t.dpopLoading, key)
		if err == nil {
			t.dpopSessions[key] = session
		}
		close(done)
		t.dpopMu.Unlock()
		return session, err
	}
}

func (t *Transport) invalidateDPoPSession(ssoToken, baseURL, accessToken string) {
	key := dpopSessionKey(baseURL, ssoToken)
	t.dpopMu.Lock()
	defer t.dpopMu.Unlock()
	if current, ok := t.dpopSessions[key]; ok && (accessToken == "" || current.accessToken == accessToken) {
		delete(t.dpopSessions, key)
	}
}

func (t *Transport) fetchDPoPSession(ctx context.Context, ssoToken, baseURL string, profile proxyProfile) (dpopSession, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return dpopSession{}, fmt.Errorf("generate Console DPoP key: %w", err)
	}
	publicJWK := publicDPoPJWK(&privateKey.PublicKey)
	payload, err := json.Marshal(map[string]any{"jwk": publicJWK})
	if err != nil {
		return dpopSession{}, err
	}
	endpoint := consoleV1Endpoint(baseURL, "/dpop/token")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return dpopSession{}, err
	}
	req.Header = BuildConsoleHeaders(ssoToken, "application/json", profile)
	req.Header.Del("Authorization")
	req.Header.Del("DPoP")
	req.Header.Del("x-cluster")

	client, err := t.ensureClient()
	if err != nil {
		return dpopSession{}, platform.UpstreamError("transport init failed: "+err.Error(), 502, "")
	}
	resp, err := client.Do(req)
	if err != nil {
		return dpopSession{}, platform.UpstreamError("Console DPoP token request failed: "+err.Error(), 502, "")
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, dpopTokenBodyLimit+1))
	if readErr != nil {
		return dpopSession{}, platform.UpstreamError("read Console DPoP token response: "+readErr.Error(), resp.StatusCode, "")
	}
	if len(body) > dpopTokenBodyLimit {
		return dpopSession{}, platform.UpstreamError("Console DPoP token response exceeds 1 MiB", 502, "")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return dpopSession{}, platform.UpstreamError(fmt.Sprintf("Console DPoP token endpoint returned %d", resp.StatusCode), resp.StatusCode, truncBody(body, 400))
	}
	var tokenResponse struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResponse); err != nil {
		return dpopSession{}, fmt.Errorf("decode Console DPoP token: %w", err)
	}
	if strings.TrimSpace(tokenResponse.AccessToken) == "" || !strings.EqualFold(strings.TrimSpace(tokenResponse.TokenType), "DPoP") {
		return dpopSession{}, errors.New("invalid Console DPoP token response")
	}
	if tokenResponse.ExpiresIn <= 0 || time.Duration(tokenResponse.ExpiresIn)*time.Second > maxDPoPTokenLifetime {
		return dpopSession{}, errors.New("invalid Console DPoP token lifetime")
	}
	thumbprint, err := dpopJWKThumbprint(publicJWK)
	if err != nil {
		return dpopSession{}, err
	}
	tokenExpiry, tokenThumbprint, err := parseDPoPAccessToken(tokenResponse.AccessToken)
	if err != nil {
		return dpopSession{}, err
	}
	if tokenThumbprint != thumbprint {
		return dpopSession{}, errors.New("Console DPoP token is not bound to the local key")
	}
	now := time.Now().UTC()
	expiresAt := now.Add(time.Duration(tokenResponse.ExpiresIn) * time.Second)
	if tokenExpiry.Before(expiresAt) {
		expiresAt = tokenExpiry
	}
	if !expiresAt.After(now.Add(dpopRefreshSkew)) {
		return dpopSession{}, errors.New("Console DPoP token is expired or near expiry")
	}
	return dpopSession{accessToken: tokenResponse.AccessToken, privateKey: privateKey, publicJWK: publicJWK, expiresAt: expiresAt}, nil
}

func publicDPoPJWK(key *ecdsa.PublicKey) dpopJWK {
	return dpopJWK{
		Kty: "EC",
		Crv: "P-256",
		X:   base64.RawURLEncoding.EncodeToString(key.X.FillBytes(make([]byte, 32))),
		Y:   base64.RawURLEncoding.EncodeToString(key.Y.FillBytes(make([]byte, 32))),
	}
}

func dpopJWKThumbprint(jwk dpopJWK) (string, error) {
	canonical := struct {
		Crv string `json:"crv"`
		Kty string `json:"kty"`
		X   string `json:"x"`
		Y   string `json:"y"`
	}{Crv: jwk.Crv, Kty: jwk.Kty, X: jwk.X, Y: jwk.Y}
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return base64.RawURLEncoding.EncodeToString(digest[:]), nil
}

func parseDPoPAccessToken(value string) (time.Time, string, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return time.Time{}, "", errors.New("invalid Console DPoP access token format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, "", errors.New("invalid Console DPoP access token payload")
	}
	var claims struct {
		ExpiresAt int64 `json:"exp"`
		CNF       struct {
			JKT string `json:"jkt"`
		} `json:"cnf"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.ExpiresAt <= 0 || strings.TrimSpace(claims.CNF.JKT) == "" {
		return time.Time{}, "", errors.New("invalid Console DPoP access token claims")
	}
	return time.Unix(claims.ExpiresAt, 0).UTC(), claims.CNF.JKT, nil
}

func applyDPoPAuthorization(req *http.Request, session dpopSession) error {
	if req == nil || req.URL == nil || session.privateKey == nil || strings.TrimSpace(session.accessToken) == "" {
		return errors.New("invalid Console DPoP request parameters")
	}
	digest := sha256.Sum256([]byte(session.accessToken))
	header := map[string]any{"alg": "ES256", "typ": "dpop+jwt", "jwk": session.publicJWK}
	claims := map[string]any{
		"jti": uuid.NewString(),
		"htm": strings.ToUpper(req.Method),
		"htu": dpopHTU(req),
		"iat": time.Now().UTC().Unix(),
		"ath": base64.RawURLEncoding.EncodeToString(digest[:]),
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	hash := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, session.privateKey, hash[:])
	if err != nil {
		return fmt.Errorf("sign Console DPoP proof: %w", err)
	}
	signature := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	proof := unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
	req.Header.Set("Authorization", "DPoP "+session.accessToken)
	req.Header.Set("DPoP", proof)
	return nil
}

func dpopHTU(req *http.Request) string {
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	return req.URL.Scheme + "://" + req.URL.Host + path
}

func consoleV1Endpoint(baseURL, path string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	path = "/" + strings.TrimLeft(strings.TrimSpace(path), "/")
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + path
	}
	return baseURL + "/v1" + path
}

func consoleRequestBase(rawURL string) string {
	reqURL, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil || reqURL.URL == nil || reqURL.URL.Scheme == "" || reqURL.URL.Host == "" {
		return ConsoleBase
	}
	return reqURL.URL.Scheme + "://" + reqURL.URL.Host
}

func parseECDSASignature(raw []byte) (*big.Int, *big.Int, error) {
	if len(raw) != 64 {
		return nil, nil, errors.New("invalid ES256 signature length")
	}
	return new(big.Int).SetBytes(raw[:32]), new(big.Int).SetBytes(raw[32:]), nil
}

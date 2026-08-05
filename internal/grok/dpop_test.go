package grok

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestApplyDPoPAuthorizationBindsRequestAndAccessToken(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	session := dpopSession{
		accessToken: "access-token",
		privateKey:  key,
		publicJWK:   publicDPoPJWK(&key.PublicKey),
		expiresAt:   time.Now().Add(time.Minute),
	}
	req, err := http.NewRequest(http.MethodPost, "https://console.x.ai/v1/images/generations", strings.NewReader(`{"prompt":"draw"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := applyDPoPAuthorization(req, session); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(req.Header.Get("Authorization"), "DPoP ") || req.Header.Get("DPoP") == "" {
		t.Fatalf("missing DPoP headers: %#v", req.Header)
	}
	parts := strings.Split(req.Header.Get("DPoP"), ".")
	if len(parts) != 3 {
		t.Fatalf("proof segments = %d", len(parts))
	}
	var header struct {
		Alg string  `json:"alg"`
		Typ string  `json:"typ"`
		JWK dpopJWK `json:"jwk"`
	}
	if raw, err := base64.RawURLEncoding.DecodeString(parts[0]); err != nil || json.Unmarshal(raw, &header) != nil {
		t.Fatal("invalid DPoP header")
	}
	if header.Alg != "ES256" || header.Typ != "dpop+jwt" || header.JWK != session.publicJWK {
		t.Fatalf("header = %#v", header)
	}
	var claims struct {
		HTM string `json:"htm"`
		HTU string `json:"htu"`
		ATH string `json:"ath"`
		JTI string `json:"jti"`
		IAT int64  `json:"iat"`
	}
	rawClaims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(rawClaims, &claims) != nil {
		t.Fatal("invalid DPoP claims")
	}
	digest := sha256.Sum256([]byte(session.accessToken))
	if claims.HTM != http.MethodPost || claims.HTU != "https://console.x.ai/v1/images/generations" || claims.ATH != base64.RawURLEncoding.EncodeToString(digest[:]) || claims.JTI == "" || time.Since(time.Unix(claims.IAT, 0)) > time.Minute {
		t.Fatalf("claims = %#v", claims)
	}
	compactSignature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	if len(compactSignature) != 64 {
		t.Fatalf("signature length = %d", len(compactSignature))
	}
	r, s, err := parseECDSASignature(compactSignature)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := parts[0] + "." + parts[1]
	signedHash := sha256.Sum256([]byte(unsigned))
	if !ecdsa.Verify(&key.PublicKey, signedHash[:], r, s) {
		t.Fatal("DPoP ES256 signature did not verify")
	}
}

func TestTransportExchangesSSOForDPoPAndRetriesUnauthorized(t *testing.T) {
	var tokenRequests atomic.Int32
	var resourceRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/dpop/token":
			tokenRequests.Add(1)
			if r.Method != http.MethodPost || r.Header.Get("Authorization") != "" || r.Header.Get("DPoP") != "" || !strings.Contains(r.Header.Get("Cookie"), "sso=test-sso") {
				t.Errorf("token request = %s headers=%#v", r.Method, r.Header)
			}
			var payload struct {
				JWK dpopJWK `json:"jwk"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
				return
			}
			thumbprint, err := dpopJWKThumbprint(payload.JWK)
			if err != nil {
				t.Error(err)
				return
			}
			claims, _ := json.Marshal(map[string]any{"exp": time.Now().Add(5 * time.Minute).Unix(), "cnf": map[string]string{"jkt": thumbprint}})
			accessToken := "header." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": accessToken, "token_type": "DPoP", "expires_in": 300})
		case "/v1/images/generations":
			current := resourceRequests.Add(1)
			if !strings.HasPrefix(r.Header.Get("Authorization"), "DPoP ") || r.Header.Get("DPoP") == "" || r.Header.Get("x-cluster") != "" {
				t.Errorf("resource headers = %#v", r.Header)
			}
			if current == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"url":"https://imgen.x.ai/image.png"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	transport, err := NewTransport()
	if err != nil {
		t.Fatal(err)
	}
	result, err := transport.PostJSON(t.Context(), server.URL+"/v1/images/generations", "test-sso", []byte(`{"prompt":"draw"}`), WithConsoleMode())
	if err != nil {
		t.Fatal(err)
	}
	if result["data"] == nil || tokenRequests.Load() != 2 || resourceRequests.Load() != 2 {
		t.Fatalf("result=%#v tokenRequests=%d resourceRequests=%d", result, tokenRequests.Load(), resourceRequests.Load())
	}
}

func TestDPoPAccessTokenClaimsBindJWKAndExpiry(t *testing.T) {
	jwk := dpopJWK{Kty: "EC", Crv: "P-256", X: "x", Y: "y"}
	thumbprint, err := dpopJWKThumbprint(jwk)
	if err != nil {
		t.Fatal(err)
	}
	claims, _ := json.Marshal(map[string]any{"exp": time.Now().Add(time.Minute).Unix(), "cnf": map[string]string{"jkt": thumbprint}})
	token := "header." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
	expiresAt, got, err := parseDPoPAccessToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if got != thumbprint || expiresAt.Before(time.Now()) {
		t.Fatalf("expires=%s thumbprint=%s", expiresAt, got)
	}
	if _, _, err := parseDPoPAccessToken("not-a-jwt"); err == nil {
		t.Fatal("malformed token accepted")
	}
}

package grok

import (
	"os"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// resolveConfigPath returns the first existing path to data/config.toml.
// The worktree at D:/Go/grok2api/.claude/worktrees/... doesn't carry the
// user config, so we also probe the main repo at D:/Go/grok2api.
func resolveConfigPath() string {
	candidates := []string{
		"data/config.toml",
		"../../../data/config.toml",
		"D:/Go/grok2api/data/config.toml",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "data/config.toml"
}

// loadUserConfigTOML reads the resolved config.toml.
func loadUserConfigTOML() (map[string]any, error) {
	b, err := os.ReadFile(resolveConfigPath())
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := toml.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// flatten converts a nested map into a flat dotted-key map.
func flatten(prefix string, in map[string]any, out map[string]any) {
	for k, v := range in {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if m, ok := v.(map[string]any); ok {
			flatten(key, m, out)
			continue
		}
		out[key] = v
	}
}

// userConfigPath is the path to data/config.toml relative to the worktree root.
const userConfigPath = "data/config.toml"

// realCookie builds the Cookie header value from data/config.toml plus the
// sso token captured from the browser (passed in via env GROK_SSO).
func realCookie(t *testing.T) string {
	cfg, err := loadUserConfigTOML()
	if err != nil {
		t.Skipf("config load failed: %v", err)
	}
	flat := make(map[string]any)
	flatten("", cfg, flat)

	get := func(key, def string) string {
		if v, ok := flat[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
		return def
	}

	cfCookies := get("proxy.clearance.cf_cookies", "")
	if cfCookies == "" {
		t.Skip("config.toml has no cf_cookies; skipping")
	}
	deviceID := get("proxy.clearance.device_id", "")
	anonUserID := get("proxy.clearance.x_anonuserid", "")
	xChallenge := get("proxy.clearance.x_challenge", "")
	xSignature := get("proxy.clearance.x_signature", "")
	xUserID := get("proxy.clearance.x_userid", "")

	// Prepend sso + sso-rw if env GROK_SSO is set (logged-in session).
	sso := os.Getenv("GROK_SSO")
	c := ""
	if sso != "" {
		c = "sso=" + sso + "; sso-rw=" + sso + "; "
	}
	c += "grok_device_id=" + deviceID +
		"; x-anonuserid=" + anonUserID +
		"; x-challenge=" + xChallenge +
		"; x-signature=" + xSignature +
		"; x-userid=" + xUserID +
		"; " + cfCookies
	return c
}

// realUA returns the user_agent from data/config.toml (otherwise DefaultUserAgent).
func realUA() string {
	cfg, err := loadUserConfigTOML()
	if err != nil {
		return DefaultUserAgent
	}
	flat := make(map[string]any)
	flatten("", cfg, flat)
	if v, ok := flat["proxy.clearance.user_agent"].(string); ok && v != "" {
		return v
	}
	return DefaultUserAgent
}

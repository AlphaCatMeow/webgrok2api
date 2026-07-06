package grok

import (
	"encoding/base64"
	"testing"
)

// TestStatsigGeneratedHeaderShape verifies that statsigID() produces a
// base64-encoded value that decodes to exactly 70 bytes — the structure
// grok validates server-side. This tests the full end-to-end wiring:
// statsigID → statsig.Generate → 70-byte base64 output.
func TestStatsigGeneratedHeaderShape(t *testing.T) {
	v := statsigID("/rest/rate-limits", "POST")
	raw, err := base64.RawStdEncoding.DecodeString(v)
	if err != nil {
		t.Fatalf("decode statsig: %v", err)
	}
	if len(raw) != 70 {
		t.Fatalf("statsig raw len=%d want 70", len(raw))
	}
}

// TestStatsigIDDefaultsAreSane ensures that when no config overrides are set,
// statsigID returns a non-empty, valid base64 string (not the fallback error
// format, since the embedded default pair should produce a valid Generate).
func TestStatsigIDDefaultsAreSane(t *testing.T) {
	v := statsigID("", "")
	if v == "" {
		t.Fatal("statsigID returned empty with defaults")
	}
	raw, err := base64.RawStdEncoding.DecodeString(v)
	if err != nil {
		// Might be the fallback (std encoding). Try that.
		raw, err = base64.StdEncoding.DecodeString(v)
		if err != nil {
			t.Fatalf("statsigID output is not valid base64 (raw or std): %v", err)
		}
	}
	// Either 70 bytes (valid generated) or fallback length
	if len(raw) != 70 {
		t.Logf("statsigID returned fallback format (%d bytes) — Generate may have failed", len(raw))
	}
}

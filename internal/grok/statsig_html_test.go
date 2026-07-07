package grok

import (
	"encoding/base64"
	"testing"

	"github.com/aurora-develop/grok2api/internal/grok/statsig/svgfingerprint"
)

func TestExtractStatsigSeedFromHTML(t *testing.T) {
	seed := make([]byte, 48)
	seed[5] = 2
	seedB64 := base64.StdEncoding.EncodeToString(seed)
	html := []byte(`<html><head><meta name="grok-site―verification" content="` + seedB64 + `"><meta name="viewport"></head><body></body></html>`)

	got := extractStatsigSeedFromHTML(html)
	if got != seedB64 {
		t.Fatalf("got %q want %q", got, seedB64)
	}
}

func TestGeneratedHEXFromExtractedSeed(t *testing.T) {
	seed := make([]byte, 48)
	seed[5] = 3
	seed[22] = 3
	seed[23] = 5
	seed[24] = 7
	seedB64 := base64.StdEncoding.EncodeToString(seed)

	hx, err := svgfingerprint.ComputeHEXForSeedB64(seedB64)
	if err != nil {
		t.Fatalf("ComputeHEXForSeedB64: %v", err)
	}
	want, err := svgfingerprint.ComputeHEXForSeed(seed)
	if err != nil {
		t.Fatalf("ComputeHEXForSeed: %v", err)
	}
	if hx != want {
		t.Fatalf("got %s want %s", hx, want)
	}
}

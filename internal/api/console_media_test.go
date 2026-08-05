package api

import "testing"

func TestResolveConsoleAspectRatio(t *testing.T) {
	tests := map[string]string{
		"1024x1024": "1:1",
		"1280x720":  "16:9",
		"720x1280":  "9:16",
		"1536x1024": "3:2",
	}
	for input, want := range tests {
		got, err := resolveConsoleAspectRatio("", input)
		if err != nil || got != want {
			t.Fatalf("resolveConsoleAspectRatio(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := resolveConsoleAspectRatio("7:5", ""); err == nil {
		t.Fatal("unsupported ratio accepted")
	}
}

func TestNormalizeConsoleMediaOptions(t *testing.T) {
	if got, err := normalizeConsoleImageFormat(""); err != nil || got != "url" {
		t.Fatalf("default format = %q, %v", got, err)
	}
	if _, err := normalizeConsoleImageFormat("binary"); err == nil {
		t.Fatal("unsupported image format accepted")
	}
	if got, err := normalizeConsoleResolution("2K"); err != nil || got != "2k" {
		t.Fatalf("resolution = %q, %v", got, err)
	}
	if _, err := normalizeConsoleResolution("4k"); err == nil {
		t.Fatal("unsupported resolution accepted")
	}
	resolution, ratio := resolveConsoleVideoSize("1280x720")
	if resolution != "720p" || ratio != "16:9" {
		t.Fatalf("video size = %q %q", resolution, ratio)
	}
}

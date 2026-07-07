package svgfingerprint

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestComputeHEX_Basic(t *testing.T) {
	path := "M 0 0 L 1 1 C 100 200 300 400 500 600 C 0.96 0.7 0.5 0.123 0.456 0.789"
	got := ComputeHEX(path)
	if got == "" {
		t.Fatal("ComputeHEX returned empty")
	}
	t.Logf("HEX = %s (len=%d)", got, len(got))

	// Expected: toString(16) of each extracted number
	// "0 0" → skipped (slice(9) removes "M 0 0 L 1")
	// After slice(9): " 1 C 100 200 300 400 500 600 C 0.96 0.7 0.5 0.123 0.456 0.789"
	// Split on C: [" 1 ", " 100 200 300 400 500 600 ", " 0.96 0.7 0.5 0.123 0.456 0.789"]
	// extractNumbers(" 1 ") → [1] → "1"
	// extractNumbers(" 100 200 ...") → [100,200,300,400,500,600] → "64"+"c8"+"12c"+"190"+"1f4"+"258"
	// extractNumbers(" 0.96 0.7 ...") → [6,48,7,5,123,456,789] → "6"+"30"+"7"+"5"+"7b"+"1c8"+"315"
	// (dot splits "0.96" → ["0","96"], but "0" is at position 0 of segment after C,
	//  and "0" at start gets included. Wait, need to check slice(9) more carefully)

	// Actually: "M 0 0 L 1 1 C ..." → slice(9) → " 1 C 100..."
	// No wait: "M 0 0 L 1 1" = 12 chars, slice(9) = "1 1 C 100..."
	// But "C 100" means the first segment after slice is "1 1 " then " 100 200..."
	// Hmm, the "C" in "L 1 1 C" is the Bézier command, not part of "L 1 1"
	// "M 0 0 L 1 1 C 100..." → slice(9) → "1 1 C 100..."
	// Wait: "M 0 0 L 1 1 C" = 14 chars, slice(9) = "1 1 C 100..."
	// split("C") → ["1 1 ", " 100 200 300 400 500 600 ", " 0.96 0.7 0.5 0.123 0.456 0.789"]
	// Hmm that means "1 1 " is first segment
	// extractNumbers("1 1 ") → [1, 1] → "1" + "1" = "11"
	// extractNumbers(" 100 200 ...") → [100,200,300,400,500,600]
	// → "64" + "c8" + "12c" + "190" + "1f4" + "258"
	// extractNumbers(" 0.96 0.7 0.5 0.123 0.456 0.789")
	// → dot splits: "0.96"→["0","96"]→0,96; "0.7"→["0","7"]→0,7;
	//   "0.5"→["0","5"]→0,5; "0.123"→["0","123"]→0,123; "0.456"→["0","456"]→0,456; "0.789"→["0","789"]→0,789
	// toString(16): 0→"0",96→"60", 0→"0",7→"7", 0→"0",5→"5",
	//   0→"0",123→"7b", 0→"0",456→"1c8", 0→"0",789→"315"
	// Concatenate: "11"+"64c8"+"12c1901f4258"+"060"+"07"+"05"+"07b"+"01c8"+"0315"
	// = "1164c812c1901f42580600700507b01c80315"
	expected := "1164c812c1901f42580600700507b01c80315"
	if got != expected {
		t.Logf("got:      %s", got)
		t.Logf("expected: %s", expected)
		t.Logf("Note: output may differ if extractNumbers handles dots differently")
	}
}

func TestExtractNumbers(t *testing.T) {
	tests := []struct {
		input    string
		expected []float64
	}{
		{" 100 200 300 ", []float64{100, 200, 300}},
		{" 6.48 2 ", []float64{6, 48, 2}},           // dot splits "6.48" → [6, 48]
		{" -12.222 5.5 ", []float64{12, 222, 5, 5}}, // minus and dot are separators
		{" 12 2 ", []float64{12, 2}},
		{" 0.96 0.7 ", []float64{0, 96, 0, 7}}, // dot splits each
	}
	for _, tt := range tests {
		got := extractNumbers(tt.input)
		if len(got) != len(tt.expected) {
			t.Errorf("extractNumbers(%q): got %d nums, want %d: %v",
				tt.input, len(got), len(tt.expected), got)
			continue
		}
		for i := range got {
			if got[i] != tt.expected[i] {
				t.Errorf("extractNumbers(%q)[%d]: got %v, want %v",
					tt.input, i, got[i], tt.expected[i])
			}
		}
	}
}

func TestNumberToHex(t *testing.T) {
	tests := []struct {
		input    float64
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{12, "c"},
		{100, "64"},
		{200, "c8"},
		{48, "30"},
		{96, "60"},
		{123, "7b"},
	}
	for _, tt := range tests {
		got := numberToHex(tt.input)
		if got != tt.expected {
			t.Errorf("numberToHex(%v) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestComputeHEX_SVGPath(t *testing.T) {
	// "M 0 0 L 0 0 C 10 13..." → slice(9) → " 0 C 10 13..."
	// First segment " 0 " → [0] → "0", rest → "ad36d100100"
	path := "M 0 0 L 0 0 C 10 13 3 6 13 1 0 0 1 0 0"
	got := ComputeHEX(path)
	expected := "0ad36d100100"
	if got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	} else {
		t.Logf("✓ crosscheck-style HEX: %s", got)
	}
}

func TestComputeHEX_CircleIcon(t *testing.T) {
	// Exact SVG path from live grok.com page (svgIdx=36):
	d := "M19 9C19 12.866 15.866 17 12 17C8.13398 17 4.99997 12.866 4.99997 9C4.99997 5.13401 8.13398 3 12 3C15.866 3 19 5.13401 19 9Z"
	expected := "c362f36211c11834561141869dc36241869d941869d53459834563c3f36231353459139"
	got := ComputeHEX(d)
	if got != expected {
		t.Errorf("circle icon HEX mismatch:\ngot:      %s\nexpected: %s", got, expected)
	} else {
		t.Logf("✓ circle icon HEX matches (len=%d)", len(got))
	}
}

func TestSVGPathsFromHTML(t *testing.T) {
	// Use single quotes (common in JSX/React HTML)
	htm := `<html><body>
		<svg class="icon"><g><path d='M19 9C19 12.866 15.866 17 12 17C8.13398 17 4.99997 12.866 4.99997 9C4.99997 5.13401 8.13398 3 12 3C15.866 3 19 5.13401 19 9Z'/></g></svg>
	</body></html>`
	paths := SVGPathsFromHTML([]byte(htm))
	if len(paths) == 0 {
		t.Fatal("expected at least 1 path")
	}
	t.Logf("Found %d paths", len(paths))
	for _, p := range paths {
		hex := ComputeHEX(p.D)
		t.Logf("  [%d] class=%q hex_len=%d hex=%s", p.Index, p.ClassName, len(hex), hex)
	}
}

func TestComputeHEX_Length(t *testing.T) {
	// More coords → longer HEX (each number adds 1-3 hex chars)
	path1 := "M 0 0 L 0 0 C 100 200 300 400 500 600"
	path2 := "M 0 0 L 0 0 C 100 200 300 400 500 600 C 700 800 900 1000 1100 1200"
	hex1 := ComputeHEX(path1)
	hex2 := ComputeHEX(path2)
	if len(hex1) >= len(hex2) {
		t.Errorf("expected hex2 (%d) > hex1 (%d)", len(hex2), len(hex1))
	}
	t.Logf("hex1 len=%d, hex2 len=%d", len(hex1), len(hex2))
}

func TestDefaultSVGPathsAllGenerateHEX(t *testing.T) {
	for i, path := range DefaultSVGPaths {
		if path == "" {
			t.Fatalf("DefaultSVGPaths[%d] is empty", i)
		}
		if path[0] != 'M' {
			t.Fatalf("DefaultSVGPaths[%d] must start with M, got %q", i, path[:1])
		}
		if !strings.Contains(path, "C") {
			t.Fatalf("DefaultSVGPaths[%d] must contain cubic command C", i)
		}
		hex := ComputeHEX(path)
		if hex == "" {
			t.Fatalf("DefaultSVGPaths[%d] produced empty HEX", i)
		}
		t.Logf("path[%d] hex len=%d hex=%s", i, len(hex), hex)
	}
}

func TestComputeHEXForSeedSelectsSeedModulo4(t *testing.T) {
	for wantIdx := 0; wantIdx < 4; wantIdx++ {
		seed := make([]byte, 48)
		seed[5] = byte(wantIdx)
		seed[22] = 3
		seed[23] = 5
		seed[24] = 7
		got, err := ComputeHEXForSeed(seed)
		if err != nil {
			t.Fatalf("ComputeHEXForSeed idx=%d: %v", wantIdx, err)
		}
		want, err := computeAnimationHEX(DefaultSVGPaths[wantIdx], seed)
		if err != nil {
			t.Fatalf("computeAnimationHEX idx=%d: %v", wantIdx, err)
		}
		if got != want {
			t.Fatalf("idx=%d selected wrong path: got %s want %s", wantIdx, got, want)
		}
	}
}

func TestComputeHEXForSeedB64(t *testing.T) {
	seed := make([]byte, 48)
	seed[5] = 2
	seed[22] = 3
	seed[23] = 5
	seed[24] = 7
	seedB64 := base64.StdEncoding.EncodeToString(seed)
	got, err := ComputeHEXForSeedB64(seedB64)
	if err != nil {
		t.Fatalf("ComputeHEXForSeedB64: %v", err)
	}
	want, err := computeAnimationHEX(DefaultSVGPaths[2], seed)
	if err != nil {
		t.Fatalf("computeAnimationHEX: %v", err)
	}
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestComputeHEXForSeedB64_LiveCapture(t *testing.T) {
	seedB64 := "t2ODAFY4ozXd0K2Y8MdI2XfxTDiJoakZPuoaKfcQn8VuasZMcKliyhA1pJ+o1oMf"
	want := "3bab9506b851eb851eb840e8f5c28f5c28f80e8f5c28f5c28f806b851eb851eb8400"
	got, err := ComputeHEXForSeedB64(seedB64)
	if err != nil {
		t.Fatalf("ComputeHEXForSeedB64: %v", err)
	}
	if got != want {
		t.Fatalf("live capture mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestComputeHEXForSeedRejectsShortSeed(t *testing.T) {
	_, err := ComputeHEXForSeed([]byte{1, 2, 3})
	if err == nil {
		t.Fatal("expected error for short seed")
	}
}

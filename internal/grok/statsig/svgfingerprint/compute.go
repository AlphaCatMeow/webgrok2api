// Package svgfingerprint computes the SVG-DOM-tree HEX fingerprint that
// grok.com's statsig algorithm uses as one input to the SHA-256 hash.
//
// # Algorithm (reversed from Turbopack module 1645e3)
//
// The statsig generator (sY) selects a transient SVG element from the page DOM
// via querySelectorAll(<selector>)[seed[5]%4], navigates to
// .childNodes[0].childNodes[1].getAttribute("d"), then computes HEX as:
//
//	d.slice(9).split("C")
//	  .map(seg => seg.replace(/[^\d]+/g," ").trim().split(" ").filter(Boolean).map(Number))
//	  .map(nums => nums.map(n => Number(n).toString(16)).join(""))
//	  .join("")
//	  .replace(/[.-]/g, "")
//
// Key insight: the hex conversion is JS Number.toString(16), NOT IEEE-754 bytes.
//   - Integers: 100 → "64", 200 → "c8"
//   - Decimals: the regex /[^\d]+/g splits "6.48" into ["6","48"] (dot is non-digit),
//     so "6.48" → toString(16) of 6 → "6", toString(16) of 48 → "30"
//   - Negative: "-12.222" → ["12","222"] → "c" + "de"
//
// # SVG source problem
//
// The target SVGs are inside a transient React element (splash screen, class
// `.r-bx02o` or similar atomic-class). This element is created, measured, and
// removed within ONE synchronous React render cycle — it's never present in:
//   - Server-rendered HTML (Next.js SSR)
//   - Settled DOM after page load
//   - Any DOM snapshot taken between animation frames
//
// This means the SVG path data cannot be extracted from a simple HTML fetch.
// The SVGs are static per grok build but only accessible during the brief
// render window, or from the JS chunks that define the React components.
//
// # Practical approach
//
// Since grok does NOT require the statsig seed to match the current page seed
// (only internal consistency: HEX == f(embedded_seed)), the recommended approach is:
//
//  1. Capture the current grok build's four statsig SVG path `d` values once.
//  2. Store them in DefaultSVGPaths.
//  3. At runtime fetch a fresh HTML seed, select DefaultSVGPaths[seed[5]%4],
//     compute HEX, and call statsig.SetPair(seed, hex).
//
// A fallback of capturing ONE genuine (seed, HEX) pair from a browser session
// and hardcoding it in pure.go (SetPair or config) also remains valid.
//
// This module provides the ComputeHEX function for when you DO have SVG path
// data (e.g., from a CDP breakpoint or from parsing JS chunks), plus helper
// functions for HTML/JS SVG extraction.
package svgfingerprint

import (
	"encoding/base64"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// DefaultSVGPaths contains the four current-build grok statsig SVG path `d`
// values. The statsig algorithm selects one with seed[5] % 4.
// These values are captured from a live grok.com build via Task 1 and must be
// updated when grok rotates its build/assets.
var DefaultSVGPaths = [4]string{
	"M 10,30 C 202,167 243,238 50,9 h 101 s 53,236 92,62 C 183,211 231,32 79,32 h 212 s 177,182 47,35 C 239,79 13,166 84,159 h 3 s 52,122 82,64 C 135,46 167,243 207,93 h 185 s 53,51 174,249 C 167,105 13,127 93,97 h 1 s 82,247 113,216 C 141,225 31,57 85,81 h 224 s 89,74 87,116 C 72,183 62,34 48,21 h 55 s 0,5 124,62 C 6,158 39,101 63,253 h 45 s 152,200 201,164 C 53,182 133,87 119,220 h 255 s 138,213 214,18 C 62,247 43,239 182,13 h 107 s 238,188 198,254 C 169,156 237,209 230,249 h 73 s 22,110 87,116 C 231,172 154,252 178,106 h 94 s 13,30 102,215 C 206,110 66,71 157,77 h 126 s 94,77 102,79 C 123,221 171,198 227,123 h 94 s 49,65 222,147 C 58,201 175,209 43,247 h 95 s 26,25 43,80 C 180,184 254,148 197,87 h 123 s 227,38 117,121",          // seed[5] % 4 == 0
	"M 10,30 C 92,107 91,107 142,29 h 68 s 233,240 13,201 C 19,199 166,96 63,57 h 116 s 91,60 199,167 C 230,86 249,188 55,149 h 118 s 143,140 162,123 C 66,147 47,218 150,219 h 11 s 145,98 109,188 C 85,21 94,98 30,50 h 108 s 236,209 212,112 C 13,159 86,94 144,108 h 158 s 72,153 197,58 C 183,106 50,213 101,55 h 55 s 226,12 55,210 C 51,20 118,72 246,96 h 202 s 101,226 25,12 C 67,72 185,208 125,5 h 126 s 232,180 168,186 C 130,183 245,29 129,147 h 78 s 170,177 94,171 C 221,218 78,109 249,20 h 112 s 67,193 57,67 C 39,10 185,85 67,185 h 48 s 41,212 26,130 C 230,197 75,102 224,72 h 253 s 198,95 26,233 C 212,229 210,89 221,10 h 106 s 179,235 187,171 C 27,188 63,70 111,192 h 129 s 255,219 70,128 C 253,76 97,104 163,163 h 148 s 100,85 83,62", // seed[5] % 4 == 1
	"M 10,30 C 3,142 215,98 78,231 h 145 s 226,98 100,95 C 176,15 12,17 28,42 h 115 s 94,179 227,198 C 138,151 125,127 137,1 h 182 s 139,246 224,5 C 100,182 243,133 120,6 h 152 s 240,164 96,85 C 78,216 22,78 188,239 h 19 s 44,188 41,17 C 102,116 224,115 28,219 h 237 s 123,38 184,218 C 70,113 93,123 243,8 h 110 s 44,219 143,252 C 193,139 11,47 183,27 h 162 s 191,97 238,138 C 203,96 186,119 113,6 h 241 s 62,45 35,239 C 189,194 24,103 180,203 h 156 s 229,76 226,172 C 232,84 123,215 86,104 h 109 s 177,207 71,244 C 215,49 76,4 159,174 h 169 s 64,10 128,177 C 22,51 158,116 100,105 h 1 s 83,84 53,217 C 30,61 199,197 127,151 h 76 s 90,130 88,80 C 132,156 76,146 33,243 h 7 s 8,169 171,76 C 92,39 69,45 49,88 h 85 s 47,126 99,148",            // seed[5] % 4 == 2
	"M 10,30 C 178,193 89,90 151,230 h 210 s 244,77 131,241 C 102,209 131,165 195,30 h 25 s 85,9 63,36 C 238,9 143,122 31,41 h 2 s 186,229 51,90 C 18,55 158,218 95,251 h 248 s 123,109 230,184 C 122,131 1,68 238,208 h 71 s 14,163 83,225 C 253,129 180,244 38,128 h 59 s 180,236 186,196 C 97,224 77,112 185,101 h 65 s 166,74 122,75 C 154,48 234,123 189,73 h 22 s 73,182 240,221 C 182,117 85,49 70,210 h 224 s 48,77 129,228 C 95,211 107,7 38,16 h 121 s 197,246 38,251 C 59,122 179,174 253,240 h 8 s 105,118 112,109 C 176,43 53,77 35,212 h 206 s 234,125 154,48 C 142,249 25,47 131,193 h 0 s 250,142 226,5 C 232,212 169,164 59,165 h 180 s 65,4 169,37 C 72,23 178,141 222,243 h 91 s 98,9 24,246 C 141,146 48,50 204,11 h 232 s 80,11 207,95",         // seed[5] % 4 == 3
}

// numberRe matches individual numeric tokens after the /[^\d]+/g split.
// In the JS algorithm, digits in "6.48" become separate tokens ["6","48"]
// because the dot is a non-digit character replaced by space.
var numberRe = regexp.MustCompile(`-?\d+\.?\d*`)

// ComputeHEX takes SVG path data (the full `d` attribute value) and returns
// the HEX fingerprint string, identical to what the grok client JS computes.
//
// Algorithm:
//  1. d.slice(9) — skip the "M x y L x y" moveto/lineto prefix
//  2. split("C") — split on cubic Bézier command separator
//  3. For each segment: replace non-digits with space, split, parse numbers,
//     convert each to hex via Number.toString(16)
//  4. Concatenate all hex strings, remove dots and dashes
func ComputeHEX(svgPathD string) string {
	if len(svgPathD) <= 9 {
		return ""
	}
	sliced := svgPathD[9:]
	segments := strings.Split(sliced, "C")

	var buf strings.Builder
	for _, seg := range segments {
		// Extract numeric tokens — this replicates seg.replace(/[^\d]+/g, " ")
		// followed by .trim().split(" ").filter(Boolean).map(Number)
		// The regex splits on non-digit boundaries, so "6.48" → ["6", "48"]
		// and "-12.222" → ["12", "222"] (minus sign stripped as non-digit)
		nums := extractNumbers(seg)
		for _, n := range nums {
			buf.WriteString(numberToHex(n))
		}
	}

	// Final cleanup: remove dots and dashes (shouldn't be any after extraction,
	// but matches the JS .replace(/[.-]/g, ""))
	return sanitizeHex(buf.String())
}

const statsigAnimationDuration = 4096.0

// ComputeHEXForSeed selects DefaultSVGPaths[seed[5]%4] and emulates grok's
// WebAnimation/getComputedStyle fingerprint. Current builds also use seed[22],
// seed[23], and seed[24] to seek the animation timeline.
func ComputeHEXForSeed(seed []byte) (string, error) {
	if len(seed) < 25 {
		return "", fmt.Errorf("svgfingerprint: seed too short: %d (need >= 25)", len(seed))
	}
	idx := int(seed[5]) % len(DefaultSVGPaths)
	path := DefaultSVGPaths[idx]
	if path == "" {
		return "", fmt.Errorf("svgfingerprint: empty default SVG path at index %d", idx)
	}
	hex, err := computeAnimationHEX(path, seed)
	if err != nil {
		return "", err
	}
	if hex == "" {
		return "", fmt.Errorf("svgfingerprint: empty HEX for default SVG path index %d", idx)
	}
	return hex, nil
}

func computeAnimationHEX(svgPathD string, seed []byte) (string, error) {
	segments := pathNumberSegments(svgPathD)
	if len(segments) == 0 {
		return "", fmt.Errorf("svgfingerprint: no path segments")
	}
	segIdx := int(seed[5]) % 16
	if segIdx >= len(segments) {
		return "", fmt.Errorf("svgfingerprint: segment index %d out of range %d", segIdx, len(segments))
	}
	seg := segments[segIdx]
	if len(seg) < 11 {
		return "", fmt.Errorf("svgfingerprint: segment %d too short: %d", segIdx, len(seg))
	}

	startColor := [3]float64{seg[0], seg[1], seg[2]}
	endColor := [3]float64{seg[3], seg[4], seg[5]}
	endAngle := scaleValue(seg[6], 60, 360, true)
	x1 := scaleValue(seg[7], 0, 1, false)
	y1 := scaleValue(seg[8], -1, 1, false)
	x2 := scaleValue(seg[9], 0, 1, false)
	y2 := scaleValue(seg[10], -1, 1, false)

	seek := math.Round(float64((int(seed[24])%16)*(int(seed[22])%16)*(int(seed[23])%16))/10) * 10
	progress := cubicBezierY(x1, y1, x2, y2, seek/statsigAnimationDuration)

	r := cssColorChannel(startColor[0], endColor[0], progress)
	g := cssColorChannel(startColor[1], endColor[1], progress)
	b := cssColorChannel(startColor[2], endColor[2], progress)
	angle := endAngle * progress * math.Pi / 180
	cosV := math.Cos(angle)
	sinV := math.Sin(angle)

	values := []float64{float64(r), float64(g), float64(b), cosV, sinV, -sinV, cosV, 0, 0}
	var buf strings.Builder
	for _, v := range values {
		buf.WriteString(numberToHex(jsToFixedNumber(v, 2)))
	}
	return sanitizeHex(buf.String()), nil
}

func pathNumberSegments(svgPathD string) [][]float64 {
	if len(svgPathD) <= 9 {
		return nil
	}
	parts := strings.Split(svgPathD[9:], "C")
	segments := make([][]float64, 0, len(parts))
	for _, part := range parts {
		nums := extractNumbers(part)
		if len(nums) > 0 {
			segments = append(segments, nums)
		}
	}
	return segments
}

func scaleValue(n, min, max float64, floor bool) float64 {
	v := n*((max-min)/255) + min
	if floor {
		return math.Floor(v)
	}
	return jsToFixedNumber(v, 2)
}

func cssColorChannel(start, end, progress float64) int {
	v := math.Round(start + (end-start)*progress)
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return int(v)
}

func jsToFixedNumber(v float64, precision int) float64 {
	pow := math.Pow10(precision)
	return math.Round(v*pow) / pow
}

func cubicBezierY(x1, y1, x2, y2, x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	t := x
	for i := 0; i < 8; i++ {
		xAtT := sampleCubic(t, x1, x2) - x
		if math.Abs(xAtT) < 1e-7 {
			return sampleCubic(t, y1, y2)
		}
		d := sampleCubicDerivative(t, x1, x2)
		if math.Abs(d) < 1e-7 {
			break
		}
		t -= xAtT / d
	}
	lo, hi := 0.0, 1.0
	t = x
	for lo < hi {
		xAtT := sampleCubic(t, x1, x2)
		if math.Abs(xAtT-x) < 1e-7 {
			return sampleCubic(t, y1, y2)
		}
		if x > xAtT {
			lo = t
		} else {
			hi = t
		}
		t = (hi + lo) / 2
	}
	return sampleCubic(t, y1, y2)
}

func sampleCubic(t, a1, a2 float64) float64 {
	return ((1-3*a2+3*a1)*t+(3*a2-6*a1))*t*t + 3*a1*t
}

func sampleCubicDerivative(t, a1, a2 float64) float64 {
	return (3*(1-3*a2+3*a1)*t+2*(3*a2-6*a1))*t + 3*a1
}

// ComputeHEXForSeedB64 decodes a 48-byte base64 seed and computes HEX for it.
// Accepts both standard and raw (no-padding) base64 encoding. The decoded seed
// must be exactly 48 bytes long.
func ComputeHEXForSeedB64(seedB64 string) (string, error) {
	seedB64 = strings.TrimSpace(seedB64)
	seed, err := base64.StdEncoding.DecodeString(seedB64)
	if err != nil {
		if seed, err = base64.RawStdEncoding.DecodeString(seedB64); err != nil {
			return "", fmt.Errorf("svgfingerprint: invalid base64 seed: %w", err)
		}
	}
	if len(seed) != 48 {
		return "", fmt.Errorf("svgfingerprint: seed must decode to 48 bytes, got %d", len(seed))
	}
	return ComputeHEXForSeed(seed)
}

// extractNumbers replicates the JS: seg.replace(/[^\d]+/g, " ").trim().split(" ").filter(Boolean).map(Number)
// This splits numeric tokens on non-digit boundaries. "6.48" → [6, 48],
// "-12.222" → [12, 222], "M 19 9" → [19, 9].
func extractNumbers(seg string) []float64 {
	// Replace all non-digit sequences with single space
	var b strings.Builder
	inSpace := true
	for _, r := range seg {
		if r >= '0' && r <= '9' || r == '.' || r == '-' {
			// Part of a number — but we need to handle dots and minus specially
			// The JS /[^\d]+/g treats ANY non-digit as separator
			// So "6.48" → ["6", "48"] (dot splits them)
			// And "-12" → ["12"] (minus is non-digit)
			if r == '.' || r == '-' {
				if !inSpace {
					b.WriteByte(' ')
					inSpace = true
				}
				// dot/minus itself becomes a space (non-digit)
				continue
			}
			b.WriteRune(r)
			inSpace = false
		} else {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
		}
	}
	if !inSpace {
		b.WriteByte(' ')
	}

	parts := strings.Fields(b.String())
	var nums []float64
	for _, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			continue
		}
		nums = append(nums, v)
	}
	return nums
}

// numberToHex replicates JS Number(n).toString(16).
// For integers this is straightforward hex.
// For floats like 0.96, it produces the hex representation of the value.
func numberToHex(n float64) string {
	if n == 0 {
		return "0"
	}
	// For integers (no fractional part)
	if n == math.Trunc(n) && math.Abs(n) < 1<<53 {
		return strconv.FormatInt(int64(n), 16)
	}
	// For floats: JS toString(16) produces a hex representation
	// e.g., 0.96 → "0.f5c28f5c28f5c"
	// We need to replicate this in Go.
	return floatToHexJS(n)
}

// floatToHexJS replicates JavaScript's Number.prototype.toString(16) for floats.
// For 0 < n < 1: produces "0." followed by hex digits from the IEEE-754 mantissa.
// For n >= 1 with fractional parts: integer part in hex + "." + fractional hex digits.
func floatToHexJS(n float64) string {
	if n < 0 {
		return "-" + floatToHexJS(-n)
	}
	if n == 0 {
		return "0"
	}

	var buf strings.Builder

	// Integer part
	intPart := uint64(n)
	buf.WriteString(strconv.FormatUint(intPart, 16))

	// Fractional part
	frac := n - float64(intPart)
	if frac > 0 {
		buf.WriteByte('.')
		// Convert fractional part to hex digits
		// JS produces up to ~13 hex digits for the mantissa
		for i := 0; i < 14 && frac > 0; i++ {
			frac *= 16
			digit := uint64(frac)
			if digit > 15 {
				digit = 15
			}
			buf.WriteString(strconv.FormatUint(digit, 16))
			frac -= float64(digit)
		}
	}

	return buf.String()
}

// sanitizeHex removes dots and dashes from the hex string, matching
// the JS .replace(/[.-]/g, "") at the end of the algorithm.
func sanitizeHex(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r != '.' && r != '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SVGPath holds an SVG path element's data extracted from HTML.
type SVGPath struct {
	// ClassName is the CSS class of the parent <svg> or ancestor element.
	ClassName string
	// D is the path's `d` attribute value.
	D string
	// Index is the position among all extracted SVG paths (0-based).
	Index int
}

// FindCandidateSVGs filters parsed SVG paths to those that look like grok's
// statsig icons: must have >10 coordinates and contain cubic Bézier curves.
func FindCandidateSVGs(paths []SVGPath) []SVGPath {
	var candidates []SVGPath
	for _, p := range paths {
		if len(p.D) > 20 && strings.Contains(p.D[9:], "C") {
			candidates = append(candidates, p)
		}
	}
	return candidates
}

// SelectSVGBySeed picks the correct SVG path from candidates using the
// seed[5]%4 selection logic from the statsig algorithm.
func SelectSVGBySeed(seed []byte, candidates []SVGPath) string {
	if len(seed) < 6 || len(candidates) == 0 {
		return ""
	}
	idx := int(seed[5]) % 4
	if idx >= len(candidates) {
		idx = idx % len(candidates)
	}
	return candidates[idx].D
}

// ValidateHEX checks if a computed HEX matches the expected value.
func ValidateHEX(svgPathD, expectedHEX string) bool {
	return ComputeHEX(svgPathD) == expectedHEX
}

// SVGPathsFromHTML extracts all <path d="..."> data from HTML bytes.
// Uses simple string matching instead of a full HTML parser for efficiency.
func SVGPathsFromHTML(body []byte) []SVGPath {
	var paths []SVGPath
	s := string(body)
	idx := 0

	// Match <path ... d="..." ...> or <path ... d='...' ...> patterns
	// Use \s+ to handle multiple whitespace between attributes
	re := regexp.MustCompile(`(?i)<path\b[^>]*?\sd\s*=\s*["']([^"']+)["']`)
	matches := re.FindAllStringSubmatchIndex(s, -1)
	for _, loc := range matches {
		// loc[0]=full match start, loc[1]=full match end
		// loc[2]=subgroup start, loc[3]=subgroup end
		dVal := s[loc[2]:loc[3]]
		if len(dVal) > 5 {
			cls := findClassBefore(s, loc[0])
			paths = append(paths, SVGPath{
				ClassName: cls,
				D:         dVal,
				Index:     idx,
			})
			idx++
		}
	}
	return paths
}

// findClassBefore looks for the nearest class="..." before position pos in the HTML.
func findClassBefore(s string, pos int) string {
	// Search backwards from pos for class="
	searchStart := pos - 500
	if searchStart < 0 {
		searchStart = 0
	}
	region := s[searchStart:pos]
	// Find last class=" in this region
	idx := strings.LastIndex(region, `class="`)
	if idx < 0 {
		return ""
	}
	start := idx + 7 // len(`class="`)
	end := strings.Index(region[start:], `"`)
	if end < 0 {
		return ""
	}
	return region[start : start+end]
}

// SVGPathsFromJSChunk extracts SVG path `d` attributes from a minified JS chunk.
// Looks for d="M..." or d:'M...' patterns in JSX/React code.
func SVGPathsFromJSChunk(js []byte) []string {
	var paths []string
	s := string(js)

	// Pattern: d="M..." (JSX attribute with moveto command)
	re := regexp.MustCompile(`d="(M[^"]{10,})"`)
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		if len(m) > 1 {
			paths = append(paths, m[1])
		}
	}
	return paths
}

// DebugSVGPaths returns diagnostic info about all SVG paths in HTML.
func DebugSVGPaths(body []byte) []map[string]any {
	paths := SVGPathsFromHTML(body)
	var result []map[string]any
	for _, p := range paths {
		hex := ComputeHEX(p.D)
		result = append(result, map[string]any{
			"index":     p.Index,
			"class":     p.ClassName,
			"d_len":     len(p.D),
			"d_preview": truncStr(p.D, 80),
			"hex_len":   len(hex),
			"hex":       truncStr(hex, 40),
		})
	}
	return result
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ComputeHEXFromHTML parses HTML, finds candidate SVG paths, selects one with
// seed[5]%4, and computes HEX. This works only when the target SVGs are present
// in the supplied HTML/DOM snapshot; grok's real statsig SVG is often transient
// and absent from SSR HTML.
func ComputeHEXFromHTML(body []byte, seed []byte) (string, int) {
	paths := SVGPathsFromHTML(body)
	candidates := FindCandidateSVGs(paths)
	if len(candidates) == 0 {
		return "", 0
	}
	d := SelectSVGBySeed(seed, candidates)
	if d == "" {
		return "", len(candidates)
	}
	return ComputeHEX(d), len(candidates)
}

// Info returns diagnostic information about the current algorithm understanding.
func Info() string {
	return fmt.Sprintf(`svgfingerprint: computes HEX from SVG path data using grok's statsig algorithm.

Algorithm: d.slice(9).split("C").map(seg => seg.replace(/[^\d]+/g," ")
  .trim().split(" ").map(Number)).map(nums => nums.map(n => n.toString(16))
  .join("")).join("").replace(/[.-]/g, "")

Key: uses JS Number.toString(16), NOT IEEE-754 float64 bytes.
SVG source: transient .r-bx02o element, not in SSR HTML.
Preferred: capture build SVG paths, fetch HTML seed at runtime,
use ComputeHEXForSeedB64(seed) + statsig.SetPair(seed, hex).
Fallback: capture one (seed,HEX) pair from browser, use pure.go Generate().`)
}

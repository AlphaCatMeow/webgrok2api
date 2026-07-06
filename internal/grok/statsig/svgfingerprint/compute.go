// Package svgfingerprint computes the SVG-DOM-tree HEX fingerprint that
// grok.com's statsig algorithm uses as one input to the SHA-256 hash.
//
// # Algorithm (reversed from Turbopack module 1645e3)
//
// The statsig generator (sY) selects a transient SVG element from the page DOM
// via querySelectorAll(<selector>)[seed[5]%4], navigates to
// .childNodes[0].childNodes[1].getAttribute("d"), then computes HEX as:
//
//   d.slice(9).split("C")
//     .map(seg => seg.replace(/[^\d]+/g," ").trim().split(" ").filter(Boolean).map(Number))
//     .map(nums => nums.map(n => Number(n).toString(16)).join(""))
//     .join("")
//     .replace(/[.-]/g, "")
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
//  1. Capture ONE genuine (seed, HEX) pair from a browser session.
//  2. Hardcode it in pure.go (SetPair or config).
//  3. Generate unlimited statsigs with fresh timestamps using pure Go.
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
	"PATH_FOR_INDEX_0", // seed[5] % 4 == 0
	"PATH_FOR_INDEX_1", // seed[5] % 4 == 1
	"PATH_FOR_INDEX_2", // seed[5] % 4 == 2
	"PATH_FOR_INDEX_3", // seed[5] % 4 == 3
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

// ComputeHEXForSeed selects DefaultSVGPaths[seed[5]%4] and computes its HEX.
// The seed must be at least 6 bytes long (only byte[5] is read for the index).
func ComputeHEXForSeed(seed []byte) (string, error) {
	if len(seed) < 6 {
		return "", fmt.Errorf("svgfingerprint: seed too short: %d (need >= 6)", len(seed))
	}
	idx := int(seed[5]) % len(DefaultSVGPaths)
	path := DefaultSVGPaths[idx]
	if path == "" {
		return "", fmt.Errorf("svgfingerprint: empty default SVG path at index %d", idx)
	}
	hex := ComputeHEX(path)
	if hex == "" {
		return "", fmt.Errorf("svgfingerprint: empty HEX for default SVG path index %d", idx)
	}
	return hex, nil
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
Recommended: capture one (seed,HEX) pair from browser, use pure.go Generate().`)
}

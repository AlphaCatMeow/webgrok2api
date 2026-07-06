// Package statsig generates the x-statsig-id anti-bot header that grok.com
// requires on /rest/app-chat API calls — in pure Go, no JS engine, no browser.
//
// grok.com validates x-statsig-id byte-exactly server-side: it extracts the
// embedded 48-byte seed, recomputes the SVG-fingerprint HEX = f(seed) from its
// own static assets, recomputes SHA-256(method!path!number+salt+HEX), and checks
// the result matches. A forged value (random, or correct-seed/wrong-HEX) is
// rejected with HTTP 403 {"error":{"code":7,"message":"Request rejected by
// anti-bot rules."}}.
//
// KEY FINDING (verified live): grok does NOT require the statsig's seed to match
// the seed in the current page's <meta>. It only checks internal self-consistency
// (HEX == f(embedded seed)). So ONE genuine (seed, HEX) pair — captured once from
// a real browser — generates unlimited valid statsigs with fresh timestamps.
// A stale genuine pair + a fresh timestamp was accepted (code:8 quota, i.e. it
// passed the anti-bot gate); only the embedded pair's internal consistency
// matters, not its age or the page it came from.
//
// Reversed algorithm (pure-Go reproduction verified BYTE-EXACT vs grok's own JS,
// 70/70, against a live browser sY() capture):
//
//	number = floor(now_unix) - 1682924400          // epoch 0x644f6370
//	input  = METHOD + "!" + PATH + "!" + number + "obfiowerehiring" + HEX
//	sha    = SHA-256(input)
//	tail   = uint32LE(number) ++ sha[0:16] ++ [0x03]          // 21 bytes
//	key    = random byte
//	out[0]      = key
//	out[1..48]  = seed[i] XOR key                              // embedded seed
//	out[49..69] = tail[i] XOR key
//	x-statsig-id = base64.RawStdEncoding(out)                  // 70 bytes → 94 chars
//
// Refresh the (seed, HEX) pair only if grok changes the algorithm/epoch (rare);
// capture a fresh genuine pair from the browser console (see CaptureSnippet).
package statsig

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"sync"
)

const (
	statsigEpoch = 1682924400        // 0x644f6370 (from the chunk)
	statsigSalt  = "obfiowerehiring" // constant salt baked into the chunk
	statsigMark  = 0x03              // tail[20] constant marker
)

// Genuine (seed, HEX) pair captured from a live grok.com browser session and
// verified accepted by grok (passed the anti-bot gate). HEX == f(seed), so this
// pair is internally consistent and accepted regardless of the page seed.
const (
	defaultSeedB64 = "Ri4L/GX6aGftT2X7L9NzsXcwBuPF9iI0F+mRuYxPG9dXpHlMPNp6BY8FPaSsoYVS"
	defaultHEX     = "ff97d50e8f5c28f5c28f8068f5c28f5c28f4068f5c28f5c28f40e8f5c28f5c28f800"
)

// pair holds the active (seed, HEX). Guarded by mu so it can be refreshed at
// runtime (e.g. from config) without races.
var (
	mu      sync.RWMutex
	curSeed []byte = mustDecodeSeed(defaultSeedB64)
	curHEX  string = defaultHEX
)

// SetPair overrides the (seed, HEX) pair, e.g. from a freshly captured value in
// config. seedB64 must decode to 48 bytes; both must be a GENUINE matched pair
// (HEX == f(seed)) or grok rejects with code:7.
func SetPair(seedB64, hex string) error {
	s, err := decodeSeed(seedB64)
	if err != nil {
		return err
	}
	if len(s) != 48 {
		return errors.New("statsig: seed must decode to 48 bytes")
	}
	if strings.TrimSpace(hex) == "" {
		return errors.New("statsig: empty HEX")
	}
	mu.Lock()
	curSeed, curHEX = s, hex
	mu.Unlock()
	return nil
}

// Generate returns a fresh x-statsig-id for the request (pathname, method),
// e.g. Generate("/rest/app-chat/conversations/new", "POST", time.Now().Unix()).
func Generate(pathname, method string, nowUnix int64) (string, error) {
	mu.RLock()
	seed, hex := curSeed, curHEX
	mu.RUnlock()
	return build(seed, hex, pathname, method, nowUnix)
}

// build assembles the 70-byte statsig from a (seed, HEX) pair.
func build(seed []byte, hex, pathname, method string, nowUnix int64) (string, error) {
	if len(seed) != 48 {
		return "", errors.New("statsig: seed must be 48 bytes")
	}
	number := uint32(nowUnix - statsigEpoch)

	var sb strings.Builder
	sb.Grow(len(method) + len(pathname) + len(hex) + 40)
	sb.WriteString(method)
	sb.WriteByte('!')
	sb.WriteString(pathname)
	sb.WriteByte('!')
	sb.WriteString(strconv.FormatUint(uint64(number), 10))
	sb.WriteString(statsigSalt)
	sb.WriteString(hex)
	sha := sha256.Sum256([]byte(sb.String()))

	var keyB [1]byte
	if _, err := rand.Read(keyB[:]); err != nil {
		return "", err
	}
	key := keyB[0]

	out := make([]byte, 70)
	out[0] = key
	for i := 0; i < 48; i++ {
		out[1+i] = seed[i] ^ key
	}
	// tail = uint32LE(number) ++ sha[0:16] ++ [mark]
	out[49] = byte(number) ^ key
	out[50] = byte(number>>8) ^ key
	out[51] = byte(number>>16) ^ key
	out[52] = byte(number>>24) ^ key
	for i := 0; i < 16; i++ {
		out[53+i] = sha[i] ^ key
	}
	out[69] = statsigMark ^ key

	return base64.RawStdEncoding.EncodeToString(out), nil
}

// CaptureSnippet is the browser-console one-liner that prints a fresh genuine
// (seed, HEX) pair to feed SetPair, should grok ever change the algorithm:
//
//	(()=>{const o=crypto.subtle.digest.bind(crypto.subtle);
//	 crypto.subtle.digest=function(a,d){const s=new TextDecoder().decode(
//	   new Uint8Array(d.buffer||d)),i=s.indexOf('obfiowerehiring');
//	   if(i>=0)console.log('SEED=',document.querySelector(
//	     'meta[name="grok-site―verification"]').content,'\nHEX=',s.slice(i+15));
//	   return o(a,d);};})();
//
// then send any chat message; copy the printed SEED and HEX.
const CaptureSnippet = `(()=>{const o=crypto.subtle.digest.bind(crypto.subtle);crypto.subtle.digest=function(a,d){const s=new TextDecoder().decode(new Uint8Array(d.buffer||d)),i=s.indexOf('obfiowerehiring');if(i>=0)console.log('SEED=',document.querySelector('meta[name="grok-site―verification"]').content,'\nHEX=',s.slice(i+15));return o(a,d);};})();`

func decodeSeed(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(s)
}

func mustDecodeSeed(s string) []byte {
	b, err := decodeSeed(s)
	if err != nil || len(b) != 48 {
		panic("statsig: bad default seed")
	}
	return b
}

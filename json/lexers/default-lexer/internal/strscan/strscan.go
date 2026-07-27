// Package strscan is the long-string scan gate for the lexer's string paths.
//
// The lexer probes the first 8-byte word of a string body inline with the [swar.StringStopMask] fast path (the
// short-string / object-key common case resolves there with no call).
// Only when that first word is CLEAN — no closing quote, escape, or control char — does it call [ScanStop], on the
// theory that a string whose first eight bytes are all ordinary is likely long (Fred's "guess long strings" heuristic).
//
// [ScanStop] is therefore the long-string path: on amd64 it hands runs above [avx2Min] bytes to an AVX2 kernel (32
// bytes/iter), and everything else — short remainders, non-amd64 — to the same 8-byte SWAR scan the inline probe
// uses, so the stop semantics are identical everywhere.
//
// The kernel is amd64-only and gated at runtime on CPUID AVX2 support (see detect_amd64.go): a machine without AVX2
// silently takes the SWAR path, never the vector instructions.
package strscan

import (
	"encoding/binary"

	"github.com/go-openapi/core/json/lexers/default-lexer/internal/swar"
)

// scanStopSWAR is the portable 8-byte SWAR scan — the same word loop the lexer runs inline, reusing
// [swar.StringStopMask]/[swar.FirstByte] so a stop is found at exactly the same byte here as on the inline fast path.
//
// It is the non-amd64 implementation of [ScanStop] and the amd64 short-remainder fallback.
func scanStopSWAR(data []byte) int {
	n, i := len(data), 0
	for i+8 <= n {
		if m := swar.StringStopMask(binary.LittleEndian.Uint64(data[i:])); m != 0 {
			return i + swar.FirstByte(m)
		}
		i += 8
	}
	for ; i < n; i++ {
		if b := data[i]; b < 0x20 || b == '"' || b == '\\' {
			return i
		}
	}

	return n
}

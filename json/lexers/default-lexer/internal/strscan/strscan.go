// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

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
//
// The second result reports whether any byte strictly before the stop is >= 0x80, accumulated from the words the scan
// already loaded (the exact analogue of the AVX2 kernel's VPMOVMSKB accumulator). Lanes at or after the stop are
// masked out so the answer is exact, not merely conservative.
func scanStopSWAR(data []byte) (int, bool) {
	n, i := len(data), 0
	var hi uint64
	for i+8 <= n {
		w := binary.LittleEndian.Uint64(data[i:])
		if m := swar.StringStopMask(w); m != 0 {
			k := swar.FirstByte(m)
			hi |= swar.LanesBelow(w, k) // only the lanes before the stop belong to the value

			return i + k, hi&swar.HighBits != 0
		}
		hi |= w
		i += 8
	}
	for ; i < n; i++ {
		b := data[i]
		if b < 0x20 || b == '"' || b == '\\' {
			return i, hi&swar.HighBits != 0
		}
		hi |= uint64(b)
	}

	return n, hi&swar.HighBits != 0
}

//go:build amd64

package utf8x

import "unicode/utf8"

//go:generate sh -c "cd _asm && go run . -out ../validate_amd64.s"

// validateUTF8BlocksAVX2 is the avo-generated AVX2 kernel (validate_amd64.s): a port of the Keiser-Lemire "lookup4"
// algorithm simdutf uses (src/generic/utf8_validation/utf8_lookup4_algorithm.h), 32 bytes per YMM iteration.
//
// Three VPSHUFB table lookups classify every (previous byte, byte) pair — the AND of the three leaves a bit set only
// where all three agree the pair violates the same rule (too short, too long, overlong, surrogate, too large, two
// continuations) — and a saturating-subtract pass asserts that a position which must carry the 2nd or 3rd
// continuation of a sequence actually does.
//
// It validates only whole 32-byte blocks and deliberately says nothing about a sequence that continues past the end:
// errors are detected block-wise, exactly as in simdutf, and the seam is resolved scalar-ly by [Valid]. len(data) must
// be a non-zero multiple of 32, and the CPU must support AVX2 ([useAVX2]).
func validateUTF8BlocksAVX2(data []byte) bool

// avx2Min is the length below which the vector kernel cannot pay for its constant setup (7 broadcasts) plus the
// non-inlinable call. It is also the kernel's hard minimum: it processes whole 32-byte blocks and needs at least one.
//
// Set from BenchmarkValid: at 32 bytes and up the kernel already wins (latin/32: 6.7ns vs the stdlib's 18.1ns), and
// the win grows to 5-12x by 128 bytes. End-to-end on the corpus the 32-vs-64 choice is not distinguishable, so the
// micro-benchmark decides.
//
// The validator only ever runs on values already known to contain a byte >= 0x80 — detection is fused into the string
// scan — so the inputs reaching it are non-ASCII text, which is what BenchmarkValid measures.
const avx2Min = blockBytes

// blockBytes is the kernel's stride: one YMM register.
const blockBytes = 32

// blockMask rounds a length down to a whole number of kernel blocks.
const blockMask = ^(blockBytes - 1)

// maxContinuations is the longest run of continuation bytes a well-formed sequence can have (a 4-byte sequence has
// three). It bounds how far [Valid] rewinds to find the seam.
const maxContinuations = 3

// Valid reports whether b is entirely valid UTF-8.
//
// On amd64 with AVX2 it runs the vector kernel over the whole 32-byte blocks and finishes with the stdlib scalar
// validator over the trailing bytes, rewound to the start of the last sequence so a sequence straddling that boundary
// — or truncated at the very end, which the block kernel does not check — is fully validated exactly once.
func Valid(b []byte) bool {
	if !useAVX2 || len(b) < avx2Min {
		return utf8.Valid(b)
	}

	blocks := len(b) & blockMask
	if !validateUTF8BlocksAVX2(b[:blocks]) {
		return false
	}

	return utf8.Valid(b[seam(b, blocks):])
}

// seam returns the index at which scalar validation must resume so that everything the block kernel could not check is
// covered, and everything before it was checked.
//
// The kernel validates a pair only when both its bytes are inside the block region, so the unchecked remainder is: the
// trailing bytes past the last whole block, plus any sequence that begins before the boundary and reaches it. Such a
// sequence starts at most three bytes back, so walking back over continuation bytes (bounded) and then taking the
// non-continuation byte that precedes them lands exactly on its first byte.
//
// The bound is not a heuristic: more than three continuation bytes in a row is itself ill-formed, and returning an
// index that lands mid-sequence makes the scalar pass reject — which is the correct answer for that input.
func seam(b []byte, blocks int) int {
	start := blocks
	for range maxContinuations {
		if start == 0 || b[start-1]&0xC0 != 0x80 {
			break
		}
		start--
	}

	if start > 0 {
		start-- // include the byte that leads the sequence (or is an ASCII byte of its own)
	}

	return start
}

// Package swar provides inlinable SWAR (SIMD-within-a-register) byte-scanning primitives for the lexer's hot paths.
//
// Each mask function operates on one 8-byte little-endian word and returns a per-lane mask: the high bit (0x80) is set
// in every byte lane that matches, and clear elsewhere.
// The caller keeps its own (inlinable) scan loop, tests the mask against zero, and uses [FirstByte] to locate the exact
// matching lane.
//
// Why a per-word mask and not a whole-loop scanner: a helper that owned the scan loop busted the Go inliner budget
// (cost 98 > 80) and regressed the string fast path.
// Extracting only the per-word bit math — never the loop — keeps every function tiny enough to inline, so the call
// site pays no call overhead.
//
// The inline gate is enforced by TestInlinable.
//
// All masks are exact (no false positives) and multibyte-safe: a UTF-8 byte >= 0x80 is correctly classified (e.g. it is
// "greater than '9'", so it ends a digit run, and it is not "less than 0x20").
// The general [MaskLess]/[MaskGreater] use the branchless byte-isolating comparison so the high bit of a lane never
// leaks across lanes; [StringStopMask] uses the cheaper ASCII-needle form valid because all its needles are < 0x80.
//
//nolint:mnd // expect a lot of offsets
package swar

import "math/bits"

const (
	lo   = ^uint64(0) / 255 // 0x0101010101010101 — low bit of every lane
	high = lo * 0x80        // 0x8080808080808080 — high bit of every lane
)

// FirstByte returns the index (0..7) of the lowest-address lane flagged in mask. mask must be non-zero and carry bits
// only at lane high-bit positions (i.e. a value returned by one of the Mask functions); on a little-endian word the
// lowest set bit belongs to the lowest-address lane.
func FirstByte(mask uint64) int { return bits.TrailingZeros64(mask) >> 3 }

// Broadcast replicates b into all eight lanes of a word.
func Broadcast(b byte) uint64 { return lo * uint64(b) }

// StringStopMask flags lanes that end a JSON string-body scan: a control char (< 0x20), a double quote (0x22) or a
// backslash (0x5c).
//
// It uses the ASCII-needle form (valid because every needle is < 0x80): the "< 0x20" term's &^ w excludes any lane
// whose high bit is set, so multibyte bytes are correctly not flagged as control chars, and the two equality terms are
// exact.
func StringStopMask(w uint64) uint64 {
	m := (w - lo*0x20) &^ w & high // any lane < 0x20
	q := w ^ (lo * 0x22)
	m |= (q - lo) &^ q & high // any lane == 0x22 (")
	s := w ^ (lo * 0x5c)
	m |= (s - lo) &^ s & high // any lane == 0x5c (\)

	return m
}

// MaskEqual flags lanes whose byte equals b. Exact for all byte values via the 0x7f saturation form (no cross-lane
// borrow).
func MaskEqual(w uint64, b byte) uint64 {
	x := w ^ Broadcast(b)
	y := ((x & ^high) + ^high) | x

	return ^y & high
}

// MaskLess flags lanes whose byte is < n, for any n in 0..255 (multibyte-safe).
func MaskLess(w uint64, n byte) uint64 {
	cm := Broadcast(n)
	d := (w | high) - (cm &^ high)
	sel := ((w & (w ^ cm)) | (d &^ (w ^ cm))) & high

	return sel ^ high
}

// MaskGreater flags lanes whose byte is > n, for any n in 0..255 (multibyte-safe).
func MaskGreater(w uint64, n byte) uint64 {
	cm := Broadcast(n)
	d := (cm | high) - (w &^ high)
	sel := ((cm & (cm ^ w)) | (d &^ (cm ^ w))) & high

	return sel ^ high
}

// DigitMask flags lanes whose byte is an ASCII digit '0'..'9' (0x30..0x39).
//
// It is the exact "hasbetween" range test (0x2f < byte < 0x3a): the &^ w term clears any lane whose high bit is set, so
// a multibyte byte (>= 0x80) is never flagged as a digit.
// Self-contained (no composed calls) so it inlines.
func DigitMask(w uint64) uint64 {
	xm := w &^ high         // byte values with the high bit masked off
	a := lo*(127+0x3a) - xm // high bit set per lane where byte < 0x3a
	c := xm + lo*(127-0x2f) // high bit set per lane where byte > 0x2f

	return a &^ w & c & high
}

// NonDigitMask flags lanes whose byte is NOT an ASCII digit — i.e. the lanes that end a digit run (including any byte
// >= 0x80, which is not a digit).
func NonDigitMask(w uint64) uint64 { return DigitMask(w) ^ high }

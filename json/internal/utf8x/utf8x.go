// Package utf8x holds the UTF-8 validation and sanitization primitives shared by the JSON lexers and writers.
//
// It lives under json/internal (rather than under a lexer- or writer-private internal package) because it is the only
// location both subtrees can import: the lexers validate what they read, the writers validate what they emit, and both
// must agree byte-for-byte on what "invalid" means and on what an invalid sequence is replaced with.
//
// The functions here are deliberately whole-buffer: the lexers detect the *presence* of non-ASCII inline, fused into
// their existing stop-byte scan (see the swar package), and only call in here for the values that actually carry a byte
// >= 0x80. So [Valid] is not on the hot path for the overwhelmingly common all-ASCII value.
package utf8x

import "unicode/utf8"

const (
	// replacement is the UTF-8 encoding of U+FFFD, the substitute for an invalid byte.
	replacement = string(utf8.RuneError)

	// replacementWidth is its encoded width (3).
	replacementWidth = len(replacement)
)

// What "valid" means here is exactly [utf8.Valid]'s definition, which is RFC 3629 plus Unicode: truncated sequences,
// overlong encodings, lead/continuation mismatches, encoded surrogates (U+D800..U+DFFF) and anything above U+10FFFF
// are all invalid. WTF-8 and CESU-8 are not accepted. [Valid] itself is platform-specific (see validate_amd64.go).

// FirstInvalid returns the index of the first byte of the first ill-formed sequence in b, or -1 if b is valid UTF-8.
//
// It is the cold, error-path counterpart of [Valid]: callers use [Valid] for the verdict and only come here to report
// *where* the input went wrong. Keeping the position search separate is what lets [Valid] stay a bulk scan (the same
// split simdutf makes between its SIMD checker and rewind_and_validate_with_errors).
func FirstInvalid(b []byte) int {
	for i := 0; i < len(b); {
		if b[i] < utf8.RuneSelf {
			i++

			continue
		}

		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size <= 1 {
			return i
		}
		i += size
	}

	return -1
}

// Sanitize appends src to dst, substituting U+FFFD for every invalid byte, and returns the extended slice.
//
// Granularity is **one replacement per invalid byte** — [utf8.DecodeRune]'s error advance — so "\xe0\xa0\xff" yields
// three replacement runes. That is what ranging over a Go string produces, and what the JSON writer's escaper already
// did before this package existed, so the lexer, the writer and caller-side string conversion all agree.
//
// A valid src is appended in one bulk copy with no per-rune work.
func Sanitize(dst, src []byte) []byte {
	i := FirstInvalid(src)
	if i < 0 {
		return append(dst, src...)
	}

	dst = append(dst, src[:i]...)

	for i < len(src) {
		if src[i] < utf8.RuneSelf {
			dst = append(dst, src[i])
			i++

			continue
		}

		r, size := utf8.DecodeRune(src[i:])
		if r == utf8.RuneError && size <= 1 {
			dst = append(dst, replacement...)
			i++

			continue
		}
		dst = append(dst, src[i:i+size]...)
		i += size
	}

	return dst
}

// SanitizedLen returns the length Sanitize would produce for src, or -1 if src is valid UTF-8 (nothing to do).
//
// It lets a caller enforce a size cap before committing to the rewrite, since replacing a 1-byte invalid sequence with
// U+FFFD grows the value.
func SanitizedLen(src []byte) int {
	i := FirstInvalid(src)
	if i < 0 {
		return -1
	}

	n := i
	for i < len(src) {
		if src[i] < utf8.RuneSelf {
			n++
			i++

			continue
		}

		r, size := utf8.DecodeRune(src[i:])
		if r == utf8.RuneError && size <= 1 {
			n += replacementWidth
			i++

			continue
		}
		n += size
		i += size
	}

	return n
}

// Policy selects what a lexer or writer does with data that cannot be represented as a sequence of Unicode scalar
// values: an ill-formed UTF-8 byte sequence, or a \u escape that does not denote one.
//
// It lives here, alongside the validator, so the lexers and the writers share one definition and cannot drift apart —
// a document rejected on the way in and one refused on the way out must mean the same thing. Both packages alias it
// (lexer.UTF8Policy, writer.UTF8Policy), so callers never name this internal package.
//
// The zero value is [PolicyStrict]: validation is on unless a caller opts out.
type Policy uint8

const (
	// PolicyStrict rejects the data. This is the default on both sides.
	PolicyStrict Policy = iota

	// PolicyReplace substitutes U+FFFD — one per invalid byte, one per broken escape — so what is emitted is always
	// valid UTF-8. See [Sanitize] for the exact granularity.
	PolicyReplace

	// PolicyPassthrough skips raw-byte validation entirely, letting ill-formed bytes through untouched.
	//
	// UNSAFE: only for data already known to be valid UTF-8. A \u escape still yields U+FFFD when broken, because an
	// escape must always produce some rune.
	PolicyPassthrough
)

// Validates reports whether the policy inspects raw bytes at all.
func (p Policy) Validates() bool { return p != PolicyPassthrough }

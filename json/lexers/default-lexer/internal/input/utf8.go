package input

import (
	"unicode/utf16"
	"unicode/utf8"

	"github.com/go-openapi/core/json/internal/utf8x"
	codes "github.com/go-openapi/core/json/lexers/error-codes"
	scan "github.com/go-openapi/core/json/lexers/internal/scan"
)

// surrogateEscapeLen is the width of the `\uXXXX` that must follow a high surrogate to complete a pair.
const surrogateEscapeLen = 6

// The UTF-8 byte order mark, and the UTF-16/UTF-32 ones — the latter recognized only to turn an unhelpful error into an
// accurate one.
const (
	bomUTF8Len       = 3
	bom0, bom1, bom2 = 0xEF, 0xBB, 0xBF

	bomUTF16Len = 2
	bomUTF16LE0 = 0xFF
	bomUTF16LE1 = 0xFE
	bomUTF16BE0 = 0xFE
	bomUTF16BE1 = 0xFF

	// UTF-32 marks are FF FE 00 00 (LE) and 00 00 FE FF (BE). The LE one opens with the UTF-16LE mark and so is already
	// caught by it; only the BE one, which shares no prefix with any other mark, needs its own bytes.
	bomUTF32Len              = 4
	bomUTF32BE0, bomUTF32BE1 = 0x00, 0x00
	bomUTF32BE2, bomUTF32BE3 = 0xFE, 0xFF
)

// CheckBOM consumes a UTF-8 byte order mark opening the input, and rejects a UTF-16 or UTF-32 one.
//
// RFC 8259 §8.1 forbids *emitting* a BOM but explicitly allows an implementation to ignore one on input, and enough
// producers emit one that rejecting the document outright is unhelpful.
//
// It runs once, before the first token, on the buffer's opening bytes only — so it costs one comparison per document
// and touches no scan loop. Only the complete three-byte sequence at offset 0 is consumed: a truncated BOM, or a
// U+FEFF anywhere else, stays what it is (an invalid token between values, or an ordinary character inside a string),
// which is what keeps n_structure_incomplete_UTF8_BOM and n_structure_UTF8_BOM_no_data rejected.
//
// The mark is NOT re-emitted by the verbatim lexer: it is consumed before any token exists, so it is not part of any
// token's leading space (see [VL.LeadingSpace]). A round-trip therefore drops it — the one documented exception to
// VL's byte-exactness on valid input, and the direction RFC 8259 §8.1 prefers.
//
// A UTF-16 or UTF-32 document cannot be lexed at all (we are UTF-8 only) and would otherwise fail with a baffling
// "invalid JSON token" on its first byte; codes.ErrNotUTF8 says what is actually wrong. Nothing is consumed there — the
// document is rejected either way — and neither 0xFF/0xFE nor a leading NUL pair can legitimately open a JSON value, so
// there is no input this misjudges. The error does not name a single encoding: UTF-32LE opens with the very bytes of
// the UTF-16LE mark, so a truncated window cannot always tell the two apart, and pinning the message on the wrong one
// is worse than naming both.
func (in *Input) CheckBOM() {
	if in.Consumed != 0 || in.Offset != 0 || in.Bufferized < bomUTF16Len {
		return
	}

	data := in.Buffer
	switch {
	case (data[0] == bomUTF16LE0 && data[1] == bomUTF16LE1) || // UTF-16LE, or the opening of UTF-32LE
		(data[0] == bomUTF16BE0 && data[1] == bomUTF16BE1) || // UTF-16BE
		(in.Bufferized >= bomUTF32Len && // UTF-32BE, which shares no prefix with the UTF-16 marks
			data[0] == bomUTF32BE0 && data[1] == bomUTF32BE1 &&
			data[2] == bomUTF32BE2 && data[3] == bomUTF32BE3):
		in.Err = codes.ErrNotUTF8

	case in.Bufferized >= bomUTF8Len &&
		data[0] == bom0 && data[1] == bom1 && data[2] == bom2:
		in.Consumed, in.Offset = bomUTF8Len, bomUTF8Len
	}
}

// brokenSurrogate applies the UTF-8 policy to a \u escape that does not denote a Unicode scalar value: an unpaired
// high surrogate, an inverted pair, or a lone low surrogate.
//
// A \u escape must always produce SOME rune — the escape text is not the value, so there is nothing to pass through —
// which is why UTF8Passthrough behaves like UTF8Replace here and only UTF8Strict errors.
//
// Both lexers route through this so they cannot drift apart again: L used to launder the failure into a silent U+FFFD
// (utf16.DecodeRune returns utf8.RuneError, and utf8.ValidRune(U+FFFD) is true) while VL rejected it.
func (in *Input) brokenSurrogate() error {
	if in.UTF8Policy == UTF8Strict {
		return codes.ErrSurrogateEscape
	}

	return nil
}

// peekLowSurrogate inspects — WITHOUT consuming — the next `\uXXXX` and reports the code unit it carries.
//
// It does not consume, because a sequence that turns out not to complete a pair must be left for the caller to process
// as an escape in its own right: `\uD800\uD800` is two ill-formed units and must yield two U+FFFD, not one.
func (in *Input) peekLowSurrogate() (rune, bool) {
	if !in.ensureWindow(surrogateEscapeLen) {
		return 0, false
	}

	w := in.Buffer[in.Consumed : in.Consumed+surrogateEscapeLen]
	if w[0] != escape || w[1] != 'u' {
		return 0, false
	}

	code, ok := scan.Hex4(w[2], w[3], w[4], w[5])
	if !ok {
		return 0, false
	}

	return rune(code), true
}

// takePeeked consumes n bytes previously inspected by [Input.peekLowSurrogate].
func (in *Input) takePeeked(n int) {
	in.Consumed += n
	in.Offset += uint64(n)
}

// readHex4 consumes the four hex digits of a \uXXXX escape (the leading "\u" is already consumed) and returns them
// verbatim along with the code unit they encode. The digits are unambiguously part of this escape, so consuming them
// needs no lookahead.
//
// The raw digits are returned because the verbatim scanner appends them to its value and cannot read them back off the
// window: consumeN may have crossed a refill.
func (in *Input) readHex4() ([4]byte, rune, error) {
	var buf [4]byte
	if err := in.consumeN(buf[:]); err != nil {
		return buf, 0, codes.ErrUnicodeEscape
	}

	code, ok := scan.Hex4(buf[0], buf[1], buf[2], buf[3])
	if !ok {
		return buf, 0, codes.ErrUnicodeEscape
	}

	return buf, rune(code), nil
}

// validateUnicodeWhole validates a \uXXXX sequence at pos (first hex digit, past the 'u') in a whole buffer, following
// one surrogate pair when present.
//
// It returns the index just past the validated sequence, or an error. A `\uXXXX` that does not complete a pair is NOT
// consumed here: it is left to be validated as its own escape on the next turn (see [Input.peekLowSurrogate]).
func validateUnicodeWhole(data []byte, pos, n int, policy UTF8Policy) (int, error) {
	if pos+4 > n {
		return pos, codes.ErrUnicodeEscape
	}

	code, ok := scan.Hex4(data[pos], data[pos+1], data[pos+2], data[pos+3])
	if !ok {
		return pos, codes.ErrUnicodeEscape
	}
	pos += 4

	r := rune(code)
	if !utf16.IsSurrogate(r) {
		return pos, nil
	}

	if pos+surrogateEscapeLen <= n && data[pos] == escape && data[pos+1] == 'u' {
		code2, ok2 := scan.Hex4(data[pos+2], data[pos+3], data[pos+4], data[pos+5])
		if ok2 && utf16.DecodeRune(r, rune(code2)) != utf8.RuneError {
			return pos + surrogateEscapeLen, nil
		}
	}

	if policy == UTF8Strict {
		return pos, codes.ErrSurrogateEscape
	}

	return pos, nil
}

// handleInvalidUTF8 applies the configured [UTF8Policy] to a string value that has just been found to carry an
// ill-formed UTF-8 sequence.
//
// It is deliberately out of line: it is unreachable for valid input, so keeping it out of finishStringValue's frame
// leaves the funnel's codegen to the fast path (the same split the string fast/slow paths use).
//
// It returns the value to emit and whether lexing may continue; under [UTF8Strict] it sets in.Err and returns false.
func (in *Input) handleInvalidUTF8(value []byte) ([]byte, bool) {
	if in.UTF8Policy == UTF8Strict {
		in.Err = codes.ErrInvalidUTF8
		in.Offset = in.faultOffset(value)

		return nil, false
	}

	// UTF8Replace: rewrite into a dedicated scratch. It cannot be in.CurrentValue, which IS the value on the
	// unescaping paths.
	if in.MaxValueBytes > 0 && utf8x.SanitizedLen(value) > in.MaxValueBytes {
		// substituting U+FFFD can grow the value (3 bytes for every invalid byte), so the cap is re-checked against the
		// rewritten length, not the scanned one.
		in.Err = codes.ErrMaxValueBytes

		return nil, false
	}

	in.sanitized = utf8x.Sanitize(in.sanitized[:0], value)

	return in.sanitized, true
}

// faultOffset maps the first ill-formed byte of value back to a stream offset.
//
// On entry the cursor sits just past the closing quote, so the value body starts at Offset-len(value)-1. That is exact
// for a value aliased from the input (every non-escaped path, and so every case a caller is likely to debug); when
// escapes were decoded the value is shorter than its source span, and the reported offset is correspondingly an
// under-estimate pointing inside the same string.
func (in *Input) faultOffset(value []byte) uint64 {
	idx := utf8x.FirstInvalid(value)
	if idx < 0 {
		return in.Offset
	}

	back := uint64(len(value)-idx) + 1
	if back > in.Offset {
		return 0
	}

	return in.Offset - back
}

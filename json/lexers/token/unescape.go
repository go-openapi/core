package token

import (
	"bytes"
	"unicode/utf16"
	"unicode/utf8"

	scan "github.com/go-openapi/core/json/lexers/internal/scan"
)

// Unescape returns the decoded form of a raw string/Key value produced by the
// verbatim lexer [lexer.VL].
//
// VL keeps string/Key values exactly as they appeared in the source (escape
// sequences intact — see the [String]/[Key] doc), so the token stream can be
// round-tripped byte-for-byte. Unescape expands the JSON escapes on demand: the
// shorthand escapes (\", \\, \/, \b, \f, \n, \r, \t) and \uXXXX sequences
// (surrogate pairs combined) become their UTF-8 bytes.
//
// If raw contains no escape it is returned unchanged with no allocation; otherwise
// a fresh slice is returned. Do NOT call this on a semantic-lexer value — the
// semantic lexer [lexer.L] already decodes.
//
// The escapes were validated when the token was scanned, so decoding cannot fail; a
// malformed sequence would have errored at scan time (any residual bad input is
// passed through rather than panicking).
func Unescape(raw []byte) []byte {
	if bytes.IndexByte(raw, '\\') < 0 {
		return raw
	}

	return unescape(raw)
}

// UnescapeString is [Unescape] as a string. It always allocates (the string header
// cannot alias the token's buffer, which is reused on the next token).
func UnescapeString(raw []byte) string {
	return string(Unescape(raw))
}

// unescape decodes a raw JSON string body (no surrounding quotes) whose escapes
// were already validated. It is the token-side counterpart of the lexer's
// unescaping scanner, kept standalone so [token] has no dependency on the lexer.
func unescape(raw []byte) []byte {
	// the decoded form is never longer than the raw form (every escape shrinks),
	// so one allocation of len(raw) suffices.
	out := make([]byte, 0, len(raw))

	for i := 0; i < len(raw); {
		c := raw[i]
		if c != '\\' {
			out = append(out, c)
			i++

			continue
		}

		// c == '\\'; a validated escape follows
		i++
		if i >= len(raw) {
			// unreachable on validated input; pass the lone backslash through
			out = append(out, '\\')

			break
		}
		switch raw[i] {
		case '"':
			out = append(out, '"')
			i++
		case '\\':
			out = append(out, '\\')
			i++
		case '/':
			out = append(out, '/')
			i++
		case 'b':
			out = append(out, '\b')
			i++
		case 'f':
			out = append(out, '\f')
			i++
		case 'n':
			out = append(out, '\n')
			i++
		case 'r':
			out = append(out, '\r')
			i++
		case 't':
			out = append(out, '\t')
			i++
		case 'u':
			r, next := decodeUnicode(raw, i+1)
			out = utf8.AppendRune(out, r)
			i = next
		default:
			// unreachable on validated input; pass through
			out = append(out, '\\', raw[i])
			i++
		}
	}

	return out
}

// decodeUnicode decodes a \uXXXX sequence (and a following \uXXXX low surrogate
// when the first is a high surrogate) starting at pos (the first hex digit, past
// the 'u'). It returns the rune and the index just past the consumed sequence.
// Input is assumed validated.
func decodeUnicode(raw []byte, pos int) (rune, int) {
	if pos+4 > len(raw) {
		return utf8.RuneError, len(raw)
	}
	// input was validated at scan time, so the hex digits are known-good (ok ignored).
	c1, _ := scan.Hex4(raw[pos], raw[pos+1], raw[pos+2], raw[pos+3])
	r := rune(c1) //nolint:gosec // c1 is indeed bounded to be a the proper rune
	pos += 4

	if utf16.IsSurrogate(r) {
		const offset = 6
		// a validated high surrogate is followed by "\uYYYY"
		if pos+offset <= len(raw) && raw[pos] == '\\' && raw[pos+1] == 'u' {
			c2, _ := scan.Hex4(raw[pos+2], raw[pos+3], raw[pos+4], raw[pos+5])
			if dec := utf16.DecodeRune(
				r,
				rune(c2), //nolint:gosec // c2 is indeed bounded to be a the proper rune
			); dec != utf8.RuneError {
				return dec, pos + offset
			}
		}

		return utf8.RuneError, pos
	}

	return r, pos
}

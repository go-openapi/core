package writer

import (
	"unicode/utf8"
)

// escapedBytes escapes input into output per the JSON string rules, returning the escaped bytes and any trailing
// bytes that form an incomplete UTF-8 sequence (which the caller stitches with the next read, or reports).
//
// policy decides what happens to a byte that is not part of a well-formed sequence: [UTF8Passthrough] copies it
// verbatim, anything else substitutes U+FFFD — one per invalid byte, the same granularity as utf8x.Sanitize, so the
// lexer and the writer agree on what a sanitized value looks like. Under [UTF8Strict] the caller has already
// rejected such input, so the substitution is unreachable there.
func escapedBytes(input, output []byte, policy UTF8Policy) ([]byte, []byte) {
	var (
		p       int
		escaped bool
	)
	output = output[:0]

	// first iterates over non-escaped bytes.
	for ; p < len(input); p++ {
		c := input[p]
		if c < lowestPrintable || c >= utf8.RuneSelf || c == '\t' || c == '\r' || c == '\n' ||
			c == '\\' ||
			c == '"' ||
			c == '\b' ||
			c == '\f' {
			escaped = true
			output = append(output, input[:p]...)
			break
		}
	}

	// if nothing to be escaped, just return the input
	if !escaped {
		return input, nil
	}

	for i := p; i < len(input); i++ {
		c := input[i]

		switch {
		case c == '\t':
			output = append(output, '\\', 't')
		case c == '\r':
			output = append(output, '\\', 'r')
		case c == '\n':
			output = append(output, '\\', 'n')
		case c == '\\':
			output = append(output, '\\', '\\')
		case c == '"':
			output = append(output, '\\', '"')
		case c == '\b':
			output = append(output, '\\', 'b')
		case c == '\f':
			output = append(output, '\\', 'f')
		case c >= 0x20 && c < utf8.RuneSelf:
			// single-width character, no escaping is required
			output = append(output, c)
		case c < lowestPrintable:
			// control character is escaped as the unicode sequence \u00{hex representation of c}
			const chars = "0123456789abcdef"
			output = append(
				output,
				'\\',
				'u',
				'0',
				'0',
				chars[c>>4],
				chars[c&0xf],
			) // hexadecimal representation of c
		default:
			// multi-byte UTF8 character.
			if !utf8.FullRune(input[i:]) {
				// needs more read to complete the current rune
				return output, input[i:]
			}
			r, runeWidth := utf8.DecodeRune(input[i:])
			if r == utf8.RuneError && runeWidth == 1 && policy == UTF8Passthrough {
				output = append(
					output,
					c,
				) // ill-formed, but the caller asked for its bytes untouched

				continue
			}
			output = utf8.AppendRune(output, r) // invalid runes are represented as \uFFFD
			i += runeWidth - 1
		}
	}

	return output, nil
}

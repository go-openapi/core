package writer

import (
	"unicode/utf8"
)

func escapedBytes(input, output []byte) ([]byte, []byte) {
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
			output = utf8.AppendRune(output, r) // invalid runes are represented as \uFFFD
			i += runeWidth - 1
		}
	}

	return output, nil
}

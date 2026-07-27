// Package scan holds stateless JSON-scanning primitives shared across the lexer
// subtree — the default lexer's hot cores and the token package's on-demand
// unescaper: whitespace skipping and hex / \uXXXX digit decoding.
//
// Every function is pure (bytes in, values out; no lexer state) and deliberately
// small so it inlines into the callers' hot loops (verified via -gcflags=-m).
package scan

const (
	blank          = ' '
	tab            = '\t'
	lineFeed       = '\n'
	carriageReturn = '\r'
)

// IsBlank reports whether c is JSON insignificant whitespace (space, tab, LF, CR).
func IsBlank(c byte) bool {
	return c == blank || c == tab || c == lineFeed || c == carriageReturn
}

// ConsumeWhitespace returns the count of leading insignificant JSON whitespace in b
// (space, tab, LF, CR) — mirrors jsontext's ConsumeWhitespace. Used to batch-skip a
// whitespace run with a local cursor (one write-back) instead of a per-byte advance.
func ConsumeWhitespace(b []byte) (n int) {
	for n < len(b) && (b[n] == blank || b[n] == tab || b[n] == lineFeed || b[n] == carriageReturn) {
		n++
	}

	return n
}

// ConsumeWhitespaceTracked is [ConsumeWhitespace] for the position-tracking (verbatim)
// cores: it also counts newlines and reports afterLastNL, the index just past the last
// '\n' in the run (0 if none), so the caller can update line/lineStart from a single
// scan instead of a per-byte walk.
func ConsumeWhitespaceTracked(b []byte) (n, lines, afterLastNL int) {
	for n < len(b) {
		c := b[n]
		if c != blank && c != tab && c != lineFeed && c != carriageReturn {
			break
		}
		if c == lineFeed {
			lines++
			afterLastNL = n + 1
		}
		n++
	}

	return n, lines, afterLastNL
}

// Unhex decodes a single hex digit (0-9, a-f, A-F), reporting whether c was valid.
func Unhex(c byte) (byte, bool) {
	const asciiOffset = 10
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + asciiOffset, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + asciiOffset, true
	default:
		return 0, false
	}
}

// Hex4 decodes four hex-digit bytes into a codepoint value (0..0xFFFF), reporting
// whether all four were valid hex digits.
func Hex4(a, b, c, d byte) (uint32, bool) {
	h1, ok1 := Unhex(a)
	l1, ok2 := Unhex(b)
	h2, ok3 := Unhex(c)
	l2, ok4 := Unhex(d)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return 0, false
	}

	return uint32(h1)<<12 | uint32(l1)<<8 | uint32(h2)<<4 | uint32(l2), true
}

//go:build !amd64

package utf8x

import "unicode/utf8"

// Valid reports whether b is entirely valid UTF-8.
//
// Off amd64 there is no vector kernel, so this is the stdlib scalar validator (which carries its own 8-byte ASCII
// fast path). The answer is identical on every platform; only the speed differs.
func Valid(b []byte) bool { return utf8.Valid(b) }

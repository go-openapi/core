package token

import (
	"testing"

	"github.com/go-openapi/testify/v2/assert"
)

func TestUnescape(t *testing.T) {
	cases := []struct {
		raw string // a verbatim value (raw source, escapes intact)
		dec string // expected Unescape output
	}{
		{"plain", "plain"},
		{"a\\nb", "a\nb"},
		{"tab\\tx", "tab\tx"},
		{"q\\\"q", `q"q`},
		{"back\\\\slash", `back\slash`},
		{"slash\\/x", "slash/x"},
		{"\\b\\f\\r", "\b\f\r"},
		{"\\u0041", "A"},
		{"caf\\u00e9", "café"},
		{"\\ud83d\\ude00", "😀"}, // surrogate pair
		{"a\\u0041\\u0042b", "aABb"},
		{"literalé", "literalé"}, // already-decoded UTF-8 passes through
		{"", ""},
	}
	for _, c := range cases {
		assert.Equalf(t, c.dec, UnescapeString([]byte(c.raw)), "decoded for raw %q", c.raw)
		assert.Equalf(t, c.dec, string(Unescape([]byte(c.raw))), "decoded bytes for raw %q", c.raw)
	}
}

func TestUnescapePassthrough(t *testing.T) {
	// an escape-free value returns the same backing slice (no allocation).
	raw := []byte("clean")
	assert.Equal(t, "clean", UnescapeString(raw))
	got := Unescape(raw)
	assert.Equal(t, "clean", string(got))
	if &got[0] != &raw[0] {
		t.Error("escape-free Unescape must alias the input (no allocation)")
	}
}

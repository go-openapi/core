package store

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	"github.com/go-openapi/core/json/lexers/token"
)

// TestAppendValueBytesMatchesGet checks that AppendValueBytes returns the same value as Get across
// every storage path (inlined number/string/ascii, large number/string, compressed string).
func TestAppendValueBytesMatchesGet(t *testing.T) {
	cases := map[string]token.T{
		"bool":         token.MakeBoolean(true),
		"small int":    token.MakeWithValue(token.Number, []byte("1234")),
		"exponent num": token.MakeWithValue(token.Number, []byte("3.8e+20")),
		"large num": token.MakeWithValue(
			token.Number,
			[]byte("12345678901234567890.12345e-7"),
		),
		"small string":   token.MakeWithValue(token.String, []byte("abcd")),
		"ascii string":   token.MakeWithValue(token.String, []byte("abcdefgh")),
		"medium string":  token.MakeWithValue(token.String, []byte("abcdefghij-klmnop")),
		"large string":   token.MakeWithValue(token.String, []byte(strings.Repeat("x", 100))),
		"compressed str": token.MakeWithValue(token.String, []byte(strings.Repeat("ab", 100))),
	}

	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			s := New()
			h := s.PutToken(tok)

			want := s.Get(h)
			got, _ := s.AppendValueBytes(nil, h)

			assert.Equal(t, want.Kind(), got.Kind())
			assert.Equal(t, want.Bytes(), got.Bytes(), "value bytes must match Get")
		})
	}
}

// TestAppendValueBytesReuseScratch exercises the steady-state reuse pattern across mixed values.
func TestAppendValueBytesReuseScratch(t *testing.T) {
	s := New()
	inputs := [][]byte{
		[]byte("1e+5"),
		[]byte(strings.Repeat("z", 200)),
		[]byte("hello"),
	}

	var scratch []byte
	for _, in := range inputs {
		h := s.PutToken(token.MakeWithValue(token.String, in))

		v, next := s.AppendValueBytes(scratch[:0], h)
		scratch = next
		assert.Equal(t, in, v.Bytes())
	}
}

// TestVerbatimAppendValueBytesBlanks checks the VerbatimStore override decodes blank handles (which
// the inherited Store.AppendValueBytes would not), matching VerbatimStore.Get.
func TestVerbatimAppendValueBytesBlanks(t *testing.T) {
	for name, blanks := range map[string][]byte{
		"inlined blanks":    []byte("  \t\n "),
		"compressed blanks": bytes.Repeat([]byte(" \t"), 64),
	} {
		t.Run(name, func(t *testing.T) {
			s := NewVerbatim()
			h := s.PutBlanks(blanks)

			want := s.Get(h)
			got, _ := s.AppendValueBytes(nil, h)

			assert.Equal(t, want.Kind(), got.Kind())
			assert.Equal(t, blanks, got.Bytes())
		})
	}
}

// TestAppendValueBytesIndependentOfStore proves an AppendValueBytes value survives store recycling,
// because it copies into caller memory (unlike a Get large-string alias).
func TestAppendValueBytesIndependentOfStore(t *testing.T) {
	s := New()
	original := []byte(strings.Repeat("payload", 30)) // large string -> arena
	h := s.PutToken(token.MakeWithValue(token.String, original))

	val, _ := s.AppendValueBytes(nil, h)
	require.Equal(t, original, val.Bytes())

	// recycle the store and overwrite the arena with unrelated data
	s.Reset()
	for range 10 {
		s.PutToken(token.MakeWithValue(token.String, bytes.Repeat([]byte("Z"), 64)))
	}

	// the previously extracted value is unaffected
	assert.Equal(
		t,
		original,
		val.Bytes(),
		"AppendValueBytes value must be independent of the store",
	)
}

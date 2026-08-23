package lexer

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	codes "github.com/go-openapi/core/json/lexers/error-codes"
	"github.com/go-openapi/core/json/lexers/token"
)

// TestBufferSizeAlignment pins the tiny-buffer guard rail: WithBufferSize rounds the requested size up to a multiple of
// bufferSizeAlignment (32 bytes, one AVX2 stride), so the streaming window is never narrower than a single vector/SWAR
// step.
//
// This floors out the pathological tiny windows that stressed the byte-by-byte refill seam.
func TestBufferSizeAlignment(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{1, 32},
		{2, 32},
		{8, 32},
		{31, 32},
		{32, 32},
		{33, 64},
		{63, 64},
		{64, 64},
		{65, 96},
		{4095, 4096},
		{4096, 4096},
	}
	for _, c := range cases {
		if got := alignBufferSize(c.in); got != c.want {
			t.Errorf("alignBufferSize(%d) = %d, want %d", c.in, got, c.want)
		}
	}

	// non-positive sizes are ignored: the option keeps the (aligned) default.
	for _, bad := range []int{0, -1, -32} {
		o := applyWithDefaults([]Option{WithBufferSize(bad)})
		if o.bufferSize != defaultBufferBytes {
			t.Errorf(
				"WithBufferSize(%d): bufferSize = %d, want default %d",
				bad,
				o.bufferSize,
				defaultBufferBytes,
			)
		}
	}

	// the aligned size is observable as the allocated window capacity.
	l := New(bytes.NewReader([]byte(`"x"`)), WithBufferSize(10))
	if cap(l.in.Buffer) != 32 {
		t.Errorf("WithBufferSize(10): window cap = %d, want 32", cap(l.in.Buffer))
	}
}

// ---- merged from avx2_knob_test.go ---- avx2Doc builds a document that exercises the AVX2 long-string delegation:
// long clean string values (well past guessLong), a long value with an escape followed by a long clean tail, plus short
// keys/values and numbers so both the inline fast path and the delegated path run.
func avx2Doc() []byte {
	long := strings.Repeat("abcdefghij", 30) // 300-byte clean value
	tail := strings.Repeat("x", 200)         // long clean tail after an escape
	return fmt.Appendf(nil,
		`{"k":"v","desc":%q,"n":123,"escaped":"pre\t%s","arr":["short",%q],"u":"café %s"}`,
		long, tail, long, tail,
	)
}

// tokenLike is satisfied by token.T (both the semantic lexer L and the verbatim lexer VL emit token.T), so the
// collector drains either lexer.
type tokenLike interface {
	Kind() token.Kind
	Value() []byte
	IsEOF() bool
}

// collectKV drains a token stream into (kind, value) pairs.
func collectKV[TK tokenLike](
	next func() TK,
	ok func() bool,
	err func() error,
) ([][2]string, error) {
	var out [][2]string
	for {
		tok := next()
		if !ok() {
			return out, err()
		}
		if tok.IsEOF() {
			return out, nil
		}
		out = append(out, [2]string{tok.Kind().String(), string(tok.Value())})
	}
}

// TestWithoutAVX2Equivalence asserts the WithoutAVX2 knob is purely a performance switch: the token stream (kinds +
// values) is identical with the AVX2 gate on and forced off, for both the semantic lexer L and the verbatim lexer VL.
func TestWithoutAVX2Equivalence(t *testing.T) {
	doc := avx2Doc()

	t.Run("semantic L", func(t *testing.T) {
		on := NewWithBytes(doc)
		off := NewWithBytes(doc, WithoutAVX2(true))
		gotOn, errOn := collectKV(on.NextToken, on.Ok, on.Err)
		gotOff, errOff := collectKV(off.NextToken, off.Ok, off.Err)
		require.NoError(t, errOn)
		require.NoError(t, errOff)
		require.Equal(t, gotOn, gotOff)
		require.NotEmpty(t, gotOn)
	})

	t.Run("verbatim VL", func(t *testing.T) {
		on := NewVerbatimWithBytes(doc)
		off := NewVerbatimWithBytes(doc, WithoutAVX2(true))
		gotOn, errOn := collectKV(on.NextToken, on.Ok, on.Err)
		gotOff, errOff := collectKV(off.NextToken, off.Ok, off.Err)
		require.NoError(t, errOn)
		require.NoError(t, errOff)
		require.Equal(t, gotOn, gotOff)
		require.NotEmpty(t, gotOn)
	})
}

// ---- merged from elide_test.go ----
func collectKinds(lex *L) ([]token.Kind, error) {
	var kinds []token.Kind
	for {
		tok := lex.NextToken()
		if !lex.Ok() {
			return kinds, lex.Err()
		}
		if tok.IsEOF() {
			return kinds, nil
		}
		kinds = append(kinds, tok.Kind())
	}
}

func TestElideSeparator(t *testing.T) {
	const doc = `{"a": "x", "b": 123, "c": [1, 2]}`

	t.Run("default elides comma and colon", func(t *testing.T) {
		kinds, err := collectKinds(NewWithBytes([]byte(doc)))
		require.NoError(t, err)

		// { Key Str Key Num Key [ Num ] }  -- no Delimiter for "," or ":"
		want := []token.Kind{
			token.Delimiter, // {
			token.Key, token.String,
			token.Key, token.Number,
			token.Key,
			token.Delimiter, // [
			token.Number, token.Number,
			token.Delimiter, // ]
			token.Delimiter, // }
		}
		require.Equal(t, want, kinds)
	})

	t.Run("no comma or colon delimiter is emitted", func(t *testing.T) {
		lex := NewWithBytes([]byte(doc))
		for {
			tok := lex.NextToken()
			if tok.IsEOF() {
				break
			}
			require.False(t, tok.IsComma(), "comma must be elided")
			require.False(t, tok.IsColon(), "colon must be elided")
		}
		require.NoError(t, lex.Err())
	})

	t.Run("WithElideSeparator(false) keeps separators", func(t *testing.T) {
		kinds, err := collectKinds(NewWithBytes([]byte(doc), WithElideSeparator(false)))
		require.NoError(t, err)

		commas, colons := 0, 0
		lex := NewWithBytes([]byte(doc), WithElideSeparator(false))
		for {
			tok := lex.NextToken()
			if tok.IsEOF() {
				break
			}
			if tok.IsComma() {
				commas++
			}
			if tok.IsColon() {
				colons++
			}
		}
		assert.Equal(t, 3, commas)
		assert.Equal(t, 3, colons)
		assert.Greater(t, len(kinds), 11) // more tokens than the elided stream
	})

	t.Run("grammar errors still fire under elision", func(t *testing.T) {
		_, err := collectKinds(NewWithBytes([]byte(`[1,,2]`)))
		require.ErrorIs(t, err, codes.ErrRepeatedComma)

		_, err = collectKinds(NewWithBytes([]byte(`[1,2,]`)))
		require.ErrorIs(t, err, codes.ErrTrailingComma)

		_, err = collectKinds(NewWithBytes([]byte(`{"a" 1}`)))
		require.ErrorIs(t, err, codes.ErrKeyColon)
	})

	t.Run("iterator honors elision", func(t *testing.T) {
		lex := NewWithBytes([]byte(doc))
		for tok := range lex.Tokens() {
			require.False(t, tok.IsComma())
			require.False(t, tok.IsColon())
		}
		require.NoError(t, lex.Err())
	})
}

func TestVerbatimElideSeparator(t *testing.T) {
	const doc = `{"a": 1, "b": 2}`

	countSeps := func(vl *VL) (commas, colons int) {
		for {
			tok := vl.NextToken()
			if tok.IsEOF() {
				break
			}
			if tok.IsComma() {
				commas++
			}
			if tok.IsColon() {
				colons++
			}
		}
		require.NoError(t, vl.Err())

		return
	}

	t.Run("default keeps every separator (round-trippable)", func(t *testing.T) {
		commas, colons := countSeps(NewVerbatimWithBytes([]byte(doc)))
		assert.Equal(t, 1, commas)
		assert.Equal(t, 2, colons)
	})

	t.Run("caller may opt into elision on the verbatim lexer", func(t *testing.T) {
		commas, colons := countSeps(NewVerbatimWithBytes([]byte(doc), WithElideSeparator(true)))
		assert.Equal(t, 0, commas)
		assert.Equal(t, 0, colons)
	})
}

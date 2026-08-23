// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package lexer

import (
	"strings"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	"github.com/go-openapi/core/json/lexers/token"
)

// nextOfKind advances the lexer until it returns a token of the given kind.
func nextOfKind(t *testing.T, lex *L, kind token.Kind) token.T {
	t.Helper()
	for {
		tok := lex.NextToken()
		require.True(t, lex.Ok(), "unexpected error: %v", lex.Err())
		require.False(t, tok.IsEOF(), "reached EOF before a %v token", kind)
		if tok.Kind() == kind {
			return tok
		}
	}
}

// Zero-copy applies to numbers only (in whole-buffer mode); strings are always copied into currentValue (they may need
// escape rewriting).
func TestZeroCopyNumbers(t *testing.T) {
	t.Run("bytes mode: number value aliases the input buffer", func(t *testing.T) {
		data := []byte(`[12345]`) // digits at indices 1..5
		lex := NewWithBytes(data)

		tok := nextOfKind(t, lex, token.Number)
		require.Equal(t, "12345", string(tok.Value()))

		data[1] = '9'
		assert.Equal(t, "92345", string(tok.Value()), "number value should alias the input")
	})

	t.Run("bytes mode: aliased number has cap == len (append-safe)", func(t *testing.T) {
		data := []byte(`[12345]`)
		lex := NewWithBytes(data)

		tok := nextOfKind(t, lex, token.Number)
		v := tok.Value()
		require.Equal(t, "12345", string(v))
		assert.Equal(t, len(v), cap(v), "aliased value must have cap == len so append reallocates")

		_ = append(v, '9')
		assert.Equal(t, `[12345]`, string(data), "append to value must not scribble into the input")
	})

	t.Run("bytes mode: unescaped string aliases the input", func(t *testing.T) {
		// since phase 2 stage 1, unescaped strings are zero-copy in bytes mode
		data := []byte(`["hello"]`)
		lex := NewWithBytes(data)

		tok := nextOfKind(t, lex, token.String)
		require.Equal(t, "hello", string(tok.Value()))

		data[2] = 'J' // 'h'
		assert.Equal(t, "Jello", string(tok.Value()), "unescaped string should alias the input")
	})

	t.Run("escaped string falls back to copy and decodes correctly", func(t *testing.T) {
		data := []byte(`["a\nb\"cé"]`)
		lex := NewWithBytes(data)
		tok := nextOfKind(t, lex, token.String)
		require.Equal(t, "a\nb\"cé", string(tok.Value()))

		// an escaped string is copied into currentValue, not aliased
		clone := string(tok.Value())
		for i := range data {
			data[i] = '?'
		}
		assert.Equal(t, "a\nb\"cé", clone)
	})

	t.Run("streaming: number is copied and correct across buffer boundaries", func(t *testing.T) {
		// a long number spanning several 4-byte buffer fills exercises the flush-on-refill path.
		lex := New(strings.NewReader(`[1234567890123456789,42]`), WithBufferSize(4))

		n1 := nextOfKind(t, lex, token.Number)
		assert.Equal(t, "1234567890123456789", string(n1.Value()))

		n2 := nextOfKind(t, lex, token.Number)
		assert.Equal(t, "42", string(n2.Value()))
	})

	t.Run("streaming: float with exponent across boundaries", func(t *testing.T) {
		lex := New(strings.NewReader(`[-3.14159e+10]`), WithBufferSize(3))
		n := nextOfKind(t, lex, token.Number)
		assert.Equal(t, "-3.14159e+10", string(n.Value()))
	})
}

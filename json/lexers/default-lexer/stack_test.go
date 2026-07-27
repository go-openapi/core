package lexer

import (
	"strings"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	codes "github.com/go-openapi/core/json/lexers/error-codes"
	"github.com/go-openapi/core/json/lexers/token"
)

// drainOK runs a lexer over s and reports whether the document was accepted (EOF reached with no error) along with the
// number of non-EOF tokens.
func drainOK(s string) (bool, int, error) {
	return drainLexer(NewWithBytes([]byte(s)))
}

func drainLexer(lex *L) (bool, int, error) {
	n := 0
	for n < 1<<16 {
		tok := lex.NextToken()
		if !lex.Ok() {
			return false, n, lex.Err()
		}
		if tok.IsEOF() {
			return true, n, nil
		}
		n++
	}

	return true, n, nil
}

func TestDeepNesting(t *testing.T) {
	// Regression: balanced nesting deeper than one stack word (stackScale=63) used to be rejected at the word boundary.
	// It must parse at any depth.
	t.Run("balanced arrays across word boundaries", func(t *testing.T) {
		for _, depth := range []int{1, 62, 63, 64, 65, 126, 127, 128, 500, 1000} {
			s := strings.Repeat("[", depth) + strings.Repeat("]", depth)

			t.Run("L", func(t *testing.T) {
				ok, n, err := drainOK(s)
				require.NoErrorf(t, err, "depth=%d", depth)
				assert.Truef(t, ok, "depth=%d", depth)
				assert.Equalf(t, 2*depth, n, "depth=%d", depth)
			})

			t.Run("VL", func(t *testing.T) {
				vl := NewVerbatimWithBytes([]byte(s))
				n := 0
				for n < 1<<16 {
					tok := vl.NextToken()
					if !vl.Ok() || tok.IsEOF() {
						break
					}
					n++
				}
				require.NoErrorf(t, vl.Err(), "depth=%d", depth)
				assert.Equalf(t, 2*depth, n, "depth=%d", depth)
			})
		}
	})

	t.Run("mixed objects and arrays across word boundary", func(t *testing.T) {
		// 70 alternating containers: deeper than one word, exercising both type bits.
		const depth = 70
		var b strings.Builder
		for i := range depth {
			if i%2 == 0 {
				b.WriteString(`{"k":`)
			} else {
				b.WriteByte('[')
			}
		}
		for i := depth - 1; i >= 0; i-- {
			if i%2 == 0 {
				b.WriteByte('}')
			} else {
				b.WriteByte(']')
			}
		}

		ok, _, err := drainOK(b.String())
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("unbalanced deep array is rejected", func(t *testing.T) {
		// one missing closing bracket past the word boundary
		s := strings.Repeat("[", 100) + strings.Repeat("]", 99)
		ok, _, err := drainOK(s)
		assert.False(t, ok)
		require.ErrorIs(t, err, codes.ErrNotInArray)
	})
}

func TestIndentLevelDeep(t *testing.T) {
	// Regression: a value at a depth that is an exact multiple of the word size (stackScale) used to report the wrong
	// IndentLevel, because the trailing closing delimiter consumed by the look-ahead dropped a stack word.
	for _, depth := range []int{3, 62, 63, 64, 65, 126, 127, 128} {
		s := strings.Repeat("[", depth) + "1" + strings.Repeat("]", depth)
		lex := NewWithBytes([]byte(s))

		var got int
		for {
			tok := lex.NextToken()
			require.True(t, lex.Ok())
			if tok.Kind() == token.Number {
				got = lex.IndentLevel()
				break
			}
			if tok.IsEOF() {
				break
			}
		}
		assert.Equalf(t, depth, got, "value IndentLevel at nesting depth %d", depth)
	}
}

func TestMaxContainerStack(t *testing.T) {
	// WithMaxContainerStack caps nesting depth as a circuit breaker.
	const maxDepth = 5

	t.Run("at the limit is accepted", func(t *testing.T) {
		s := strings.Repeat("[", maxDepth) + strings.Repeat("]", maxDepth)
		lex := NewWithBytes([]byte(s), WithMaxContainerStack(maxDepth))
		ok, _, err := drainLexer(lex)
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("beyond the limit trips the breaker", func(t *testing.T) {
		s := strings.Repeat("[", maxDepth+1) + strings.Repeat("]", maxDepth+1)
		lex := NewWithBytes([]byte(s), WithMaxContainerStack(maxDepth))
		ok, _, err := drainLexer(lex)
		assert.False(t, ok)
		require.ErrorIs(t, err, codes.ErrMaxContainerStack)
	})
}

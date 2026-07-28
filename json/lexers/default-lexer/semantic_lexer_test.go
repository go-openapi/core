package lexer

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	codes "github.com/go-openapi/core/json/lexers/error-codes"
	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/swag/pools"
)

func TestNextToken(t *testing.T) {
	t.Run("with reader", testNextToken(getLexer))
	t.Run("with buffer", testNextToken(getLexerWithBytes))
}

func testNextToken(newLexer func(string) *L) func(*testing.T) {
	return func(t *testing.T) {
		t.Run("happy path", func(t *testing.T) {
			t.Run("with object", func(t *testing.T) {
				lex := newLexer(`{"a": "x", "b": 123 , "c": true,  "d": null}`)

				var (
					token token.T
					i     int
				)

				for ; !token.IsEOF(); i++ {
					token = lex.NextToken()
					require.NoError(t, lex.Err())
					require.True(t, lex.Ok())
					require.Less(t, i, 20)
				}
				assert.Equal(t, 18, i)
			})

			t.Run("with array", func(t *testing.T) {
				lex := newLexer(`["a", "x", "b", 123 , "c", true,  "d", null]`)

				var (
					token token.T
					i     int
				)

				for ; !token.IsEOF(); i++ {
					token = lex.NextToken()
					require.NoError(t, lex.Err())
					require.True(t, lex.Ok())
					require.Less(t, i, 20)
				}
				assert.Equal(t, 18, i)
			})

			t.Run("with nested", func(t *testing.T) {
				lex := newLexer(
					`{"a": {"x": [1,2,3], "y": [true, false], "z": ["b", 123 , "c", true,  "d", null, {"w": true}]}}`,
				)

				var (
					token token.T
					i     int
				)

				for ; !token.IsEOF(); i++ {
					token = lex.NextToken()
					require.NoError(t, lex.Err())
					require.True(t, lex.Ok())
					require.Less(t, i, 100)
				}
			})
		})

		t.Run("error path", func(t *testing.T) {
			t.Run("invalid key", func(t *testing.T) {
				lex := newLexer(`{:1}`)

				token := lex.NextToken()
				require.NoError(t, lex.Err())
				require.True(t, lex.Ok())
				require.True(t, token.IsStartObject())

				_ = lex.NextToken()
				require.False(t, lex.Ok())
				require.Error(t, lex.Err())
				t.Logf("expected error: %v", lex.Err())
				errCtx := lex.ErrInContext()
				require.NotNil(t, errCtx)
				t.Logf("error context:\n%s", errCtx.Pretty(30))
			})

			t.Run("brackets imbalance", func(t *testing.T) {
				lex := newLexer(`{"a": {"z": {}}`)

				tok := lex.NextToken()
				require.NoError(t, lex.Err())
				require.True(t, lex.Ok())
				require.True(t, tok.IsStartObject())

				tok = lex.NextToken()
				require.NoError(t, lex.Err())
				require.True(t, lex.Ok())
				require.Equal(t, token.Key, tok.Kind())

				tok = lex.NextToken()
				require.NoError(t, lex.Err())
				require.True(t, lex.Ok())
				require.Equal(t, token.Delimiter, tok.Kind())

				tok = lex.NextToken()
				require.NoError(t, lex.Err())
				require.True(t, lex.Ok())
				require.True(t, tok.IsStartObject())

				tok = lex.NextToken()
				require.NoError(t, lex.Err())
				require.True(t, lex.Ok())
				require.Equal(t, token.Key, tok.Kind())

				tok = lex.NextToken()
				require.NoError(t, lex.Err())
				require.True(t, lex.Ok())
				require.Equal(t, token.Delimiter, tok.Kind())

				tok = lex.NextToken()
				require.NoError(t, lex.Err())
				require.True(t, lex.Ok())
				require.True(t, tok.IsStartObject())

				tok = lex.NextToken()
				require.NoError(t, lex.Err())
				require.True(t, lex.Ok())
				require.True(t, tok.IsEndObject())

				tok = lex.NextToken()
				require.NoError(t, lex.Err())
				require.True(t, lex.Ok())
				require.True(t, tok.IsEndObject())

				_ = lex.NextToken()
				require.False(t, lex.Ok())
				require.Error(t, lex.Err())
				t.Logf("expected error: %v", lex.Err())
			})

			t.Run("should error after EOF", func(t *testing.T) {
				lex := newLexer(`{}`) // ICI
				var tok token.T

				for !tok.IsEOF() {
					tok = lex.NextToken()
				}

				tok = lex.NextToken()
				require.False(t, lex.Ok())
				require.Equal(t, token.EOF, tok.Kind())
				require.ErrorIs(t, lex.Err(), io.EOF)
			})
		})
	}
}

func TestNumber(t *testing.T) {
	t.Run("with reader", testNumber(getLexer))
	t.Run("with buffer", testNumber(getLexerWithBytes))
}

func testNumber(newLexer func(string) *L) func(*testing.T) {
	return func(t *testing.T) {
		t.Run("happy path", func(t *testing.T) {
			assertNumberOK := func(t testing.TB, fixture string) {
				lex := newLexer(fixture)
				tok := lex.NextToken()
				require.NoError(t, lex.Err())
				require.True(t, lex.Ok())
				require.Equal(t, token.Number, tok.Kind())
				require.Equal(t, strings.TrimSpace(fixture), string(tok.Value()))
				require.True(t, lex.NextToken().IsEOF())
			}

			t.Run("with integer", func(t *testing.T) {
				assertNumberOK(t, `0`)
				assertNumberOK(t, `1230`)
				assertNumberOK(t, `-1230`)
			})

			t.Run("with fractional part", func(t *testing.T) {
				assertNumberOK(t, `0.01`)
				assertNumberOK(t, `1230.0001`)
				assertNumberOK(t, `1230.00`)
				assertNumberOK(t, `1230.99999`)
				assertNumberOK(t, `-1230.99999`)
				assertNumberOK(t, `0.0`)
			})

			t.Run("with exponent part", func(t *testing.T) {
				assertNumberOK(t, `1e00`)
				assertNumberOK(t, `1E00`)
				assertNumberOK(t, `1e001`)
				assertNumberOK(t, `-1e-001`)
				assertNumberOK(t, `123e123`)
				assertNumberOK(t, `-123E-123`)
				assertNumberOK(t, `0e0`)
				assertNumberOK(t, `-0e0`)
			})

			t.Run("with fractional and exponent part", func(t *testing.T) {
				assertNumberOK(t, `123.34e123`)
				assertNumberOK(t, `123.34e+123`)
				assertNumberOK(t, `-123.03490E-0123`)
				assertNumberOK(t, `0.0e0`)
			})

			t.Run("with blanks", func(t *testing.T) {
				assertNumberOK(t, `   123   `)
			})
		})

		t.Run("error path", func(t *testing.T) {
			// drain to error: with the look-ahead folded out, a valid value may be surfaced before trailing garbage is rejected
			// on the next token.
			assertNumberKO := func(t testing.TB, fixture string) {
				lex := newLexer(fixture)
				for {
					tok := lex.NextToken()
					if !lex.Ok() || tok.IsEOF() {
						break
					}
				}
				require.Falsef(t, lex.Ok(), "expected an error for %q", fixture)
				require.Error(t, lex.Err())
				t.Logf("expected error: %v", lex.Err())
			}

			t.Run("invalid integer (leading zero)", func(t *testing.T) {
				assertNumberKO(t, `00`)
				assertNumberKO(t, `-00`)
			})

			t.Run("invalid integer (leading +)", func(t *testing.T) {
				assertNumberKO(t, `+123`)
				assertNumberKO(t, `+0`)
			})

			t.Run("invalid fractional part (multiple .)", func(t *testing.T) {
				assertNumberKO(t, `123.456.789`)
			})

			t.Run("invalid fractional part (empty fractional)", func(t *testing.T) {
				assertNumberKO(t, `123.`)
			})

			t.Run("invalid integer part (empty integer)", func(t *testing.T) {
				assertNumberKO(t, `.123`)
			})

			t.Run("invalid exponent part (multiple e)", func(t *testing.T) {
				assertNumberKO(t, `123e456e789`)
			})

			t.Run("invalid exponent part (empty exponent)", func(t *testing.T) {
				assertNumberKO(t, `123.456e`)
				assertNumberKO(t, `123456e`)
			})

			t.Run("invalid exponent part (fractional exponent)", func(t *testing.T) {
				assertNumberKO(t, `123e456.789`)
			})

			t.Run("leading zeros", func(t *testing.T) {
				assertNumberKO(t, `01`)
				assertNumberKO(t, `-01`)
				assertNumberKO(t, `00e0`)
				assertNumberKO(t, `00.00e0`)
			})

			t.Run("interleaved blanks", func(t *testing.T) {
				assertNumberKO(t, `   123 234   `)
				assertNumberKO(t, `   123 .234 e56   `)
			})
		})
	}
}

func TestBoolean(t *testing.T) {
	t.Run("with reader", testBoolean(getLexer))
	t.Run("with buffer", testBoolean(getLexerWithBytes))
}

func testBoolean(newLexer func(string) *L) func(t *testing.T) {
	return func(t *testing.T) {
		t.Run("happy path", func(t *testing.T) {
			assertBooleanOK := func(t testing.TB, fixture string) {
				lex := newLexer(fixture)
				tok := lex.NextToken()
				require.NoError(t, lex.Err())
				require.True(t, lex.Ok())
				require.Equal(t, token.Boolean, tok.Kind())
				expected, err := strconv.ParseBool(strings.TrimSpace(fixture))
				require.NoError(t, err)
				require.Equal(t, expected, tok.Bool())
				require.True(t, lex.NextToken().IsEOF())
			}

			t.Run("true", func(t *testing.T) {
				assertBooleanOK(t, `true`)
				assertBooleanOK(t, `true   `)
			})

			t.Run("false", func(t *testing.T) {
				assertBooleanOK(t, `false`)
				assertBooleanOK(t, `false `)
			})
		})

		t.Run("error path", func(t *testing.T) {
			// drain to error: a valid literal may be surfaced before trailing garbage is rejected on the next token (look-ahead
			// folded out).
			assertBooleanKO := func(t testing.TB, fixture string) {
				lex := newLexer(fixture)
				for {
					tok := lex.NextToken()
					if !lex.Ok() || tok.IsEOF() {
						break
					}
				}
				require.Falsef(t, lex.Ok(), "expected an error for %q", fixture)
				require.Error(t, lex.Err())
				t.Logf("expected error: %v", lex.Err())
			}

			t.Run("True", func(t *testing.T) {
				assertBooleanKO(t, `True`)
				assertBooleanKO(t, `truth`)
				assertBooleanKO(t, `truthy`)
				assertBooleanKO(t, `trueth`)
			})

			t.Run("False", func(t *testing.T) {
				assertBooleanKO(t, `False`)
				assertBooleanKO(t, `falsy`)
				assertBooleanKO(t, `falseth`)
			})
		})
	}
}

func TestString(t *testing.T) {
	t.Run("with reader", testString(getLexer))
	t.Run("with buffer", testString(getLexerWithBytes))
}

func testString(newLexer func(string) *L) func(*testing.T) {
	return func(t *testing.T) {
		t.Run("happy path", func(t *testing.T) {
			t.Run("simple string", func(t *testing.T) {
				const fixture = `"plain"  `
				lex := newLexer(fixture)
				tok := lex.NextToken()
				require.NoError(t, lex.Err())
				require.True(t, lex.Ok())
				require.Equal(t, token.String, tok.Kind())
				require.Equal(t, "plain", string(tok.Value()))
			})

			t.Run("blank spaces", func(t *testing.T) {
				const fixture = `"   "  `
				lex := newLexer(fixture)
				tok := lex.NextToken()
				require.NoError(t, lex.Err())
				require.True(t, lex.Ok())
				require.Equal(t, token.String, tok.Kind())
				require.Equal(t, "   ", string(tok.Value()))
			})

			t.Run("escaped string", func(t *testing.T) {
				const fixture = `  "plain\"x\\y\/z\n1\t2\r3\b4\f"  `
				lex := newLexer(fixture)
				tok := lex.NextToken()
				require.NoError(t, lex.Err())
				require.True(t, lex.Ok())
				require.Equal(t, token.String, tok.Kind())
				require.Equal(t, "plain\"x\\y/z\n1\t2\r3\b4\f", string(tok.Value()))
			})

			t.Run("escaped unicode string", func(t *testing.T) {
				const fixture = `  "plain\u005C" `
				lex := newLexer(fixture)
				tok := lex.NextToken()
				require.NoError(t, lex.Err())
				require.True(t, lex.Ok())
				require.Equal(t, token.String, tok.Kind())
				require.Equal(t, "plain\\", string(tok.Value()))
			})

			t.Run("escaped UTF-16 unicode point", func(t *testing.T) {
				const fixture = `  "plain\ud834\udd1e" `
				lex := newLexer(fixture)
				tok := lex.NextToken()
				require.NoError(t, lex.Err())
				require.True(t, lex.Ok())
				require.Equal(t, token.String, tok.Kind())
				require.Equal(t, "plain𝄞", string(tok.Value()))
			})
		})

		t.Run("error path", func(t *testing.T) {
			t.Run("unknown escape sequence", func(t *testing.T) {
				const fixture = `  "plain\x" `
				lex := newLexer(fixture)
				_ = lex.NextToken()
				require.False(t, lex.Ok())
				require.Error(t, lex.Err())
				t.Logf("expected error: %v", lex.Err())
			})

			t.Run("wrong escaped unicode string", func(t *testing.T) {
				const fixture = `  "plain\u005X" `
				lex := newLexer(fixture)
				_ = lex.NextToken()
				require.False(t, lex.Ok())
				require.Error(t, lex.Err())
				t.Logf("expected error: %v", lex.Err())
			})

			t.Run("invalid rune in escape sequence (1)", func(t *testing.T) {
				const fixture = `  "plain\uD800" ` // this is the start of a surrogate pair
				lex := newLexer(fixture)
				_ = lex.NextToken()
				require.False(t, lex.Ok())
				require.Error(t, lex.Err())
				t.Logf("expected error: %v", lex.Err())
			})
		})
	}
}

func TestIndentLevel(t *testing.T) {
	t.Run("indent should be 0", func(t *testing.T) {
		lex := getLexer(`true`)
		_ = lex.NextToken()
		require.True(t, lex.Ok())
		require.Equal(t, 0, lex.IndentLevel())
	})

	t.Run("indent sequence", func(t *testing.T) {
		lex := getLexer(`[{"a":[{"b":1}]}]`)
		tok := lex.NextToken() // [
		require.True(t, lex.Ok())
		require.Equalf(t, 1, lex.IndentLevel(), "token: %v", tok)

		tok = lex.NextToken() // {
		require.True(t, lex.Ok(), "token: %v", tok)
		require.Equalf(t, 2, lex.IndentLevel(), "token: %v", tok)

		tok = lex.NextToken() // "a"
		require.True(t, lex.Ok(), "token: %v", tok)
		require.Equalf(t, 2, lex.IndentLevel(), "token: %v", tok)

		tok = lex.NextToken() // :
		require.True(t, lex.Ok(), "token: %v", tok)
		require.Equalf(t, 2, lex.IndentLevel(), "token: %v", tok)

		tok = lex.NextToken() // [
		require.True(t, lex.Ok(), "token: %v", tok)
		require.Equalf(t, 3, lex.IndentLevel(), "token: %v", tok)

		tok = lex.NextToken() // {
		require.True(t, lex.Ok(), "token: %v", tok)
		require.Equalf(t, 4, lex.IndentLevel(), "token: %v", tok)

		tok = lex.NextToken() // "b"
		require.True(t, lex.Ok(), "token: %v", tok)
		require.Equalf(t, 4, lex.IndentLevel(), "token: %v", tok)

		tok = lex.NextToken() // ":"
		require.True(t, lex.Ok(), "token: %v", tok)
		require.Equalf(t, 4, lex.IndentLevel(), "token: %v", tok)

		tok = lex.NextToken() // 1
		require.True(t, lex.Ok(), "token: %v", tok)
		require.Equalf(t, 4, lex.IndentLevel(), "token: %v", tok)

		tok = lex.NextToken() // }
		require.True(t, lex.Ok(), "token: %v", tok)
		require.Equalf(t, 3, lex.IndentLevel(), "token: %v", tok)

		tok = lex.NextToken() // ]
		require.True(t, lex.Ok(), "token: %v", tok)
		require.Equalf(t, 2, lex.IndentLevel(), "token: %v", tok)

		tok = lex.NextToken() // }
		require.True(t, lex.Ok(), "token: %v", tok)
		require.Equalf(t, 1, lex.IndentLevel(), "token: %v", tok)

		tok = lex.NextToken() // ]
		require.True(t, lex.Ok(), "token: %v", tok)
		require.Equal(t, 0, lex.IndentLevel())

		tok = lex.NextToken() // ]
		require.True(t, lex.Ok())
		require.Equal(t, token.EOF, tok.Kind())
		require.Equal(t, 0, lex.IndentLevel())
	})
}

func TestGrammar(t *testing.T) {
	t.Run("should be correct", shouldBeCorrect(`[{}]`))
	t.Run("should be correct", shouldBeCorrect(`[{"a": 1}]`))
	t.Run("should be correct", shouldBeCorrect(`["x", {"a": 1}]`))
	t.Run("should be correct", shouldBeCorrect(`{"x":{"a": 1}}`))
	t.Run("should be correct", shouldBeCorrect(`{"x":{}}`))
	t.Run("should be correct", shouldBeCorrect(`[true]`))
	t.Run("should be correct", shouldBeCorrect(`[4,5]`))
	t.Run("should be correct", shouldBeCorrect(`[null]`))
	t.Run("should be correct", shouldBeCorrect(`true`))
	t.Run("should be correct", shouldBeCorrect(`null`))
	t.Run("should be correct", shouldBeCorrect(`4`))
	t.Run("should be correct", shouldBeCorrect(`[{},{}]`))

	t.Run("should be incorrect", shouldBeIncorrect(`{{}}`, codes.ErrMissingKey))
	t.Run("should be incorrect", shouldBeIncorrect(`{true:{}}`, codes.ErrMissingKey))
	t.Run("should be incorrect", shouldBeIncorrect(`{1:{}}`, codes.ErrMissingKey))
	t.Run("should be incorrect", shouldBeIncorrect(`{"x"{}}`, codes.ErrKeyColon))
	t.Run("should be incorrect", shouldBeIncorrect(`["x" {"a": 1}]`, codes.ErrInvalidToken))
	t.Run("should be incorrect", shouldBeIncorrect(`["x":{"a": 1}]`, codes.ErrMissingObject))
	t.Run("should be incorrect", shouldBeIncorrect(`[{"a": 1}]}`, codes.ErrNotInObject))
	t.Run("should be incorrect", shouldBeIncorrect(`[{"a": 1}]]`, codes.ErrNotInArray))
	t.Run("should be incorrect", shouldBeIncorrect(`[{"a": 1}],]`, codes.ErrCommaInContainer))
	t.Run("should be incorrect", shouldBeIncorrect(`[{"a": 1},]`, codes.ErrTrailingComma))
	t.Run("should be incorrect", shouldBeIncorrect(`{true}`, codes.ErrMissingKey))
	t.Run("should be incorrect", shouldBeIncorrect(`{4}`, codes.ErrMissingKey))
	t.Run("should be incorrect", shouldBeIncorrect(`{"x"}`, codes.ErrKeyColon))
	t.Run("should be incorrect", shouldBeIncorrect(`{null}`, codes.ErrMissingKey))
	t.Run("should be incorrect", shouldBeIncorrect(`()`, codes.ErrInvalidToken))
	t.Run("should be incorrect", shouldBeIncorrect(`"abc`, codes.ErrUnterminatedString))
	t.Run("should be incorrect", shouldBeIncorrect(`{"x":{},}`, codes.ErrTrailingComma))
	t.Run("should be incorrect", shouldBeIncorrect(`{"x":{},,{}}`, codes.ErrRepeatedComma))
	t.Run("should be incorrect", shouldBeIncorrect(`[{}:{}]`, codes.ErrMissingKey))
	t.Run("should be incorrect", shouldBeIncorrect(`[{},null {}]`, codes.ErrInvalidToken))
	t.Run("should be incorrect", shouldBeIncorrect(`{"x":{},}`, codes.ErrTrailingComma))
	t.Run("should be incorrect", shouldBeIncorrect(`{"x":{},"y"}`, codes.ErrKeyColon))
	t.Run("should be incorrect", shouldBeIncorrect(`{"x":}`, codes.ErrColonValue))
	t.Run("should be incorrect", shouldBeIncorrect(`{"x" : }`, codes.ErrColonValue))
	t.Run("should be incorrect", shouldBeIncorrect(`{"x":1,"y":}`, codes.ErrColonValue))
	t.Run("should be incorrect", shouldBeIncorrect(`{"x":{"y":}}`, codes.ErrColonValue))
	t.Run("should be incorrect", shouldBeIncorrect(`[{"x":}]`, codes.ErrColonValue))
}

func shouldBeCorrect(s string) func(*testing.T) {
	return func(t *testing.T) {
		var (
			tok token.T
			i   int
		)
		lex := getLexer(s)

		for ; !tok.IsEOF(); i++ {
			tok = lex.NextToken()
			require.NoError(t, lex.Err())
			require.True(t, lex.Ok())
		}
	}
}

func shouldBeIncorrect(s string, err error) func(*testing.T) {
	return func(t *testing.T) {
		var (
			tok token.T
			i   int
		)
		lex := getLexer(s)

		for ; !tok.IsEOF(); i++ {
			tok = lex.NextToken()
			if !lex.Ok() {
				break
			}
		}
		require.Error(t, lex.Err(), "expected an error but got none, for input %q", s)
		require.ErrorIsf(t, lex.Err(), err,
			"expected error to be %v, but got %v instead\n%s",
			err, lex.Err(), lex.ErrInContext().Pretty(10),
		)
	}
}

func TestExample(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join(currentDir(), "testdata", "example.json"))
	require.NoError(t, err)

	t.Run("with reader", testFixture(func() *L {
		rdr := bytes.NewBuffer(fixture)
		return New(rdr, WithBufferSize(50))
	}))

	t.Run("with buffer", testFixture(func() *L {
		return NewWithBytes(fixture)
	}))
}

func testFixture(newLexer func() *L) func(*testing.T) {
	return func(t *testing.T) {
		lex := newLexer()

		var (
			i   int
			tok token.T
		)

		for ; !tok.IsEOF(); i++ {
			tok = lex.NextToken()
			if !assert.NoError(t, lex.Err()) {
				errCtx := lex.ErrInContext()
				require.NotNil(t, errCtx)
				t.Logf("unexpected error: %#v", errCtx)
				t.Logf("\n%s", errCtx.Pretty(50))
			}
			require.True(t, lex.Ok())
			t.Logf("-> %v", tok)
		}

		t.Logf("split %d tokens", i)
	}
}

// getLexer / getLexerWithBytes build a lexer that emits the full token stream, including the "," and ":" separators.
//
// The package default is to elide them (see WithElideSeparator); these helpers opt out so the tests below can assert on
// the complete, raw token sequence.
// The default elision behavior is covered by TestElideSeparator.
func getLexer(fixture string) *L {
	rdr := bytes.NewBufferString(fixture)

	return New(rdr, WithElideSeparator(false))
}

func getLexerWithBytes(fixture string) *L {
	return NewWithBytes([]byte(fixture), WithElideSeparator(false))
}

func TestResetWithBytesReuse(t *testing.T) {
	// drain a lexer into a comparable list of tokens (kind + value).
	//
	// The semantic lexer no longer tracks line/column (position is a verbatim-lexer concern); kind+value is enough to
	// prove ResetWithBytes clears carried-over state.
	type tok struct {
		kind  token.Kind
		value string
	}
	drainAll := func(l *L) ([]tok, error) {
		var out []tok
		for {
			tk := l.NextToken()
			if !l.Ok() {
				return out, l.Err()
			}
			if tk.IsEOF() {
				return out, nil
			}
			out = append(out, tok{tk.Kind(), string(tk.Value())})
		}
	}

	docA := []byte(`{"a":[1,-2,3.5e2],"b":true}`)
	docB := []byte("[null,\n\"x\",0.5]")

	// reference token streams from fresh lexers
	wantA, errA := drainAll(NewWithBytes(docA, WithElideSeparator(false)))
	require.NoError(t, errA)
	wantB, errB := drainAll(NewWithBytes(docB, WithElideSeparator(false)))
	require.NoError(t, errB)

	// a single reused lexer must reproduce both streams exactly, in any order, proving ResetWithBytes clears all
	// carried-over scanning state.
	l := NewWithBytes(docA, WithElideSeparator(false))
	gotA1, err := drainAll(l)
	require.NoError(t, err)
	require.Equal(t, wantA, gotA1)

	l.ResetWithBytes(docB)
	gotB, err := drainAll(l)
	require.NoError(t, err)
	require.Equal(t, wantB, gotB)

	l.ResetWithBytes(docA)
	gotA2, err := drainAll(l)
	require.NoError(t, err)
	require.Equal(t, wantA, gotA2)
}

func TestPoolReuse(t *testing.T) {
	// drain a lexer into a comparable list of tokens (kind + value)
	type tok struct {
		kind  token.Kind
		value string
	}
	drainAll := func(l *L) ([]tok, error) {
		var out []tok
		for {
			tk := l.NextToken()
			if !l.Ok() {
				return out, l.Err()
			}
			if tk.IsEOF() {
				return out, nil
			}
			out = append(out, tok{tk.Kind(), string(tk.Value())})
		}
	}

	docA := []byte(
		`{"a":[1,-2,3.5e2],"b":true}`,
	) // < bufferSize: exercises the grow-on-reset alloc bug
	docB := []byte(`[null,"x",0.5]`)

	wantA, errA := drainAll(NewWithBytes(docA))
	require.NoError(t, errA)
	wantB, errB := drainAll(NewWithBytes(docB))
	require.NoError(t, errB)

	t.Run("borrow/redeem reproduces streams", func(t *testing.T) {
		for range 3 {
			l, redeem := BorrowLexerWithBytes(docA)
			got, err := drainAll(l)
			require.NoError(t, err)
			require.Equal(t, wantA, got)
			redeem()

			l, redeem = BorrowLexerWithBytes(docB)
			got, err = drainAll(l)
			require.NoError(t, err)
			require.Equal(t, wantB, got)
			redeem()
		}
	})

	t.Run("Reset drops the caller's buffer (no pinning)", func(t *testing.T) {
		l := NewWithBytes(docA)
		_, err := drainAll(l)
		require.NoError(t, err)
		require.NotNil(t, l.in.Buffer) // still aliasing docA while in use

		l.Reset()
		require.Nil(
			t,
			l.in.Buffer,
			"Reset must drop the aliased caller buffer so the pool does not pin user memory",
		)
		require.False(t, l.in.WholeBuffer)
		require.Equal(t, noopReader, l.in.R, "Reset must drop the caller's reader")
	})

	t.Run("Reset keeps the streaming-owned buffer", func(t *testing.T) {
		l := New(bytes.NewReader(docA))
		_, err := drainAll(l)
		require.NoError(t, err)

		l.Reset()
		require.NotNil(t, l.in.Buffer, "streaming buffer is lexer-owned and must be kept for reuse")
		require.GreaterOrEqual(t, cap(l.in.Buffer), l.bufferSize)
	})

	// NOTE: the "borrow/redeem cycle is allocation-free" assertion lives in lexer_norace_test.go (build tag !race):
	// testing.AllocsPerRun is unreliable under -race (the detector instruments allocations), so it is enforced only in the
	// normal build, which reflects production — mirrors writer_norace_test.go.

	// In the poolsdebug build, assert the pool tracker recorded no leaked borrows from this test (a no-op in release
	// builds).
	t.Cleanup(func() { pools.AssertNoLeaks(t) })
}

func currentDir() string {
	_, filename, _, _ := runtime.Caller( //nolint:dogsled // no issue there: we just need the file name
		1,
	)

	return filepath.Dir(filename)
}

func TestAllTokens(t *testing.T) {
	const jazon = `{"test": [null,1,2,"a","x\n\t\r"]}`
	r := bytes.NewBufferString(jazon)
	l := New(r)
	for {
		tok := l.NextToken()
		if tok.IsEOF() {
			break
		}
		t.Logf("tok: %v", tok)
	}
	require.NoError(t, l.Err())
}

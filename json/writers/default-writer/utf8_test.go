package writer

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	"github.com/go-openapi/core/json/internal/utf8x"
	lexer "github.com/go-openapi/core/json/lexers/default-lexer"
	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/core/json/stores/values"
)

// UTF-8 handling on the way OUT (see .claude/plans/utf8-validation.md, round 3).
//
// Before the policy existed the writers substituted U+FFFD for ill-formed input silently and unconditionally, with
// one inconsistency: a rune truncated at the very end of a StringCopy errored while every other fault was rewritten.
// These tests pin the three policies across all four writers and every entry point, and — because the same
// substitution rule is now claimed on both sides — cross-check the output against the lexer's utf8x.Sanitize.

// utf8WriterCase is an input and what each policy must make of it.
type utf8WriterCase struct {
	name string
	in   string
	// valid inputs are written unchanged by every policy.
	valid bool
}

func utf8WriterCases() []utf8WriterCase {
	return []utf8WriterCase{
		{name: "ascii", in: "plain text", valid: true},
		{name: "2-byte", in: "café", valid: true},
		{name: "3-byte", in: "\u65e5\u672c\u8a9e", valid: true},
		{name: "4-byte", in: "\U0001D11E", valid: true},
		{name: "needs escaping", in: "a\"b\\c\nd\te", valid: true},
		{name: "control chars", in: "a\x00b\x1fc", valid: true},

		{name: "lone ff", in: "\xff"},
		{name: "lone continuation", in: "a\x80b"},
		{name: "overlong", in: "a\xc0\xafb"},
		{name: "encoded surrogate", in: "a\xed\xa0\x80b"},
		{name: "beyond U+10FFFF", in: "a\xf4\xbf\xbf\xbfb"},
		{name: "truncated at end", in: "abc\xe2\x82"},
		{name: "truncated then text", in: "a\xe0\xffb"},
		{name: "invalid amid escapes", in: "a\"b\xffc\nd"},
	}
}

// utf8Writer is one writer implementation configured with a policy, reduced to what the tests need.
type utf8Writer struct {
	name  string
	build func(w io.Writer, policy UTF8Policy) utf8WriterUnderTest
}

type utf8WriterUnderTest interface {
	String(string)
	StringBytes([]byte)
	StringRunes([]rune)
	StringCopy(io.Reader)
	Raw([]byte)
	RawCopy(io.Reader)
	Token(token.T)
	VerbatimToken([]byte, token.T)
	Err() error
}

// flusher is implemented by the buffered writers; the unbuffered one needs no flush.
type flusher interface{ Flush() error }

func utf8Writers() []utf8Writer {
	return []utf8Writer{
		{name: "Unbuffered", build: func(w io.Writer, p UTF8Policy) utf8WriterUnderTest {
			return NewUnbuffered(w, WithUnbufferedUTF8Policy(p))
		}},
		{name: "Buffered", build: func(w io.Writer, p UTF8Policy) utf8WriterUnderTest {
			return NewBuffered(w, WithUTF8Policy(p))
		}},
		{name: "Buffered/tiny", build: func(w io.Writer, p UTF8Policy) utf8WriterUnderTest {
			// a buffer smaller than the values forces flushes in the middle of escaping
			return NewBuffered(w, WithUTF8Policy(p), WithBufferSize(minBufferSize))
		}},
		{name: "Indented", build: func(w io.Writer, p UTF8Policy) utf8WriterUnderTest {
			return NewIndented(w, WithIndentBufferedOptions(WithUTF8Policy(p)))
		}},
		{name: "YAML", build: func(w io.Writer, p UTF8Policy) utf8WriterUnderTest {
			return NewYAML(w, WithYAMLBufferedOptions(WithUTF8Policy(p)))
		}},
	}
}

// writeWith runs one entry point and returns what reached the underlying writer.
func writeWith(
	t *testing.T,
	wr utf8Writer,
	policy UTF8Policy,
	in string,
	call func(utf8WriterUnderTest, string),
) (string, error) {
	t.Helper()

	var buf bytes.Buffer
	w := wr.build(&buf, policy)
	call(w, in)
	if f, ok := w.(flusher); ok {
		_ = f.Flush()
	}

	return buf.String(), w.Err()
}

// entryPoints are the caller-supplied ways in. Each is subject to the policy.
func entryPoints() []struct {
	name    string
	quoted  bool // the output is a quoted, escaped JSON string
	call    func(utf8WriterUnderTest, string)
	skipFor func(utf8WriterCase) bool
} {
	return []struct {
		name    string
		quoted  bool
		call    func(utf8WriterUnderTest, string)
		skipFor func(utf8WriterCase) bool
	}{
		{name: "String", quoted: true, call: func(w utf8WriterUnderTest, s string) { w.String(s) }},
		{
			name:   "StringBytes",
			quoted: true,
			call:   func(w utf8WriterUnderTest, s string) { w.StringBytes([]byte(s)) },
		},
		{name: "StringCopy", quoted: true, call: func(w utf8WriterUnderTest, s string) {
			w.StringCopy(strings.NewReader(s))
		}},
		{name: "Raw", call: func(w utf8WriterUnderTest, s string) { w.Raw([]byte(s)) }},
		{
			name: "RawCopy",
			call: func(w utf8WriterUnderTest, s string) { w.RawCopy(strings.NewReader(s)) },
		},
	}
}

// TestWriterUTF8Strict pins the default: ill-formed input is refused, well-formed input is written.
func TestWriterUTF8Strict(t *testing.T) {
	for _, wr := range utf8Writers() {
		for _, ep := range entryPoints() {
			for _, tc := range utf8WriterCases() {
				t.Run(wr.name+"/"+ep.name+"/"+tc.name, func(t *testing.T) {
					out, err := writeWith(t, wr, UTF8Strict, tc.in, ep.call)

					if !tc.valid {
						require.Errorf(t, err, "ill-formed input must be refused, got %q", out)
						assert.ErrorIs(t, err, ErrInvalidUTF8)

						return
					}

					require.NoError(t, err)
					assert.Truef(t, utf8.ValidString(out), "output must be valid UTF-8: %q", out)
					if !ep.quoted {
						assert.Equal(t, tc.in, out)
					}
				})
			}
		}
	}
}

// TestWriterUTF8ReplaceAlwaysEmitsValidUTF8 pins the promise of UTF8Replace: it never errors on ill-formed input, and
// what comes out is always valid UTF-8 — on every writer and every entry point, including the streaming ones where a
// sequence can be split across reads.
func TestWriterUTF8ReplaceAlwaysEmitsValidUTF8(t *testing.T) {
	for _, wr := range utf8Writers() {
		for _, ep := range entryPoints() {
			for _, tc := range utf8WriterCases() {
				t.Run(wr.name+"/"+ep.name+"/"+tc.name, func(t *testing.T) {
					out, err := writeWith(t, wr, UTF8Replace, tc.in, ep.call)

					require.NoErrorf(t, err, "UTF8Replace must not error on %q", tc.in)
					assert.Truef(t, utf8.ValidString(out),
						"UTF8Replace must emit valid UTF-8, got %q for input %q", out, tc.in)
					if tc.valid && !ep.quoted {
						assert.Equal(t, tc.in, out)
					}
				})
			}
		}
	}
}

// TestWriterSubstitutionMatchesLexer is the point of sharing utf8x: the bytes a writer substitutes must be the bytes
// the lexer would have produced for the same input, so a value can round-trip through both without changing again.
func TestWriterSubstitutionMatchesLexer(t *testing.T) {
	for _, wr := range utf8Writers() {
		for _, tc := range utf8WriterCases() {
			if tc.valid {
				continue
			}
			t.Run(wr.name+"/"+tc.name, func(t *testing.T) {
				want := string(utf8x.Sanitize(nil, []byte(tc.in)))

				out, err := writeWith(t, wr, UTF8Replace, tc.in,
					func(w utf8WriterUnderTest, s string) { w.Raw([]byte(s)) })
				require.NoError(t, err)
				assert.Equalf(t, want, out, "Raw under UTF8Replace must agree with utf8x.Sanitize")

				// the escaped path reaches the same text, wrapped in quotes and with escapes applied
				quoted, err := writeWith(t, wr, UTF8Replace, tc.in,
					func(w utf8WriterUnderTest, s string) { w.StringBytes([]byte(s)) })
				require.NoError(t, err)
				assert.Equalf(t, expectedJSONString(want), quoted,
					"the escaped path must substitute the same way")
			})
		}
	}
}

// TestWriterUTF8Passthrough pins that passthrough really passes bytes through rather than quietly substituting,
// which is the only thing that distinguishes it from UTF8Replace.
func TestWriterUTF8Passthrough(t *testing.T) {
	for _, wr := range utf8Writers() {
		for _, tc := range utf8WriterCases() {
			if tc.valid {
				continue
			}
			t.Run(wr.name+"/"+tc.name, func(t *testing.T) {
				out, err := writeWith(t, wr, UTF8Passthrough, tc.in,
					func(w utf8WriterUnderTest, s string) { w.Raw([]byte(s)) })
				require.NoError(t, err)
				assert.Equal(t, tc.in, out, "Raw must not touch the bytes")

				esc, err := writeWith(t, wr, UTF8Passthrough, tc.in,
					func(w utf8WriterUnderTest, s string) { w.StringBytes([]byte(s)) })
				require.NoError(t, err)
				assert.Falsef(
					t,
					utf8.ValidString(esc),
					"passthrough must NOT sanitize: the ill-formed bytes should survive escaping, got %q",
					esc,
				)
				assert.NotContainsf(t, esc, string(utf8.RuneError),
					"passthrough must not substitute U+FFFD, got %q", esc)
			})
		}
	}
}

// TestWriterStreamingSplitSequence forces a multi-byte sequence to straddle a read boundary at every offset. A chunk
// ending mid-sequence is NOT an error, and the validator must not mistake it for one — nor miss a genuine fault that
// only shows up once the chunks are joined.
func TestWriterStreamingSplitSequence(t *testing.T) {
	valid := strings.Repeat("a", 40) + "\u65e5\U0001D11E" + strings.Repeat("b", 40)
	broken := strings.Repeat(
		"a",
		40,
	) + "\xe6\x41" + strings.Repeat(
		"b",
		40,
	) // lead byte then a non-continuation

	for _, wr := range utf8Writers() {
		for chunk := 1; chunk <= 8; chunk++ {
			t.Run(fmt.Sprintf("%s/chunk=%d", wr.name, chunk), func(t *testing.T) {
				for _, ep := range []string{"StringCopy", "RawCopy"} {
					call := func(w utf8WriterUnderTest, s string) { w.StringCopy(&chunkReader{data: []byte(s), chunk: chunk}) }
					if ep == "RawCopy" {
						call = func(w utf8WriterUnderTest, s string) {
							w.RawCopy(&chunkReader{data: []byte(s), chunk: chunk})
						}
					}

					out, err := writeWith(t, wr, UTF8Strict, valid, call)
					require.NoErrorf(
						t,
						err,
						"%s: a sequence split across reads is not an error",
						ep,
					)
					assert.Truef(t, utf8.ValidString(out), "%s: %q", ep, out)

					_, err = writeWith(t, wr, UTF8Strict, broken, call)
					require.Errorf(
						t,
						err,
						"%s: a fault spanning the read boundary must be caught",
						ep,
					)

					out, err = writeWith(t, wr, UTF8Replace, broken, call)
					require.NoError(t, err)
					assert.Truef(t, utf8.ValidString(out), "%s: %q", ep, out)
				}
			})
		}
	}
}

// TestWriterTrustsTokenValues pins the deliberate loophole: a value coming from a lexer token is NOT re-validated,
// because the lexers already guarantee valid UTF-8. A lexer relaxed to UTF8Passthrough can therefore hand the writer
// ill-formed bytes that a strict writer will emit — the documented cost of not paying for a second check.
func TestWriterTrustsTokenValues(t *testing.T) {
	const doc = "[\"a\xffb\"]"

	// the lexer must be relaxed for such a token to exist at all
	lx := lexer.NewWithBytes([]byte(doc), lexer.WithUTF8Policy(lexer.UTF8Passthrough))
	var value token.T
	for tok := range lx.Tokens() {
		if tok.Kind() == token.String {
			value = token.MakeWithValue(token.String, bytes.Clone(tok.Value()))
		}
	}
	require.NoError(t, lx.Err())
	require.False(
		t,
		utf8.Valid(value.Value()),
		"the relaxed lexer should have produced ill-formed bytes",
	)

	// Token: not re-validated, so no error — but the escaper still decodes as it goes, so the ill-formed byte is
	// silently substituted. The loophole is the SILENCE, not invalid output.
	var buf bytes.Buffer
	w := NewUnbuffered(&buf) // UTF8Strict
	w.Token(value)
	require.NoError(t, w.Err(), "token values are trusted, not re-validated")
	assert.Equal(t, "\"a\ufffdb\"", buf.String(),
		"the escaper substitutes as it goes, so the output stays valid UTF-8")

	// VerbatimToken writes the raw value with no escaping at all, so there ill-formed bytes really do reach the
	// output. This is the sharp edge of trusting tokens and is documented as such.
	var verbatim bytes.Buffer
	vw := NewUnbuffered(&verbatim) // UTF8Strict
	vw.VerbatimToken(nil, value)
	require.NoError(t, vw.Err())
	assert.Equal(t, "\"a\xffb\"", verbatim.String(),
		"verbatim writing is byte-for-byte by definition: nothing inspects or rewrites the value")

	// VerbatimValue is NOT in the trusted group: it renders through the ordinary string path
	var vv bytes.Buffer
	vvw := NewUnbuffered(&vv)
	vvw.VerbatimValue(values.MakeVerbatimValue(nil, values.MakeStringValue(string(value.Value()))))
	require.ErrorIs(t, vvw.Err(), ErrInvalidUTF8,
		"VerbatimValue goes through the string path, so it is checked like any caller data")

	// the same bytes offered as caller data ARE checked
	var caller bytes.Buffer
	cw := NewUnbuffered(&caller)
	cw.StringBytes(value.Value())
	require.ErrorIs(t, cw.Err(), ErrInvalidUTF8)
}

// TestWriterStringRunesRejectsNonScalar pins that a []rune carrying a surrogate or an out-of-range value is refused
// under UTF8Strict rather than silently encoded as U+FFFD by utf8.AppendRune.
func TestWriterStringRunesRejectsNonScalar(t *testing.T) {
	for _, tc := range []struct {
		name  string
		runes []rune
	}{
		{name: "surrogate", runes: []rune{'a', 0xD800, 'b'}},
		{name: "beyond max", runes: []rune{'a', 0x110000, 'b'}},
		{name: "negative", runes: []rune{'a', -1, 'b'}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := NewUnbuffered(&buf)
			w.StringRunes(tc.runes)
			require.ErrorIs(t, w.Err(), ErrInvalidUTF8)

			var replaced bytes.Buffer
			rw := NewUnbuffered(&replaced, WithUnbufferedUTF8Policy(UTF8Replace))
			rw.StringRunes(tc.runes)
			require.NoError(t, rw.Err())
			assert.Equal(t, "\"a�b\"", replaced.String())
		})
	}
}

// TestWriterDefaultIsStrict pins the default so it cannot regress to the old silent substitution.
func TestWriterDefaultIsStrict(t *testing.T) {
	for _, wr := range utf8Writers() {
		t.Run(wr.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := wr.build(&buf, UTF8Strict) // what a caller passing no option gets
			w.StringBytes([]byte("a\xffb"))
			require.ErrorIs(t, w.Err(), ErrInvalidUTF8)
		})
	}

	// and literally with no option at all
	var buf bytes.Buffer
	u := NewUnbuffered(&buf)
	u.StringBytes([]byte("a\xffb"))
	require.ErrorIs(t, u.Err(), ErrInvalidUTF8)

	var buf2 bytes.Buffer
	b := NewBuffered(&buf2)
	b.StringBytes([]byte("a\xffb"))
	require.ErrorIs(t, b.Err(), ErrInvalidUTF8)
}

// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package lexer

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	codes "github.com/go-openapi/core/json/lexers/error-codes"
	"github.com/go-openapi/core/json/lexers/token"
)

// UTF-8 validation of string values (see .claude/plans/utf8-validation.md).
//
// The lexers used to wave every byte >= 0x80 straight into the token value: the string scanners stop only on '"',
// '\\' and control chars, so ill-formed UTF-8 was never looked at. These tests pin the three policies across every
// string path — whole-buffer vs streaming, clean vs escaped, L vs VL — because the detection that gates validation is
// fused into each scanner separately and can only be wrong per-path.

// utf8Case is one input and what each policy must make of it.
type utf8Case struct {
	name string
	// body is the string BODY (no quotes); the case is exercised as a value, as an object key, and with a long
	// pure-ASCII prefix that forces the AVX2/long-string path.
	body string
	// wantStrict is the error the default policy must report (nil means the input is valid).
	wantStrict error
	// wantReplaced is the decoded value under UTF8Replace.
	wantReplaced string
	// wantRawKept is true when VL keeps the body verbatim under UTF8Replace: it rewrites invalid BYTES but never
	// rewrites escape text (the U+FFFD for a broken escape appears on decode, in token.Unescape).
	wantRawKept bool
}

func utf8Cases() []utf8Case {
	return []utf8Case{
		// --- valid: must be untouched by every policy ---
		{name: "ascii", body: "plain", wantReplaced: "plain"},
		{name: "2-byte", body: "caf\xc3\xa9", wantReplaced: "caf\u00e9"},
		{name: "3-byte", body: "\xe6\x97\xa5\xe6\x9c\xac", wantReplaced: "\u65e5\u672c"},
		{name: "4-byte", body: "\xf0\x9d\x84\x9e", wantReplaced: "\U0001D11E"},
		{name: "max scalar", body: "\xf4\x8f\xbf\xbf", wantReplaced: "\U0010FFFF"},
		{name: "encoded U+FFFD", body: "\xef\xbf\xbd", wantReplaced: "\ufffd"},
		{
			name:         "valid surrogate pair escape",
			body:         `\uD834\uDD1E`,
			wantReplaced: "\U0001D11E",
			wantRawKept:  true,
		},

		// --- ill-formed bytes: one U+FFFD per invalid byte ---
		{name: "lone ff", body: "\xff", wantStrict: codes.ErrInvalidUTF8, wantReplaced: "\ufffd"},
		{
			name:         "lone continuation",
			body:         "\x81",
			wantStrict:   codes.ErrInvalidUTF8,
			wantReplaced: "\ufffd",
		},
		{name: "latin-1", body: "\xe9", wantStrict: codes.ErrInvalidUTF8, wantReplaced: "\ufffd"},
		{
			name: "truncated 3-byte", body: "\xe0\xff",
			wantStrict: codes.ErrInvalidUTF8, wantReplaced: "\ufffd\ufffd",
		},
		{
			name: "valid then invalid lead", body: "\xe6\x97\xa5\xd1\x88\xfa",
			wantStrict: codes.ErrInvalidUTF8, wantReplaced: "\u65e5\u0448\ufffd",
		},
		{
			name: "encoded surrogate", body: "\xed\xa0\x80",
			wantStrict: codes.ErrInvalidUTF8, wantReplaced: "\ufffd\ufffd\ufffd",
		},
		{
			name: "beyond U+10FFFF", body: "\xf4\xbf\xbf\xbf",
			wantStrict: codes.ErrInvalidUTF8, wantReplaced: "\ufffd\ufffd\ufffd\ufffd",
		},
		{
			name: "overlong", body: "\xc0\xaf",
			wantStrict: codes.ErrInvalidUTF8, wantReplaced: "\ufffd\ufffd",
		},
		{
			name: "invalid between valid", body: "a\xffb",
			wantStrict: codes.ErrInvalidUTF8, wantReplaced: "a\ufffdb",
		},
		{
			name: "truncated at end", body: "ok\xe2\x82",
			wantStrict: codes.ErrInvalidUTF8, wantReplaced: "ok\ufffd\ufffd",
		},

		// --- ill-formed \u escapes: one U+FFFD per broken escape, and the NEXT escape is not swallowed ---
		{
			name: "unpaired high surrogate then escape", body: `\uD800\uD800\n`,
			wantStrict: codes.ErrSurrogateEscape, wantReplaced: "\ufffd\ufffd\n", wantRawKept: true,
		},
		{
			name: "high surrogate then non-surrogate", body: `\uD888\u1234`,
			wantStrict: codes.ErrSurrogateEscape, wantReplaced: "\ufffd\u1234", wantRawKept: true,
		},
		{
			name: "inverted pair", body: `\uDd1e\uD834`,
			wantStrict: codes.ErrSurrogateEscape, wantReplaced: "\ufffd\ufffd", wantRawKept: true,
		},
		{
			name: "lone high surrogate at end", body: `\uDADA`,
			wantStrict: codes.ErrSurrogateEscape, wantReplaced: "\ufffd", wantRawKept: true,
		},
		{
			name: "lone low surrogate", body: `\uDFAA`,
			wantStrict: codes.ErrSurrogateEscape, wantReplaced: "\ufffd", wantRawKept: true,
		},
		{
			name: "surrogate then literal text", body: `\uD800abc`,
			wantStrict: codes.ErrSurrogateEscape, wantReplaced: "\ufffdabc", wantRawKept: true,
		},

		// --- mixed: an escape and an ill-formed byte in the same value ---
		{
			name: "escape then bad byte", body: "a\\nb\xffc",
			wantStrict: codes.ErrInvalidUTF8, wantReplaced: "a\nb\ufffdc",
		},
		{
			name: "bad byte then escape", body: "a\xffb\\tc",
			wantStrict: codes.ErrInvalidUTF8, wantReplaced: "a\ufffdb\tc",
		},
	}
}

// longPrefix is longer than guessLong (16) so the scan delegates to the AVX2/long-string kernel, whose fused
// non-ASCII accumulator is a separate implementation from the inline SWAR one.
const longPrefix = "0123456789abcdefghijklmnopqrstuvwxyz0123456789"

// utf8Driver is one way of running a document through a lexer: the eight string paths are reached by crossing
// {L, VL} x {whole-buffer, streaming} x {clean, escaped}, and the escaped axis comes from the fixtures themselves.
type utf8Driver struct {
	name string
	// run drains the document and returns the decoded value of the first String/Key token, the raw token value, and
	// the lexer error.
	run func(doc []byte, policy UTF8Policy) (decoded, raw []byte, err error)
}

func utf8Drivers() []utf8Driver {
	drainL := func(lx *L) (decoded, raw []byte, err error) {
		for tok := range lx.Tokens() {
			if k := tok.Kind(); (k == token.String || k == token.Key) && decoded == nil {
				decoded = bytes.Clone(tok.Value())
				raw = decoded
			}
		}

		return decoded, raw, lx.Err()
	}
	drainVL := func(lx *VL) (decoded, raw []byte, err error) {
		for tok := range lx.Tokens() {
			if k := tok.Kind(); (k == token.String || k == token.Key) && raw == nil {
				raw = bytes.Clone(tok.Value())
				decoded = token.Unescape(bytes.Clone(raw))
			}
		}

		return decoded, raw, lx.Err()
	}

	return []utf8Driver{
		{name: "L/bytes", run: func(doc []byte, p UTF8Policy) ([]byte, []byte, error) {
			return drainL(NewWithBytes(doc, WithUTF8Policy(p)))
		}},
		{name: "L/bytes/noavx2", run: func(doc []byte, p UTF8Policy) ([]byte, []byte, error) {
			return drainL(NewWithBytes(doc, WithUTF8Policy(p), WithoutAVX2(true)))
		}},
		{name: "L/reader", run: func(doc []byte, p UTF8Policy) ([]byte, []byte, error) {
			return drainL(New(newChunkReader(doc, 7), WithUTF8Policy(p), WithBufferSize(32)))
		}},
		{name: "VL/bytes", run: func(doc []byte, p UTF8Policy) ([]byte, []byte, error) {
			return drainVL(NewVerbatimWithBytes(doc, WithUTF8Policy(p)))
		}},
		{name: "VL/bytes/noavx2", run: func(doc []byte, p UTF8Policy) ([]byte, []byte, error) {
			return drainVL(NewVerbatimWithBytes(doc, WithUTF8Policy(p), WithoutAVX2(true)))
		}},
		{name: "VL/reader", run: func(doc []byte, p UTF8Policy) ([]byte, []byte, error) {
			return drainVL(
				NewVerbatim(newChunkReader(doc, 7), WithUTF8Policy(p), WithBufferSize(32)),
			)
		}},
	}
}

// newChunkReader returns a reader that hands out at most size bytes per Read, so a value — and an ill-formed sequence
// or a \u escape inside it — is forced across refill boundaries.
func newChunkReader(data []byte, size int) *chunkReader {
	return &chunkReader{data: data, size: size}
}

type chunkReader struct {
	data []byte
	size int
	pos  int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:min(r.pos+r.size, len(r.data))])
	r.pos += n

	return n, nil
}

func TestUTF8Policy(t *testing.T) {
	// each fixture is exercised in three shapes, which route to different scanners
	shapes := []struct {
		name string
		doc  func(body string) string
	}{
		{name: "value", doc: func(b string) string { return `["` + b + `"]` }},
		{name: "key", doc: func(b string) string { return `{"` + b + `":1}` }},
		{name: "long-value", doc: func(b string) string { return `["` + longPrefix + b + `"]` }},
	}

	for _, tc := range utf8Cases() {
		for _, shape := range shapes {
			doc := []byte(shape.doc(tc.body))
			prefix := ""
			if shape.name == "long-value" {
				prefix = longPrefix
			}

			for _, drv := range utf8Drivers() {
				t.Run(tc.name+"/"+shape.name+"/"+drv.name, func(t *testing.T) {
					isVerbatim := strings.HasPrefix(drv.name, "VL")

					t.Run("strict", func(t *testing.T) {
						decoded, _, err := drv.run(doc, UTF8Strict)
						if tc.wantStrict == nil {
							require.NoError(t, err)
							assert.Equal(t, prefix+tc.wantReplaced, string(decoded))

							return
						}
						require.Error(t, err)
						assert.ErrorIs(t, err, tc.wantStrict)
					})

					t.Run("replace", func(t *testing.T) {
						decoded, raw, err := drv.run(doc, UTF8Replace)
						require.NoError(t, err)
						assert.Equal(t, prefix+tc.wantReplaced, string(decoded))
						assert.Truef(t, utf8.Valid(decoded),
							"an emitted value must always be valid UTF-8, got %q", decoded)

						// VL keeps escape text verbatim; only ill-formed BYTES are rewritten in the raw value.
						if isVerbatim && tc.wantRawKept {
							assert.Equal(t, prefix+tc.body, string(raw))
						}
					})

					t.Run("passthrough", func(t *testing.T) {
						_, raw, err := drv.run(doc, UTF8Passthrough)
						require.NoError(t, err)
						// ill-formed bytes reach the caller untouched. Only checked for bodies without escapes: L
						// decodes them, so its "raw" is the decoded value and would not match the source text.
						if tc.wantStrict == codes.ErrInvalidUTF8 &&
							!strings.Contains(tc.body, `\`) {
							assert.Equal(t, prefix+tc.body, string(raw))
						}
					})
				})
			}
		}
	}
}

// TestUTF8StrictErrorOffset pins that the reported offset points at the first ill-formed byte, not at the end of the
// value — the reason FirstInvalid exists next to Valid.
func TestUTF8StrictErrorOffset(t *testing.T) {
	cases := []struct {
		name     string
		doc      string
		want     uint64
		wantByte byte
	}{
		{name: "first byte of the value", doc: "[\"\xffabc\"]", want: 2, wantByte: 0xff},
		// the truncated 3-byte lead is the start of the ill-formed sequence, not the 0xff that reveals it
		{name: "after a valid prefix", doc: "[\"ok\xe0\xff\"]", want: 4, wantByte: 0xe0},
		{
			name: "in a long value", doc: "[\"" + longPrefix + "\xff\"]",
			want: uint64(2 + len(longPrefix)), wantByte: 0xff,
		},
		{name: "in a key", doc: "{\"k\xff\":1}", want: 3, wantByte: 0xff},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lx := NewWithBytes([]byte(tc.doc))
			for range lx.Tokens() { //nolint:revive // draining is the point
			}
			require.ErrorIs(t, lx.Err(), codes.ErrInvalidUTF8)
			require.Equal(t, tc.want, lx.Offset())
			assert.Equal(t, tc.wantByte, tc.doc[lx.Offset()],
				"the offset must land on the first byte of the ill-formed sequence")
		})
	}
}

// TestUTF8ReplaceKeepsValidValuesAliased pins the zero-copy invariant: only an offending value is copied out of the
// input, so enabling replacement costs nothing on a well-formed document.
func TestUTF8ReplaceKeepsValidValuesAliased(t *testing.T) {
	doc := []byte(`["` + longPrefix + `",  "caf\u00e9"]`)

	for _, p := range []UTF8Policy{UTF8Strict, UTF8Replace, UTF8Passthrough} {
		lx := NewWithBytes(doc, WithUTF8Policy(p))
		lx.NextToken() // '['
		require.True(t, lx.Ok())
		tok := lx.NextToken() // the long clean string
		require.True(t, lx.Ok())
		require.Equal(t, token.String, tok.Kind())

		v := tok.Value()
		require.NotEmpty(t, v)
		assert.Samef(t, &doc[2], &v[0],
			"a valid value must still alias the caller's buffer under policy %d", p)
	}
}

// TestUTF8MaxValueBytesAfterReplacement pins that the size cap is re-checked against the REWRITTEN length: replacing a
// 1-byte fault with U+FFFD grows the value threefold, so a value that fit before sanitizing may not fit after.
func TestUTF8MaxValueBytesAfterReplacement(t *testing.T) {
	doc := []byte("[\"ab\xff\"]") // 3 bytes scanned, 5 bytes once sanitized

	lx := NewWithBytes(doc, WithUTF8Policy(UTF8Replace), WithMaxValueBytes(4))
	for range lx.Tokens() { //nolint:revive // draining is the point
	}
	require.ErrorIs(t, lx.Err(), codes.ErrMaxValueBytes)

	lx = NewWithBytes(doc, WithUTF8Policy(UTF8Replace), WithMaxValueBytes(8))
	for range lx.Tokens() { //nolint:revive // draining is the point
	}
	require.NoError(t, lx.Err())
}

// TestUTF8SplitSequence forces an ill-formed sequence and a \u escape to straddle a refill boundary at every possible
// split point: the streaming scanners assemble the value across windows, so a fault can be split between the window
// that detected non-ASCII and the one that carries the rest.
func TestUTF8SplitSequence(t *testing.T) {
	docs := []struct {
		name string
		doc  string
		want error
	}{
		{
			name: "ill-formed bytes",
			doc:  "[\"" + longPrefix + "\xe0\xff" + longPrefix + "\"]",
			want: codes.ErrInvalidUTF8,
		},
		{
			name: "broken surrogate pair",
			doc:  `["` + longPrefix + `\uD800\uD800` + longPrefix + `"]`,
			want: codes.ErrSurrogateEscape,
		},
		{name: "valid surrogate pair", doc: `["` + longPrefix + `\uD834\uDD1E` + longPrefix + `"]`},
		{name: "valid multibyte", doc: "[\"" + longPrefix + "caf\xc3\xa9" + longPrefix + "\"]"},
	}

	for _, d := range docs {
		for chunk := 1; chunk <= 24; chunk++ {
			t.Run(fmt.Sprintf("%s/chunk=%d", d.name, chunk), func(t *testing.T) {
				for _, verbatim := range []bool{false, true} {
					var err error
					var decoded []byte
					if verbatim {
						lx := NewVerbatim(newChunkReader([]byte(d.doc), chunk),
							WithBufferSize(32), WithUTF8Policy(UTF8Replace))
						for tok := range lx.Tokens() {
							if tok.Kind() == token.String {
								decoded = token.Unescape(bytes.Clone(tok.Value()))
							}
						}
						err = lx.Err()
					} else {
						lx := New(newChunkReader([]byte(d.doc), chunk),
							WithBufferSize(32), WithUTF8Policy(UTF8Replace))
						for tok := range lx.Tokens() {
							if tok.Kind() == token.String {
								decoded = bytes.Clone(tok.Value())
							}
						}
						err = lx.Err()
					}

					require.NoErrorf(t, err, "verbatim=%v: replacement never errors", verbatim)
					assert.Truef(t, utf8.Valid(decoded),
						"verbatim=%v: emitted value must be valid UTF-8, got %q", verbatim, decoded)
				}

				// the same document under the default policy must agree with itself at every chunk size
				lx := New(newChunkReader([]byte(d.doc), chunk), WithBufferSize(32))
				for range lx.Tokens() { //nolint:revive // draining is the point
				}
				if d.want == nil {
					require.NoError(t, lx.Err())
				} else {
					require.ErrorIs(t, lx.Err(), d.want)
				}
			})
		}
	}
}

// TestUTF8LexersAgree is the anti-divergence guard: L and VL must accept and reject exactly the same documents under
// every policy, and must decode to the same string. L accepting what VL rejected is precisely the bug class this work
// closed (L laundered a broken surrogate pair into a silent U+FFFD while VL errored).
func TestUTF8LexersAgree(t *testing.T) {
	for _, tc := range utf8Cases() {
		for _, doc := range []string{`["` + tc.body + `"]`, `{"` + tc.body + `":1}`} {
			for _, p := range []UTF8Policy{UTF8Strict, UTF8Replace, UTF8Passthrough} {
				t.Run(fmt.Sprintf("%s/%d", tc.name, p), func(t *testing.T) {
					got := make([]string, 0, len(utf8Drivers()))
					for _, drv := range utf8Drivers() {
						decoded, _, err := drv.run([]byte(doc), p)
						got = append(
							got,
							fmt.Sprintf("%s: err=%v decoded=%q", drv.name, err != nil, decoded),
						)
					}
					for _, line := range got[1:] {
						assert.Equal(t,
							strings.SplitN(got[0], ": ", 2)[1],
							strings.SplitN(line, ": ", 2)[1],
							"lexer drivers disagree on %q:\n  %s", doc, strings.Join(got, "\n  "))
					}
				})
			}
		}
	}
}

// TestUTF8ReplaceMangledValueContract pins what mangling does and does not preserve, because the two halves are easy
// to conflate: a rewritten VALUE no longer maps onto its source span (U+FFFD is 3 bytes replacing 1), while token
// POSITIONS stay exact because they come from the scan cursor rather than the value.
func TestUTF8ReplaceMangledValueContract(t *testing.T) {
	// one ill-formed byte, then a truncated 3-byte sequence: 1 + 3 faults => 4 replacements, 12 bytes for 4 source
	const body = "a\xffb\xe0\xa0\xffc"
	doc := []byte("  [\"" + body + "\", 7]")

	for _, verbatim := range []bool{false, true} {
		t.Run(fmt.Sprintf("verbatim=%v", verbatim), func(t *testing.T) {
			var (
				value        []byte
				tokenOffset  uint64
				line, column int
			)
			if verbatim {
				lx := NewVerbatimWithBytes(doc, WithUTF8Policy(UTF8Replace))
				require.Equal(t, token.Delimiter, lx.NextToken().Kind()) // '['
				tok := lx.NextToken()
				require.True(t, lx.Ok())
				require.Equal(t, token.String, tok.Kind())
				value, line, column = bytes.Clone(tok.Value()), lx.Line(), lx.Column()
				tokenOffset = lx.Offset()
			} else {
				lx := NewWithBytes(doc, WithUTF8Policy(UTF8Replace))
				require.Equal(t, token.Delimiter, lx.NextToken().Kind()) // '['
				tok := lx.NextToken()
				require.True(t, lx.Ok())
				require.Equal(t, token.String, tok.Kind())
				value = bytes.Clone(tok.Value())
				tokenOffset = lx.Offset()
			}

			// the value is well-formed, and LONGER than the text it came from
			assert.True(t, utf8.Valid(value))
			assert.Equal(t, "a�b���c", string(value))
			assert.Greaterf(t, len(value), len(body),
				"U+FFFD is 3 bytes replacing 1: a mangled value cannot have its source width")

			// positions are still exact: the cursor sits just past the closing quote of the string
			wantOffset := uint64(len("  [\"") + len(body) + 1)
			assert.Equalf(t, wantOffset, tokenOffset,
				"the scan cursor must track the SOURCE, not the rewritten value")
			if verbatim {
				assert.Equal(t, 1, line)
				assert.Equal(t, len("  [")+1, column)
			}
		})
	}
}

// TestUTF8BOM pins the leading-BOM rules: the complete mark at offset 0 is consumed, and nothing else is.
func TestUTF8BOM(t *testing.T) {
	const bom = "\uFEFF"

	t.Run("consumed at offset 0", func(t *testing.T) {
		for _, doc := range []string{bom + "{}", bom + `{"a":1}`, bom + "  [1,2]", bom + "null"} {
			lx := NewWithBytes([]byte(doc))
			for range lx.Tokens() { //nolint:revive // draining is the point
			}
			require.NoErrorf(t, lx.Err(), "input %q", doc)

			// streaming, with the mark split across the very first reads
			for chunk := 1; chunk <= 4; chunk++ {
				sl := New(newChunkReader([]byte(doc), chunk), WithBufferSize(32))
				for range sl.Tokens() { //nolint:revive // draining is the point
				}
				assert.NoErrorf(t, sl.Err(), "input %q, chunk=%d", doc, chunk)
			}
		}
	})

	t.Run("offsets account for the consumed mark", func(t *testing.T) {
		vl := NewVerbatimWithBytes([]byte(bom + "{}"))
		tok := vl.NextToken()
		require.True(t, vl.Ok())
		assert.Equal(t, token.Delimiter, tok.Kind())
		// the '{' is the fourth byte of the document
		assert.Equal(t, uint64(len(bom)+1), vl.Offset())
	})

	t.Run("not re-emitted by the verbatim lexer", func(t *testing.T) {
		// the documented exception to VL's byte-exact round-trip: the mark belongs to no token's leading space
		vl := NewVerbatimWithBytes([]byte(bom + " {}"))
		tok := vl.NextToken()
		require.True(t, vl.Ok())
		require.Equal(t, token.Delimiter, tok.Kind())
		assert.Equal(t, " ", string(vl.LeadingSpace()))
	})

	t.Run("only the complete mark, only at offset 0", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			doc  string
		}{
			{name: "truncated mark", doc: "\xef\xbb{}"},
			{name: "mark alone", doc: bom},
			{name: "mark after a value", doc: "{}" + bom},
			{name: "mark inside an array", doc: "[" + bom + "1]"},
			{name: "doubled mark", doc: bom + bom + "{}"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				lx := NewWithBytes([]byte(tc.doc))
				for range lx.Tokens() { //nolint:revive // draining is the point
				}
				assert.Error(t, lx.Err())
			})
		}
	})

	t.Run("a mark inside a string is ordinary content", func(t *testing.T) {
		lx := NewWithBytes([]byte(`["` + bom + `"]`))
		var got []byte
		for tok := range lx.Tokens() {
			if tok.Kind() == token.String {
				got = bytes.Clone(tok.Value())
			}
		}
		require.NoError(t, lx.Err())
		assert.Equal(t, bom, string(got))
	})
}

// TestUTF16BOMDiagnostic pins the accurate error for a UTF-16 or UTF-32 document, which would otherwise fail as a
// generic invalid token on its first byte.
//
// UTF-32BE is the case that needs its own bytes: its mark (00 00 FE FF) shares no prefix with the UTF-16 ones. UTF-32LE
// opens with the UTF-16LE mark, so it is caught by that test — which is why the error names both encodings rather than
// guessing one.
func TestUTF16BOMDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
	}{
		{name: "UTF-16LE", doc: "\xff\xfe[\x00\"\x00\xe9\x00\"\x00]\x00"},
		{name: "UTF-16BE", doc: "\xfe\xff\x00[\x00\"\x00\xe9\x00\"\x00]"},
		{name: "UTF-32LE", doc: "\xff\xfe\x00\x00[\x00\x00\x00]\x00\x00\x00"},
		{name: "UTF-32BE", doc: "\x00\x00\xfe\xff\x00\x00\x00[\x00\x00\x00]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lx := NewWithBytes([]byte(tc.doc))
			for range lx.Tokens() { //nolint:revive // draining is the point
			}
			assert.ErrorIs(t, lx.Err(), codes.ErrNotUTF8)

			vl := NewVerbatim(newChunkReader([]byte(tc.doc), 3), WithBufferSize(32))
			for range vl.Tokens() { //nolint:revive // draining is the point
			}
			assert.ErrorIs(t, vl.Err(), codes.ErrNotUTF8)
		})
	}
}

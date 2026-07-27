package lexer

import (
	"iter"
	"slices"
	"strings"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	"github.com/go-openapi/core/json/expressions"
	"github.com/go-openapi/core/json/lexers/token"
)

func TestJSONPointerSemanticsL(t *testing.T) {
	for tc := range pointerCases() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := collectPtrsPull(NewWithBytes([]byte(tc.in), WithJSONPointer(true)))
			require.NoError(t, err)
			assert.Equal(t, tc.ptrs, got, "pull whole-buffer")
		})
	}
}

// TestJSONPointerEquivalence pins that the tracked pointer stream is identical across all four lanes — {L, VL} ×
// {pull, push} — and across whole-buffer vs streaming (tiny buffer to force refills), for every case.
//
// VL emits separators (elide off by default) but the pointer must be unaffected.
func TestJSONPointerEquivalence(t *testing.T) {
	for tc := range pointerCases() {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(tc.in)

			// reference: L pull whole-buffer
			want := tc.ptrs

			// L push whole-buffer
			gotLPush, err := collectPtrsPush(NewWithBytes(data, WithJSONPointer(true)))
			require.NoError(t, err)
			assert.Equal(t, want, gotLPush, "L push whole-buffer")

			// L pull streaming
			gotLStream, err := collectPtrsPull(
				New(strings.NewReader(tc.in), WithJSONPointer(true), WithBufferSize(4)),
			)
			require.NoError(t, err)
			assert.Equal(t, want, gotLStream, "L pull streaming")

			// L push streaming
			gotLPushStream, err := collectPtrsPush(
				New(strings.NewReader(tc.in), WithJSONPointer(true), WithBufferSize(4)),
			)
			require.NoError(t, err)
			assert.Equal(t, want, gotLPushStream, "L push streaming")

			// VL lanes elide separators so the token stream matches L's (VL's non-elided default, where the pointer simply
			// repeats at each ',' / ':', is locked by TestJSONPointerVerbatimSeparators).
			gotVL, err := collectPtrsPull(
				NewVerbatimWithBytes(data, WithJSONPointer(true), WithElideSeparator(true)),
			)
			require.NoError(t, err)
			assert.Equal(t, want, gotVL, "VL pull whole-buffer")

			// VL push whole-buffer
			gotVLPush, err := collectPtrsPush(
				NewVerbatimWithBytes(data, WithJSONPointer(true), WithElideSeparator(true)),
			)
			require.NoError(t, err)
			assert.Equal(t, want, gotVLPush, "VL push whole-buffer")

			// VL pull streaming
			gotVLStream, err := collectPtrsPull(
				NewVerbatim(
					strings.NewReader(tc.in),
					WithJSONPointer(true),
					WithElideSeparator(true),
					WithBufferSize(4),
				),
			)
			require.NoError(t, err)
			assert.Equal(t, want, gotVLStream, "VL pull streaming")
		})
	}
}

// TestJSONPointerKeyForm pins the documented L-vs-VL key form: a member name carrying a JSON escape (an escaped
// solidus) decodes under L to the logical name "a/b" but stays raw under VL.
//
// Their pointer segments therefore differ — while RFC 6901 "~"/"/" escaping is applied by Pointer.String in both
// cases.
// VL elides separators here so both streams are the 4 tokens { key 1 }, directly comparable.
func TestJSONPointerKeyForm(t *testing.T) {
	in := []byte("{\"a\\u002Fb\":1}") // JSON bytes: {"a/b":1}

	// L decodes the escape to '/', giving the logical name a/b; Pointer.String then re-escapes that '/' as ~1.
	gotL, err := collectPtrsPull(NewWithBytes(in, WithJSONPointer(true)))
	require.NoError(t, err)
	assert.Equal(t, []string{"", "/a~1b", "/a~1b", ""}, gotL, "L: decoded logical key")

	// VL keeps the key raw (escape intact): the segment is the bytes a/b, which contain no unescaped '/' or '~', so
	// Pointer.String leaves them verbatim.
	gotVL, err := collectPtrsPull(
		NewVerbatimWithBytes(in, WithJSONPointer(true), WithElideSeparator(true)),
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"", "/a\\u002Fb", "/a\\u002Fb", ""}, gotVL, "VL: raw key")
}

// TestJSONPointerVerbatimSeparators locks that under VL's default (separators emitted) the pointer is a no-op at each
// ',' and ':': it repeats the surrounding value's pointer, so eliding or not eliding separators never changes a
// value/key/delimiter token's pointer.
func TestJSONPointerVerbatimSeparators(t *testing.T) {
	// tokens: { "a" : 1 , "b" : 2 }
	got, err := collectPtrsPull(
		NewVerbatimWithBytes([]byte(`{"a":1,"b":2}`), WithJSONPointer(true)),
	)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"",   // {
		"/a", // key a
		"/a", // :
		"/a", // 1
		"/a", // ,   (still inside member a until the next key is read)
		"/b", // key b
		"/b", // :
		"/b", // 2
		"",   // }
	}, got)
}

// TestJSONPointerParts checks the structured pointer (not just its string form): integer array indices are PathElemInt
// (raw ints), object keys are PathElemString.
func TestJSONPointerParts(t *testing.T) {
	l := NewWithBytes([]byte(`{"a":[7]}`), WithJSONPointer(true))

	var atSeven expressions.Pointer
	for tok := range l.Tokens() {
		if tok.Kind() == token.Number {
			atSeven = l.JSONPointer().Clone() // clone to retain past the next token
		}
	}
	require.NoError(t, l.Err())

	require.Len(t, atSeven, 2)
	parts := make([]expressions.PointerPart, 0, 2)
	for p := range atSeven.Parts() {
		parts = append(parts, p)
	}
	require.Len(t, parts, 2)
	assert.True(t, parts[0].IsKey(), "first part is the object key 'a'")
	assert.Equal(t, "a", parts[0].Key().String())
	assert.True(t, parts[1].IsElem(), "second part is a raw integer array index")
	assert.Equal(t, 0, parts[1].Elem())
	assert.Equal(t, "/a/0", atSeven.String())
}

// TestJSONPointerDefaultOff pins that without WithJSONPointer the accessor yields the empty pointer and tracking does
// no work.
func TestJSONPointerDefaultOff(t *testing.T) {
	l := NewWithBytes([]byte(`{"a":[1,2]}`))
	for range l.Tokens() {
		assert.Equal(
			t,
			"",
			l.JSONPointer().String(),
			"pointer must stay empty when the option is off",
		)
	}
	require.NoError(t, l.Err())

	vl := NewVerbatimWithBytes([]byte(`{"a":[1,2]}`))
	for range vl.Tokens() {
		assert.Equal(t, "", vl.JSONPointer().String())
	}
	require.NoError(t, vl.Err())
}

// TestJSONPointerReuse pins that a lexer reused via ResetWithBytes rewinds the tracker.
func TestJSONPointerReuse(t *testing.T) {
	l := NewWithBytes([]byte(`{"a":1}`), WithJSONPointer(true))
	_, err := collectPtrsPull(l)
	require.NoError(t, err)

	l.ResetWithBytes([]byte(`[9,8]`))
	got, err := collectPtrsPull(l)
	require.NoError(t, err)
	assert.Equal(t, []string{"", "/0", "/1", ""}, got, "tracker must rewind on reuse")
}

// ptrLexer is the shared surface of *L and *VL exercised by the pointer tests, so a single table drives both lexers
// across pull/push and buffer/stream.
type ptrLexer interface {
	NextToken() token.T
	Tokens() iter.Seq[token.T]
	JSONPointer() expressions.Pointer
	Ok() bool
	Err() error
}

// collectPtrsPull walks the lexer with NextToken and records the RFC 6901 string form of JSONPointer() at each non-EOF
// token.
func collectPtrsPull(l ptrLexer) ([]string, error) {
	var ptrs []string
	for {
		t := l.NextToken()
		if !l.Ok() {
			return ptrs, l.Err()
		}
		if t.Kind() == token.EOF {
			return ptrs, nil
		}
		ptrs = append(ptrs, l.JSONPointer().String())
	}
}

// collectPtrsPush walks the lexer with the Tokens() iterator and records the same.
func collectPtrsPush(l ptrLexer) ([]string, error) {
	var ptrs []string
	for range l.Tokens() {
		ptrs = append(ptrs, l.JSONPointer().String())
	}

	return ptrs, l.Err()
}

type pointerCase struct {
	name string
	in   string
	ptrs []string
}

// pointerCases: input → the JSON pointer (RFC 6901 string form) expected at each successive token.
//
// Uses semantic-lexer token streams (separators elided); the pointer is derived from value/key/delimiter tokens, so it
// is identical whether or not separators are elided (asserted separately for VL below).
func pointerCases() iter.Seq[pointerCase] {
	return slices.Values([]pointerCase{
		{"scalar-root", `42`, []string{""}},
		{"empty-object", `{}`, []string{"", ""}},
		{"empty-array", `[]`, []string{"", ""}},
		{
			"flat-object",
			`{"a":1,"b":2}`,
			[]string{"", "/a", "/a", "/b", "/b", ""},
		},
		{
			"flat-array",
			`[10,20,30]`,
			[]string{"", "/0", "/1", "/2", ""},
		},
		{
			"object-with-array",
			`{"a":[10,20],"b":true}`,
			[]string{"", "/a", "/a", "/a/0", "/a/1", "/a", "/b", "/b", ""},
		},
		{
			"nested-arrays",
			`[[1],[2]]`,
			[]string{"", "/0", "/0/0", "/0", "/1", "/1/0", "/1", ""},
		},
		{
			"nested-objects",
			`{"x":{"y":5}}`,
			[]string{"", "/x", "/x", "/x/y", "/x/y", "/x", ""},
		},
		{
			"object-array-object",
			`{"a":[{"b":1}]}`,
			[]string{"", "/a", "/a", "/a/0", "/a/0/b", "/a/0/b", "/a/0", "/a", ""},
		},
		{
			"array-of-objects",
			`[{"k":1},{"k":2}]`,
			[]string{"", "/0", "/0/k", "/0/k", "/0", "/1", "/1/k", "/1/k", "/1", ""},
		},
		{
			"rfc-escaping",
			`{"m/n":1,"p~q":2}`, // literal '/' and '~' in member names → escaped by Pointer.String
			[]string{"", "/m~1n", "/m~1n", "/p~0q", "/p~0q", ""},
		},
	})
}

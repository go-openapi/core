package lexer_test

import (
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	jsonlexer "github.com/go-openapi/core/json/lexers/default-lexer"
	"github.com/go-openapi/core/json/lexers/token"
	yamllexer "github.com/go-openapi/core/json/lexers/yaml-lexer"
)

// A leading UTF-8 byte order mark.
//
// YAML 1.2 allows a document to be prefixed by a BOM, and it is not content. goccy does not
// strip it, and the consequence is not a dirty value but a different parse: the mark becomes the
// first character of the first token, so "<BOM>{}" comes back as the SCALAR "<BOM>{}" instead of
// an empty mapping. YL therefore strips it before parsing (walk.go:stripBOM).
//
// Found by the fuzzer as a JSON-subset divergence — L consumes a leading BOM, so the two lexers
// disagreed on documents that are valid to both.

const bomPrefix = "\uFEFF"

func TestLeadingBOMDoesNotChangeTheParse(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "number", body: "0"},
		{name: "number after a tab", body: "\t0"},
		{name: "empty flow mapping", body: "{}"},
		{name: "flow sequence", body: "[1]"},
		{name: "flow mapping", body: "{a: 1}"},
		{name: "block mapping", body: "a: 1\n"},
		{name: "block sequence", body: "- 1\n- 2\n"},
		{name: "quoted scalar", body: `"x"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := lexKinds(t, tc.body)
			got := lexKinds(t, bomPrefix+tc.body)

			assert.Equal(
				t,
				want,
				got,
				"a leading BOM is a document prefix, not content: it must not change the token stream",
			)
		})
	}
}

// TestLeadingBOMMatchesTheJSONLexer is the invariant the fuzzer checks, pinned directly: for a
// document both lexers accept, they must produce the same stream — BOM or no BOM.
func TestLeadingBOMMatchesTheJSONLexer(t *testing.T) {
	for _, src := range []string{
		bomPrefix + "\t0",
		bomPrefix + "0",
		bomPrefix + "{}",
		bomPrefix + "[1]",
		bomPrefix + `{"a": 1}`,
		bomPrefix + `"x"`,
	} {
		t.Run(src, func(t *testing.T) {
			jl := jsonlexer.NewWithBytes([]byte(src))
			var want []string
			for tok := range jl.Tokens() {
				if tok.Kind() != token.EOF {
					want = append(want, tok.Kind().String()+"("+string(tok.Value())+")")
				}
			}
			require.NoError(
				t,
				jl.Err(),
				"the JSON lexer must accept this for the comparison to mean anything",
			)

			assert.Equal(t, want, lexKinds(t, src))
		})
	}
}

// TestBOMOnlyAtTheStart pins the boundary: U+FEFF anywhere else is an ordinary character.
func TestBOMOnlyAtTheStart(t *testing.T) {
	t.Run("inside a scalar", func(t *testing.T) {
		assert.Equal(
			t,
			[]string{"delimiter()", "key(a)", "string(" + bomPrefix + "x)", "delimiter()"},
			lexKinds(t, "a: "+bomPrefix+"x\n"),
		)
	})

	t.Run("doubled: the second is content", func(t *testing.T) {
		got := lexKinds(t, bomPrefix+bomPrefix+"0")
		assert.Equal(t, []string{"string(" + bomPrefix + "0)"}, got,
			"only ONE leading mark is a prefix; the second belongs to the scalar")
	})
}

// lexKinds renders YL's stream as "kind(value)" strings.
func lexKinds(t *testing.T, src string) []string {
	t.Helper()

	l := yamllexer.NewWithBytes([]byte(src))
	var out []string
	for tok := range l.Tokens() {
		if tok.Kind() == token.EOF {
			continue
		}
		out = append(out, tok.Kind().String()+"("+string(tok.Value())+")")
	}
	require.NoErrorf(t, l.Err(), "must lex: %q", src)

	return out
}

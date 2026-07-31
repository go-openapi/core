package lexer_test

import (
	"strconv"
	"strings"
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

// TestOffsetAddressesTheSource pins that Offset is a 0-based index into the caller's bytes: the
// token's first byte is src[Offset()]. goccy counts from 1, so the walk converts; the property
// asserted here is the one consumers rely on (slicing the source), not the conversion.
//
// Note this deliberately does NOT match L.Offset, which is a consumption cursor pointing past the
// token it returned. See the doc on YL.Offset.
func TestOffsetAddressesTheSource(t *testing.T) {
	for _, src := range []string{
		"abc: 1\n",
		"a:\n  - 10\n  - twenty\n",
		"{a: 1, bb: 22}\n",
		"\ufeffabc: 1\n", // with a BOM, offsets stay on the caller's bytes
		"# lead\nk: v\n", // goccy #856: its own offset is short by one per comment line
		"# c1\n# c2\nk: v\n",
		"a: 1 # trailing\nb: 2\n",
		"a:\n  # inner\n  b: 2\n",
		"é: 1\nb: 2\n", // multi-byte runes: goccy's offset counts runes, not bytes
		"k: héllo wörld\nb: 2\n",
		"ключ: значение\nb: 2\n",
		"k: \U0001F600\nlonger: 2\n", // outside the BMP: 4 bytes, one column
		"# ünïcode\nk: v\n",          // both defects at once
	} {
		t.Run(strconv.Quote(src), func(t *testing.T) {
			for _, got := range lexPositions(t, src) {
				val := got.val
				if val == "" {
					continue // delimiters carry no text to find
				}

				require.LessOrEqual(t, got.off, uint64(len(src)),
					"offset past end of input for %q", val)
				assert.Truef(t, strings.HasPrefix(src[got.off:], val) ||
					strings.HasPrefix(src[got.off:], `"`+val) ||
					strings.HasPrefix(src[got.off:], "'"+val),
					"src[%d:] should start with %q, got %q", got.off, val, truncate(src[got.off:]))
			}
		})
	}
}

func truncate(s string) string {
	if len(s) > 12 {
		return s[:12] + "..."
	}

	return s
}

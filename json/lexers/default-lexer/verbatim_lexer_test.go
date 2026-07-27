package lexer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	codes "github.com/go-openapi/core/json/lexers/error-codes"
	"github.com/go-openapi/core/json/lexers/token"
)

func TestVerbatimExample(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join(currentDir(), "testdata", "example.json"))
	require.NoError(t, err)

	t.Run("with reader", testFixtureVerbatim(func() *VL {
		rdr := bytes.NewBuffer(fixture)
		return NewVerbatim(rdr, WithBufferSize(50))
	}))

	t.Run("with buffer", testFixtureVerbatim(func() *VL {
		return NewVerbatimWithBytes(fixture)
	}))
}

func TestVerbatim(t *testing.T) {
	rdr := bytes.NewBufferString(`  {   "a":  "1",    "b":    123  , "c"  :  false }   `)
	lex := NewVerbatim(rdr, WithBufferSize(50))

	var (
		i   int
		tok token.T
	)

	t.Run("should preseve blank space in tokens", func(t *testing.T) {
		for ; !tok.IsEOF(); i++ {
			tok = lex.NextToken()
			require.NoErrorf(t, lex.Err(), errorDetails(t, lex.Err(), lex))
			require.True(t, lex.Ok())
			t.Logf("-> %v", tok)
			t.Logf("-> %q", lex.LeadingSpace())
			blanks := string(lex.LeadingSpace())
			require.Empty(t, strings.TrimSpace(blanks))
			switch i {
			case 0: // {
				require.Len(t, blanks, 2)
			case 1: // "a"
				require.Len(t, blanks, 3)
			case 2: // :
				require.Empty(t, blanks)
			case 3: // "1"
				require.Len(t, blanks, 2)
			case 4: // ,
				require.Empty(t, blanks)
			case 5: // "b"
				require.Len(t, blanks, 4)
			case 6: // :
				require.Empty(t, blanks)
			case 7: // 123
				require.Len(t, blanks, 4)
			case 8: // ,
				require.Len(t, blanks, 2)
			case 9: // "c"
				require.Len(t, blanks, 1)
			case 10: // :
				require.Len(t, blanks, 2)
			case 11: // false
				require.Len(t, blanks, 2)
			case 12: // }
				require.Len(t, blanks, 1)
			case 13: // EOF
				t.Run("should keep trailing blanks before EOF", func(t *testing.T) {
					// EOF
					blanks := string(lex.LeadingSpace())
					require.Empty(t, strings.TrimSpace(blanks))
					require.Len(t, blanks, 3)
				})
			default:
				t.Errorf("unexpected number of tokens: %d", i)
				t.FailNow()
			}
		}
	})

	t.Logf("last -> %v", tok)
	t.Logf("last -> %q", lex.LeadingSpace())
}

func errorDetails(
	t testing.TB,
	err error,
	reporter interface{ ErrInContext() *codes.ErrContext },
) string {
	t.Helper()

	if err == nil {
		return ""
	}

	ctx := reporter.ErrInContext()
	require.NotNil(t, ctx)
	return fmt.Sprintf("unexpected error: %#v\n%s",
		ctx, ctx.Pretty(50),
	)
}

func testFixtureVerbatim(newLexer func() *VL) func(*testing.T) {
	return func(t *testing.T) {
		lex := newLexer()

		var (
			i   int
			tok token.T
		)

		for ; !tok.IsEOF(); i++ {
			tok = lex.NextToken()
			require.NoErrorf(t, lex.Err(), errorDetails(t, lex.Err(), lex))
			require.True(t, lex.Ok())
			t.Logf("-> %v", tok)
		}

		t.Logf("split %d tokens", i)
	}
}

// ---- merged from vs_test.go ---- vsInputs are well-formed documents exercising whitespace (leading/trailing/pretty),
// escapes, \u, unicode, numbers, nesting and multi-line positions — the surface the verbatim lexer VL must reproduce
// faithfully.
func vsInputs() []string {
	return []string{
		`""`, `"a"`, `"hello world"`, `"esc\ttab\n\"q\""`, `"uniécode"`, `"héllo😀"`,
		`0`, `-0`, `42`, `-3.14`, `1e10`, `12.34E-5`,
		`true`, `false`, `null`,
		`[]`, `{}`, `[1,2,3]`, `{"a":1}`, `{"a":1,"b":[true,false,null]}`,
		`  42  `, "\n\t[ 1 ,\n\t  2 ]\n", `{ "k" : "v" , "n" : 3.5 }`,
		"{\n  \"a\": 1,\n  \"b\": {\n    \"c\": [ 10, 20 ]\n  }\n}",
		`   `, // whitespace-only (no value → ErrNoData; still must reconstruct the blanks)
		`{"desc":"` + strings.Repeat("word ", 40) + `\n" }`,
		`[` + strings.Repeat(`"`+strings.Repeat("x", 100)+`",`, 20) + `"tail"]`,
	}
}

// renderRaw reconstructs a token's raw source text (VL keeps string/number values raw, so this is exact).
//
// Separators are not elided by VL, so they arrive as delimiter tokens.
func renderRaw(t token.T) string {
	switch t.Kind() {
	case token.String, token.Key:
		return `"` + string(t.Value()) + `"`
	case token.Number:
		return string(t.Value())
	case token.Boolean:
		if t.Bool() {
			return "true"
		}

		return "false"
	case token.Null:
		return "null"
	case token.Delimiter:
		return t.Delimiter().String()
	default:
		return ""
	}
}

// TestVerbatimRoundTrip proves the verbatim lexer is FAITHFUL: reconstructing the input from each token's
// LeadingSpace() + raw text yields the original document byte for byte — the whole point of a verbatim lexer,
// delivered with a light token.T + accessors instead of a heavy per-token verbatim token.
//
// Whole-buffer and streaming.
func TestVerbatimRoundTrip(t *testing.T) {
	for _, in := range vsInputs() {
		data := []byte(in)

		if got, ok := reconstruct(NewVerbatimWithBytes(data)); ok && got != in {
			t.Errorf("buffer round-trip mismatch\n in=%q\nout=%q", in, got)
		}

		for _, bs := range []int{1, 3, 8, 32, 256} {
			if got, ok := reconstruct(
				NewVerbatim(bytes.NewReader(data), WithBufferSize(bs)),
			); ok &&
				got != in {
				t.Errorf("stream(bs=%d) round-trip mismatch\n in=%q\nout=%q", bs, in, got)
			}
		}
	}
}

// reconstruct rebuilds the source from a VL token stream: LeadingSpace() before each token (and before EOF, the
// trailing blanks) plus the token's raw text. ok is false if the document was rejected (nothing to round-trip against).
func reconstruct(vl *VL) (string, bool) {
	var b strings.Builder
	for {
		tok := vl.NextToken()
		b.Write(vl.LeadingSpace())
		if !vl.Ok() {
			return "", false
		}
		if tok.Kind() == token.EOF {
			return b.String(), true
		}
		b.WriteString(renderRaw(tok))
	}
}

// ---- merged from position_test.go ----
type pos struct{ line, col int }

// doc spans two lines; column/line are 1-based:
//
//	line 1: {"a": 12,
//	line 2:  "b": true}
//
// expected start positions of every token (separators included; VL never elides):
//
//	{    (1,1)
//	"a"  (1,2)   :  (1,5)   12  (1,7)   ,  (1,9)
//	"b"  (2,2)   :  (2,5)   true(2,7)   } (2,11)
const posDoc = "{\"a\": 12,\n \"b\": true}"

func posWant() []pos {
	return []pos{
		{1, 1},
		{1, 2},
		{1, 5},
		{1, 7},
		{1, 9},
		{2, 2},
		{2, 5},
		{2, 7},
		{2, 11},
	}
}

func TestLinePosition(t *testing.T) {
	t.Run("VL reports start line/column as lexer state", func(t *testing.T) {
		vl := NewVerbatimWithBytes([]byte(posDoc))

		var got []pos
		for {
			tok := vl.NextToken()
			if !vl.Ok() || tok.IsEOF() {
				break
			}
			got = append(got, pos{vl.Line(), vl.Column()})
		}
		require.NoError(t, vl.Err())
		assert.Equal(t, posWant(), got)
	})

	t.Run("VL streaming with a tiny buffer reports the same positions", func(t *testing.T) {
		vl := NewVerbatim(strings.NewReader(posDoc), WithBufferSize(4))

		var got []pos
		for {
			tok := vl.NextToken()
			if !vl.Ok() || tok.IsEOF() {
				break
			}
			got = append(got, pos{vl.Line(), vl.Column()})
		}
		require.NoError(t, vl.Err())
		assert.Equal(t, posWant(), got)
	})
}

func TestLinePositionMultiline(t *testing.T) {
	// values spread across several lines, with a CRLF line ending mixed in
	doc := "[\n  1,\r\n  2,\n  3\n]"
	// 	line1: [
	// 	line2:   1,
	// 	line3:   2,
	// 	line4:   3
	// 	line5: ]
	vl := NewVerbatimWithBytes([]byte(doc))

	var got []pos
	for {
		tok := vl.NextToken()
		if !vl.Ok() || tok.IsEOF() {
			break
		}
		got = append(got, pos{vl.Line(), vl.Column()})
	}
	require.NoError(t, vl.Err())
	assert.Equal(t, []pos{
		{1, 1},         // [
		{2, 3}, {2, 4}, // 1 ,
		{3, 3}, {3, 4}, // 2 ,
		{4, 3}, // 3
		{5, 1}, // ]
	}, got)
}

// ---- merged from rawstring_test.go ----
func isStr(k token.Kind) bool { return k == token.String || k == token.Key }

// firstVerbatimString drains to the first String/Key token and returns raw + decoded.
func firstVerbatimString(t *testing.T, vl *VL, src string) (raw, dec string) {
	t.Helper()
	for {
		tok := vl.NextToken()
		require.Truef(t, vl.Ok(), "lex error on %q: %v", src, vl.Err())
		if tok.IsEOF() {
			t.Fatalf("no string token in %q", src)
		}
		if isStr(tok.Kind()) {
			return string(tok.Value()), token.UnescapeString(tok.Value())
		}
	}
}

func TestVerbatimKeepsRawStrings(t *testing.T) {
	cases := []struct {
		src     string // a bare JSON string literal (with quotes)
		wantRaw string // expected VT.Value() (between quotes, escapes intact)
		wantDec string // expected VT.Unescaped()
	}{
		{`"plain"`, "plain", "plain"},
		{`"a\nb"`, `a\nb`, "a\nb"},
		{`"tab\tx"`, `tab\tx`, "tab\tx"},
		{`"q\"q"`, `q\"q`, `q"q`},
		{`"back\\slash"`, `back\\slash`, `back\slash`},
		{`"slash\/x"`, `slash\/x`, "slash/x"},
		{`"Az"`, `Az`, "Az"},
		{`"emoji😀!"`, `emoji😀!`, "emoji😀!"},
		{`"café"`, `café`, "café"},
		{`"literalé"`, "literalé", "literalé"},
	}
	for _, c := range cases {
		raw, dec := firstVerbatimString(t, NewVerbatimWithBytes([]byte(c.src)), c.src)
		assert.Equalf(t, c.wantRaw, raw, "raw for %s", c.src)
		assert.Equalf(t, c.wantDec, dec, "decoded for %s", c.src)
	}
}

func TestVerbatimRawStreamingMatchesWholeBuffer(t *testing.T) {
	srcs := []string{
		`"a\nb\tcA😀tail with spaces and é"`,
		`"éèê"`,
		`"no escapes just a fairly long plain ascii string value"`,
		`"mixed \"quotes\" and \\ backslashes \/ slashes"`,
	}
	for _, s := range srcs {
		wRaw, wDec := firstVerbatimString(t, NewVerbatimWithBytes([]byte(s)), s)
		// tiny buffer forces refills mid-string
		gRaw, gDec := firstVerbatimString(
			t,
			NewVerbatim(strings.NewReader(s), WithBufferSize(4)),
			s,
		)
		assert.Equalf(t, wRaw, gRaw, "streaming raw for %s", s)
		assert.Equalf(t, wDec, gDec, "streaming decoded for %s", s)
	}
}

func TestSemanticStillDecodes(t *testing.T) {
	l := NewWithBytes([]byte(`"a\nbA"`))
	tok := l.NextToken()
	require.True(t, l.Ok())
	assert.Equal(t, "a\nbA", string(tok.Value()), "semantic Value must stay decoded")
}

func TestVerbatimRawRejectsBadEscapes(t *testing.T) {
	bad := []string{
		`"bad\q"`,
		`"short\u12"`,
		`"badhex\uZZZZ"`,
		`"lonesurr\ud83d"`,
	}
	for _, s := range bad {
		vl := NewVerbatimWithBytes([]byte(s))
		for vl.Ok() {
			if vl.NextToken().IsEOF() {
				break
			}
		}
		assert.Falsef(t, vl.Ok(), "expected error for %s", s)
	}
}

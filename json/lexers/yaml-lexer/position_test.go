package lexer_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	"github.com/go-openapi/core/json/lexers/token"
	yamllexer "github.com/go-openapi/core/json/lexers/yaml-lexer"
)

// Source positions (Line/Column/Offset).
//
// Block collections have no "{" / "[" / "}" / "]" characters, so goccy has no token to point a
// container delimiter at: it hands us the first entry's separator (the ":" of the first pair, the
// "-" of the first item) as the node's Start, and nothing at all as its End. Used literally that
// put the opening delimiter AFTER the key it precedes and the closing one at line 0 column 0 —
// so a consumer laying tokens out by position had to sort them back and discard the closers.
//
// walk.go:patchBlockSpan gives them the span of what they enclose instead. These tests pin that,
// and pin that FLOW collections keep their real delimiter positions untouched.

// posTok is one token with the position YL reported for it.
type posTok struct {
	kind token.Kind
	val  string
	line int
	col  int
	off  uint64
}

func (p posTok) String() string {
	return fmt.Sprintf("L%dC%d %v(%q)", p.line, p.col, p.kind, p.val)
}

func lexPositions(t *testing.T, src string, opts ...yamllexer.Option) []posTok {
	t.Helper()

	l := yamllexer.NewWithBytes([]byte(src), opts...)
	var out []posTok
	for tok := range l.Tokens() {
		if tok.Kind() == token.EOF {
			break
		}
		out = append(out, posTok{
			kind: tok.Kind(), val: string(tok.Value()),
			line: l.Line(), col: l.Column(), off: l.Offset(),
		})
	}
	require.NoError(t, l.Err(), "input must lex: %q", src)

	return out
}

// TestBlockContainerSpan pins where a block collection's delimiters land: the opening one on the
// first token it encloses, the closing one on the last.
func TestBlockContainerSpan(t *testing.T) {
	t.Run("nested block mappings", func(t *testing.T) {
		got := lexPositions(t, "info:\n  title: Petstore\n  version: 1\n")

		assert.Equal(t, []string{
			`L1C1 delimiter("")`, // root mapping opens on `info`, not on its colon
			`L1C1 key("info")`,
			`L2C3 delimiter("")`, // /info opens on `title`, not on the colon a line below
			`L2C3 key("title")`,
			`L2C10 string("Petstore")`,
			`L3C3 key("version")`,
			`L3C12 number("1")`,
			`L3C12 delimiter("")`, // /info closes on its last token
			`L3C12 delimiter("")`, // root closes there too
		}, render(got))
	})

	t.Run("block sequence", func(t *testing.T) {
		got := lexPositions(t, "schemes:\n  - http\n  - https\n")

		assert.Equal(t, []string{
			`L1C1 delimiter("")`,
			`L1C1 key("schemes")`,
			`L2C5 delimiter("")`, // opens on `http`, not on the "-" before it
			`L2C5 string("http")`,
			`L3C5 string("https")`,
			`L3C5 delimiter("")`,
			`L3C5 delimiter("")`,
		}, render(got))
	})
}

// TestFlowContainerPositionsUnchanged pins the other side of the fix: a flow collection HAS real
// delimiter characters, and those positions must survive untouched.
func TestFlowContainerPositionsUnchanged(t *testing.T) {
	got := lexPositions(t, "tags: [a, b]\n")

	assert.Equal(t, []string{
		`L1C1 delimiter("")`,
		`L1C1 key("tags")`,
		`L1C7 delimiter("")`, // the real "["
		`L1C8 string("a")`,
		`L1C11 string("b")`,
		`L1C12 delimiter("")`, // the real "]"
		`L1C12 delimiter("")`,
	}, render(got))

	t.Run("flow mapping", func(t *testing.T) {
		got := lexPositions(t, "m: {a: 1}\n")
		assert.Equal(t, `L1C4 delimiter("")`, render(got)[2], "the real \"{\"")
		assert.Equal(t, `L1C9 delimiter("")`, render(got)[5], "the real \"}\"")
	})
}

// TestNoTokenHasAZeroPosition pins that every emitted token carries a usable position. Line and
// column are 1-based, so 0 is not a position: a consumer that sees one can only discard the token.
func TestNoTokenHasAZeroPosition(t *testing.T) {
	for _, src := range []string{
		"info:\n  title: Petstore\n",
		"schemes:\n  - http\n  - https\n",
		"tags: [a, b]\n",
		"m: {a: 1}\n",
		"a:\n  b:\n    c:\n      - 1\n      - x: 2\n",
		"- 1\n- 2\n",
		"root: null\n",
		"empty: {}\n",
		"emptyseq: []\n",
	} {
		t.Run(src, func(t *testing.T) {
			for _, p := range lexPositions(t, src) {
				assert.Positivef(t, p.line, "%s: line must be 1-based", p)
				assert.Positivef(t, p.col, "%s: column must be 1-based", p)
			}
		})
	}
}

// TestPositionsAreNonDecreasing is the property the TUI actually needs: laying tokens out by
// position must not require sorting them first.
//
// Non-decreasing rather than strictly increasing: in block style a container delimiter has no
// character of its own, so it shares its neighbour's position.
func TestPositionsAreNonDecreasing(t *testing.T) {
	for _, src := range []string{
		"info:\n  title: Petstore\n  version: 1\n",
		"schemes:\n  - http\n  - https\n",
		"tags: [a, b]\n",
		"openapi: 3.0.0\ninfo:\n  title: t\npaths:\n  /p:\n    get:\n      responses:\n        '200':\n          description: ok\n",
		"- a\n- b: 1\n  c: 2\n- - nested\n  - seq\n",
	} {
		t.Run(src, func(t *testing.T) {
			assertNonDecreasing(t, lexPositions(t, src))
		})
	}
}

// TestAliasPositionsStillPointAtTheAnchor documents the one case that stays out of order, so the
// behaviour is a recorded decision rather than an accident.
//
// Alias-expanded tokens deliberately report the ANCHOR DEFINITION site (see walkAlias), which is
// earlier in the document than the alias. A consumer highlighting an alias-bearing document
// therefore still cannot assume monotonicity.
func TestAliasPositionsStillPointAtTheAnchor(t *testing.T) {
	got := lexPositions(t, "defs: &d\n  p: 1\nuse: *d\n")

	var backwards bool
	prev := posTok{}
	for _, p := range got {
		if p.line < prev.line || (p.line == prev.line && p.col < prev.col) {
			backwards = true
		}
		prev = p
	}

	assert.True(t, backwards,
		"expansion still reports the anchor site: if this ever stops being true, the walkAlias "+
			"doc and the TUI's ordering assumptions both need revisiting")
}

// TestOffsetTracksPosition pins that Offset moved with Line/Column — it comes from the same
// source position, so a container delimiter used to report offset 0 as well.
func TestOffsetTracksPosition(t *testing.T) {
	src := "info:\n  title: Petstore\n"
	got := lexPositions(t, src)

	for _, p := range got {
		assert.LessOrEqualf(t, p.off, uint64(len(src)), "%s: offset must be inside the input", p)
	}
	// the closing delimiters sit on the last token, not at offset 0
	last := got[len(got)-1]
	assert.Positivef(t, last.off, "%s: a closing delimiter must carry a real offset", last)
}

func render(toks []posTok) []string {
	out := make([]string, 0, len(toks))
	for _, p := range toks {
		out = append(out, p.String())
	}

	return out
}

func assertNonDecreasing(t *testing.T, toks []posTok) {
	t.Helper()

	prev := posTok{line: 1, col: 1}
	for i, p := range toks {
		ok := p.line > prev.line || (p.line == prev.line && p.col >= prev.col)
		assert.Truef(t, ok, "token %d goes backwards: %s after %s\nstream: %s",
			i, p, prev, strings.Join(render(toks), " "))
		prev = p
	}
}

// TestPositionsOverTheWholeSuite runs the position invariants over every document the vendored
// YAML Test Suite says is valid — 400-odd real-world shapes rather than the handful above.
//
// Alias-bearing documents are skipped for the ordering check only: expansion reports the anchor
// site by design (see TestAliasPositionsStillPointAtTheAnchor). Everything else must carry a
// 1-based position and never go backwards.
func TestPositionsOverTheWholeSuite(t *testing.T) {
	var checked, skipped int

	for _, c := range loadSuite(t) {
		if c.fail {
			continue
		}

		l := yamllexer.NewWithBytes([]byte(c.yaml),
			yamllexer.WithMaxContainerStack(512), yamllexer.WithMaxTokens(1<<20))

		var toks []posTok
		for tok := range l.Tokens() {
			if tok.Kind() == token.EOF {
				break
			}
			toks = append(toks, posTok{
				kind: tok.Kind(), val: string(tok.Value()),
				line: l.Line(), col: l.Column(), off: l.Offset(),
			})
		}
		if l.Err() != nil || len(toks) == 0 {
			continue // rejected or empty: nothing to say about its positions
		}

		t.Run(c.label(), func(t *testing.T) {
			for _, p := range toks {
				require.Positivef(t, p.line, "%s: line must be 1-based\nsrc=%q", p, c.yaml)
				require.Positivef(t, p.col, "%s: column must be 1-based\nsrc=%q", p, c.yaml)
			}

			// an alias re-walks the anchored node, so its tokens report the definition site
			if strings.ContainsAny(c.yaml, "*&") {
				skipped++

				return
			}
			checked++
			assertNonDecreasing(t, toks)
		})
	}

	t.Logf("ordering checked on %d documents, %d skipped for aliases", checked, skipped)
}

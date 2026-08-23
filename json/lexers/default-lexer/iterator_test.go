// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package lexer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	"github.com/go-openapi/core/json/lexers/token"
)

func TestTokensIterator(t *testing.T) {
	const doc = `{"a":[1,2.5e3,"x",true,null],"b":{"c":false}}`

	t.Run("yields the same tokens as the NextToken loop", func(t *testing.T) {
		// reference: manual loop
		var want []token.T
		ref := NewWithBytes([]byte(doc))
		for {
			tok := ref.NextToken()
			if !ref.Ok() || tok.IsEOF() {
				break
			}
			want = append(want, tok.Clone())
		}
		require.NoError(t, ref.Err())

		// iterator
		var got []token.T
		lex := NewWithBytes([]byte(doc))
		for tok := range lex.Tokens() {
			got = append(got, tok.Clone())
		}
		require.NoError(t, lex.Err())
		require.True(t, lex.Ok())

		require.Len(t, got, len(want))
		for i := range want {
			assert.Equalf(t, want[i].Kind(), got[i].Kind(), "token %d kind", i)
			assert.Equalf(t, want[i].Value(), got[i].Value(), "token %d value", i)
		}
	})

	t.Run("does not yield EOF", func(t *testing.T) {
		lex := NewWithBytes([]byte(`[1]`))
		for tok := range lex.Tokens() {
			require.False(t, tok.IsEOF(), "EOF must not be yielded")
		}
		require.True(t, lex.Ok())
	})

	t.Run("early break stops cleanly", func(t *testing.T) {
		lex := NewWithBytes([]byte(`[1,2,3,4,5]`))
		n := 0
		for range lex.Tokens() {
			n++
			if n == 2 {
				break
			}
		}
		assert.Equal(t, 2, n)
	})

	t.Run("stops on error, recorded in state", func(t *testing.T) {
		lex := NewWithBytes([]byte(`[1,,2]`)) // repeated comma
		n := 0
		for range lex.Tokens() {
			n++
		}
		assert.False(t, lex.Ok())
		require.Error(t, lex.Err())
	})
}

func TestVerbatimTokensIterator(t *testing.T) {
	const doc = ` { "a" : [ 1 , true ] } `

	var got []token.T
	var firstBlanks string
	vl := NewVerbatimWithBytes([]byte(doc))
	for tok := range vl.Tokens() {
		if len(got) == 0 {
			firstBlanks = string(vl.LeadingSpace()) // blanks are lexer state, read per token
		}
		got = append(got, tok.Clone())
	}
	require.NoError(t, vl.Err())
	require.NotEmpty(t, got)

	// the verbatim lexer preserves leading blanks (the doc starts with a space)
	assert.NotEmpty(t, firstBlanks, "first verbatim token should carry leading blanks")
}

// ---- merged from handoff_test.go ----
func acceptFixtures(t *testing.T) map[string][]byte {
	t.Helper()
	dir := filepath.Join(currentDir(), "..", "testdata", "JSONTestSuite", "test_parsing")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	out := make(map[string][]byte)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || !strings.HasPrefix(name, "y_") {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, rerr)
		out[name] = data
	}
	require.NotEmpty(t, out)

	return out
}

// TestHandoffTokensThenNextToken_Semantic: for L, taking `cut` tokens via Tokens() then finishing via NextToken()
// yields the same stream as a pure NextToken() drain, for every cut point on every accept fixture.
func TestHandoffTokensThenNextToken_Semantic(t *testing.T) {
	type tk struct {
		kind  token.Kind
		value string
	}
	mk := func(x token.T) tk { return tk{x.Kind(), string(x.Value())} }

	for name, data := range acceptFixtures(t) {
		var want []tk
		lp := NewWithBytes(data)
		for {
			x := lp.NextToken()
			if x.IsEOF() {
				break
			}
			want = append(want, mk(x))
		}
		require.NoError(t, lp.Err(), name)

		for cut := 1; cut <= len(want); cut++ {
			l := NewWithBytes(data)
			var got []tk
			n := 0
			for x := range l.Tokens() {
				got = append(got, mk(x))
				n++
				if n == cut {
					break
				}
			}
			for {
				x := l.NextToken()
				if x.IsEOF() {
					break
				}
				got = append(got, mk(x))
			}
			require.NoErrorf(t, l.Err(), "%s cut=%d", name, cut)
			require.Equalf(t, want, got, "%s cut=%d: Tokens()->NextToken() mismatch", name, cut)
		}
	}
}

// TestHandoffTokensThenNextToken_Verbatim: same as the semantic case but for VL, also checking that blanks and position
// survive the handoff.
func TestHandoffTokensThenNextToken_Verbatim(t *testing.T) {
	type tk struct {
		kind      token.Kind
		value     string
		blanks    string
		line, col int
	}
	// blanks/position are lexer state (read via the accessors), valid for the token just returned — so mk snapshots them
	// from the lexer at each step.
	mk := func(x token.T, l *VL) tk {
		return tk{x.Kind(), string(x.Value()), string(l.LeadingSpace()), l.Line(), l.Column()}
	}

	for name, data := range acceptFixtures(t) {
		var want []tk
		lp := NewVerbatimWithBytes(data)
		for {
			x := lp.NextToken()
			if x.IsEOF() {
				break
			}
			want = append(want, mk(x, lp))
		}
		require.NoError(t, lp.Err(), name)

		for cut := 1; cut <= len(want); cut++ {
			l := NewVerbatimWithBytes(data)
			var got []tk
			n := 0
			for x := range l.Tokens() {
				got = append(got, mk(x, l))
				n++
				if n == cut {
					break
				}
			}
			for {
				x := l.NextToken()
				if x.IsEOF() {
					break
				}
				got = append(got, mk(x, l))
			}
			require.NoErrorf(t, l.Err(), "%s cut=%d", name, cut)
			require.Equalf(t, want, got, "%s cut=%d: Tokens()->NextToken() mismatch", name, cut)
		}
	}
}

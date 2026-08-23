// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package lexer_test

import (
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	"github.com/go-openapi/core/json/lexers/token"
	yamllexer "github.com/go-openapi/core/json/lexers/yaml-lexer"
)

// Mapping keys that RESOLVE to a scalar.
//
// A JSON object key is a string, so YL only accepts a key that denotes a scalar. YAML lets a key
// carry node properties and indirection that JSON has no place for but that do not stop it being
// a string — an explicit key ("? k"), a tag ("!!str k"), an anchor ("&a k"), an alias ("*a"), and
// combinations. Those were rejected as "complex" until walk.go:resolveKey peeled them; 22 of the
// YAML Test Suite's documents turn on it.
//
// A key that really is a sequence or a mapping stays rejected: that one JSON cannot express.

func keysOf(t *testing.T, src string) []string {
	t.Helper()

	l := yamllexer.NewWithBytes([]byte(src))
	var keys []string
	for tok := range l.Tokens() {
		if tok.Kind() == token.Key {
			keys = append(keys, string(tok.Value()))
		}
	}
	require.NoErrorf(t, l.Err(), "must lex: %q", src)

	return keys
}

func TestResolvedScalarKeys(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{name: "plain", src: "k: v\n", want: []string{"k"}},
		{name: "explicit key", src: "? k\n: v\n", want: []string{"k"}},
		{name: "explicit key, flow value", src: "? k\n: [1, 2]\n", want: []string{"k"}},
		{name: "tagged key", src: "!!str k: v\n", want: []string{"k"}},
		{name: "tagged integer key", src: "!!int 3: v\n", want: []string{"3"}},
		{name: "anchored key", src: "&a k: v\n", want: []string{"k"}},
		{name: "alias key", src: "d: &a name\nm:\n  *a : v\n", want: []string{"d", "m", "name"}},
		{name: "explicit + tag", src: "? !!str k\n: v\n", want: []string{"k"}},
		{name: "explicit + anchor", src: "? &a k\n: v\n", want: []string{"k"}},
		{name: "tag + anchor", src: "!!str &a k: v\n", want: []string{"k"}},
		{
			name: "anchor defined on a key is reusable",
			src:  "&a k: v\nm:\n  *a : w\n",
			want: []string{"k", "m", "k"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, keysOf(t, tc.src))
		})
	}
}

// TestComplexKeysStillRejected pins the boundary: a key that denotes a collection is outside the
// JSON data model however many wrappers are peeled off it.
//
// Which error you get depends on how the key is written, and that is worth recording. Written
// out directly, goccy refuses the document before we see it ("found an invalid key for this
// map"), so ErrComplexKey is unreachable for those. It is reached through INDIRECTION — an alias
// naming a collection — where goccy is happy and only our JSON-shaped model objects.
func TestComplexKeysStillRejected(t *testing.T) {
	t.Run("rejected upstream, before our walk", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			src  string
		}{
			{name: "sequence key", src: "? [a, b]\n: v\n"},
			{name: "mapping key", src: "? {a: 1}\n: v\n"},
			{name: "anchored sequence key", src: "? &a [x]\n: v\n"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				l := yamllexer.NewWithBytes([]byte(tc.src))
				for range l.Tokens() { //nolint:revive // draining is the point
				}
				require.Error(t, l.Err(), "a collection key must be rejected")
				assert.NotErrorIs(t, l.Err(), yamllexer.ErrComplexKey,
					"goccy rejects these at parse time, so our own guard never runs")
			})
		}
	})

	t.Run("rejected by our model", func(t *testing.T) {
		// goccy parses this happily: the key is an alias, and only resolving it reveals a sequence
		l := yamllexer.NewWithBytes([]byte("d: &a [x]\nm:\n  *a : v\n"))
		for range l.Tokens() { //nolint:revive // draining is the point
		}
		require.Error(t, l.Err())
		assert.ErrorIs(t, l.Err(), yamllexer.ErrComplexKey)
	})
}

// TestResolvedKeyPosition pins where a resolved key reports itself: on the scalar it denotes, so
// the position always holds the text the token carries.
func TestResolvedKeyPosition(t *testing.T) {
	// "&a k" -- the key token says "k", so it must point at k (column 4), not at the anchor
	// one mapping: open, key, value, close
	got := lexPositions(t, "&a k: v\n")
	require.Len(t, got, 4)
	assert.Equal(t, `L1C4 key("k")`, got[1].String(),
		"the token says \"k\", so it must point at k -- not at the anchor that precedes it")

	t.Run("an alias key points at the anchor definition", func(t *testing.T) {
		// consistent with alias-expanded VALUES (see walkAlias): the position holds the text
		got := lexPositions(t, "d: &a name\nm:\n  *a : v\n")
		var key posTok
		for _, p := range got {
			if p.kind == token.Key && p.val == "name" {
				key = p
			}
		}
		assert.Equal(
			t,
			1,
			key.line,
			"reports the anchor site (line 1), not the alias site (line 3)",
		)
	})
}

// TestResolvedKeyCycleIsRejected pins that a key aliasing its own definition terminates rather
// than recursing: the guard is shared with the alias-cycle machinery.
func TestResolvedKeyCycleIsRejected(t *testing.T) {
	l := yamllexer.NewWithBytes([]byte("&a *a : v\n"))
	for range l.Tokens() { //nolint:revive // draining is the point
	}
	assert.Error(t, l.Err(), "a self-referential key must not recurse")
}

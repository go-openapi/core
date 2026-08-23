package lexer

import (
	"testing"

	"github.com/go-openapi/testify/v2/require"
)

// TestResetDropsReferences pins the non-pinning contract a pooled lexer relies on: once redeemed, a YL holds no
// reference to the caller's source buffer — neither directly (l.data) nor through the token values that alias it.
//
// The token slice keeps its capacity across a reset, so entries left behind by a LONGER previous document are the
// case that matters: they sit past the current length, where nothing overwrites them.
func TestResetDropsReferences(t *testing.T) {
	const (
		long  = "a: xxxxxxxx\nb:\n  - 1\n  - 2\n  - 3\n"
		short = "z: 1\n"
	)

	l := NewWithBytes([]byte(long))
	for range l.Tokens() { //nolint:revive // draining the stream is the point
	}
	require.NoError(t, l.Err())
	require.NotEmpty(t, l.toks)
	longTokens := len(l.toks)
	longCap := cap(l.toks)

	// a shorter document: its build leaves the long document's entries in the retained capacity
	l.ResetWithBytes([]byte(short))
	for range l.Tokens() { //nolint:revive // draining the stream is the point
	}
	require.NoError(t, l.Err())
	require.Less(
		t,
		len(l.toks),
		longTokens,
		"the second document must be the shorter one for this test to bite",
	)

	l.Reset()

	require.Nil(t, l.data, "Reset must drop the caller's buffer")
	require.Equal(t, longCap, cap(l.toks), "Reset must keep the token slice capacity for reuse")

	for i, e := range l.toks[:cap(l.toks)] {
		require.Nilf(t, e.tok.Value(),
			"toks[%d] still aliases a source buffer after Reset: a pooled lexer would pin it", i)
		require.Zerof(t, e.off, "toks[%d] still holds a stale offset after Reset", i)
	}
}

// TestApplyWithDefaultsSeedsDefaults checks that options never leak between two builds: applyWithDefaults always
// starts from defaultOptions, so a lexer recycled through the pool cannot inherit the previous borrower's settings.
func TestApplyWithDefaultsSeedsDefaults(t *testing.T) {
	require.Equal(t, defaultOptions, applyWithDefaults(nil))

	o := applyWithDefaults([]Option{WithMaxTokens(3), WithJSONPointer(true)})
	require.Equal(t, 3, o.maxTokens)
	require.True(t, o.jsonPointer)

	require.Equal(
		t,
		defaultOptions,
		applyWithDefaults(nil),
		"a later call must not see the previous options",
	)
}

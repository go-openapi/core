package lexer_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	yamllexer "github.com/go-openapi/core/json/lexers/yaml-lexer"
)

// Merge-key ("<<") cycles.
//
// A mapping whose own merge key aliases itself resolves to its own entries, which contain that same merge key. The
// flattening in resolveMapping therefore recursed forever and the process died with a fatal stack overflow — not a
// Go panic, so nothing could recover it and no circuit breaker (WithMaxTokens, WithMaxContainerStack) applied. CI
// found it as "fuzzing process hung or terminated unexpectedly".
//
// mergeEntries did have an anchor guard, but it only spanned resolving the alias to a node list — which does not
// recurse — and was released before those entries were expanded, which is where the cycle closes.

// runBounded lexes data on a watchdog so a regression shows up as a failure rather than taking the suite down with
// it. A stack overflow cannot be recovered, so this cannot catch that directly; it does catch a plain runaway.
func runBounded(t *testing.T, data string, opts ...yamllexer.Option) (int, error) {
	t.Helper()

	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)

	go func() {
		l := yamllexer.NewWithBytes([]byte(data), opts...)
		n := 0
		for range l.Tokens() {
			n++
			if n > 100000 {
				break
			}
		}
		done <- result{n: n, err: l.Err()}
	}()

	select {
	case r := <-done:
		return r.n, r.err
	case <-time.After(10 * time.Second):
		t.Fatalf("lexing did not terminate for %q", data)

		return 0, nil
	}
}

func TestMergeKeyCycleIsRejected(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{
			name: "mapping merges itself",
			in:   "e: &b\n  <<: *b\n",
		},
		{
			name: "mapping merges itself among other keys",
			in:   "e: &b\n  a: 1\n  <<: *b\n  c: 3\n",
		},
		{
			name: "self-merge inside a merge sequence",
			in:   "e: &b\n  <<: [*b]\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runBounded(t, tc.in)
			require.Error(t, err, "a merge-key cycle must be reported, not expanded forever")
			assert.ErrorIs(t, err, yamllexer.ErrAliasCycle)
		})
	}
}

// TestMergeCycleNeedsSelfReference records WHY the self-merge is the only shape that reaches the cycle guard: an
// anchor must be defined before it is aliased, so a multi-node merge cycle cannot be written at all — closing it
// needs a forward reference, which fails earlier as an unknown anchor. That is not a second bug; it is the reason
// the fix only has to handle self-reference.
func TestMergeCycleNeedsSelfReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
	}{
		{name: "two-step", in: "x: &x\n  <<: *y\ny: &y\n  <<: *x\n"},
		{name: "three-step", in: "a: &a\n  <<: *b\nb: &b\n  <<: *c\nc: &c\n  <<: *a\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runBounded(t, tc.in)
			require.Error(t, err)
			assert.ErrorIs(
				t,
				err,
				yamllexer.ErrUnknownAnchor,
				"the cycle cannot form: the second anchor is not defined yet at the point it is aliased",
			)
		})
	}
}

// TestMergeKeyLegitimateUsesStillWork guards the other side of the fix: the guard keys on the merge SOURCE node, so
// merging one anchor several times — redundant but perfectly legal YAML — must not be mistaken for a cycle.
//
// (Repeating "<<" twice as two separate keys in the same mapping is not in this table: goccy rejects that at parse
// time as a duplicate key, before the merge resolution ever runs.)
func TestMergeKeyLegitimateUsesStillWork(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantKey string // a key that must survive the merge
	}{
		{
			name:    "simple merge",
			in:      "base: &b {a: 1, b: 2}\nderived:\n  <<: *b\n  c: 3\n",
			wantKey: "a",
		},
		{
			name:    "same anchor merged into two siblings",
			in:      "base: &b {a: 1}\nx:\n  <<: *b\ny:\n  <<: *b\n",
			wantKey: "a",
		},
		{
			name:    "same anchor twice in a merge sequence",
			in:      "base: &b {a: 1}\nx:\n  <<: [*b, *b]\n",
			wantKey: "a",
		},
		{
			name:    "chained merges, no cycle",
			in:      "a: &a {x: 1}\nb: &b\n  <<: *a\n  y: 2\nc:\n  <<: *b\n  z: 3\n",
			wantKey: "x",
		},
		{
			name:    "merge sequence of distinct anchors",
			in:      "p: &p {a: 1}\nq: &q {b: 2}\nr:\n  <<: [*p, *q]\n",
			wantKey: "b",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, err := runBounded(t, tc.in)
			require.NoError(t, err, "a legal merge must not be mistaken for a cycle")
			assert.Positive(t, n)
		})
	}
}

// TestAliasCycleWithoutMergeStillRejected pins the pre-existing guard in walkAlias, which was already correct: only
// the merge path was broken, and the fix must not have disturbed it.
func TestAliasCycleWithoutMergeStillRejected(t *testing.T) {
	// a sequence that contains an alias to itself
	_, err := runBounded(t, "a: &x\n  - *x\n")
	require.Error(t, err)
	assert.ErrorIs(t, err, yamllexer.ErrAliasCycle)
}

// TestDeepMergeChainTerminates checks that a long but ACYCLIC merge chain is still resolved rather than tripping the
// cycle guard — the guard is a stack, so it must unmark on the way out.
func TestDeepMergeChainTerminates(t *testing.T) {
	const depth = 200

	var b strings.Builder
	b.WriteString("k0: &a0 {v: 1}\n")
	for i := 1; i <= depth; i++ {
		fmt.Fprintf(&b, "k%d: &a%d\n  <<: *a%d\n", i, i, i-1)
	}

	n, err := runBounded(t, b.String())
	require.NoError(t, err)
	assert.Positive(t, n)
}

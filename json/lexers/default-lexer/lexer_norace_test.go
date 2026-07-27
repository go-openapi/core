//go:build !race

package lexer

import (
	"testing"

	"github.com/go-openapi/testify/v2/require"

	"github.com/go-openapi/swag/pools"
)

// TestBorrowRedeemAllocFree pins that a borrow→lex→redeem cycle of a small whole-buffer document allocates nothing
// (the pool amortizes the lexer and its scratch buffers).
//
// It is gated to non-race builds: testing.AllocsPerRun is unreliable under -race (the race detector instruments
// allocations, inflating the count), so the 0-alloc property is enforced here, in the build that reflects production.
// This mirrors the writer's TestAllocs in writer_norace_test.go.
func TestBorrowRedeemAllocFree(t *testing.T) {
	if pools.DebugBuild {
		t.Skip("the poolsdebug build allocates a per-borrow redeemer to track redemptions")
	}

	docA := []byte(`{"a":[1,-2,3.5e2],"b":true}`)

	// warm the pool so the first Borrow does not allocate the lexer itself
	_, redeem := BorrowLexerWithBytes(docA)
	redeem()

	allocs := testing.AllocsPerRun(100, func() {
		l, redeem := BorrowLexerWithBytes(docA)
		for {
			tk := l.NextToken()
			if tk.IsEOF() || !l.Ok() {
				break
			}
		}
		redeem()
	})
	require.Zerof(t, allocs, "borrow→lex→redeem of a small doc must not allocate, got %v", allocs)
}

//go:build !race

package lexer

import (
	"testing"

	"github.com/go-openapi/testify/v2/require"

	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/swag/pools"
)

// TestTokensPushAllocFreePointerOff pins that the push iterator (range over Tokens) stays zero-allocation when the
// JSON-pointer option is OFF — i.e. that routing the tracked case through the NextToken fallback loop did not make
// the loop-body yield closure escape on the champion path.
//
// Gated to non-race builds (see TestBorrowRedeemAllocFree for why AllocsPerRun is unreliable under -race).
func TestTokensPushAllocFreePointerOff(t *testing.T) {
	if pools.DebugBuild {
		t.Skip("the poolsdebug build allocates a per-borrow redeemer")
	}

	doc := []byte(`{"a":[1,-2,3.5e2],"b":true,"c":{"d":null}}`)
	l := NewWithBytes(doc) // pointer tracking OFF (default)

	allocs := testing.AllocsPerRun(100, func() {
		l.ResetWithBytes(doc)
		for tok := range l.Tokens() {
			if tok.Kind() == token.EOF {
				break
			}
		}
	})
	require.Zerof(
		t,
		allocs,
		"push iteration with pointer tracking off must not allocate, got %v",
		allocs,
	)
}

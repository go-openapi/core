// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

//go:build !race

package lexer_test

import (
	"testing"

	"github.com/go-openapi/swag/pools"
	"github.com/go-openapi/testify/v2/require"

	yamllexer "github.com/go-openapi/core/json/lexers/yaml-lexer"
)

// docPool is the small document the pool guards borrow.
const docPool = "a:\n  - 1\n  - -2\nb: true\n"

// TestBorrowRedeemAllocFree pins that a borrow→redeem cycle allocates nothing: the pool amortizes the YL, and
// [pools.PoolRedeemable] hands out a cached redeem closure instead of building one per borrow.
//
// The cycle measured here stops short of lexing, and that is not an oversight. Unlike the JSON lexer, YL cannot make
// a full borrow→lex→redeem cycle allocation-free: build hands the source to goccy/go-yaml, which allocates a fresh
// AST for every parse. What the pool amortizes is the YL and the capacity of its token slice.
//
// It is gated to non-race builds: testing.AllocsPerRun is unreliable under -race (the race detector instruments
// allocations, inflating the count), so the 0-alloc property is enforced here, in the build that reflects production.
func TestBorrowRedeemAllocFree(t *testing.T) {
	if pools.DebugBuild {
		t.Skip("the poolsdebug build allocates a per-borrow redeemer to track redemptions")
	}

	data := []byte(docPool)

	// warm the pool so the first Borrow does not allocate the lexer itself
	_, redeem := yamllexer.BorrowLexerWithBytes(data)
	redeem()

	allocs := testing.AllocsPerRun(100, func() {
		_, redeem := yamllexer.BorrowLexerWithBytes(data)
		redeem()
	})
	require.Zerof(t, allocs, "borrow→redeem must not allocate, got %v", allocs)
}

// TestBorrowWithOptionsAllocFree pins that passing options to a borrow costs nothing: the variadic slice, the option
// closures it holds and the options struct they build all stay on the stack.
//
// The options struct carries no pointer field, so applyWithDefaults seeds and returns it by value with no heap
// traffic — but that only holds while the option closures stay non-escaping, which is a property of inlining and
// escape analysis rather than of the source. This test fails if a future change (an option capturing a slice, a call
// site the compiler stops inlining) pushes them to the heap.
//
// WithJSONPointer(true) is deliberately absent: it forfeits the guarantee by design, since the path stack allocates
// and object keys are interned.
//
// Gated to non-race builds for the same reason as [TestBorrowRedeemAllocFree].
func TestBorrowWithOptionsAllocFree(t *testing.T) {
	if pools.DebugBuild {
		t.Skip("the poolsdebug build allocates a per-borrow redeemer to track redemptions")
	}

	data := []byte(docPool)

	// warm the pool so the first Borrow does not allocate the lexer itself
	_, redeem := yamllexer.BorrowLexerWithBytes(data, yamllexer.WithMaxTokens(1000))
	redeem()

	allocs := testing.AllocsPerRun(100, func() {
		_, redeem := yamllexer.BorrowLexerWithBytes(
			data,
			yamllexer.WithMaxContainerStack(64),
			yamllexer.WithMaxValueBytes(1<<20),
			yamllexer.WithMaxTokens(1000),
			yamllexer.WithJSONPointer(false),
		)
		redeem()
	})
	require.Zerof(t, allocs, "borrowing with options must not allocate, got %v", allocs)
}

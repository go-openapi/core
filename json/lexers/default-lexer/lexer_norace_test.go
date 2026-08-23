// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

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

// TestBorrowWithOptionsAllocFree pins that passing options to a borrow costs nothing: the variadic slice, the option
// closures it holds and the [options] struct they build all stay on the stack.
//
// [options] carries no pointer field, so applyWithDefaults seeds and returns it by value with no heap traffic — but
// that only holds while the option closures stay non-escaping, which is a property of inlining and escape analysis
// rather than of the source. This test fails if a future change (an option capturing a slice, a call site the compiler
// stops inlining) pushes them to the heap.
//
// The options chosen leave the zero-alloc paths intact. WithJSONPointer(true) is deliberately absent: it forfeits the
// guarantee by design, since the path stack allocates and object keys are copied out of the value buffer.
//
// Gated to non-race builds for the same reason as [TestBorrowRedeemAllocFree].
func TestBorrowWithOptionsAllocFree(t *testing.T) {
	if pools.DebugBuild {
		t.Skip("the poolsdebug build allocates a per-borrow redeemer to track redemptions")
	}

	docA := []byte(`{"a":[1,-2,3.5e2],"b":true}`)
	opts := func() []Option {
		return []Option{
			WithBufferSize(8192),
			WithMaxContainerStack(64),
			WithMaxValueBytes(1 << 20),
			WithoutAVX2(true),
			WithUTF8Policy(UTF8Replace),
			WithElideSeparator(false),
		}
	}

	// warm the pool so the first Borrow does not allocate the lexer itself
	_, redeem := BorrowLexerWithBytes(docA, opts()...)
	redeem()

	allocs := testing.AllocsPerRun(100, func() {
		l, redeem := BorrowLexerWithBytes(
			docA,
			WithBufferSize(8192),
			WithMaxContainerStack(64),
			WithMaxValueBytes(1<<20),
			WithoutAVX2(true),
			WithUTF8Policy(UTF8Replace),
			WithElideSeparator(false),
		)
		for {
			tk := l.NextToken()
			if tk.IsEOF() || !l.Ok() {
				break
			}
		}
		redeem()
	})
	require.Zerof(t, allocs, "borrowing with options must not allocate, got %v", allocs)
}

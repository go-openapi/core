// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package lexer

import (
	"iter"

	"github.com/go-openapi/core/json/lexers/token"
)

// Tokens returns an iterator over the JSON tokens, as a convenience over the [L.NextToken] loop.
//
// The range yields every token up to (but not including) EOF, then ends.
// It also ends on error.
// As with NextToken, errors are kept in the lexer's state: check [L.Ok] / [L.Err] after the loop.
//
//	for tok := range lex.Tokens() {
//	    // use tok
//	}
//	if !lex.Ok() {
//	    // handle lex.Err()
//	}
//
// The iterator is single-pass: it consumes from the same input as NextToken and does not rewind.
//
// Tokens and [L.NextToken] share the lexer's state, so the two may be interleaved: you can range over Tokens, break,
// and then continue with NextToken (or the other way around) NextToken resumes exactly where the range stopped.
//
// Note the standard range-over-func semantics: the token delivered in the iteration where you break has already been
// consumed, so the next NextToken returns the following token, not a repeat.
func (l *L) Tokens() iter.Seq[token.T] {
	return func(yield func(token.T) bool) {
		l.primeStream() // resolve the whole-buffer short-circuit before choosing the core whole-buffer fast path.

		// A native push scan loop that keeps the cursor in a local across the whole scan (no per-byte struct writes).
		// Streaming and value-capped modes keep the proven NextToken loop.
		//
		// JSON-pointer tracking (l.trackPtr) takes the NextToken fallback loop below — NextToken already runs the pointer
		// hook, so push tracking reuses the pull path.
		// This keeps the two native push fast paths (and their zero-alloc yield seam) exactly as-is when tracking is off:
		// yield is passed straight to the //go:noinline cores and never escapes.
		//
		// Tracking has already forfeited zero-alloc, so the loop's per-token call cost is an accepted trade.
		if !l.trackPtr {
			if l.in.WholeBuffer && l.maxValueBytes == 0 {
				// whole-buffer fast path: run the concrete push core through the //go:noinline yield seam (keeps Tokens inlinable +
				// yield on the stack).
				l.scanPushSemantic(yield)

				return
			}

			// streaming: the streaming push core, instead of looping over NextToken (which paid per-token call overhead PLUS
			// this closure).
			// Whole-buffer with a value cap still falls through to the NextToken loop below.
			if !l.in.WholeBuffer {
				l.scanPushStreamSemantic(yield)

				return
			}
		}

		for {
			tok := l.NextToken()
			if l.in.Err != nil {
				return
			}
			if tok.Kind() == token.EOF {
				return
			}
			if !yield(tok) {
				return
			}
		}
	}
}

// Tokens returns an iterator over the verbatim JSON tokens (light [token.T] with raw values; the preceding blanks and
// position are read via [VL.LeadingSpace] / [VL.Line] / [VL.Column], valid inside each iteration).
//
// In whole-buffer mode it takes the native push path; streaming takes the native streaming push core (§10.5g);
// value-capped whole-buffer keeps the NextToken loop.
// See [L.Tokens].
func (l *VL) Tokens() iter.Seq[token.T] {
	return func(yield func(token.T) bool) {
		l.primeStream() // resolve the whole-buffer short-circuit before choosing the core

		// Tracking takes the NextToken fallback loop (see [L.Tokens]); the fast paths are untouched when tracking is off.
		if !l.trackPtr {
			if l.in.WholeBuffer && l.maxValueBytes == 0 {
				l.scanPushVerbatim(yield)

				return
			}
			if !l.in.WholeBuffer {
				l.scanPushStreamVerbatim(yield) // §10.5g native streaming push

				return
			}
		}

		for {
			tok := l.NextToken()
			if l.in.Err != nil {
				return
			}
			if tok.Kind() == token.EOF {
				return
			}
			if !yield(tok) {
				return
			}
		}
	}
}

// Push scan entries (the "yield seam") — the concrete functions Tokens() calls to run a whole-buffer or streaming
// push scan.
//
// These are NOT the devirtualizer: the generated concrete cores (scanPush*Core in scan_gen.go) already replace the
// generics-dictionary policy calls with direct, inlined ones.
// Their job is the range-over-func contract.
//
// Tokens() returns an iter.Seq whose body must call a real, //go:noinline function so the loop-body `yield` closure
// stays stack-allocated (a zero-alloc range).
// The boundary is load-bearing for two reasons, both verifiable with -gcflags=-m:
//
//   - it keeps Tokens (and its returned Seq closure) inlinable at the call site, so
//     the compiler sees through the iterator; and
//   - the wrapper's escape summary leaks only l (the lexer pointer), NOT yield, so
//     the yield closure never reaches the heap.
//
// Inline any of these and the ~400-line core resurfaces in the Seq body: the range desugaring then heap-allocates yield
// in external callers (+2 allocs/call).
//
// The pull path (NextToken) returns a token by value — no closure, nothing to keep on the stack — so it calls the
// generated core directly, with no such wrapper.
//
// The A/B baselines that wrap the GENERIC (un-monomorphized) core are test-only and live in devirt_test.go
// (scanPush{Semantic,Verbatim}Generic).

//go:noinline
func (l *L) scanPushSemantic(yield func(token.T) bool) {
	scanPushSemanticCore(l, semanticPolicy{}, yield)
}

//go:noinline
func (l *L) scanPushVerbatim(yield func(token.T) bool) {
	scanPushVerbatimCore(l, verbatimPolicy{}, yield)
}

// scanPushStream{Semantic,Verbatim} are the io.Reader counterparts: they call the streaming push cores
// (scanPushStream*Core) that refill the buffer as they yield, so Tokens() over a reader avoids the old
// NextToken-loop-in-a-closure fallthrough.

//go:noinline
func (l *L) scanPushStreamSemantic(yield func(token.T) bool) {
	scanPushStreamSemanticCore(l, semanticPolicy{}, yield)
}

//go:noinline
func (l *L) scanPushStreamVerbatim(yield func(token.T) bool) {
	scanPushStreamVerbatimCore(l, verbatimPolicy{}, yield)
}

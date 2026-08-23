// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package lexer

//go:generate go run ./internal/lexgen

// Unified lexer core: two policy-parameterized generic cores — scanPushG (push) and scanTokenG (pull) — are the
// single source of truth for both the semantic lexer L and the verbatim lexer VL.
//
// A concrete zero-size policy per lexer (semanticPolicy / verbatimPolicy) selects the emitted token type and how each
// token is built, replacing the four hand-written loops the two lexers used to carry.
//
// Design:
//   - The per-byte hot loop is policy-free: it operates on []byte + ints. No generics cost there by construction.
//   - l.current (a token.T) stays the grammar-state memory: the loop reads its Kind/Delimiter to validate the next token.
//   - Emission is the ONLY thing routed through the policy, once per token.
//     For the semantic lexer the policy is identity (it emits the token.T already built for grammar state);
//     for the verbatim lexer, it keeps trakcs of the preceding blanks + position.
//   - Accepted cost: the per-token policy call routes through the generics dictionary (Go does not devirtualize type-param method calls), ~5% on L.

import (
	"errors"
	"io"

	codes "github.com/go-openapi/core/json/lexers/error-codes"
	scan "github.com/go-openapi/core/json/lexers/internal/scan"
	"github.com/go-openapi/core/json/lexers/token"
)

// emitPolicy converts the grammar/value token the core just built (a token.T) into the emitted token type T, attaching
// any policy-specific extra information: the preceding blanks (the whitespace run since the previous token, sliced
// zero-copy from the input) and the token's 1-based line/column.
//
// The semantic lexer ignores both; the verbatim lexer stores them into the lexer state.
type emitPolicy[T any] interface {
	emit(t token.T, blanks []byte, line, col int) T
	// none is the zero/error token (token.None / token.VNone), returned when the lexer enters an error state.
	none() T
	// eof is the end-of-input token; the verbatim policy attaches the trailing blanks, the semantic policy ignores them.
	eof(blanks []byte) T
	// tracksPosition reports whether the core must maintain line/column accounting.
	//
	// Only the verbatim lexer [VL] needs it (it exposes line/col as lexer state); the semantic lexer drops it.
	// Returning a constant lets the devirtualized cores (scan_gen.go) constant-fold and dead-code-eliminate the accounting
	// entirely in the semantic core, so its hot loop does no per-newline / per-token position bookkeeping — matching
	// jsontext's offset-only model.
	tracksPosition() bool
	// storesBlanks reports whether the core must stash the preceding-blanks slice into lexer state (l.blanks) at each
	// token boundary.
	//
	// True only for the verbatim lexer [VL], which emits a light token.T and exposes the blanks via [VL.LeadingSpace]
	// instead of baking them into the token.
	// Constant-folds away where false (semantic).
	storesBlanks() bool
}

// semanticPolicy is the policy for the semantic lexer L: the emitted token IS the grammar-state token.T, so emission is
// the identity (blanks/position are dropped — the core computes them anyway, but the slice is just a header).
type semanticPolicy struct{}

func (semanticPolicy) emit(t token.T, _ []byte, _, _ int) token.T { return t }
func (semanticPolicy) none() token.T                              { return token.None }
func (semanticPolicy) eof(_ []byte) token.T                       { return token.EOFToken }
func (semanticPolicy) tracksPosition() bool                       { return false }
func (semanticPolicy) storesBlanks() bool                         { return false }

// verbatimPolicy is the policy for the verbatim lexer [VL].
//
// "token-vs-state arbitrage":
//
//	We keep the verbatim feature as LEXER STATE: the preceding-blanks slice is stashed in l.blanks
//	(via storesBlanks, read back through [VL.LeadingSpace]) and the position stays in l.tokLine / l.tokCol
//	(the core writes it since tracksPosition is true, read back through [VL.Line] / [VL.Column]).
type verbatimPolicy struct{}

func (verbatimPolicy) emit(t token.T, _ []byte, _, _ int) token.T { return t }
func (verbatimPolicy) none() token.T                              { return token.None }
func (verbatimPolicy) eof(_ []byte) token.T                       { return token.EOFToken }
func (verbatimPolicy) tracksPosition() bool                       { return true }
func (verbatimPolicy) storesBlanks() bool                         { return true }

// errCheckG performs the shared EOF/error classification for both cores, returning the policy's eof token (with
// trailing blanks for the verbatim policy) on clean EOF, or the none token otherwise.
func errCheckG[T any, P emitPolicy[T]](l *L, p P, err error) T {
	hadToken := l.current.IsKnown()
	l.current = token.None

	if errors.Is(err, io.EOF) {
		switch {
		case l.isInContainer():
			if l.isInObject() {
				l.in.Err = codes.ErrNotInObject
			} else {
				l.in.Err = codes.ErrNotInArray
			}
		case l.isAtEOF:
			l.in.Err = io.EOF
		case !hadToken:
			l.in.Err = codes.ErrNoData
		}

		l.isAtEOF = true

		return p.eof(l.blanks)
	}

	l.in.Err = err

	return p.none()
}

// whitespace scanning + hex/\u decoding are stateless primitives shared with the token package; they live in
// internal/scan (still inline into the hot cores).

// skipBlanksRestStream batch-skips the CONTINUATION of a whitespace run in the current window for the position-tracking
// stream cores (§10.5d) — the verbatim/state analogue of the semantic core's consumeWhitespace batch-skip.
//
// The caller has already consumed and captured the run's first byte and confirmed (a cheap inline peek) that
// l.in.Consumed points at another whitespace byte, so this scans the rest of the run in ONE step, updates
// line/lineStart from a single scan, and — when trackBlanks — BULK-appends the rest into l.blanks.
//
// Splitting it this way keeps SHORT runs (e.g. mesh's 73k single-byte runs) on the cheap inline path — no call —
// while LONG runs (pretty) pay one call and one memcpy instead of a per-byte walk.
// A run reaching the window end stops at bufferized; the outer loop refills and re-enters, so a run spanning refills
// accumulates across calls.
//
// The caller does the maxValueBytes check once afterwards.
func (l *L) skipBlanksRestStream() {
	base := l.in.Offset - uint64(l.in.Consumed) // absolute offset of buffer index 0 this window
	start := l.in.Consumed
	n, lines, afterNL := scan.ConsumeWhitespaceTracked(l.in.Buffer[start:l.in.Bufferized])

	if lines > 0 {
		l.line += lines
		l.lineStart = base + uint64(start+afterNL) // just past the last newline in the run
	}

	l.in.Consumed = start + n
	l.in.Offset = base + uint64(start+n)

	if l.trackBlanks {
		l.blanks = append(l.blanks, l.in.Buffer[start:l.in.Consumed]...)
	}
}

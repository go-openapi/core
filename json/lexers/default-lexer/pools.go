package lexer

import (
	"io"
	"slices"

	"github.com/go-openapi/swag/pools"
)

type poolOfLexers struct {
	*pools.PoolRedeemable[L]
}

// lexersPool is a redeemable pool: borrowing yields a cached redeem closure (no per-borrow allocation), and under the
// poolsdebug build tag it detects double-redeem, foreign-redeem and leaks.
var lexersPool = poolOfLexers{ //nolint:gochecknoglobals // okay to have a pool as a global
	PoolRedeemable: pools.NewRedeemable[L](),
}

func (p *poolOfLexers) borrowWithBytes(data []byte, opts ...Option) (*L, func()) {
	l, redeem := p.BorrowWithRedeem()
	l.options = applyWithDefaults(opts)
	l.in.R = noopReader
	l.in.Buffer = data
	l.in.Bufferized = len(data)
	l.in.PreviousBuffer = nil
	l.keepPreviousBuffer = 0 // disabled option
	l.in.WholeBuffer = true  // the whole input is in the buffer: values may alias it
	l.in.NeedFirstFill = false
	l.reset()

	return l, redeem
}

func (p *poolOfLexers) borrowWithReader(r io.Reader, opts ...Option) (*L, func()) {
	l, redeem := p.BorrowWithRedeem()
	l.options = applyWithDefaults(opts)
	l.in.R = r
	l.in.Bufferized = 0
	l.in.WholeBuffer = false  // streaming: the buffer is refilled, values must be copied
	l.in.NeedFirstFill = true // §10.5f: the initial read + whole-buffer short-circuit is pending
	l.reset()

	if cap(l.in.Buffer) < l.bufferSize {
		// reallocates an internal buffer only if options have changed
		l.in.Buffer = slices.Grow(l.in.Buffer, l.bufferSize-cap(l.in.Buffer))[:l.bufferSize]
	}

	if l.keepPreviousBuffer > 0 && cap(l.in.PreviousBuffer) < l.keepPreviousBuffer {
		l.in.PreviousBuffer = slices.Grow(
			l.in.PreviousBuffer,
			l.keepPreviousBuffer-cap(l.in.PreviousBuffer),
		)
	}

	return l, redeem
}

// BorrowLexerWithReader borrows a L(exer) from a global pool, together with the closure that redeems it back to the
// pool.
//
// This is equivalent to calling [New], but may recycle a previously allocated lexer if available from the pool.
// The internal buffer of the lexer is also reused, provided the [WithBufferSize] option has not changed the pooled
// size.
//
// The redeem closure must be called exactly once when the lexer is no longer needed (typically via defer); after
// calling it, drop the reference to the lexer.
// Calling it more than once panics.
// To maximize the amortizing effect of the pool, make sure every borrowed lexer is eventually redeemed.
func BorrowLexerWithReader(r io.Reader, opts ...Option) (*L, func()) {
	return lexersPool.borrowWithReader(r, opts...)
}

// BorrowLexerWithBytes borrows a L(exer) from a global pool, together with the closure that redeems it back to the
// pool.
//
// This is equivalent to calling [NewWithBytes], but may recycle a previously allocated lexer if available from the
// pool.
//
// The redeem closure must be called exactly once when the lexer is no longer needed (typically via defer); after
// calling it, drop the reference to the lexer.
// Calling it more than once panics.
func BorrowLexerWithBytes(data []byte, opts ...Option) (*L, func()) {
	return lexersPool.borrowWithBytes(data, opts...)
}

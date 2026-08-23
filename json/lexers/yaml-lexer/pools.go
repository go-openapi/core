// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package lexer

import (
	"io"

	"github.com/go-openapi/swag/pools"
)

type poolOfLexers struct {
	*pools.PoolRedeemable[YL]
}

// lexersPool is a redeemable pool: borrowing yields a cached redeem closure (no per-borrow allocation), and under
// the poolsdebug build tag it detects double-redeem, foreign-redeem and leaks.
var lexersPool = poolOfLexers{ //nolint:gochecknoglobals // okay to have a pool as a global
	PoolRedeemable: pools.NewRedeemable[YL](),
}

func (p *poolOfLexers) borrowWithBytes(data []byte, opts ...Option) (*YL, func()) {
	l, redeem := p.BorrowWithRedeem()
	l.options = applyWithDefaults(opts)
	l.data = data
	l.reset()

	return l, redeem
}

func (p *poolOfLexers) borrowWithReader(r io.Reader, opts ...Option) (*YL, func()) {
	l, redeem := p.BorrowWithRedeem()
	l.options = applyWithDefaults(opts)
	l.reset()
	l.setReader(r)

	return l, redeem
}

// BorrowLexerWithReader borrows a YL(exer) from a global pool, together with the closure that redeems it back to
// the pool.
//
// This is equivalent to calling [New], but may recycle a previously allocated lexer if available from the pool.
//
// The redeem closure must be called exactly once when the lexer is no longer needed (typically via defer); after
// calling it, drop the reference to the lexer.
// Calling it more than once panics.
// To maximize the amortizing effect of the pool, make sure every borrowed lexer is eventually redeemed.
//
// Note that a reader is drained with [io.ReadAll], so each borrow allocates the buffer holding the source.
// [BorrowLexerWithBytes] avoids that.
func BorrowLexerWithReader(r io.Reader, opts ...Option) (*YL, func()) {
	return lexersPool.borrowWithReader(r, opts...)
}

// BorrowLexerWithBytes borrows a YL(exer) from a global pool, together with the closure that redeems it back to
// the pool.
//
// This is equivalent to calling [NewWithBytes], but may recycle a previously allocated lexer if available from the
// pool.
//
// The redeem closure must be called exactly once when the lexer is no longer needed (typically via defer); after
// calling it, drop the reference to the lexer.
// Calling it more than once panics.
//
// What the pool amortizes is the YL and the capacity of its token slice, NOT the parse: lexing hands the source to
// goccy/go-yaml, which builds a fresh AST every time. Borrowing therefore lowers the allocation count of a
// repeated lex, it does not bring it to zero the way [github.com/go-openapi/core/json/lexers/default-lexer]'s pool
// does.
func BorrowLexerWithBytes(data []byte, opts ...Option) (*YL, func()) {
	return lexersPool.borrowWithBytes(data, opts...)
}

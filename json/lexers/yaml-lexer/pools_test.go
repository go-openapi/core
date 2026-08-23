// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package lexer_test

import (
	"strings"
	"testing"

	"github.com/go-openapi/swag/pools"
	"github.com/go-openapi/testify/v2/require"

	yamllexer "github.com/go-openapi/core/json/lexers/yaml-lexer"
)

func TestPoolReuse(t *testing.T) {
	const (
		docA = "a:\n  - 1\n  - -2\n  - 3.5e2\nb: true\n"
		docB = "- null\n- x\n- 0.5\n"
	)

	drain := func(l *yamllexer.YL) ([]string, error) {
		var out []string
		for tok := range l.Tokens() {
			out = append(out, repr(tok))
		}

		return out, l.Err()
	}

	wantA, errA := drain(yamllexer.NewWithBytes([]byte(docA)))
	require.NoError(t, errA)
	wantB, errB := drain(yamllexer.NewWithBytes([]byte(docB)))
	require.NoError(t, errB)
	require.NotEmpty(t, wantA)

	t.Run("borrow/redeem reproduces the stream", func(t *testing.T) {
		// alternating documents of different shapes and lengths: a recycled lexer must carry no state
		// from the previous borrower, and must not truncate to the shorter of the two token streams.
		for range 3 {
			l, redeem := yamllexer.BorrowLexerWithBytes([]byte(docA))
			got, err := drain(l)
			require.NoError(t, err)
			require.Equal(t, wantA, got)
			redeem()

			l, redeem = yamllexer.BorrowLexerWithBytes([]byte(docB))
			got, err = drain(l)
			require.NoError(t, err)
			require.Equal(t, wantB, got)
			redeem()
		}
	})

	t.Run("borrow with a reader", func(t *testing.T) {
		l, redeem := yamllexer.BorrowLexerWithReader(strings.NewReader(docA))
		defer redeem()

		got, err := drain(l)
		require.NoError(t, err)
		require.Equal(t, wantA, got)
	})

	t.Run("options are honored, and do not survive the redeem", func(t *testing.T) {
		l, redeem := yamllexer.BorrowLexerWithBytes([]byte(docA), yamllexer.WithMaxTokens(3))
		_, err := drain(l)
		require.ErrorIs(t, err, yamllexer.ErrMaxTokens)
		redeem()

		// the next borrower passes no option: it must get the defaults back, not WithMaxTokens(3)
		l, redeem = yamllexer.BorrowLexerWithBytes([]byte(docA))
		defer redeem()

		got, err := drain(l)
		require.NoError(t, err)
		require.Equal(t, wantA, got)
	})

	// In the poolsdebug build, assert the pool tracker recorded no leaked borrows from this test (a no-op in
	// release builds).
	t.Cleanup(func() { pools.AssertNoLeaks(t) })
}

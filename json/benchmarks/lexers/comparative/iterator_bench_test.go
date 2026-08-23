// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package comparative

import (
	"fmt"
	"io"
	"testing"

	deflex "github.com/go-openapi/core/json/lexers/default-lexer"
	"github.com/go-openapi/core/json/testdata/workloads"
)

// BenchmarkIterator compares the manual NextToken loop against the Tokens()
// range iterator (the wrapper implementation), on the default-lexer.
func BenchmarkIterator(b *testing.B) {
	// sink prevents the compiler from eliminating the token-walk loops.
	var sink int

	suite, err := workloads.Suite()
	if err != nil {
		b.Fatalf("loading workloads: %v", err)
	}

	for _, w := range suite {
		b.Run(w.Name, func(b *testing.B) {
			b.Run("manual", func(b *testing.B) {
				b.SetBytes(int64(len(w.Data)))
				b.ReportAllocs()
				b.ResetTimer()

				n := 0
				for range b.N {
					lex := deflex.NewWithBytes(w.Data)
					for {
						tok := lex.NextToken()
						if !lex.Ok() || tok.IsEOF() {
							break
						}
						n += int(tok.Kind())
					}
				}
				sink = n
			})

			b.Run("iterator", func(b *testing.B) {
				b.SetBytes(int64(len(w.Data)))
				b.ReportAllocs()
				b.ResetTimer()

				n := 0
				for range b.N {
					lex := deflex.NewWithBytes(w.Data)
					for tok := range lex.Tokens() {
						n += int(tok.Kind())
					}
				}
				sink = n
			})
		})
	}

	fmt.Fprint(io.Discard, sink)
}

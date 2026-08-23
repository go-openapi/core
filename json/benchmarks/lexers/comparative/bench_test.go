package comparative

import (
	"fmt"
	"io"
	"testing"

	"github.com/go-openapi/core/json/benchmarks/lexers/comparative/easyjson"
	"github.com/go-openapi/core/json/benchmarks/lexers/comparative/jsontext"
	"github.com/go-openapi/core/json/benchmarks/lexers/comparative/stdlib"
	jlex "github.com/go-openapi/core/json/lexers"
	deflex "github.com/go-openapi/core/json/lexers/default-lexer"
	"github.com/go-openapi/core/json/testdata/workloads"
	"github.com/go-openapi/swag/pools"
)

// TestWorkloadsLex guards the benchmarks: every workload must lex cleanly to EOF
// on every implementation, otherwise a benchmark could silently time a partial or errored scan.
func TestWorkloadsLex(t *testing.T) {
	var sink int
	suite, err := workloads.Suite()
	if err != nil {
		t.Fatalf("loading workloads: %v", err)
	}

	// every borrowed lexer is released below; under -tags poolsdebug this asserts
	// no borrow leaked (a no-op in release builds).
	t.Cleanup(func() { pools.AssertNoLeaks(t) })

	for _, w := range suite {
		for _, f := range factories() {
			lex, release := f.make(w.Data)
			n, err := drain(lex)
			release()
			if err != nil {
				t.Errorf("%s / %s: unexpected error after %d tokens: %v", w.Name, f.name, n, err)
			}
			if n == 0 {
				t.Errorf("%s / %s: no tokens produced", w.Name, f.name)
			}
		}

		// verbatim lexer has its own token type, drive it separately
		vl := deflex.NewVerbatimWithBytes(w.Data)
		n := 0
		for {
			tok := vl.NextToken()
			if !vl.Ok() || tok.IsEOF() {
				break
			}
			n++
		}
		if err := vl.Err(); err != nil {
			t.Errorf(
				"%s / default-lexer/verbatim: unexpected error after %d tokens: %v",
				w.Name,
				n,
				err,
			)
		}

		// easyjson generic walk (recursive)
		if err := easyjson.Walk(w.Data); err != nil {
			t.Errorf("%s / easyjson: unexpected error: %v", w.Name, err)
		}

		// jsontext (encoding/json/v2) tokenizer
		if err := jsontext.Walk(w.Data); err != nil {
			t.Errorf("%s / jsontext: unexpected error: %v", w.Name, err)
		}
	}

	fmt.Fprint(io.Discard, sink)
}

func BenchmarkLexers(b *testing.B) {
	var sink int
	suite, err := workloads.Suite()
	if err != nil {
		b.Fatalf("loading workloads: %v", err)
	}

	for _, w := range suite {
		b.Run(w.Name, func(b *testing.B) {
			for _, f := range factories() {
				b.Run(f.name, func(b *testing.B) {
					b.SetBytes(int64(len(w.Data)))
					b.ReportAllocs()
					b.ResetTimer()

					for range b.N {
						lex, release := f.make(w.Data)
						_, _ = drain(lex)
						release()
					}
				})
			}

			// default-lexer native push Tokens() iterator (whole-buffer fast path)
			b.Run("default-lexer/tokens", func(b *testing.B) {
				b.SetBytes(int64(len(w.Data)))
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					lex := deflex.NewWithBytes(w.Data)
					for tok := range lex.Tokens() {
						sink += int(tok.Kind())
					}
				}
			})

			// default-lexer reused across iterations via ResetWithBytes: the
			// lexer is allocated once outside the loop, so steady-state scanning
			// should report 0 allocs/op (the construction bias is amortized away).
			b.Run("default-lexer/reset", func(b *testing.B) {
				lex := deflex.NewWithBytes(nil)
				b.SetBytes(int64(len(w.Data)))
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					lex.ResetWithBytes(w.Data)
					_, _ = drain(lex)
				}
			})

			// verbatim lexer (VL), constructed per iteration — apples-to-apples
			// with default-lexer/bytes (L, also constructed per iteration). The
			// delta is the cost of full fidelity: blanks + always-on positions +
			// the verbatim look-ahead.
			b.Run("default-lexer/verbatim", func(b *testing.B) {
				b.SetBytes(int64(len(w.Data)))
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					vl := deflex.NewVerbatimWithBytes(w.Data)
					for {
						tok := vl.NextToken()
						if !vl.Ok() || tok.IsEOF() {
							break
						}
					}
				}
			})

			// verbatim lexer reused via ResetWithBytes (allocated once outside the
			// loop): the steady-state VL scanning cost, comparable to
			// default-lexer/reset (L reused the same way). This is the cleanest
			// L-vs-VL lexing-speed comparison, free of per-iteration construction.
			b.Run("default-lexer/verbatim-reset", func(b *testing.B) {
				vl := deflex.NewVerbatimWithBytes(nil)
				b.SetBytes(int64(len(w.Data)))
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					vl.ResetWithBytes(w.Data)
					for {
						tok := vl.NextToken()
						if !vl.Ok() || tok.IsEOF() {
							break
						}
					}
				}
			})

			// easyjson []byte-only pull lexer, generic recursive walk; numbers
			// taken raw (no numeric conversion) for an apples-to-apples comparison
			b.Run("easyjson/bytes", func(b *testing.B) {
				b.SetBytes(int64(len(w.Data)))
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					_ = easyjson.Walk(w.Data)
				}
			})

			// easyjson with number conversion (Float64): this is where jlexer
			// actually validates number grammar, so it is the fair comparison
			// against the always-validating default-lexer (Float64 is lossy,
			// which the default-lexer avoids).
			b.Run("easyjson-f64/bytes", func(b *testing.B) {
				b.SetBytes(int64(len(w.Data)))
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					_ = easyjson.WalkConvertNumbers(w.Data)
				}
			})

			// jsontext (encoding/json/v2): fully-validating streaming tokenizer,
			// numbers validated but not converted -> the closest peer to the
			// default-lexer. Fed a *bytes.Buffer for the zero-copy bytes path.
			b.Run("jsontext/bytes", func(b *testing.B) {
				b.SetBytes(int64(len(w.Data)))
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					_ = jsontext.Walk(w.Data)
				}
			})
		})
	}

	fmt.Fprint(io.Discard, sink)
}

// factory builds a fresh lexers.Lexer over data, together with a release
// callback (e.g. to redeem a pooled lexer); release is a no-op when not needed.
type factory struct {
	name string
	make func(data []byte) (jlex.Lexer, func())
}

func factories() []factory {
	noRelease := func() {}

	return []factory{
		{
			name: "default-lexer/bytes",
			make: func(d []byte) (jlex.Lexer, func()) {
				return deflex.NewWithBytes(d), noRelease
			},
		},
		{
			name: "default-lexer/pooled",
			make: func(d []byte) (jlex.Lexer, func()) {
				l, redeem := deflex.BorrowLexerWithBytes(d)

				return l, redeem
			},
		},
		{
			name: "stdlib/bytes",
			make: func(d []byte) (jlex.Lexer, func()) {
				return stdlib.NewWithBytes(d), noRelease
			},
		},
	}
}

// drain consumes every token until EOF or error, returning the token count.
func drain(lex jlex.Lexer) (int, error) {
	n := 0
	for {
		tok := lex.NextToken()
		if !lex.Ok() {
			return n, lex.Err()
		}
		if tok.IsEOF() {
			return n, nil
		}
		n++
	}
}

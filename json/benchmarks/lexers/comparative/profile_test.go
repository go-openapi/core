package comparative

import (
	"bytes"
	"testing"

	deflex "github.com/go-openapi/core/json/lexers/default-lexer"
	"github.com/go-openapi/core/json/testdata/workloads"
)

// profileFixture picks a representative real-world document for profiling.
func profileFixture(tb testing.TB) []byte {
	tb.Helper()
	corpus, err := workloads.Corpus()
	if err != nil {
		tb.Fatalf("loading corpus: %v", err)
	}
	for _, w := range corpus {
		if w.Name == "citm_catalog" {
			return w.Data
		}
	}
	tb.Fatal("citm_catalog fixture not found")

	return nil
}

func drainL(lex *deflex.L) int {
	n := 0
	for {
		tok := lex.NextToken()
		if !lex.Ok() || tok.IsEOF() {
			break
		}
		n += int(tok.Kind())
	}

	return n
}

// BenchmarkProfile isolates the allocation sources by varying only how the lexer
// is obtained, while the scanning work is identical:
//
//   - new-per-op : a fresh lexer is constructed every iteration (what the
//     headline BenchmarkLexers does) — this is what inflates allocs/op.
//   - pooled     : the lexer (and its buffers) are recycled via the pool — this
//     reflects the true steady-state cost of scanning.
//
// Run with profiles, e.g.:
//
//	go test -run '^$' -bench 'BenchmarkProfile/bytes/new-per-op' \
//	    -benchmem -cpuprofile /tmp/cpu_bytes.out -memprofile /tmp/mem_bytes.out ./lexers/
func BenchmarkProfile(b *testing.B) {
	data := profileFixture(b)

	b.Run("bytes", func(b *testing.B) {
		b.Run("new-per-op", func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				sink = drainL(deflex.NewWithBytes(data))
			}
		})

		b.Run("pooled", func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				lex, redeem := deflex.BorrowLexerWithBytes(data)
				sink = drainL(lex)
				redeem()
			}
		})
	})

	b.Run("reader", func(b *testing.B) {
		b.Run("new-per-op", func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				sink = drainL(deflex.New(bytes.NewReader(data)))
			}
		})

		b.Run("pooled", func(b *testing.B) {
			// reuse a single bytes.Reader to avoid measuring its allocation
			rdr := bytes.NewReader(data)
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				rdr.Reset(data)
				lex, redeem := deflex.BorrowLexerWithReader(rdr)
				sink = drainL(lex)
				redeem()
			}
		})
	})
}

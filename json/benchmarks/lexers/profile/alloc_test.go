package profile

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime/trace"
	"testing"

	lexer "github.com/go-openapi/core/json/lexers/default-lexer"
	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/testify/v2/require"
	"github.com/pkg/profile"
)

func TestProfile(t *testing.T) {
	fixture, err := os.ReadFile(example())
	require.NoError(t, err)
	rdr := bytes.NewReader(fixture)

	p := profile.Start(profile.MemProfile, profile.MemProfileRate(1), profile.ProfilePath("."))
	for range 10000 {
		lex, redeem := lexer.BorrowLexerWithReader(rdr, lexer.WithMaxValueBytes(1000))
		measureIt := measureIt(lex)
		err = measureIt(t)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
			t.FailNow()
		}
		redeem()
		_, _ = rdr.Seek(0, io.SeekStart)
	}
	p.Stop()
}

func TestHeap(t *testing.T) {
	fixture, err := os.ReadFile(example())
	require.NoError(t, err)
	dump, err := os.Create("trace.out")
	require.NoError(t, err)
	t.Cleanup(func() { _ = dump.Close() })
	rdr := bytes.NewReader(fixture)
	_ = trace.Start(dump)
	defer trace.Stop()

	for range 10000 {
		lex, redeem := lexer.BorrowLexerWithReader(rdr, lexer.WithoutAVX2(true))
		measureIt := measureIt(lex)
		err = measureIt(t)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
			t.FailNow()
		}
		redeem()
		_, _ = rdr.Seek(0, io.SeekStart)
	}

	// debug.WriteHeapDump(dump.Fd())
}

func BenchmarkLexer(b *testing.B) {
	fixture, err := os.ReadFile(example())
	require.NoError(b, err)
	rdr := bytes.NewReader(fixture)

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		lex, redeem := lexer.BorrowLexerWithReader(rdr)
		measureIt := measureIt(lex)
		err = measureIt(b)
		if err != nil {
			b.Errorf("unexpected error: %v", err)
			b.FailNow()
		}
		redeem()
		_, _ = rdr.Seek(0, io.SeekStart)
	}
}

func measureIt(lex *lexer.L) func(testing.TB) error {
	return func(_ testing.TB) error {
		var (
			i     int
			tok   token.T
			value []byte
		)

		// shouldn't report any allocs, after amortization
		for ; !tok.IsEOF(); i++ {
			tok = lex.NextToken()
			if !lex.Ok() {
				return lex.Err()
			}
			// keep the token unoptimized
			value = tok.Value()
		}

		if len(value) > 0 {
			return errors.New("unexpected non-empty EOF value")
		}

		return nil
	}
}

func example() string {
	return filepath.Join("..", "..", "..", "lexers", "default-lexer", "testdata", "example.json")
}

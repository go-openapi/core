package lane

// SCRATCH (utf-8 validation prototype): measures the cost of the three prototype
// validation modes across the corpus. Delete when the design is settled.

import (
	"fmt"
	"io"
	"testing"
	"unicode/utf8"

	lexer "github.com/go-openapi/core/json/lexers/default-lexer"
	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/core/json/testdata/workloads"
)

func BenchmarkUTF8Validate(b *testing.B) {
	suite, err := workloads.Corpus()
	if err != nil {
		b.Fatal(err)
	}

	modes := []struct {
		name   string
		policy lexer.UTF8Policy
	}{
		{"passthrough", lexer.UTF8Passthrough}, // the pre-validation baseline
		{"strict", lexer.UTF8Strict},           // the new default
		{"replace", lexer.UTF8Replace},
	}

	for _, wl := range suite {
		data := wl.Data
		b.Run(wl.Name, func(b *testing.B) {
			for _, m := range modes {
				b.Run("L/"+m.name, func(b *testing.B) {
					var sink int
					lx := lexer.NewWithBytes(data, lexer.WithUTF8Policy(m.policy))
					b.SetBytes(int64(len(data)))
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						lx.ResetWithBytes(data)
						for t := range lx.Tokens() {
							sink += int(t.Kind())
						}
					}
					fmt.Fprint(io.Discard, sink)
				})
				b.Run("VL/"+m.name, func(b *testing.B) {
					var sink int
					lx := lexer.NewVerbatimWithBytes(data, lexer.WithUTF8Policy(m.policy))
					b.SetBytes(int64(len(data)))
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						lx.ResetWithBytes(data)
						for t := range lx.Tokens() {
							sink += int(t.Kind())
						}
					}
					fmt.Fprint(io.Discard, sink)
				})
			}
		})
	}
}

// TestCorpusStringProfile reports, per workload, how much of the payload is string
// bytes and what share of those strings carry a non-ASCII byte -- i.e. how many
// values a fused detector would hand to the validator.
func TestCorpusStringProfile(t *testing.T) {
	suite, err := workloads.Corpus()
	if err != nil {
		t.Fatal(err)
	}

	for _, wl := range suite {
		var (
			strings, nonASCII, strBytes, nonASCIIBytes int
			longStrings, longNonASCII                  int
		)
		lx := lexer.NewWithBytes(wl.Data)
		for tok := range lx.Tokens() {
			if tok.Kind() != token.String && tok.Kind() != token.Key {
				continue
			}
			v := tok.Value()
			strings++
			strBytes += len(v)
			if len(v) >= 16 {
				longStrings++
			}
			if !isASCII(v) {
				nonASCII++
				nonASCIIBytes += len(v)
				if len(v) >= 16 {
					longNonASCII++
				}
			}
			if !utf8.Valid(v) {
				t.Errorf("%s: corpus carries invalid utf8", wl.Name)
			}
		}
		if !lx.Ok() {
			t.Fatalf("%s: %v", wl.Name, lx.Err())
		}
		t.Logf(
			"%-20s bytes=%d strings=%d (%d long) strBytes=%d (%.1f%% of payload) nonASCII=%d (%.2f%% of strings, %d long) nonASCIIBytes=%d",
			wl.Name,
			len(wl.Data),
			strings,
			longStrings,
			strBytes,
			100*float64(strBytes)/float64(len(wl.Data)),
			nonASCII,
			100*float64(nonASCII)/float64(max(strings, 1)),
			longNonASCII,
			nonASCIIBytes,
		)
	}
}

func isASCII(b []byte) bool {
	for _, c := range b {
		if c >= 0x80 {
			return false
		}
	}

	return true
}

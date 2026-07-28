package lane

import (
	"fmt"
	"io"
	"testing"

	deflex "github.com/go-openapi/core/json/lexers/default-lexer"
	"github.com/go-openapi/core/json/testdata/workloads"
)

// impl is one tokenizer under test: given the corpus bytes, drain it end-to-end.
type impl struct {
	name string
	run  func(data []byte)
}

func implementations() []impl {
	return []impl{
		// our semantic lexer L: fast-path scan, decodes strings, elides separators.
		{name: "L-semantic", run: func(data []byte) {
			var sink int
			lx := deflex.NewWithBytes(data)
			for tok := range lx.Tokens() {
				sink += int(tok.Kind())
			}
			fmt.Fprint(io.Discard, sink)
		}},
		// our verbatim lexer VL: keeps strings raw, tracks blanks + line/column.
		{name: "VL-verbatim", run: func(data []byte) {
			var sink int
			lx := deflex.NewVerbatimWithBytes(data)
			for tok := range lx.Tokens() {
				sink += int(tok.Kind())
			}
			fmt.Fprint(io.Discard, sink)
		}},
		// mailru/easyjson jlexer (our design's inspiration): recursive walk.
		{name: "easyjson", run: func(data []byte) { _ = easyjsonWalk(data) }},
		// go-json-experiment jsontext (encoding/json/v2): streaming tokenizer.
		{name: "jsontext", run: func(data []byte) { _ = jsontextWalk(data) }},
	}
}

// BenchmarkLexers reports input throughput (MB/s) for each tokenizer on each
// corpus workload. The chart in benchviz/ is rendered from this benchmark's
// output (see benchviz/README.md).
func BenchmarkLexers(b *testing.B) {
	suite, err := workloads.Corpus()
	if err != nil {
		b.Fatal(err)
	}

	for _, wl := range suite {
		b.Run(wl.Name, func(b *testing.B) {
			for _, im := range implementations() {
				b.Run(im.name, func(b *testing.B) {
					b.SetBytes(int64(len(wl.Data)))
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						im.run(wl.Data)
					}
				})
			}
		})
	}
}

// TestWalkersAgree is a sanity check that every tokenizer accepts each corpus
// document without error (they all lex the same valid JSON).
func TestWalkersAgree(t *testing.T) {
	suite, err := workloads.Corpus()
	if err != nil {
		t.Fatal(err)
	}

	for _, wl := range suite {
		t.Run(wl.Name, func(t *testing.T) {
			if err := easyjsonWalk(wl.Data); err != nil {
				t.Errorf("easyjson: %v", err)
			}
			if err := jsontextWalk(wl.Data); err != nil {
				t.Errorf("jsontext: %v", err)
			}
			lx := deflex.NewWithBytes(wl.Data)
			for range lx.Tokens() {
				continue
			}
			if err := lx.Err(); err != nil {
				t.Errorf("L: %v", err)
			}
			vl := deflex.NewVerbatimWithBytes(wl.Data)
			for range vl.Tokens() {
				continue
			}
			if err := vl.Err(); err != nil {
				t.Errorf("VL: %v", err)
			}
		})
	}
}

package writers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"testing"
	"unsafe"

	"github.com/mailru/easyjson/jwriter"

	deflex "github.com/go-openapi/core/json/lexers/default-lexer"
	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/core/json/testdata/workloads"
	"github.com/go-openapi/core/json/writers"
	writer "github.com/go-openapi/core/json/writers/default-writer"
)

// impl is one writer implementation under test.
type impl struct {
	name string
	run  func(out io.Writer, toks []token.T)
}

// bigBufferSize is large enough to hold each corpus document in full, so the buffered
// writer flushes exactly once at the end — the same "build it all in memory, then dump"
// strategy easyjson uses. It is the explicit trade-memory-for-speed configuration.
const bigBufferSize = 2 << 20 // 2 MiB

func implementations() []impl {
	return []impl{
		{name: "our-unbuffered", run: runOurUnbuffered},
		{name: "our-buffered", run: runOurBuffered},
		{name: "our-buffered-2MB", run: runOurBufferedBig},
		{name: "our-yaml", run: runOurYAML},
		{name: "easyjson", run: runEasyjson},
	}
}

func BenchmarkWriters(b *testing.B) {
	suite, err := workloads.Corpus()
	if err != nil {
		b.Fatal(err)
	}

	for _, wl := range suite {
		toks := lexTokens(wl.Data)

		b.Run(wl.Name, func(b *testing.B) {
			for _, im := range implementations() {
				b.Run(im.name, func(b *testing.B) {
					// size the throughput denominator on the bytes this writer emits
					var counter countWriter
					im.run(&counter, toks)
					b.SetBytes(int64(counter.n))

					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						im.run(io.Discard, toks)
					}
				})
			}
		})
	}
}

// TestReplayRoundTrip validates that replaying the lexed token stream through the
// JSON writers reproduces a document semantically equal to the original corpus.
func TestReplayRoundTrip(t *testing.T) {
	suite, err := workloads.Corpus()
	if err != nil {
		t.Fatal(err)
	}

	for _, wl := range suite {
		t.Run(wl.Name, func(t *testing.T) {
			toks := lexTokens(wl.Data)

			var want any
			if err := json.Unmarshal(wl.Data, &want); err != nil {
				t.Fatalf("unmarshal original: %v", err)
			}

			check := func(name string, run func(io.Writer, []token.T)) {
				var buf bytes.Buffer
				run(&buf, toks)

				if !json.Valid(buf.Bytes()) {
					t.Errorf("%s: produced invalid JSON", name)

					return
				}

				var got any
				if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
					t.Errorf("%s: unmarshal output: %v", name, err)

					return
				}

				if !reflect.DeepEqual(want, got) {
					t.Errorf("%s: round-trip mismatch with original", name)
				}
			}

			check("our-unbuffered", runOurUnbuffered)
			check("our-buffered", runOurBuffered)
			check("easyjson", runEasyjson)
			// our-yaml emits YAML, not JSON: benchmarked but not round-trip-checked here.
		})
	}
}

// --- runners -------------------------------------------------------------

func runOurUnbuffered(out io.Writer, toks []token.T) {
	w := writer.BorrowUnbuffered(out)
	replayOur(w, toks)
	writer.RedeemUnbuffered(w)
}

func runOurBuffered(out io.Writer, toks []token.T) {
	w := writer.BorrowBuffered(out)
	replayOur(w, toks)
	_ = w.Flush()
	writer.RedeemBuffered(w)
}

// runOurBufferedBig mirrors easyjson's strategy: a buffer large enough to hold the whole
// document, so the only write to the underlying io.Writer is a single final Flush.
func runOurBufferedBig(out io.Writer, toks []token.T) {
	w := writer.BorrowBuffered(out, writer.WithBufferSize(bigBufferSize))
	replayOur(w, toks)
	_ = w.Flush()
	writer.RedeemBuffered(w)
}

func runOurYAML(out io.Writer, toks []token.T) {
	w := writer.BorrowYAML(out)
	replayOur(w, toks)
	_ = w.Flush()
	writer.RedeemYAML(w)
}

func runEasyjson(out io.Writer, toks []token.T) {
	w := &jwriter.Writer{NoEscapeHTML: true} // match our writers: no HTML escaping
	replayEasyjson(w, toks)
	_, _ = w.DumpTo(out)
}

// replayOur drives any of our writers through the shared TokenWriter interface.
func replayOur(w writers.TokenWriter, toks []token.T) {
	for i := range toks {
		w.Token(toks[i])
	}
}

// replayEasyjson maps each token onto the equivalent easyjson jwriter call.
func replayEasyjson(w *jwriter.Writer, toks []token.T) {
	for i := range toks {
		tok := toks[i]
		switch tok.Kind() {
		case token.Delimiter:
			switch tok.Delimiter() {
			case token.OpeningBracket:
				w.RawByte('{')
			case token.ClosingBracket:
				w.RawByte('}')
			case token.OpeningSquareBracket:
				w.RawByte('[')
			case token.ClosingSquareBracket:
				w.RawByte(']')
			case token.Comma:
				w.RawByte(',')
			case token.Colon:
				w.RawByte(':')
			default:
				// NotADelimiter: ignore
			}
		case token.String, token.Key:
			w.String(bytesToString(tok.Value()))
		case token.Number:
			w.Buffer.AppendBytes(tok.Value()) // raw number bytes, no reformatting
		case token.Boolean:
			w.Bool(tok.Bool())
		case token.Null:
			w.RawString("null")
		default:
			// EOF/None/Unknown: nothing to emit
		}
	}
}

// --- helpers -------------------------------------------------------------

// lexTokens lexes data into a stable, self-owned slice of tokens (separators
// included). Cloning detaches token values from any lexer-internal buffer so the
// slice stays valid for the lifetime of the benchmark.
func lexTokens(data []byte) []token.T {
	lx := deflex.NewWithBytes(data, deflex.WithElideSeparator(false))

	var toks []token.T
	for tok := range lx.Tokens() {
		toks = append(toks, cloneToken(tok))
	}

	if err := lx.Err(); err != nil {
		panic(fmt.Sprintf("lexing workload: %v", err))
	}

	return toks
}

func cloneToken(t token.T) token.T {
	switch t.Kind() {
	case token.Delimiter:
		return token.MakeDelimiter(t.Delimiter())
	case token.Boolean:
		return token.MakeBoolean(t.Bool())
	case token.Null:
		return token.NullToken
	case token.String, token.Key, token.Number:
		v := append([]byte(nil), t.Value()...)

		return token.MakeWithValue(t.Kind(), v)
	default:
		return t
	}
}

func bytesToString(b []byte) string {
	if len(b) == 0 {
		return ""
	}

	return unsafe.String(unsafe.SliceData(b), len(b))
}

// countWriter is an io.Writer that only counts the bytes written, used to size
// the per-writer throughput denominator.
type countWriter struct{ n int }

func (c *countWriter) Write(p []byte) (int, error) {
	c.n += len(p)

	return len(p), nil
}

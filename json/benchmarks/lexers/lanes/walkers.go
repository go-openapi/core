// Package lane prepares a benchmark on a single workload, but many execution paths (lanes)
// for our lexer.
//
// It compares the tokenization throughput of the in-repo
// default-lexer — both the semantic lexer (L) and the verbatim lexer (VL) —
// against two external JSON tokenizers: mailru/easyjson's jlexer (the lexer our
// design is inspired from) and go-json-experiment's jsontext (encoding/json/v2),
// the fully-validating streaming tokenizer that is our closest peer.
//
// Each real-world corpus document is tokenized end-to-end (drained to EOF);
// b.SetBytes is the input size, so the reported MB/s is *input* throughput.
package lane

import (
	"bytes"
	"fmt"
	"io"

	"github.com/go-json-experiment/json/jsontext"
	"github.com/mailru/easyjson/jlexer"
)

// easyjsonWalk fully tokenizes data with jlexer as a generic recursive walk,
// descending into every container. Numbers are taken via Raw() (raw sub-slice, no
// numeric conversion) and strings via String(), matching the default-lexer, which
// emits raw token bytes and never converts numbers. Returns the lexer error.
func easyjsonWalk(data []byte) error {
	l := &jlexer.Lexer{Data: data}
	easyjsonValue(l)

	return l.Error()
}

func easyjsonValue(l *jlexer.Lexer) {
	// sink prevents the compiler from eliminating a walk.
	var sink int

	switch l.CurrentToken() {
	case jlexer.TokenString:
		sink += len(l.String())
	case jlexer.TokenNumber:
		sink += len(l.Raw())
	case jlexer.TokenBool:
		if l.Bool() {
			sink++
		}
	case jlexer.TokenNull:
		l.Null()
	case jlexer.TokenDelim:
		switch {
		case l.IsDelim('{'):
			l.Delim('{')
			for l.Ok() && !l.IsDelim('}') {
				sink += len(l.String()) // key
				l.WantColon()
				easyjsonValue(l)
				l.WantComma()
			}
			l.Delim('}')
		case l.IsDelim('['):
			l.Delim('[')
			for l.Ok() && !l.IsDelim(']') {
				easyjsonValue(l)
				l.WantComma()
			}
			l.Delim(']')
		}
	default: // TokenUndef
	}
	fmt.Fprint(io.Discard, sink)
}

// jsontextWalk fully tokenizes data with the go-json-experiment jsontext decoder,
// draining every token to EOF. Numbers are validated but never converted (no
// native value is built) — the closest peer to the default-lexer.
func jsontextWalk(data []byte) error {
	var sink int
	dec := jsontext.NewDecoder(bytes.NewBuffer(data))
	for {
		tok, err := dec.ReadToken()
		if err != nil {
			if err == io.EOF {
				return nil
			}

			fmt.Fprint(io.Discard, sink)

			return err
		}
		sink += int(tok.Kind())
	}
}

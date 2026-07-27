// Package jsontext drives the go-json-experiment (encoding/json/v2) jsontext
// tokenizer over a whole JSON document, so it can be benchmarked as a comparison
// point for the default-lexer.
//
// jsontext.Decoder.ReadToken is a genuine, fully RFC 8259-validating streaming
// tokenizer: it validates the JSON grammar — including number grammar — while
// scanning, and does NOT convert numbers to native Go types. That makes it the
// closest peer to the default-lexer of all the comparison points (closer than
// easyjson, whose Raw() path skips number validation and whose Float64() path
// over-validates by also converting and losing precision).
//
// For an apples-to-apples, bytes-mode comparison the decoder is fed a
// *bytes.Buffer: jsontext parses directly from a bytes.Buffer without copying
// into an intermediate buffer (see jsontext.NewDecoder), matching the
// default-lexer's []byte fast path.
package jsontext

import (
	"bytes"
	"io"

	"github.com/go-json-experiment/json/jsontext"
)

// Sink prevents the compiler from eliminating the walk.
var Sink int

// Walk fully tokenizes data with the jsontext decoder, draining every token to
// EOF. Numbers are validated but never converted (no native value is built).
// Returns the first non-EOF error.
func Walk(data []byte) error {
	dec := jsontext.NewDecoder(bytes.NewBuffer(data))
	for {
		tok, err := dec.ReadToken()
		if err != nil {
			if err == io.EOF {
				return nil
			}

			return err
		}
		Sink += int(tok.Kind())
	}
}

// Package easyjson drives mailru/easyjson's jlexer over a whole JSON document as
// a generic recursive walk, so it can be benchmarked as a comparison point for
// the default-lexer (both are pull-style, []byte-only lexers; easyjson is the
// original inspiration for the default-lexer).
//
// To compare apples to apples with the default-lexer — which emits raw token
// bytes and never converts numbers to native Go types — numbers here are taken
// via Raw() (the raw sub-slice, no numeric conversion). Strings are taken via
// String() (easyjson's typical, non-mutating path; UnsafeBytes would avoid the
// allocation but rewrites escapes in place, corrupting a reused fixture).
package easyjson

import "github.com/mailru/easyjson/jlexer"

// Sink prevents the compiler from eliminating the walk.
var Sink int

// Walk fully tokenizes data with jlexer, descending into every container, taking
// numbers as raw bytes (no numeric conversion). Returns the lexer error.
func Walk(data []byte) error { return walk(data, false) }

// WalkConvertNumbers is like Walk but converts each number with Float64(), which
// is where jlexer actually validates number grammar (Raw/JsonNumber do not). This
// rebalances the comparison against the default-lexer, which always validates
// numbers while lexing — though Float64 also *loses precision*, which the
// default-lexer never does.
func WalkConvertNumbers(data []byte) error { return walk(data, true) }

func walk(data []byte, convertNumbers bool) error {
	l := &jlexer.Lexer{Data: data}
	walkValue(l, convertNumbers)

	return l.Error()
}

func walkValue(l *jlexer.Lexer, convertNumbers bool) {
	switch l.CurrentToken() {
	case jlexer.TokenString:
		Sink += len(l.String())

	case jlexer.TokenNumber:
		if convertNumbers {
			if l.Float64() != 0 { // validates via strconv.ParseFloat (lossy)
				Sink++
			}
		} else {
			Sink += len(l.Raw()) // raw bytes, no numeric conversion
		}

	case jlexer.TokenBool:
		if l.Bool() {
			Sink++
		}

	case jlexer.TokenNull:
		l.Null()

	case jlexer.TokenDelim:
		switch {
		case l.IsDelim('{'):
			l.Delim('{')
			for l.Ok() && !l.IsDelim('}') {
				Sink += len(l.String()) // key
				l.WantColon()
				walkValue(l, convertNumbers)
				l.WantComma()
			}
			l.Delim('}')

		case l.IsDelim('['):
			l.Delim('[')
			for l.Ok() && !l.IsDelim(']') {
				walkValue(l, convertNumbers)
				l.WantComma()
			}
			l.Delim(']')
		}

	default: // TokenUndef
	}
}

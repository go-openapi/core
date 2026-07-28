// Package stdlib provides a baseline [lexers.Lexer] implemented on top of the
// standard library encoding/json tokenizer (v1).
//
// It exists purely as a performance and behavior baseline for the in-repo
// default-lexer. It is intentionally minimal:
//
//   - it relies on [encoding/json.Decoder.Token], which already evaluates
//     booleans and (without UseNumber) numbers; we enable UseNumber so numbers
//     are kept as their raw text, closer to the no-evaluation design of the
//     default-lexer;
//   - the standard tokenizer elides ',' and ':' delimiters, so this lexer never
//     emits them (the default-lexer does, unless WithElideSeparator is set);
//   - strings are always reported as [token.String]: the standard tokenizer does
//     not distinguish object keys from string values;
//   - [Lexer.IndentLevel] is tracked approximately from the bracket delimiters.
//
// A v2 baseline on top of encoding/json/jsontext is planned separately.
package stdlib

import (
	"bytes"
	stdjson "encoding/json"
	"errors"
	"io"
	"iter"

	"github.com/go-openapi/core/json/lexers/token"
)

// Lexer is a baseline JSON lexer wrapping the standard library decoder.
//
// It implements github.com/go-openapi/core/json/lexers.Lexer.
type Lexer struct {
	dec   *stdjson.Decoder
	err   error
	depth int
}

// New builds a baseline lexer consuming from an [io.Reader].
func New(r io.Reader) *Lexer {
	l := &Lexer{}
	l.dec = stdjson.NewDecoder(r)
	l.dec.UseNumber()

	return l
}

// NewWithBytes builds a baseline lexer consuming from a buffer of bytes.
func NewWithBytes(data []byte) *Lexer {
	return New(bytes.NewReader(data))
}

// NextToken returns the next JSON token, or [token.EOFToken] at the end of the input.
func (l *Lexer) NextToken() token.T {
	if l.err != nil {
		return token.None
	}

	jtok, err := l.dec.Token()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return token.EOFToken
		}

		l.err = err

		return token.None
	}

	switch v := jtok.(type) {
	case nil:
		return token.NullToken

	case bool:
		return token.MakeBoolean(v)

	case stdjson.Number:
		return token.MakeWithValue(token.Number, []byte(v))

	case string:
		return token.MakeWithValue(token.String, []byte(v))

	case stdjson.Delim:
		switch v {
		case '{':
			l.depth++

			return token.MakeDelimiter(token.OpeningBracket)
		case '}':
			l.depth--

			return token.MakeDelimiter(token.ClosingBracket)
		case '[':
			l.depth++

			return token.MakeDelimiter(token.OpeningSquareBracket)
		case ']':
			l.depth--

			return token.MakeDelimiter(token.ClosingSquareBracket)
		}
	}

	l.err = errors.New("unexpected standard library JSON token")

	return token.None
}

// Tokens iterates over the JSON tokens up to (not including) EOF; check Ok/Err
// after the loop.
func (l *Lexer) Tokens() iter.Seq[token.T] {
	return func(yield func(token.T) bool) {
		for {
			tok := l.NextToken()
			if l.err != nil {
				return
			}
			if tok.Kind() == token.EOF {
				return
			}
			if !yield(tok) {
				return
			}
		}
	}
}

// Offset yields the number of bytes consumed so far.
func (l *Lexer) Offset() uint64 {
	return uint64(l.dec.InputOffset()) //nolint:gosec // G1115 false positive
}

// IndentLevel yields the current nesting depth of containers (approximate).
func (l *Lexer) IndentLevel() int {
	return l.depth
}

// Ok reports whether no error has occurred so far.
func (l *Lexer) Ok() bool {
	return l.err == nil
}

// Err returns any error that happened during lexing.
func (l *Lexer) Err() error {
	return l.err
}

// SetErr injects an error state into the lexer.
func (l *Lexer) SetErr(err error) {
	l.err = err
}

// Reset clears the error state. The underlying decoder is replaced on each new input.
func (l *Lexer) Reset() {
	l.err = nil
	l.depth = 0
}

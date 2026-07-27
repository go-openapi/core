package stdlib

import (
	"testing"

	"github.com/go-openapi/core/json/lexers/token"
)

func TestBaselineLexer(t *testing.T) {
	const doc = `{"a":[1,2.5e3,"x",true,null],"b":{"c":false}}`

	lex := NewWithBytes([]byte(doc))

	var (
		kinds []token.Kind
		n     int
	)
	for {
		tok := lex.NextToken()
		if !lex.Ok() {
			t.Fatalf("unexpected error after %d tokens: %v", n, lex.Err())
		}
		if tok.IsEOF() {
			break
		}
		kinds = append(kinds, tok.Kind())
		n++
	}

	if n == 0 {
		t.Fatal("no tokens produced")
	}
	if !lex.Ok() {
		t.Fatalf("lexer ended in error: %v", lex.Err())
	}

	// the standard tokenizer elides ',' and ':' so we only see brackets + values
	wantOpen := kinds[0]
	if wantOpen != token.Delimiter {
		t.Fatalf("expected first token to be a delimiter, got %v", wantOpen)
	}
}

func TestBaselineRejectsInvalid(t *testing.T) {
	lex := NewWithBytes([]byte(`{"a":}`))
	for {
		tok := lex.NextToken()
		if !lex.Ok() || tok.IsEOF() {
			break
		}
	}
	if lex.Ok() {
		t.Fatal("expected an error on invalid input")
	}
}

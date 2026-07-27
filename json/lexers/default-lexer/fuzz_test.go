package lexer

// Fuzz harness for the two lexers (L semantic, VL verbatim).
//
// It does far more than "must not panic": for EVERY input it asserts the structural
// invariants that the lexers must uphold no matter how adversarial the bytes are —
// self-consistent properties, so a failure is always a real bug (no external oracle
// that could disagree for legitimate reasons):
//
//   1. Safety: no panic (the fuzzer catches it), and the scan always terminates
//      (a hard token cap catches a non-advancing / infinite loop).
//   2. No mutation: whole-buffer lexing aliases the caller's bytes and must never
//      write them.
//   3. push ≡ pull and buffer ≡ stream, in two tiers:
//        - on ACCEPTED input: the byte-identical token stream;
//        - on ALL input: the same accept/reject verdict.
//      The token *prefix* on rejected input is intentionally NOT compared. The four
//      cores are independent code paths and, by design, defer error detection to
//      different points — e.g. "0.0.0" emits the "0.0" token then errors on the stray
//      '.' in the buffer core, but rejects up front in the stream core (a deliberate
//      choice made while implementing the push/stream path). Both correctly reject.
//   4. L/VL agree on validity: both accept or both reject the same input (they lex
//      the same grammar; only their token *shape* differs).
//   5. Verbatim round-trip: for any ACCEPTED input, reassembling the raw VL token
//      stream (leading blanks + verbatim token text) reproduces the input byte for
//      byte — the defining property of the verbatim lexer.
//
// Run the seeds:   go test -run FuzzLexer
// Fuzz:            go test -run '^$' -fuzz FuzzLexer -fuzztime 60s

import (
	"bytes"
	"iter"
	"strings"
	"testing"

	"github.com/go-openapi/core/json/lexers/token"
)

// fzLexer is the shared surface of *L and *VL the harness drives.
type fzLexer interface {
	NextToken() token.T
	Tokens() iter.Seq[token.T]
	Ok() bool
	Err() error
}

// fzTok is a comparable snapshot of a token (value cloned, since the lexer reuses the
// backing buffer between tokens).
type fzTok struct {
	kind  token.Kind
	delim token.KindDelimiter
	bval  bool
	val   []byte
}

func fzRec(t token.T) fzTok {
	return fzTok{
		kind:  t.Kind(),
		delim: t.Delimiter(),
		bval:  t.Bool(),
		val:   append([]byte(nil), t.Value()...),
	}
}

// fzCap bounds the token count so a non-terminating scan (a lexer that neither
// advances nor errors) surfaces as a panic the fuzzer records, instead of hanging.
func fzCap(n int) int { return 8*n + 256 }

func fzPull(l fzLexer, n int) ([]fzTok, error) {
	out := make([]fzTok, 0, 16)
	limit := fzCap(n)
	for i := 0; ; i++ {
		if i > limit {
			panic("lexer did not terminate (pull)")
		}
		tok := l.NextToken()
		if !l.Ok() {
			return out, l.Err()
		}
		if tok.Kind() == token.EOF {
			return out, nil
		}
		out = append(out, fzRec(tok))
	}
}

func fzPush(l fzLexer, n int) ([]fzTok, error) {
	out := make([]fzTok, 0, 16)
	limit := fzCap(n)
	i := 0
	for tok := range l.Tokens() {
		if i > limit {
			panic("lexer did not terminate (push)")
		}
		i++
		out = append(out, fzRec(tok))
	}

	return out, l.Err()
}

func fzErr(e error) string {
	if e == nil {
		return ""
	}

	return e.Error()
}

// fzSameStream asserts the two drains agree. The contract has two tiers, both
// self-consistent (a failure is always a real bug):
//
//   - For ALL input: the same accept/reject verdict. The two lanes lex the same
//     grammar, so one must never accept what the other rejects.
//   - For ACCEPTED input only: the byte-identical token stream. On REJECTED input the
//     token *prefix* is deliberately NOT compared — the buffer and stream (and push vs
//     pull) scanners are independent code paths whose error-recovery boundaries
//     legitimately differ (e.g. "0.0.0" emits the "0.0" token then errors on the
//     stray '.' in the buffer core, but rejects with zero tokens in the stream core).
//     Both correctly reject; how far each got before bailing is not a contract.
//
// This still catches the bugs that matter: any wrong tokenization of valid input, any
// accept/reject disagreement, plus (via the caller) panics, non-termination, mutation
// and round-trip breakage.
func fzSameStream(
	t *testing.T,
	label string,
	in []byte,
	a []fzTok,
	aErr error,
	b []fzTok,
	bErr error,
) {
	t.Helper()
	if (aErr == nil) != (bErr == nil) {
		t.Fatalf(
			"%s: accept/reject mismatch: %q vs %q\ninput=%q",
			label,
			fzErr(aErr),
			fzErr(bErr),
			in,
		)
	}
	if aErr != nil {
		return // rejected: token-prefix divergence across independent scanners is allowed
	}
	if len(a) != len(b) {
		t.Fatalf(
			"%s: token count mismatch on accepted input: %d vs %d\ninput=%q",
			label,
			len(a),
			len(b),
			in,
		)
	}
	for i := range a {
		if a[i].kind != b[i].kind || a[i].delim != b[i].delim ||
			a[i].bval != b[i].bval || !bytes.Equal(a[i].val, b[i].val) {
			t.Fatalf(
				"%s: token %d mismatch on accepted input: %+v vs %+v\ninput=%q",
				label,
				i,
				a[i],
				b[i],
				in,
			)
		}
	}
}

// fzSourceText reconstructs the exact source bytes of a verbatim token. VL keeps string
// values raw (escapes intact), so a string/key is just quote + raw value + quote; a
// number is its raw text; delimiters/keywords are literal.
func fzSourceText(tok token.T) []byte {
	switch tok.Kind() {
	case token.String, token.Key:
		b := make([]byte, 0, len(tok.Value())+2)
		b = append(b, '"')
		b = append(b, tok.Value()...)
		b = append(b, '"')

		return b
	case token.Number:
		return tok.Value()
	case token.Boolean:
		if tok.Bool() {
			return []byte("true")
		}

		return []byte("false")
	case token.Null:
		return []byte("null")
	case token.Delimiter:
		switch tok.Delimiter() {
		case token.OpeningBracket:
			return []byte("{")
		case token.ClosingBracket:
			return []byte("}")
		case token.OpeningSquareBracket:
			return []byte("[")
		case token.ClosingSquareBracket:
			return []byte("]")
		case token.Comma:
			return []byte(",")
		case token.Colon:
			return []byte(":")
		default:
			return nil
		}
	default:
		return nil
	}
}

// fzVerbatimRoundtrip reassembles the input from a fresh VL drain: each token is
// preceded by its LeadingSpace (the raw insignificant whitespace), and the EOF token's
// LeadingSpace carries any trailing whitespace. Called only for accepted input.
func fzVerbatimRoundtrip(data []byte, n int) []byte {
	vl := NewVerbatimWithBytes(data)
	out := make([]byte, 0, len(data))
	limit := fzCap(n)
	for i := 0; ; i++ {
		if i > limit {
			panic("lexer did not terminate (roundtrip)")
		}
		tok := vl.NextToken()
		if !vl.Ok() {
			return out // unreachable for accepted input; guard against a spurious loop
		}
		out = append(
			out,
			vl.LeadingSpace()...) // blanks preceding this token (or trailing blanks at EOF)
		if tok.Kind() == token.EOF {
			return out
		}
		out = append(out, fzSourceText(tok)...)
	}
}

func FuzzLexer(f *testing.F) {
	seeds := []string{
		// valid
		``,
		` `,
		"\t\n ",
		`{}`,
		`[]`,
		`null`,
		`true`,
		`false`,
		`0`,
		`-0`,
		`42`,
		`-42`,
		`3.14`,
		`-3.14`,
		`1e10`,
		`1E-10`,
		`-0.44e10`,
		`12.3456E-3`,
		`"hello"`,
		`"esc\t\n\"\\\/\b\f\r"`,
		`"unicode é☃"`,
		`"surrogate 😀"`,
		`{"a":1,"b":[2,3],"c":{"d":null},"e":true}`,
		`[1,2,3]`,
		`{"k":"v"}`,
		`   {  "k" :  "v"  }  `,
		"{\n\t\"a\": 1\n}",
		`{"дом":"ключ","emoji":"☃"}`,
		`["item-0001","line\t1\ncol\"2\""]`,
		// malformed / adversarial
		`[1,2,3,]`,
		`{"a":}`,
		`{,}`,
		`{:}`,
		`"unterminated`,
		`[`,
		`{`,
		`}`,
		`]`,
		`,`,
		`:`,
		`01`,
		`1.`,
		`.1`,
		`1e`,
		`+1`,
		`1..2`,
		`nul`,
		`tru`,
		`fals`,
		`nullx`,
		`truefalse`,
		`"\uZZZZ"`,
		`"\u"`,
		`"\`,
		`"\x"`,
		"\"\x00\"",
		"\"\x1f\"",
		`{"a":1,"a":2}`,
		`[1 2]`,
		`{"k"1}`,
		`123 456`,
		`[]extra`,
		"\uFEFF{}", // BOM (not JSON whitespace)
		`[[[[[[[[[[]]]]]]]]]]`,
		strings.Repeat("[", 64),
		strings.Repeat("[", 64) + strings.Repeat("]", 64),
		strings.Repeat(" ", 40) + "1" + strings.Repeat(" ", 40),
		`123456789012345678901234567890`,
		`1e999999`,
		`-`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		n := len(data)
		mkbuf := func() []byte { return append([]byte(nil), data...) }

		// --- L: four lanes ---
		db1, db2 := mkbuf(), mkbuf()
		lPull, lPullErr := fzPull(NewWithBytes(db1), n)
		lPush, lPushErr := fzPush(NewWithBytes(db2), n)
		fzSameStream(t, "L buffer pull vs push", data, lPull, lPullErr, lPush, lPushErr)

		lStream, lStreamErr := fzPull(New(bytes.NewReader(data), WithBufferSize(16)), n)
		fzSameStream(t, "L buffer vs stream", data, lPull, lPullErr, lStream, lStreamErr)

		// --- VL: four lanes (default: separators emitted, blanks/position tracked) ---
		vb1, vb2 := mkbuf(), mkbuf()
		vPull, vPullErr := fzPull(NewVerbatimWithBytes(vb1), n)
		vPush, vPushErr := fzPush(NewVerbatimWithBytes(vb2), n)
		fzSameStream(t, "VL buffer pull vs push", data, vPull, vPullErr, vPush, vPushErr)

		vStream, vStreamErr := fzPull(NewVerbatim(bytes.NewReader(data), WithBufferSize(16)), n)
		fzSameStream(t, "VL buffer vs stream", data, vPull, vPullErr, vStream, vStreamErr)

		// --- L and VL must agree on accept/reject (same grammar) ---
		if (lPullErr == nil) != (vPullErr == nil) {
			t.Fatalf(
				"L/VL accept-reject disagree: L err=%v, VL err=%v\ninput=%q",
				lPullErr,
				vPullErr,
				data,
			)
		}

		// --- verbatim round-trip on accepted input ---
		if vPullErr == nil {
			rt := fzVerbatimRoundtrip(data, n)
			if !bytes.Equal(rt, data) {
				t.Fatalf("VL round-trip mismatch:\n in =%q\n out=%q", data, rt)
			}
		}

		// --- whole-buffer lexing must not mutate the caller's bytes ---
		for _, d := range [][]byte{db1, db2, vb1, vb2} {
			if !bytes.Equal(d, data) {
				t.Fatalf(
					"whole-buffer lexing mutated the caller's input\n before=%q\n after =%q",
					data,
					d,
				)
			}
		}
	})
}

// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

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
//      byte — the defining property of the verbatim lexer. The single exception is a
//      leading UTF-8 BOM, consumed before any token exists and therefore not
//      re-emitted (input.CheckBOM; RFC 8259 §8.1 asks implementations not to emit one).
//   6. Well-formed on accept: an ACCEPTED token stream obeys the JSON grammar
//      (balanced and correctly typed containers, keys only in object key slots,
//      key/value alternation, exactly one root value). The lexers are "almost a
//      parser" (see DESIGN.md §5), so accepting a stream that no JSON parser could
//      build a document from is a bug — this is what caught `{"a":}`, which every
//      lane used to accept in agreement.
//
// Run the seeds:   go test -run FuzzLexer
// Fuzz:            go test -run '^$' -fuzz FuzzLexer -fuzztime 60s

import (
	"bytes"
	"errors"
	"fmt"
	"iter"
	"strings"
	"testing"
	"unicode/utf8"

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

// fzWellFormed checks that an ACCEPTED token stream obeys the JSON grammar. Separators are
// skipped (L elides them, VL emits them), so the same check runs over either lexer's
// stream: the document structure is carried by the container, key and value tokens.
func fzWellFormed(toks []fzTok) error {
	type frame struct {
		obj       bool
		expectKey bool // in an object, the next non-closing token must be a key
	}
	var (
		st       []frame
		rootDone bool
	)

	consumeValue := func() error {
		if len(st) == 0 {
			if rootDone {
				return errors.New("second root value")
			}
			rootDone = true

			return nil
		}
		top := &st[len(st)-1]
		if top.obj {
			if top.expectKey {
				return errors.New("value where an object key was expected")
			}
			top.expectKey = true // the pair is complete
		}

		return nil
	}

	for i, tk := range toks {
		switch {
		case tk.kind == token.EOF:
			continue
		case tk.kind == token.Delimiter &&
			(tk.delim == token.Comma || tk.delim == token.Colon):
			continue // separators carry no structure here
		case tk.kind == token.Key:
			if len(st) == 0 || !st[len(st)-1].obj || !st[len(st)-1].expectKey {
				return fmt.Errorf("token %d: key outside a key slot", i)
			}
			st[len(st)-1].expectKey = false
		case tk.kind == token.Delimiter && tk.delim == token.OpeningBracket:
			if err := consumeValue(); err != nil {
				return err
			}
			st = append(st, frame{obj: true, expectKey: true})
		case tk.kind == token.Delimiter && tk.delim == token.OpeningSquareBracket:
			if err := consumeValue(); err != nil {
				return err
			}
			st = append(st, frame{obj: false})
		case tk.kind == token.Delimiter && tk.delim == token.ClosingBracket:
			// expectKey is false only after a key whose value never came ({"a":}).
			if len(st) == 0 || !st[len(st)-1].obj || !st[len(st)-1].expectKey {
				return fmt.Errorf("token %d: mismatched } (or a key with no value)", i)
			}
			st = st[:len(st)-1]
		case tk.kind == token.Delimiter && tk.delim == token.ClosingSquareBracket:
			if len(st) == 0 || st[len(st)-1].obj {
				return fmt.Errorf("token %d: mismatched ]", i)
			}
			st = st[:len(st)-1]
		default: // a scalar value
			if err := consumeValue(); err != nil {
				return err
			}
		}
	}

	if len(st) != 0 {
		return fmt.Errorf("unbalanced containers: %d still open", len(st))
	}
	if !rootDone {
		return errors.New("no root value")
	}

	return nil
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
		// ill-formed UTF-8 in string bodies (rejected by the default policy; see utf8_test.go)
		"[\"\xff\"]",
		"[\"\xc0\xaf\"]",
		"[\"\xed\xa0\x80\"]",
		"[\"\xe0\xff\"]",
		"[\"ok\xe2\x82\"]",
		"{\"k\xff\":1}",
		"[\"caf\xc3\xa9\"]",
		"[\"\xe6\x97\xa5\xe6\x9c\xac\"]",
		`["\uD800\uD800"]`,
		`["𝄞"]`,
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

		// --- an accepted stream must be a well-formed JSON document ---
		if lPullErr == nil {
			if err := fzWellFormed(lPull); err != nil {
				t.Fatalf("L accepted an ill-formed stream: %v\ninput=%q", err, data)
			}
		}
		if vPullErr == nil {
			if err := fzWellFormed(vPull); err != nil {
				t.Fatalf("VL accepted an ill-formed stream: %v\ninput=%q", err, data)
			}
		}

		// --- verbatim round-trip on accepted input ---
		//
		// A leading UTF-8 BOM is the ONE documented exception: it is consumed before any token exists (see
		// input.CheckBOM), so it belongs to no token's leading space and is not re-emitted. RFC 8259 §8.1 asks
		// implementations not to emit a BOM, so dropping it is the conformant direction — but it does mean the
		// round-trip is byte-exact only for the document body.
		if vPullErr == nil {
			want := bytes.TrimPrefix(data, []byte("\uFEFF"))
			rt := fzVerbatimRoundtrip(data, n)
			if !bytes.Equal(rt, want) {
				t.Fatalf("VL round-trip mismatch:\n in =%q\n out=%q\n want=%q", data, rt, want)
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

		// --- UTF-8: no ill-formed sequence may reach the caller ---
		fzUTF8Invariants(t, data, n)
	})
}

// fzUTF8Invariants pins the promise the UTF-8 work exists to make: under the default policy an accepted document
// yields only well-formed values, and under UTF8Replace EVERY document that is otherwise grammatical is accepted and
// still yields only well-formed values.
//
// It is the broadest guard we have, because it holds for arbitrary bytes rather than for a fixture table: the fused
// non-ASCII detection is duplicated across eight scanners, and a miss in any one of them shows up here.
func fzUTF8Invariants(t *testing.T, data []byte, n int) {
	t.Helper()

	check := func(lane string, policy UTF8Policy, values [][]byte, err error) {
		if err != nil {
			return
		}
		for _, v := range values {
			if !utf8.Valid(v) {
				t.Fatalf(
					"%s emitted an ill-formed value under policy %d: %q\ninput=%q",
					lane,
					policy,
					v,
					data,
				)
			}
		}
	}

	for _, policy := range []UTF8Policy{UTF8Strict, UTF8Replace} {
		lVals, lErr := fzStringValues(
			NewWithBytes(append([]byte(nil), data...), WithUTF8Policy(policy)),
			n,
		)
		check("L/bytes", policy, lVals, lErr)

		sVals, sErr := fzStringValues(
			New(bytes.NewReader(data), WithBufferSize(32), WithUTF8Policy(policy)), n)
		check("L/reader", policy, sVals, sErr)

		// VL keeps escapes as source text, so its values are checked through the decoder — the form a caller
		// actually consumes.
		vVals, vErr := fzVerbatimDecodedValues(
			NewVerbatimWithBytes(append([]byte(nil), data...), WithUTF8Policy(policy)), n)
		check("VL/bytes", policy, vVals, vErr)

		vsVals, vsErr := fzVerbatimDecodedValues(
			NewVerbatim(bytes.NewReader(data), WithBufferSize(32), WithUTF8Policy(policy)), n)
		check("VL/reader", policy, vsVals, vsErr)

		// Replacement must never be the reason a document is rejected: it only ever turns a UTF-8 error into a
		// substitution, so anything the default policy accepts it must accept too.
		if policy == UTF8Replace && lErr != nil {
			if _, strictErr := fzStringValues(
				NewWithBytes(append([]byte(nil), data...)), n); strictErr == nil {
				t.Fatalf(
					"UTF8Replace rejected a document the default policy accepts: %v\ninput=%q",
					lErr,
					data,
				)
			}
		}
	}
}

// fzStringValues drains a semantic lexer and returns the value of every String/Key token.
func fzStringValues(lex *L, n int) ([][]byte, error) {
	var out [][]byte
	limit := fzCap(n)
	for i := 0; ; i++ {
		if i > limit {
			panic("lexer did not terminate (utf8 invariants)")
		}
		tok := lex.NextToken()
		if !lex.Ok() {
			return out, lex.Err()
		}
		if k := tok.Kind(); k == token.String || k == token.Key {
			out = append(out, bytes.Clone(tok.Value()))
		}
		if tok.IsEOF() {
			return out, nil
		}
	}
}

// fzVerbatimDecodedValues drains a verbatim lexer and returns every String/Key value in DECODED form.
func fzVerbatimDecodedValues(lex *VL, n int) ([][]byte, error) {
	var out [][]byte
	limit := fzCap(n)
	for i := 0; ; i++ {
		if i > limit {
			panic("lexer did not terminate (utf8 invariants, verbatim)")
		}
		tok := lex.NextToken()
		if !lex.Ok() {
			return out, lex.Err()
		}
		if k := tok.Kind(); k == token.String || k == token.Key {
			out = append(out, token.Unescape(bytes.Clone(tok.Value())))
		}
		if tok.IsEOF() {
			return out, nil
		}
	}
}

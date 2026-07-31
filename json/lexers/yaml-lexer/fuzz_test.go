package lexer_test

// Fuzz harness for the YAML lexer YL.
//
// For EVERY input it asserts self-consistent structural invariants (a failure is always a
// real bug, no external oracle that could legitimately disagree):
//
//  1. Safety: no panic (the fuzzer catches it), and a bounded token budget catches a
//     runaway walk.
//  2. No mutation: whole-buffer lexing aliases the caller's bytes and must never write them.
//  3. Determinism: two independent lexings of the same bytes agree (tokens + accept/reject).
//  4. Reset equivalence: a recycled lexer (ResetWithBytes) yields the same result as a fresh
//     one.
//  5. Well-formed on accept: an accepted token stream obeys the JSON grammar (balanced,
//     correctly typed containers; keys only in objects; key/value alternation).
//  6. JSON-subset differential: when BOTH the JSON lexer L and YL accept the same bytes, the
//     token stream (and IndentLevel) is identical — JSON is a subset of YAML. (Only gated on
//     both accepting: YAML is stricter in places, e.g. it rejects duplicate keys that JSON
//     tolerates, so L-accepts does not imply YL-accepts.)
//
// Run seeds:  go test -run FuzzYL
// Fuzz:       go test -run '^$' -fuzz FuzzYL -fuzztime 60s

import (
	"bytes"
	"errors"
	"fmt"
	"iter"
	"testing"
	"unicode/utf8"

	jsonlexer "github.com/go-openapi/core/json/lexers/default-lexer"
	"github.com/go-openapi/core/json/lexers/token"
	yamllexer "github.com/go-openapi/core/json/lexers/yaml-lexer"
)

// fzTok is a comparable snapshot of a token (value cloned, since the lexer reuses the
// backing buffer between tokens).
type fzTok struct {
	kind  token.Kind
	delim token.KindDelimiter
	bval  bool
	val   string
	lvl   int
}

func fzRec(t token.T, lvl int) fzTok {
	return fzTok{
		kind:  t.Kind(),
		delim: t.Delimiter(),
		bval:  t.Bool(),
		val:   string(t.Value()),
		lvl:   lvl,
	}
}

// fzCap bounds the token count so a runaway walk surfaces as a failure, not a hang.
func fzCap(data []byte) int { return 16*len(data) + 4096 }

type tokenLexer interface {
	Tokens() iter.Seq[token.T]
	IndentLevel() int
	Err() error
}

// fzDrain collects the snapshotted token stream and the terminal error.
func fzDrain(l tokenLexer, budget int, t *testing.T) ([]fzTok, error) {
	t.Helper()
	var out []fzTok
	for tok := range l.Tokens() {
		out = append(out, fzRec(tok, l.IndentLevel()))
		if len(out) > budget {
			t.Fatalf("token budget %d exceeded: runaway walk", budget)
		}
	}

	return out, l.Err()
}

// fzValidate checks that an accepted token stream obeys the JSON grammar.
func fzValidate(toks []fzTok) error {
	type frame struct {
		obj       bool
		expectKey bool
	}
	var st []frame
	rootDone := false

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
				return errors.New("value where object key expected")
			}
			top.expectKey = true
		}

		return nil
	}

	for i, tk := range toks {
		switch {
		case tk.kind == token.EOF:
			continue
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
			if len(st) == 0 || !st[len(st)-1].obj || !st[len(st)-1].expectKey {
				return fmt.Errorf("token %d: mismatched }", i)
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

	return nil
}

func fzToksEqual(a, b []fzTok) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func FuzzYL(f *testing.F) {
	seeds := []string{
		"", " ", "\n", "null", "42", `"x"`, "true",
		"{}", "[]", "a: 1", "- 1\n- 2",
		`{"a":1,"b":[true,false,null],"c":"hi"}`,
		"a: &x 1\nb: *x",
		"base: &b {a: 1, b: 2}\nderived:\n  <<: *b\n  c: 3",
		"v: 0x1F", "v: 1_000", "v: 1e3", "v: .5", "v: .inf", "v: .nan",
		"---\na: 1\n---\nb: 2",
		"123: ok", "? [a, b]\n: c",
		"[", "{", "a: [", ": :", "\x00", "\xff\xfe", "\xef\xbb\xbfa: 1", "a:\t1",
		"a: !!str 7\nb: !!bool yes",
		// merge-key cycles: a mapping whose own "<<" aliases itself used to recurse forever in
		// resolveMapping and kill the process with a fatal stack overflow (CI: "fuzzing process
		// hung or terminated unexpectedly").
		"e: &b\n  <<: *b\n",
		"e: &b\n  <<: *b\n  c ",
		"e: &b\n  a: 1\n  <<: *b\n  c: 3\n",
		"e: &b\n  <<: [*b]\n",
		"a: &x\n  - *x\n",
		// a leading UTF-8 BOM is a document prefix, not content. goccy does not strip it, so it
		// used to become the first character of the first token and change the parse outright:
		// "<BOM>{}" came back as a SCALAR. Caught by the JSON-subset differential, since L does
		// consume a leading BOM.
		"\uFEFF\t0",
		"\uFEFF{}",
		"\uFEFF[1]",
		"\uFEFFa: 1\n",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// Keep goccy away from fatal (unrecoverable) deep-recursion overflow; the fuzzer's
		// interesting inputs for OUR logic are small.
		if len(data) > 64<<10 {
			t.Skip()
		}

		opts := []yamllexer.Option{
			yamllexer.WithMaxContainerStack(256), // bound OUR recursive walk
			yamllexer.WithMaxTokens(1 << 20),
		}
		budget := fzCap(data)

		// keep a copy to detect mutation of the aliased buffer
		orig := append([]byte(nil), data...)

		toks1, err1 := fzDrain(yamllexer.NewWithBytes(data, opts...), budget, t)

		if !bytes.Equal(orig, data) {
			t.Fatalf("input mutated during lexing")
		}

		// determinism
		toks2, err2 := fzDrain(yamllexer.NewWithBytes(data, opts...), budget, t)
		if (err1 == nil) != (err2 == nil) || !fzToksEqual(toks1, toks2) {
			t.Fatalf("non-deterministic: err1=%v err2=%v", err1, err2)
		}

		// reset equivalence
		reused := yamllexer.NewWithBytes([]byte("seed: 1"), opts...)
		reused.ResetWithBytes(data)
		toks3, err3 := fzDrain(reused, budget, t)
		if (err1 == nil) != (err3 == nil) || !fzToksEqual(toks1, toks3) {
			t.Fatalf("reset not equivalent to fresh: err1=%v err3=%v", err1, err3)
		}

		// well-formed on accept
		if err1 == nil {
			if err := fzValidate(toks1); err != nil {
				t.Fatalf("accepted an ill-formed stream: %v", err)
			}
		}

		// JSON-subset differential (only for valid UTF-8, and only when both accept).
		// On INVALID UTF-8 inside a string the two libraries legitimately differ: the JSON
		// lexer passes the raw bytes through, while goccy replaces them with U+FFFD. That is
		// a decoding-policy difference, not a grammar divergence, so it is out of scope here.
		if !utf8.Valid(data) {
			return
		}
		jl := jsonlexer.NewWithBytes(data)
		var jtoks []fzTok
		for tok := range jl.Tokens() {
			jtoks = append(jtoks, fzRec(tok, jl.IndentLevel()))
			if len(jtoks) > budget {
				return
			}
		}
		if jl.Err() == nil && err1 == nil {
			if !fzToksEqual(jtoks, toks1) {
				t.Fatalf("JSON-subset divergence:\n L=%v\nYL=%v", jtoks, toks1)
			}
		}
	})
}

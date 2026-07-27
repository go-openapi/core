package lexer

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/go-openapi/testify/v2/require"

	"github.com/go-openapi/core/json/lexers/token"
)

// TestStreamFastEquivalence pins the core Phase-1 invariant: for ANY input and ANY window width, streaming NextToken
// yields the exact same token stream (kinds AND decoded values) and terminal error as whole-buffer NextToken.
//
// Small windows drive the span/delegate path; large ones drive the zero-copy alias fast path; the sizes between land
// the closing quote on either side of a window boundary.
func TestStreamFastEquivalence(t *testing.T) {
	bufSizes := []int{1, 2, 3, 4, 5, 7, 8, 15, 16, 17, 31, 32, 33, 64, 128, 1024}

	for _, in := range streamFastInputs() {
		data := []byte(in)

		want, wantErr := collectPullValues(NewWithBytes(data))

		for _, bs := range bufSizes {
			got, gotErr := collectPullValues(newStreamLexerWindow(data, bs))

			// For MALFORMED input the whole-buffer and streaming number consumers can differ in HOW a rejection surfaces: the
			// whole-buffer scanner folds a look-ahead (e.g. "1.2.3" → emit "1.2", defer the error to the rejected ".3"), while
			// consumeNumberStreaming rejects inline (repeated separator).
			//
			// Both REJECT the document — that is the contract we pin here. (This pre-existing divergence dissolves in Phase 2
			// when the byte-by-byte consumers are retired; see §10.3.) For well-formed input the streams must be
			// byte-identical.
			if wantErr != nil || gotErr != nil {
				if (wantErr == nil) != (gotErr == nil) {
					t.Errorf(
						"input %q bufsize %d: only one mode rejected: buffer=%v stream=%v",
						in,
						bs,
						wantErr,
						gotErr,
					)
				}

				continue
			}
			if fmt.Sprint(want) != fmt.Sprint(got) {
				t.Errorf(
					"input %q bufsize %d: token stream mismatch\n buffer=%v\n stream=%v",
					in,
					bs,
					want,
					got,
				)
			}
		}
	}
}

// TestStreamFastAliasesWindow asserts the fast path actually aliases the buffer (zero-copy) for a clean string that
// fits the window, rather than always copying through currentValue — the whole point of Phase 1. A returned String
// token whose value header points inside l.in.Buffer proves the alias.
func TestStreamFastAliasesWindow(t *testing.T) {
	data := []byte(`"a clean string that fits comfortably in the window"`)
	l := New(bytes.NewReader(data), WithBufferSize(256))

	tok := l.NextToken()
	if tok.Kind() != token.String {
		t.Fatalf("expected String, got %v", tok.Kind())
	}
	val := tok.Value()
	buf := l.in.Buffer[:cap(l.in.Buffer)]
	// the value must be a sub-slice of the lexer's window (alias), not a copy.
	aliased := len(val) > 0 &&
		&val[0] == &buf[l.in.Consumed-1-len(val)] // value ends just before the closing quote at consumed-1
	if !aliased {
		t.Errorf("streaming clean string was not aliased to the window (copied instead)")
	}
}

// ---- merged from stream_promote_test.go ---- TestFirstFillPromotion pins the whole-buffer short-circuit (§10.5f): a
// streaming lexer whose entire input fits in the buffer flips to wholeBuffer mode on the first token (running the fast
// in-buffer cores), while an input larger than the buffer stays in streaming mode.
//
// The token stream must be correct either way.
func TestFirstFillPromotion(t *testing.T) {
	doc := `{"a":[1,2,3],"b":"hello","c":true}`

	// fits in the (default, 4KB) buffer → promoted to whole-buffer on first token.
	t.Run("fits/promotes", func(t *testing.T) {
		l := New(bytes.NewReader([]byte(doc)))
		if l.in.WholeBuffer {
			t.Fatal("wholeBuffer set before first token")
		}
		_ = l.NextToken()
		if !l.in.WholeBuffer {
			t.Fatal("expected promotion to whole-buffer after first token (input fits)")
		}
		// drain and compare to the pure whole-buffer lexer.
		want, _ := collectPullValues(NewWithBytes([]byte(doc)))
		l2 := New(bytes.NewReader([]byte(doc)))
		got, _ := collectPullValues(l2)
		if !l2.in.WholeBuffer {
			t.Fatal("second lexer did not promote")
		}
		if len(want) != len(got) {
			t.Fatalf("token count: whole=%d promoted=%d", len(want), len(got))
		}
	})

	// larger than the window → stays streaming (window forced small via reslice).
	t.Run("overflows/streams", func(t *testing.T) {
		big := `["` + strings.Repeat("x", 200) + `","` + strings.Repeat("y", 200) + `"]`
		l := New(bytes.NewReader([]byte(big)), WithBufferSize(64))
		l.in.Buffer = l.in.Buffer[:64] // force a 64-byte window < input
		_ = l.NextToken()
		if l.in.WholeBuffer {
			t.Fatal("did not expect promotion: input exceeds the window")
		}
	})

	// empty input: promotes (whole input — nothing — fits) and reports ErrNoData.
	t.Run("empty/promotes", func(t *testing.T) {
		l := New(bytes.NewReader(nil))
		tok := l.NextToken()
		if !l.in.WholeBuffer {
			t.Fatal("empty input should promote to whole-buffer")
		}
		require.False(t, l.Ok())
		require.EqualT(t, token.EOF, tok.Kind())
	})
}

// TestStreamPushEquivalence pins that the native streaming push core (§10.5g, backing Tokens() over a reader) yields
// exactly the same token stream as the streaming pull core (NextToken), for both the semantic lexer L and the
// state-based verbatim lexer VS, across window sizes that force streaming (small) and promotion (large).
func TestStreamPushEquivalence(t *testing.T) {
	inputs := append(streamFastInputs(), vsInputs()...)
	sizes := []int{1, 4, 16, 64, 1024}

	for _, in := range inputs {
		data := []byte(in)

		// reference: streaming pull via NextToken (forced small window).
		wantPull, pullErr := collectPullValues(forceStreamWindow(data, 8))

		for _, bs := range sizes {
			// L: Tokens() push over a reader.
			gotPush := collectPushT(func(yield func(token.T) bool) {
				lx := forceStreamWindow(data, bs)
				for tk := range lx.Tokens() {
					if !yield(tk) {
						return
					}
				}
			})
			if pullErr == nil && fmt.Sprint(wantPull) != fmt.Sprint(gotPush) {
				t.Errorf(
					"L push vs pull mismatch, input %q bufsize %d\n pull=%v\n push=%v",
					in,
					bs,
					wantPull,
					gotPush,
				)
			}
		}

		// VS: Tokens() push must equal VS NextToken pull (kinds + RAW values).
		vsPull := drainVSPull(forceStreamWindowVS(data, 8))
		for _, bs := range sizes {
			vsPush := drainVSPush(forceStreamWindowVS(data, bs))
			if vsPull.ok && fmt.Sprint(vsPull.toks) != fmt.Sprint(vsPush.toks) {
				t.Errorf(
					"VS push vs pull mismatch, input %q bufsize %d\n pull=%v\n push=%v",
					in,
					bs,
					vsPull.toks,
					vsPush.toks,
				)
			}
			// blanks accompanying each token must match too (accessor vs accessor).
			if vsPull.ok && fmt.Sprint(vsPull.blanks) != fmt.Sprint(vsPush.blanks) {
				t.Errorf(
					"VS push vs pull BLANKS mismatch, input %q bufsize %d\n pull=%v\n push=%v",
					in,
					bs,
					vsPull.blanks,
					vsPush.blanks,
				)
			}
		}
	}
}

// newStreamLexerWindow builds a streaming lexer whose read window is EXACTLY bs bytes wide, bypassing the
// WithBufferSize alignment floor by reslicing the buffer's length (its capacity stays aligned).
//
// The public guard rail keeps a caller's window at >= 32 bytes, but the internal refill/fast-path machinery must stay
// correct at ANY window width — a sub-32 window is just the general case of a partial read (every 4 KB buffer's final
// read leaves bufferized < cap), and Phase 2 slide+grow may reintroduce narrow effective windows — so the equivalence
// sweep exercises widths below the floor too.
func newStreamLexerWindow(data []byte, bs int) *L {
	l := New(bytes.NewReader(data), WithBufferSize(bs))
	if bs < cap(l.in.Buffer) {
		l.in.Buffer = l.in.Buffer[:bs]
	}

	return l
}

// forceStreamWindow builds a streaming lexer whose read window is EXACTLY bs
// bytes (reslicing past the WithBufferSize floor), so an input larger than bs stays in streaming mode — exercising
// the streaming push/pull cores rather than the whole-buffer short-circuit (§10.5f).
func forceStreamWindow(data []byte, bs int) *L {
	l := New(bytes.NewReader(data), WithBufferSize(bs))
	if bs < cap(l.in.Buffer) {
		l.in.Buffer = l.in.Buffer[:bs]
	}

	return l
}

func collectPushT(seq func(func(token.T) bool)) [][2]string {
	var out [][2]string
	for t := range seq {
		out = append(out, [2]string{t.Kind().String(), string(t.Value())})
	}

	return out
}

func forceStreamWindowVS(data []byte, bs int) *VL {
	vs := NewVerbatim(bytes.NewReader(data), WithBufferSize(bs))
	if bs < cap(vs.in.Buffer) {
		vs.in.Buffer = vs.in.Buffer[:bs]
	}

	return vs
}

type vsDrain struct {
	toks   [][2]string
	blanks []string
	ok     bool
}

func drainVSPull(vs *VL) vsDrain {
	var d vsDrain
	for {
		t := vs.NextToken()
		if !vs.Ok() {
			d.ok = false

			return d
		}
		if t.Kind() == token.EOF {
			d.ok = true

			return d
		}
		d.toks = append(d.toks, [2]string{t.Kind().String(), string(t.Value())})
		d.blanks = append(d.blanks, string(vs.LeadingSpace()))
	}
}

func drainVSPush(vs *VL) vsDrain {
	var d vsDrain
	for t := range vs.Tokens() {
		d.toks = append(d.toks, [2]string{t.Kind().String(), string(t.Value())})
		d.blanks = append(d.blanks, string(vs.LeadingSpace()))
	}
	d.ok = vs.Ok()

	return d
}

// collectPullValues drains a lexer via NextToken, returning (kind, value) pairs and the terminal error.
//
// It is mode-agnostic: buffer or stream, any buffer size.
func collectPullValues(l *L) ([][2]string, error) {
	var out [][2]string
	for {
		t := l.NextToken()
		if !l.Ok() {
			return out, l.Err()
		}
		if t.Kind() == token.EOF {
			return out, nil
		}
		out = append(out, [2]string{t.Kind().String(), string(t.Value())})
	}
}

// streamFastInputs exercises the streaming string fast path (§10.3 Phase 1) and its delegation seam: clean strings of
// many lengths (aliased when they fit the window, spanning when they don't), escapes at assorted offsets, \u sequences,
// control/ unterminated errors, and mixed value/number/structure documents.
func streamFastInputs() []string {
	const approxTestCases = 100
	in := make([]string, 0, approxTestCases)
	in = append(in,
		`""`, `"a"`, `"ab"`, `"abcdefg"`, `"abcdefgh"`, `"abcdefghi"`,
		`"0123456789abcdef"`,                  // 16
		`"0123456789abcdef0123456789abcdef"`,  // 32
		`"0123456789abcdef0123456789abcdefX"`, // 33
		`"`+strings.Repeat("x", 200)+`"`,      // long clean (AVX2 territory)
		`"esc\ttab"`, `"q\"q"`, `"back\\slash"`, `"sl\/ash"`,
		`"\n\r\t\b\f"`, `"a\nb\tc"`,
		`"uniécode"`, `"surrogate😀end"`,
		`"lead\nthen a long clean tail `+strings.Repeat("y", 100)+`"`,
		// gsoc-like: long clean runs (SWAR/AVX2 territory) between escapes — the Phase 1c bulk-clean-run path in the
		// delegated escaped scanner.
		`"`+strings.Repeat("clean words here ", 8)+`\n`+
			strings.Repeat("more clean text ", 8)+`\t`+
			strings.Repeat("and a long tail ", 12)+`"`,
		`{"doc":"`+strings.Repeat("x", 60)+`\"`+strings.Repeat("y", 60)+`"}`,
		`{"key":"value","k2":123,"k3":[true,false,null]}`,
		`{"description":"`+strings.Repeat("word ", 50)+`"}`,
		`[0,1,-1,42,-42,3.14,-3.14,1e10,1E-10,-0.44e10,12.3456E-3]`,
		"[\n\t \"a\" ,\n\t \"b\" \n]",
		// errors — buffer and stream must reach the same terminal state
		"\"unterminated", `"ctrl`+"\x01"+`"`, `"bad\xescape"`,
	)
	// numbers: fast-path forms, bail forms (must reach the same terminal state on both paths), and long numbers that span
	// small windows.
	in = append(in,
		`0`, `-0`, `42`, `-42`, `3.14`, `-3.14`, `1e10`, `1E-10`, `-0.44e10`, `12.3456E-3`,
		`[0]`, `[42,7]`, `[3.14,-2.5e8]`, `{"n":123}`, `{"n":-0.5e-3,"m":9}`,
		// bail / malformed — deferred-error semantics must match buffer mode
		`01`, `1.`, `1e`, `1e+`, `-`, `1.2.3`, `[1 2]`, `00`, `1.2e`, `-.5`,
		`123456789012345678901234567890`,                        // long int
		`3.14159265358979323846264338327950288`,                 // long decimal
		`1`+strings.Repeat("0", 80)+`e`+strings.Repeat("9", 40), // long int+exp
	)

	// strings whose closing quote lands at every offset near the small-buffer boundaries, to stress the "stop exactly at
	// window end" case.
	for _, l := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 15, 16, 17, 31, 32, 33} {
		in = append(in, `"`+strings.Repeat("a", l)+`"`)
	}
	// numbers whose terminator lands at every offset near the boundaries.
	for _, l := range []int{6, 7, 8, 9, 15, 16, 17, 31, 32, 33} {
		in = append(in, `[`+strings.Repeat("9", l)+`,0]`, strings.Repeat("9", l))
	}

	return in
}

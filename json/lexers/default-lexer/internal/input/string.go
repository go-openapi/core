package input

import (
	"encoding/binary"
	"errors"
	"io"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/go-openapi/core/json/internal/utf8x"
	"github.com/go-openapi/core/json/lexers/default-lexer/internal/strscan"
	"github.com/go-openapi/core/json/lexers/default-lexer/internal/swar"
	codes "github.com/go-openapi/core/json/lexers/error-codes"
	"github.com/go-openapi/core/json/lexers/token"
)

// swarProbe is the scalar look-ahead (bytes) the unescape slow path scans before switching a clean run to SWAR.
//
// Runs shorter than this (the escape-dense case) resolve scalar with no SWAR overhead; longer runs (sparse escapes +
// clean tail) switch to the word-at-a-time scan.
// One word keeps dense strings cheap.
const swarProbe = 8

// guessLong is the number of clean leading bytes after which the whole-buffer string scan stops probing inline (8-byte
// SWAR words) and hands the rest to the AVX2-gated strscan.ScanStop — Fred's "guess long strings" heuristic.
//
// It is the real long-string signal: strscan.ScanStop receives the buffer remainder (huge mid-document), so its own
// length guard cannot tell a short value from a long one; only the count of clean bytes already seen can.
// Short/medium values (object keys, most citm strings) resolve entirely inline and never pay the (non-inlinable) call;
// only genuinely long values, where AVX2's 32-bytes/iter pays off, delegate.
//
// 16 is the measured sweet spot across the full corpus (plan §9.3): it maximises geometric-mean throughput (+5.8%)
// while keeping the short-string workloads that gain nothing from AVX2 (citm, instruments, apache) at or above baseline
// — the larger win at 8 came with a real regression there.
// Must be a multiple of 8.
const guessLong = 16

const controlCharsUpperBound = 0x20 // ASCII control chars range

// ConsumeString scans a string value (the opening quote is already consumed).
//
// In whole-buffer mode it takes the fast path: a local-cursor scan that aliases the input for unescaped strings (zero
// copy) and falls back to copying only on the first escape.
// Streaming uses the buffer-refilling path.
//
// The verbatim lexer (flagged by trackBlanks, set only for VL) keeps strings RAW — escapes intact for faithful
// round-tripping, decoded on demand via token.VT.Unescaped — so it routes to the validate-but-don't-decode scanners.
//
// The choice lives here, not in the shared scan core: that keeps both cores calling l.in.ConsumeString() directly, so
// the semantic core's codegen is unchanged (plan §9.1 — routing this through the policy or an inline branch in the
// core perturbed the semantic path's escape analysis, costing an alloc).
func (in *Input) ConsumeString() token.T {
	if in.TrackBlanks {
		if in.WholeBuffer {
			return in.consumeStringRawWhole()
		}

		return in.consumeStringRawStreamFast()
	}

	if in.WholeBuffer {
		return in.consumeStringWhole()
	}

	return in.consumeStringStreamFast()
}

// consumeStringStreamFast is the streaming string fast path (§10.3 Phase 1).
//
// It treats the CURRENT buffer window l.in.Buffer[:l.in.Bufferized] like whole-buffer mode: it scans for the closing
// quote with the shared SWAR/AVX2 string-stop scanner and, when a clean string completes inside the window, ALIASES the
// buffer zero-copy — the common case (token << window).
//
// It hands off to the byte-by-byte consumeStringStreaming (which refills and unescapes) only when the value actually
// needs the slow path: an escape, or the scan reaches the window end (the string may span a refill).
// Aliasing is valid until the next refill — the token's contractual lifespan (Fred: within the reuse contract).
//
// Unlike consumeStringWhole, reaching l.in.Bufferized is NOT end-of-input, so it must delegate there rather than report
// ErrUnterminatedString; and because streaming keeps l.in.Offset as the absolute stream offset (l.in.Consumed is the
// window index), advances are RELATIVE deltas, not absolute assignments.
//
//nolint:dupl // the structure is the same but differs subtly and its critical that the inside remains inlined.
func (in *Input) consumeStringStreamFast() token.T {
	data := in.Buffer
	n := in.Bufferized
	start := in.Consumed // first content byte (opening quote already consumed)

	// jump to the first stop byte (closing quote, escape, or control), 8 bytes at a time; delegate to the AVX2 scan once a
	// run stays clean past guessLong.
	// Identical probe to consumeStringWhole, but bounded by the window end n = in.Bufferized.
	i := start
	var hi uint64 // OR of the content bytes scanned; see consumeStringWhole
	guard := start + guessLong
	if in.NoAVX2 {
		guard = n + 1
	}
	for i+8 <= n {
		w := binary.LittleEndian.Uint64(data[i:])
		if m := swar.StringStopMask(w); m != 0 {
			k := swar.FirstByte(m)
			hi |= swar.LanesBelow(w, k)
			i += k

			break
		}
		hi |= w
		i += 8
		if i >= guard {
			break
		}
	}
	if i >= guard && i+8 <= n {
		if c := data[i]; c != doubleQuote && c != escape && c >= controlCharsUpperBound {
			delta, nonASCII := strscan.ScanStop(data[i:n])
			i += delta
			if nonASCII {
				hi |= swar.HighBits
			}
		}
	}
	for ; i < n; i++ {
		c := data[i]
		if c == doubleQuote || c == escape || c < controlCharsUpperBound {
			break
		}
		hi |= uint64(c)
	}

	if i >= n {
		// window end reached without a stop byte: the string may continue past a refill boundary → hand off to the
		// refilling byte-by-byte path (in.Consumed is still start, so it re-scans the clean prefix and continues correctly).
		return in.consumeStringStreaming()
	}

	switch c := data[i]; {
	case c == doubleQuote:
		if in.MaxValueBytes > 0 && i-start > in.MaxValueBytes {
			in.Offset += uint64(i - start)
			in.Consumed = i
			in.Err = codes.ErrMaxValueBytes

			return token.None
		}
		value := data[start:i:i] // alias the window (valid until next refill)
		end := i + 1             // past the closing quote
		in.Offset += uint64(end - start)
		in.Consumed = end

		return in.finishStringValue(value, hi&swar.HighBits != 0)

	case c < controlCharsUpperBound:
		in.Offset += uint64(i - start)
		in.Consumed = i
		in.Err = codes.ErrControlChar

		return token.None
	}

	// an escape was found inside the window: delegate to the streaming unescape path (re-scans from in.Consumed == start;
	// handles escapes + any refill).
	return in.consumeStringStreaming()
}

// consumeStringWhole scans a string when the whole input is in l.in.Buffer.
//
// The cursor is a pure local; in whole-buffer mode l.in.Offset always equals the buffer index, so it (and
// l.in.Consumed) are written back only at exit points.
//
//nolint:dupl // the structure is the same but differs subtly and its critical that the inside remains inlined.
func (in *Input) consumeStringWhole() token.T {
	data := in.Buffer
	n := in.Bufferized
	start := in.Consumed // first content byte

	// fast path: jump to the first byte that needs attention — the closing quote, an escape, or a control char —
	// scanning 8 bytes at a time with the shared SWAR string-stop mask (swar.StringStopMask inlines, so there is no call
	// per word; see internal/swar).
	// FirstByte locates the exact stop within the matching word; the scalar tail handles the final < 8 bytes.
	//
	// The overwhelmingly common case (no escapes, no control chars) aliases the input with zero copy.
	i := start
	// hi accumulates every content byte the scan passes over, so (hi & swar.HighBits) != 0 answers "did this value
	// contain a byte >= 0x80" for free — the word is already in a register. A value that is pure ASCII is thereby
	// proven valid UTF-8 with no second pass and no call; only the rest reaches the validator (see finishStringValue).
	// Lanes at or after the stop are trimmed off so the answer is exact, matching strscan.ScanStop's.
	var hi uint64
	// guard is where the inline probe stops and delegates to the AVX2 scan.
	// With WithoutAVX2 it is pushed past the buffer so the loop never breaks to delegate — the string is scanned
	// entirely by the inline SWAR word loop (the pre-AVX2 baseline), no vector call at alin.
	guard := start + guessLong
	if in.NoAVX2 {
		guard = n + 1
	}
	for i+8 <= n {
		w := binary.LittleEndian.Uint64(data[i:])
		if m := swar.StringStopMask(w); m != 0 {
			k := swar.FirstByte(m) // exact stop lane; skips the scalar re-scan
			hi |= swar.LanesBelow(w, k)
			i += k

			break
		}
		hi |= w
		i += 8
		if i >= guard {
			break // guessLong clean bytes in — leave the loop to delegate below
		}
	}
	// If the run stayed clean past guessLong (not stopped) and the buffer holds more, guess this is a long value and hand
	// the rest to the AVX2 scan.
	//
	// The call lives OUTSIDE the word loop on purpose: a call in the loop body pessimizes its register allocation for
	// every short string that never reaches it (plan §9.1), so short-string workloads must keep the tight, call-free loop
	// above. i lands on the stop byte or on n.
	if i >= guard && i+8 <= n {
		if c := data[i]; c != doubleQuote && c != escape && c >= controlCharsUpperBound {
			delta, nonASCII := strscan.ScanStop(data[i:n])
			i += delta
			if nonASCII {
				hi |= swar.HighBits
			}
		}
	}
	for ; i < n; i++ {
		c := data[i]
		if c == doubleQuote || c == escape || c < controlCharsUpperBound {
			break
		}
		hi |= uint64(c)
	}
	if i >= n {
		in.Consumed, in.Offset = i, uint64(i)
		in.Err = codes.ErrUnterminatedString

		return token.None
	}

	switch c := data[i]; {
	case c == doubleQuote:
		if in.MaxValueBytes > 0 && i-start > in.MaxValueBytes {
			in.Consumed, in.Offset = i, uint64(i)
			in.Err = codes.ErrMaxValueBytes

			return token.None
		}
		value := data[start:i:i] // alias the input (cap == len)
		i++                      // past the closing quote
		in.Consumed, in.Offset = i, uint64(i)

		return in.finishStringValue(value, hi&swar.HighBits != 0)

	case c < controlCharsUpperBound:
		in.Consumed, in.Offset = i, uint64(i)
		in.Err = codes.ErrControlChar

		return token.None
	}

	// an escape was found at i: hand off to the unescape slow path.
	// It is a separate function on purpose — keeping the byte-by-byte escape machinery out of this frame insulates the
	// fast path's codegen from it (and vice versa); they were previously one function, where a fast-path change could
	// regress the slow path by ~12% and vice versa (plan §4.2).
	return in.consumeStringEscaped(start, i, hi&swar.HighBits != 0)
}

// consumeStringEscaped is the unescape slow path, split out of consumeStringWhole.
//
// It is entered with data[i] == escape and start..i the clean prefix already scanned; nonASCII reports whether that
// prefix carried a byte >= 0x80, and is extended here over every further raw run copied into the value.
//
// Runes produced by a \u escape are deliberately NOT accumulated: they are valid by construction (the escape decoder
// rejects or replaces anything that is not a scalar value), so a value whose only non-ASCII comes from escapes still
// skips the validator.
func (in *Input) consumeStringEscaped(start, i int, nonASCII bool) token.T {
	data := in.Buffer
	n := in.Bufferized

	in.CurrentValue = append(in.CurrentValue[:0], data[start:i]...)

	for i < n {
		switch c := data[i]; {
		case c == doubleQuote:
			i++
			in.Consumed, in.Offset = i, uint64(i)

			return in.finishStringValue(in.CurrentValue, nonASCII)

		case c == escape:
			i++
			if i >= n {
				in.Consumed, in.Offset = i, uint64(i)
				in.Err = codes.ErrUnterminatedString

				return token.None
			}
			switch data[i] {
			case doubleQuote:
				in.CurrentValue = append(in.CurrentValue, '"')
				i++
			case escape:
				in.CurrentValue = append(in.CurrentValue, '\\')
				i++
			case slash:
				in.CurrentValue = append(in.CurrentValue, '/')
				i++
			case 'b':
				in.CurrentValue = append(in.CurrentValue, '\b')
				i++
			case 'f':
				in.CurrentValue = append(in.CurrentValue, '\f')
				i++
			case 'n':
				in.CurrentValue = append(in.CurrentValue, '\n')
				i++
			case 't':
				in.CurrentValue = append(in.CurrentValue, '\t')
				i++
			case 'r':
				in.CurrentValue = append(in.CurrentValue, '\r')
				i++
			case 'u':
				// hand off to the surrogate-aware decoder, which reads from in.Consumed; offset==index lets us sync trivially.
				in.Consumed = i + 1 // past 'u'
				in.Offset = uint64(in.Consumed)
				r, err := in.unescapeUnicodeSequence()
				if err != nil {
					in.Err = err

					return token.None
				}
				in.CurrentValue = utf8.AppendRune(in.CurrentValue, r)
				i = in.Consumed
			default:
				in.Consumed, in.Offset = i, uint64(i)
				in.Err = codes.ErrUnknownEscape

				return token.None
			}

		case c < controlCharsUpperBound:
			in.Consumed, in.Offset = i, uint64(i)
			in.Err = codes.ErrControlChar

			return token.None
		}

		// Scan the clean run after the escape to the next stop byte, then bulk-append it.
		// Adaptive scan (escapes are usually sparse): start scalar — in escape-dense strings the runs are tiny and a SWAR
		// word-load would cost more than it saves — but once a run proves longer than a word, bet the rest of the string is
		// mostly clean and finish the run with SWAR.
		//
		// This keeps the dense case cheap while making the long-clean-tail case (sparse escapes + a long unescaped trail)
		// fast.
		// The bound is checked against len + the run width *before* the append so an over-long value is rejected without
		// copying a huge clean run, and escape-only expansion (zero-width run) is still caught.
		var hi uint64
		stop := i
		probe := min(i+swarProbe, n)
		for ; stop < probe; stop++ {
			c := data[stop]
			if c == doubleQuote || c == escape || c < controlCharsUpperBound {
				break
			}
			hi |= uint64(c)
		}
		if stop == probe &&
			stop < n { // run outran the scalar probe → SWAR, guess long past guessLong
			for stop+8 <= n {
				w := binary.LittleEndian.Uint64(data[stop:])
				if m := swar.StringStopMask(w); m != 0 {
					k := swar.FirstByte(m)
					hi |= swar.LanesBelow(w, k)
					stop += k

					break
				}
				hi |= w
				stop += 8
				if stop-i >= guessLong && !in.NoAVX2 {
					delta, runNonASCII := strscan.ScanStop(data[stop:n])
					stop += delta
					if runNonASCII {
						hi |= swar.HighBits
					}

					break
				}
			}
			for ; stop < n; stop++ {
				c := data[stop]
				if c == doubleQuote || c == escape || c < controlCharsUpperBound {
					break
				}
				hi |= uint64(c)
			}
		}
		if in.MaxValueBytes > 0 && len(in.CurrentValue)+(stop-i) > in.MaxValueBytes {
			in.Consumed, in.Offset = i, uint64(i)
			in.Err = codes.ErrMaxValueBytes

			return token.None
		}
		nonASCII = nonASCII || hi&swar.HighBits != 0
		in.CurrentValue = append(in.CurrentValue, data[i:stop]...)
		i = stop
	}

	in.Consumed, in.Offset = i, uint64(i)
	in.Err = codes.ErrUnterminatedString

	return token.None
}

// finishStringValue turns a scanned string body into a Key (in object key position) or String token, handling the
// trailing colon for keys.
//
// It is the single funnel every string path reaches, so it is where the UTF-8 policy is applied. nonASCII is the
// verdict the scan already produced: false means the value is pure ASCII and therefore valid UTF-8 by construction, so
// the common case costs one predictable branch and never touches the bytes again.
func (in *Input) finishStringValue(value []byte, nonASCII bool) token.T {
	if nonASCII && in.UTF8Policy.Validates() && !utf8x.Valid(value) {
		var ok bool
		if value, ok = in.handleInvalidUTF8(value); !ok {
			return token.None
		}
	}

	if in.ExpectKey {
		// the following colon is validated on the next scan (see in.AfterKey)
		in.ExpectKey = false
		in.AfterKey = true

		return token.MakeWithValue(token.Key, value)
	}

	return token.MakeWithValue(token.String, value)
}

// consumeStringStreaming is the byte-by-byte scan over a refilling buffer: it decodes escapes and copies content into
// in.CurrentValue so the value survives buffer turnover.
//
// The clean-run bulk scan in its default branch is deliberately inline rather than shared with the three sibling
// scanners: extracting it costs a call per run and, more importantly, the escape-analysis and inlining properties of
// this frame are load-bearing (see the fast/slow split note on consumeStringEscaped).
//
//nolint:maintidx // one long switch by design; splitting it perturbs the scan's codegen (see core_pull_buffer.go)
func (in *Input) consumeStringStreaming() token.T {
	var (
		escapeSequence bool
		nonASCII       bool // did any raw source byte carry the high bit (see consumeStringEscaped)
	)
	in.CurrentValue = in.CurrentValue[:0]

	for {
		if err := in.ReadMore(); err != nil {
			if errors.Is(err, io.EOF) {
				in.Err = codes.ErrUnterminatedString
			} else {
				in.Err = err
			}

			return token.None
		}

		for in.Consumed < in.Bufferized {

			if in.MaxValueBytes > 0 && len(in.CurrentValue) > in.MaxValueBytes {
				in.Err = codes.ErrMaxValueBytes

				return token.None
			}

			b := in.Buffer[in.Consumed]
			in.Offset++
			in.Consumed++

			switch b {
			case escape:
				if escapeSequence {
					//  "\\"
					in.CurrentValue = append(in.CurrentValue, b)
					escapeSequence = false

					continue
				}

				escapeSequence = true

			case doubleQuote:
				if escapeSequence {
					//  "\""
					escapeSequence = false
					in.CurrentValue = append(in.CurrentValue, b)

					continue
				}

				return in.finishStringValue(in.CurrentValue, nonASCII)

			case slash:
				if escapeSequence {
					// "\/"
					escapeSequence = false
				}

				in.CurrentValue = append(in.CurrentValue, b)

			case 'b', 'f', 'n', 't', 'r':
				if !escapeSequence {
					in.CurrentValue = append(in.CurrentValue, b)

					continue
				}
				// shorthand escaped representations of popular characters https://www.rfc-editor.org/rfc/rfc8259#page-9.
				escapeSequence = false

				switch b {
				case 'b':
					in.CurrentValue = append(in.CurrentValue, '\b')
				case 'f':
					in.CurrentValue = append(in.CurrentValue, '\f')
				case 'n':
					in.CurrentValue = append(in.CurrentValue, '\n')
				case 't':
					in.CurrentValue = append(in.CurrentValue, '\t')
				case 'r':
					in.CurrentValue = append(in.CurrentValue, '\r')
				}

			case 'u':
				if !escapeSequence {
					in.CurrentValue = append(in.CurrentValue, b)

					continue
				}

				escapeSequence = false
				r, err := in.unescapeUnicodeSequence()
				if err != nil {
					in.Err = err

					return token.None
				}

				in.CurrentValue = utf8.AppendRune(in.CurrentValue, r)

			default:
				if escapeSequence {
					in.Err = codes.ErrUnknownEscape

					return token.None
				}

				if b < controlCharsUpperBound {
					// RFC 8259: control characters U+0000..U+001F must be escaped
					in.Err = codes.ErrControlChar

					return token.None
				}

				nonASCII = nonASCII || b >= utf8.RuneSelf
				in.CurrentValue = append(in.CurrentValue, b)

				// bulk-scan the rest of this clean run within the current window (§10.3 Phase 1c): a long clean stretch (e.g.
				// between two escapes, or a clean string that spilled past the fast path's window) is copied in one shot —
				// adaptive scalar-probe → SWAR → AVX2, the streaming analogue of consumeStringEscaped's clean-run copy —
				// instead of one byte per loop turn.
				//
				// A run that reaches the window end just stops at bufferized; the outer loop refills and re-enters here for the
				// continuation.
				data := in.Buffer
				n := in.Bufferized
				runStart := in.Consumed
				stop := runStart
				var hi uint64
				probe := min(stop+swarProbe, n)
				for ; stop < probe; stop++ {
					c := data[stop]
					if c == doubleQuote || c == escape ||
						c < controlCharsUpperBound {
						break
					}
					hi |= uint64(c)
				}
				if stop == probe && stop < n { // run outran the scalar probe → SWAR
					for stop+8 <= n {
						w := binary.LittleEndian.Uint64(data[stop:])
						if m := swar.StringStopMask(w); m != 0 {
							k := swar.FirstByte(m)
							hi |= swar.LanesBelow(w, k)
							stop += k

							break
						}
						hi |= w
						stop += 8
						if stop-runStart >= guessLong && !in.NoAVX2 {
							delta, runNonASCII := strscan.ScanStop(data[stop:n])
							stop += delta
							if runNonASCII {
								hi |= swar.HighBits
							}

							break
						}
					}
					for ; stop < n; stop++ {
						c := data[stop]
						if c == doubleQuote || c == escape ||
							c < controlCharsUpperBound {
							break
						}
						hi |= uint64(c)
					}
				}
				nonASCII = nonASCII || hi&swar.HighBits != 0
				if stop > runStart {
					if in.MaxValueBytes > 0 &&
						len(in.CurrentValue)+(stop-runStart) > in.MaxValueBytes {
						in.Err = codes.ErrMaxValueBytes

						return token.None
					}
					in.CurrentValue = append(in.CurrentValue, data[runStart:stop]...)
					in.Offset += uint64(stop - runStart)
					in.Consumed = stop
				}
			}
		}
	}
}

// unescapeUnicodeSequence decodes a \uXXXX escape (the leading "\u" is already consumed), combining a surrogate pair
// when one follows.
//
// A code unit that does not denote a Unicode scalar value is governed by the UTF-8 policy (see
// [Input.brokenSurrogate]): rejected under UTF8Strict, decoded to U+FFFD otherwise. Crucially the trailing escape is
// only consumed when it actually completes the pair, so `\uD800\uD800` yields TWO replacement runes rather than
// swallowing the second escape — the behavior [token.Unescape] already has for verbatim values.
func (in *Input) unescapeUnicodeSequence() (rune, error) {
	_, r, err := in.readHex4()
	if err != nil {
		return utf8.RuneError, err
	}

	if !utf16.IsSurrogate(r) {
		// four hex digits that are not a surrogate always denote a scalar value: nothing further to check
		return r, nil
	}

	if low, ok := in.peekLowSurrogate(); ok {
		if decoded := utf16.DecodeRune(r, low); decoded != utf8.RuneError {
			in.takePeeked(surrogateEscapeLen)

			return decoded, nil
		}
	}

	return utf8.RuneError, in.brokenSurrogate()
}

package input

import (
	"encoding/binary"
	"errors"
	"io"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/go-openapi/core/json/lexers/default-lexer/internal/strscan"
	"github.com/go-openapi/core/json/lexers/default-lexer/internal/swar"
	codes "github.com/go-openapi/core/json/lexers/error-codes"
	"github.com/go-openapi/core/json/lexers/token"
)

// The raw string scanners back the VERBATIM lexer.
// Unlike ConsumeString*, they do NOT decode escapes: they validate the whole string grammar (every escape, so a later
// decode via token.VT.Unescaped cannot fail) but keep the RAW source bytes.
//
// This is both the faithful round-trip contract for [VT] and strictly less work than decoding (no output
// materialization; whole-buffer aliases with zero copy).
// ConsumeString dispatches here when l.in.TrackBlanks is set (see string.go).

// consumeStringRawWhole is the whole-buffer raw scan.
//
// The clean-prefix fast path is identical to consumeStringWhole (SWAR to the first stop); the difference is only in how
// an escape is handled — validated in place, never decoded — so the returned value always aliases the raw input.
//
//nolint:dupl // the structure is the same but differs subtly and its critical that the inside remains inlined.
func (in *Input) consumeStringRawWhole() token.T {
	data := in.Buffer
	n := in.Bufferized
	start := in.Consumed

	i := start
	var hi uint64 // fused non-ASCII accumulator, see consumeStringWhole
	guard := start + guessLong
	if in.NoAVX2 {
		guard = n + 1 // WithoutAVX2: never delegate, pure inline SWAR (see consumeStringWhole)
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
			break // guessLong clean bytes in — delegate below (call kept out of the loop)
		}
	}
	// clean past guessLong → guess long, AVX2 scan of the rest (same heuristic and out-of-loop placement as
	// consumeStringWhole).
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
		// no escapes: raw == decoded, same aliasing exit as consumeStringWhole
		if in.MaxValueBytes > 0 && i-start > in.MaxValueBytes {
			in.Consumed, in.Offset = i, uint64(i)
			in.Err = codes.ErrMaxValueBytes

			return token.None
		}
		value := data[start:i:i]
		i++
		in.Consumed, in.Offset = i, uint64(i)

		return in.finishStringValue(value, hi&swar.HighBits != 0)

	case c < controlCharsUpperBound:
		in.Consumed, in.Offset = i, uint64(i)
		in.Err = codes.ErrControlChar

		return token.None
	}

	// an escape was found at i: validate the rest but keep the raw bytes.
	return in.consumeStringRawEscaped(start, i, hi&swar.HighBits != 0)
}

// consumeStringRawEscaped validates a string that contains at least one escape (data[i] == escape) without decoding it,
// and returns the raw content aliased from the input.
//
// Clean runs between escapes are skipped with the same adaptive scalar-probe-then-SWAR scan the decoder uses
// (consumeStringEscaped), but with no copying — so a sparse-escape string with a long clean tail stays fast.
// nonASCII carries the verdict for the clean prefix already scanned and is extended over every further raw run; see
// consumeStringEscaped. Unlike the decoding path, the value here keeps its escapes as source text, so a \u escape
// contributes only its (ASCII) escape bytes.
func (in *Input) consumeStringRawEscaped(start, i int, nonASCII bool) token.T {
	data := in.Buffer
	n := in.Bufferized

	for i < n {
		switch c := data[i]; {
		case c == doubleQuote:
			if in.MaxValueBytes > 0 && i-start > in.MaxValueBytes {
				in.Consumed, in.Offset = i, uint64(i)
				in.Err = codes.ErrMaxValueBytes

				return token.None
			}
			value := data[start:i:i]
			i++
			in.Consumed, in.Offset = i, uint64(i)

			return in.finishStringValue(value, nonASCII)

		case c == escape:
			i++
			if i >= n {
				in.Consumed, in.Offset = i, uint64(i)
				in.Err = codes.ErrUnterminatedString

				return token.None
			}
			switch data[i] {
			case doubleQuote, escape, slash, 'b', 'f', 'n', 'r', 't':
				i++
			case 'u':
				next, err := validateUnicodeWhole(data, i+1, n, in.UTF8Policy)
				if err != nil {
					in.Consumed, in.Offset = i, uint64(i)
					in.Err = err

					return token.None
				}
				i = next
			default:
				in.Consumed, in.Offset = i, uint64(i)
				in.Err = codes.ErrUnknownEscape

				return token.None
			}

		case c < controlCharsUpperBound:
			in.Consumed, in.Offset = i, uint64(i)
			in.Err = codes.ErrControlChar

			return token.None

		default:
			// skip the clean run to the next stop (quote/escape/control) without copying — adaptive scalar probe then SWAR
			// (see consumeStringEscaped).
			run := i
			stop := i + 1
			hi := uint64(c) // data[i], the byte that entered this run
			probe := min(stop+swarProbe, n)
			for ; stop < probe; stop++ {
				b := data[stop]
				if b == doubleQuote || b == escape || b < controlCharsUpperBound {
					break
				}
				hi |= uint64(b)
			}
			if stop == probe &&
				stop < n { // outran the scalar probe → SWAR, guess long past guessLong
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
					if stop-run >= guessLong && !in.NoAVX2 {
						delta, runNonASCII := strscan.ScanStop(data[stop:n])
						stop += delta
						if runNonASCII {
							hi |= swar.HighBits
						}

						break
					}
				}
				for ; stop < n; stop++ {
					b := data[stop]
					if b == doubleQuote || b == escape ||
						b < controlCharsUpperBound {
						break
					}
					hi |= uint64(b)
				}
			}
			nonASCII = nonASCII || hi&swar.HighBits != 0
			i = stop
		}
	}

	in.Consumed, in.Offset = i, uint64(i)
	in.Err = codes.ErrUnterminatedString

	return token.None
}

// consumeStringRawStreamFast is the raw streaming string fast path (§10.5c): the raw analogue of
// consumeStringStreamFast.
//
// It treats the current window like whole-buffer mode — SWAR/AVX2-scans for the closing quote / escape / control —
// and, when a clean string completes inside the window, ALIASES l.in.Buffer zero-copy (a clean raw string IS its own
// value, no copy into currentValue), the common case.
// It hands off to the byte-by-byte consumeStringRawStreaming only on an escape or a value that spans a refill.
//
// This is what closes the verbatim reader-mode string gap (VS/VL used the byte-by-byte path unconditionally; L had this
// fast path since Phase 1a).
//
// Like consumeStringStreamFast, reaching l.in.Bufferized is NOT end-of-input (delegate, don't error) and advances are
// RELATIVE (l.in.Offset is the absolute stream offset, l.in.Consumed the window index).
//
//nolint:dupl // the structure is the same but differs subtly and its critical that the inside remains inlined.
func (in *Input) consumeStringRawStreamFast() token.T {
	data := in.Buffer
	n := in.Bufferized
	start := in.Consumed // first content byte (opening quote already consumed)

	i := start
	var hi uint64 // fused non-ASCII accumulator, see consumeStringWhole
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
		// window end reached without a stop byte: the string may span a refill → hand off to the byte-by-byte raw path
		// (in.Consumed still start, re-scans clean).
		return in.consumeStringRawStreaming()
	}

	switch c := data[i]; {
	case c == doubleQuote:
		if in.MaxValueBytes > 0 && i-start > in.MaxValueBytes {
			in.Offset += uint64(i - start)
			in.Consumed = i
			in.Err = codes.ErrMaxValueBytes

			return token.None
		}
		value := data[start:i:i] // alias the window (raw == value; valid until refill)
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

	// an escape was found inside the window: delegate to the byte-by-byte raw path, which keeps escapes verbatim and
	// handles refills (re-scans from in.Consumed==start).
	return in.consumeStringRawStreaming()
}

// consumeStringRawStreaming is the raw scan over a refilling buffer: it copies the source bytes verbatim (escapes
// intact) into l.in.CurrentValue while validating them, so the value survives buffer turnover.
func (in *Input) consumeStringRawStreaming() token.T {
	var nonASCII bool // did any raw source byte carry the high bit (see consumeStringEscaped)
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
			in.Consumed++
			in.Offset++

			switch {
			case b == doubleQuote:
				return in.finishStringValue(in.CurrentValue, nonASCII)

			case b == escape:
				in.CurrentValue = append(in.CurrentValue, escape)
				if err := in.rawEscapeStreaming(); err != nil {
					in.Err = err

					return token.None
				}

			case b < controlCharsUpperBound:
				in.Err = codes.ErrControlChar

				return token.None

			default:
				nonASCII = nonASCII || b >= utf8.RuneSelf
				in.CurrentValue = append(in.CurrentValue, b)

				// bulk-scan the rest of this clean run within the current window (§10.5c, the raw analogue of
				// consumeStringStreaming's Phase-1c copy): a long clean stretch — between escapes, or a clean string that spilled
				// past the fast path's window — is copied in ONE shot (adaptive scalar-probe → SWAR → AVX2) instead of one
				// byte per loop turn.
				//
				// Raw copies verbatim, so there is no decode; a run that reaches the window end stops at bufferized and the outer
				// loop refills for the continuation.
				// This is what closes gsoc-style escaped-long-string reader mode for VS/VL.
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
				if stop == probe && stop < n { // run outran the scalar probe → SWAR/AVX2
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

// rawEscapeStreaming validates the escape sequence following a backslash (already appended) in the streaming raw scan,
// appending its raw bytes to l.in.CurrentValue.
func (in *Input) rawEscapeStreaming() error {
	var one [1]byte
	if err := in.consumeN(one[:]); err != nil {
		return codes.ErrUnterminatedString
	}
	e := one[0]
	in.CurrentValue = append(in.CurrentValue, e)

	switch e {
	case doubleQuote, escape, slash, 'b', 'f', 'n', 'r', 't':
		return nil
	case 'u':
		return in.rawUnicodeStreaming()
	default:
		return codes.ErrUnknownEscape
	}
}

// rawUnicodeStreaming validates a \uXXXX sequence (and a following \uYYYY low surrogate for a high surrogate) in the
// streaming raw scan, appending the raw hex bytes verbatim.
//
// The leading "\u" has already been appended.
func (in *Input) rawUnicodeStreaming() error {
	start := len(in.CurrentValue)
	digits, r, err := in.readHex4()
	if err != nil {
		return err
	}
	in.CurrentValue = append(in.CurrentValue, digits[:]...)

	if !utf16.IsSurrogate(r) {
		return nil
	}

	// a high surrogate must be completed by \uXXXX; peek so an escape that does not complete it is left to be
	// validated on its own (see [Input.peekLowSurrogate]).
	if low, ok := in.peekLowSurrogate(); ok {
		if utf16.DecodeRune(r, low) != utf8.RuneError {
			in.CurrentValue = append(
				in.CurrentValue,
				in.Buffer[in.Consumed:in.Consumed+surrogateEscapeLen]...)
			in.takePeeked(surrogateEscapeLen)

			return nil
		}
	}

	if berr := in.brokenSurrogate(); berr != nil {
		in.CurrentValue = in.CurrentValue[:start] // the value is discarded with the error; keep it consistent anyway

		return berr
	}

	// UTF8Replace / UTF8Passthrough: VL keeps the escape as SOURCE TEXT, so nothing is rewritten here. The U+FFFD
	// substitution happens on decode, in token.Unescape, which already yields it for a broken pair (plan §2.5).
	return nil
}

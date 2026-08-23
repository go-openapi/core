// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

//nolint:dupl // these functions are duplicated by codegen to monomorphize (aka devirt).
package lexer

import (
	"io"

	codes "github.com/go-openapi/core/json/lexers/error-codes"
	scan "github.com/go-openapi/core/json/lexers/internal/scan"
	"github.com/go-openapi/core/json/lexers/token"
)

// scanTokenBufferG is the generic, policy-parameterized pull core for WHOLE-BUFFER
// input: it scans and returns exactly one token, dispatched from L.NextToken /
// VL.NextToken when l.in.WholeBuffer is true. It is the yield→return counterpart of
// scanPushG (roadmap §10) — the same proven whole-buffer shape: the cursor is a
// pure local i (no readMore, no per-byte struct write), and the preceding blanks
// are a zero-copy slice of the input (data[blankStart:i:i], as push does) rather
// than the byte-by-byte l.blanks append the streaming core needs. It resumes from
// l.in.Consumed on the next call and only continues its loop past elided separators
// and whitespace; every value/delimiter token returns immediately.
//
// Keeping this separate from scanTokenStreamG is the whole point of the split: the
// register-delicate buffer loop (§9.1, source of the 16/18 corpus wins) is frozen
// here, and stream-refill optimization happens next door without touching it.
//
//nolint:gocognit,gocyclo,maintidx
func scanTokenBufferG[T any, P emitPolicy[T]](l *L, p P) T {
	if l.in.Err != nil {
		return p.none()
	}

	data := l.in.Buffer[:l.in.Bufferized]
	i := l.in.Consumed
	// blankStart is the index right after the previous token: [blankStart:i] is the whitespace run the verbatim policy
	// bakes into the token (zero-copy).
	// It is only advanced past elided separators; every emit resets it via the next call's re-init from l.in.Consumed.
	blankStart := i

	writeback := func(pos int) {
		l.in.Consumed = pos
		l.in.Offset = uint64(pos)
	}

	for i < len(data) {
		b := data[i]

		switch b {
		case lineFeed:
			if !p.tracksPosition() {
				i += scan.ConsumeWhitespace(data[i:]) // semantic batch-skip (citm bottleneck)

				continue
			}
			i++
			l.line++
			l.lineStart = uint64(i)

			continue
		case blank, tab, carriageReturn:
			if !p.tracksPosition() {
				i += scan.ConsumeWhitespace(data[i:])

				continue
			}
			i++

			continue
		}

		if p.tracksPosition() {
			l.tokLine = l.line
			l.tokCol = int(uint64(i+1) - l.lineStart)
		}
		// the whitespace run since the previous token (zero-copy); i is the token start.
		blanks := data[blankStart:i:i]
		if p.storesBlanks() {
			l.blanks = blanks // state-based VL: stash the leading blanks in lexer state
		}

		// verbatim circuit breaker: bound the preceding-whitespace run.
		// The stream core checks this per-byte as it appends; here blanks is a zero-copy slice, so one length check at the
		// token boundary is equivalent (folded away in the semantic core, where tracksPosition() is a false constant).
		if p.tracksPosition() && l.maxValueBytes > 0 && len(blanks) > l.maxValueBytes {
			l.in.Err = codes.ErrMaxValueBytes
			writeback(i)

			return p.none()
		}

		if l.in.AfterKey {
			l.in.AfterKey = false
			if b != colon {
				l.in.Err = codes.ErrKeyColon
				writeback(i + 1)

				return p.none()
			}

			i++
			l.current = token.MakeDelimiter(token.Colon)
			blankStart = i
			if l.elideSeparator {
				continue
			}
			writeback(i)

			return p.emit(l.current, blanks, l.tokLine, l.tokCol)
		}

		switch b {
		case colon:
			if l.current.Kind() == token.String {
				l.in.Err = codes.ErrMissingObject
			} else {
				l.in.Err = codes.ErrMissingKey
			}
			writeback(i + 1)

			return p.none()

		case closingBracket:
			if l.current.IsComma() {
				l.in.Err = codes.ErrTrailingComma
				writeback(i + 1)

				return p.none()
			}
			if l.current.IsColon() { // {"a":}
				l.in.Err = codes.ErrColonValue
				writeback(i + 1)

				return p.none()
			}
			if !l.isInObject() {
				l.in.Err = codes.ErrNotInObject
				writeback(i + 1)

				return p.none()
			}

			i++
			l.in.ExpectKey = false
			l.popContainer()
			l.current = token.MakeDelimiter(token.ClosingBracket)
			writeback(i)

			return p.emit(l.current, blanks, l.tokLine, l.tokCol)

		case closingSquareBracket:
			if l.current.IsComma() {
				l.in.Err = codes.ErrTrailingComma
				writeback(i + 1)

				return p.none()
			}
			if !l.isInArray() {
				l.in.Err = codes.ErrNotInArray
				writeback(i + 1)

				return p.none()
			}

			i++
			l.popContainer()
			l.current = token.MakeDelimiter(token.ClosingSquareBracket)
			writeback(i)

			return p.emit(l.current, blanks, l.tokLine, l.tokCol)

		case comma:
			if l.current.IsComma() {
				l.in.Err = codes.ErrRepeatedComma
				writeback(i + 1)

				return p.none()
			}
			if l.in.ExpectKey {
				l.in.Err = codes.ErrMissingKey
				writeback(i + 1)

				return p.none()
			}
			if !l.isInContainer() {
				l.in.Err = codes.ErrCommaInContainer
				writeback(i + 1)

				return p.none()
			}
			if l.current.IsStartObject() || l.current.IsStartArray() || l.current.IsColon() {
				l.in.Err = codes.ErrMissingValue
				writeback(i + 1)

				return p.none()
			}

			if l.isInObject() {
				l.in.ExpectKey = true
			}

			i++
			l.current = token.MakeDelimiter(token.Comma)
			blankStart = i
			if l.elideSeparator {
				continue
			}
			writeback(i)

			return p.emit(l.current, blanks, l.tokLine, l.tokCol)

		case openingBracket:
			if l.current.IsKnown() {
				if l.current.Kind() != token.Delimiter {
					l.in.Err = codes.ErrInvalidToken
					writeback(i + 1)

					return p.none()
				}
				if l.current.Delimiter().IsClosing() {
					l.in.Err = codes.ErrMissingComma
					writeback(i + 1)

					return p.none()
				}
				if l.isInArray() {
					if l.current.Delimiter() != token.OpeningSquareBracket &&
						l.current.Delimiter() != token.Comma {
						l.in.Err = codes.ErrMissingComma
						writeback(i + 1)

						return p.none()
					}
				} else if !l.current.IsColon() {
					l.in.Err = codes.ErrMissingKey
					writeback(i + 1)

					return p.none()
				}
			}
			if l.in.ExpectKey {
				l.in.Err = codes.ErrMissingKey
				writeback(i + 1)

				return p.none()
			}

			i++
			l.in.ExpectKey = true
			l.pushObject()
			if l.in.Err != nil {
				writeback(i)

				return p.none()
			}
			l.current = token.MakeDelimiter(token.OpeningBracket)
			writeback(i)

			return p.emit(l.current, blanks, l.tokLine, l.tokCol)

		case openingSquareBracket:
			if l.current.IsKnown() {
				if l.current.Kind() != token.Delimiter {
					l.in.Err = codes.ErrInvalidToken
					writeback(i + 1)

					return p.none()
				}
				if l.current.Delimiter().IsClosing() {
					l.in.Err = codes.ErrMissingComma
					writeback(i + 1)

					return p.none()
				}
			}
			if l.in.ExpectKey {
				l.in.Err = codes.ErrMissingKey
				writeback(i + 1)

				return p.none()
			}

			i++
			l.pushArray()
			if l.in.Err != nil {
				writeback(i)

				return p.none()
			}
			l.current = token.MakeDelimiter(token.OpeningSquareBracket)
			writeback(i)

			return p.emit(l.current, blanks, l.tokLine, l.tokCol)

		case doubleQuote:
			if l.current.IsKnown() && !l.current.Delimiter().AcceptValue() {
				l.in.Err = codes.ErrDelimitedValue
				l.current = token.None
				writeback(i + 1)

				return p.none()
			}

			writeback(i + 1)
			l.current = l.in.ConsumeString()
			if l.in.Err != nil {
				return p.none()
			}

			return p.emit(l.current, blanks, l.tokLine, l.tokCol)

		case startOfTrue, startOfFalse:
			if l.current.IsKnown() && !l.current.Delimiter().AcceptValue() {
				l.in.Err = codes.ErrDelimitedValue
				l.current = token.None
				writeback(i + 1)

				return p.none()
			}
			if l.in.ExpectKey {
				l.in.Err = codes.ErrMissingKey
				writeback(i + 1)

				return p.none()
			}

			writeback(i + 1)
			l.current = l.in.ConsumeBoolean(b)
			if l.in.Err != nil {
				return p.none()
			}

			return p.emit(l.current, blanks, l.tokLine, l.tokCol)

		case minusSign, decimalPoint, '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			if l.current.IsKnown() && !l.current.Delimiter().AcceptValue() {
				l.in.Err = codes.ErrDelimitedValue
				l.current = token.None
				writeback(i + 1)

				return p.none()
			}
			if l.in.ExpectKey {
				l.in.Err = codes.ErrMissingKey
				writeback(i + 1)

				return p.none()
			}

			// Bounded values (maxValueBytes) route to the streaming number consumer, which enforces the cap; the inline fast
			// path + consumeNumberWhole below do not check it.
			// In whole-buffer mode this gate reduces to maxValueBytes, so the unbounded common case keeps the full inline fast
			// path (§10 keeps the champion shape; the old combined core gated it the same way).
			if l.maxValueBytes > 0 {
				writeback(i + 1)
				l.current = l.in.ConsumeNumberStreaming(b)
				if l.in.Err != nil {
					return p.none()
				}

				return p.emit(l.current, blanks, l.tokLine, l.tokCol)
			}

			numStart := i
			runFrom := i + 1
			var firstDigit byte
			ok := true

			switch {
			case b >= '0' && b <= '9':
				firstDigit = b
			case b == minusSign:
				if uint(i+1) < uint(len(data)) && data[i+1] >= '0' && data[i+1] <= '9' {
					firstDigit = data[i+1]
					runFrom = i + 1 + 1 // skip
				} else {
					ok = false
				}
			default: // decimalPoint
				ok = false
			}

			if ok {
				n := runFrom
				for uint(n) < uint(len(data)) && '0' <= data[n] && data[n] <= '9' {
					n++
				}

				leadingZero := firstDigit == '0' && n > runFrom
				end := n
				var term byte
				if uint(end) < uint(len(data)) {
					term = data[end]
				}
				// extend over a fractional part ('.'
				// 1*DIGIT); a trailing dot leaves term==decimalPoint for consumeNumberWhole's error.
				if !leadingZero && term == decimalPoint {
					m := end + 1
					for uint(m) < uint(len(data)) && '0' <= data[m] && data[m] <= '9' {
						m++
					}
					if m > end+1 { // at least one fractional digit
						end = m
						term = 0
						if uint(end) < uint(len(data)) {
							term = data[end]
						}
					}
				}
				// extend over an exponent ((e|E) [+|-] 1*DIGIT); cheaper than bailing to consumeNumberWhole and re-scanning.
				// A malformed exponent leaves term=='e'/'E' for the slow-path error.
				if !leadingZero && (term == 'e' || term == 'E') {
					m := end + 1
					if uint(m) < uint(len(data)) && (data[m] == '+' || data[m] == '-') {
						m++
					}
					expStart := m
					for uint(m) < uint(len(data)) && '0' <= data[m] && data[m] <= '9' {
						m++
					}
					if m > expStart { // at least one exponent digit
						end = m
						term = 0
						if uint(end) < uint(len(data)) {
							term = data[end]
						}
					}
				}

				if !leadingZero && term != decimalPoint && term != 'e' && term != 'E' {
					l.current = token.MakeWithValue(token.Number, data[numStart:end:end])
					writeback(end)

					return p.emit(l.current, blanks, l.tokLine, l.tokCol)
				}
			}

			writeback(i + 1)
			l.current = l.in.ConsumeNumberWhole(b)
			if l.in.Err != nil {
				return p.none()
			}

			return p.emit(l.current, blanks, l.tokLine, l.tokCol)

		case startOfNull:
			if l.current.IsKnown() && !l.current.Delimiter().AcceptValue() {
				l.in.Err = codes.ErrDelimitedValue
				l.current = token.None
				writeback(i + 1)

				return p.none()
			}
			if l.in.ExpectKey {
				l.in.Err = codes.ErrMissingKey
				writeback(i + 1)

				return p.none()
			}

			writeback(i + 1)
			l.current = l.in.ConsumeNull(b)
			if l.in.Err != nil {
				return p.none()
			}

			return p.emit(l.current, blanks, l.tokLine, l.tokCol)

		default:
			l.in.Err = codes.ErrInvalidToken
			writeback(i + 1)

			return p.none()
		}
	}

	// buffer exhausted: the trailing whitespace run [blankStart:i] is the verbatim EOF token's preceding blanks
	// (zero-copy); the semantic policy ignores it.
	l.blanks = data[blankStart:i:i]
	writeback(i)

	// verbatim circuit breaker on a trailing whitespace flood with no closing token (parity with the stream core's
	// per-byte check); folded away in the semantic core.
	if p.tracksPosition() && l.maxValueBytes > 0 && len(l.blanks) > l.maxValueBytes {
		l.in.Err = codes.ErrMaxValueBytes

		return p.none()
	}

	return errCheckG(l, p, io.EOF)
}

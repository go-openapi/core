// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

//nolint:dupl // these functions are duplicated by codegen to monomorphize (aka devirt).
package lexer

import (
	codes "github.com/go-openapi/core/json/lexers/error-codes"
	scan "github.com/go-openapi/core/json/lexers/internal/scan"
	"github.com/go-openapi/core/json/lexers/token"
)

// scanTokenStreamG is the generic, policy-parameterized pull core for STREAMING
// input (io.Reader): it scans and returns exactly one token, dispatched from
// L.NextToken / VL.NextToken when l.in.WholeBuffer is false.
//
// The cursor lives in the struct (per-byte advance, readMore for refills, deferred-error semantics) and
// each token is emitted via the policy. When l.trackBlanks is set (verbatim) it
// accumulates the preceding whitespace run into l.blanks (byte-by-byte, so it
// survives streaming refills); the semantic policy ignores blanks.
//
// The whole-buffer lane is scanTokenBufferG (no readMore, local cursor, zero-copy
// blanks). Splitting the two (roadmap §10) lets us optimize the stream refill
// path without perturbing the register-delicate whole-buffer core.
//
// Here l.in.WholeBuffer is always false; values take the streaming fast paths
// (consumeStringStreamFast / consumeNumberStreamFast, §10.3) — optimistic in-window
// scan + zero-copy alias, delegating to the byte-by-byte consumers only on an
// escape or a token that spans a refill.
//
//nolint:gocognit,gocyclo,maintidx
func scanTokenStreamG[T any, P emitPolicy[T]](l *L, p P) T {
	if l.in.Err != nil {
		return p.none()
	}

	if l.trackBlanks {
		l.blanks = l.blanks[:0]
	}

	for {
		if err := l.in.ReadMore(); err != nil {
			return errCheckG(l, p, err)
		}

		for l.in.Consumed < l.in.Bufferized {
			b := l.in.Buffer[l.in.Consumed]
			l.in.Offset++
			l.in.Consumed++

			switch b {
			case lineFeed:
				if !p.tracksPosition() {
					// semantic: batch-skip the rest of the whitespace run with a local cursor (no line/col, no blanks) — folds to
					// the only path in the devirtualized semantic core.
					// This kills the per-byte struct-cursor cost over whitespace (the citm bottleneck).
					ws := scan.ConsumeWhitespace(l.in.Buffer[l.in.Consumed:l.in.Bufferized])
					l.in.Consumed += ws
					l.in.Offset += uint64(ws)

					continue
				}
				// verbatim/state: handle the first byte inline (b is a newline), then batch-skip the REST only if the run
				// actually continues — so a single-byte run stays as cheap as the old per-byte path (no call).
				l.line++
				l.lineStart = l.in.Offset // just past the newline b
				if l.trackBlanks {
					l.blanks = append(l.blanks, b)
				}
				if l.in.Consumed < l.in.Bufferized && scan.IsBlank(l.in.Buffer[l.in.Consumed]) {
					l.skipBlanksRestStream()
				}
				if l.trackBlanks && l.maxValueBytes > 0 && len(l.blanks) > l.maxValueBytes {
					l.in.Err = codes.ErrMaxValueBytes

					return p.none()
				}

				continue

			case blank, tab, carriageReturn:
				if !p.tracksPosition() {
					ws := scan.ConsumeWhitespace(l.in.Buffer[l.in.Consumed:l.in.Bufferized])
					l.in.Consumed += ws
					l.in.Offset += uint64(ws)

					continue
				}
				// verbatim/state (§10.5d): first byte inline; batch-skip the rest only if the run continues (b is not a newline).
				if l.trackBlanks {
					l.blanks = append(l.blanks, b)
				}
				if l.in.Consumed < l.in.Bufferized && scan.IsBlank(l.in.Buffer[l.in.Consumed]) {
					l.skipBlanksRestStream()
				}
				if l.trackBlanks && l.maxValueBytes > 0 && len(l.blanks) > l.maxValueBytes {
					l.in.Err = codes.ErrMaxValueBytes

					return p.none()
				}

				continue
			}

			// a significant byte starts a token: snapshot its position (verbatim only)
			if p.tracksPosition() {
				l.tokLine = l.line
				l.tokCol = int(l.in.Offset - l.lineStart)
			}

			if l.in.AfterKey {
				l.in.AfterKey = false
				if b != colon {
					l.in.Err = codes.ErrKeyColon

					return p.none()
				}

				l.current = token.MakeDelimiter(token.Colon)
				if l.elideSeparator {
					continue
				}

				return p.emit(l.current, l.blanks, l.tokLine, l.tokCol)
			}

			switch b {
			case colon:
				if l.current.Kind() == token.String {
					l.in.Err = codes.ErrMissingObject
				} else {
					l.in.Err = codes.ErrMissingKey
				}

				return p.none()

			case closingBracket:
				if l.current.IsComma() {
					l.in.Err = codes.ErrTrailingComma

					return p.none()
				}
				if l.current.IsColon() { // {"a":}
					l.in.Err = codes.ErrColonValue

					return p.none()
				}
				if !l.isInObject() {
					l.in.Err = codes.ErrNotInObject

					return p.none()
				}

				l.in.ExpectKey = false
				l.popContainer()
				l.current = token.MakeDelimiter(token.ClosingBracket)

				return p.emit(l.current, l.blanks, l.tokLine, l.tokCol)

			case closingSquareBracket:
				if l.current.IsComma() {
					l.in.Err = codes.ErrTrailingComma

					return p.none()
				}
				if !l.isInArray() {
					l.in.Err = codes.ErrNotInArray

					return p.none()
				}

				l.popContainer()
				l.current = token.MakeDelimiter(token.ClosingSquareBracket)

				return p.emit(l.current, l.blanks, l.tokLine, l.tokCol)

			case comma:
				if l.current.IsComma() {
					l.in.Err = codes.ErrRepeatedComma

					return p.none()
				}
				if l.in.ExpectKey {
					l.in.Err = codes.ErrMissingKey

					return p.none()
				}
				if !l.isInContainer() {
					l.in.Err = codes.ErrCommaInContainer

					return p.none()
				}
				if l.current.IsStartObject() || l.current.IsStartArray() || l.current.IsColon() {
					l.in.Err = codes.ErrMissingValue

					return p.none()
				}

				if l.isInObject() {
					l.in.ExpectKey = true
				}

				l.current = token.MakeDelimiter(token.Comma)
				if l.elideSeparator {
					continue
				}

				return p.emit(l.current, l.blanks, l.tokLine, l.tokCol)

			case openingBracket:
				if l.current.IsKnown() {
					if l.current.Kind() != token.Delimiter {
						l.in.Err = codes.ErrInvalidToken

						return p.none()
					}
					if l.current.Delimiter().IsClosing() {
						l.in.Err = codes.ErrMissingComma

						return p.none()
					}
					if l.isInArray() {
						if l.current.Delimiter() != token.OpeningSquareBracket &&
							l.current.Delimiter() != token.Comma {
							l.in.Err = codes.ErrMissingComma

							return p.none()
						}
					} else if !l.current.IsColon() {
						l.in.Err = codes.ErrMissingKey

						return p.none()
					}
				}
				if l.in.ExpectKey {
					l.in.Err = codes.ErrMissingKey

					return p.none()
				}

				l.in.ExpectKey = true
				l.pushObject()
				if l.in.Err != nil {
					return p.none()
				}
				l.current = token.MakeDelimiter(token.OpeningBracket)

				return p.emit(l.current, l.blanks, l.tokLine, l.tokCol)

			case openingSquareBracket:
				if l.current.IsKnown() {
					if l.current.Kind() != token.Delimiter {
						l.in.Err = codes.ErrInvalidToken

						return p.none()
					}
					if l.current.Delimiter().IsClosing() {
						l.in.Err = codes.ErrMissingComma

						return p.none()
					}
				}
				if l.in.ExpectKey {
					l.in.Err = codes.ErrMissingKey

					return p.none()
				}

				l.pushArray()
				if l.in.Err != nil {
					return p.none()
				}
				l.current = token.MakeDelimiter(token.OpeningSquareBracket)

				return p.emit(l.current, l.blanks, l.tokLine, l.tokCol)

			case doubleQuote:
				if l.current.IsKnown() && !l.current.Delimiter().AcceptValue() {
					l.in.Err = codes.ErrDelimitedValue
					l.current = token.None

					return p.none()
				}

				l.current = l.in.ConsumeString()
				if l.in.Err != nil {
					return p.none()
				}

				return p.emit(l.current, l.blanks, l.tokLine, l.tokCol)

			case startOfTrue, startOfFalse:
				if l.current.IsKnown() && !l.current.Delimiter().AcceptValue() {
					l.in.Err = codes.ErrDelimitedValue
					l.current = token.None

					return p.none()
				}
				if l.in.ExpectKey {
					l.in.Err = codes.ErrMissingKey

					return p.none()
				}

				l.current = l.in.ConsumeBoolean(b)
				if l.in.Err != nil {
					return p.none()
				}

				return p.emit(l.current, l.blanks, l.tokLine, l.tokCol)

			case minusSign, decimalPoint, '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
				if l.current.IsKnown() && !l.current.Delimiter().AcceptValue() {
					l.in.Err = codes.ErrDelimitedValue
					l.current = token.None

					return p.none()
				}
				if l.in.ExpectKey {
					l.in.Err = codes.ErrMissingKey

					return p.none()
				}

				l.current = l.in.ConsumeNumberStreamFast(b)
				if l.in.Err != nil {
					return p.none()
				}

				return p.emit(l.current, l.blanks, l.tokLine, l.tokCol)

			case startOfNull:
				if l.current.IsKnown() && !l.current.Delimiter().AcceptValue() {
					l.in.Err = codes.ErrDelimitedValue
					l.current = token.None

					return p.none()
				}
				if l.in.ExpectKey {
					l.in.Err = codes.ErrMissingKey

					return p.none()
				}

				l.current = l.in.ConsumeNull(b)
				if l.in.Err != nil {
					return p.none()
				}

				return p.emit(l.current, l.blanks, l.tokLine, l.tokCol)

			default:
				l.in.Err = codes.ErrInvalidToken

				return p.none()
			}
		}
	}
}

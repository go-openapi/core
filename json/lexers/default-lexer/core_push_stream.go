// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

//nolint:dupl // these functions are duplicated by codegen to monomorphize (aka devirt).
package lexer

import (
	codes "github.com/go-openapi/core/json/lexers/error-codes"
	scan "github.com/go-openapi/core/json/lexers/internal/scan"
	"github.com/go-openapi/core/json/lexers/token"
)

// scanPushStreamG is the generic, policy-parameterized PUSH core for STREAMING input
// (io.Reader): the yield→loop counterpart of scanTokenStreamG (§10.5g). It backs
// Tokens() over a reader, replacing the previous fallthrough to a NextToken loop
// wrapped in a range-over-func closure (iterator.go): that fallthrough paid per-token
// NextToken call overhead PLUS the closure, running BELOW the streaming pull core. Here
// the cursor and scan state stay put across the whole scan and each token is delivered
// inline via yield(...) — the same win the whole-buffer push core scanPushG has over
// its pull twin, now on the streaming lane.
//
// Structurally it is scanTokenStreamG with `return p.emit(...)` replaced by
// `if !yield(p.emit(...)) { return }` and, for the position-tracking policies, an
// l.blanks reset so the next token's preceding-blanks run starts fresh (gated on the
// compile-time tracksPosition() so it folds away in the semantic core). A grammar error
// stops the range (bare return, l.in.Err already set); readMore's EOF is classified by
// errCheckG (no EOF token is yielded, matching scanPushG). Must be reached only through
// the //go:noinline devirt shim so the yield closure stays on the stack.
//
//nolint:gocognit,gocyclo,maintidx,unused // this is used by codegen for devirtualization
func scanPushStreamG[T any, P emitPolicy[T]](l *L, p P, yield func(T) bool) {
	if l.in.Err != nil {
		return
	}

	if l.trackBlanks {
		l.blanks = l.blanks[:0]
	}

	for {
		if err := l.in.ReadMore(); err != nil {
			_ = errCheckG(l, p, err) // classify EOF/error; push yields no EOF token

			return
		}

		for l.in.Consumed < l.in.Bufferized {
			b := l.in.Buffer[l.in.Consumed]
			l.in.Offset++
			l.in.Consumed++

			switch b {
			case lineFeed:
				if !p.tracksPosition() {
					ws := scan.ConsumeWhitespace(l.in.Buffer[l.in.Consumed:l.in.Bufferized])
					l.in.Consumed += ws
					l.in.Offset += uint64(ws)

					continue
				}
				// verbatim/state (§10.5d): first byte inline (newline), batch-skip the rest.
				l.line++
				l.lineStart = l.in.Offset
				if l.trackBlanks {
					l.blanks = append(l.blanks, b)
				}
				if l.in.Consumed < l.in.Bufferized && scan.IsBlank(l.in.Buffer[l.in.Consumed]) {
					l.skipBlanksRestStream()
				}
				if l.trackBlanks && l.maxValueBytes > 0 && len(l.blanks) > l.maxValueBytes {
					l.in.Err = codes.ErrMaxValueBytes

					return
				}

				continue

			case blank, tab, carriageReturn:
				if !p.tracksPosition() {
					ws := scan.ConsumeWhitespace(l.in.Buffer[l.in.Consumed:l.in.Bufferized])
					l.in.Consumed += ws
					l.in.Offset += uint64(ws)

					continue
				}
				if l.trackBlanks {
					l.blanks = append(l.blanks, b)
				}
				if l.in.Consumed < l.in.Bufferized && scan.IsBlank(l.in.Buffer[l.in.Consumed]) {
					l.skipBlanksRestStream()
				}
				if l.trackBlanks && l.maxValueBytes > 0 && len(l.blanks) > l.maxValueBytes {
					l.in.Err = codes.ErrMaxValueBytes

					return
				}

				continue
			}

			if p.tracksPosition() {
				l.tokLine = l.line
				l.tokCol = int(l.in.Offset - l.lineStart)
			}

			if l.in.AfterKey {
				l.in.AfterKey = false
				if b != colon {
					l.in.Err = codes.ErrKeyColon

					return
				}

				l.current = token.MakeDelimiter(token.Colon)
				if l.elideSeparator {
					continue
				}
				if !yield(p.emit(l.current, l.blanks, l.tokLine, l.tokCol)) {
					return
				}
				if p.tracksPosition() {
					l.blanks = l.blanks[:0]
				}

				continue
			}

			switch b {
			case colon:
				if l.current.Kind() == token.String {
					l.in.Err = codes.ErrMissingObject
				} else {
					l.in.Err = codes.ErrMissingKey
				}

				return

			case closingBracket:
				if l.current.IsComma() {
					l.in.Err = codes.ErrTrailingComma

					return
				}
				if l.current.IsColon() { // {"a":}
					l.in.Err = codes.ErrColonValue

					return
				}
				if !l.isInObject() {
					l.in.Err = codes.ErrNotInObject

					return
				}

				l.in.ExpectKey = false
				l.popContainer()
				l.current = token.MakeDelimiter(token.ClosingBracket)
				if !yield(p.emit(l.current, l.blanks, l.tokLine, l.tokCol)) {
					return
				}
				if p.tracksPosition() {
					l.blanks = l.blanks[:0]
				}

			case closingSquareBracket:
				if l.current.IsComma() {
					l.in.Err = codes.ErrTrailingComma

					return
				}
				if !l.isInArray() {
					l.in.Err = codes.ErrNotInArray

					return
				}

				l.popContainer()
				l.current = token.MakeDelimiter(token.ClosingSquareBracket)
				if !yield(p.emit(l.current, l.blanks, l.tokLine, l.tokCol)) {
					return
				}
				if p.tracksPosition() {
					l.blanks = l.blanks[:0]
				}

			case comma:
				if l.current.IsComma() {
					l.in.Err = codes.ErrRepeatedComma

					return
				}
				if l.in.ExpectKey {
					l.in.Err = codes.ErrMissingKey

					return
				}
				if !l.isInContainer() {
					l.in.Err = codes.ErrCommaInContainer

					return
				}
				if l.current.IsStartObject() || l.current.IsStartArray() || l.current.IsColon() {
					l.in.Err = codes.ErrMissingValue

					return
				}

				if l.isInObject() {
					l.in.ExpectKey = true
				}

				l.current = token.MakeDelimiter(token.Comma)
				if l.elideSeparator {
					continue
				}
				if !yield(p.emit(l.current, l.blanks, l.tokLine, l.tokCol)) {
					return
				}
				if p.tracksPosition() {
					l.blanks = l.blanks[:0]
				}

			case openingBracket:
				if l.current.IsKnown() {
					if l.current.Kind() != token.Delimiter {
						l.in.Err = codes.ErrInvalidToken

						return
					}
					if l.current.Delimiter().IsClosing() {
						l.in.Err = codes.ErrMissingComma

						return
					}
					if l.isInArray() {
						if l.current.Delimiter() != token.OpeningSquareBracket &&
							l.current.Delimiter() != token.Comma {
							l.in.Err = codes.ErrMissingComma

							return
						}
					} else if !l.current.IsColon() {
						l.in.Err = codes.ErrMissingKey

						return
					}
				}
				if l.in.ExpectKey {
					l.in.Err = codes.ErrMissingKey

					return
				}

				l.in.ExpectKey = true
				l.pushObject()
				if l.in.Err != nil {
					return
				}
				l.current = token.MakeDelimiter(token.OpeningBracket)
				if !yield(p.emit(l.current, l.blanks, l.tokLine, l.tokCol)) {
					return
				}
				if p.tracksPosition() {
					l.blanks = l.blanks[:0]
				}

			case openingSquareBracket:
				if l.current.IsKnown() {
					if l.current.Kind() != token.Delimiter {
						l.in.Err = codes.ErrInvalidToken

						return
					}
					if l.current.Delimiter().IsClosing() {
						l.in.Err = codes.ErrMissingComma

						return
					}
				}
				if l.in.ExpectKey {
					l.in.Err = codes.ErrMissingKey

					return
				}

				l.pushArray()
				if l.in.Err != nil {
					return
				}
				l.current = token.MakeDelimiter(token.OpeningSquareBracket)
				if !yield(p.emit(l.current, l.blanks, l.tokLine, l.tokCol)) {
					return
				}
				if p.tracksPosition() {
					l.blanks = l.blanks[:0]
				}

			case doubleQuote:
				if l.current.IsKnown() && !l.current.Delimiter().AcceptValue() {
					l.in.Err = codes.ErrDelimitedValue
					l.current = token.None

					return
				}

				l.current = l.in.ConsumeString()
				if l.in.Err != nil {
					return
				}
				if !yield(p.emit(l.current, l.blanks, l.tokLine, l.tokCol)) {
					return
				}
				if p.tracksPosition() {
					l.blanks = l.blanks[:0]
				}

			case startOfTrue, startOfFalse:
				if l.current.IsKnown() && !l.current.Delimiter().AcceptValue() {
					l.in.Err = codes.ErrDelimitedValue
					l.current = token.None

					return
				}
				if l.in.ExpectKey {
					l.in.Err = codes.ErrMissingKey

					return
				}

				l.current = l.in.ConsumeBoolean(b)
				if l.in.Err != nil {
					return
				}
				if !yield(p.emit(l.current, l.blanks, l.tokLine, l.tokCol)) {
					return
				}
				if p.tracksPosition() {
					l.blanks = l.blanks[:0]
				}

			case minusSign, decimalPoint, '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
				if l.current.IsKnown() && !l.current.Delimiter().AcceptValue() {
					l.in.Err = codes.ErrDelimitedValue
					l.current = token.None

					return
				}
				if l.in.ExpectKey {
					l.in.Err = codes.ErrMissingKey

					return
				}

				l.current = l.in.ConsumeNumberStreamFast(b)
				if l.in.Err != nil {
					return
				}
				if !yield(p.emit(l.current, l.blanks, l.tokLine, l.tokCol)) {
					return
				}
				if p.tracksPosition() {
					l.blanks = l.blanks[:0]
				}

			case startOfNull:
				if l.current.IsKnown() && !l.current.Delimiter().AcceptValue() {
					l.in.Err = codes.ErrDelimitedValue
					l.current = token.None

					return
				}
				if l.in.ExpectKey {
					l.in.Err = codes.ErrMissingKey

					return
				}

				l.current = l.in.ConsumeNull(b)
				if l.in.Err != nil {
					return
				}
				if !yield(p.emit(l.current, l.blanks, l.tokLine, l.tokCol)) {
					return
				}
				if p.tracksPosition() {
					l.blanks = l.blanks[:0]
				}

			default:
				l.in.Err = codes.ErrInvalidToken

				return
			}
		}
	}
}

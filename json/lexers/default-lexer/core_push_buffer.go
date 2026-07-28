//nolint:dupl // these functions are duplicated by codegen to monomorphize (aka devirt).
package lexer

import (
	"io"

	codes "github.com/go-openapi/core/json/lexers/error-codes"
	scan "github.com/go-openapi/core/json/lexers/internal/scan"
	"github.com/go-openapi/core/json/lexers/token"
)

// scanPushG is the generic, policy-parameterized push core backing Tokens() for
// both L and VL in whole-buffer mode. The per-byte hot loop keeps the cursor in
// a local; each token is emitted via `yield(p.emit(...))`, with the token type
// the type parameter T. lexgen monomorphizes it into scanPush{Semantic,Verbatim}Core
// (scan_gen.go) so the policy calls devirtualize; the generic form here stays the
// single source of truth (and the A/B baseline, driven via the *Generic test helpers).
//
//nolint:gocognit,gocyclo,maintidx
func scanPushG[T any, P emitPolicy[T]](l *L, p P, yield func(T) bool) {
	if l.in.Err != nil {
		return
	}

	data := l.in.Buffer[:l.in.Bufferized]
	i := l.in.Consumed
	// blankStart is the index right after the previous token: the whitespace run [blankStart:tokenStart] is the preceding
	// blanks the verbatim policy bakes into the token (zero-copy slice of the input).
	// The semantic policy ignores it.
	// It is reset to i after each emitted token.
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
				// semantic: batch-skip the whole whitespace run in one shot (the citm/indentation bottleneck), mirroring the pull
				// core.
				// Gated by the compile-time tracksPosition() constant so the verbatim core, which must walk each blank to
				// accumulate the preceding-blanks slice and count lines, keeps its per-byte path below.
				//
				// Register- safe: the semantic core has headroom since it dropped line/col.
				i += scan.ConsumeWhitespace(data[i:])

				continue
			}
			i++
			l.line++
			l.lineStart = uint64(i)

			continue
		case blank, tab, carriageReturn:
			if !p.tracksPosition() {
				i += scan.ConsumeWhitespace(data[i:]) // semantic batch-skip; see lineFeed

				continue
			}
			i++

			continue
		}

		if p.tracksPosition() {
			l.tokLine = l.line
			l.tokCol = int(uint64(i+1) - l.lineStart)
		}
		// the whitespace run since the previous token (zero-copy); i is the index of the first significant byte (the token
		// start).
		blanks := data[blankStart:i:i]
		if p.storesBlanks() {
			l.blanks = blanks // state-based VL: stash the leading blanks in lexer state
		}

		if l.in.AfterKey {
			l.in.AfterKey = false
			if b != colon {
				l.in.Err = codes.ErrKeyColon
				writeback(i + 1)

				return
			}

			i++
			l.current = token.MakeDelimiter(token.Colon)
			blankStart = i
			if l.elideSeparator {
				continue
			}
			if !yield(p.emit(l.current, blanks, l.tokLine, l.tokCol)) {
				writeback(i)

				return
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
			writeback(i + 1)

			return

		case closingBracket:
			if l.current.IsComma() {
				l.in.Err = codes.ErrTrailingComma
				writeback(i + 1)

				return
			}
			if l.current.IsColon() { // {"a":}
				l.in.Err = codes.ErrColonValue
				writeback(i + 1)

				return
			}
			if !l.isInObject() {
				l.in.Err = codes.ErrNotInObject
				writeback(i + 1)

				return
			}

			i++
			l.in.ExpectKey = false
			l.popContainer()
			l.current = token.MakeDelimiter(token.ClosingBracket)
			if !yield(p.emit(l.current, blanks, l.tokLine, l.tokCol)) {
				writeback(i)

				return
			}

		case closingSquareBracket:
			if l.current.IsComma() {
				l.in.Err = codes.ErrTrailingComma
				writeback(i + 1)

				return
			}
			if !l.isInArray() {
				l.in.Err = codes.ErrNotInArray
				writeback(i + 1)

				return
			}

			i++
			l.popContainer()
			l.current = token.MakeDelimiter(token.ClosingSquareBracket)
			if !yield(p.emit(l.current, blanks, l.tokLine, l.tokCol)) {
				writeback(i)

				return
			}

		case comma:
			if l.current.IsComma() {
				l.in.Err = codes.ErrRepeatedComma
				writeback(i + 1)

				return
			}
			if l.in.ExpectKey {
				l.in.Err = codes.ErrMissingKey
				writeback(i + 1)

				return
			}
			if !l.isInContainer() {
				l.in.Err = codes.ErrCommaInContainer
				writeback(i + 1)

				return
			}
			if l.current.IsStartObject() || l.current.IsStartArray() || l.current.IsColon() {
				l.in.Err = codes.ErrMissingValue
				writeback(i + 1)

				return
			}

			if l.isInObject() {
				l.in.ExpectKey = true
			}

			i++
			l.current = token.MakeDelimiter(token.Comma)
			if l.elideSeparator {
				continue
			}
			if !yield(p.emit(l.current, blanks, l.tokLine, l.tokCol)) {
				writeback(i)

				return
			}

		case openingBracket:
			if l.current.IsKnown() {
				if l.current.Kind() != token.Delimiter {
					l.in.Err = codes.ErrInvalidToken
					writeback(i + 1)

					return
				}
				if l.current.Delimiter().IsClosing() {
					l.in.Err = codes.ErrMissingComma
					writeback(i + 1)

					return
				}
				if l.isInArray() {
					if l.current.Delimiter() != token.OpeningSquareBracket &&
						l.current.Delimiter() != token.Comma {
						l.in.Err = codes.ErrMissingComma
						writeback(i + 1)

						return
					}
				} else if !l.current.IsColon() {
					l.in.Err = codes.ErrMissingKey
					writeback(i + 1)

					return
				}
			}
			if l.in.ExpectKey {
				l.in.Err = codes.ErrMissingKey
				writeback(i + 1)

				return
			}

			i++
			l.in.ExpectKey = true
			l.pushObject()
			if l.in.Err != nil {
				writeback(i)

				return
			}
			l.current = token.MakeDelimiter(token.OpeningBracket)
			if !yield(p.emit(l.current, blanks, l.tokLine, l.tokCol)) {
				writeback(i)

				return
			}

		case openingSquareBracket:
			if l.current.IsKnown() {
				if l.current.Kind() != token.Delimiter {
					l.in.Err = codes.ErrInvalidToken
					writeback(i + 1)

					return
				}
				if l.current.Delimiter().IsClosing() {
					l.in.Err = codes.ErrMissingComma
					writeback(i + 1)

					return
				}
			}
			if l.in.ExpectKey {
				l.in.Err = codes.ErrMissingKey
				writeback(i + 1)

				return
			}

			i++
			l.pushArray()
			if l.in.Err != nil {
				writeback(i)

				return
			}
			l.current = token.MakeDelimiter(token.OpeningSquareBracket)
			if !yield(p.emit(l.current, blanks, l.tokLine, l.tokCol)) {
				writeback(i)

				return
			}

		case doubleQuote:
			if l.current.IsKnown() && !l.current.Delimiter().AcceptValue() {
				l.in.Err = codes.ErrDelimitedValue
				l.current = token.None
				writeback(i + 1)

				return
			}

			writeback(i + 1)
			l.current = l.in.ConsumeString()
			if l.in.Err != nil {
				return
			}
			i = l.in.Consumed
			if !yield(p.emit(l.current, blanks, l.tokLine, l.tokCol)) {
				return
			}

		case startOfTrue, startOfFalse:
			if l.current.IsKnown() && !l.current.Delimiter().AcceptValue() {
				l.in.Err = codes.ErrDelimitedValue
				l.current = token.None
				writeback(i + 1)

				return
			}
			if l.in.ExpectKey {
				l.in.Err = codes.ErrMissingKey
				writeback(i + 1)

				return
			}

			writeback(i + 1)
			l.current = l.in.ConsumeBoolean(b)
			if l.in.Err != nil {
				return
			}
			i = l.in.Consumed
			if !yield(p.emit(l.current, blanks, l.tokLine, l.tokCol)) {
				return
			}

		case minusSign, decimalPoint, '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			if l.current.IsKnown() && !l.current.Delimiter().AcceptValue() {
				l.in.Err = codes.ErrDelimitedValue
				l.current = token.None
				writeback(i + 1)

				return
			}
			if l.in.ExpectKey {
				l.in.Err = codes.ErrMissingKey
				writeback(i + 1)

				return
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
				// extend the fast path over a fractional part ('.'
				// 1*DIGIT).
				// A trailing dot (no fraction digit) leaves term==decimalPoint, which the final guard routes to consumeNumberWhole
				// for the right error.
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
				// extend over an exponent ((e|E) [+|-] 1*DIGIT).
				// Completing it inline is cheaper than bailing to consumeNumberWhole, which would re-scan the int+frac we already
				// consumed.
				// A malformed exponent leaves term=='e'/'E' and routes to the slow path for the right error.
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
					i = end
					writeback(end)
					blankStart = i // this path continues, bypassing the loop-bottom update
					if !yield(p.emit(l.current, blanks, l.tokLine, l.tokCol)) {
						return
					}

					continue
				}
			}

			writeback(i + 1)
			l.current = l.in.ConsumeNumberWhole(b)
			if l.in.Err != nil {
				return
			}
			i = l.in.Consumed
			if !yield(p.emit(l.current, blanks, l.tokLine, l.tokCol)) {
				return
			}

		case startOfNull:
			if l.current.IsKnown() && !l.current.Delimiter().AcceptValue() {
				l.in.Err = codes.ErrDelimitedValue
				l.current = token.None
				writeback(i + 1)

				return
			}
			if l.in.ExpectKey {
				l.in.Err = codes.ErrMissingKey
				writeback(i + 1)

				return
			}

			writeback(i + 1)
			l.current = l.in.ConsumeNull(b)
			if l.in.Err != nil {
				return
			}
			i = l.in.Consumed
			if !yield(p.emit(l.current, blanks, l.tokLine, l.tokCol)) {
				return
			}

		default:
			l.in.Err = codes.ErrInvalidToken
			writeback(i + 1)

			return
		}

		// the token just processed ends at i: the next blanks run starts here. (the afterKey colon path updates blankStart
		// itself before its continue.)
		blankStart = i
	}

	writeback(i)
	// classify the terminal EOF for its side effects on l.in.Err/l.isAtEOF; the push core yields no token here, so the
	// returned EOF token is discarded.
	_ = errCheckG(l, p, io.EOF)
}

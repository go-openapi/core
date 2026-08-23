// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package input

import (
	"errors"
	"io"

	codes "github.com/go-openapi/core/json/lexers/error-codes"
	"github.com/go-openapi/core/json/lexers/token"
)

// ConsumeNumberWhole scans a JSON number in whole-buffer mode using tight digit-run loops, mirroring jsontext's
// ConsumeNumberResumable: the grammar is validated only at the transitions between runs (sign, integer, fraction,
// exponent), not on every byte.
//
// The hot loops use uint() index comparisons so the bounds check is elided, and the value aliases the input buffer.
//
// In whole-buffer mode there are no refills, so l.in.Offset == l.in.Consumed throughout; both are written back once
// from the local cursor n.
//
// This is the authoritative whole-buffer number scanner, but it is no longer on the hot path: the inline fast path in
// scanToken/scanPush now consumes every WELL-FORMED number — integer, fractional and exponent (see generic.go) — so
// it reaches this fallback only when that fast path conservatively bails (a leading-zero form, a trailing dot, a
// malformed exponent, or an ambiguous prefix).
//
// It is kept complete — NOT reduced to an error reporter — precisely because some bails still resolve to a VALID
// shorter value: like the rest of the folded-look-ahead design, a malformed number may be surfaced as a shorter valid
// value with the error deferred to the next token (e.g. "1.2.3" -> "1.2" then a rejected ".3"); the document is still
// rejected.
func (in *Input) ConsumeNumberWhole(start byte) token.T {
	buf := in.Buffer[:in.Bufferized]
	// Index and length are unsigned locals.
	// The unsigned compare `n < lbuf` is the bounds-check-elimination idiom: it folds the n>=0 check into one comparison,
	// so the compiler drops the bounds check on every buf[n] below.
	//
	// Keeping n unsigned avoids re-casting it at each comparison (in.Consumed et ain. stay int — they are sliced and
	// arithmetic'd as int across the whole lexer; converting them all would ripple far past this hot loop for no gain).
	// buf is never re-sliced, so lbuf is hoisted once.
	lbuf := uint(len(buf))
	numStart := uint(in.Consumed) - 1 // in.Consumed >= 1 here: the start byte was consumed
	n := uint(in.Consumed)            // index just past start

	fail := func(code error) token.T {
		in.Consumed = int(n)
		in.Offset = uint64(n)
		in.Err = code

		return token.None
	}

	// integer part: optional '-', then '0' alone or [1-9][0-9]*
	if start == minusSign {
		if n >= lbuf {
			return fail(codes.ErrMissingInteger)
		}
		start = buf[n]
		n++
	}

	switch {
	case start == '0':
		// a leading zero is only valid as the lone integer digit "0"
		if n < lbuf && buf[n] >= '0' && buf[n] <= '9' {
			return fail(codes.ErrLeadingZero)
		}
	case start >= '1' && start <= '9':
		for n < lbuf && buf[n] >= '0' && buf[n] <= '9' {
			n++
		}
	default: // start is '.' (or otherwise not a digit): missing integer part
		return fail(codes.ErrMissingInteger)
	}

	// fractional part: '.'
	// 1*digit.
	if n < lbuf && buf[n] == decimalPoint {
		n++
		if n >= lbuf || buf[n] < '0' || buf[n] > '9' {
			return fail(codes.ErrInvalidFractional)
		}
		for n < lbuf && buf[n] >= '0' && buf[n] <= '9' {
			n++
		}
	}

	// exponent part: ('e'|'E') ['+'|'-'] 1*digit
	if n < lbuf && (buf[n] == 'e' || buf[n] == 'E') {
		n++
		if n < lbuf && (buf[n] == '+' || buf[n] == '-') {
			n++
		}
		if n >= lbuf || buf[n] < '0' || buf[n] > '9' {
			return fail(codes.ErrInvalidExponent)
		}
		for n < lbuf && buf[n] >= '0' && buf[n] <= '9' {
			n++
		}
	}

	// n stops at the terminator (or end of input); it is left unconsumed, so the next scan validates it via the standard
	// start-of-token checks.
	in.Consumed = int(n)
	in.Offset = uint64(n)

	return token.MakeWithValue(token.Number, buf[numStart:n:n])
}

// ConsumeNumberStreamFast is the streaming number fast path (§10.3 Phase 1b), mirror of consumeStringStreamFast.
//
// It runs the whole-buffer inline number scan over the CURRENT window l.in.Buffer[:l.in.Bufferized]: when the number's
// terminator is visible inside the window, the value is complete and ALIASES l.in.Buffer zero-copy.
//
// It delegates to the byte-by-byte ConsumeNumberStreaming only when it cannot decide in-window — the scan reaches the
// window end (the number may continue past a refill), the fast path bails (leading zero / trailing dot / malformed
// exponent / ambiguous prefix), or a value cap is active (the streaming path enforces it).
//
// Like the string fast path, advances are RELATIVE (streaming l.in.Offset is absolute, l.in.Consumed is the window
// index), and l.in.Consumed/l.in.Offset are left untouched until the alias succeeds, so a delegate re-scans cleanly
// from the number's start.
//
// start is the previously consumed byte that decided to parse a number.
func (in *Input) ConsumeNumberStreamFast(start byte) token.T {
	if in.MaxValueBytes > 0 {
		return in.ConsumeNumberStreaming(start)
	}

	buf := in.Buffer
	n := in.Bufferized
	numStart := in.Consumed - 1 // in.Consumed >= 1 here: the start byte was consumed
	runFrom := in.Consumed
	var firstDigit byte
	ok := true

	switch {
	case start >= '0' && start <= '9':
		firstDigit = start
	case start == minusSign:
		if uint(in.Consumed) < uint(n) && buf[in.Consumed] >= '0' && buf[in.Consumed] <= '9' {
			firstDigit = buf[in.Consumed]
			runFrom = in.Consumed + 1
		} else {
			ok = false
		}
	default: // decimalPoint
		ok = false
	}

	if ok {
		m := runFrom
		for uint(m) < uint(n) && '0' <= buf[m] && buf[m] <= '9' {
			m++
		}

		leadingZero := firstDigit == '0' && m > runFrom
		end := m
		termIn := uint(end) < uint(n) // is the terminator byte inside the window?
		var term byte
		if termIn {
			term = buf[end]
		}
		// fractional part ('.'
		// 1*DIGIT)
		if !leadingZero && term == decimalPoint {
			k := end + 1
			for uint(k) < uint(n) && '0' <= buf[k] && buf[k] <= '9' {
				k++
			}
			if k > end+1 { // at least one fractional digit
				end = k
				termIn = uint(end) < uint(n)
				term = 0
				if termIn {
					term = buf[end]
				}
			}
		}
		// exponent part ((e|E) [+|-] 1*DIGIT)
		if !leadingZero && (term == 'e' || term == 'E') {
			k := end + 1
			if uint(k) < uint(n) && (buf[k] == '+' || buf[k] == '-') {
				k++
			}
			expStart := k
			for uint(k) < uint(n) && '0' <= buf[k] && buf[k] <= '9' {
				k++
			}
			if k > expStart { // at least one exponent digit
				end = k
				termIn = uint(end) < uint(n)
				term = 0
				if termIn {
					term = buf[end]
				}
			}
		}

		// Alias ONLY when the terminator is visible in the window: otherwise the number may continue in the next read (end ==
		// bufferized is NOT EOF here, unlike whole-buffer mode), so we cannot know it is complete.
		if termIn && !leadingZero && term != decimalPoint && term != 'e' && term != 'E' {
			value := buf[numStart:end:end] // alias the window (valid until next refill)
			in.Offset += uint64(end - in.Consumed)
			in.Consumed = end

			return token.MakeWithValue(token.Number, value)
		}
	}

	// spans the window, or a bail form (leading zero / trailing dot / malformed exponent / ambiguous): hand off to the
	// byte-by-byte path, which refills and re-scans from the number's start (in.Consumed is unchanged).
	return in.ConsumeNumberStreaming(start)
}

// ConsumeNumberStreaming consumes a JSON number byte-by-byte.
//
// It is the general path used for streaming input (refillable buffer) and when a value-size cap is active; the
// whole-buffer fast paths handle the common bytes-mode case.
//
// start is the previously consumed byte that decided to parse a number.
//
//nolint:gocyclo,maintidx
func (in *Input) ConsumeNumberStreaming(start byte) token.T {
	var (
		isExponent     bool
		exponentSign   bool
		hasLeadingZero bool
		hasFractional  bool
		isFractional   bool
		integerPart    int
		fractionalPart int
		exponentPart   int
	)

	// The number is scanned without copying byte-by-byte: numStart marks the start of the pending segment in in.Buffer.
	// In whole-buffer mode the value aliases the input; otherwise the pending segment is bulk-copied into currentValue
	// (once at the end, or flushed when a streaming buffer is refilled mid-number).
	// This keeps the hot loop free of per-byte branches.
	numStart := in.Consumed - 1
	in.CurrentValue = in.CurrentValue[:0]

	switch {
	case start == decimalPoint:
		hasFractional = true
		isFractional = hasFractional
	case start == '0':
		hasLeadingZero = true
		integerPart++
	case start >= '1' && start <= '9':
		integerPart++
	}
	start = 0

NUMBER:
	for {
		for in.Consumed < in.Bufferized {

			if in.MaxValueBytes > 0 && len(in.CurrentValue)+in.Consumed-numStart > in.MaxValueBytes {
				in.Err = codes.ErrMaxValueBytes

				return token.None
			}

			b := in.Buffer[in.Consumed]
			in.Consumed++
			in.Offset++

			switch {
			case b == decimalPoint:
				if hasFractional || isExponent {
					// only 1 decimal separator allowed, exponent is integer
					in.Err = codes.ErrRepeatedDecimalSeparator

					return token.None
				}

				hasFractional = true
				isFractional = true

			case b == '+' || b == '-':
				if !isExponent || exponentPart > 0 || exponentSign {
					// a sign is only valid right after the exponent marker, before any exponent digit and only once.
					in.Err = codes.ErrInvalidSign

					return token.None
				}
				exponentSign = true

			case b == 'e' || b == 'E':
				if isExponent {
					in.Err = codes.ErrRepeatedExponent

					return token.None
				}

				isExponent = true
				isFractional = false

			case b == '0':
				if hasLeadingZero && !isFractional && !isExponent {
					// no leading zeroes on integer part, unless this is just 0
					in.Err = codes.ErrLeadingZero

					return token.None
				}

				switch {
				case isFractional:
					fractionalPart++
				case isExponent:
					exponentPart++
				default:
					integerPart++
					if integerPart == 1 {
						hasLeadingZero = true
					}
				}

			case b >= '1' && b <= '9':
				if hasLeadingZero && !isFractional && !isExponent {
					in.Err = codes.ErrLeadingZero

					return token.None
				}

				switch {
				case isFractional:
					fractionalPart++
				case isExponent:
					exponentPart++
				default:
					integerPart++
				}

			default:
				if b == 0 {
					in.Err = codes.ErrInvalidToken

					return token.None
				}

				start = b

				break NUMBER
			}
		}

		// buffer exhausted mid-number: in streaming mode preserve the pending segment before ReadMore overwrites it.
		if !in.WholeBuffer {
			in.CurrentValue = append(in.CurrentValue, in.Buffer[numStart:in.Consumed]...)
			numStart = in.Consumed
		}

		if err := in.ReadMore(); err != nil {
			if errors.Is(err, io.EOF) {
				break NUMBER
			}

			in.Err = err

			return token.None
		}

		numStart = 0 // the buffer was refilled: the pending segment restarts at 0
	}

	if hasFractional && fractionalPart == 0 {
		// a decimal point must be followed by at least one fractional digit
		in.Err = codes.ErrInvalidFractional
		return token.None
	}

	if isExponent && exponentPart == 0 {
		in.Err = codes.ErrInvalidExponent

		return token.None
	}

	if hasLeadingZero && integerPart > 1 {
		in.Err = codes.ErrLeadingZero

		return token.None
	}

	if integerPart == 0 {
		in.Err = codes.ErrMissingInteger

		return token.None
	}

	// a terminator byte (start != 0) was consumed past the number; EOF (start == 0) was not
	numEnd := in.Consumed
	if start != 0 {
		// un-consume the terminator: with the look-ahead folded out, the next scan validates it via the standard
		// start-of-token checks.
		numEnd--
		in.Consumed = numEnd
		in.Offset--
	}

	var value []byte
	if in.WholeBuffer {
		// alias the contiguous number bytes in the input buffer (cap == len)
		value = in.Buffer[numStart:numEnd:numEnd]
	} else {
		// bulk-copy the final pending segment after any earlier flushed segments
		in.CurrentValue = append(in.CurrentValue, in.Buffer[numStart:numEnd]...)
		value = in.CurrentValue
	}

	return token.MakeWithValue(token.Number, value)
}

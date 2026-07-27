//nolint:dupl // writegen lifts commonWriter verbatim onto each concrete writer; the triplication is by design.
package writer

import (
	"encoding"
	"errors"
	"fmt"
	"io"
	"math/big"
	"unicode/utf8"
	"unsafe"

	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/core/json/stores/values"
	"github.com/go-openapi/core/json/types"
	"github.com/go-openapi/swag/conv"
)

type wrt interface {
	writeSingleByte(byte)
	writeBinary([]byte)
	writeEscaped([]byte) []byte
	// Ok is the cheap, inlinable hot-path status check (w.err == nil), used for the
	// per-operation guards instead of the value-receiver Err which copies and wraps.
	Ok() bool
	SetErr(error)
}

type baseWriter struct {
	w       io.Writer
	written int64
	err     error
}

// Ok tells the status of the writer.
func (w *baseWriter) Ok() bool {
	return w.err == nil
}

// Err yields the current error status of the writer.
func (w baseWriter) Err() error {
	if w.err != nil {
		return errors.Join(w.err, ErrDefaultWriter)
	}

	return nil
}

// SetErr injects an error into the writer.
//
// Whenever an error is injected, [W] short-circuits all operations.
func (w *baseWriter) SetErr(err error) {
	w.err = err
}

// Reset the writer, which may be thus recycled.
func (w *baseWriter) Reset() {
	w.err = nil
	w.written = 0
}

// Size returns the bytes that have been written so far.
func (w *baseWriter) Size() int64 {
	return w.written
}

func (w *baseWriter) inc(n int) {
	w.written += int64(n)
}

// commonWriter implements most methods for writers based on
// a few primitive methods defined by the [wrt] interface.
//
// Wrapping a commonWriter is roughly just as fast as repeating code in each writer (only about 5% slower).
type commonWriter[T wrt] struct {
	jw T
}

// Comma writes a comma separator, ','.
func (w *commonWriter[T]) Comma() {
	w.jw.writeSingleByte(comma)
}

// Colon writes a colon separator, ':'.
func (w *commonWriter[T]) Colon() {
	w.jw.writeSingleByte(colon)
}

// EndArray writes an end-of-array separator, i.e. ']'.
func (w *commonWriter[T]) EndArray() {
	w.jw.writeSingleByte(closingSquareBracket)
}

// EndObject writes an end-of-object separator, i.e. '}'.
func (w *commonWriter[T]) EndObject() {
	w.jw.writeSingleByte(closingBracket)
}

// StartArray writes a start-of-array separator, i.e. '['.
func (w *commonWriter[T]) StartArray() {
	w.jw.writeSingleByte(openingSquareBracket)
}

// StartObject writes a start-of-object separator, i.e. '{'.
func (w *commonWriter[T]) StartObject() {
	w.jw.writeSingleByte(openingBracket)
}

// Bool writes a boolean value as JSON.
func (w *commonWriter[T]) Bool(v bool) {
	if !w.jw.Ok() {
		return
	}

	if v {
		w.jw.writeBinary(trueBytes)

		return
	}

	w.jw.writeBinary(falseBytes)
}

// Raw appends raw bytes to the buffer, without quotes and without escaping.
func (w *commonWriter[T]) Raw(data []byte) {
	if !w.jw.Ok() || len(data) == 0 {
		return
	}

	w.jw.writeBinary(data)
}

// String writes a string as a JSON string value enclosed by double quotes, with escaping.
//
// The empty string is a legit input.
func (w *commonWriter[T]) String(s string) {
	if !w.jw.Ok() {
		return
	}

	w.writeTextString(s)
}

// StringBytes writes a slice of bytes as a JSON string enclosed by double quotes ('"'), with escaping.
//
// An empty slice is a legit input.
func (w *commonWriter[T]) StringBytes(data []byte) {
	if !w.jw.Ok() || data == nil {
		return
	}

	w.writeText(data)
}

// StringRunes writes a slice of bytes as a JSON string enclosed by double quotes ('"'), with escaping.
//
// An empty slice is a legit input.
func (w *commonWriter[T]) StringRunes(data []rune) {
	if !w.jw.Ok() || data == nil {
		return
	}
	// worst case is utf8.UTFMax (4) bytes per rune. utf8.MaxRune is a code point, not a
	// byte width: using it here over-allocates by ~280,000x.
	holder, redeem := poolOfEscapedBuffers.BorrowWithSizeAndRedeem(len(data) * utf8.UTFMax)
	defer redeem()

	buf := holder.Slice()
	for _, r := range data {
		buf = utf8.AppendRune(buf, r)
	}

	w.writeText(buf)
}

// NumberBytes writes a slice of bytes as a JSON number.
//
// No check is carried out.
func (w *commonWriter[T]) NumberBytes(data []byte) {
	if !w.jw.Ok() || len(data) == 0 {
		return
	}

	w.jw.writeBinary(data)
}

// NumberCopy writes the bytes consumed from an [io.Reader] as a JSON number.
//
// No check is carried out.
func (w *commonWriter[T]) NumberCopy(r io.Reader) {
	w.RawCopy(r)
}

// RawCopy writes the bytes consumed from an [io.Reader], without quotes and without escaping.
func (w *commonWriter[T]) RawCopy(r io.Reader) {
	if !w.jw.Ok() {
		return
	}

	bufHolder, redeemReadBuffer := poolOfReadBuffers.BorrowWithRedeem()
	buf := bufHolder.Slice()

	for {
		n, err := r.Read(buf)
		if err != nil && !errors.Is(err, io.EOF) {
			w.jw.SetErr(err)

			break
		}

		if n > 0 {
			w.jw.writeBinary(buf[:n])
			if !w.jw.Ok() {
				break
			}
		}

		if n == 0 || (err != nil && errors.Is(err, io.EOF)) {
			break
		}
	}

	redeemReadBuffer()
}

func (w *commonWriter[T]) StringCopy(r io.Reader) {
	w.jw.writeSingleByte(quote)
	if !w.jw.Ok() {
		return
	}

	var remainder []byte
	bufHolder, redeemReadBuffer := poolOfReadBuffers.BorrowWithRedeem()
	defer func() {
		redeemReadBuffer()
	}()

	buf := bufHolder.Slice()

	for {
		n, err := r.Read(buf)
		if err != nil && !errors.Is(err, io.EOF) {
			w.jw.SetErr(err)

			return
		}

		if n > 0 {
			remainder = w.jw.writeEscaped(buf[:n])
			if !w.jw.Ok() {
				return
			}

			notWritten := len(remainder)
			if notWritten > 0 {
				// if the previous read reported an incomplete rune, consume the expected remaining bytes and realign to runes
				if notWritten > utf8.UTFMax {
					w.jw.SetErr(
						fmt.Errorf(
							"unexpected incomplete rune (remainder larger than possible rune): %c : %w",
							remainder,
							ErrDefaultWriter,
						),
					)

					return
				}

				runeSize := completeRuneSize(remainder[0]) // TODO: check if 0 (invalid)
				if runeSize == 0 {
					w.jw.SetErr(
						fmt.Errorf(
							"unexpected incomplete rune (invalid first byte): %c: %w",
							remainder,
							ErrDefaultWriter,
						),
					)

					return
				}

				var single [utf8.UTFMax]byte
				copy(single[:], remainder)

				// Read exactly the missing bytes of the split rune. io.ReadFull tolerates
				// short reads (e.g. a reader that yields one byte at a time) and only fails
				// when the input ends before the rune is complete.
				// Note: this must not clobber the outer loop's n/err, which drive termination.
				if _, rerr := io.ReadFull(r, single[notWritten:runeSize]); rerr != nil {
					if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
						w.jw.SetErr(
							fmt.Errorf(
								"unexpected incomplete rune at end of input: %c: %w",
								remainder,
								ErrDefaultWriter,
							),
						)

						return
					}

					w.jw.SetErr(rerr)

					return
				}

				remainder = w.jw.writeEscaped(single[:runeSize])
				if !w.jw.Ok() {
					return
				}
				if len(remainder) > 0 {
					w.jw.SetErr(
						fmt.Errorf(
							"unexpected incomplete rune at end of input: %c: %w",
							remainder,
							ErrDefaultWriter,
						),
					)

					return
				}
			}
		}

		if n == 0 || (err != nil && errors.Is(err, io.EOF)) {
			break
		}
	}

	w.jw.writeSingleByte(quote)
}

// completeRuneSize returns the total byte width of a UTF-8 rune from its leading byte.
//
// It returns 0 when c is not a valid multi-byte lead byte (i.e. an ASCII byte or a
// continuation byte 10xxxxxx).
func completeRuneSize(c byte) int {
	//nolint:mnd
	switch {
	case c&0b11111000 == 0b11110000:
		return 4 // 11110xxx
	case c&0b11110000 == 0b11100000:
		return 3 // 1110xxxx
	case c&0b11100000 == 0b11000000:
		return 2 // 110xxxxx
	default:
		return 0 // ASCII (0xxxxxxx) or continuation byte (10xxxxxx): not a multi-byte lead
	}
}

// JSONString writes a JSON value of [types.String].
//
// Nothing is written if the value is undefined (nil). A defined but empty value
// (e.g. [types.EmptyString]) is a legit input and renders as the empty JSON string ("").
func (w *commonWriter[T]) JSONString(value types.String) {
	if !w.jw.Ok() || !value.IsDefined() {
		return
	}

	w.writeText(value.Value)
}

// JSONNumber writes a JSON value of [types.Number].
//
// Nothing is written if the value is undefined.
func (w *commonWriter[T]) JSONNumber(value types.Number) {
	if !w.jw.Ok() || !value.IsDefined() || len(value.Value) == 0 {
		return
	}

	w.jw.writeBinary(value.Value)
}

// JSONBoolean writes a JSON value of [types.Boolean].
//
// Nothing is written if the value is undefined.
func (w *commonWriter[T]) JSONBoolean(value types.Boolean) {
	if !w.jw.Ok() || !value.IsDefined() {
		return
	}

	w.Bool(value.Value)
}

// JSONNull writes a JSON value of [types.NullType], i.e. the "null" token.
//
// Nothing is written if the value is undefined.
func (w *commonWriter[T]) JSONNull(value types.NullType) {
	if !w.jw.Ok() || !value.IsDefined() {
		return
	}

	w.jw.writeBinary(nullToken)
}

// Value writes a [values.Value]
func (w *commonWriter[T]) Value(v values.Value) {
	switch v.Kind() {
	case token.String:
		w.StringBytes(v.StringValue().Value)
	case token.Number:
		w.NumberBytes(v.NumberValue().Value)
	case token.Boolean:
		w.Bool(v.Bool())
	case token.Null:
		w.Null()
	default:
		// skip
	}
}

// Null writes a null token ("null").
func (w *commonWriter[T]) Null() {
	if !w.jw.Ok() {
		return
	}

	w.jw.writeBinary(nullToken)
}

// Key write a key [values.InternedKey] followed by a colon (":").
func (w *commonWriter[T]) Key(key values.InternedKey) {
	w.String(key.String())
	w.Colon()
}

// Number writes a number from any native numerical go type, except complex numbers.
//
// Types from the math/big package are supported: [big.Int], [big.Rat], [big.Float].
//
// Numbers provided as a slice of bytes are also supported (no check is carried out).
//
// The method panics if the argument is not a numerical type or []byte.
func (w *commonWriter[T]) Number(v any) {
	if !w.jw.Ok() {
		return
	}

	holder, redeem := poolOfNumberBuffers.BorrowWithRedeem()
	defer redeem()
	dst := holder.Slice()

	switch n := v.(type) {
	case uint8:
		w.jw.writeBinary(conv.AppendUinteger(dst, n))
	case uint16:
		w.jw.writeBinary(conv.AppendUinteger(dst, n))
	case uint32:
		w.jw.writeBinary(conv.AppendUinteger(dst, n))
	case uint64:
		w.jw.writeBinary(conv.AppendUinteger(dst, n))
	case uint:
		w.jw.writeBinary(conv.AppendUinteger(dst, n))
	case int8:
		w.jw.writeBinary(conv.AppendInteger(dst, n))
	case int16:
		w.jw.writeBinary(conv.AppendInteger(dst, n))
	case int32:
		w.jw.writeBinary(conv.AppendInteger(dst, n))
	case int64:
		w.jw.writeBinary(conv.AppendInteger(dst, n))
	case int:
		w.jw.writeBinary(conv.AppendInteger(dst, n))
	case float32:
		w.jw.writeBinary(conv.AppendFloat(dst, n))
	case float64:
		w.jw.writeBinary(conv.AppendFloat(dst, n))
	case []byte:
		w.jw.writeBinary(n)
	case *big.Int:
		if n == nil {
			return
		}
		w.append(n)
		return
	case big.Int:
		w.append(&n)
		return
	case *big.Rat:
		if n == nil {
			return
		}
		f, _ := n.Float64()
		w.jw.writeBinary(conv.AppendFloat(dst, f))
	case big.Rat:
		f, _ := n.Float64()
		w.jw.writeBinary(conv.AppendFloat(dst, f))
	case *big.Float:
		if n == nil {
			return
		}
		w.appendFloat(n)
		return
	case big.Float:
		w.appendFloat(&n)
		return
	default:
		panic(fmt.Errorf(
			"expected argument to Number() to be of a numerical type, but got: %T: %w",
			v, ErrDefaultWriter,
		))
	}
}

// Token writes a token [token.T] from a lexer.
//
// For key tokens, you'd need to call explicitly with the following colon token.
func (w *commonWriter[T]) Token(tok token.T) {
	if !w.jw.Ok() {
		return
	}

	switch tok.Kind() {
	case token.Delimiter:
		switch tok.Delimiter() {
		case token.OpeningBracket:
			w.StartObject()
		case token.ClosingBracket:
			w.EndObject()
		case token.OpeningSquareBracket:
			w.StartArray()
		case token.ClosingSquareBracket:
			w.EndArray()
		case token.Comma:
			w.Comma()
		case token.Colon:
			w.Colon()
		default:
			// ignore
		}
	case token.String, token.Key:
		w.writeText(tok.Value())
	case token.Number:
		w.NumberBytes(tok.Value())
	case token.Boolean:
		w.Bool(tok.Bool())
	case token.Null:
		w.Null()
	case token.EOF:
		fallthrough
	default:
		// ignore
	}
}

// VerbatimToken writes a token from the verbatim lexer [lexer.VL] preceded by its
// leading blanks, reproducing the source byte-for-byte.
//
// leadingSpace is the non-significant white-space run preceding the token (from
// [lexer.VL.LeadingSpace]); it is written to the buffer first.
//
// A verbatim string/key value is kept RAW by the lexer (escapes intact); it is
// emitted as-is between quotes. Re-escaping it through the normal string path would
// double-encode the backslashes.
func (w *commonWriter[T]) VerbatimToken(leadingSpace []byte, tok token.T) {
	if !w.jw.Ok() {
		return
	}

	w.jw.writeBinary(leadingSpace)

	if k := tok.Kind(); k == token.String || k == token.Key {
		w.jw.writeSingleByte(quote)
		w.jw.writeBinary(tok.Value())
		w.jw.writeSingleByte(quote)

		return
	}

	w.Token(tok)
}

func (w *commonWriter[T]) VerbatimValue(value values.VerbatimValue) {
	if !w.jw.Ok() {
		return
	}

	w.jw.writeBinary(value.Blanks())
	w.Value(value.Value)
}

// append writes down the result of AppendText.
//
// This borrows a temporary buffer to decode the result of AppendText()
func (w *commonWriter[T]) append(n encoding.TextAppender) {
	buf, redeem := poolOfNumberBuffers.BorrowWithRedeem()
	defer redeem()
	b := buf.Slice()

	b, err := n.AppendText(b)
	if err != nil {
		w.jw.SetErr(err)

		return
	}

	w.jw.writeBinary(b)
}

func (w *commonWriter[T]) appendFloat(n *big.Float) {
	buf, redeem := poolOfNumberBuffers.BorrowWithRedeem()
	defer redeem()
	b := buf.Slice()

	// MinPrec() keeps the conversion allocation-free for the common float64-backed case.
	// It prints the full binary expansion (e.g. 12.23 -> 12.2300…426) rather than the
	// shortest round-trip form, but the value is exact; prec -1 would roughly double allocs.
	b = n.Append(b, 'g', int(n.MinPrec()))
	w.jw.writeBinary(b)
}

func (w *commonWriter[T]) writeTextString(input string) {
	b := unsafe.Slice(unsafe.StringData(input), len(input))
	w.writeText(b)
}

func (w *commonWriter[T]) writeText(data []byte) {
	w.jw.writeSingleByte(quote)
	remainder := w.jw.writeEscaped(data)
	if len(remainder) > 0 {
		w.jw.SetErr(
			fmt.Errorf(
				"unexpected incomplete rune (invalid first byte): %c: %w",
				remainder,
				ErrDefaultWriter,
			),
		)

		return
	}
	w.jw.writeSingleByte(quote)
}

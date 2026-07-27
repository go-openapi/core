//nolint:dupl // writegen lifts commonWriter verbatim onto each concrete writer; the triplication is by design.
package writer

import (
	"encoding"
	"errors"
	"fmt"
	"io"
	"math/big"
	"runtime"
	"unicode/utf8"
	"unsafe"

	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/core/json/stores/values"
	"github.com/go-openapi/core/json/types"
	"github.com/go-openapi/core/json/writers"
	"github.com/go-openapi/swag/conv"
)

var (
	_ writers.StoreWriter    = &Buffered{}
	_ writers.JSONWriter     = &Buffered{}
	_ writers.TokenWriter    = &Buffered{}
	_ writers.VerbatimWriter = &Buffered{}
)

//go:generate go run ./internal/writegen -target Buffered

// Buffered JSON writer.
type Buffered struct {
	buffered
	commonWriter[*buffered]
}

type buffered struct {
	baseWriter
	bufferedOptions // configuration, embedded by value (no pool, no finalizer)

	buf          []byte // working buffer (direct field: keeps the hot path one indirection shallower)
	redeemBuffer func() // returns buf to poolOfBuffers; runtime state, set by borrowBuffer
}

func NewBuffered(w io.Writer, opts ...BufferedOption) *Buffered {
	writer := &Buffered{
		buffered: buffered{
			baseWriter: baseWriter{
				w: w,
			},
			bufferedOptions: bufferedOptionsWithDefaults(opts),
		},
	}
	writer.borrowBuffer()
	writer.jw = &writer.buffered

	// On the New path the working buffer is relinquished to the pool when the gc claims the writer.
	// (The Borrow path redeems it explicitly via RedeemBuffered.)
	runtime.AddCleanup(writer, func(redeem func()) {
		if redeem != nil {
			redeem()
		}
	}, writer.redeemBuffer)

	return writer
}

func (w *Buffered) Reset() {
	w.baseWriter.Reset()
	w.buf = w.buf[:0]
	// configuration (bufferedOptions) is preserved across Reset; the Borrow path re-sets it explicitly.
}

// Flush the internal buffer of the [Buffered] writer to the underlying [io.Writer].
func (w *Buffered) Flush() error {
	if w.err != nil {
		return w.err
	}

	w.flush()

	return w.err
}

// Size returns the number of bytes written so far, including bytes still pending in the
// internal buffer (the base counter is only incremented on flush).
func (w *buffered) Size() int64 {
	return w.written + int64(len(w.buf))
}

// borrowBuffer borrows the working buffer from the pool, sized per the options, and records
// its redeem handle. Must be called after bufferedOptions is set.
func (w *buffered) borrowBuffer() {
	bufHolder, redeem := poolOfBuffers.BorrowWithSizeAndRedeem(w.bufferSize)
	w.buf = bufHolder.Slice()[:0:w.bufferSize] // clip: the pool may hand back a larger capacity
	w.redeemBuffer = redeem
}

func (w *buffered) flush() {
	n, err := w.w.Write(w.buf)
	w.inc(n)
	w.err = err
	w.buf = w.buf[:0]
}

// redeem inner resources: return the working buffer to the pool.
func (w *buffered) redeem() {
	if w.redeemBuffer != nil {
		w.redeemBuffer()
		w.redeemBuffer = nil
	}
	w.buf = nil
}

// writeSingleByte appends one byte to the working buffer, flushing first when it is full.
//
// It deliberately omits a post-flush error check (kept small enough to inline): on a flush
// failure w.err is set and the surrounding writer short-circuits via Ok(); the stray byte is
// never emitted because Flush refuses to write once w.err is set.
func (w *buffered) writeSingleByte(c byte) {
	if len(w.buf) == cap(w.buf) {
		w.flush()
	}

	w.buf = append(w.buf, c)
}

func (w *buffered) writeBinary(data []byte) {
	var offset int

	for offset < len(data) {
		if len(w.buf) == cap(w.buf) {
			w.flush()
			if w.err != nil {
				return
			}
		}

		chunkSize := min(len(data[offset:]), cap(w.buf)-len(w.buf))
		w.buf = append(w.buf, data[offset:offset+chunkSize]...) // copy data to the buffer

		offset += chunkSize
	}
}

//nolint:gocyclo // inline the escape loop in one single call
func (w *buffered) writeEscaped(data []byte) (remainder []byte) {
	if w.err != nil {
		return nil
	}

	var (
		p       int
		escaped bool
	)

	// first iterates over non-escaped bytes.
	for ; p < len(data); p++ {
		c := data[p]
		if c < lowestPrintable || c >= utf8.RuneSelf || c == '\t' || c == '\r' || c == '\n' ||
			c == '\\' ||
			c == '"' ||
			c == '\b' ||
			c == '\f' {
			escaped = true

			break
		}
	}

	if p > 0 {
		w.writeBinary(data[:p])
	}

	if !escaped {
		//  nothing to be escaped: we are done
		return nil
	}

	for i := p; i < len(data); i++ {
		const (
			escapedSize        = 2
			escapedUnicodeSize = 6
		)

		c := data[i]
		available := cap(w.buf) - len(w.buf)

		switch {
		// TODO: compare perf with table lookup
		case c == '\t':
			if available < escapedSize {
				w.flush()
				if w.err != nil {
					return nil
				}
			}
			w.buf = append(w.buf, '\\', 't')
		case c == '\r':
			if available < escapedSize {
				w.flush()
				if w.err != nil {
					return nil
				}
			}
			w.buf = append(w.buf, '\\', 'r')
		case c == '\n':
			if available < escapedSize {
				w.flush()
				if w.err != nil {
					return nil
				}
			}
			w.buf = append(w.buf, '\\', 'n')
		case c == '\\':
			if available < escapedSize {
				w.flush()
				if w.err != nil {
					return nil
				}
			}
			w.buf = append(w.buf, '\\', '\\')
		case c == '"':
			if available < escapedSize {
				w.flush()
				if w.err != nil {
					return nil
				}
			}
			w.buf = append(w.buf, '\\', '"')
		case c == '\b':
			if available < escapedSize {
				w.flush()
				if w.err != nil {
					return nil
				}
			}
			w.buf = append(w.buf, '\\', 'b')
		case c == '\f':
			if available < escapedSize {
				w.flush()
				if w.err != nil {
					return nil
				}
			}
			w.buf = append(w.buf, '\\', 'f')
		case c >= 0x20 && c < utf8.RuneSelf:
			// single-width character, no escaping is required
			if available == 0 {
				w.flush()
				if w.err != nil {
					return nil
				}
			}
			w.buf = append(w.buf, c)
		case c < lowestPrintable:
			// control character is escaped as the unicode sequence \u00{hex representation of c}
			const chars = "0123456789abcdef"
			if available < escapedUnicodeSize {
				w.flush()
				if w.err != nil {
					return nil
				}
			}
			w.buf = append(
				w.buf,
				'\\',
				'u',
				'0',
				'0',
				chars[c>>4],
				chars[c&0xf],
			) // hexadecimal representation of c
		default:
			// multi-byte UTF8 character.
			if !utf8.FullRune(data[i:]) {
				// needs more read to complete the current rune
				return data[i:]
			}

			r, runeWidth := utf8.DecodeRune(data[i:])
			if available < runeWidth {
				w.flush()
				if w.err != nil {
					return nil
				}
			}
			w.buf = utf8.AppendRune(w.buf, r) // invalid runes are represented as \uFFFD
			i += runeWidth - 1
		}
	}

	return nil
}

// Comma is generated by writegen from commonWriter.Comma; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) Comma() {
	w.jw.writeSingleByte(comma)
}

// Colon is generated by writegen from commonWriter.Colon; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) Colon() {
	w.jw.writeSingleByte(colon)
}

// EndArray is generated by writegen from commonWriter.EndArray; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) EndArray() {
	w.jw.writeSingleByte(closingSquareBracket)
}

// EndObject is generated by writegen from commonWriter.EndObject; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) EndObject() {
	w.jw.writeSingleByte(closingBracket)
}

// StartArray is generated by writegen from commonWriter.StartArray; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) StartArray() {
	w.jw.writeSingleByte(openingSquareBracket)
}

// StartObject is generated by writegen from commonWriter.StartObject; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) StartObject() {
	w.jw.writeSingleByte(openingBracket)
}

// Bool is generated by writegen from commonWriter.Bool; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) Bool(v bool) {
	if !w.jw.Ok() {
		return
	}

	if v {
		w.jw.writeBinary(trueBytes)

		return
	}

	w.jw.writeBinary(falseBytes)
}

// Raw is generated by writegen from commonWriter.Raw; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) Raw(data []byte) {
	if !w.jw.Ok() || len(data) == 0 {
		return
	}

	w.jw.writeBinary(data)
}

// String is generated by writegen from commonWriter.String; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) String(s string) {
	if !w.jw.Ok() {
		return
	}

	w.writeTextString(s)
}

// StringBytes is generated by writegen from commonWriter.StringBytes; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) StringBytes(data []byte) {
	if !w.jw.Ok() || data == nil {
		return
	}

	w.writeText(data)
}

// StringRunes is generated by writegen from commonWriter.StringRunes; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) StringRunes(data []rune) {
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

// NumberBytes is generated by writegen from commonWriter.NumberBytes; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) NumberBytes(data []byte) {
	if !w.jw.Ok() || len(data) == 0 {
		return
	}

	w.jw.writeBinary(data)
}

// NumberCopy is generated by writegen from commonWriter.NumberCopy; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) NumberCopy(r io.Reader) {
	w.RawCopy(r)
}

// RawCopy is generated by writegen from commonWriter.RawCopy; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) RawCopy(r io.Reader) {
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

// StringCopy is generated by writegen from commonWriter.StringCopy; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) StringCopy(r io.Reader) {
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

// JSONString is generated by writegen from commonWriter.JSONString; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) JSONString(value types.String) {
	if !w.jw.Ok() || !value.IsDefined() {
		return
	}

	w.writeText(value.Value)
}

// JSONNumber is generated by writegen from commonWriter.JSONNumber; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) JSONNumber(value types.Number) {
	if !w.jw.Ok() || !value.IsDefined() || len(value.Value) == 0 {
		return
	}

	w.jw.writeBinary(value.Value)
}

// JSONBoolean is generated by writegen from commonWriter.JSONBoolean; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) JSONBoolean(value types.Boolean) {
	if !w.jw.Ok() || !value.IsDefined() {
		return
	}

	w.Bool(value.Value)
}

// JSONNull is generated by writegen from commonWriter.JSONNull; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) JSONNull(value types.NullType) {
	if !w.jw.Ok() || !value.IsDefined() {
		return
	}

	w.jw.writeBinary(nullToken)
}

// Value is generated by writegen from commonWriter.Value; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) Value(v values.Value) {
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

// Null is generated by writegen from commonWriter.Null; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) Null() {
	if !w.jw.Ok() {
		return
	}

	w.jw.writeBinary(nullToken)
}

// Key is generated by writegen from commonWriter.Key; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) Key(key values.InternedKey) {
	w.String(key.String())
	w.Colon()
}

// Number is generated by writegen from commonWriter.Number; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) Number(v any) {
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

// Token is generated by writegen from commonWriter.Token; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) Token(tok token.T) {
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

// VerbatimToken is generated by writegen from commonWriter.VerbatimToken; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) VerbatimToken(leadingSpace []byte, tok token.T) {
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

// VerbatimValue is generated by writegen from commonWriter.VerbatimValue; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) VerbatimValue(value values.VerbatimValue) {
	if !w.jw.Ok() {
		return
	}

	w.jw.writeBinary(value.Blanks())
	w.Value(value.Value)
}

// append is generated by writegen from commonWriter.append; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) append(n encoding.TextAppender) {
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

// appendFloat is generated by writegen from commonWriter.appendFloat; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) appendFloat(n *big.Float) {
	buf, redeem := poolOfNumberBuffers.BorrowWithRedeem()
	defer redeem()
	b := buf.Slice()

	// MinPrec() keeps the conversion allocation-free for the common float64-backed case.
	// It prints the full binary expansion (e.g. 12.23 -> 12.2300…426) rather than the
	// shortest round-trip form, but the value is exact; prec -1 would roughly double allocs.
	b = n.Append(b, 'g', int(n.MinPrec()))
	w.jw.writeBinary(b)
}

// writeTextString is generated by writegen from commonWriter.writeTextString; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) writeTextString(input string) {
	b := unsafe.Slice(unsafe.StringData(input), len(input))
	w.writeText(b)
}

// writeText is generated by writegen from commonWriter.writeText; DO NOT EDIT (edit commonWriter, re-run go generate).
func (w *Buffered) writeText(data []byte) {
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

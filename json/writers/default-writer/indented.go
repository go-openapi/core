package writer

import (
	"io"

	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/core/json/stores/values"
	"github.com/go-openapi/core/json/types"
	"github.com/go-openapi/core/json/writers"
)

var (
	_ writers.StoreWriter = &Indented{}
	_ writers.JSONWriter  = &Indented{}
	_ writers.TokenWriter = &Indented{}
)

type Indented struct {
	*Buffered
	indentedOptions // configuration, embedded by value (no pool, no finalizer)

	level           int
	redeemBuffered  *Buffered // mark that the Buffered must be redeemed
	lastSeparator   byte
	containerOnHold bool
}

func NewIndented(w io.Writer, opts ...IndentedOption) *Indented {
	o := indentedOptionsWithDefaults(opts)

	// the inner Buffered registers its own working-buffer finalizer; Indented holds no extra pooled
	// resource of its own on the New path, so it needs none.
	return &Indented{
		Buffered:        NewBuffered(w, o.applyBufferedOptions...),
		indentedOptions: o,
	}
}

func (w *Indented) Reset() {
	w.lastSeparator = 0
	w.level = 0

	if w.Buffered != nil {
		w.Buffered.Reset()
	}
	// configuration (indentedOptions) is preserved across Reset; the Borrow path re-sets it explicitly.
}

func (w *Indented) Flush() error {
	w.releaseHold(true)

	return w.Buffered.Flush()
}

// Comma writes a comma separator, ','.
func (w *Indented) Comma() {
	w.releaseHold(true)
	w.Buffered.Comma()
	w.writeNewlineIndent()
	w.lastSeparator = comma
}

// Colon writes a colon separator, ':'.
func (w *Indented) Colon() {
	w.releaseHold(true)
	w.Buffered.Colon()
	w.jw.writeSingleByte(space)
	w.lastSeparator = colon
}

// EndArray writes an end-of-array separator, i.e. ']'.
func (w *Indented) EndArray() {
	// if the previous written was [, do not indent on empty array
	if w.containerOnHold {
		w.releaseHold(w.lastSeparator != openingSquareBracket)

		w.Buffered.EndArray()
		w.lastSeparator = closingSquareBracket
		w.level--

		return
	}

	// no hold: close array and indent normally
	w.level--
	w.writeNewlineIndent()
	w.Buffered.EndArray()
	w.lastSeparator = closingSquareBracket
}

// EndObject writes an end-of-object separator, i.e. '}'.
func (w *Indented) EndObject() {
	// if the previous written was {, do not indent on empty object
	if w.containerOnHold {
		w.releaseHold(w.lastSeparator != openingBracket)

		w.Buffered.EndObject()
		w.lastSeparator = closingBracket
		w.level--

		return
	}

	// no hold: close object and indent normally
	w.level--
	w.writeNewlineIndent()
	w.Buffered.EndObject()
	w.lastSeparator = closingBracket
}

// StartArray writes a start-of-array separator, i.e. '['.
func (w *Indented) StartArray() {
	// if it was previously on hold, and we got a new token, release previous container
	w.releaseHold(true)

	// this is a new array: keep containerOnHold

	// put on hold until next write or flush
	w.containerOnHold = true
	w.lastSeparator = openingSquareBracket
}

// StartObject writes a start-of-object separator, i.e. '{'.
func (w *Indented) StartObject() {
	// if it was previously on hold, and we got a new token, release previous container
	w.releaseHold(true)

	// this is a new object: keep containerOnHold

	// put on hold until next write or flush
	w.containerOnHold = true
	w.lastSeparator = openingBracket
}

func (w *Indented) Key(key values.InternedKey) {
	w.releaseHold(true)
	w.Buffered.String(key.String())
	w.Colon()
}

func (w *Indented) Token(tok token.T) {
	if !w.Ok() {
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
	default:
		w.releaseHold(true)
		w.Buffered.Token(tok)
	}
}

func (w *Indented) Bool(v bool) {
	w.releaseHold(true)
	w.Buffered.Bool(v)
}

func (w *Indented) Raw(data []byte) {
	w.releaseHold(true)
	w.Buffered.Raw(data)
}

func (w *Indented) String(s string) {
	w.releaseHold(true)
	w.Buffered.String(s)
}

func (w *Indented) StringBytes(data []byte) {
	w.releaseHold(true)
	w.Buffered.StringBytes(data)
}

func (w *Indented) StringRunes(data []rune) {
	w.releaseHold(true)
	w.Buffered.StringRunes(data)
}

func (w *Indented) NumberBytes(data []byte) {
	w.releaseHold(true)
	w.Buffered.NumberBytes(data)
}

func (w *Indented) NumberCopy(r io.Reader) {
	w.releaseHold(true)
	w.Buffered.NumberCopy(r)
}

func (w *Indented) RawCopy(r io.Reader) {
	w.releaseHold(true)
	w.Buffered.RawCopy(r)
}

func (w *Indented) StringCopy(r io.Reader) {
	w.releaseHold(true)
	w.Buffered.StringCopy(r)
}

func (w *Indented) JSONString(value types.String) {
	w.releaseHold(true)
	w.Buffered.JSONString(value)
}

func (w *Indented) JSONNumber(value types.Number) {
	w.releaseHold(true)
	w.Buffered.JSONNumber(value)
}

func (w *Indented) JSONBoolean(value types.Boolean) {
	w.releaseHold(true)
	w.Buffered.JSONBoolean(value)
}

func (w *Indented) JSONNull(value types.NullType) {
	w.releaseHold(true)
	w.Buffered.JSONNull(value)
}

func (w *Indented) Value(v values.Value) {
	w.releaseHold(true)
	w.Buffered.Value(v)
}

func (w *Indented) Null() {
	w.releaseHold(true)
	w.Buffered.Null()
}

func (w *Indented) Number(v any) {
	w.releaseHold(true)
	w.Buffered.Number(v)
}

func (w *Indented) redeem() {
	// redeemBuffered is NOT cleared: a recycled Indented keeps reusing the same inner *Buffered across
	// Borrow/Redeem cycles, so the handle must survive to redeem the working buffer next time.
	if w.redeemBuffered != nil { // hydrated when borrowing from a pool; nil when created with New
		RedeemBuffered(w.redeemBuffered)
	}
}

func (w *Indented) releaseHold(wantIndent bool) {
	if !w.containerOnHold {
		return
	}

	switch w.lastSeparator {
	case openingSquareBracket:
		w.Buffered.StartArray()
	case openingBracket:
		w.Buffered.StartObject()
	}

	w.level++
	if wantIndent {
		w.writeNewlineIndent()
	}
	w.containerOnHold = false
}

func (w *Indented) writeNewlineIndent() {
	if !w.Ok() {
		return
	}

	w.jw.writeSingleByte(newline)

	for range w.level {
		w.jw.writeBinary(w.indent)
	}
}

package writer

import (
	"io"

	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/core/json/stores/values"
	"github.com/go-openapi/core/json/types"
	"github.com/go-openapi/core/json/writers"
)

var (
	_ writers.StoreWriter = &YAML{}
	_ writers.JSONWriter  = &YAML{}
	_ writers.TokenWriter = &YAML{}
)

const (
	yamlNull    = '~'
	yamlElement = '-'
)

// TODO: optionally add yaml doc separator "---"
// TODO: text escaping rules

// YAML is a writer that produces a yaml document.
//
// The [YAML] writer is always buffered.
type YAML struct {
	*Buffered
	yamlOptions // configuration, embedded by value (no pool, no finalizer)

	level           int
	redeemBuffered  *Buffered // mark that the Buffered must be redeemed
	lastSeparator   byte
	containerOnHold bool

	nestingLevel []uint64 // the stack of nested containers. Every bit represent an extra nesting. Capped if maxContainerStack > 0
	lastStack    uint64
}

func NewYAML(w io.Writer, opts ...YAMLOption) *YAML {
	o := yamlOptionsWithDefaults(opts)
	writer := &YAML{
		Buffered:     NewBuffered(w, o.applyBufferedOptions...),
		yamlOptions:  o,
		nestingLevel: make([]uint64, 1),
	}
	writer.nestingLevel[0] = 1 // the initial value for the stack must be 1: this bit is thereafter shifted right or left

	// the inner Buffered registers its own working-buffer finalizer; YAML holds no extra pooled
	// resource of its own on the New path, so it needs none.

	return writer
}

func (w *YAML) Reset() {
	w.lastSeparator = 0
	w.level = 0
	// a YAML borrowed fresh from the pool has a nil nestingLevel: the pool calls Reset
	// before BorrowYAML initializes it, so allocate the initial word here when needed.
	if cap(w.nestingLevel) == 0 {
		w.nestingLevel = make([]uint64, 1)
	} else {
		w.nestingLevel = w.nestingLevel[:1]
	}
	w.nestingLevel[0] = 1
	w.lastStack = 0

	if w.Buffered != nil {
		w.Buffered.Reset()
	}
	// configuration (yamlOptions) is preserved across Reset; the Borrow path re-sets it explicitly.
}

func (w *YAML) Flush() error {
	w.releaseHold(true)

	return w.Buffered.Flush()
}

// Comma writes a comma separator, ','.
func (w *YAML) Comma() {
	w.releaseHold(true)
	// this tests avoid an extra blank line after an array followed by a comma
	if w.lastSeparator != closingSquareBracket {
		w.writeNewlineIndent()
	}
	w.lastSeparator = comma
}

// Colon writes a colon separator, ':'.
func (w *YAML) Colon() {
	w.releaseHold(true)
	w.Buffered.Colon()
	w.jw.writeSingleByte(space)
	w.lastSeparator = colon
}

// EndArray writes an end-of-array separator, i.e. ']'.
func (w *YAML) EndArray() {
	// if the previous written was [, do not indent on empty array
	if w.containerOnHold {
		w.releaseHold(w.lastSeparator != openingSquareBracket)
		w.Buffered.EndArray()

		w.level--
		w.lastSeparator = closingSquareBracket
		w.lastStack = w.nestingLevel[len(w.nestingLevel)-1] // save the current stack for the current token
		w.popContainer()

		return
	}

	// no hold: close array and indent normally
	w.level--
	w.lastSeparator = closingSquareBracket
	w.lastStack = w.nestingLevel[len(w.nestingLevel)-1] // save the current stack for the current token
	w.popContainer()

	w.writeNewlineIndent()
}

// EndObject writes an end-of-object separator, i.e. '}'.
func (w *YAML) EndObject() {
	// if the previous written was {, do not indent on empty object
	if w.containerOnHold {
		w.releaseHold(w.lastSeparator != openingBracket)
		w.Buffered.EndObject()

		w.level--
		w.lastSeparator = closingBracket
		w.lastStack = w.nestingLevel[len(w.nestingLevel)-1] // save the current stack for the current token
		w.popContainer()

		return
	}

	// no hold: close object and indent normally
	w.level--
	w.lastSeparator = closingBracket
	w.lastStack = w.nestingLevel[len(w.nestingLevel)-1] // save the current stack for the current token
	w.popContainer()

	w.writeNewlineIndent()
}

// StartArray writes a start-of-array separator, i.e. '['.
func (w *YAML) StartArray() {
	// if it was previously on hold, and we got a new token, release previous container
	w.releaseHold(true)
	w.yamlCheckIsElement()
	w.pushArray()

	// this is a new array: keep containerOnHold

	// put on hold until next write or flush
	w.containerOnHold = true
	w.lastSeparator = openingSquareBracket
}

// StartObject writes a start-of-object separator, i.e. '{'.
func (w *YAML) StartObject() {
	// TODO: on top-level object, do not indent
	// if it was previously on hold, and we got a new token, release previous container
	w.releaseHold(true)
	w.yamlCheckIsElement()
	w.pushObject()

	// this is a new object: keep containerOnHold

	// put on hold until next write or flush
	w.containerOnHold = true
	w.lastSeparator = openingBracket
}

func (w *YAML) Key(key values.InternedKey) {
	w.releaseHold(true)
	w.yamlCheckIsElement()
	w.Buffered.String(key.String())
	w.Colon()
}

func (w *YAML) Token(tok token.T) {
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
	case token.Null:
		// route through YAML.Null so a null token renders as "~", consistently with
		// Null/Value/JSONNull (Buffered.Token would emit the literal "null").
		w.Null()
	default:
		w.releaseHold(true)
		w.yamlCheckIsElement()
		w.Buffered.Token(tok)
	}
}

func (w *YAML) Bool(v bool) {
	w.releaseHold(true)
	w.yamlCheckIsElement()
	w.Buffered.Bool(v)
}

func (w *YAML) Raw(data []byte) {
	w.releaseHold(true)
	w.yamlCheckIsElement()
	w.Buffered.Raw(data)
}

func (w *YAML) String(s string) {
	w.releaseHold(true)
	w.yamlCheckIsElement()
	w.Buffered.String(s)
}

func (w *YAML) StringBytes(data []byte) {
	w.releaseHold(true)
	w.yamlCheckIsElement()
	w.Buffered.StringBytes(data)
}

func (w *YAML) StringRunes(data []rune) {
	w.releaseHold(true)
	w.yamlCheckIsElement()
	w.Buffered.StringRunes(data)
}

func (w *YAML) NumberBytes(data []byte) {
	w.releaseHold(true)
	w.yamlCheckIsElement()
	w.Buffered.NumberBytes(data)
}

func (w *YAML) NumberCopy(r io.Reader) {
	w.releaseHold(true)
	w.yamlCheckIsElement()
	w.Buffered.NumberCopy(r)
}

func (w *YAML) RawCopy(r io.Reader) {
	w.releaseHold(true)
	w.yamlCheckIsElement()
	w.Buffered.RawCopy(r)
}

func (w *YAML) StringCopy(r io.Reader) {
	w.releaseHold(true)
	w.yamlCheckIsElement()
	w.Buffered.StringCopy(r)
}

func (w *YAML) JSONString(value types.String) {
	w.releaseHold(true)
	w.yamlCheckIsElement()
	w.Buffered.JSONString(value)
}

func (w *YAML) JSONNumber(value types.Number) {
	w.releaseHold(true)
	w.yamlCheckIsElement()
	w.Buffered.JSONNumber(value)
}

func (w *YAML) JSONBoolean(value types.Boolean) {
	w.releaseHold(true)
	w.yamlCheckIsElement()
	w.Buffered.JSONBoolean(value)
}

func (w *YAML) JSONNull(value types.NullType) {
	if !value.IsDefined() {
		return
	}

	w.Null()
}

func (w *YAML) Value(v values.Value) {
	if v.Kind() == token.Null {
		w.Null()

		return
	}

	w.releaseHold(true)
	w.yamlCheckIsElement()
	w.Buffered.Value(v)
}

func (w *YAML) Null() {
	if !w.jw.Ok() {
		return
	}

	w.releaseHold(true)
	w.yamlCheckIsElement()
	w.jw.writeSingleByte(yamlNull)
}

func (w *YAML) Number(v any) {
	w.releaseHold(true)
	w.yamlCheckIsElement()
	w.Buffered.Number(v)
}

func (w *YAML) redeem() {
	if w.redeemBuffered == nil { // created with New: there is no borrowed inner writer to give back
		return
	}

	// Give the inner Buffered back to its pool and drop EVERY alias to it. Past Redeem the
	// instance may be handed to another borrower, so keeping w.Buffered (or w.redeemBuffered)
	// would be a use-after-redeem, and the next RedeemYAML would redeem it twice.
	// BorrowYAML therefore borrows a fresh inner Buffered on every cycle.
	RedeemBuffered(w.redeemBuffered)
	w.redeemBuffered = nil
	w.Buffered = nil
}

func (w *YAML) yamlCheckIsElement() {
	if !w.isInArray() {
		return
	}

	// TODO: option to not indent array elements
	w.writeBinary(yamlElementPrefix)
}

// releaseHold releases a previously hold separator token
func (w *YAML) releaseHold(wantIndent bool) {
	if !w.containerOnHold {
		return
	}

	if !wantIndent {
		// in YAML output, write [] or {} only for empty containers
		switch w.lastSeparator {
		case openingSquareBracket:
			w.Buffered.StartArray()
		case openingBracket:
			w.Buffered.StartObject()
		}

		w.level++
		w.containerOnHold = false

		return
	}

	// this is not an empty container. In YAML, we don't use { or [, so we just indent
	w.level++
	w.writeNewlineIndent()
	w.containerOnHold = false
}

func (w *YAML) writeNewlineIndent() {
	if !w.Ok() {
		return
	}

	w.jw.writeSingleByte(newline)

	for range w.level {
		w.jw.writeBinary(w.indent)
	}
}

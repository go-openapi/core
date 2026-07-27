package expressions

import (
	"errors"
	"iter"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/go-openapi/core/json/stores/values"
)

// EmptyPointer represents the empty JSON pointer, which matches a whole document.
var EmptyPointer = Pointer( //nolint:gochecknoglobals // ok to predefine a static empty value
	[]stringOrInt{},
)

var (
	// replacers that implement JSON pointer escaping and unescaping rules

	pthEscaper = strings.NewReplacer( //nolint:gochecknoglobals // ok to declare a strings replacer as a global
		"~",
		"~0",
		"/",
		"~1",
	)

	pthUnescaper = strings.NewReplacer( //nolint:gochecknoglobals // ok to declare a strings replacer as a global
		"~1",
		"/",
		"~0",
		"~",
	)
)

type pointerError string

func (e pointerError) Error() string {
	return string(e)
}

const (
	// ErrPointer is an error raised by a JSON pointer.
	ErrPointer pointerError = "JSON pointer error"

	// ErrPointerNotFound is raised when a JSON pointer cannot be resolved against a document.
	ErrPointerNotFound pointerError = "JSON pointer not found"

	// ErrInvalidStart states that a JSON pointer must start with a separator ("/"), or be the empty JSON pointer.
	ErrInvalidStart pointerError = `JSON pointer must be empty or start with a /"`
)

// Pointer represents a JSON Pointer.
type Pointer []stringOrInt

type PathElemKind uint8

func (k PathElemKind) IsKey() bool {
	return k&PathElemString > 0
}

func (k PathElemKind) IsElem() bool {
	return k&PathElemInt > 0
}

func (k PathElemKind) IsAmbiguous() bool {
	return k == PathElemStringOrInt
}

const (
	PathElemEmpty       PathElemKind = 0
	PathElemString      PathElemKind = 1
	PathElemInt         PathElemKind = 2
	PathElemStringOrInt PathElemKind = 3 // ambiguous string: could match an array element (preferred) or an integer key.
)

type stringOrInt struct {
	key  values.InternedKey
	elem int
	kind PathElemKind
}

func (s stringOrInt) IsEmpty() bool {
	return s.kind == PathElemEmpty
}

func (s stringOrInt) Kind() PathElemKind {
	return s.kind
}

func (s stringOrInt) Key() values.InternedKey {
	return s.key
}

func (s stringOrInt) Elem() int {
	return s.elem
}

func (s stringOrInt) IsKey() bool {
	return s.kind.IsKey()
}

func (s stringOrInt) IsElem() bool {
	return s.kind.IsElem()
}

func (s stringOrInt) IsAmbiguous() bool {
	return s.kind.IsAmbiguous()
}

// MakePointer builds a JSON [Pointer] from its string representation.
//
// RFC6901 definition of a JSON pointer:
//
//   - may be empty
//   - if not empty, must start by "/"
//   - all tokens are separated by "/"
//   - "/" is escaped by "~1"
//   - "~" is escaped by "~0"
//   - tokens representing a numerical array index are non-negative integers
//   - an integer digit may be "0" or any integer without a leading "0"
//
// Notice that this definition of a JSON pointer does not yield a unique match:
// token "123" would both match key "123" in an object or item 123 in an array.
func MakePointer(s string) (Pointer, error) {
	if s == "" {
		return EmptyPointer, nil
	}

	unrooted, ok := strings.CutPrefix(s, "/")
	if !ok {
		return nil, errors.Join(ErrInvalidStart, ErrPointer)
	}

	tokens := strings.Split(unrooted, "/")
	p := make(Pointer, len(tokens))

	for i, token := range tokens {
		unescaped := pthUnescaper.Replace(token)
		p[i].key = values.MakeInternedKey(unescaped)
		idx := asNumber(unescaped)
		if idx < 0 {
			p[i].kind = PathElemString
			continue
		}
		p[i].elem = idx
		p[i].kind = PathElemStringOrInt
	}

	return p, nil
}

// MakePointerFromElements buils a JSON [Pointer] from a list of elements
// that constitute the search path.
//
// Elements may be of type string, [values.InternedKey], or an integer value (int, uint32 etc).
//
// String elements are not escaped.
//
// Integer elements only apply to arrays. In this representation, we no longer have an ambiguous
// search that could match a key with the string representation of the integer element.
func MakePointerFromElements(elems ...any) (Pointer, error) {
	if len(elems) == 0 {
		return EmptyPointer, nil
	}

	p := make(Pointer, len(elems))

	for i, e := range elems {
		switch te := e.(type) {
		case string:
			p[i] = makePartFromString(te)
		case values.InternedKey:
			p[i] = makePartFromInternedString(te)
		case uint32:
			p[i] = makePartFromInteger(te)
		case int32:
			if te < 0 {
				return nil, ErrPointer
			}
			p[i] = makePartFromInteger(te)
		case uint:
			p[i] = makePartFromInteger(te)
		case int:
			if te < 0 {
				return nil, ErrPointer
			}
			p[i] = makePartFromInteger(te)
		case uint8:
			p[i] = makePartFromInteger(te)
		case int8:
			if te < 0 {
				return nil, ErrPointer
			}
			p[i] = makePartFromInteger(te)
		case uint16:
			p[i] = makePartFromInteger(te)
		case int16:
			if te < 0 {
				return nil, ErrPointer
			}
			p[i] = makePartFromInteger(te)
		case uint64:
			if te > math.MaxInt {
				return nil, ErrPointer
			}
			p[i] = makePartFromInteger(te)
		case int64:
			if te < 0 || te > math.MaxInt {
				return nil, ErrPointer
			}
			p[i] = makePartFromInteger(te)

		default:
			return nil, ErrPointer
		}
	}

	return p, nil
}

type PointerPart struct {
	stringOrInt
}

func (p Pointer) Clone() Pointer {
	return slices.Clone(p)
}

// Parts iterates over the parts of the pointer.
func (p Pointer) Parts() iter.Seq[PointerPart] {
	return func(yield func(look PointerPart) bool) {
		for _, part := range p {
			if !yield(PointerPart{stringOrInt: part}) {
				return
			}
		}
	}
}

// String representation of a JSON pointer, with escaping rules as per RFC 6901
func (p Pointer) String() string {
	var w strings.Builder

	for _, e := range p {
		w.WriteByte('/')
		if e.kind == PathElemString {
			w.WriteString(pthEscaper.Replace(e.key.String()))

			continue
		}
		const base10 = 10
		w.WriteString(strconv.FormatInt(int64(e.elem), base10))
	}

	return w.String()
}

func (p Pointer) AppendKey(key string) Pointer {
	p = append(p, makePartFromKey(key))

	return p
}

func (p Pointer) AppendElem(elem int) Pointer {
	p = append(p, makePartFromInteger(elem))

	return p
}

func asNumber(s string) int {
	l := len(s)
	if l == 0 {
		return -1
	}
	if s[0] < '0' || s[0] > '9' {
		return -1
	}
	if s[0] == '0' && l > 1 {
		return -1
	}

	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}

	return n
}

func makePartFromString(te string) stringOrInt {
	idx := asNumber(te)
	if idx < 0 {
		return stringOrInt{
			kind: PathElemString,
			key:  values.MakeInternedKey(te),
		}
	}

	// ambiguous case to be resolved at search time
	return stringOrInt{
		kind: PathElemStringOrInt,
		key:  values.MakeInternedKey(te),
		elem: idx,
	}
}

func makePartFromInternedString(te values.InternedKey) stringOrInt {
	idx := asNumber(te.String())
	if idx < 0 {
		return stringOrInt{
			kind: PathElemString,
			key:  te,
		}
	}

	// ambiguous case to be resolved at search time
	return stringOrInt{
		kind: PathElemStringOrInt,
		key:  te,
		elem: idx,
	}
}

func makePartFromKey(te string) stringOrInt {
	return stringOrInt{
		kind: PathElemString,
		key:  values.MakeInternedKey(te),
	}
}

func makePartFromInteger[T integer](i T) stringOrInt {
	// precondition: the input i must not overflow the internal int
	return stringOrInt{
		kind: PathElemInt,
		elem: int(i), // conversion of a positive integer, guarded against overflow
	}
}

type integer interface {
	~int | ~uint | ~int8 | ~uint8 | ~int16 | ~uint16 | ~int32 | ~uint32 | ~int64 | ~uint64
}

/*
func errPointerGotIndex(i int) error {
	return fmt.Errorf("expected a path key string to search an object, but got %d instead", i)
}

func errPointerNoKey(k values.InternedKey) error {
	return fmt.Errorf("searching path key %q in object, but was not found", k.String())
}
*/

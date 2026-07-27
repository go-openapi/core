package values

import (
	"math/big"
	"slices"

	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/core/json/types"
	"github.com/go-openapi/swag/conv"
)

//nolint:gochecknoglobals // global immutable values
var (
	// UndefinedValue represents the absence of a value: it is what a [Store] returns for the zero
	// [stores.Handle] ("no value").
	//
	// It is distinct from [NullValue]: per the JSON value model a null is a defined value, whereas an
	// undefined value marks absence (and marshals as empty). [Value.IsDefined] reports the difference.
	UndefinedValue = Value{kind: token.Unknown}

	// NullValue represents the (unique) value of the null type.
	NullValue = Value{kind: token.Null, z: types.Null}

	// EmptyStringValue represents the (unique) value for the empty string.
	EmptyStringValue = Value{kind: token.String, s: types.EmptyString}

	// TrueValue represents the boolean true value.
	TrueValue = Value{kind: token.Boolean, b: types.True}

	// FalseValue represents the boolean false value.
	FalseValue = Value{kind: token.Boolean, b: types.False}

	// ZeroValue represents the numerical 0 value.
	ZeroValue = Value{kind: token.Number, n: types.Zero}
)

// Value represents a JSON scalar value to be used in a [Store]. It is immutable.
//
// [Value] s are used to tie a general purpose value type to the more specialized JSON [types].
//
// A [Value] may contain either a [types.String], a [types.Number], a [types.Boolean] or a [types.NullType] (i.e. the unique [NullValue]).
//
// A [Value] does not represent non-significant blank space. To do this, see [VerbatimValue].
type Value struct {
	kind token.Kind

	s types.String
	n types.Number
	b types.Boolean
	z types.NullType
}

// Kind of value, which may be [token.String], [token.Number], [token.Boolean], or [token.Null].
func (v Value) Kind() token.Kind {
	return v.kind
}

// IsDefined reports whether the value is defined, i.e. it is not the [UndefinedValue] (the absence of
// a value). A null value is defined.
func (v Value) IsDefined() bool {
	return v.kind != token.Unknown
}

// StringValue returns the underlying JSON [types.String] for string values.
//
// It always returns an undefined [types.String] for non-string values.
func (v Value) StringValue() types.String {
	if v.kind != token.String {
		return types.String{}
	}

	return v.s
}

// NumberValue returns the underlying JSON [types.Number] for numerical values.
//
// It always returns an undefines [types.Number] for non-number values.
func (v Value) NumberValue() types.Number {
	if v.kind != token.Number {
		return types.Number{}
	}

	return v.n
}

// String go value for values that are JSON strings.
//
// It always returns the empty string for non-string values.
func (v Value) String() string {
	if v.kind != token.String {
		return ""
	}
	return v.s.String()
}

// Bool go value for values that are JSON booleans.
//
// It always returns false for non-boolean values.
func (v Value) Bool() bool {
	if v.kind != token.Boolean {
		return false
	}
	return v.b.Bool()
}

// Bytes slice for values that are JSON strings or numbers.
//
// It always returns nil for other value types.
func (v Value) Bytes() []byte {
	switch v.kind {
	case token.String:
		return v.s.Value
	case token.Number:
		return v.n.Value
	case token.Unknown, token.Delimiter, token.Key, token.Boolean, token.Null, token.EOF:
		fallthrough
	default:
		return nil
	}
}

// MakeStringValue builds a value from a go string.
func MakeStringValue(value string) Value {
	if len(value) == 0 {
		return EmptyStringValue
	}

	return Value{
		kind: token.String,
		s: types.String{
			Value: []byte(value),
		},
	}
}

// MakeNumberValue builds a value from a JSON [types.Number].
func MakeNumberValue(value types.Number) Value {
	return Value{
		kind: token.Number,
		n:    value,
	}
}

// MakeIntegerValue builds a value from any go signed integer type (int, int8, int16, int32 and int64).
func MakeIntegerValue[T conv.Signed](value T) Value {
	return Value{
		kind: token.Number,
		n: types.Number{
			Value: []byte(conv.FormatInteger(value)),
		},
	}
}

// MakeUintegerValue builds a value from any go unsigned integer type (uint, uint8, uint16, uint32, uint64).
func MakeUintegerValue[T conv.Unsigned](value T) Value {
	return Value{
		kind: token.Number,
		n: types.Number{
			Value: []byte(conv.FormatUinteger(value)),
		},
	}
}

// MakeFloatValue builds a value from any go float type (float64, float32).
func MakeFloatValue[T conv.Float](value T) Value {
	return Value{
		kind: token.Number,
		n: types.Number{
			Value: []byte(conv.FormatFloat(value)),
		},
	}
}

// MakeBigIntValue builds an integer value from a [big.Int].
func MakeBigIntValue(value *big.Int) Value {
	const base = 10
	digits := len(value.Bits()) + 1 // reserve an extra slot for negative

	return Value{
		kind: token.Number,
		n: types.Number{
			Value: value.Append(make([]byte, 0, digits), base),
		},
	}
}

// MakeBigRatValue builds a decimal value from a [big.Rat].
//
// There may be a loss of precision on rational numbers with an infinite decimal representation (e.g. 1/3).
func MakeBigRatValue(value *big.Rat) Value {
	f, _ := value.Float64()

	return MakeFloatValue(f)
}

// MakeBigFloatValue builds a decimal value from a [big.Float], without loss of precision.
func MakeBigFloatValue(value *big.Float) Value {
	const defaultDigits = 10 // sensible defaults for pre-allocation
	return Value{
		kind: token.Number,
		n: types.Number{
			Value: value.Append(make([]byte, 0, defaultDigits), 'g', -1),
		},
	}
}

// MakeBoolValue builds a value from a go bool.
func MakeBoolValue(value bool) Value {
	var b types.Boolean
	return Value{
		kind: token.Boolean,
		b:    b.With(value),
	}
}

// MakeScalarValue builds a value from a token provided by a JSON lexer.
//
// Supported token kinds are [token.String], [token.Number], [token.Boolean] and [token.Null].
//
// Passing other JSON tokens, such as delimiters will result in a [NullValue].
//
// This assumes that the token has been provided with temporary content: it clones the token value.
//
// See [MakeRawValue] for a version that doesn't clone the content.
func MakeScalarValue(t token.T) Value {
	k := t.Kind()
	switch k {
	case token.String:
		return Value{
			kind: k,
			s: types.String{
				Value: slices.Clone(t.Value()),
			},
		}
	case token.Number:
		return Value{
			kind: k,
			n: types.Number{
				Value: slices.Clone(t.Value()),
			},
		}
	case token.Boolean:
		return Value{
			kind: k,
			b:    types.Boolean{Value: t.Bool()},
		}
	case token.Null:
		fallthrough
	case token.Unknown, token.Delimiter, token.Key, token.EOF:
		fallthrough
	default:
		return NullValue
	}
}

// MakeRawValue builds a value from a token provided by a JSON lexer.
//
// Supported token kinds are [token.String], [token.Number], [token.Boolean] and [token.Null].
//
// Passing other JSON tokens, such as delimiters will result in a [NullValue].
//
// This assumes that the value is consumed before the token is discarded: content is not cloned.
// See [MakeScalarValue] for a version that clones the content, so the input token may be safely discarded.
func MakeRawValue(t token.T) Value {
	k := t.Kind()
	switch k {
	case token.String:
		return Value{
			kind: k,
			s: types.String{
				Value: t.Value(),
			},
		}
	case token.Number:
		return Value{
			kind: k,
			n: types.Number{
				Value: t.Value(),
			},
		}
	case token.Boolean:
		return Value{
			kind: k,
			b:    types.Boolean{Value: t.Bool()},
		}
	case token.Null:
		fallthrough
	case token.Unknown, token.Delimiter, token.Key, token.EOF:
		fallthrough
	default:
		return NullValue
	}
}

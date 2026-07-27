// Package writer exposes an implementation of the JSON writer interface [writers.Writer].
//
// It knows how to write JSON tokens [token.T] or [token.VT], JSON values [values.Value] and [values.InternedKey],
// JSON scalar types ([types.Boolean], [types.String], [types.Number], [types.NullType])
// or any go scalar value that fits as JSON.
//
// It handles proper JSON string escaping.
//
// It is NOT intended to write down as JSON complex structures such as objects or arrays, so you won't find options
// such as "should nil or empty slices or maps be rendered or not".
//
// Similarly, it handles "null" values as actual values. Absent data (i.e. a nil slice of bytes) is not considered
// a "null" value and is therefore not rendered at all.
package writer

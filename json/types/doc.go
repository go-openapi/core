// Package types defines common types to work with json.
//
// The JSON grammar defines four primitive types: strings, numbers, booleans and null.
//
// The null type may only have the single value "null".
//
// A value may be either defined or undefined. Notice that "null" doesn't mean undefined.
//
// An undefined value will marshal as empty in JSON, whereas a "null" will unmarshal as the "null" token.
//
// Strings, numbers and booleans may not be null. This package exposes a [Nullable] wrapper to support
// types that may be string or null, numbers or null and boolean or null.
package types

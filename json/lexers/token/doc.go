// Package token defines the JSON token types with their kind.
//
// These data structures should be returned by all implementations of a JSON lexer.
//
// There are two flavors of supported tokens:
//
// - the basic token type [T], which is suitable for most purposes
//
// The verbatim token type should be reserved to specific use-cases such as rendering verbatim documents
// (e.g. for linters, auto-fixers, editor plugins, etc.)
package token

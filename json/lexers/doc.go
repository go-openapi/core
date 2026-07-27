// Package lexers exposes interfaces for lexing JSON.
//
// The [Lexer] interface allows for picking a specific implementation of a JSON lexer.
//
// The default-lexer package exposes a regular lexer and verbatim lexer that should meet most requirements,
// with no external dependencies.
//
// For specific purposes, the contrib package holds independent modules implementing the lexer interface.
//
// All lexer implementations return a sequence of [token.T] (regular lexers),
// or [token.VT] (verbatim lexers).
package lexers

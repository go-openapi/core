// Package lexer provides a JSON lexer.
//
// The lexer splits a JSON input stream or a slice of bytes into tokens [token.T] (or [token.VT] for verbatim support).
//
// It checks that the input JSON is grammatically correct (so technically, this is a "parser").
//
// It keeps the context of errors.
//
// The lexer provides a low-level interface for projects which want to manipulate JSON directly, and do not necessarily
// want to unmarshal into go data structures.
//
// The lexer is designed to be low on memory usage: it should never need to allocate more memory than your longest
// string or number value in a stream.
//
// # Hardening against hostile input
//
// When lexing untrusted JSON, three resources may be abused independently; each has its own guard, and they compose:
//
//   - Total bytes consumed from a stream. This is not a concern of the lexer:
//     use the standard idiom and wrap the reader, e.g.
//
//     lex := lexer.New(io.LimitReader(r, maxBytes))
//
//     so the lexer simply sees EOF once the cap is reached. The buffer-based
//     constructors ([NewWithBytes], [BorrowLexerWithBytes]) are already bounded
//     by the caller-provided slice.
//
//   - Nesting depth. A stream of "[[[[..." would otherwise grow the container
//     stack (and wreck recursive consumers). Bound it with [WithMaxContainerStack].
//
//   - Peak per-value working memory. A single unbounded string or number value
//     (and, for the verbatim lexer, a flood of whitespace) is bounded with
//     [WithMaxValueBytes].
//
// These guards are off by default: a low-level lexer should not silently reject valid-but-large or deeply-nested
// documents.
// Opt in according to your threat model.
// A typical hardening recipe for an untrusted stream:
//
//	lex := lexer.New(
//		io.LimitReader(r, 16<<20),       // total input ceiling
//		lexer.WithMaxContainerStack(512), // nesting depth ceiling
//		lexer.WithMaxValueBytes(1<<20),   // per-value / whitespace ceiling
//	)
package lexer

// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

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
//
// # UTF-8
//
// RFC 8259 §8.1 requires JSON text to be UTF-8, and both lexers enforce it on string values: an ill-formed byte
// sequence, or a \u escape that does not denote a Unicode scalar value (an unpaired or inverted surrogate), is
// rejected by default with [codes.ErrInvalidUTF8] / [codes.ErrSurrogateEscape].
//
// Callers who would rather sanitize than fail can select [UTF8Replace], which substitutes U+FFFD (one per invalid
// byte, one per broken escape); [UTF8Passthrough] restores the unvalidated behavior for input already known to be
// well-formed. See [WithUTF8Policy].
//
// Validation is not a second pass over the value: detection of non-ASCII bytes is fused into the string scan the
// lexer already performs, so a pure-ASCII value — the overwhelmingly common case — is proven valid with no extra read
// and no call, and only values that actually carry a byte >= 0x80 reach the validator, where an AVX2 kernel handles
// them. Measured on the reference corpus, enabling it is free on ASCII-dominated documents and costs ~6% (L) / ~2%
// (VL) on heavily non-ASCII ones (twitter_status).
//
// # Document encoding
//
// The input must be UTF-8, as RFC 8259 §8.1 requires for interchange. Other encodings are out of scope: a caller
// holding UTF-16 or UTF-32 transcodes upstream, by wrapping the reader handed to [New] or [NewVerbatim] (for instance
// with golang.org/x/text/transform). Transcoding here instead would make [VL] emit a re-encoding of the document
// rather than the document, and would leave Offset/Line/Column addressing a stream the caller never had.
//
// A document opening with a UTF-16 or UTF-32 byte order mark is therefore not decoded but diagnosed: it is rejected
// with [codes.ErrNotUTF8], which is an error message rather than an input mode. A leading UTF-8 BOM is consumed
// before the first token and is not re-emitted by [VL] (RFC 8259 §8.1 asks implementations not to emit one).
package lexer

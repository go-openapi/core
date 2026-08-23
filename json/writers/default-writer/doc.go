// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

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
//
// # UTF-8
//
// RFC 8259 §8.1 requires JSON text to be UTF-8, so a writer that emits an ill-formed sequence emits a document no
// conforming parser has to accept. By default ([UTF8Strict]) the writers refuse such data with [ErrInvalidUTF8]
// instead of writing it; [UTF8Replace] substitutes U+FFFD (one per invalid byte) and [UTF8Passthrough] writes the
// bytes through unchecked. See [WithUTF8Policy].
//
// Note that a Go string may legally hold ill-formed UTF-8, so [Buffered.String] and [Buffered.StringBytes] can trip
// the default on data that arrived from a non-Unicode source. Before this policy existed the writers substituted
// U+FFFD silently and unconditionally — [UTF8Replace] is that old behavior, now explicit.
//
// The substitution rule is shared with the lexers, so a value sanitized on the way in and one sanitized on the way
// out are the same bytes.
//
// # What is checked, and the token loophole
//
// The policy applies to data the CALLER supplies: String, StringBytes, StringRunes, StringCopy, Raw and RawCopy.
//
// It does NOT apply to values that arrive in a lexer token — Token and VerbatimToken. The lexers validate string
// values as they scan, so re-checking them here would be pure overhead on the token-copy path that exists to be fast.
// That trust is only as good as the lexer's own policy, which leaves a deliberate loophole: if a document is lexed
// with lexer.UTF8Passthrough, its tokens may carry ill-formed bytes, and then
//
//   - Token does not report them, but the escaper decodes as it goes, so they are silently substituted with U+FFFD:
//     the output stays valid UTF-8 while differing from the input with no error;
//   - VerbatimToken writes the value byte-for-byte with no escaping at all — that is what verbatim means — so there
//     the ill-formed bytes DO reach the output.
//
// VerbatimValue is NOT in that group: it renders through the ordinary string path, so it is checked like any other
// caller data. If you lex with a relaxed policy and write the tokens out, validate at the boundary you care about.
//
// Numbers are also unchecked ([Buffered.NumberBytes], [Buffered.NumberCopy]): they are documented as taking whatever
// the caller provides, and a number containing non-ASCII is already invalid JSON for grammatical reasons.
package writer

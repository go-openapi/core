// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package lexer

import (
	"io"

	"github.com/go-openapi/core/json/lexers"
)

var _ lexers.Lexer = &VL{}

// VL is a verbatim lexer for JSON.
//
// It splits JSON input into tokens using NextToken().
//
// The lexer may operate from a stream of bytes (consuming from an io.Reader) or from a provided []byte buffer.
// Whenever consuming from an io.Reader, the stream is buffered so it is not useful to use a buffered reader.
//
// The lexer performs JSON grammar check such as opening/closing brackets, expected sequence of delimiter/values etc.
//
// # The verbatim pledge
//
// VL reproduces its input byte-for-byte: the token stream plus [VL.LeadingSpace] reassembles the source exactly, and
// every token value is a slice of the source with its escapes intact. Under the default [UTF8Strict] policy this is
// unconditional, because input that is not valid UTF-8 is rejected rather than emitted.
//
// Two documented exceptions:
//
//   - A leading UTF-8 BOM is consumed before any token exists, so it belongs to no token's leading space and is not
//     re-emitted. RFC 8259 §8.1 asks implementations not to emit a BOM, so this is the conformant direction.
//   - Under [UTF8Replace], an ill-formed byte sequence inside a string value is rewritten (see "Mangling" below).
//
// # Mangling under UTF8Replace, and what it costs
//
// [UTF8Replace] substitutes U+FFFD for each ill-formed byte in a string value. Only the offending value is affected —
// every other token still aliases the source — but for that value the byte-for-byte pledge is off, and so is the
// ability to map value bytes back onto source bytes:
//
//   - U+FFFD encodes as THREE bytes and replaces ONE. A value carrying k ill-formed bytes is 2k bytes longer than its
//     source span, so len(token.Value()) no longer equals the width of the text it came from. Multi-byte faults
//     compound this: a truncated three-byte sequence is three separate ill-formed bytes, hence three replacements and
//     nine bytes where the source had three.
//   - Consequently an index INTO a mangled value cannot be turned into a source offset by adding it to the token's
//     start, and the source text of such a value cannot be recovered from the token. If you need the original bytes,
//     use [UTF8Strict] (and handle the error) or [UTF8Passthrough] (and validate downstream yourself).
//
// What does NOT change: [VL.Line], [VL.Column], [L.Offset] and [VL.LeadingSpace] stay exact under every policy. They
// are derived from the scan cursor walking the source, never from the value, so token POSITIONS remain trustworthy
// even when a token's CONTENT has been rewritten. A linter or formatter can still point at the right place; it just
// cannot assume the value it holds is the text that was there.
//
// Escape text is never rewritten: VL keeps `\uXXXX` as source text, so a broken surrogate pair stays verbatim in the
// value and its U+FFFD appears only when the value is decoded with [token.Unescape].
type VL struct {
	*L
}

// NewVerbatim yields a new JSON verbatim lexer consuming from an io.Reader.
//
// The lexer performs some internal buffering on a fixed size buffer to call the reader on chunks.
//
// Use option WithBufferSize to alter the size of this buffer (defaults to 4KB).
//
// The verbatim lexer defaults to NOT eliding separators (unlike the semantic lexer [L]), so a faithful, round-trippable
// token stream is produced.
// A caller may still pass WithElideSeparator(true) to drop "," and ":" — this constructor only seeds the verbatim
// default, which the caller's options override.
//
// If you plan to allocate many lexers with a short life span, consider using the global pool with the
// [BorrowLexerWithBytes] / [BorrowLexerWithReader] functions and the returned redeem closure.
func NewVerbatim(r io.Reader, opts ...Option) *VL {
	l := new(VL)
	l.L = New(r, verbatimOpts(opts)...)
	l.reset()

	return l
}

// NewVerbatimWithBytes yields a new verbatim JSON lexer consuming from a provided fixed []bytes buffer.
//
// Since the full buffer is provided by the caller, there is no additional internal buffering.
//
// Like [NewVerbatim], separators are not elided by default; pass WithElideSeparator(true) to drop them.
//
// If you plan to allocate many lexers with a short life span, consider using the global pool with the
// BorrowLexerWithBytes() function and the returned redeem closure.
func NewVerbatimWithBytes(data []byte, opts ...Option) *VL {
	l := new(VL)
	l.L = NewWithBytes(data, verbatimOpts(opts)...)
	l.reset()

	return l
}

// verbatimOpts prepends the verbatim-specific default (do NOT elide separators) ahead of the caller's options, so a
// caller-supplied WithElideSeparator wins while the default flips from the semantic lexer's elide-on to elide-off.
func verbatimOpts(opts []Option) []Option {
	return append([]Option{WithElideSeparator(false)}, opts...)
}

// LeadingSpace returns the run of insignificant whitespace that preceded the most-recently-returned token (empty if
// none), sliced zero-copy from the input (valid until the next NextToken).
//
// For the EOF token it is the trailing whitespace of the document.
// This is what makes the token stream round-trippable without a per-token verbatim token.
func (l *VL) LeadingSpace() []byte { return l.blanks }

// Line yields the 1-based line number at which the most recently returned token starts (0 before the first token).
//
// The verbatim lexer maintains line/column accounting (the semantic lexer does not — see lexer.go); the cost is
// already paid, so these accessors are free.
func (l *VL) Line() int { return l.tokLine }

// Column yields the 1-based column at which the most recently returned token starts (0 before the first token).
//
// See [VL.Line].
func (l *VL) Column() int { return l.tokCol }

// Reset returns the verbatim lexer to a clean, source-less state for reuse, scrubbing the embedded L (which drops
// references to caller-supplied memory) and re-arming the verbatim-specific blanks tracking.
//
// See [L.Reset].
func (l *VL) Reset() {
	l.L.Reset()
	l.reset()
}

// ResetWithBytes rebinds the verbatim lexer to a new input buffer and resets all scanning state, so a single lexer can
// be reused across inputs with no allocation.
//
// See [L.ResetWithBytes].
func (l *VL) ResetWithBytes(data []byte) {
	l.L.ResetWithBytes(data)
	l.reset()
}

// ResetWithReader rebinds the verbatim lexer to a new reader and resets all scanning state, so a single lexer can be
// reused across inputs.
//
// See [L.ResetWithReader].
func (l *VL) ResetWithReader(r io.Reader) {
	l.L.ResetWithReader(r)
	l.reset()
}

func (l *VL) reset() {
	l.blanks = l.blanks[:0]
	// elideSeparator is seeded at construction (default false, see verbatimOpts) and preserved across L.Reset*; do NOT
	// clobber it here or a caller's WithElideSeparator(true) would be lost on reuse.
	// The unified core accumulates the preceding blanks, read off the embedded L.
	l.trackBlanks = true    // the cores read this off the embedded L (hot whitespace-skip path)
	l.in.TrackBlanks = true // consumeString (a *Input method) reads this to route to the raw scanners
}

// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package lexers

import (
	"iter"

	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/core/json/types"
)

// Lexer for JSON input.
//
// Lexer enforces the JSON grammar, and may ignore non-significant space and transform escaped unicode sequences as UTF8.
//
// Notice that NextToken() never returns any error: errors are transferred to the lexer's internal error state.
//
// The special token [token.EOF] indicates that the end of the input stream has been reached.
type Lexer interface {
	// NextToken returns the next token from the JSON input.
	NextToken() token.T

	// Tokens iterates over the JSON tokens up to (not including) EOF.
	//
	// The range also ends on error; as with NextToken, errors are kept in the
	// lexer's state (check Ok/Err after the loop).
	Tokens() iter.Seq[token.T]

	// Offset indicates the current position of the lexer in the input buffer or stream.
	Offset() uint64

	// IndentLevel indicates the current nesting level of containers (objects or arrays).
	IndentLevel() int

	// WithErrState reports about the internal error state of the lexer, using OK() or Err().
	types.WithErrState

	// ErrStateSetter indicates that the caller may inject an error state into the lexer using SetErr().
	//
	// This may be useful when building a hierarchy of JSON nodes that doesn't
	// want to allocate space for error handling at every node.
	types.ErrStateSetter

	// Resettable indicates that the lexer can be recycled, e.g. when used from a pool.
	types.Resettable
}

// VerbatimLexer for JSON input.
//
// Lexer enforces the JSON grammar, and maintains non-significant space and escaped UTF8 sequences.
//
// Notice that NextToken() never returns any error: errors are transferred to the lexer's internal error state.
//
// The special token [token.EOF] indicates that the end of the input stream has been reached.
type VerbatimLexer interface {
	// NextToken returns the next token from the JSON input, as a light [token.T]
	// with string/number values kept RAW (decode on demand with [token.Unescape]).
	NextToken() token.T

	// Tokens iterates over the JSON tokens up to (not including) EOF.
	//
	// The range also ends on error; as with NextToken, errors are kept in the
	// lexer's state (check Ok/Err after the loop).
	Tokens() iter.Seq[token.T]

	// LeadingSpace returns the whitespace run preceding the most-recently-returned
	// token (zero-copy, valid until the next NextToken) — the verbatim feature kept
	// as lexer state rather than baked into each token.
	LeadingSpace() []byte

	// Line and Column give the 1-based source position of the most-recent token's
	// start (0 before the first token).
	Line() int
	Column() int

	// Offset indicates the current position of the lexer
	Offset() uint64

	// WithErrState reports about the internal error state of the lexer, using OK() or Err().
	types.WithErrState

	// ErrStateSetter indicates that the caller may inject an error state into the lexer using SetErr().
	//
	// This may be useful when building a hierarchy of JSON nodes that doesn't
	// want to allocate space for error handling at every node.
	types.ErrStateSetter

	// Resettable indicates that the lexer can be recycled, e.g. when used from a pool.
	types.Resettable
}

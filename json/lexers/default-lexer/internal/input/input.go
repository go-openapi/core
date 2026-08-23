// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package input

import (
	"io"

	"github.com/go-openapi/core/json/internal/utf8x"
)

// Input holds the lexer's buffered-input and scan-cursor state: the buffer window and read position, the
// value-accumulation scratch, the reader and refill buffers, the error state, and the two grammar flags the string
// scanner consults to finish a value (ExpectKey/AfterKey).
//
// It is the cohesive, L-independent state the value scanners operate on; [L] embeds it as `in`.
// Its fields are exported because the scan cores (in package lexer) read and advance the cursor directly in their hot
// loops, and Input lives in its own internal package.
type Input struct {
	R   io.Reader
	Err error

	Buffer         []byte // determined by bufferSize
	CurrentValue   []byte // capped if MaxValueBytes > 0
	PreviousBuffer []byte // used when KeepPreviousBuffer > 0

	Offset     uint64
	Consumed   int
	Bufferized int

	WholeBuffer   bool // the buffer holds the entire input (no refills): values may alias it
	NeedFirstFill bool // streaming: the initial read (and whole-buffer short-circuit) is pending
	ExpectKey     bool
	AfterKey      bool // the previous token was an object key: a ':' must follow

	// TrackBlanks mirrors L.trackBlanks for ConsumeString's dispatch to the raw (validate-not-decode) string scanners.
	//
	// It is deliberately duplicated rather than moved: the cores read L.trackBlanks on the hot whitespace-skip path, and
	// keeping those refs off Input keeps the core codegen byte-identical.
	// Set by VL setup alongside L.trackBlanks; L.reset() does not clear it.
	TrackBlanks bool

	// NoAVX2 disables the AVX2 string-stop scanner (mirrors options.noAVX2), and MaxValueBytes / KeepPreviousBuffer mirror
	// the like-named options so the value scanners and ReadMore can enforce the caps/gates without reaching back into L.
	// The mirrored options are set in L.reset().
	//
	// Placed last so the hot cursor fields keep their offsets.
	NoAVX2             bool
	MaxValueBytes      int
	KeepPreviousBuffer int

	// UTF8Policy governs what happens to a string value that is not valid UTF-8. Mirrored from the like-named option in
	// L.reset(); the lexer package aliases this type so it is the single definition.
	UTF8Policy UTF8Policy

	// sanitized holds the U+FFFD-substituted rewrite of an ill-formed value under UTF8Replace.
	//
	// It is a buffer of its own rather than CurrentValue because on the unescaping paths the value to rewrite IS
	// CurrentValue. It is allocated lazily on the first ill-formed value, so a well-formed document never pays for it.
	sanitized []byte
}

// UTF8Policy is [utf8x.Policy]: the lexers and the writers share one definition so they cannot drift apart. The
// lexer package aliases this in turn, so callers name neither internal package.
type UTF8Policy = utf8x.Policy

// The policy values, re-exported so the scanners read UTF8Strict rather than utf8x.PolicyStrict.
const (
	UTF8Strict      = utf8x.PolicyStrict
	UTF8Replace     = utf8x.PolicyReplace
	UTF8Passthrough = utf8x.PolicyPassthrough
)

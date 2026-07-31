package writer

import (
	"fmt"
	"unicode/utf8"

	"github.com/go-openapi/core/json/internal/utf8x"
)

// UTF8Policy selects what a writer does with caller-supplied data that is not valid UTF-8.
//
// RFC 8259 §8.1 requires JSON text to be UTF-8, so a writer that emits an ill-formed sequence emits a document no
// conforming parser has to accept. It is the same type the lexers use ([lexer.UTF8Policy]) — a document refused on
// the way out and one rejected on the way in mean the same thing.
type UTF8Policy = utf8x.Policy

const (
	// UTF8Strict refuses to write ill-formed input: the writer goes into an error state reporting
	// [ErrInvalidUTF8], and every subsequent operation short-circuits. This is the default.
	//
	// Note that Go strings may legally hold ill-formed UTF-8, so String() and StringBytes() can trip this on data
	// that arrived from a non-Unicode source.
	UTF8Strict = utf8x.PolicyStrict

	// UTF8Replace substitutes U+FFFD for each invalid byte, so the output is always valid UTF-8 and no error is
	// raised. This is what the writers did unconditionally before the policy existed.
	UTF8Replace = utf8x.PolicyReplace

	// UTF8Passthrough writes ill-formed bytes through untouched and performs no validation.
	//
	// UNSAFE: the result is not valid JSON text. It exists for callers who have already validated their data and
	// want neither the check nor the substitution.
	UTF8Passthrough = utf8x.PolicyPassthrough
)

// checkUTF8 applies the policy to caller-supplied data before it is written.
//
// It reports whether writing may proceed; under [UTF8Strict] an ill-formed input puts the writer into an error state
// and returns false. Under the other policies it is a no-op — the escaper substitutes (or passes through) as it goes,
// so there is nothing to check up front.
func (w *commonWriter[T]) checkUTF8(data []byte) bool {
	if w.jw.escapePolicy() != UTF8Strict || utf8x.Valid(data) {
		return true
	}

	w.jw.SetErr(
		fmt.Errorf("%w at byte %d: %w", ErrInvalidUTF8, utf8x.FirstInvalid(data), ErrDefaultWriter),
	)

	return false
}

// rawUTF8 applies the policy to caller-supplied bytes that will be written WITHOUT escaping.
//
// It returns the bytes to write and whether writing may proceed. Well-formed input is returned unchanged, so the
// zero-copy pass-through of Raw is preserved for every valid payload; only an ill-formed one is rewritten, and only
// under [UTF8Replace], into the borrowed scratch buffer.
func (w *commonWriter[T]) rawUTF8(data []byte, scratch *[]byte) ([]byte, bool) {
	policy := w.jw.escapePolicy()
	if policy == UTF8Passthrough || utf8x.Valid(data) {
		return data, true
	}

	if policy == UTF8Strict {
		w.jw.SetErr(
			fmt.Errorf(
				"%w at byte %d: %w",
				ErrInvalidUTF8,
				utf8x.FirstInvalid(data),
				ErrDefaultWriter,
			),
		)

		return nil, false
	}

	*scratch = utf8x.Sanitize((*scratch)[:0], data)

	return *scratch, true
}

// finishRemainder deals with the bytes left at the end of a value that do not form a complete UTF-8 sequence.
//
// The escapers hand these back rather than guessing, because in a streamed value a chunk may legitimately end inside
// a sequence. Once there is nothing left to read, though, the sequence is truncated, and that is an ill-formed input
// like any other: refused under [UTF8Strict], substituted per byte under [UTF8Replace], written as-is under
// [UTF8Passthrough].
//
// It reports whether writing may continue.
func (w *commonWriter[T]) finishRemainder(remainder []byte) bool {
	switch w.jw.escapePolicy() {
	case UTF8Strict:
		w.jw.SetErr(fmt.Errorf("%w: input ends inside a multi-byte sequence (% x): %w",
			ErrInvalidUTF8, remainder, ErrDefaultWriter))

		return false

	case UTF8Passthrough:
		w.jw.writeBinary(remainder)

	default: // UTF8Replace
		var buf [utf8.UTFMax * utf8.UTFMax]byte
		w.jw.writeBinary(utf8x.Sanitize(buf[:0], remainder))
	}

	return w.jw.Ok()
}

// checkUTF8Chunk is [commonWriter.checkUTF8] for one chunk of a streamed value.
//
// A chunk may legitimately end mid-sequence — the caller's reader knows nothing of rune boundaries — so a fault that
// is merely a truncated tail is tolerated here and left to the rune-stitching in [commonWriter.StringCopy], which
// reports it if the input really does end there. Any other fault is a genuine error.
func (w *commonWriter[T]) checkUTF8Chunk(chunk []byte) bool {
	if w.jw.escapePolicy() != UTF8Strict {
		return true
	}

	idx := utf8x.FirstInvalid(chunk)
	if idx < 0 || !utf8.FullRune(chunk[idx:]) {
		return true
	}

	w.jw.SetErr(
		fmt.Errorf("%w at byte %d of this chunk: %w", ErrInvalidUTF8, idx, ErrDefaultWriter),
	)

	return false
}

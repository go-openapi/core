package lexer

import (
	"io"
	"iter"

	"github.com/go-openapi/core/json/expressions"
	"github.com/go-openapi/core/json/lexers"
	"github.com/go-openapi/core/json/lexers/token"
	"github.com/goccy/go-yaml/ast"
)

var _ lexers.Lexer = &YL{}

// YL is a lexer for YAML input that yields JSON-compatible tokens ([token.T]).
//
// It parses the whole YAML document with goccy/go-yaml, then walks the resolved AST,
// emitting the same token stream the JSON lexer would produce for the equivalent JSON:
// object/array delimiters, keys, and scalar values in document order.
//
// Unlike the JSON lexers, YL is SEMANTIC ONLY (there is no verbatim variant): string and
// key values are decoded, and YAML-specific constructs (comments, tags, the distinction
// between block and flow style) are not preserved. An [io.Reader] source is fully buffered
// (goccy has no streaming parser). Numbers are kept unconverted whenever their YAML
// spelling is already a valid JSON number; YAML-only spellings (hex, octal, binary,
// underscores, a leading "+", a leading/trailing dot) are normalised in-house.
//
// The zero value is not usable: construct with [New] / [NewWithBytes] or rebind an existing
// instance with [YL.ResetWithReader] / [YL.ResetWithBytes].
type YL struct {
	options

	data []byte // the full YAML source (buffered from a reader if needed)

	toks  []emit // the materialised token stream, built once on first use
	pos   int    // index of the next token to return
	built bool   // whether toks has been built for the current input
	err   error  // sticky lexer error state

	// bomBytes is 3 when the input opened with a UTF-8 byte order mark, which is stripped
	// before parsing and added back to reported positions. See walk.go:build.
	bomBytes int

	// snapshot of the most-recently-returned token, feeding Offset/IndentLevel/Line/Column
	cur emit

	// JSON pointer tracking (opt-in via WithJSONPointer). See pointer.go.
	trackPtr  bool
	ptr       expressions.Pointer
	ptrFrames []ptrFrame

	// build-time transient state (nil outside build): anchor table and the two alias-cycle
	// guards — the set of anchors currently being expanded, and the set of "<<" merge sources
	// whose entries are currently being flattened. See walk.go.
	anchors   map[string]ast.Node
	expanding map[string]bool
	merging   map[ast.Node]bool
}

// emit is one entry in the materialised token stream: the token plus the source position
// metadata surfaced through Offset/IndentLevel/Line/Column.
type emit struct {
	tok  token.T
	off  uint64 // byte offset of the token's source in the input
	line int    // 1-based source line
	col  int    // 1-based source column
	lvl  int    // container nesting level (IndentLevel) at this token
}

// New returns a YAML lexer consuming from an [io.Reader].
//
// The reader is fully read and buffered before parsing (goccy has no streaming parser), so
// there is no benefit in wrapping it with a buffered reader. To bound how many bytes are
// consumed from an untrusted reader, wrap it with [io.LimitReader].
func New(r io.Reader, opts ...Option) *YL {
	l := &YL{}
	l.options = applyWithDefaults(l.options, opts)
	l.reset()
	l.setReader(r)

	return l
}

// NewWithBytes returns a YAML lexer consuming from a provided buffer of bytes.
//
// The buffer is not copied; emitted string/number token values may alias it, so it must
// stay stable until the lexer is done with it.
func NewWithBytes(data []byte, opts ...Option) *YL {
	l := &YL{}
	l.options = applyWithDefaults(l.options, opts)
	l.data = data
	l.reset()

	return l
}

// NextToken returns the next token from the YAML input, or [token.EOFToken] once the stream
// is exhausted. As with the JSON lexer, errors are not returned: they are kept in the
// lexer's state (check Ok/Err).
func (l *YL) NextToken() token.T {
	if !l.built {
		l.build()
	}

	if l.pos >= len(l.toks) {
		if l.err != nil {
			return token.None
		}

		return token.EOFToken
	}

	e := l.toks[l.pos]
	l.pos++
	l.cur = e

	if l.trackPtr {
		l.updatePointer(e.tok)
	}

	return e.tok
}

// Tokens iterates over the YAML tokens up to (not including) EOF. The range also ends on
// error; check Ok/Err after the loop.
func (l *YL) Tokens() iter.Seq[token.T] {
	return func(yield func(token.T) bool) {
		if !l.built {
			l.build()
		}

		for l.pos < len(l.toks) {
			e := l.toks[l.pos]
			l.pos++
			l.cur = e
			if l.trackPtr {
				l.updatePointer(e.tok)
			}
			if !yield(e.tok) {
				return
			}
		}
	}
}

// Offset yields the byte offset of the most-recently-returned token in the input.
//
// Two YAML-specific caveats apply to all of Offset/Line/Column:
//   - tokens produced by expanding an alias (*x) or a merge key (<<) report the position of
//     the anchor DEFINITION, not the alias/merge usage site (expansion re-walks the original
//     node);
//   - a block-style closing delimiter (} or ]) reports 0: goccy records no position for it
//     (only flow-style {…}/[…] closers carry one).
func (l *YL) Offset() uint64 {
	return l.cur.off
}

// IndentLevel yields the current nesting level of containers (objects or arrays).
func (l *YL) IndentLevel() int {
	return l.cur.lvl
}

// Line and Column give the 1-based source position of the most-recently-returned token's
// start (0 before the first token). See [YL.Offset] for the alias/merge and block-closer caveats.
func (l *YL) Line() int   { return l.cur.line }
func (l *YL) Column() int { return l.cur.col }

// Ok reports whether no error has occurred so far.
func (l *YL) Ok() bool { return l.err == nil }

// Err returns any error that occurred during lexing.
func (l *YL) Err() error { return l.err }

// SetErr injects an error state into the lexer.
func (l *YL) SetErr(err error) { l.err = err }

// Reset returns the lexer to a clean, source-less state so it can be recycled. It drops the
// reference to caller-supplied memory (the input buffer) so a pooled lexer does not pin it.
func (l *YL) Reset() {
	l.data = nil
	l.reset()
}

// ResetWithBytes rebinds the lexer to a new input buffer and resets scanning state, so a
// single lexer can be reused across inputs. Configured options are kept.
func (l *YL) ResetWithBytes(data []byte) {
	l.data = data
	l.reset()
}

// ResetWithReader rebinds the lexer to a new reader (fully buffered) and resets scanning
// state. Configured options are kept.
func (l *YL) ResetWithReader(r io.Reader) {
	l.reset()
	l.setReader(r)
}

// setReader drains r into l.data. A read error is stored as the lexer error state and
// surfaced on the first token.
func (l *YL) setReader(r io.Reader) {
	data, err := io.ReadAll(r)
	l.data = data
	if err != nil {
		l.err = err
	}
}

// reset clears the per-input scanning state, preserving allocated capacity for reuse.
func (l *YL) reset() {
	l.toks = l.toks[:0]
	l.pos = 0
	l.built = false
	l.bomBytes = 0
	l.cur = emit{}
	l.err = nil

	l.trackPtr = l.jsonPointer
	l.ptr = l.ptr[:0]
	l.ptrFrames = l.ptrFrames[:0]
}

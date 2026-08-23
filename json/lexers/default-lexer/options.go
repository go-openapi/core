package lexer

import "github.com/go-openapi/core/json/lexers/default-lexer/internal/input"

type (
	// Option for the lexer.
	Option func(options) options

	options struct {
		bufferSize         int
		maxContainerStack  int
		maxValueBytes      int
		keepPreviousBuffer int
		elideSeparator     bool
		noAVX2             bool
		utf8Policy         UTF8Policy
		jsonPointer        bool
	}
)

// UTF8Policy selects what the lexer does with a string that is not valid UTF-8 — an ill-formed byte sequence in the
// source, or a \u escape that does not denote a Unicode scalar value.
//
// RFC 8259 §8.1 requires JSON text to be UTF-8, so the default ([UTF8Strict]) rejects such input. See
// [WithUTF8Policy].
type UTF8Policy = input.UTF8Policy

const (
	// UTF8Strict rejects the document: [L.Err] reports [codes.ErrInvalidUTF8] for ill-formed bytes, or
	// [codes.ErrSurrogateEscape] for a broken \u surrogate pair.
	//
	// This is the default for both [L] and [VL], and it never copies: validation only reads the value the scan already
	// produced, so the zero-copy aliasing of string values is unaffected.
	UTF8Strict = input.UTF8Strict

	// UTF8Replace accepts the document and substitutes U+FFFD (the replacement character) — one per invalid byte, one
	// per broken escape — so every emitted value is valid UTF-8.
	//
	// Only an offending value is rewritten (and therefore copied out of the input); valid values are still aliased with
	// zero copy.
	//
	// A mangled value no longer maps onto its source text: U+FFFD is three bytes replacing one, so its length differs
	// from the source span and an index into it cannot be turned into a source offset. Token POSITIONS are unaffected
	// — [L.Offset], [VL.Line], [VL.Column] and [VL.LeadingSpace] come from the scan cursor, never from the value. See
	// the [VL] documentation for the full contract.
	UTF8Replace = input.UTF8Replace

	// UTF8Passthrough disables raw-byte validation: ill-formed sequences are passed to the caller untouched, exactly as
	// the lexer behaved before UTF-8 validation existed.
	//
	// A \u escape still yields U+FFFD when it does not denote a scalar value, because an escape must always decode to
	// some rune — there is nothing to "pass through" for an escape, the escape text is not the value.
	//
	// UNSAFE: use it only for input already known to be valid UTF-8 (or when a downstream stage validates). Invalid
	// bytes will reach your []byte and silently become U+FFFD the moment the value is iterated as a string.
	UTF8Passthrough = input.UTF8Passthrough
)

// WithUTF8Policy selects how string values that are not valid UTF-8 are handled: rejected ([UTF8Strict], the default),
// sanitized to U+FFFD ([UTF8Replace]), or waved through unchecked ([UTF8Passthrough]).
//
// It applies to both the semantic lexer [L] and the verbatim lexer [VL], and it covers both ways a value can carry a
// non-character: ill-formed UTF-8 in the source bytes, and a \u escape that does not denote a Unicode scalar value
// (an unpaired or inverted surrogate).
//
// The strategy used to enforce the policy is not a knob: detection of non-ASCII bytes is fused into the string scan
// the lexer already performs, so an all-ASCII value — the overwhelmingly common case — is proven valid with no second
// pass. Only values that actually carry a byte >= 0x80 are validated.
func WithUTF8Policy(policy UTF8Policy) Option {
	return func(o options) options {
		o.utf8Policy = policy

		return o
	}
}

const defaultBufferBytes = 4096

// bufferSizeAlignment is the granularity to which a caller-supplied buffer size is rounded up.
//
// It covers one full AVX2 stride (32 bytes) — which also subsumes the 8-byte SWAR word, so that the streaming window
// is never smaller than a single vector or SWAR step.
//
// This keeps the in-window fast paths (string/number stop scans) on their vectorized/word footing for any
// WithBufferSize, and floors out the pathological tiny-window cases (a window narrower than a token separator) that
// would otherwise stress the byte-by-byte refill seam.
const bufferSizeAlignment = 32

// alignBufferSize rounds size up to the next multiple of [bufferSizeAlignment].
func alignBufferSize(size int) int {
	return (size + bufferSizeAlignment - 1) &^ (bufferSizeAlignment - 1)
}

var defaultOptions = options{ //nolint:gochecknoglobals // okay to preallocate immutable defaults as a global
	bufferSize:     defaultBufferBytes,
	elideSeparator: true,
}

// applyWithDefaults resolves the effective configuration: it seeds [defaultOptions], then applies opts left to right.
//
// It always starts from the defaults, so a lexer borrowed from the pool never inherits the options of the previous
// borrower.
//
// The options struct holds no pointer, so the seed copy and the returned value stay on the stack: building it costs no
// allocation.
func applyWithDefaults(opts []Option) options {
	o := defaultOptions

	for _, apply := range opts {
		o = apply(o)
	}

	return o
}

// WithElideSeparator controls whether the structural separators "," and ":" are emitted as tokens by the semantic lexer
// [L].
//
// When enabled (the default for [L]), these separators are validated against the JSON grammar but not surfaced by
// [L.NextToken] / [L.Tokens]: the token stream carries only values and the container delimiters "{", "}", "[", "]".
//
// This matches the behavior of the standard [encoding/json/jsontext] lexer and yields a simpler walk — the structure
// is unambiguous from the value/key tokens and the container delimiters.
//
// Disable it (WithElideSeparator(false)) to receive every separator token.
//
// The verbatim lexer [VL] honors this option too, but defaults to NOT eliding (elide-off) so its stream stays faithful
// and round-trippable.
// Pass WithElideSeparator(true) to a verbatim constructor to drop "," and ":" there as well; it is up to the caller to
// decide.
func WithElideSeparator(enabled bool) Option {
	return func(o options) options {
		o.elideSeparator = enabled

		return o
	}
}

// WithoutAVX2 disables the AVX2-accelerated long-string scan, keeping the lexer on the portable 8-byte SWAR path
// everywhere: once set, a long string value is scanned by the inline SWAR word loop instead of being handed to the
// vector kernel (see internal/strscan).
//
// The AVX2 gate is on by default on amd64 CPUs that support it: once a string value stays clean past a short inline
// probe it is scanned 32 bytes at a time, which is a large win on long values (descriptions, documentation, text
// payloads) and neutral on short ones.
// On non-amd64, or CPUs without AVX2, the SWAR path is already used and this option is a no-op.
//
// Most callers should leave the gate on.
// Reach for WithoutAVX2 when you want deterministic, CPU-independent behavior (identical code path regardless of the
// host's vector support — useful for reproducibility or differential testing), or if profiling a specific workload
// shows the vector path is not paying off.
//
// It does not change the token stream, only how the scan is performed.
func WithoutAVX2(disabled bool) Option {
	return func(o options) options {
		o.noAVX2 = disabled

		return o
	}
}

// WithBufferSize specifies the size in bytes of the internal buffer used by the lexer.
//
// The default is 4kB.
//
// The requested size is rounded up to the next multiple of 32 bytes (one AVX2 stride, which also subsumes the 8-byte
// SWAR word).
// This guarantees the streaming window is always at least one vector step wide, keeping the in-window string and number
// fast paths on their vectorized footing and avoiding pathologically small windows.
//
// A size <= 0 is ignored and the default is kept.
func WithBufferSize(size int) Option {
	return func(o options) options {
		if size > 0 {
			o.bufferSize = alignBufferSize(size)
		}

		return o
	}
}

// WithMaxContainerStack sets a circuit breaker on the maximum level of nested containers.
//
// This avoids edge cases when memory is exhausted by faulty inputs (e.g. a nasty stream pushes an infinite sequence of
// "{" or "[").
//
// The lexer allocates 8 bytes (a uint64) every additional 63 nested levels.
//
// NOTE: this option is primarily intended to secure the lexing of JSON streams, and should not be needed for lexers
// built with a full data buffer.
//
// The default value is zero: there is no maximum and no circuit breaker enabled.
func WithMaxContainerStack(maxDepth int) Option {
	return func(o options) options {
		o.maxContainerStack = maxDepth

		return o
	}
}

// WithMaxValueBytes sets a circuit breaker on the maximum size of a string or number.
//
// This avoids edge cases when memory is exhausted by faulty inputs (e.g. a nasty stream pushes an infinite sequence
// after an opening double quote).
//
// For the verbatim lexer, this also bounds the buffer that accumulates a run of non-significant whitespace, so a flood
// of blanks between tokens cannot exhaust memory either.
//
// NOTE: this option is primarily intended to secure the lexing of JSON streams, and should not be needed for lexers
// built with a full data buffer.
//
// It does NOT bound the total input size: to limit how many bytes are consumed from an [io.Reader], wrap it with
// [io.LimitReader] (see the package overview).
//
// The default value is zero: there is no maximum and no circuit breaker enabled.
func WithMaxValueBytes(size int) Option {
	return func(o options) options {
		o.maxValueBytes = size

		return o
	}
}

// WithJSONPointer enables tracking of the RFC 6901 JSON pointer of the current token.
//
// When enabled, both lexers ([L] and [VL]) maintain the path from the document root to the most-recently-returned
// token, retrievable as an [github.com/go-openapi/core/json/expressions.Pointer] via [L.JSONPointer].
// Only the current token's pointer is retained (there is no history).
//
// This is OFF by default.
// Enabling it FORFEITS the zero-allocation guarantee: the internal path stack allocates, and each retained object key
// is copied out of the reused value buffer and interned.
//
// Array indices stay as raw integers (no decimal formatting until the pointer is rendered), so arrays are cheaper than
// objects.
// Leave it off unless you need positional context.
//
// The pointer is valid only until the next token is read (like [VL.LeadingSpace] / [VL.Line]); clone it to retain it.
// See [L.JSONPointer] for the exact per-kind semantics.
func WithJSONPointer(enabled bool) Option {
	return func(o options) options {
		o.jsonPointer = enabled

		return o
	}
}

// WithEnsureErrorContext guarantees that all errors will get at least min(maxLength,bufferSize) bytes of context.
//
// This option maximizes the readability of the ErrorContext returned by ErrInContext(), at the cost of an extra copy of
// maxLength bytes every time an internal buffer is overturned.
//
// Without this, the lexer only remembers the current buffer, and the error context may be truncated for error that
// occur toward the beginning of the buffer.
//
// NOTE: this option is primarily intended to improve error readability on JSON streams, and is not needed by lexers
// built with a full data buffer.
//
// By default, the value is zero and there is no such buffer copy.
func WithEnsureErrorContext(maxLength int) Option {
	return func(o options) options {
		o.keepPreviousBuffer = maxLength

		return o
	}
}

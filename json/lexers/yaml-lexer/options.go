package lexer

type (
	// Option for the YAML lexer [YL].
	Option func(options) options

	options struct {
		maxContainerStack int
		maxValueBytes     int
		maxTokens         int
		jsonPointer       bool
	}
)

var defaultOptions = options{}

func applyWithDefaults(o options, opts []Option) options {
	o = defaultOptions
	for _, apply := range opts {
		o = apply(o)
	}

	return o
}

// WithMaxContainerStack sets a circuit breaker on the maximum level of nested containers.
//
// This avoids edge cases when memory is exhausted by faulty inputs (e.g. a document nesting
// thousands of mappings or sequences, or an alias that expands into deeply nested content).
//
// The default value is zero: there is no maximum and no circuit breaker enabled.
func WithMaxContainerStack(maxDepth int) Option {
	return func(o options) options {
		o.maxContainerStack = maxDepth

		return o
	}
}

// WithMaxValueBytes sets a circuit breaker on the maximum size of a single string or number value.
//
// This bounds the memory a single scalar may consume (e.g. a huge block scalar).
//
// The default value is zero: there is no maximum and no circuit breaker enabled.
func WithMaxValueBytes(size int) Option {
	return func(o options) options {
		o.maxValueBytes = size

		return o
	}
}

// WithMaxTokens sets a circuit breaker on the total number of tokens the lexer will emit
// for a single document.
//
// This is the primary defence against "billion laughs" alias-expansion attacks: [YL] resolves
// YAML anchors by inlining the anchored node at every alias, so a small document can expand into
// an exponential number of tokens. Neither [WithMaxContainerStack] (which bounds nesting depth)
// nor [WithMaxValueBytes] (which bounds a single scalar) bounds that fan-out — this does.
//
// The default value is zero: there is no maximum and no circuit breaker enabled. When lexing
// untrusted YAML that may contain anchors, set a sane budget.
func WithMaxTokens(maxTokens int) Option {
	return func(o options) options {
		o.maxTokens = maxTokens

		return o
	}
}

// WithJSONPointer enables tracking of the RFC 6901 JSON pointer of the current token.
//
// When enabled, [YL] maintains the path from the document root to the most-recently-returned
// token, retrievable as an [github.com/go-openapi/core/json/expressions.Pointer] via [YL.JSONPointer].
// Only the current token's pointer is retained (there is no history).
//
// This is OFF by default. The pointer is valid only until the next token is read; clone it to
// retain it. See [YL.JSONPointer] for the exact per-kind semantics.
func WithJSONPointer(enabled bool) Option {
	return func(o options) options {
		o.jsonPointer = enabled

		return o
	}
}

package lexer

import "fmt"

// Error is a sentinel error type for YAML-specific lexing conditions that have no
// counterpart in the JSON lexer's error-codes package.
type Error string

func (e Error) Error() string { return string(e) }

const (
	// ErrMultipleDocuments is returned when the YAML input holds more than one document
	// (separated by "---"): a JSON token stream has a single root.
	ErrMultipleDocuments Error = "YAML input holds multiple documents, but a JSON token stream has a single root"

	// ErrUnsupportedScalar is returned for YAML scalars with no JSON representation
	// (.inf, -.inf, .nan).
	ErrUnsupportedScalar Error = "YAML scalar has no JSON representation (infinity or NaN)"

	// ErrComplexKey is returned for a non-scalar mapping key (a sequence or mapping used
	// as a key via the "? :" explicit-key syntax): JSON object keys must be strings.
	ErrComplexKey Error = "YAML mapping key is not a scalar, but JSON object keys must be strings"

	// ErrInvalidNumber is returned when a node goccy typed as a number cannot be
	// normalised to a JSON number.
	ErrInvalidNumber Error = "YAML number cannot be represented as a JSON number"

	// ErrMaxTokens is returned when the total number of emitted tokens exceeds the
	// budget set by WithMaxTokens (billion-laughs circuit breaker).
	ErrMaxTokens Error = "circuit breaker stopped lexing because the maximum number of tokens has been reached"

	// ErrAliasCycle is returned when an alias resolves into one of its own ancestors,
	// which would expand forever.
	ErrAliasCycle Error = "YAML alias forms a cycle"

	// ErrUnknownAnchor is returned when an alias references an anchor that was never defined.
	ErrUnknownAnchor Error = "YAML alias references an unknown anchor"

	// ErrUnsupportedNode is returned for an AST node kind the lexer does not handle.
	ErrUnsupportedNode Error = "unsupported YAML node"
)

// parseError wraps an error reported by the goccy parser.
func parseError(err error) error {
	return fmt.Errorf("YAML parse error: %w", err)
}

// parsePanic wraps a value recovered from a panic in the goccy parser.
func parsePanic(r any) error {
	return fmt.Errorf("YAML parse panic: %v", r)
}

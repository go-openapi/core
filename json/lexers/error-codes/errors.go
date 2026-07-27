package codes

// LexerError is an error raised by a lexer.
//
// It tries to be as specific as it can regarding the cause of the detected issue.
type LexerError string

const (
	ErrUnterminatedString       LexerError = "unterminated string"
	ErrNoData                   LexerError = "no JSON value found in input"
	ErrNotInArray               LexerError = "mismatched ]"
	ErrNotInObject              LexerError = "mismatched }"
	ErrMissingObject            LexerError = "key must be defined in object"
	ErrInvalidToken             LexerError = "invalid JSON token" //#nosec
	ErrRepeatedComma            LexerError = "invalid repeated comma"
	ErrMissingComma             LexerError = "missing comma"
	ErrTrailingComma            LexerError = "invalid trailing comma"
	ErrMissingKey               LexerError = "missing key string"
	ErrMissingValue             LexerError = "missing value before comma"
	ErrInvalidExponent          LexerError = "invalid exponent in number"
	ErrRepeatedExponent         LexerError = "duplicate exponent in number"
	ErrRepeatedDecimalSeparator LexerError = "duplicate decimal separator in number"
	ErrInvalidFractional        LexerError = "invalid fractional part after decimal separator in number"
	ErrInvalidSign              LexerError = "invalid sign of integer part in number"
	ErrLeadingZero              LexerError = "forbidden leading zero for integer part in number"
	ErrMissingInteger           LexerError = "number has no integer part"
	ErrControlChar              LexerError = "unescaped control character in string"
	ErrUnicodeEscape            LexerError = "invalid unicode escape sequence"
	ErrUnknownEscape            LexerError = "unknown escape sequence"
	ErrInvalidRune              LexerError = "invalid rune in unicode escape sequence"
	ErrSurrogateEscape          LexerError = "expected an escaped UTF-16 code point to come as a surrogate pair"
	ErrDelimitedValue           LexerError = "value should follow a delimiter"
	ErrCommaInContainer         LexerError = "comma should appear within an object or an array"
	ErrMaxContainerStack        LexerError = "circuit breaker stopped parsing JSON because the maximum depth of nested containers has been reached"
	ErrMaxValueBytes            LexerError = "circuit breaker stopped parsing JSON because the maximum size for a string or number value has been reached"
	ErrKeyColon                 LexerError = "object key must be followed by a :"
)

// Error implements the error interface.
func (e LexerError) Error() string {
	return string(e)
}

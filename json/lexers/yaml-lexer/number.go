package lexer

import (
	"math/big"
	"strings"
)

// isJSONNumber reports whether s is a valid JSON number (RFC 8259):
//
//	-? ( 0 | [1-9][0-9]* ) ( \.[0-9]+ )? ( [eE][-+]?[0-9]+ )?
//
// It is the fast path for numbers: a YAML integer/float whose source spelling already
// obeys this grammar is emitted verbatim (unconverted), and a plain YAML scalar that
// matches it is promoted to a Number token.
func isJSONNumber(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	if s[i] == '-' {
		i++
	}
	// integer part: 0 alone, or a non-zero digit followed by digits (no leading zero)
	if i >= len(s) {
		return false
	}
	switch {
	case s[i] == '0':
		i++
	case s[i] >= '1' && s[i] <= '9':
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	default:
		return false
	}
	// fractional part
	if i < len(s) && s[i] == '.' {
		i++
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return false
		}
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	// exponent part
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return false
		}
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}

	return i == len(s)
}

// convertYAMLInt normalises a YAML integer spelling (goccy's raw token value) into a
// canonical JSON integer. base is chosen from goccy's token type (10, 16, 8 or 2).
//
// YAML additions handled: underscores between digits, a leading "+", and the base
// prefixes 0x/0o/0b (and legacy leading-zero octal). math/big makes the conversion exact
// regardless of magnitude — so integers too large for int64/uint64 round-trip losslessly.
func convertYAMLInt(raw string, base int) ([]byte, bool) {
	s := strings.ReplaceAll(raw, "_", "")

	neg := false
	switch {
	case strings.HasPrefix(s, "-"):
		neg = true
		s = s[1:]
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	}

	const (
		baseDecimal = 10
		baseHexa    = 16
		baseOctal   = 8
		baseBinary  = 2
	)

	switch base {
	case baseHexa:
		s = trimBasePrefix(s, 'x')
	case baseOctal:
		s = trimBasePrefix(s, 'o')
	case baseBinary:
		s = trimBasePrefix(s, 'b')
	}

	if s == "" {
		return nil, false
	}

	var z big.Int
	if _, ok := z.SetString(s, base); !ok {
		return nil, false
	}
	if neg {
		z.Neg(&z)
	}

	return z.Append(nil, baseDecimal), true
}

// convertYAMLFloat normalises a YAML float spelling into a canonical JSON number, purely
// textually so the digits are preserved exactly (no float64 round-trip).
//
// YAML additions handled: underscores, a leading "+", a leading dot (".5" → "0.5") and a
// trailing dot ("5." → "5.0").
func convertYAMLFloat(raw string) ([]byte, bool) {
	s := strings.ReplaceAll(raw, "_", "")

	sign := ""
	switch {
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	case strings.HasPrefix(s, "-"):
		sign = "-"
		s = s[1:]
	}

	mant := s
	exp := ""
	if k := strings.IndexAny(s, "eE"); k >= 0 {
		mant, exp = s[:k], s[k:]
	}

	if mant == "" || mant == "." {
		return nil, false
	}
	if strings.HasPrefix(mant, ".") {
		mant = "0" + mant
	}
	if strings.HasSuffix(mant, ".") {
		mant += "0"
	}

	out := sign + mant + exp
	if !isJSONNumber(out) {
		return nil, false
	}

	return []byte(out), true
}

// trimBasePrefix removes a "0x"/"0o"/"0b" style prefix (letter given, case-insensitive)
// if present. Legacy leading-zero octal ("012") has no letter prefix and is left as-is
// for big.Int to parse in base 8.
func trimBasePrefix(s string, letter byte) string {
	if len(s) >= 2 && s[0] == '0' && (s[1] == letter || s[1] == letter-32) {
		return s[2:]
	}

	return s
}

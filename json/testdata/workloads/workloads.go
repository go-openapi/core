// Package workloads generates deterministic JSON payloads used to stress the
// lexer benchmarks along different axes (numbers, strings, escapes, nesting,
// keys, whitespace, ...).
//
// Each workload is generated to be roughly the same size in bytes, so that the
// per-byte throughput (MB/s) reported by the benchmarks is comparable across
// workload shapes, not just across implementations.
package workloads

import (
	"fmt"
	"strconv"
	"strings"
)

// targetBytes is the approximate size each generated workload aims for.
const targetBytes = 256 * 1024

// Workload is a named JSON payload.
type Workload struct {
	Name string
	Data []byte
}

// All returns the full set of workloads, each ~256KiB of valid JSON.
func All() []Workload {
	return []Workload{
		{Name: "ints", Data: arrayOf(intElem)},
		{Name: "floats", Data: arrayOf(floatElem)},
		{Name: "strings_plain", Data: arrayOf(plainStringElem)},
		{Name: "strings_escaped", Data: arrayOf(escapedStringElem)},
		{Name: "strings_escaped_long", Data: arrayOf(escapedLongStringElem)},
		{Name: "strings_unicode", Data: arrayOf(unicodeStringElem)},
		{Name: "strings_uescaped", Data: arrayOf(uEscapedStringElem)},
		{Name: "bools_nulls", Data: arrayOf(boolNullElem)},
		{Name: "object_keys", Data: objectKeys()},
		{Name: "nested_arrays", Data: nested("[", "]")},
		{Name: "nested_objects", Data: nestedObjects()},
		{Name: "whitespace_heavy", Data: whitespaceHeavy()},
		{Name: "mixed", Data: mixed()},
	}
}

// Micro returns the laser-focused micro-benchmark suite: small, single-path
// workloads that each exercise one code path of the lexer (a number shape, a
// string shape, or a short-token/dispatch shape), so a unitary design choice can
// be judged in isolation before it is validated on the real-world [Corpus]. Each
// payload is still ~256KiB so per-byte throughput stays comparable across shapes.
//
// These deliberately overlap the string workloads in [All] (same generators) and
// add the number splits (positive/negative ints, decimals, exponential) and the
// short-token splits (nulls, bools, separators) that [All] only covers in
// aggregate. Kept separate from [Suite] so the heavy corpus gauntlet does not
// balloon; wire into [Suite] only at gate reviews if a full-matrix run is wanted.
func Micro() []Workload {
	return []Workload{
		// numbers — one grammar shape each
		{Name: "ints_pos", Data: arrayOf(intPosElem)},
		{Name: "ints_neg", Data: arrayOf(intNegElem)},
		{Name: "decimals", Data: arrayOf(decimalElem)},
		{Name: "exponential", Data: arrayOf(expElem)},
		// strings — same generators as All(), the path-specific shapes
		{Name: "strings_plain", Data: arrayOf(plainStringElem)},
		{Name: "strings_escaped", Data: arrayOf(escapedStringElem)},
		{Name: "strings_escaped_long", Data: arrayOf(escapedLongStringElem)},
		{Name: "strings_unicode", Data: arrayOf(unicodeStringElem)},
		{Name: "strings_uescaped", Data: arrayOf(uEscapedStringElem)},
		// short tokens — dispatch-dominated
		{Name: "nulls", Data: arrayOf(nullElem)},
		{Name: "bools", Data: arrayOf(boolElem)},
		{Name: "separators", Data: arrayOf(separatorElem)},
	}
}

// arrayOf builds a JSON array "[e0,e1,...]" by appending elements until the
// target size is reached. elem renders element i.
func arrayOf(elem func(i int) string) []byte {
	var b strings.Builder
	b.Grow(targetBytes + 64)
	b.WriteByte('[')

	for i := 0; b.Len() < targetBytes; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(elem(i))
	}

	b.WriteByte(']')

	return []byte(b.String())
}

func intElem(i int) string {
	// vary sign and magnitude
	n := int64(i*2654435761) % 1_000_000_007
	return strconv.FormatInt(n, 10)
}

func floatElem(i int) string {
	// mantissa + fraction + exponent, kept as text (no rounding intent)
	return fmt.Sprintf("%d.%04de%d", i%1000, (i*7)%10000, (i%18)-9)
}

// intPosElem renders pure non-negative integers (no sign, no leading zeros). The
// uint64 cast keeps the wrapped product positive, unlike intElem which is signed.
func intPosElem(i int) string {
	n := uint64(i*2654435761) % 1_000_000_007

	return strconv.FormatUint(n, 10)
}

// intNegElem renders pure negative integers ("-N", N >= 1). Exercises the leading
// '-' fast-path branch distinct from the all-digits one.
func intNegElem(i int) string {
	n := int64(i*2654435761) % 1_000_000_007
	if n < 0 {
		n = -n
	}

	return strconv.FormatInt(-n-1, 10)
}

// decimalElem renders fixed-point decimals (no exponent), half of them negative:
// "-1234.5678". Exercises the integer-part + '.' + fraction grammar without the
// scientific-notation tail.
func decimalElem(i int) string {
	sign := ""
	if i%2 == 0 {
		sign = "-"
	}

	return fmt.Sprintf("%s%d.%04d", sign, i%100000, (i*7)%10000)
}

// expElem renders numbers in scientific notation, cycling the legal shapes: lower
// and upper 'e'/'E', signed exponents, a negative mantissa, and the "-0.NNeNN"
// form (a leading-zero integer part is legal — only multi-digit leading zeros are
// not). This drives the full number slow path.
func expElem(i int) string {
	switch i % 4 {
	case 0:
		return fmt.Sprintf("%de%d", i%1000, (i%18)-9) // 123e-5
	case 1:
		return fmt.Sprintf("%dE%d", i%1000, (i%12)-6) // 123E4
	case 2:
		return fmt.Sprintf("-0.%02de%d", (i%99)+1, (i%10)+1) // -0.44e10 shape
	default:
		return fmt.Sprintf("%d.%04dE-%d", i%100, i%10000, (i%9)+1) // 12.3456E-3
	}
}

func nullElem(int) string { return "null" }

func boolElem(i int) string {
	if i%2 == 0 {
		return "true"
	}

	return "false"
}

// separatorElem alternates empty containers so the workload is almost entirely
// structural delimiters separated by commas: "[{},[],{},[],...]". With separator
// elision on (the default) the emitted tokens are the '{ } [ ]' delimiters — the
// dispatch-dominated path, minimal value work.
func separatorElem(i int) string {
	if i%2 == 0 {
		return "{}"
	}

	return "[]"
}

func plainStringElem(i int) string {
	return fmt.Sprintf("%q", fmt.Sprintf("item-%08d-value", i))
}

func escapedStringElem(i int) string {
	// embed escapes the lexer must process: quote, backslash, control shorthands
	return fmt.Sprintf(`"line\t%d\ncol\\\"%d\"end"`, i, i)
}

// escapedLongStringElem renders a string with a couple of escapes up front
// followed by a long run of clean bytes. This is the worst case for a
// byte-by-byte unescape loop and the best case for clean-run batching: once the
// leading escapes are handled, the long tail should be copied in one bulk append
// rather than one append per byte.
func escapedLongStringElem(i int) string {
	const tail = "the quick brown fox jumps over the lazy dog and keeps on running far past the edge of the field"
	return fmt.Sprintf(`"hdr\t%d\n%s %s %08d"`, i, tail, tail, i)
}

func unicodeStringElem(i int) string {
	// literal multibyte UTF-8 (NOT \u escapes), incl. an astral rune every few
	// elements — exercises the zero-copy alias path, not the unescape path.
	if i%4 == 0 {
		return fmt.Sprintf(`"snow☃ clef𝄞 n%04d"`, i%10000)
	}
	return fmt.Sprintf(`"accentéèê x%04d"`, i%10000)
}

// uEscapedStringElem renders a string using \uXXXX escapes, including a UTF-16
// surrogate pair (U+1D11E MUSICAL SYMBOL G CLEF = 𝄞) every fourth
// element. This is the only workload that exercises the \u decode + surrogate
// path under benchmark.
func uEscapedStringElem(i int) string {
	if i%4 == 0 {
		// surrogate pair + BMP accented letters, all as \u escapes
		return fmt.Sprintf(`"clef𝄞 néèê %04d"`, i%10000)
	}
	return fmt.Sprintf(`"accentéèê x%04d"`, i%10000)
}

func boolNullElem(i int) string {
	switch i % 3 {
	case 0:
		return "true"
	case 1:
		return "false"
	default:
		return "null"
	}
}

func objectKeys() []byte {
	var b strings.Builder
	b.Grow(targetBytes + 64)
	b.WriteByte('{')

	for i := 0; b.Len() < targetBytes; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `"field_%08d":%d`, i, i)
	}

	b.WriteByte('}')

	return []byte(b.String())
}

// nested builds an array nested to a depth that reaches the target size, then
// closes it. open/closing are the matching delimiters.
func nested(open, closing string) []byte {
	depth := targetBytes / 2

	var b strings.Builder
	b.Grow(2*depth + 8)
	b.WriteString(strings.Repeat(open, depth))
	b.WriteString(strings.Repeat(closing, depth))

	return []byte(b.String())
}

func nestedObjects() []byte {
	// {"k":{"k":{...1...}}} nested deeply
	const wrap = `{"k":`
	depth := targetBytes / (len(wrap) + 1)

	var b strings.Builder
	b.Grow(targetBytes + 16)
	b.WriteString(strings.Repeat(wrap, depth))
	b.WriteByte('1')
	b.WriteString(strings.Repeat("}", depth))

	return []byte(b.String())
}

func whitespaceHeavy() []byte {
	// a small object pretty-printed with deep, repeated indentation
	const indent = "\n\t\t\t\t\t\t\t\t"

	var b strings.Builder
	b.Grow(targetBytes + 64)
	b.WriteByte('[')

	for i := 0; b.Len() < targetBytes; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(indent)
		fmt.Fprintf(&b, "%d", i%100)
	}

	b.WriteString("\n")
	b.WriteByte(']')

	return []byte(b.String())
}

func mixed() []byte {
	// array of small heterogeneous objects, the closest to real-world payloads
	var b strings.Builder
	b.Grow(targetBytes + 128)
	b.WriteByte('[')

	for i := 0; b.Len() < targetBytes; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b,
			`{"id":%d,"name":"user_%06d","active":%t,"score":%d.%02d,"tags":["a","b"],"note":null}`,
			i, i, i%2 == 0, i%1000, i%100,
		)
	}

	b.WriteByte(']')

	return []byte(b.String())
}

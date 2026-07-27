package writer

import (
	"bytes"
	"io"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"
)

// chunkReader yields its data in fixed-size chunks, forcing multi-byte UTF-8 runes to be
// split across Read boundaries. With chunk == 1 every multi-byte rune is split, which
// exercises the rune-stitching path of StringCopy independently of the internal read
// buffer size.
type chunkReader struct {
	data  []byte
	pos   int
	chunk int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}

	end := min(r.pos+r.chunk, len(r.data))
	n := copy(p, r.data[r.pos:end])
	r.pos += n

	return n, nil
}

// TestCompleteRuneSize is a regression test for the UTF-8 lead-byte width detection.
//
// The previous implementation used "any bit set" masks and returned 4 for almost every
// byte, which broke StringCopy's rune stitching across read boundaries.
func TestCompleteRuneSize(t *testing.T) {
	cases := []struct {
		name string
		b    byte
		want int
	}{
		{"ascii", 'a', 0},
		{"ascii high", 0x7F, 0},
		{"continuation 0x80", 0x80, 0},
		{"continuation 0xA9", 0xA9, 0},
		{"continuation 0xBF", 0xBF, 0},
		{"2-byte lead 0xC2", 0xC2, 2},
		{"2-byte lead 0xC3", 0xC3, 2},
		{"2-byte lead 0xDF", 0xDF, 2},
		{"3-byte lead 0xE0", 0xE0, 3},
		{"3-byte lead 0xE2", 0xE2, 3},
		{"3-byte lead 0xEF", 0xEF, 3},
		{"4-byte lead 0xF0", 0xF0, 4},
		{"4-byte lead 0xF4", 0xF4, 4},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, completeRuneSize(c.b))
		})
	}
}

// TestStringCopySplitRunes is a regression test for multi-byte runes split across the
// read boundaries of StringCopy. It covers 2-, 3- and 4-byte runes as well as escaped
// characters interleaved with multi-byte runes, for both the Unbuffered and Buffered
// writers, at several chunk sizes.
func TestStringCopySplitRunes(t *testing.T) {
	// Inputs use \u/\U escapes for the wider runes so the source stays plain (CJK/emoji
	// glyphs upset gosmopolitan); the encoded UTF-8 bytes are what matter here.
	inputs := map[string]string{
		"2-byte runes":     "héllo wörld",                    // U+00E9, U+00F6
		"3-byte runes":     "→ \u4e16\u754c ←",               // arrows + CJK
		"4-byte runes":     "\U0001f30d\U0001f680\U0001f600", // astral plane
		"mixed widths":     "aé→\u4e16\U0001f30dz",           // 1,2,3,3,4,1 byte runes
		"escape and runes": "a\té\n\u4e16\"\U0001f30d\\end",  // escapes + runes
		"leading rune":     "é at start",
		"trailing rune":    "ends with \u4e16\u754c",
	}

	// chunk sizes that split runes at every possible offset (1 guarantees every multi-byte
	// rune straddles a boundary; 2/3/5 split the wider runes).
	chunks := []int{1, 2, 3, 5}

	writers := []struct {
		name string
		make func(io.Writer) interface {
			StringCopy(io.Reader)
			Err() error
		}
		flush func(any)
	}{
		{
			name: "unbuffered",
			make: func(w io.Writer) interface {
				StringCopy(io.Reader)
				Err() error
			} {
				return NewUnbuffered(w)
			},
		},
		{
			name: "buffered",
			make: func(w io.Writer) interface {
				StringCopy(io.Reader)
				Err() error
			} {
				return NewBuffered(w)
			},
			flush: func(w any) { _ = w.(*Buffered).Flush() },
		},
	}

	for _, wc := range writers {
		t.Run(wc.name, func(t *testing.T) {
			for name, input := range inputs {
				for _, chunk := range chunks {
					t.Run(name, func(t *testing.T) {
						var buf bytes.Buffer
						jw := wc.make(&buf)

						jw.StringCopy(&chunkReader{data: []byte(input), chunk: chunk})
						require.NoError(t, jw.Err())

						if wc.flush != nil {
							wc.flush(jw)
						}

						// StringCopy must yield a complete, correctly escaped JSON string with
						// the multi-byte content reassembled byte-for-byte.
						assert.Equal(t, expectedJSONString(input), buf.String())
					})
				}
			}
		})
	}
}

// TestStringCopyTruncatedRuneErrors checks that a genuinely truncated multi-byte rune at
// end of input (no further bytes available to complete it) is still reported as an error,
// for both writers. This guards against the fix to the split-rune stitching swallowing
// real truncation.
func TestStringCopyTruncatedRuneErrors(t *testing.T) {
	// "abc" followed by a lone 2-byte lead byte with no continuation byte.
	truncated := append([]byte("abc"), 0xC3)

	t.Run("unbuffered", func(t *testing.T) {
		var buf bytes.Buffer
		jw := NewUnbuffered(&buf)
		jw.StringCopy(&chunkReader{data: truncated, chunk: 64})
		require.Error(t, jw.Err())
	})

	t.Run("buffered", func(t *testing.T) {
		var buf bytes.Buffer
		jw := NewBuffered(&buf)
		jw.StringCopy(&chunkReader{data: truncated, chunk: 64})
		require.Error(t, jw.Err())
	})
}

// expectedJSONString renders s as the JSON string the writers are expected to produce:
// enclosed in double quotes, with the package's escaping rules applied. Multi-byte runes
// are emitted verbatim.
func expectedJSONString(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	escaped, remainder := escapedBytes([]byte(s), make([]byte, 0, len(s)))
	out = append(out, escaped...)
	out = append(out, remainder...) // none expected for complete inputs
	out = append(out, '"')

	return string(out)
}

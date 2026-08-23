// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package utf8x

import (
	"testing"
	"unicode/utf8"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"
)

// invalidSamples are the ill-formed byte sequences the JSON lexers used to accept silently — the ten string bodies of
// JSONTestSuite's i_string_* UTF-8 cases, plus a few shapes those do not cover.
//
// firstBad is the index of the first byte of the first ill-formed sequence.
var invalidSamples = []struct { //nolint:gochecknoglobals // shared by several tests
	name     string
	in       []byte
	firstBad int
	// want is the sanitized form: one U+FFFD per invalid byte (see [Sanitize]).
	want string
}{
	{name: "lone ff", in: []byte{0xff}, firstBad: 0, want: "�"},
	{name: "lone continuation", in: []byte{0x81}, firstBad: 0, want: "�"},
	{name: "iso latin-1", in: []byte{0xe9}, firstBad: 0, want: "�"},
	{name: "truncated 3-byte", in: []byte{0xe0, 0xff}, firstBad: 0, want: "��"},
	{
		name:     "valid prefix then invalid lead",
		in:       []byte{0xe6, 0x97, 0xa5, 0xd1, 0x88, 0xfa},
		firstBad: 5,
		want:     "\u65e5\u0448\ufffd",
	},
	{name: "encoded surrogate U+D800", in: []byte{0xed, 0xa0, 0x80}, firstBad: 0, want: "���"},
	{name: "beyond U+10FFFF", in: []byte{0xf4, 0xbf, 0xbf, 0xbf}, firstBad: 0, want: "����"},
	{name: "overlong 2 bytes", in: []byte{0xc0, 0xaf}, firstBad: 0, want: "��"},
	{
		name:     "obsolete 6-byte form",
		in:       []byte{0xfc, 0x83, 0xbf, 0xbf, 0xbf, 0xbf},
		firstBad: 0,
		want:     "������",
	},
	{
		name:     "overlong NUL, 6 bytes",
		in:       []byte{0xfc, 0x80, 0x80, 0x80, 0x80, 0x80},
		firstBad: 0,
		want:     "������",
	},
	// shapes the conformance corpus does not carry
	{name: "truncated at end", in: []byte("ok\xe2\x82"), firstBad: 2, want: "ok��"},
	{
		name:     "invalid between valid",
		in:       []byte("a\xffbéc"),
		firstBad: 1,
		want:     "a�béc",
	},
	{
		// the maximal-subpart rule would give 2 replacements here; we deliberately give 3 (one per byte).
		name:     "partial 3-byte then invalid",
		in:       []byte{0xe0, 0xa0, 0xff},
		firstBad: 0,
		want:     "���",
	},
}

var validSamples = []struct { //nolint:gochecknoglobals // shared by several tests
	name string
	in   []byte
}{
	{name: "empty", in: []byte{}},
	{name: "ascii", in: []byte("the quick brown fox")},
	{name: "2-byte", in: []byte("café naïve")},
	{name: "3-byte", in: []byte("\u65e5\u672c\u8a9e")},
	{name: "4-byte", in: []byte("\U0001D11E\U0001F600")},
	{name: "encoded U+FFFD", in: []byte("�")}, // a legitimate replacement char is valid input
	{name: "max scalar", in: []byte("\U0010FFFF")},
	{name: "NUL and DEL", in: []byte{0x00, 0x7f}},
	{name: "BOM as content", in: []byte("\uFEFF")},
}

func TestValid(t *testing.T) {
	t.Parallel()

	for _, tc := range validSamples {
		t.Run("valid/"+tc.name, func(t *testing.T) {
			t.Parallel()
			assert.True(t, Valid(tc.in))
			assert.Equal(t, -1, FirstInvalid(tc.in))
		})
	}

	for _, tc := range invalidSamples {
		t.Run("invalid/"+tc.name, func(t *testing.T) {
			t.Parallel()
			assert.False(t, Valid(tc.in))
			assert.Equal(t, tc.firstBad, FirstInvalid(tc.in))
		})
	}
}

func TestSanitize(t *testing.T) {
	t.Parallel()

	t.Run("valid input is appended verbatim", func(t *testing.T) {
		t.Parallel()
		for _, tc := range validSamples {
			assert.Equal(t, string(tc.in), string(Sanitize(nil, tc.in)))
			assert.Equal(t, -1, SanitizedLen(tc.in))
		}
	})

	t.Run("invalid input is replaced per byte", func(t *testing.T) {
		t.Parallel()
		for _, tc := range invalidSamples {
			got := Sanitize(nil, tc.in)
			assert.Equalf(t, tc.want, string(got), "sanitizing %q", tc.in)
			assert.Truef(t, utf8.Valid(got), "sanitized output must be valid: %q", got)
			assert.Equalf(
				t,
				len(got),
				SanitizedLen(tc.in),
				"SanitizedLen must predict the rewrite of %q",
				tc.in,
			)
		}
	})

	t.Run("appends to an existing buffer", func(t *testing.T) {
		t.Parallel()
		dst := []byte("prefix:")
		assert.Equal(t, "prefix:a�b", string(Sanitize(dst, []byte("a\xffb"))))
	})

	t.Run("is idempotent", func(t *testing.T) {
		t.Parallel()
		for _, tc := range invalidSamples {
			once := Sanitize(nil, tc.in)
			assert.Equal(t, string(once), string(Sanitize(nil, once)))
		}
	})
}

// TestSanitizeMatchesWriterRule pins the granularity choice against the rule Go itself applies when a caller ranges
// over a string — the reason we replace per byte rather than per maximal subpart.
func TestSanitizeMatchesWriterRule(t *testing.T) {
	t.Parallel()

	for _, tc := range invalidSamples {
		var want []byte
		for _, r := range string(tc.in) { // range over a string yields U+FFFD per invalid byte
			want = utf8.AppendRune(want, r)
		}
		assert.Equalf(t, string(want), string(Sanitize(nil, tc.in)),
			"Sanitize must agree with range-over-string for %q", tc.in)
	}
}

// FuzzValid pins the two invariants the whole design rests on: our verdict is the stdlib's verdict, and a sanitized
// value is always valid UTF-8. When the AVX2 kernel replaces the scalar Valid, this is the test that keeps it honest.
func FuzzValid(f *testing.F) {
	for _, tc := range validSamples {
		f.Add(tc.in)
	}
	for _, tc := range invalidSamples {
		f.Add(tc.in)
	}
	f.Add([]byte("\xf0\x9f\x98\x80 mixed \xc3 tail"))

	f.Fuzz(func(t *testing.T, in []byte) {
		require.Equal(t, utf8.Valid(in), Valid(in))

		idx := FirstInvalid(in)
		require.Equal(t, utf8.Valid(in), idx < 0)
		if idx >= 0 {
			require.True(
				t,
				utf8.Valid(in[:idx]),
				"the prefix before the first fault must itself be valid",
			)
		}

		out := Sanitize(nil, in)
		require.True(t, utf8.Valid(out), "sanitized output must always be valid UTF-8")
		require.Equal(t, string(out), string(Sanitize(nil, out)), "sanitizing must be idempotent")

		if idx < 0 {
			require.Equal(
				t,
				string(in),
				string(out),
				"valid input must be passed through unchanged",
			)
			require.Equal(t, -1, SanitizedLen(in))
		} else {
			require.Equal(t, len(out), SanitizedLen(in))
		}
	})
}

// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package utf8x

import (
	"bytes"
	"fmt"
	"io"
	"testing"
	"unicode/utf8"
)

// BenchmarkValid compares this package's validator against the stdlib across the value sizes the lexer actually hands
// it, so the AVX2 length gate (avx2Min) can be set from measurement rather than guesswork.
//
// The lexer only calls in here for values that already contain a byte >= 0x80 (detection is fused into the string
// scan), so the interesting inputs are non-ASCII text, not ASCII.
func BenchmarkValid(b *testing.B) {
	corpora := []struct {
		name string
		unit string
	}{
		// a Latin word with accents: mostly ASCII with a sprinkle of 2-byte sequences, the common European shape
		{name: "latin", unit: "les donn\u00e9es d'entr\u00e9e sont valid\u00e9es "},
		// CJK: dense 3-byte sequences, the twitter_status shape
		{name: "cjk", unit: "\u65e5\u672c\u8a9e\u306e\u30c6\u30ad\u30b9\u30c8 "},
		// emoji: 4-byte sequences
		{name: "emoji", unit: "\U0001F600\U0001F601\U0001F602 "},
	}
	sizes := []int{16, 32, 48, 64, 96, 128, 256, 512, 1024, 4096}

	var sink bool
	for _, c := range corpora {
		for _, size := range sizes {
			data := bytes.Repeat([]byte(c.unit), size/len(c.unit)+1)[:size]
			// never cut a multi-byte sequence: the benchmark must measure valid input
			for len(data) > 0 && !utf8.Valid(data) {
				data = data[:len(data)-1]
			}

			b.Run(fmt.Sprintf("%s/%d/utf8x", c.name, size), func(b *testing.B) {
				b.SetBytes(int64(len(data)))
				for b.Loop() {
					sink = Valid(data)
				}
			})
			b.Run(fmt.Sprintf("%s/%d/stdlib", c.name, size), func(b *testing.B) {
				b.SetBytes(int64(len(data)))
				for b.Loop() {
					sink = utf8.Valid(data)
				}
			})
		}
	}

	fmt.Fprint(io.Discard, sink)
}

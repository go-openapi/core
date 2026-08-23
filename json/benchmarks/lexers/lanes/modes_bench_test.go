// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package lane

// Compass benchmark (§10.5): a FAIR matrix of every way to drive the two lexers,
// across the whole corpus, so we can see which usage laggards on which payloads.
//
// 8 modes = {L, VL} × {buffer, reader} × {push (Tokens), pull (NextToken)}:
//
//	L/buffer/push   devirt push core (whole-buffer champion; iterator.go fast path)
//	L/buffer/pull   scanTokenBufferG (yield->return port of the push core)
//	L/reader/push   Tokens() over a reader falls through to the NextToken loop
//	                (iterator.go:47) — pull + a range-over-func closure, NOT a
//	                distinct optimized path; expected ≈ L/reader/pull
//	scanTokenStreamG (§10.3 streaming fast paths)
//	VL/...          same four with the verbatim policy (blanks + line/col baked in)
//
// Fairness: each lexer is constructed ONCE and Reset per iteration (buffer aliases
// the input; reader reuses the internal window across resets), and a single
// bytes.Reader is rewound with Reset — so NO per-iteration construction / window
// allocation is charged, only steady-state scanning. Reader modes use the default
// 4KB window. THROWAWAY compass instrument; delete before retrofit to reference.

import (
	"bytes"
	"fmt"
	"io"
	"testing"

	lexer "github.com/go-openapi/core/json/lexers/default-lexer"
	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/core/json/testdata/workloads"
)

func BenchmarkLexerModes(b *testing.B) {
	var modeSink int
	suite, err := workloads.Corpus()
	if err != nil {
		b.Fatal(err)
	}

	for _, wl := range suite {
		data := wl.Data

		b.Run(wl.Name, func(b *testing.B) {
			// --- semantic lexer L ---

			b.Run("L/buffer/push", func(b *testing.B) {
				lx := lexer.NewWithBytes(data)
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					lx.ResetWithBytes(data)
					for t := range lx.Tokens() {
						modeSink += int(t.Kind())
					}
				}
			})

			b.Run("L/buffer/pull", func(b *testing.B) {
				lx := lexer.NewWithBytes(data)
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					lx.ResetWithBytes(data)
					for {
						t := lx.NextToken()
						if !lx.Ok() || t.Kind() == token.EOF {
							break
						}
						modeSink += int(t.Kind())
					}
				}
			})

			b.Run("L/reader/push", func(b *testing.B) {
				var br bytes.Reader
				br.Reset(data)
				lx := lexer.New(&br)
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					br.Reset(data)
					lx.ResetWithReader(&br)
					for t := range lx.Tokens() {
						modeSink += int(t.Kind())
					}
				}
			})

			b.Run("L/reader/pull", func(b *testing.B) {
				var br bytes.Reader
				br.Reset(data)
				lx := lexer.New(&br)
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					br.Reset(data)
					lx.ResetWithReader(&br)
					for {
						t := lx.NextToken()
						if !lx.Ok() || t.Kind() == token.EOF {
							break
						}
						modeSink += int(t.Kind())
					}
				}
			})

			// --- state-based verbatim lexer VS (§10.5b): the future VL. Same verbatim
			// feature (raw values + blanks + position via accessors), light token.T.
			// The old token.VT-based VL is no longer measured (on death row, §10.5b). ---

			b.Run("VL/buffer/push", func(b *testing.B) {
				lx := lexer.NewVerbatimWithBytes(data)
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					lx.ResetWithBytes(data)
					for t := range lx.Tokens() {
						modeSink += int(t.Kind())
					}
				}
			})

			b.Run("VL/buffer/pull", func(b *testing.B) {
				lx := lexer.NewVerbatimWithBytes(data)
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					lx.ResetWithBytes(data)
					for {
						t := lx.NextToken()
						if !lx.Ok() || t.Kind() == token.EOF {
							break
						}
						modeSink += int(t.Kind())
					}
				}
			})

			b.Run("VL/reader/push", func(b *testing.B) {
				var br bytes.Reader
				br.Reset(data)
				lx := lexer.NewVerbatim(&br)
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					br.Reset(data)
					lx.ResetWithReader(&br)
					for t := range lx.Tokens() {
						modeSink += int(t.Kind())
					}
				}
			})

			b.Run("VL/reader/pull", func(b *testing.B) {
				var br bytes.Reader
				br.Reset(data)
				lx := lexer.NewVerbatim(&br)
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					br.Reset(data)
					lx.ResetWithReader(&br)
					for {
						t := lx.NextToken()
						if !lx.Ok() || t.Kind() == token.EOF {
							break
						}
						modeSink += int(t.Kind())
					}
				}
			})
		})
	}

	fmt.Fprint(io.Discard, modeSink)
}

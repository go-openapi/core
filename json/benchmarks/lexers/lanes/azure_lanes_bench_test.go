// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package lane

// Azure-lanes benchmark: a single payload (the merged go-openapi/azure OpenAPI spec)
// driven through EVERY variant/option lane of the default lexer, so a companion
// benchviz chart can show the relative cost of each knob — and, run twice (plain vs
// PGO build), the PGO uplift per lane. See benchviz/azure_lanes/README.md.
//
// Restricting to one payload is deliberate: the lane matrix is large, and holding the
// payload fixed isolates the lane/option/PGO effect from workload variance. Azure is
// chosen because its long-description / short-key shape exercises every path that
// matters (AVX2 long-string scan, whitespace skip, keys, structure).
//
// Fairness mirrors modes_bench_test.go: each lexer is constructed ONCE and Reset per
// iteration (buffer aliases the input; reader rewinds a single bytes.Reader and reuses
// the internal window), so only steady-state scanning is charged.

import (
	"bytes"
	"fmt"
	"io"
	"iter"
	"testing"

	lexer "github.com/go-openapi/core/json/lexers/default-lexer"
	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/core/json/testdata/workloads"
)

// laneLexer is the surface both L and VL expose, so one pair of drain helpers serves
// every lane regardless of lexer or input mode.
type laneLexer interface {
	Tokens() iter.Seq[token.T]
	NextToken() token.T
	Ok() bool
	ResetWithBytes([]byte)
	ResetWithReader(io.Reader)
}

func drainPush(lx laneLexer) {
	var azureSink int
	for t := range lx.Tokens() {
		azureSink += int(t.Kind())
	}
	fmt.Fprint(io.Discard, azureSink)
}

func drainPull(lx laneLexer) {
	var azureSink int
	for {
		t := lx.NextToken()
		if !lx.Ok() || t.Kind() == token.EOF {
			break
		}
		azureSink += int(t.Kind())
	}
	fmt.Fprint(io.Discard, azureSink)
}

func runBuffer(b *testing.B, data []byte, lx laneLexer, drain func(laneLexer)) {
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		lx.ResetWithBytes(data)
		drain(lx)
	}
}

func runReader(b *testing.B, data []byte, lx laneLexer, br *bytes.Reader, drain func(laneLexer)) {
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		br.Reset(data)
		lx.ResetWithReader(br)
		drain(lx)
	}
}

type lane struct {
	id  string
	run func(b *testing.B, data []byte)
}

// azureLanes enumerates the lanes. IDs are chosen so no ID is a substring of another
// (sem/vrb rather than L/VL, distinct option tokens), so the benchviz config can match
// each lane unambiguously.
func azureLanes() []lane {
	return []lane{
		// core matrix: {sem,vrb} × {buffer,reader} × {push,pull}, default options.
		{"sem-buffer-push-dflt", func(b *testing.B, data []byte) {
			runBuffer(b, data, lexer.NewWithBytes(data), drainPush)
		}},
		{"sem-buffer-pull-dflt", func(b *testing.B, data []byte) {
			runBuffer(b, data, lexer.NewWithBytes(data), drainPull)
		}},
		{"sem-reader-push-dflt", func(b *testing.B, data []byte) {
			var br bytes.Reader
			br.Reset(data)
			runReader(b, data, lexer.New(&br), &br, drainPush)
		}},
		{"sem-reader-pull-dflt", func(b *testing.B, data []byte) {
			var br bytes.Reader
			br.Reset(data)
			runReader(b, data, lexer.New(&br), &br, drainPull)
		}},
		{"vrb-buffer-push-dflt", func(b *testing.B, data []byte) {
			runBuffer(b, data, lexer.NewVerbatimWithBytes(data), drainPush)
		}},
		{"vrb-buffer-pull-dflt", func(b *testing.B, data []byte) {
			runBuffer(b, data, lexer.NewVerbatimWithBytes(data), drainPull)
		}},
		{"vrb-reader-push-dflt", func(b *testing.B, data []byte) {
			var br bytes.Reader
			br.Reset(data)
			runReader(b, data, lexer.NewVerbatim(&br), &br, drainPush)
		}},
		{"vrb-reader-pull-dflt", func(b *testing.B, data []byte) {
			var br bytes.Reader
			br.Reset(data)
			runReader(b, data, lexer.NewVerbatim(&br), &br, drainPull)
		}},

		// options on the champion base (sem-buffer-push).
		{"sem-buffer-push-noavx2", func(b *testing.B, data []byte) {
			runBuffer(b, data, lexer.NewWithBytes(data, lexer.WithoutAVX2(true)), drainPush)
		}},
		{"sem-buffer-push-pointer", func(b *testing.B, data []byte) {
			runBuffer(b, data, lexer.NewWithBytes(data, lexer.WithJSONPointer(true)), drainPush)
		}},
		{"sem-buffer-push-noelide", func(b *testing.B, data []byte) {
			runBuffer(b, data, lexer.NewWithBytes(data, lexer.WithElideSeparator(false)), drainPush)
		}},

		// options on the verbatim base (vrb-buffer-push).
		{"vrb-buffer-push-pointer", func(b *testing.B, data []byte) {
			runBuffer(
				b,
				data,
				lexer.NewVerbatimWithBytes(data, lexer.WithJSONPointer(true)),
				drainPush,
			)
		}},
		{"vrb-buffer-push-elide", func(b *testing.B, data []byte) {
			runBuffer(
				b,
				data,
				lexer.NewVerbatimWithBytes(data, lexer.WithElideSeparator(true)),
				drainPush,
			)
		}},
	}
}

// azureData returns the single payload these lanes measure (the compact merged Azure
// OpenAPI spec).
func azureData(tb testing.TB) []byte {
	tb.Helper()
	suite, err := workloads.Corpus()
	if err != nil {
		tb.Fatal(err)
	}
	for _, wl := range suite {
		if wl.Name == "azure_swagger" {
			return wl.Data
		}
	}
	tb.Fatal("azure_swagger workload not found")

	return nil
}

// BenchmarkAzureLanes drains the Azure payload once per lane. Run it twice — a plain
// build and a PGO build — and tag the two outputs to chart the PGO uplift (see
// benchviz/azure_lanes/README.md for the exact commands).
func BenchmarkAzureLanes(b *testing.B) {
	data := azureData(b)
	for _, ln := range azureLanes() {
		b.Run(ln.id, func(b *testing.B) {
			ln.run(b, data)
		})
	}
}

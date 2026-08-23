// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package lexer

import (
	"iter"
	"reflect"
	"strings"
	"testing"

	"github.com/go-openapi/core/json/lexers/token"
)

// scanPushSemanticGeneric / scanPushVerbatimGeneric are the A/B BASELINE push
// entries: non-inlined shims around the GENERIC core scanPushG (un-monomorphized,
// so the policy calls route through the generics dictionary). They exist only to
// measure the devirtualization gap against the production scanPush{Semantic,Verbatim}
// (which wrap the generated concrete cores). Test-only — that is why they live here
// and not in the package sources. Same //go:noinline yield-seam discipline as the
// production wrappers (see push.go).
//
//go:noinline
func (l *L) scanPushSemanticGeneric(yield func(token.T) bool) {
	scanPushG[token.T, semanticPolicy](l, semanticPolicy{}, yield)
}

//go:noinline
func (l *L) scanPushVerbatimGeneric(yield func(token.T) bool) {
	scanPushG[token.T, verbatimPolicy](l, verbatimPolicy{}, yield)
}

// tokensGeneric / VL.tokensGeneric mirror the pre-adoption Tokens() (the generic push shim).
//
// Retained as the A/B baseline now that Tokens() routes through the devirtualized core; used by the push equivalence
// test and BenchmarkDevirt.
func (l *L) tokensGeneric() iter.Seq[token.T] {
	return func(yield func(token.T) bool) {
		if l.in.WholeBuffer && l.maxValueBytes == 0 {
			l.scanPushSemanticGeneric(yield)

			return
		}
		for {
			tok := l.nextTokenGeneric()
			if l.in.Err != nil || tok.Kind() == token.EOF {
				return
			}
			if !yield(tok) {
				return
			}
		}
	}
}

func (l *VL) tokensGeneric() iter.Seq[token.T] {
	return func(yield func(token.T) bool) {
		if l.in.WholeBuffer && l.maxValueBytes == 0 {
			l.scanPushVerbatimGeneric(yield)

			return
		}
		for {
			tok := l.nextTokenGeneric()
			if l.in.Err != nil || tok.Kind() == token.EOF {
				return
			}
			if !yield(tok) {
				return
			}
		}
	}
}

// nextTokenGeneric / VL.nextTokenGeneric drive the generic pull cores directly, dispatching on wholeBuffer exactly as
// NextToken does (§10) so the generic↔devirt equivalence guard covers both the buffer and the stream lane.
//
// Retained as the A/B baseline now that NextToken() routes through the devirt cores.
func (l *L) nextTokenGeneric() token.T {
	if l.in.WholeBuffer {
		return scanTokenBufferG[token.T, semanticPolicy](l, semanticPolicy{})
	}

	return scanTokenStreamG[token.T, semanticPolicy](l, semanticPolicy{})
}

func (l *VL) nextTokenGeneric() token.T {
	if l.in.WholeBuffer {
		return scanTokenBufferG[token.T, verbatimPolicy](l.L, verbatimPolicy{})
	}

	return scanTokenStreamG[token.T, verbatimPolicy](l.L, verbatimPolicy{})
}

func collectPull(l *L) ([]token.T, error) {
	var toks []token.T
	for {
		t := l.NextToken()
		if !l.Ok() {
			return toks, l.Err()
		}
		if t.Kind() == token.EOF {
			return toks, nil
		}
		toks = append(toks, t)
	}
}

func collectPullGeneric(l *L) ([]token.T, error) {
	var toks []token.T
	for {
		t := l.nextTokenGeneric()
		if !l.Ok() {
			return toks, l.Err()
		}
		if t.Kind() == token.EOF {
			return toks, nil
		}
		toks = append(toks, t)
	}
}

// TestDevirtEquivalencePull asserts the devirtualized pull core returns the exact same token stream and terminal error
// as the generic core, for L, across whole- buffer and streaming modes.
func TestDevirtEquivalencePull(t *testing.T) {
	for _, in := range devirtInputs() {
		data := []byte(in)

		gen, genErr := collectPullGeneric(NewWithBytes(data))
		dev, devErr := collectPull(NewWithBytes(data)) // collectPull uses NextToken = devirt
		if !sameErr(genErr, devErr) {
			t.Errorf("pull whole-buffer %q: err mismatch: generic=%v devirt=%v", in, genErr, devErr)
		}
		if !reflect.DeepEqual(gen, dev) {
			t.Errorf(
				"pull whole-buffer %q: token stream mismatch\n generic=%v\n devirt =%v",
				in,
				gen,
				dev,
			)
		}

		// streaming mode (tiny buffer to force refills)
		genS, genSErr := collectPullGeneric(New(strings.NewReader(in), WithBufferSize(4)))
		devS, devSErr := collectPull(New(strings.NewReader(in), WithBufferSize(4)))
		if !sameErr(genSErr, devSErr) {
			t.Errorf("pull streaming %q: err mismatch: generic=%v devirt=%v", in, genSErr, devSErr)
		}
		if !reflect.DeepEqual(genS, devS) {
			t.Errorf(
				"pull streaming %q: token stream mismatch\n generic=%v\n devirt =%v",
				in,
				genS,
				devS,
			)
		}
	}
}

// TestDevirtEquivalencePush asserts the devirtualized push core (whole-buffer fast path) matches the generic push core
// for L.
func TestDevirtEquivalencePush(t *testing.T) {
	for _, in := range devirtInputs() {
		data := []byte(in)

		var gen []token.T
		lg := NewWithBytes(data)
		for tok := range lg.tokensGeneric() {
			gen = append(gen, tok)
		}

		var dev []token.T
		ld := NewWithBytes(data)
		for tok := range ld.Tokens() { // Tokens() is the devirt path post-adoption
			dev = append(dev, tok)
		}

		if !sameErr(lg.Err(), ld.Err()) {
			t.Errorf("push %q: err mismatch: generic=%v devirt=%v", in, lg.Err(), ld.Err())
		}
		if !reflect.DeepEqual(gen, dev) {
			t.Errorf("push %q: token stream mismatch\n generic=%v\n devirt =%v", in, gen, dev)
		}
	}
}

// TestDevirtEquivalenceVerbatim asserts the verbatim devirt cores (pull + push) match the generic verbatim cores,
// including blanks and position.
func TestDevirtEquivalenceVerbatim(t *testing.T) {
	for _, in := range devirtInputs() {
		data := []byte(in)

		// pull
		var genP []token.T
		vg := NewVerbatimWithBytes(data)
		for {
			tok := vg.nextTokenGeneric()
			if !vg.Ok() || tok.Kind() == token.EOF {
				break
			}
			genP = append(genP, tok)
		}
		var devP []token.T
		vd := NewVerbatimWithBytes(data)
		for {
			tok := vd.NextToken() // NextToken = devirt post-adoption
			if !vd.Ok() || tok.Kind() == token.EOF {
				break
			}
			devP = append(devP, tok)
		}
		if !sameErr(vg.Err(), vd.Err()) {
			t.Errorf("verbatim pull %q: err mismatch: generic=%v devirt=%v", in, vg.Err(), vd.Err())
		}
		if !reflect.DeepEqual(genP, devP) {
			t.Errorf(
				"verbatim pull %q: token stream mismatch\n generic=%v\n devirt =%v",
				in,
				genP,
				devP,
			)
		}

		// push
		var genPush []token.T
		vgp := NewVerbatimWithBytes(data)
		for tok := range vgp.tokensGeneric() {
			genPush = append(genPush, tok)
		}
		var devPush []token.T
		vdp := NewVerbatimWithBytes(data)
		for tok := range vdp.Tokens() { // devirt path post-adoption
			devPush = append(devPush, tok)
		}
		if !reflect.DeepEqual(genPush, devPush) {
			t.Errorf(
				"verbatim push %q: token stream mismatch\n generic=%v\n devirt =%v",
				in,
				genPush,
				devPush,
			)
		}
	}
}

// devirtInputs exercise every dispatch arm and value path, plus malformed inputs so the error/none path is compared
// too.
func devirtInputs() []string {
	return []string{
		// structure
		`{}`, `[]`, `[[],[],{}]`, `{"k":{"k":{"k":1}}}`,
		`{"a":1,"b":2,"c":3}`,
		// strings
		`"plain ascii"`, `"esc\t\n\"\\\/\b\f\r"`, `"accentéèê snow☃"`, `"clef𝄞 x"`,
		`["item-0001","item-0002","line\t1\ncol\"2\"end"]`,
		// numbers
		`[0,1,-1,42,-42,3.14,-3.14,1e10,1E-10,-0.44e10,12.3456E-3]`,
		// short tokens + whitespace
		`[true,false,null]`, "[\n\t 1 ,\n\t 2 \n]",
		// keys + nesting + mixed
		`{"id":1,"name":"x","active":true,"score":1.5,"tags":["a","b"],"note":null}`,
		// malformed: each must put both paths into the same error state
		`{`, `[1,]`, `{"k"1}`, `tru`, `01`, `"unterminated`, `[1 2]`, `{,}`,
	}
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}

	return a.Error() == b.Error()
}

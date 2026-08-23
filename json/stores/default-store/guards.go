// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

//go:build guards || writerguards

package store

import (
	"bytes"
	"fmt"
	"math"

	"github.com/go-openapi/core/json/stores/values"
)

const (
	maxAddressable = ^uint64(
		0,
	) >> (headerBits + lengthBits) // 2^(64-24)-1 or 1 099 511 627 775 bytes ~ 1 TB
	maxArenaSize = int(uint64(math.MaxInt) & maxAddressable) // 2^32-1 or 2^40-1
)

// code assertions turned on

func assertCompressDeflateError(err error) {
	if err != nil {
		panic(fmt.Errorf("internal compression error: %w", err))
	}
}

func assertCompressInflateError(err error) {
	if err != nil {
		panic(fmt.Errorf("corrupted compressed data: %w", err))
	}
}

func assertCompressOptionWriter(err error) {
	if err != nil {
		panic(fmt.Errorf("invalid compression options: %w: %w", err, ErrStore))
	}
}

func assertInlineASCIISize(value []byte) {
	if len(value) != maxInlineBytes+1 {
		panic(fmt.Errorf("inlined ASCII requires %d bytes strings", maxInlineBytes+1))
	}
}

func assertInlineASCIIUnpackSize(size int) {
	if size != maxInlineBytes {
		panic(
			fmt.Errorf(
				"unpack ASCII requires a size of exactly %d bytes characters. Got %d",
				maxInlineBytes+1,
				size,
			),
		)
	}
}

/*
func assertInlineASCIIBufferLength(out []byte) {
	if len(out) < maxInlineBytes+1 {
		panic(fmt.Errorf("buffer passed to unpackASCII should have len=%d: %w", maxInlineBytes+1, ErrStore))
	}
}
*/

func assertInlinePackBytes(value []byte) {
	if len(value) > maxInlineBytes {
		panic(fmt.Errorf("pack at most %d bytes in an inlined payload", maxInlineBytes))
	}
}

func assertInlinePackBlanks(value []byte) {
	if len(value) > maxInlineBlanks {
		panic(fmt.Errorf("pack at most %d blank characters in an inlined payload", maxInlineBlanks))
	}
}

func assertOffsetAddressable(offset int) {
	if offset > maxArenaSize {
		panic(fmt.Errorf("offset is too large for the space addressable in arena: %w", ErrStore))
	}
}

func assertVerbatimOnlyBlanks(blanks []byte) {
	if bytes.ContainsFunc(blanks, func(r rune) bool {
		switch r {
		case blank, tab, carriageReturn, lineFeed:
			return false
		default:
			return true
		}
	}) {
		panic(fmt.Errorf("passed a blank string, but this contained non-blanks: %w", ErrStore))
	}
}

func assertVerbatimIsBlank(b byte) {
	switch b {
	case blank, tab, carriageReturn, lineFeed:
		return
	default:
		panic(fmt.Errorf("expectd a blank character, but got: %x: %w", b, ErrStore))
	}
}

func assertBlankHeader(header uint8) {
	if header != headerInlinedBlank && header != headerCompressedBlank { // not a blank string
		panic(
			fmt.Errorf(
				"expected a header representing blank values, but got: %d: %w",
				header,
				ErrStore,
			),
		)
	}
}

// assertValidValue verifies that the passed value is legit and not malformed.
func assertValidValue(v values.Value) {
	panic(
		fmt.Errorf(
			"invalid value kind passed to PutValue. Must be a scalar value. Got %v: %w",
			v.Kind(),
			ErrStore,
		),
	)
}

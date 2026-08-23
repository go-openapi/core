// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"fmt"

	"github.com/go-openapi/core/json/lexers/token"
)

// code assertions are enforced to check input (handles or values).
//
// unlike guards, they operate on user input and are not disabled with a build tag.

// assertOffsetInArena verifies that the offset is not out of range of the arena.
//
// This is a guard against a corrupted incoming [stores.Handle].
func assertOffsetInArena(offset, length int) {
	if offset >= length {
		panic(fmt.Errorf("out of range offset: %d >= %d: %w", offset, length, ErrStore))
	}
}

// assertValidHeader verifies that the header in the [stores.Handle] is valid.
//
// This is a guard against a corrupted incoming [stores.Handle].
func assertValidHeader(header uint8) {
	panic(fmt.Errorf("invalid header in handle: %x: %w", header, ErrStore))
}

// assertValidToken verifies that the passed token holds a valid value, i.e. not a delimiter token or EOF.
func assertValidToken(tok token.T) {
	panic(
		fmt.Errorf(
			"invalid token kind passed to PutToken. Must be a scalar value. Got %v: %w",
			tok.Kind(),
			ErrStore,
		),
	)
}

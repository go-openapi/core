// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

//go:build guards || writerguards

package bcd

import (
	"fmt"
)

// code assertions turned on

type storeBCDError string

func (e storeBCDError) Error() string {
	return string(e)
}

const ErrBCDStore storeBCDError = "json document store error"

func assertBCDOutCapacity(out []byte, l int) {
	if cap(out) < l {
		panic(
			fmt.Errorf(
				"insufficient bytes in out slice. Wanted at least %d. Got %d: %w",
				l,
				cap(out),
				ErrBCDStore,
			),
		)
	}
}

func assertBCDDigit(ok bool, digit byte) {
	if !ok {
		panic(fmt.Errorf("invalid input numerical character: %c: %w", digit, ErrBCDStore))
	}
}

func assertBCDNibble(ok bool, nibble byte) {
	if !ok {
		panic(fmt.Errorf("invalid input BCD nibble: %X: %w", nibble, ErrBCDStore))
	}
}

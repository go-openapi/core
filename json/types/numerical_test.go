// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package types

import (
	"testing"

	"github.com/go-openapi/testify/v2/assert"
)

func TestCompareNumbers(t *testing.T) {
	t.Run("should be less (integers)", checkNumbers("122", "456", -1))
	t.Run("should be less (decimals)", checkNumbers("122.345", "34560.456", -1))
	t.Run("should be less (exponents)", checkNumbers("122.345e5", "34560.456E8", -1))
	t.Run("should be greater (integers)", checkNumbers("123456", "4567", 1))
	t.Run("should be greater (decimals)", checkNumbers("99122.345", "34560.456", 1))
	t.Run("should be greater (exponents)", checkNumbers("122.345e-5", "34560.456E-8", 1))
	t.Run("should be equal (integers)", checkNumbers("123", "123", 0))
	t.Run("should be equal (decimals)", checkNumbers("123.45", "123.45", 0))
	t.Run("should be equal (exponents)", checkNumbers("123.45e12", "123.45E12", 0))
}

func checkNumbers(a, b string, expected int) func(*testing.T) {
	return func(t *testing.T) {
		t.Helper()
		x := Number{Value: []byte(a)}
		y := Number{Value: []byte(b)}

		assert.Equal(t, expected, compareNumbers(x, y))
	}
}

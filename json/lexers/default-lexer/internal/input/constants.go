// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package input

// Byte constants of the JSON grammar consulted by the value scanners.
//
// Duplicated from the lexer package's constants.go (the scan cores use their own copy).
const (
	decimalPoint = '.'
	minusSign    = '-'
	doubleQuote  = '"'
	escape       = '\\'
	startOfTrue  = 't'
	startOfFalse = 'f'
	colon        = ':'
	slash        = '/'
)

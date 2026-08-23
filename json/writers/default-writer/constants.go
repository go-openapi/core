// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package writer

const (
	comma                = ','
	colon                = ':'
	closingBracket       = '}'
	closingSquareBracket = ']'
	openingBracket       = '{'
	openingSquareBracket = '['
	quote                = '"'
	newline              = '\n'
	space                = ' '
	lowestPrintable      = byte(0x20)
)

var (
	trueBytes  = []byte("true")  //nolint:gochecknoglobals
	falseBytes = []byte("false") //nolint:gochecknoglobals
	nullToken  = []byte("null")  //nolint:gochecknoglobals
)

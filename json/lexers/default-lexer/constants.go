// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package lexer

const (
	// delimiters for the JSON grammar
	openingBracket       = '{'
	closingBracket       = '}'
	openingSquareBracket = '['
	closingSquareBracket = ']'
	comma                = ','
	colon                = ':'

	// other significant  delimiters
	decimalPoint = '.'
	minusSign    = '-'
	doubleQuote  = '"'
	escape       = '\\'
	startOfTrue  = 't'
	startOfFalse = 'f'
	startOfNull  = 'n'

	// ignored blank space
	blank          = ' '
	tab            = '\t'
	lineFeed       = '\n'
	carriageReturn = '\r'
)

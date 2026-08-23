// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

//go:build !guards && !writerguards

package store

import "github.com/go-openapi/core/json/stores/values"

// code assertions turned off

// code assertions carry out extra checks with a panic outcome

func assertCompressDeflateError(_ error) {}
func assertCompressInflateError(_ error) {}
func assertCompressOptionWriter(_ error) {}
func assertInlineASCIISize(_ []byte)     {}
func assertInlineASCIIUnpackSize(_ int)  {}
func assertInlinePackBytes(_ []byte)     {}
func assertInlinePackBlanks(_ []byte)    {}
func assertOffsetAddressable(_ int)      {}
func assertVerbatimOnlyBlanks(_ []byte)  {}
func assertBlankHeader(_ uint8)          {}
func assertVerbatimIsBlank(_ byte)       {}
func assertValidValue(_ values.Value)    {}

// func assertInlineASCIIBufferLength(_ []byte) {}

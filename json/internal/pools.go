// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package internal

import (
	"bytes"

	"github.com/go-openapi/swag/pools"
)

//nolint:gochecknoglobals // pools are globals
var (
	poolOfAppendWriters = pools.New[AppendWriter]()
	poolOfBuffers       = pools.New[bytes.Buffer]()
)

func BorrowAppendWriter() *AppendWriter {
	return poolOfAppendWriters.Borrow()
}

func RedeemAppendWriter(w *AppendWriter) {
	poolOfAppendWriters.Redeem(w)
}

func BorrowBytesBuffer() *bytes.Buffer {
	return poolOfBuffers.Borrow()
}

func RedeemBytesBuffer(b *bytes.Buffer) {
	poolOfBuffers.Redeem(b)
}

// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package lexer

import "io"

var noopReader io.Reader = eofReader{} //nolint:gochecknoglobals // okay to preallocate this dummy empty value

// eofReader is a dummy reader that returns EOF upon call to Read()
type eofReader struct{}

func (r eofReader) Read(_ []byte) (int, error) {
	return 0, io.EOF
}

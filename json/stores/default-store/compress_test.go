// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"io"
	"strings"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"
)

// TestUncompressStringReader is a regression test: uncompressStringReader used to return the raw
// compressed buffer instead of the inflating reader, so WriteTo emitted compressed bytes.
func TestUncompressStringReader(t *testing.T) {
	s := New()                                    // compression enabled by default (threshold 128 bytes)
	original := strings.Repeat("abcdefghij ", 40) // ~440 bytes, compressible

	h := s.putCompressedString([]byte(original))
	size, offset := withOffset(h)

	session := s.uncompressStringReader(s.arena[offset : offset+size])
	got, err := io.ReadAll(session.reader)
	session.redeem()

	require.NoError(t, err)
	assert.Equal(t, original, string(got))
}

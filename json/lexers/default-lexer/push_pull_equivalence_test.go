// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package lexer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-openapi/testify/v2/require"

	"github.com/go-openapi/core/json/lexers/token"
)

// TestScanPushMatchesNextToken asserts that the whole-buffer push back-end of Tokens() (the generic push core
// scanPushG) yields byte-for-byte the same token stream — and reaches the same final error/Ok state — as the pull
// NextToken loop (the generic pull core scanTokenBufferG in whole-buffer mode, §10), across the entire JSONTestSuite
// parsing corpus (valid, invalid and implementation-defined).
func TestScanPushMatchesNextToken(t *testing.T) {
	type tok struct {
		kind  token.Kind
		value string
	}

	pullStream := func(data []byte) (out []tok, ok bool, errStr string) {
		l := NewWithBytes(data)
		for {
			tk := l.NextToken()
			if !l.Ok() {
				return out, false, errString(l.Err())
			}
			if tk.IsEOF() {
				return out, true, ""
			}
			out = append(out, tok{tk.Kind(), string(tk.Value())})
		}
	}

	pushStream := func(data []byte) (out []tok, ok bool, errStr string) {
		l := NewWithBytes(data)
		for tk := range l.Tokens() {
			out = append(out, tok{tk.Kind(), string(tk.Value())})
		}
		if !l.Ok() {
			return out, false, errString(l.Err())
		}

		return out, true, ""
	}

	dir := filepath.Join(currentDir(), "..", "testdata", "JSONTestSuite", "test_parsing")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	var checked int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, rerr)

		pullToks, pullOk, pullErr := pullStream(data)
		pushToks, pushOk, pushErr := pushStream(data)

		require.Equalf(
			t,
			pullOk,
			pushOk,
			"Ok mismatch on %s (pull err=%q push err=%q)",
			name,
			pullErr,
			pushErr,
		)
		require.Equalf(t, pullErr, pushErr, "error mismatch on %s", name)
		require.Equalf(t, pullToks, pushToks, "token stream mismatch on %s", name)
		checked++
	}

	t.Logf("compared push vs pull token streams on %d fixtures", checked)
	require.Positive(t, checked)
}

func errString(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

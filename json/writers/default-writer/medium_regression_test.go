// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package writer

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/core/json/stores/values"
	"github.com/go-openapi/core/json/types"
)

// TestJSONStringEmptyDefined is a regression test: a defined but empty types.String
// (IsDefined() == true, len == 0, e.g. types.EmptyString) must render as the empty JSON
// string "", not be dropped. An undefined value (nil) still renders nothing.
func TestJSONStringEmptyDefined(t *testing.T) {
	t.Run("defined empty renders empty string", func(t *testing.T) {
		t.Run("unbuffered", func(t *testing.T) {
			var buf bytes.Buffer
			w := NewUnbuffered(&buf)
			w.StartArray()
			w.JSONString(types.EmptyString)
			w.EndArray()
			require.NoError(t, w.Err())
			assert.Equal(t, `[""]`, buf.String())
		})

		t.Run("buffered", func(t *testing.T) {
			var buf bytes.Buffer
			w := NewBuffered(&buf)
			w.StartArray()
			w.JSONString(types.EmptyString)
			w.EndArray()
			require.NoError(t, w.Err())
			require.NoError(t, w.Flush())
			assert.Equal(t, `[""]`, buf.String())
		})
	})

	t.Run("undefined renders nothing", func(t *testing.T) {
		var buf bytes.Buffer
		w := NewUnbuffered(&buf)
		w.StartArray()
		w.JSONString(types.String{}) // nil Value => undefined
		w.EndArray()
		require.NoError(t, w.Err())
		assert.Equal(t, `[]`, buf.String())
	})
}

// TestStringRunesDoesNotOverAllocate is a regression test for the escaped-buffer sizing in
// StringRunes. The previous code borrowed len(data)*utf8.MaxRune bytes (~1.1MB per rune)
// instead of len(data)*utf8.UTFMax. A few hundred runes would request hundreds of MB.
func TestStringRunesDoesNotOverAllocate(t *testing.T) {
	runes := []rune(strings.Repeat("abcde", 100)) // 500 runes

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	var buf bytes.Buffer
	w := NewUnbuffered(&buf)
	w.StringRunes(runes)
	require.NoError(t, w.Err())

	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc

	// With the utf8.MaxRune bug this would be 500*1114111 bytes (>500MB). With the fix the
	// escaped buffer is ~2KB; the 1MB ceiling leaves ample room for incidental allocations.
	assert.Less(t, allocated, uint64(1<<20),
		"StringRunes allocated %d bytes for %d runes", allocated, len(runes))

	// sanity: output is the correctly quoted string
	assert.Equal(t, `"`+string(runes)+`"`, buf.String())
}

// TestYAMLNullConsistency is a regression test: all four ways of emitting a null in the
// YAML writer must render the YAML null token "~", never the literal JSON "null".
func TestYAMLNullConsistency(t *testing.T) {
	var buf bytes.Buffer
	w := NewYAML(&buf)

	w.StartArray()
	w.Null() // native null
	w.Comma()
	w.Value(values.NullValue) // store value null
	w.Comma()
	w.JSONNull(types.Null) // JSON null (defined)
	w.Comma()
	w.Token(token.NullToken) // lexer null token
	w.EndArray()
	require.NoError(t, w.Flush())

	out := buf.String()
	assert.NotContains(t, out, "null", "YAML must not emit the literal JSON null token")
	assert.Equal(t, 4, strings.Count(out, "~"), "expected four YAML null tokens in %q", out)
}

// TestYAMLJSONNullUndefined is a regression test: an undefined JSON null must render
// nothing in YAML (matching the base JSONNull contract), rather than always writing "~".
func TestYAMLJSONNullUndefined(t *testing.T) {
	var buf bytes.Buffer
	w := NewYAML(&buf)

	w.JSONNull(types.NullType{}) // undefined
	require.NoError(t, w.Flush())

	assert.Empty(t, buf.String())
}

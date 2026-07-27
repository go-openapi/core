package writer

import (
	"bytes"
	"io"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	"github.com/go-openapi/core/json/stores/values"
)

// TestBufferedBufferSizing checks the working buffer is borrowed at the configured size and
// that recycling a pooled Buffered through different sizes re-borrows correctly (the working
// buffer now lives directly on buffered, borrowed via borrowBuffer).
func TestBufferedBufferSizing(t *testing.T) {
	sizes := []int{64, 8192, 256} // shrink and grow across re-borrows of the same pool slot
	for _, size := range sizes {
		var sink bytes.Buffer
		w := BorrowBuffered(&sink, WithBufferSize(size))
		assert.Equalf(t, size, w.bufferSize, "bufferSize for %d", size)
		assert.Equalf(t, size, cap(w.buf), "buffer capacity for %d", size)
		assert.Emptyf(t, w.buf, "buffer starts empty for %d", size)
		RedeemBuffered(w)
	}
}

// TestBufferedSizeReflectsPending is a regression test: Buffered.Size() must include bytes
// still pending in the internal buffer, not just the bytes already flushed.
func TestBufferedSizeReflectsPending(t *testing.T) {
	var buf bytes.Buffer
	w := NewBuffered(&buf, WithBufferSize(1024)) // large enough that nothing flushes

	w.StartArray()
	w.Number(12345)
	w.EndArray()

	require.Equal(t, 0, buf.Len(), "nothing should have been flushed yet")
	assert.Equal(t, int64(len(`[12345]`)), w.Size(), "Size must count pending buffer bytes")

	require.NoError(t, w.Flush())
	assert.Equal(t, int64(buf.Len()), w.Size(), "Size must match output after flush")
}

// TestBorrowYAMLPooled is a regression test: BorrowYAML must not panic on a fresh pool
// instance (whose nestingLevel starts nil, and the pool calls Reset before BorrowYAML
// initializes it), and must keep working across redeem / re-borrow cycles.
func TestBorrowYAMLPooled(t *testing.T) {
	for range 3 {
		var buf bytes.Buffer

		w := BorrowYAML(&buf)
		w.StartObject()
		w.Key(values.MakeInternedKey("a"))
		w.Number(1)
		w.EndObject()
		require.NoError(t, w.Flush())
		RedeemYAML(w)

		assert.Contains(t, buf.String(), "1")
	}
}

// TestYAMLStackDeepNesting is a regression test for the YAML container stack beyond a single
// 63-bit word. The previous overflow handling clobbered the freshly pushed word (and seeded
// arrays with the wrong marker), so isInArray()/IndentLevel() went wrong past depth 63.
func TestYAMLStackDeepNesting(t *testing.T) {
	w := NewYAML(io.Discard)

	const depth = 130 // spans three stack words (63 levels each)

	for i := 1; i <= depth; i++ {
		w.pushArray()
		assert.Truef(t, w.isInArray(), "depth %d should report in-array", i)
		assert.Equalf(t, i, w.IndentLevel(), "IndentLevel at depth %d", i)
	}

	for i := depth; i >= 1; i-- {
		assert.Truef(t, w.isInArray(), "while unwinding at depth %d should report in-array", i)
		w.popContainer()
	}

	// back to the initial, empty state
	require.Len(t, w.nestingLevel, 1)
	assert.Equal(t, uint64(1), w.nestingLevel[0])
}

// TestYAMLStackObjectAtWordBoundary checks the overflow seeding distinguishes object from
// array exactly at the 63/64 word boundary.
func TestYAMLStackObjectAtWordBoundary(t *testing.T) {
	w := NewYAML(io.Discard)

	for range 63 {
		w.pushArray()
	}
	require.True(t, w.isInArray())
	require.Equal(t, 63, w.IndentLevel())

	w.pushObject() // 64th level -> overflows into a new word, seeded as an object
	assert.False(t, w.isInArray(), "an object level must not report in-array")
	assert.Equal(t, 64, w.IndentLevel())

	w.popContainer() // close the object
	assert.True(t, w.isInArray(), "after closing the object we are back inside the array")
	assert.Equal(t, 63, w.IndentLevel())
}

// TestYAMLDeepNestingOutput exercises deep nesting through the public API to ensure the
// stack fix holds end-to-end without panic.
func TestYAMLDeepNestingOutput(t *testing.T) {
	const depth = 130

	var buf bytes.Buffer
	w := NewYAML(&buf, WithYAMLIndent(" "))

	for range depth {
		w.StartArray()
	}
	w.Number(42)
	for range depth {
		w.EndArray()
	}

	require.NoError(t, w.Flush())
	require.NoError(t, w.Err())
	assert.Contains(t, buf.String(), "42")
	// every array level renders its child as a YAML element, so the "- " marker count
	// must keep growing with depth rather than stalling at 63.
	assert.GreaterOrEqual(t, bytes.Count(buf.Bytes(), []byte("- ")), depth)
}

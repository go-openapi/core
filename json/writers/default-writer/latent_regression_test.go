// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

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

// TestNewBufferedReleasesWorkingBufferOnce is a regression test: a writer built with
// NewBuffered registers a GC cleanup that returns its working buffer to poolOfBuffers. The
// cleanup used to capture the pool's redeem closure directly, so a writer that was ALSO
// redeemed explicitly (RedeemBuffered, or a New-built writer handed to RedeemYAML) returned the
// same buffer twice — which panics in the pool ("double redeem") and would otherwise hand one
// buffer to two borrowers. The release must be one-shot: whoever gets there first wins.
func TestNewBufferedReleasesWorkingBufferOnce(t *testing.T) {
	t.Run("explicit redeem then cleanup", func(t *testing.T) {
		w := NewBuffered(io.Discard)
		release := w.redeemBuffer // the very closure runtime.AddCleanup will call
		require.NotNil(t, release)

		w.redeem() // explicit path (what RedeemBuffered does)
		assert.NotPanics(t, release, "the GC cleanup must not redeem the buffer a second time")
		assert.NotPanics(t, release, "the release must stay a no-op however often it runs")
	})

	t.Run("cleanup then explicit redeem", func(t *testing.T) {
		w := NewBuffered(io.Discard)
		release := w.redeemBuffer

		release() // cleanup path wins the race
		assert.NotPanics(t, w.redeem, "an explicit redeem after the cleanup must be a no-op")
	})

	t.Run("the buffer is really returned to the pool", func(t *testing.T) {
		// a one-shot that never runs would silently leak the buffer instead of panicking:
		// check the first call does reach the pool by borrowing the same size back.
		w := NewBuffered(io.Discard, WithBufferSize(4242))
		require.Equal(t, 4242, cap(w.buf))
		w.redeem()

		back := BorrowBuffered(io.Discard, WithBufferSize(4242))
		defer RedeemBuffered(back)
		assert.Equal(t, 4242, cap(back.buf))
	})
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

// TestPooledCompositeDoesNotShareInnerWriter is a regression test for a pool misuse: YAML and
// Indented used to hand their inner *Buffered back to poolOfBuffered on redeem while keeping
// the pointer, then reuse that same instance on the next borrow. The instance was therefore
// live in the pool AND owned by the writer: the next RedeemYAML/RedeemIndented redeemed it a
// second time (a panic under the poolsdebug tag), and any concurrent BorrowBuffered received a
// writer someone else was already writing into.
//
//nolint:dupl // YAML and Indented are distinct types: the two lifecycles are asserted side by side on purpose
func TestPooledCompositeDoesNotShareInnerWriter(t *testing.T) {
	t.Run("YAML", func(t *testing.T) {
		var first bytes.Buffer
		w := BorrowYAML(&first)
		w.StartObject()
		w.Key(values.MakeInternedKey("a"))
		w.Number(1)
		w.EndObject()
		require.NoError(t, w.Flush())
		RedeemYAML(w)

		// the inner writer went back to its pool: no alias may survive on the redeemed writer
		assert.Nil(t, w.Buffered, "a redeemed YAML must not keep its inner Buffered")
		assert.Nil(t, w.redeemBuffered, "a redeemed YAML must not keep its redeem handle")

		// another borrower may now legitimately be handed that very instance; a re-borrowed
		// YAML must not write into it
		var direct bytes.Buffer
		other := BorrowBuffered(&direct)
		defer RedeemBuffered(other)

		var second bytes.Buffer
		w2 := BorrowYAML(&second)
		assert.NotSame(t, other, w2.Buffered, "a pooled YAML must not share its inner writer")

		other.StartArray()
		other.Number(2)
		other.EndArray()
		w2.StartObject()
		w2.Key(values.MakeInternedKey("b"))
		w2.Number(3)
		w2.EndObject()
		require.NoError(t, w2.Flush())
		RedeemYAML(w2)
		require.NoError(t, other.Flush())

		assert.Equal(t, "[2]", direct.String())
		assert.Contains(t, second.String(), "b")
		assert.Contains(t, second.String(), "3")
		assert.NotContains(t, second.String(), "2", "the two writers must not share a buffer")
	})

	t.Run("Indented", func(t *testing.T) {
		var first bytes.Buffer
		w := BorrowIndented(&first)
		w.StartObject()
		w.Key(values.MakeInternedKey("a"))
		w.Number(1)
		w.EndObject()
		require.NoError(t, w.Flush())
		RedeemIndented(w)

		assert.Nil(t, w.Buffered, "a redeemed Indented must not keep its inner Buffered")
		assert.Nil(t, w.redeemBuffered, "a redeemed Indented must not keep its redeem handle")

		var direct bytes.Buffer
		other := BorrowBuffered(&direct)
		defer RedeemBuffered(other)

		var second bytes.Buffer
		w2 := BorrowIndented(&second)
		assert.NotSame(t, other, w2.Buffered, "a pooled Indented must not share its inner writer")

		other.StartArray()
		other.Number(2)
		other.EndArray()
		w2.StartObject()
		w2.Key(values.MakeInternedKey("b"))
		w2.Number(3)
		w2.EndObject()
		require.NoError(t, w2.Flush())
		RedeemIndented(w2)
		require.NoError(t, other.Flush())

		assert.Equal(t, "[2]", direct.String())
		assert.Contains(t, second.String(), `"b"`)
		assert.Contains(t, second.String(), "3")
		assert.NotContains(t, second.String(), "2", "the two writers must not share a buffer")
	})
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

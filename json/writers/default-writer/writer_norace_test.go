// Allocation counting needs an un-instrumented build: the race detector and the pools
// tracker (poolsdebug) both allocate per borrow, which would swamp the amortized counts
// these tests assert.
//go:build !race && !poolsdebug

package writer

import (
	"bytes"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	"github.com/go-openapi/core/json/writers"
)

func TestAllocs(t *testing.T) {
	const epsilon = 1e-6
	stuff := prepareStuff()

	t.Run("with unbuffered", func(t *testing.T) {
		var tw bytes.Buffer

		t.Run(
			"all allocations should be amortized (excluding math/big values)",
			func(t *testing.T) {
				allocs := testing.AllocsPerRun(100, func() {
					tw.Reset()
					jw := BorrowUnbuffered(&tw)
					defer func() {
						RedeemUnbuffered(jw)
					}()
					writeStuffWithoutBig(t, jw, stuff)
					require.NoError(t, jw.Err())
				})
				assert.InDelta(t, 0, allocs, epsilon)
			},
		)

		t.Run(
			"most but not all allocations should be amortized (including math/big values)",
			func(t *testing.T) {
				allocs := testing.AllocsPerRun(100, func() {
					tw.Reset()
					jw := BorrowUnbuffered(&tw)
					defer func() {
						RedeemUnbuffered(jw)
					}()
					writeStuff(t, jw, stuff)
					require.NoError(t, jw.Err())
					require.NoError(t, jw.Err())
				})
				assert.InDelta(
					t,
					10,
					allocs,
					epsilon,
				) // this assertion is sensitive to the math/big package
			},
		)
	})

	t.Run("with pooled buffered", func(t *testing.T) {
		var tw bytes.Buffer

		t.Run(
			"all allocations should be amortized (excluding math/big values)",
			func(t *testing.T) {
				allocs := testing.AllocsPerRun(10000, func() {
					tw.Reset()
					jw := BorrowBuffered(&tw)
					defer func() {
						RedeemBuffered(jw)
					}()
					writeStuffWithoutBig(t, jw, stuff)
					require.NoError(t, jw.Err())
					require.NoError(t, jw.Flush())
				})
				assert.InDelta(t, 0, allocs, epsilon)
			},
		)

		t.Run(
			"most but not all allocations should be amortized (including math/big values)",
			func(t *testing.T) {
				allocs := testing.AllocsPerRun(100, func() {
					tw.Reset()
					jw := BorrowBuffered(&tw)
					defer func() {
						RedeemBuffered(jw)
					}()
					writeStuff(t, jw, stuff)
					require.NoError(t, jw.Err())
					require.NoError(t, jw.Flush())
				})
				assert.InDelta(
					t,
					10,
					allocs,
					epsilon,
				) // this assertion is sensitive to the math/big package
			},
		)
	})

	t.Run("with pooled indented", func(t *testing.T) {
		var tw bytes.Buffer

		t.Run(
			"all allocations should be amortized (excluding math/big values)",
			func(t *testing.T) {
				allocs := testing.AllocsPerRun(10000, func() {
					tw.Reset()
					jw := BorrowIndented(&tw)
					defer func() {
						RedeemIndented(jw)
					}()
					writeStuffWithoutBig(t, jw, stuff)
					require.NoError(t, jw.Err())
					require.NoError(t, jw.Flush())
				})
				assert.InDelta(t, 0, allocs, epsilon)
			},
		)

		t.Run(
			"most but not all allocations should be amortized (including math/big values)",
			func(t *testing.T) {
				allocs := testing.AllocsPerRun(100, func() {
					tw.Reset()
					jw := BorrowIndented(&tw)
					defer func() {
						RedeemIndented(jw)
					}()
					writeStuff(t, jw, stuff)
					require.NoError(t, jw.Err())
					require.NoError(t, jw.Flush())
				})
				assert.InDelta(
					t,
					10,
					allocs,
					epsilon,
				) // this assertion is sensitive to the math/big package
			},
		)
	})
}

// plainWriter is an io.Writer that implements neither io.ByteWriter nor io.StringWriter,
// so the writers cannot take any single-byte/string fast path. It is the worst case for
// the streaming writers and the one that previously leaked a per-byte allocation.
type plainWriter struct{}

func (plainWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestZeroAllocPlainWriter guards the "zero unamortized allocations" guarantee against a
// plain io.Writer target (not an io.ByteWriter): regression for the []byte{c} allocation
// that used to happen on every single-byte write in the unbuffered writer.
func TestZeroAllocPlainWriter(t *testing.T) {
	const epsilon = 1e-6

	var sink plainWriter

	// all byte inputs are hoisted out of the measured closure so the conversions
	// themselves do not count as allocations.
	var (
		key     = []byte("key")
		plain   = []byte("hello world")
		escaped = []byte("tab\tquote\"newline\nbackslash\\end")
		unicode = []byte("accentéè snow☃ clef𝄞")
		number  = []byte("123456.789e-3")
	)

	writeDoc := func(w writers.TokenWriter) {
		w.StartObject()
		w.StringBytes(key)
		w.Colon()
		w.StartArray()
		w.StringBytes(plain)
		w.Comma()
		w.StringBytes(escaped)
		w.Comma()
		w.StringBytes(unicode)
		w.Comma()
		w.NumberBytes(number)
		w.Comma()
		w.Bool(true)
		w.Comma()
		w.Null()
		w.EndArray()
		w.EndObject()
	}

	t.Run("unbuffered", func(t *testing.T) {
		w := BorrowUnbuffered(sink) // warm the pools
		writeDoc(w)
		RedeemUnbuffered(w)

		allocs := testing.AllocsPerRun(200, func() {
			w := BorrowUnbuffered(sink)
			writeDoc(w)
			RedeemUnbuffered(w)
		})
		assert.InDelta(t, 0, allocs, epsilon)
	})

	t.Run("buffered", func(t *testing.T) {
		w := BorrowBuffered(sink)
		writeDoc(w)
		_ = w.Flush()
		RedeemBuffered(w)

		allocs := testing.AllocsPerRun(200, func() {
			w := BorrowBuffered(sink)
			writeDoc(w)
			_ = w.Flush()
			RedeemBuffered(w)
		})
		assert.InDelta(t, 0, allocs, epsilon)
	})

	t.Run("yaml", func(t *testing.T) {
		w := BorrowYAML(sink)
		writeDoc(w)
		_ = w.Flush()
		RedeemYAML(w)

		allocs := testing.AllocsPerRun(200, func() {
			w := BorrowYAML(sink)
			writeDoc(w)
			_ = w.Flush()
			RedeemYAML(w)
		})
		assert.InDelta(t, 0, allocs, epsilon)
	})
}

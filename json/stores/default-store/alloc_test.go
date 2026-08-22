// Allocation counting needs an un-instrumented build: the race detector and the pools
// tracker (poolsdebug) both allocate per borrow, which would swamp the amortized counts
// these tests assert.
//go:build !race && !poolsdebug

package store

import (
	"bytes"
	"math"
	"math/rand/v2"
	"strconv"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/core/json/stores"
	writer "github.com/go-openapi/core/json/writers/default-writer"
)

// headerName gives a readable name to the encoding a [stores.Handle] carries, for failure messages.
func headerName(h stores.Handle) string {
	switch header := uint8(h & headerMask); header {
	case headerNone:
		return "none"
	case headerNull:
		return "null"
	case headerFalse:
		return "false"
	case headerTrue:
		return "true"
	case headerInlinedNumber:
		return "inlined number"
	case headerInlinedASCII:
		return "inlined ASCII string"
	case headerInlinedString:
		return "inlined string"
	case headerNumber:
		return "arena number"
	case headerString:
		return "arena string"
	case headerCompressedString:
		return "arena compressed string"
	case headerInlinedCompressedString:
		return "inlined compressed string"
	default:
		return "header " + strconv.Itoa(int(header))
	}
}

// expectedGetAllocs reports how many allocations a PutToken + Get round-trip may cost, keyed on the
// encoding the store actually chose for the value.
//
// Which branch a given input lands in is not stable across Go releases: go1.27 rewrote
// compress/flate, so a string that used to be stored compressed in the arena may now compress small
// enough to be inlined in the handle, or — being incompressible — not be compressed at all. Keying
// the expectation on the handle keeps this an assertion about the store's behaviour, rather than a
// restatement of whatever the measurement happened to be.
func expectedGetAllocs(t *testing.T, h stores.Handle) float64 {
	t.Helper()

	switch header := uint8(h & headerMask); header {
	case headerNone, headerNull, headerFalse, headerTrue:
		// the handle *is* the value: there is nothing to decode
		return 0
	case headerString:
		// Get aliases the arena: no copy at all
		return 0
	case headerInlinedNumber, headerInlinedASCII, headerInlinedString,
		headerNumber, headerCompressedString, headerInlinedCompressedString:
		// the value is decoded (unpacked, BCD-decoded or inflated) into a fresh buffer that Get
		// hands over to the caller. That one allocation is required: it cannot be recycled.
		return 1
	default:
		t.Fatalf("unexpected header in this test: %d", header)

		return 0
	}
}

func TestStoreAllocations(t *testing.T) {
	// This test verifies that all the necessary care has been taken in the store
	// so as to minimize internal allocations when Get/Put values.
	//
	// Attention: not to be asserted with -race: when running with the race detector,
	// some contention on the pools  may prevent memory from being perfectly recycled, as in real life.
	//
	// This may alter the measurement of the number of allocations, which is no longer fully accurate.

	const (
		epsilon = 1e-6
		runs    = 10
	)

	t.Run("with expected allocations on get", func(t *testing.T) {
		s := New()

		// assertRoundTripAllocs asserts that a PutToken + Get round-trip costs exactly the number of
		// allocations the chosen encoding justifies. Put itself is amortized to zero: the arena
		// grows geometrically and every scratch buffer it needs comes from a pool.
		assertRoundTripAllocs := func(t *testing.T, input token.T, iterations int) stores.Handle {
			t.Helper()

			h := s.PutToken(input)
			expected := expectedGetAllocs(t, h)
			allocs := testing.AllocsPerRun(iterations, func() {
				handle := s.PutToken(input) // amortized to 0 allocs
				_ = s.Get(handle)           // expected allocs
			})
			assert.InDeltaf(t, expected, allocs, epsilon,
				"value encoded as %q: would expect %.0f alloc(s) but got %f",
				headerName(h), expected, allocs,
			)

			return h
		}

		t.Run("should not require any allocation on bool values", func(t *testing.T) {
			assertRoundTripAllocs(t, token.MakeBoolean(true), runs)
		})

		t.Run("should require 1 allocation for small integer values", func(t *testing.T) {
			h := assertRoundTripAllocs(t, token.MakeWithValue(token.Number, []byte("1234")), runs)
			assert.Equal(t, headerInlinedNumber, uint8(h&headerMask))
		})

		t.Run("should require 1 allocation for small string values", func(t *testing.T) {
			h := assertRoundTripAllocs(t, token.MakeWithValue(token.String, []byte("abcd")), runs)
			assert.Equal(t, headerInlinedString, uint8(h&headerMask))
		})

		t.Run("should require 1 allocation for small ASCII string values", func(t *testing.T) {
			input := token.MakeWithValue(token.String, []byte("abcdefgh"))
			h := assertRoundTripAllocs(t, input, runs)
			assert.Equal(t, headerInlinedASCII, uint8(h&headerMask))
		})

		t.Run(
			"should not require any allocation for not so large string values",
			func(t *testing.T) {
				h := assertRoundTripAllocs(
					t,
					token.MakeWithValue(token.String, []byte("abcdefghij")),
					runs,
				)
				assert.Equal(t, headerString, uint8(h&headerMask))
			},
		)

		t.Run(
			"should require 1 allocation for a highly compressible string value",
			func(t *testing.T) {
				// This sample has a super-high compression ratio, so it exercises the compression
				// path. Which of the two compressed encodings it lands in depends on the flate
				// implementation: up to go1.26 it deflates to 9 bytes and goes to the arena, from
				// go1.27 it deflates to 6 bytes and is inlined in the handle. Either way, Get owes
				// the caller exactly one allocation for the inflated bytes.
				input := token.MakeWithValue(token.String, bytes.Repeat([]byte("a"), 129))
				h := assertRoundTripAllocs(t, input, 10000*runs)
				assert.Contains(t,
					[]uint8{headerCompressedString, headerInlinedCompressedString},
					uint8(h&headerMask),
					"a string of 129 identical bytes should compress",
				)
			},
		)

		t.Run(
			"should not require any allocation for an incompressible string value",
			func(t *testing.T) {
				// Shuffled, so there is nothing for DEFLATE to find. The store must notice that
				// compression did not pay off and keep the raw bytes in the arena — which Get then
				// serves by aliasing, at no allocation.
				//
				// Up to go1.26 flate still Huffman-coded this sample down to ~251 bytes, so it did
				// land in the arena compressed and Get owed one allocation. From go1.27 flate falls
				// back to a stored block (357 bytes for a 350-byte input) and the store keeps the
				// original. Both outcomes are correct; the handle says which one happened.
				alphabet := []byte("0123456789abcdefghijklmnoprstuvwxyz")
				testString := bytes.Repeat(alphabet, 10)
				rand.Shuffle( //nolint:gosec // G404 is not relevant for tests
					len(testString),
					func(i, j int) {
						testString[i], testString[j] = testString[j], testString[i]
					},
				)
				input := token.MakeWithValue(token.String, testString)
				h := assertRoundTripAllocs(t, input, 10000*runs)
				assert.LessOrEqual(t,
					sizeInArena(h), len(testString),
					"an incompressible value should never take more arena space than its raw bytes",
				)
			},
		)

		t.Run(
			"should not require any allocation for a longer incompressible string value",
			func(t *testing.T) {
				alphabet := []byte(strconv.FormatFloat(math.Pi, 'f', -1, 64))
				testString := bytes.Repeat(alphabet, 50)
				rand.Shuffle( //nolint:gosec // G404 is not relevant for tests
					len(testString),
					func(i, j int) {
						testString[i], testString[j] = testString[j], testString[i]
					},
				)
				input := token.MakeWithValue(token.String, testString)
				h := assertRoundTripAllocs(t, input, 10000*runs)
				assert.LessOrEqual(t,
					sizeInArena(h), len(testString),
					"an incompressible value should never take more arena space than its raw bytes",
				)
			},
		)
	})

	t.Run("with caller-provided scratch (AppendValueBytes)", func(t *testing.T) {
		s := New()
		scratch := make([]byte, 0, 1024)

		// AppendValueBytes decodes into the caller's buffer, so with enough spare capacity every
		// encoding must be entirely allocation-free — including the compressed ones, whose scratch
		// buffers all come from pools.
		assertAppendAllocs := func(t *testing.T, input token.T) stores.Handle {
			t.Helper()

			h := s.PutToken(input)
			allocs := testing.AllocsPerRun(runs, func() {
				_, scratch = s.AppendValueBytes(scratch[:0], h)
			})
			assert.InDeltaf(t, 0.00, allocs, epsilon,
				"value encoded as %q: would expect no alloc but got %f", headerName(h), allocs,
			)

			return h
		}

		t.Run("should not allocate for a small integer value", func(t *testing.T) {
			assertAppendAllocs(t, token.MakeWithValue(token.Number, []byte("1234")))
		})

		t.Run("should not allocate for a small string value", func(t *testing.T) {
			assertAppendAllocs(t, token.MakeWithValue(token.String, []byte("abcd")))
		})

		t.Run("should not allocate for a compressed string value", func(t *testing.T) {
			input := token.MakeWithValue(token.String, bytes.Repeat([]byte("a"), 129))
			h := assertAppendAllocs(t, input)
			require.Contains(t,
				[]uint8{headerCompressedString, headerInlinedCompressedString},
				uint8(h&headerMask),
				"this case is only meaningful if the value did go through compression",
			)
		})
	})

	t.Run("with a streaming writer (WriteTo)", func(t *testing.T) {
		s := New()
		var sink bytes.Buffer
		w := writer.NewBuffered(&sink)

		// WriteTo streams the decoded value straight to the writer instead of materializing it, so
		// no encoding should need an allocation at all: every scratch buffer, every source buffer
		// and the inflate reader itself come from a pool.
		assertWriteToAllocs := func(t *testing.T, input token.T) stores.Handle {
			t.Helper()

			h := s.PutToken(input)
			allocs := testing.AllocsPerRun(runs, func() {
				w.Reset()
				sink.Reset()
				s.WriteTo(w, h)
			})
			assert.InDeltaf(t, 0.00, allocs, epsilon,
				"value encoded as %q: would expect no alloc but got %f", headerName(h), allocs,
			)

			return h
		}

		t.Run("should not allocate for a small string value", func(t *testing.T) {
			assertWriteToAllocs(t, token.MakeWithValue(token.String, []byte("abcd")))
		})

		t.Run("should not allocate for a small ASCII string value", func(t *testing.T) {
			assertWriteToAllocs(t, token.MakeWithValue(token.String, []byte("abcdefgh")))
		})

		t.Run("should not allocate for a small integer value", func(t *testing.T) {
			assertWriteToAllocs(t, token.MakeWithValue(token.Number, []byte("1234")))
		})

		t.Run("should not allocate for a large string value", func(t *testing.T) {
			assertWriteToAllocs(t, token.MakeWithValue(token.String, []byte("abcdefghij")))
		})

		t.Run("should not allocate for a compressed string value", func(t *testing.T) {
			// This one guards the inflate path specifically: uncompressStringReader used to return a
			// redeem closure, which captured its pooled resources and escaped to the heap on every
			// call — one allocation per compressed value written.
			input := token.MakeWithValue(token.String, bytes.Repeat([]byte("a"), 129))
			h := assertWriteToAllocs(t, input)
			require.Contains(t,
				[]uint8{headerCompressedString, headerInlinedCompressedString},
				uint8(h&headerMask),
				"this case is only meaningful if the value did go through compression",
			)
		})
	})
}

// sizeInArena reports how many bytes a handle occupies in the arena (0 when it is inlined in the
// handle itself).
func sizeInArena(h stores.Handle) int {
	switch uint8(h & headerMask) {
	case headerNumber, headerString, headerCompressedString:
		size, _ := withOffset(h)

		return size
	default:
		return 0
	}
}

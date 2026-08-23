// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"strings"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	"github.com/go-openapi/core/json/stores"
	"github.com/go-openapi/core/json/stores/values"
)

// dictTestPayloads returns long, compressible strings that share substrings with the preset
// dictionary below, so a Store with WithCompressionDict actually exercises the dictionary on both
// the compress and the decompress side.
func dictTestPayloads() []string {
	return []string{
		strings.Repeat("the quick brown fox ", 12),
		strings.Repeat("lazy dog jumps over ", 10),
		"the quick brown fox " + strings.Repeat("and the lazy dog ", 8),
		strings.Repeat("0123456789abcdef", 20),
	}
}

func dictTestDict() []byte {
	return []byte("the quick brown fox lazy dog jumps over and the 0123456789abcdef")
}

// newDictStore builds a Store whose compression always kicks in (low threshold) and is seeded with a
// caller-provided preset dictionary.
func newDictStore(dict []byte) *Store {
	return New(
		WithCompressionDict(dict),
		WithCompressionThreshold(16))
}

// putAndCheck stores each payload, asserts it round-trips through Get and through the allocation-free
// AppendValueBytes path, and returns the handles for later re-verification.
func putAndCheck(t *testing.T, s stores.Store, payloads []string) []stores.Handle {
	t.Helper()

	handles := make([]stores.Handle, 0, len(payloads))
	var scratch []byte

	for _, p := range payloads {
		h := s.PutValue(values.MakeStringValue(p))
		handles = append(handles, h)

		got := s.Get(h)
		require.Equal(t, p, got.String(), "Get must round-trip a dict-compressed string")

		var v values.Value
		v, scratch = s.AppendValueBytes(scratch[:0], h)
		assert.Equal(
			t,
			p,
			string(v.Bytes()),
			"AppendValueBytes must round-trip a dict-compressed string",
		)
	}

	return handles
}

// TestCompressionDictRoundTrip proves the core invariant: a value compressed against an injected
// preset dictionary decodes back identically against that same dictionary, on both the Get and the
// AppendValueBytes paths.
func TestCompressionDictRoundTrip(t *testing.T) {
	s := newDictStore(dictTestDict())

	payloads := dictTestPayloads()
	handles := putAndCheck(t, s, payloads)

	// re-read every handle once more: the arena holds compressed bytes bound to the frozen dict.
	for i, h := range handles {
		assert.Equal(t, payloads[i], s.Get(h).String())
	}
}

// TestCompressionDictSurvivesGob proves the dictionary travels with the Store across a gob round-trip
// and that the reloaded Store rebuilds its compression writer (cw): old payloads still decode, and
// the reloaded Store can compress a NEW payload that itself round-trips.
func TestCompressionDictSurvivesGob(t *testing.T) {
	dict := dictTestDict()
	src := newDictStore(dict)

	payloads := dictTestPayloads()
	handles := make([]stores.Handle, 0, len(payloads))
	for _, p := range payloads {
		handles = append(handles, src.PutValue(values.MakeStringValue(p)))
	}

	blob, err := src.MarshalBinary()
	require.NoError(t, err)

	var loaded Store
	require.NoError(t, loaded.UnmarshalBinary(blob))

	// the dictionary travelled with the arena: previously-compressed payloads still decode.
	for i, h := range handles {
		assert.Equal(
			t,
			payloads[i],
			loaded.Get(h).String(),
			"reloaded Store must decode old payloads",
		)
	}

	// cw was rebuilt on load: the reloaded Store can compress a fresh payload and read it back.
	fresh := "the quick brown fox " + strings.Repeat("writes again ", 12)
	h := loaded.PutValue(values.MakeStringValue(fresh))
	assert.Equal(
		t,
		fresh,
		loaded.Get(h).String(),
		"reloaded Store must be able to compress and decode new payloads",
	)
}

// TestCompressionDictReleasedOnReset proves the recycle contract: Store.Reset restores the defaults,
// releasing the injected dictionary and dropping the (lazily-built) compression writer. The recycled
// Store is a clean slate that still round-trips correctly — crucially, with no leftover dict-encoding
// writer to desync against (the bug class the old preserving-Reset risked). The caller re-injects a
// dictionary at borrow time for the next generation.
func TestCompressionDictReleasedOnReset(t *testing.T) {
	dict := dictTestDict()
	s := newDictStore(dict)
	payloads := dictTestPayloads()

	putAndCheck(t, s, payloads)
	require.NotNil(t, s.dict, "the dictionary is in force while the Store is in use")
	require.NotNil(t, s.cw, "compressing built the writer")

	s.Reset()
	require.Empty(t, s.arena, "Reset rewinds the arena (data)")
	require.Nil(t, s.dict, "Reset releases the injected dictionary reference")
	require.Nil(t, s.cw, "Reset drops the lazily-built writer")

	// the recycled Store works at defaults: a long string compresses against the (now empty) dict and
	// round-trips, proving no desync survived the reset.
	fresh := strings.Repeat("recycled payload ", 20)
	h := s.PutValue(values.MakeStringValue(fresh))
	require.Equal(t, fresh, s.Get(h).String())
}

// TestCompressionDictAliasesCaller documents that the Store aliases (does not copy) the injected
// dictionary slice: the contract is that the caller must treat it as immutable for the Store's life.
func TestCompressionDictAliasesCaller(t *testing.T) {
	dict := dictTestDict()
	s := newDictStore(dict)

	assert.Equal(
		t,
		&dict[0],
		&s.dict[0],
		"WithCompressionDict aliases the caller slice; it must not be mutated while the Store is alive",
	)
}

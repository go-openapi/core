package store

import (
	"strings"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	"github.com/go-openapi/core/json/stores/values"
)

// TestLazyCompressWriter proves the compression writer (cw) is built only when a value is actually
// compressed: option application and stores that never cross the threshold never allocate it.
func TestLazyCompressWriter(t *testing.T) {
	long := strings.Repeat("the quick brown fox ", 30) // ~600 bytes, > default threshold (128)

	t.Run("not built at construction", func(t *testing.T) {
		s := New()
		require.Nil(t, s.cw, "cw must not be built until first compression")
	})

	t.Run("not built while values stay under the threshold", func(t *testing.T) {
		s := New()
		s.PutValue(values.MakeStringValue("short"))
		s.PutValue(values.MakeStringValue(strings.Repeat("x", 64))) // < 128
		assert.Nil(t, s.cw, "no value crossed the threshold, so cw must still be nil")
	})

	t.Run("built on first compression", func(t *testing.T) {
		s := New()
		h := s.PutValue(values.MakeStringValue(long))
		require.NotNil(t, s.cw, "compressing a long string must build cw")
		assert.Equal(t, long, s.Get(h).String())
	})
}

// TestDisabledCompressionNeverBuildsWriter proves the bonus: a Store with compression disabled never
// pays for the flate writer, even when handed long strings (they are stored verbatim in the arena).
func TestDisabledCompressionNeverBuildsWriter(t *testing.T) {
	long := strings.Repeat("the quick brown fox ", 30)

	s := New(WithEnableCompression(false))
	h := s.PutValue(values.MakeStringValue(long))

	assert.Nil(t, s.cw, "compression disabled: cw must never be built")
	assert.Equal(t, long, s.Get(h).String(), "the long string round-trips, stored uncompressed")

	// header must be a plain large string, not a compressed one
	header := uint8(h & headerMask)
	assert.Equal(t, headerString, header, "value must be stored verbatim, not compressed")
}

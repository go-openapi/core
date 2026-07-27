package store

import (
	"strings"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	"github.com/go-openapi/core/json/stores/values"
)

func TestGob(t *testing.T) {
	s := New(
		WithCompressionLevel(9),
		WithCompressionThreshold(16),
		WithArenaSize(8192))

	value := values.MakeStringValue(strings.Repeat("xyz", 100))
	h := s.PutValue(value)

	t.Run("should serialize the Store", func(t *testing.T) {
		encoded, err := s.MarshalBinary()
		require.NoError(t, err)

		t.Run("should deserialize the Store", func(t *testing.T) {
			ns := New()
			require.NoError(t, ns.UnmarshalBinary(encoded))

			t.Run("should have the original options", func(t *testing.T) {
				assert.Equal(t, s.compressionLevel, ns.compressionLevel)
				assert.Equal(t, s.compressionThreshold, ns.compressionThreshold)
				assert.Equal(t, s.minArenaSize, ns.minArenaSize)
			})

			t.Run("should retrieve value from restored Store", func(t *testing.T) {
				restored := ns.Get(h)

				assert.Equal(t, value, restored)
			})
		})
	})
}

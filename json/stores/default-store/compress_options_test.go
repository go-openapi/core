// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"compress/flate"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
)

func TestOptions(t *testing.T) {
	t.Run("should clamp an out-of-range compression level", func(t *testing.T) {
		hi := optionsWithDefaults([]Option{WithCompressionLevel(12)})
		assert.Equal(t, flate.BestCompression, hi.compressionLevel, "above range clamps to max")

		lo := optionsWithDefaults([]Option{WithCompressionLevel(-10)})
		assert.Equal(t, flate.HuffmanOnly, lo.compressionLevel, "below range clamps to min")
	})

	t.Run("should apply options", func(t *testing.T) {
		o := optionsWithDefaults([]Option{
			WithCompressionLevel(4),
			WithCompressionThreshold(45),
		})

		assert.Equal(t, 4, o.compressionLevel)
		assert.Equal(t, 45, o.compressionThreshold)
		assert.Nil(t, o.cw, "the compression writer is built lazily, not at option time")
		assert.Nil(t, o.dict, "no preset dictionary by default")

		t.Run("should build the writer lazily and cache it", func(t *testing.T) {
			w := o.compressWriter()
			assert.NotNil(t, w)
			assert.Same(t, w, o.compressWriter(), "cw is cached after first build")
		})
	})

	t.Run("options compose left to right and defaults seed the rest", func(t *testing.T) {
		o := optionsWithDefaults([]Option{WithCompressionLevel(9)})

		assert.Equal(t, 9, o.compressionLevel)
		assert.Equal(
			t,
			defaultCompressionThreshold,
			o.compressionThreshold,
			"unset fields keep their defaults",
		)

		base := optionsWithDefaults(nil)
		assert.Equal(
			t,
			defaultCompressionLevel,
			base.compressionLevel,
			"defaults are unaffected by other resolutions",
		)
	})
}

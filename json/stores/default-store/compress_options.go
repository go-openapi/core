package store

import (
	"bytes"
	"compress/flate"
	"io"
)

const (
	defaultCompressionThreshold = 128
	defaultCompressionLevel     = flate.DefaultCompression // i.e. level 6
	minCompressedSize           = 9                        // flate never produces compressed strings smaller then 9 bytes
)

type compressionOptions struct {
	compressionThreshold int
	compressionLevel     int
	cw                   flateWriter
	dict                 []byte
}

type flateReader interface {
	io.ReadCloser
	flate.Resetter
}

type flateWriter interface {
	io.WriteCloser
	Reset(io.Writer)
}

// compressWriter returns the cached DEFLATE writer, building it on first use from the frozen
// (compressionLevel, dict).
//
// cw is a derived artifact of those two fields, which are frozen for the Store's lifetime, so the
// writer and the dict every reader is seeded with can never drift apart. Building it lazily means a
// caller that never compresses a string — compression disabled, or no value ever exceeding the
// threshold — never pays for the (sizeable) flate writer allocation.
//
// It mutates cw, so it must be called on the write path only; for a [ConcurrentStore] that is under
// the write lock (the only place that compresses).
func (co *compressionOptions) compressWriter() flateWriter {
	if co.cw == nil {
		var wrt bytes.Buffer
		cw, err := flate.NewWriterDict(&wrt, co.compressionLevel, co.dict)
		assertCompressOptionWriter(err)
		co.cw = cw
	}

	return co.cw
}

// WithCompressionLevel sets the DEFLATE compression level (see [compress/flate]: [flate.HuffmanOnly]
// (-2) to [flate.BestCompression] (9), with [flate.DefaultCompression] (-1) meaning level 6).
//
// A level outside that range is silently clamped into it.
func WithCompressionLevel(level int) Option {
	level = min(max(level, flate.HuffmanOnly), flate.BestCompression)

	return func(o options) options {
		o.compressionLevel = level

		return o
	}
}

// WithCompressionThreshold sets the minimum string length (in bytes) above which compression is
// attempted. A threshold of 0 disables compression via this path (nothing is ever compressed).
func WithCompressionThreshold(threshold int) Option {
	return func(o options) options {
		o.compressionThreshold = threshold

		return o
	}
}

// WithCompressionDict injects a preset DEFLATE dictionary used to seed both string compression and
// decompression in the store.
//
// A preset dictionary lifts the compression ratio of short, repetitive payloads by priming the
// DEFLATE window with frequently-seen byte sequences. It is typically trained offline from a
// representative corpus.
//
// # Lifecycle and immutability
//
// The dictionary is frozen for the whole lifetime of the store: every value the store compresses and
// every value it later decompresses is bound to this exact dictionary. The store aliases (does not
// copy) the provided slice and never mutates it; the caller MUST NOT mutate dict while any store
// seeded with it is alive, otherwise previously-compressed payloads become undecodable. A gob
// round-trip carries the dictionary along so a reloaded store stays self-consistent.
//
// Because the dictionary is caller-owned, it may outlive the store and be shared across successive
// store generations. This is how a compression dictionary "learns": the corpus is trained externally
// and a fresh, frozen dictionary is injected into the next generation — the dict mutates between
// stores, never within one.
func WithCompressionDict(dict []byte) Option {
	return func(o options) options {
		o.dict = dict

		return o
	}
}

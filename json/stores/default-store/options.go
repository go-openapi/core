package store

const (
	defaultMinArenaSize      = 4096
	defaultEnableCompression = true
)

// Option configures a store ([Store], [ConcurrentStore] or [VerbatimStore]).
//
// An option threads the configuration value through — it receives a copy and returns it — so a call
// like
//
//	s := store.New(store.WithCompressionLevel(9), store.WithCompressionDict(trainedDict), store.WithArenaSize(8192))
//
// builds and applies the configuration entirely on the stack: no allocation, and the configuration
// type stays unexported.
//
// Pass options to [New], [NewConcurrent], [NewVerbatim] or [BorrowStore]. Those constructors are
// variadic for convenience — calling them with no options yields the defaults — and apply the options
// left to right.
type Option func(options) options

// options is the resolved configuration embedded in a store.
type options struct {
	compressionOptions

	enableCompression bool
	minArenaSize      int
}

// WithArenaSize sets the initial capacity of the inner arena that stores large values.
func WithArenaSize(size int) Option {
	return func(o options) options {
		o.minArenaSize = size

		return o
	}
}

// WithEnableCompression enables or disables compression of long strings in the store.
//
// Compression is enabled by default and uses the DEFLATE method from the standard library package
// [compress/flate]. By default it kicks in for strings longer than 128 bytes, at level
// [flate.DefaultCompression] (level 6). Tune it with [WithCompressionLevel], [WithCompressionThreshold]
// and [WithCompressionDict].
//
// A store with compression disabled never allocates a DEFLATE writer (see
// [compressionOptions.compressWriter]) and stores long strings verbatim in the arena.
func WithEnableCompression(enabled bool) Option {
	return func(o options) options {
		o.enableCompression = enabled

		return o
	}
}

// defaultStoreOptions is the immutable seed for [optionsWithDefaults]. Holding it as a value (copied,
// never mutated) keeps optionsWithDefaults small enough to inline, so the variadic option slice does
// not escape and configuring a store stays allocation-free. dict is nil; cw is built lazily.
var defaultStoreOptions = options{ //nolint:gochecknoglobals
	enableCompression: defaultEnableCompression,
	minArenaSize:      defaultMinArenaSize,
	compressionOptions: compressionOptions{
		compressionThreshold: defaultCompressionThreshold,
		compressionLevel:     defaultCompressionLevel,
	},
}

// optionsWithDefaults resolves the effective configuration: it seeds the defaults, then applies the
// options left to right. An empty list yields the defaults.
func optionsWithDefaults(opts []Option) options {
	o := defaultStoreOptions

	for _, apply := range opts {
		o = apply(o)
	}

	return o
}

// getBuffer allocates a fresh buffer for a value decoded by [Store.Get].
//
// It is always a fresh allocation: the returned buffer is aliased by the [values.Value] that Get
// hands back, which the caller may keep, share and read concurrently. For a zero-allocation,
// caller-owned alternative for transient values, see [Store.AppendValueBytes].
func (o *options) getBuffer(capacity int) []byte {
	return make([]byte, 0, capacity)
}

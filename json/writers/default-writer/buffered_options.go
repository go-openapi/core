package writer

const (
	defaultBufferSize = 4096
	minBufferSize     = 6
)

// BufferedOption configures the [Buffered] writer.
//
// An option threads the configuration value through — it receives a copy and returns it — so a call
// like NewBuffered(w, WithBufferSize(8192)) builds and applies the options entirely on the stack: no
// allocation, and the configuration type stays unexported. See [bufferedOptions].
type BufferedOption func(bufferedOptions) bufferedOptions

// WithBufferSize specifies the size in bytes of the write buffer.
//
// The default is 4096.
//
// The minimum value is 6 bytes.
func WithBufferSize(size int) BufferedOption {
	return func(o bufferedOptions) bufferedOptions {
		o.bufferSize = max(size, minBufferSize)

		return o
	}
}

// bufferedOptions carries the (immutable, unexported) [Buffered] configuration.
//
// It holds configuration only. Runtime state such as the working-buffer redeem handle lives on
// [buffered], not here — that separation is what lets the configuration be a plain value (no pooling,
// no finalizer for the options themselves).
type bufferedOptions struct {
	bufferSize int
}

func bufferedOptionsWithDefaults(opts []BufferedOption) bufferedOptions {
	// Seeded with the default; WithBufferSize clamps to >= minBufferSize, and there is no public way to
	// set a non-positive size, so no post-loop guard is needed (keeping this inlinable).
	o := bufferedOptions{bufferSize: defaultBufferSize}

	for _, apply := range opts {
		o = apply(o)
	}

	return o
}

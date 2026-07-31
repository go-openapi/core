package writer

// Error is a sentinel error type for all errors raised by this package.
type Error string

func (e Error) Error() string {
	return string(e)
}

const (
	// ErrDefaultWriter is a sentinel error that wraps all errors raised by this package.
	ErrDefaultWriter Error = "error in default writer"

	// ErrUnsupportedInterface means that a method is called but the underlying buffer does not support this interface.
	ErrUnsupportedInterface Error = "the underlying buffer does not support this interface"

	// ErrInvalidUTF8 means that caller-supplied data is not valid UTF-8 and the writer runs under [UTF8Strict].
	// RFC 8259 §8.1 requires JSON text to be UTF-8, so writing it would emit a document no conforming parser has to
	// accept. Select [UTF8Replace] to substitute U+FFFD instead.
	ErrInvalidUTF8 Error = "data to write is not valid UTF-8"
)

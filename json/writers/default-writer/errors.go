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
)

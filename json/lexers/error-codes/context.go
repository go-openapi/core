package codes

import (
	"fmt"
	"strings"
)

// ErrContext provides context about where a lexing error occurred.
type ErrContext struct {
	Err      error
	Buffer   string // text window around where the error was detected
	Offset   uint64 // errors occurred after reading that many bytes from the stream
	Position int    // Position of the error in the text window
	Path     string // JSON Pointer (RFC 6901) to the node being processed; empty at the document root or for a pure lexing error
}

// Pretty print the error context with a vertical arrow pointed
// at the location of the error in the buffer.
func (e ErrContext) Pretty(windowSize int) string {
	pos := max(0, e.Position-1)
	start := max(0, pos-windowSize/2)
	stop := min(len(e.Buffer), start+windowSize)

	return fmt.Sprintf(
		"%[1]s\n%[2]s^\n%[2]s|\n%[2]s%[3]v\n",
		e.Buffer[start:stop],
		strings.Repeat(" ", pos-start),
		e.Err,
	)
}

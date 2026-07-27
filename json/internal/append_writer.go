package internal

import (
	"io"
	"slices"
)

var _ io.Writer = &AppendWriter{}

type AppendWriter struct {
	b []byte
}

func (a *AppendWriter) Set(b []byte) {
	a.b = b
}

func (a *AppendWriter) Bytes() []byte {
	return a.b
}

func (a *AppendWriter) Write(p []byte) (int, error) {
	a.b = slices.Grow(a.b, len(p))
	a.b = append(a.b, p...)

	return len(p), nil
}

func (a *AppendWriter) Reset() {
	a.b = a.b[:0]
}

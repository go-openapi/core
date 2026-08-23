// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package writers

import (
	"io"

	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/core/json/stores/values"
	"github.com/go-openapi/core/json/types"
)

//nolint:interfacebloat // the writer has a rich interface to support programmatic writing of all JSON tokens
type BaseWriter interface {
	// write delimiters
	StartObject()
	EndObject()
	StartArray()
	EndArray()
	Comma()
	Colon()

	// write native go types
	Null()
	String(string)

	// StringBytes, Raw and NumberBytes must consume their argument synchronously and must NOT retain
	// the slice past the call: callers may pass a pooled or arena-backed buffer that is reused or
	// recycled immediately afterwards. An implementation that needs to keep the bytes must copy them.
	StringBytes([]byte)
	StringRunes([]rune)
	StringCopy(io.Reader)

	Raw([]byte)
	RawCopy(io.Reader)

	Number(any)
	NumberBytes([]byte)
	NumberCopy(io.Reader)

	Bool(bool)

	// Size yields the number of bytes written so far
	Size() int64

	types.ErrStateSetter
	types.Resettable
	types.WithErrState
}

type TokenWriter interface {
	Token(token.T) // write a token

	BaseWriter
}

// VerbatimWriter is the interface for types that know how to write verbatim JSON tokens and values.
type VerbatimWriter interface {
	VerbatimToken(leadingSpace []byte, tok token.T) // write leading blanks + a verbatim token
	VerbatimValue(values.VerbatimValue)             // write a verbatim value

	BaseWriter
}

type Flusher interface {
	Flush() error
}

type StoreWriter interface {
	// write data from a [stores.Store]
	Key(values.InternedKey)
	Value(values.Value)

	BaseWriter
}

type JSONWriter interface {
	// write JSON types
	JSONString(types.String)
	JSONNumber(types.Number)
	JSONBoolean(types.Boolean)
	JSONNull(types.NullType)

	BaseWriter
}

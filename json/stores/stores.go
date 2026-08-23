// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package stores

import (
	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/core/json/stores/values"
	"github.com/go-openapi/core/json/types"
	"github.com/go-openapi/core/json/writers"
)

// Handle distributed by a [Store], which corresponds to some [values.Value].
type Handle uint64

// HandleZero is the zero value of a [Handle].
//
// It represents "no value" (an absent or unset value), which is distinct from a JSON null.
// Resolving it yields [values.UndefinedValue], not a null.
//
// Reserving the zero value for absence means an uninitialized Handle is never mistaken for a
// legitimate null. Use [Handle.IsZero] to test for it.
const HandleZero Handle = 0

// IsZero reports whether the [Handle] is the zero handle [HandleZero], i.e. "no value".
func (h Handle) IsZero() bool {
	return h == HandleZero
}

// Resolve this [Handle] against a [Store].
//
// This is equivalent to calling [Store.Get] with this [Handle].
func (h Handle) Resolve(s Store) values.Value {
	return s.Get(h)
}

// Store is the interface for JSON value stores.
//
// A [Store] knows how to hold the scalar values in a JSON document.
//
// [Store] doesn't have to handle errors as it present a closed, error-free interface.
//
// Corrupted input such as a hand-crafted invalid [Handle] or corrupted [Value] results in a panic.
type Store interface {
	// Get a [Value] given a [Handle].
	//
	// Whenever used in the context of a [VerbatimStore], a [Handle] representing non-significant blank space
	// returns a string [Value].
	//
	// A value returned by Get owns its memory and may be kept, shared and (for stores that support it)
	// read concurrently.
	//
	// For a zero-allocation alternative for transient values, see [Store.AppendValueBytes].
	Get(Handle) values.Value

	// AppendValueBytes is the allocation-free counterpart of [Store.Get], for transient values.
	//
	// It decodes the value for the given [Handle], appends its bytes to dst, and returns the value
	// together with the possibly-grown dst (to reuse on the next call). When dst has spare capacity it
	// does not allocate.
	//
	// The returned value is copied into dst (caller-owned memory): unlike [Store.Get] it never aliases
	// the Store, so it stays valid even after the Store is modified or recycled.
	//
	// It does alias dst, so it is valid only until the caller next writes to or discards dst. Use [Store.Get] for a value
	// that outlives its scratch buffer.
	AppendValueBytes(dst []byte, h Handle) (values.Value, []byte)

	// WriteTo writes the value pointed to by the [Handle] to a [writers.StoreWriter].
	//
	// Using [Store.WriteTo] avoids to create an intermediary [values.Value], if the intent is just to write it.
	//
	// Whenever used in the context of a [VerbatimStore], a [Handle] representing non-significant blank space
	// is written down as-is (and not as a JSON string).
	WriteTo(writers.StoreWriter, Handle)

	// Put in the [Store] a new value from a token [token.T] and return the inner [Handle].
	//
	// Token values are copied into the [Store], and it is safe to reuse the provided [token.T].
	//
	// Only tokens representing a scalar value are  allowed: string, number, bool or null.
	//
	// [Store.PutToken] should panic for invalid tokens such as a separator.
	PutToken(token.T) Handle

	// Put a [values.Value] in the [Store] and return the inner [Handle].
	PutValue(values.Value) Handle

	// PutNull puts a null value into the [Store].
	//
	// This is equivalent to [Store.PutValue] with a null [values.Value]. The returned [Handle] is the
	// (non-zero) null handle, distinct from [HandleZero] which represents the absence of a value.
	PutNull() Handle

	// PutBool puts a bool value into the [Store].
	//
	// This is equivalent to [Store.PutValue] with a boolean [values.Value].
	PutBool(bool) Handle

	// Len yields the current size of the [Store] inner memory arena.
	//
	// Notice that the [Store] does not necessarily store all [Handle] s in the arena.
	Len() int

	// Resettable means the [Store] knows how to reset its internal state so as to be reused.
	//
	// Reset marks the end of a [Store] lifecycle and no values stored are available afterwards.
	types.Resettable
}

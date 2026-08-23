// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package types

import "github.com/go-openapi/swag/pools"

// Resettable is implemented by types that can reset their state.
//
// This is useful when working with pools.
type Resettable = pools.Resettable

// WithErrState is the common interface for all types that manage an internal error state.
//
// This is useful to descend a hierarchical structure without stacking return errors.
type WithErrState interface {
	Ok() bool
	Err() error
}

// ErrStateSetter is the common interface for all types that accept that callers may override their internal error state.
type ErrStateSetter interface {
	SetErr(error)
}

// BytesLoaderFunc: should be from reader
type BytesLoaderFunc func(string) ([]byte, error)

// DocumentShareable allows a [stores.Store] object to share services across the [json.Document]s it holds.
type DocumentShareable interface {
	// Loader function to grab JSON from a remote or local file location.
	Loader() BytesLoaderFunc

	// RefCache acts as a searchable index of documents loaded from a $ref URL
	// RefCache(string) // TODO
}

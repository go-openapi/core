// Package stores exposes the interface to work with a JSON [Store].
//
// A JSON [Store] acts as an in-memory store for scalar [Value] s found in a JSON document.
//
// The [Store] is designed to hold these values in a memory-efficient way.
//
// It delivers [Handle] s to callers, which are akin to references to the stored values.
//
// The package default-store provide default implementations of the [Store] interface.
//
// Package contrib holds possible alternate implementations.
package stores

// Package store provides default implementations for [stores.Store].
//
// It exposes a [Store] type to pack JSON values in memory.
//
// An additional [ConcurrentStore] implementation supports concurrent write access using
// [ConcurrentStore.Get] and [ConcurrentStore.Put].
//
// The [VerbatimStore] implements [stores.VerbatimStore]: it allow users keeping non-significant blank space
// and reconstruct JSON documents verbatim.
package store

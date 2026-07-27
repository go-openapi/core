package store

import (
	"sync"

	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/core/json/stores"
	"github.com/go-openapi/core/json/stores/internal/bcd"
	"github.com/go-openapi/core/json/stores/values"
	"github.com/go-openapi/core/json/writers"
)

// ConcurrentStore is a [stores.Store] just like [Store] and may be used concurrently.
//
// See also [Store]: The [ConcurrentStore] exposes identical behavior and lifecycle constraints.
//
// # Concurrency
//
// It safe to retrieve values concurrently with [store.Get],
// and have several go routines storing content concurrently.
//
// Although it is safe to use [store.WriteTo] concurrently, it should not be used that way, as the result is not deterministic.
type ConcurrentStore struct {
	*Store

	rwx sync.RWMutex
	_   struct{}
}

var _ stores.Store = &ConcurrentStore{} // ConcurrentStore implements [stores.Store]

func NewConcurrent(opts ...Option) *ConcurrentStore {
	return &ConcurrentStore{
		Store: New(opts...),
	}
}

// Len returns the current size in bytes of the inner memory arena.
func (s *ConcurrentStore) Len() int {
	s.rwx.RLock()
	defer s.rwx.RUnlock()
	return len(s.arena)
}

// Get a [values.Value] from a [stores.Handle].
func (s *ConcurrentStore) Get(h stores.Handle) values.Value {
	header := uint8(h & headerMask)

	switch header {
	case headerNone:
		return values.UndefinedValue
	case headerNull:
		return values.NullValue
	case headerFalse:
		return values.FalseValue
	case headerTrue:
		return values.TrueValue
	case headerInlinedNumber: // small number inlined
		return s.getInlinedNumber(h)
	case headerInlinedASCII: // small ascii string inlined: 8 bytes exactly
		return s.getInlinedASCII(h)
	case headerInlinedString: // small string inlined
		return s.getInlinedString(h)
	case headerNumber: // large number
		s.rwx.RLock()
		defer s.rwx.RUnlock()
		return s.getLargeNumber(h)
	case headerString: // large string
		s.rwx.RLock()
		defer s.rwx.RUnlock()
		return s.getLargeString(h)
	case headerCompressedString: // large compressed string
		// we get a new reader (from a pool) rather than locking
		s.rwx.RLock()
		defer s.rwx.RUnlock()
		return s.getCompressedString(h)
	case headerInlinedCompressedString: // small compressed string
		s.rwx.RLock()
		defer s.rwx.RUnlock()
		// this case is not active: flate's minimum size is 9 bytes
		return s.getInlinedCompressedString(h)
	default:
		assertValidHeader(header)
		return values.NullValue
	}
}

// AppendValueBytes is the allocation-free counterpart of [ConcurrentStore.Get]. See
// [Store.AppendValueBytes] for semantics.
func (s *ConcurrentStore) AppendValueBytes(dst []byte, h stores.Handle) (values.Value, []byte) {
	header := uint8(h & headerMask)

	switch header {
	case headerNumber, headerString, headerCompressedString:
		// these read the shared arena: guard against a concurrent Put
		s.rwx.RLock()
		defer s.rwx.RUnlock()

		return s.Store.AppendValueBytes(dst, h)
	default:
		// null, bool and inlined values do not touch the arena
		return s.Store.AppendValueBytes(dst, h)
	}
}

// WriteTo writes the value pointed to be the [stores.Handle] to a JSON [writers.StoreWriter].
//
// This avoids unnessary allocations when transferring the value to the writer.
func (s *ConcurrentStore) WriteTo(writer writers.StoreWriter, h stores.Handle) {
	s.rwx.Lock()
	s.Store.WriteTo(writer, h)
	s.rwx.Unlock()
}

// PutToken puts a value inside a [token.T] and returns its [stores.Handle] for later retrieval.
func (s *ConcurrentStore) PutToken(tok token.T) stores.Handle {
	switch tok.Kind() {
	case token.Null:
		return s.PutNull()

	case token.Boolean:
		return s.PutBool(tok.Bool())

	case token.Number:
		return s.putNumber(tok.Value())

	case token.String, token.Key:
		return s.putString(tok.Value())

	default:
		assertValidToken(tok)
		return stores.HandleZero
	}
}

// PutValue puts a [values.Value] and returns its [stores.Handle] for later retrieval.
func (s *ConcurrentStore) PutValue(v values.Value) stores.Handle {
	switch v.Kind() {
	case token.Null:
		return s.PutNull()

	case token.Boolean:
		return s.PutBool(v.Bool())

	case token.Number:
		return s.putNumber(v.NumberValue().Value)

	case token.String, token.Key:
		return s.putString(v.StringValue().Value)

	default:
		assertValidValue(v)
		return stores.HandleZero
	}
}

func (s *ConcurrentStore) putNumber(value []byte) stores.Handle {
	nibbles, redeem := borrowBytesWithRedeem(bcd.NibbleSize(value))
	defer redeem()
	nibbles = bcd.EncodeNumberAsBCD(value, nibbles)
	if len(nibbles) <= maxInlineBytes {
		return s.putInlinedNumber(nibbles)
	}

	s.rwx.Lock()
	defer s.rwx.Unlock()

	return s.putLargeNumber(nibbles)
}

func (s *ConcurrentStore) putString(value []byte) stores.Handle {
	l := len(value)

	switch {
	case l <= maxInlineBytes:
		return s.putInlinedString(value)
	case l == maxInlineBytes+1 && isOnlyASCII(value):
		return s.putInlinedASCIIString(value)
	case s.enableCompression && s.compressionThreshold > 0 && l > s.compressionThreshold:
		s.rwx.Lock()
		defer s.rwx.Unlock()
		return s.putCompressedString(value)
	default:
		s.rwx.Lock()
		defer s.rwx.Unlock()
		return s.putLargeString(value)
	}
}

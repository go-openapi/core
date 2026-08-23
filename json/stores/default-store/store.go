// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/core/json/stores"
	"github.com/go-openapi/core/json/stores/internal/bcd"
	"github.com/go-openapi/core/json/stores/values"
	"github.com/go-openapi/core/json/writers"
)

const (
	headerBits     = 4
	lengthBits     = 20
	smallBits      = 4
	maxInlineBytes = 7

	headerMask = stores.Handle(uint64(1)<<headerBits - 1)

	// length field for offset in arena
	lengthMask = stores.Handle(uint64(1)<<lengthBits-1) << headerBits
	offsetMask = ^stores.Handle(0) & ^headerMask & ^lengthMask

	// length field for inlined payload
	smallMask   = stores.Handle(uint64(1)<<smallBits-1) << headerBits
	payloadMask = ^stores.Handle(0) & ^headerMask & ^smallMask // 7 bytes
)

// Store is the default implementation for [stores.Store].
//
// It acts an in-memory store for JSON values, with an emphasis on compactness.
//
// # Concurrency
//
// It safe to retrieve values concurrently with [store.Get], but it is unsafe to have
// several go routines storing content concurrently.
//
// [store.WriteTo] should not be used concurrently.
//
// # Lifecycle and value aliasing
//
// For compactness, large (uncompressed) string values returned by [Store.Get] alias the Store's
// internal arena rather than being copied (numbers and all other values are decoded into freshly
// allocated buffers). Consequently:
//
//   - The [Store] must outlive every [values.Value] obtained from it: a consumer that keeps a value
//     keeps a reference into the arena.
//   - A [Store] must not be reset or recycled (see [Store.Reset], [RedeemStore]) while any value
//     borrowed from it is still in use, nor mid-way through constructing a document. Doing so lets
//     subsequent writes overwrite the arena bytes that a live value still points at.
//
// Recycling a Store from the pool is therefore only safe between whole, independent sets of documents
// which values are no longer referenced (e.g. a short-lived untyped JSON exchange).
//
// # Compression configuration
//
// The compression level, threshold and preset dictionary (see [Options.WithCompressionDict]) are frozen
// for as long as the Store holds compressed payloads: every payload in the arena stays decodable against
// the dictionary it was compressed with.
//
// [Store.Reset] rewinds the arena and the configuration together (back to the defaults),
// so a recycled Store never carries a dictionary that would mismatch leftover payloads.
//
// Configure a (recycled) Store at construction or borrow time via [Options] ([New], [BorrowStore]).
type Store struct {
	options

	arena []byte
	_     struct{}
}

var _ stores.Store = &Store{} // [Store] implements [stores.Store]

// New [Store].
//
// Call New() for the defaults, or pass [Option] functions (e.g. [WithCompressionLevel],
// [WithArenaSize]) to alter settings.
func New(opts ...Option) *Store {
	s := &Store{
		options: optionsWithDefaults(opts),
	}

	s.arena = make([]byte, 0, s.minArenaSize)

	return s
}

func (s *Store) Len() int {
	return len(s.arena)
}

// Get a [values.Value] from a [stores.Handle].
//
// A large (uncompressed) string value aliases the Store's internal arena and stays valid only while
// the Store is alive and not reset/recycled (see the "Lifecycle and value aliasing" note on [Store]).
//
// Other values (numbers, inlined and compressed strings) are decoded into fresh buffers and are
// independent of the Store.
func (s *Store) Get(h stores.Handle) values.Value {
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
		return s.getLargeNumber(h)
	case headerString: // large string
		return s.getLargeString(h)
	case headerCompressedString: // large compressed string
		return s.getCompressedString(h)
	case headerInlinedCompressedString: // small compressed string
		// this case is not active before go1.27: flate's minimum size before that is 9 bytes
		return s.getInlinedCompressedString(h)
	default:
		assertValidHeader(header)
		return values.NullValue
	}
}

// AppendValueBytes is the allocation-free counterpart of [Store.Get], for transient values.
//
// It decodes the value identified by h and appends its bytes to dst, returning the value together
// with the possibly-grown dst (for reuse on the next call). When dst has spare capacity it does not
// allocate. Boolean and null values carry no bytes and leave dst unchanged.
//
// Unlike Get, the returned value is a full copy into caller-owned memory: it never aliases the
// Store's arena, so it stays valid even if the Store is modified or recycled. It does however alias
// dst, so it is only valid until the caller next writes to or discards dst. Use this for values that
// are consumed immediately (e.g. a short-lived, per-request Store); use Get for values you keep.
//
// Typical reuse pattern:
//
//	var scratch []byte
//	for _, h := range handles {
//		var v values.Value
//		v, scratch = store.AppendValueBytes(scratch[:0], h)
//		consume(v)
//	}
func (s *Store) AppendValueBytes(dst []byte, h stores.Handle) (values.Value, []byte) {
	header := uint8(h & headerMask)

	switch header {
	case headerNone:
		return values.UndefinedValue, dst
	case headerNull:
		return values.NullValue, dst
	case headerFalse:
		return values.FalseValue, dst
	case headerTrue:
		return values.TrueValue, dst
	case headerInlinedNumber:
		size, payload := inlined(h)
		start := len(dst)
		dst = appendInlinedBCD(dst, size, payload)
		return values.MakeRawValue(token.MakeWithValue(token.Number, dst[start:])), dst
	case headerInlinedASCII:
		_, payload := inlined(h)
		start := len(dst)
		dst = appendUnpackASCII(dst, payload)
		return values.MakeRawValue(token.MakeWithValue(token.String, dst[start:])), dst
	case headerInlinedString:
		size, payload := inlined(h)
		if size == 0 {
			return values.EmptyStringValue, dst
		}
		start := len(dst)
		dst = appendInlinedBytes(dst, size, payload)
		return values.MakeRawValue(token.MakeWithValue(token.String, dst[start:])), dst
	case headerNumber:
		size, offset := withOffset(h)
		assertOffsetInArena(offset, len(s.arena))
		start := len(dst)
		dst = bcd.AppendBCDAsNumber(dst, s.arena[offset:offset+size])
		return values.MakeRawValue(token.MakeWithValue(token.Number, dst[start:])), dst
	case headerString:
		size, offset := withOffset(h)
		assertOffsetInArena(offset, len(s.arena))
		start := len(dst)
		dst = append(dst, s.arena[offset:offset+size]...)
		return values.MakeRawValue(token.MakeWithValue(token.String, dst[start:])), dst
	case headerCompressedString:
		size, offset := withOffset(h)
		assertOffsetInArena(offset, len(s.arena))
		start := len(dst)
		dst = s.appendUncompressString(dst, s.arena[offset:offset+size])
		return values.MakeRawValue(token.MakeWithValue(token.String, dst[start:])), dst
	case headerInlinedCompressedString:
		// this case is not active before go1.27: flate's minimum size before that is 9 bytes
		size, payload := inlined(h)
		// a stack array would escape here: the unpacked payload is handed to the pooled inflate
		// reader. Borrow the scratch so the whole path stays allocation-free.
		buffer, redeem := borrowBytesWithRedeem(maxInlineBytes + 1)
		out := unpackString(size, payload, buffer)
		start := len(dst)
		dst = s.appendUncompressString(dst, out)
		redeem() // the result aliases dst, never out: safe to give the scratch back here
		return values.MakeRawValue(token.MakeWithValue(token.String, dst[start:])), dst
	default:
		assertValidHeader(header)
		return values.NullValue, dst
	}
}

// WriteTo writes the value pointed to be the [stores.Handle] to a JSON [writers.StoreWriter].
//
// This avoids unnessary buffering when transferring the value down to the writer.
func (s *Store) WriteTo(writer writers.StoreWriter, h stores.Handle) {
	header := uint8(h & headerMask)

	switch header {
	case headerNone:
		// undefined value (the zero Handle): marshals as empty, so write nothing
		return
	case headerNull:
		writer.Null()
	case headerFalse:
		writer.Bool(false)
	case headerTrue:
		writer.Bool(true)
	case headerInlinedNumber: // small number inlined
		size, payload := inlined(h)
		buffer, redeem := borrowBytesWithRedeem(
			size * bcd.DigitsPerByte,
		) // amortize the allocation of this temporary buffer
		buffer = unpackBCD(size, payload, buffer)
		writer.NumberBytes(buffer) // sends the buffer directly to the writer
		redeem()
	case headerInlinedASCII: // small ascii string inlined: 8 bytes exactly
		size, payload := inlined(h) // 7 bytes
		// writer is an interface, so out escapes and a stack array would be heap-allocated on every
		// call: amortize it through the pool instead.
		buffer, redeem := borrowBytesWithRedeem(maxInlineBytes + 1)
		writer.StringBytes(unpackASCII(size, payload, buffer))
		redeem()
	case headerInlinedString: // small string inlined
		size, payload := inlined(h) // 0-7 bytes (0-8 packed characters)
		if size == 0 {
			writer.String("")
			return
		}
		buffer, redeem := borrowBytesWithRedeem(maxInlineBytes + 1)
		writer.StringBytes(unpackString(size, payload, buffer))
		redeem()
	case headerNumber: // large number
		size, offset := withOffset(h)
		assertOffsetInArena(offset, len(s.arena))

		nibbles := s.arena[offset : offset+size]
		buffer, redeem := borrowBytesWithRedeem(size * bcd.DigitsPerByte)
		buffer = bcd.DecodeBCDAsNumber(nibbles, buffer)
		writer.NumberBytes(buffer)
		redeem()
	case headerString: // large string
		size, offset := withOffset(h)
		assertOffsetInArena(offset, len(s.arena))

		strBytes := s.arena[offset : offset+size]
		writer.StringBytes(strBytes)
	case headerCompressedString: // large compressed string
		size, offset := withOffset(h)
		assertOffsetInArena(offset, len(s.arena))

		session := s.uncompressStringReader(s.arena[offset : offset+size])
		writer.StringCopy(session.reader)
		session.redeem()
	case headerInlinedCompressedString: // small compressed string
		// this case is not active before go1.27: flate's minimum size before that is 9 bytes
		size, payload := inlined(h) // 0-7 bytes
		buffer, redeemBuffer := borrowBytesWithRedeem(maxInlineBytes + 1)
		out := unpackString(size, payload, buffer)
		session := s.uncompressStringReader(out)
		writer.StringCopy(session.reader)
		session.redeem()
		redeemBuffer()
	default:
		assertValidHeader(header)
	}
}

// PutToken puts a value inside a [token.T] and returns its [stores.Handle] for later retrieval.
func (s *Store) PutToken(tok token.T) stores.Handle {
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
		return stores.HandleZero // no value for an unsupported token
	}
}

// PutValue puts a [values.Value] and returns its [stores.Handle] for later retrieval.
func (s *Store) PutValue(v values.Value) stores.Handle {
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
		assertValidValue(
			v,
		) // moved to guards: it is normally not possible to build an invalid values.Value
		return stores.HandleZero // no value for an undefined/unsupported value
	}
}

// PutNull is a shorthand for putting a null value.
//
// The returned [stores.Handle] is the constant null handle (a non-zero value): the zero Handle
// ([stores.HandleZero]) is reserved for "no value", which is distinct from a JSON null.
func (s *Store) PutNull() stores.Handle {
	return stores.Handle(headerNull)
}

// PutBool is a shorthand for putting a bool value.
func (s *Store) PutBool(b bool) stores.Handle {
	if b {
		return stores.Handle(headerTrue)
	}
	return stores.Handle(headerFalse)
}

// Reset the [Store] to its initial state.
//
// This is useful to recycle [Store] s from a memory pool.
//
// The caller must ensure no [values.Value] previously returned by [Store.Get] is still in use, and
// that no document is mid-construction: large string values alias the arena that Reset rewinds (see
// the "Lifecycle and value aliasing" note on [Store]).
//
// Reset rewinds the arena (the per-document data) and restores the default configuration, so a
// recycled Store starts from a clean slate: any injected dictionary reference is released and the
// lazily-built compression writer is dropped (it rebuilds on demand). This is cheap — the defaults
// are two ints and a nil dict (see [optionsWithDefaults]).
//
// Reconfigure a recycled Store at borrow time via [BorrowStore]; the dictionary, being caller-owned,
// is simply re-injected (aliased, not copied) for the next generation.
//
// Implements [pools.Resettable].
func (s *Store) Reset() {
	s.arena = s.arena[:0]
	s.options = optionsWithDefaults(nil)
}

func (s *Store) getInlinedNumber(h stores.Handle) values.Value {
	size, payload := inlined(h)
	buffer := s.getBuffer(maxInlineBytes + 1)

	return values.MakeRawValue(token.MakeWithValue(token.Number, unpackBCD(size, payload, buffer)))
}

func (s *Store) getInlinedASCII(h stores.Handle) values.Value {
	size, payload := inlined(h) // 7 bytes (0-8 packed characters)
	// convention: in this case, size is always equal to 8
	buffer := s.getBuffer(maxInlineBytes + 1)

	return values.MakeRawValue(
		token.MakeWithValue(token.String, unpackASCII(size, payload, buffer)),
	)
}

func (s *Store) getInlinedString(h stores.Handle) values.Value {
	size, payload := inlined(h) // 0-7 bytes
	if size == 0 {
		return values.EmptyStringValue
	}
	buffer := s.getBuffer(maxInlineBytes + 1)

	return values.MakeRawValue(
		token.MakeWithValue(token.String, unpackString(size, payload, buffer)),
	)
}

func (s *Store) getLargeNumber(h stores.Handle) values.Value {
	size, offset := withOffset(h)
	assertOffsetInArena(offset, len(s.arena))
	nibbles := s.arena[offset : offset+size]
	buffer := s.getBuffer(bcd.DigitsPerByte * size)

	return values.MakeRawValue(
		token.MakeWithValue(token.Number, bcd.DecodeBCDAsNumber(nibbles, buffer)),
	)
}

func (s *Store) getLargeString(h stores.Handle, _ ...[]byte) values.Value {
	size, offset := withOffset(h)
	assertOffsetInArena(offset, len(s.arena))
	strBytes := s.arena[offset : offset+size]

	return values.MakeRawValue(token.MakeWithValue(token.String, strBytes))
}

func (s *Store) getInlinedCompressedString(h stores.Handle) values.Value {
	size, payload := inlined(h) // 0-7 bytes
	// the unpacked payload is handed to the (pooled, hence heap-resident) inflate reader, so a stack
	// array would escape: borrow the scratch instead.
	buf, redeem := borrowBytesWithRedeem(maxInlineBytes + 1)
	defer redeem()
	out := unpackString(size, payload, buf)
	uncompressed := s.uncompressString(out) // the caller owns the result: it cannot be recycled

	return values.MakeRawValue(token.MakeWithValue(token.String, uncompressed))
}

func (s *Store) getCompressedString(h stores.Handle) values.Value {
	size, offset := withOffset(h)
	assertOffsetInArena(offset, len(s.arena))

	// No pre-sized buffer here: uncompressString allocates the result once it knows the inflated
	// size, so the caller gets an exactly-sized buffer and a wrong ratio guess can never cost a
	// second allocation.
	uncompressed := s.uncompressString(s.arena[offset : offset+size])

	return values.MakeRawValue(token.MakeWithValue(token.String, uncompressed))
}

func (s *Store) putNumber(value []byte) stores.Handle {
	nibbles, redeem := borrowBytesWithRedeem(bcd.NibbleSize(value))
	defer redeem()
	nibbles = bcd.EncodeNumberAsBCD(value, nibbles)
	if len(nibbles) <= maxInlineBytes {
		return s.putInlinedNumber(nibbles)
	}

	return s.putLargeNumber(nibbles)
}

func (s *Store) putString(value []byte) stores.Handle {
	l := len(value)

	switch {
	case l <= maxInlineBytes:
		return s.putInlinedString(value)
	case l == maxInlineBytes+1 && isOnlyASCII(value):
		return s.putInlinedASCIIString(value)
	case s.enableCompression && s.compressionThreshold > 0 && l > s.compressionThreshold:
		return s.putCompressedString(value)
	default:
		return s.putLargeString(value)
	}
}

func (s *Store) putInlinedString(value []byte) stores.Handle {
	// inlined string (up to 7 bytes)
	l := len(value)
	const headerPart = uint64(headerInlinedString)
	lengthPart := uint64(l) << headerBits
	payload := packString(value) << (headerBits + smallBits)

	return stores.Handle(headerPart | lengthPart | payload)
}

func (s *Store) putInlinedASCIIString(value []byte) stores.Handle {
	// inlined 8 bytes ASCII-only string
	const headerPart = uint64(headerInlinedASCII)
	const lengthPart = uint64(maxInlineBytes) << headerBits
	payload := packASCII(value) << (headerBits + smallBits)

	return stores.Handle(headerPart | lengthPart | payload)
}

func (s *Store) putLargeString(value []byte) stores.Handle {
	// long string put into arena
	l := len(value)
	const headerPart = uint64(headerString)
	lengthPart := uint64(l) << headerBits
	offsetPart := uint64(len(s.arena)) << (headerBits + lengthBits)
	s.arena = append(s.arena, value...)

	return stores.Handle(headerPart | lengthPart | offsetPart)
}

func (s *Store) putCompressedString(value []byte) stores.Handle {
	// the scratch is sized for the worst case, not for len(value): DEFLATE may inflate data that
	// does not compress (see [compressBound]).
	buffer, redeem := borrowBytesWithRedeem(compressBound(len(value)))
	defer redeem()
	compressed := s.compressString(value, buffer)
	l := len(compressed)

	if l >= len(value) {
		// DEFLATE did not shrink this value. Keep the original bytes: storing the "compressed" form
		// would cost more arena space *and* an inflate round on every read.
		return s.putLargeString(value)
	}

	if l > maxInlineBytes {
		const headerPart = uint64(headerCompressedString)
		lengthPart := uint64(l) << headerBits
		offsetPart := uint64(len(s.arena)) << (headerBits + lengthBits)
		s.arena = append(s.arena, compressed...)

		return stores.Handle(headerPart | lengthPart | offsetPart)
	}

	// this part starts being active with go1.27: where min length of a compressed string
	// may be less than 9 bytes.
	const headerPart = uint64(headerInlinedCompressedString)
	lengthPart := uint64(l) << headerBits
	payload := packString(compressed) << (headerBits + smallBits)

	return stores.Handle(headerPart | lengthPart | payload)
}

func (s *Store) putInlinedNumber(nibbles []byte) stores.Handle {
	// inlined number
	const headerPart = uint64(headerInlinedNumber)
	sizePart := uint64(len(nibbles)) << headerBits
	payload := packBCD(nibbles) << (headerBits + smallBits)

	return stores.Handle(headerPart | sizePart | payload)
}

func (s *Store) putLargeNumber(nibbles []byte) stores.Handle {
	// BCD number put into arena
	const headerPart = uint64(headerNumber)
	lengthPart := uint64(len(nibbles)) << headerBits
	offsetPart := uint64(len(s.arena)) << (headerBits + lengthBits)
	s.arena = append(s.arena, nibbles...)

	return stores.Handle(headerPart | lengthPart | offsetPart)
}

// withOffset extracts the size and offset in arena from a handle
//
//nolint:gosec
func withOffset(h stores.Handle) (size int, offset int) {
	size = int((h & lengthMask) >> headerBits)
	offset = int(h&offsetMask) >> (headerBits + lengthBits)
	assertOffsetAddressable(
		offset,
	) // impossible on 64-bit systems, theoretically possible on 32-bits systems if the handle is corrupted.

	return
}

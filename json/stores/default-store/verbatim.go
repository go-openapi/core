package store

import (
	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/core/json/stores"
	"github.com/go-openapi/core/json/stores/values"
	"github.com/go-openapi/core/json/writers"
)

var _ stores.VerbatimStore = &VerbatimStore{} // [VerbatimStore] implements [stores.VerbatimStore]

const (
	blank          = ' '
	tab            = '\t'
	lineFeed       = '\n'
	carriageReturn = '\r'

	blankBits       = 6 // bits used to store the length of an inlined blank string
	bitsPerBlank    = 2
	blanksPerByte   = 8 / bitsPerBlank
	maxInlineBlanks = 27 // < 2^blankBits  (54 bits)

	// length field for inlined payload
	blankMask        = stores.Handle(uint64(1)<<blankBits-1) << headerBits
	blankPayloadMask = ^stores.Handle(0) & ^headerMask & ^blankMask // 6 bytes + 6 bits
)

// VerbatimStore is like [Store], but with the ability to store and retrieve non-significant blank space,
// such as indentation, space before commas, line feeds, etc.
//
// This [stores.VerbatimStore] is designed to hold and reconstruct verbatim JSON documents. It not safe to
// use concurrently.
//
// # JSON blanks
//
// Valid blank space characters in JSON are: blank, tab, carriageReturn and lineFeed.
//
// The generalized notion of blank space in unicode does not apply (e.g. with unicode property "WSpace = Y")
// and should result in invalid tokens when parsing your JSON.
type VerbatimStore struct {
	*Store

	blankArena []byte // a memory arena dedicated to storing non-significant blanks
	_          struct{}
}

func NewVerbatim(opts ...Option) *VerbatimStore {
	s := &VerbatimStore{
		Store: New(opts...),
	}
	s.blankArena = make([]byte, 0, s.minArenaSize)

	return s
}

// Reset the store so it can be recycled. Implements [pools.Resettable].
func (s *VerbatimStore) Reset() {
	s.Store.Reset()
	s.blankArena = s.blankArena[:0]
}

// Get a [values.Value] from a [stores.Handle].
func (s *VerbatimStore) Get(h stores.Handle) values.Value {
	header := uint8(h & headerMask)

	if header != headerInlinedBlank && header != headerCompressedBlank { // not a blank string
		return s.Store.Get(h)
	}

	return s.getBlankValue(header, h)
}

// AppendValueBytes is the allocation-free counterpart of [VerbatimStore.Get]. See
// [Store.AppendValueBytes] for semantics. Blank-space handles decode into dst as a string value, as
// they do with [VerbatimStore.Get].
func (s *VerbatimStore) AppendValueBytes(dst []byte, h stores.Handle) (values.Value, []byte) {
	header := uint8(h & headerMask)

	switch header {
	case headerInlinedBlank:
		size, payload := inlinedBlanks(h)
		if size == 0 {
			return values.EmptyStringValue, dst
		}
		start := len(dst)
		dst = appendUnpackBlanks(dst, size, payload)

		return values.MakeRawValue(token.MakeWithValue(token.String, dst[start:])), dst
	case headerCompressedBlank:
		size, offset := withOffset(h)
		assertOffsetInArena(offset, len(s.blankArena))
		start := len(dst)
		dst = s.appendUncompressString(dst, s.blankArena[offset:offset+size])

		return values.MakeRawValue(token.MakeWithValue(token.String, dst[start:])), dst
	default: // not a blank string
		return s.Store.AppendValueBytes(dst, h)
	}
}

func (s *VerbatimStore) GetVerbatim(h stores.VerbatimHandle) values.VerbatimValue {
	blanks := s.Get(h.Blanks())
	value := s.Get(h.Value())

	return values.MakeVerbatimValue(blanks.Bytes(), value)
}

func (s *VerbatimStore) WriteTo(writer writers.StoreWriter, h stores.Handle) {
	header := uint8(h & headerMask)

	switch header {
	case headerInlinedBlank:
		// writer is an interface, so the unpacked blanks escape: borrow the scratch rather than
		// heap-allocating a stack array on every call.
		buffer, redeem := borrowBytesWithRedeem(maxInlineBlanks)
		writer.Raw(s.getInlinedBlanks(h, buffer))
		redeem()
	case headerCompressedBlank:
		size, offset := withOffset(h)
		assertOffsetInArena(offset, len(s.blankArena))
		session := s.uncompressStringReader(s.blankArena[offset : offset+size])
		writer.RawCopy(session.reader)
		session.redeem()
	default: // not a blank string
		s.Store.WriteTo(writer, h)
	}
}

func (s *VerbatimStore) PutVerbatimToken(leadingSpace []byte, tok token.T) stores.VerbatimHandle {
	blanksHandle := s.putBlanks(leadingSpace)
	value := s.PutToken(tok)

	return stores.MakeVerbatimHandle(blanksHandle, value)
}

func (s *VerbatimStore) PutVerbatimValue(v values.VerbatimValue) stores.VerbatimHandle {
	blanks := s.putBlanks(v.Blanks())
	value := s.PutValue(v.Value)

	return stores.MakeVerbatimHandle(blanks, value)
}

func (s *VerbatimStore) PutBlanks(blanks []byte) stores.Handle {
	return s.putBlanks(blanks)
}

func (s *VerbatimStore) getBlankValue(header uint8, h stores.Handle) values.Value {
	return values.MakeRawValue(token.MakeWithValue(token.String, s.getBlanks(header, h)))
}

func (s *VerbatimStore) getBlanks(header uint8, h stores.Handle) []byte {
	switch header {
	case headerInlinedBlank:
		buffer := s.getBuffer(maxInlineBlanks)
		return s.getInlinedBlanks(h, buffer)

	case headerCompressedBlank:
		return s.getCompressedBlanks(h)
	default:
		assertBlankHeader(header)
	}

	return nil
}

func (s *VerbatimStore) putBlanks(blanks []byte) stores.Handle {
	assertVerbatimOnlyBlanks(blanks)

	if len(blanks) < maxInlineBlanks+1 {
		return s.putInlinedBlanks(blanks)
	}

	return s.putCompressedBlanks(blanks)
}

func (s *VerbatimStore) getInlinedBlanks(h stores.Handle, buffer []byte) []byte {
	size, payload := inlinedBlanks(h) // 7 bytes (0-28 packed 2-bit blank signs)
	if size == 0 {
		return nil
	}

	return unpackBlanks(size, payload, buffer)
}

func (s *VerbatimStore) putInlinedBlanks(value []byte) stores.Handle {
	const headerPart = uint64(headerInlinedBlank)
	sizePart := uint64(len(value)) << headerBits
	payloadPart := packBlanks(value) << (headerBits + blankBits)

	return stores.Handle(headerPart | sizePart | payloadPart)
}

func (s *VerbatimStore) getCompressedBlanks(h stores.Handle) []byte {
	size, offset := withOffset(h)
	assertOffsetInArena(offset, len(s.blankArena))
	buffer := s.getBuffer(s.uncompressRatioHeuristic(size))
	return s.uncompressString(s.blankArena[offset:offset+size], buffer)
}

func (s *VerbatimStore) putCompressedBlanks(value []byte) stores.Handle {
	buffer, redeem := borrowBytesWithRedeem(len(value))
	defer redeem()
	compressed := s.compressString(value, buffer)

	const headerPart = uint64(headerCompressedBlank)
	lengthPart := uint64(len(compressed)) << headerBits
	offsetPart := uint64(len(s.blankArena)) << (headerBits + lengthBits)
	s.blankArena = append(s.blankArena, compressed...)

	return stores.Handle(headerPart | lengthPart | offsetPart)
}

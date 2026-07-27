package bcd

import "slices"

const (
	bcdFiller  = 0xf
	nibbleBits = 4
)

//nolint:mnd,gochecknoglobals
var bcdEncoding = map[byte]byte{
	'0': 0x0,
	'1': 0x1,
	'2': 0x2,
	'3': 0x3,
	'4': 0x4,
	'5': 0x5,
	'6': 0x6,
	'7': 0x7,
	'8': 0x8,
	'9': 0x9,
	'.': 0xa,
	'e': 0xb,
	'E': 0xc,
	'-': 0xd,
	'+': 0xe,
}

//nolint:gochecknoglobals
var bcdDecoding = map[byte]byte{
	0x0: '0',
	0x1: '1',
	0x2: '2',
	0x3: '3',
	0x4: '4',
	0x5: '5',
	0x6: '6',
	0x7: '7',
	0x8: '8',
	0x9: '9',
	0xa: '.',
	0xb: 'e',
	0xc: 'E',
	0xd: '-',
	0xe: '+',
}

// DigitsPerByte is the number of digit nibbles we pack in a single byte.
const DigitsPerByte = 2

// EncodeNumberAsBCD encodes an input numeric string into BCD.
func EncodeNumberAsBCD(in, out []byte) []byte {
	l := nibbleSize(in)
	assertBCDOutCapacity(out, l)
	out = out[:l]

	size := len(in)
	j := 0
	for i := 0; i < size; i++ { // todo use slices.Chunk? not sure about perf
		digit := in[i]
		nibble1, ok := bcdEncoding[digit]
		assertBCDDigit(ok, digit)
		i++

		if i >= size {
			out[j] = nibble1 | (bcdFiller << nibbleBits)
			j++

			break
		}

		digit = in[i]
		nibble2, ok := bcdEncoding[digit]
		assertBCDDigit(ok, digit)

		out[j] = nibble1 | (nibble2 << nibbleBits)
		j++
	}

	return out[:j]
}

// DecodeBCDAsNumber transforms BCD nibbles into decimal digits.
//
// The returned string is an ASCII decimal representation of the number.
//
// Unless an extra buffer is explicitly provided, this allocates the result slice of bytes to return to the end user.
func DecodeBCDAsNumber(in []byte, buffer ...[]byte) []byte {
	const nibbleMask = 0xf

	var out []byte
	size := DigitsPerByte * len(in)
	if len(buffer) == 0 || buffer[0] == nil {
		out = make([]byte, 0, size) // this allocation is returned to the caller. Can't recycle it.
	} else {
		out = slices.Grow(buffer[0], size)
		out = out[:0]
	}

	for _, nibbles := range in {
		nibble1 := nibbles & nibbleMask
		nibble2 := nibbles >> nibbleBits
		if nibble1 == bcdFiller {
			break
		}
		digit1, ok := bcdDecoding[nibble1]
		assertBCDNibble(ok, nibble1)

		out = append(out, digit1)
		if nibble2 == bcdFiller {
			break
		}

		digit2, ok := bcdDecoding[nibble2]
		assertBCDNibble(ok, nibble2)
		out = append(out, digit2)
	}

	return out
}

// AppendBCDAsNumber decodes BCD nibbles and appends the resulting decimal digits to dst,
// returning the extended slice.
//
// It is the append-style counterpart of [DecodeBCDAsNumber] to decode into a caller-owned buffer without allocating.
func AppendBCDAsNumber(dst, in []byte) []byte {
	const nibbleMask = 0xf

	for _, nibbles := range in {
		nibble1 := nibbles & nibbleMask
		if nibble1 == bcdFiller {
			break
		}

		digit1, ok := bcdDecoding[nibble1]
		assertBCDNibble(ok, nibble1)
		dst = append(dst, digit1)

		nibble2 := nibbles >> nibbleBits
		if nibble2 == bcdFiller {
			break
		}

		digit2, ok := bcdDecoding[nibble2]
		assertBCDNibble(ok, nibble2)
		dst = append(dst, digit2)
	}

	return dst
}

// NibbleSize returns the size of the
func NibbleSize(value []byte) int {
	return nibbleSize(value)
}

func nibbleSize(value []byte) int {
	return len(value)/2 + len(value)%2
}

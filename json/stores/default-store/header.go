package store

// The header of th uint64 that a [stores.Handle] gives us
// is encoded on 4 bits. This leaves 16 possibilities to encode your piece of data.

const (
	// headerNone is the header of the zero [stores.Handle]: it represents "no value" (an absent or
	// unset value), which is distinct from a JSON null (see headerNull). Reserving 0 for "none" keeps
	// the zero value of a Handle from being mistaken for a legitimate null.
	headerNone uint8 = iota
	headerNull
	headerFalse
	headerTrue
	headerInlinedNumber
	headerInlinedASCII
	headerInlinedString
	headerNumber
	headerString
	headerCompressedString
	headerInlinedBlank
	headerCompressedBlank
	headerInlinedCompressedString // unused for now: DEFLATE produces strings of at least 9 bytes
)

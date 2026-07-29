package input

import "io"

// Input holds the lexer's buffered-input and scan-cursor state: the buffer window and read position, the
// value-accumulation scratch, the reader and refill buffers, the error state, and the two grammar flags the string
// scanner consults to finish a value (ExpectKey/AfterKey).
//
// It is the cohesive, L-independent state the value scanners operate on; [L] embeds it as `in`.
// Its fields are exported because the scan cores (in package lexer) read and advance the cursor directly in their hot
// loops, and Input lives in its own internal package.
type Input struct {
	R   io.Reader
	Err error

	Buffer         []byte // determined by bufferSize
	CurrentValue   []byte // capped if MaxValueBytes > 0
	PreviousBuffer []byte // used when KeepPreviousBuffer > 0

	Offset     uint64
	Consumed   int
	Bufferized int

	WholeBuffer   bool // the buffer holds the entire input (no refills): values may alias it
	NeedFirstFill bool // streaming: the initial read (and whole-buffer short-circuit) is pending
	ExpectKey     bool
	AfterKey      bool // the previous token was an object key: a ':' must follow

	// TrackBlanks mirrors L.trackBlanks for ConsumeString's dispatch to the raw (validate-not-decode) string scanners.
	//
	// It is deliberately duplicated rather than moved: the cores read L.trackBlanks on the hot whitespace-skip path, and
	// keeping those refs off Input keeps the core codegen byte-identical.
	// Set by VL setup alongside L.trackBlanks; L.reset() does not clear it.
	TrackBlanks bool

	// NoAVX2 disables the AVX2 string-stop scanner (mirrors options.noAVX2), and MaxValueBytes / KeepPreviousBuffer mirror
	// the like-named options so the value scanners and ReadMore can enforce the caps/gates without reaching back into L.
	// The mirrored options are set in L.reset().
	//
	// Placed last so the hot cursor fields keep their offsets.
	NoAVX2             bool
	MaxValueBytes      int
	KeepPreviousBuffer int

	// ValidateMode (PROTOTYPE) selects the UTF-8 validation strategy.
	ValidateMode int

	// SawNonASCII (PROTOTYPE) reports whether the scan observed a byte >= 0x80 in the value just produced.
	SawNonASCII bool
}

// UTF-8 validation strategies (PROTOTYPE).
const (
	ValidateOff   = 0 // current behavior: no validation
	ValidateNaive = 1 // utf8.Valid over every string value at the funnel
	ValidateFused = 2 // non-ASCII accumulated during the existing scan; utf8.Valid only when a high bit was seen
)

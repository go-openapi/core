package input

import (
	"bytes"
	"errors"
	"io"

	codes "github.com/go-openapi/core/json/lexers/error-codes"
	"github.com/go-openapi/core/json/lexers/token"
)

// FirstFill performs the initial read for a streaming lexer and SHORT-CIRCUITS to whole-buffer mode when the entire
// input fits in the buffer (§10.5f).
//
// It reads into in.Buffer until the buffer is full or the reader is exhausted; if EOF arrives before the buffer fills,
// the whole input is now buffered, so the lexer flips to WholeBuffer mode — thereafter the fast in-buffer cores run
// (and, for Tokens(), the native whole-buffer push core instead of the NextToken+closure fallthrough) with no per-token
// streaming overhead.
//
// Inputs larger than the buffer stay in streaming mode with the first window pre-filled.
// A non-EOF read error is recorded in in.Err.
//
// It is done LAZILY (first NextToken/Tokens, gated by NeedFirstFill) rather than at construction, so a caller that
// reslices in.Buffer to force a narrow window (tests) still gets the window it asked for.
// Runs exactly once per bound input.
func (in *Input) FirstFill() {
	in.NeedFirstFill = false

	n := 0
	for n < len(in.Buffer) {
		m, err := in.R.Read(in.Buffer[n:])
		n += m
		if err != nil {
			in.Bufferized = n
			if errors.Is(err, io.EOF) {
				in.WholeBuffer = true // whole input fits: run the fast in-buffer cores
			} else {
				in.Err = err
			}

			return
		}
		if m == 0 {
			break // reader returned (0, nil): don't spin — stay streaming
		}
	}
	in.Bufferized = n
}

// ReadMore provides more input from the internal buffer or consumes from the input stream.
//
// This is a private implementation for a simplified buffered reader, allowing us to scan bytes without nested function
// calls at every single byte.
func (in *Input) ReadMore() error {
	if in.Consumed < in.Bufferized {
		return nil
	}

	if in.KeepPreviousBuffer > 0 {
		// copy the start of the buffer before reuse, for error context
		if in.PreviousBuffer == nil {
			in.PreviousBuffer = make([]byte, 0, in.KeepPreviousBuffer)
		}

		copied := min(in.KeepPreviousBuffer, in.Bufferized)
		in.PreviousBuffer = in.PreviousBuffer[0:copied]
		copy(in.PreviousBuffer, in.Buffer[:copied])
	}

	var err error
	in.Bufferized, err = in.R.Read(in.Buffer)
	if err != nil {
		in.Bufferized = 0

		return err
	}

	in.Consumed = 0

	return nil
}

// ConsumeNull scans the remaining bytes of a null literal (the leading 'n' was already consumed by the core).
func (in *Input) ConsumeNull(_ byte) token.T {
	var buf [3]byte
	if err := in.consumeN(buf[:]); err != nil {
		in.Err = codes.ErrInvalidToken

		return token.None
	}

	// n[ull]
	if !bytes.Equal(buf[:], []byte("ull")) {
		in.Err = codes.ErrInvalidToken

		return token.None
	}

	return token.NullToken
}

// consumeN consumes a small buffer of n bytes to decide tokens such as "true", "false" or "null".
func (in *Input) consumeN(buffer []byte) error {
	minReadSize := len(buffer)
	n := 0

	for {
		if err := in.ReadMore(); err != nil {
			return err
		}

		// need is how many more bytes this token still requires.
		need := minReadSize - n
		if delta := in.Bufferized - in.Consumed; delta < need {
			// the window holds fewer than we need: take all of it, then refill.
			copy(buffer[n:], in.Buffer[in.Consumed:in.Bufferized])
			in.Consumed += delta
			in.Offset += uint64(delta)
			n += delta

			continue
		}

		// the window holds at least `need` bytes: take EXACTLY need, advancing consumed by need — NOT by the whole window.
		// Advancing by the full delta (the old `delta < minReadSize` form) skipped the surplus, which belongs to the
		// following token, breaking a literal read through a window smaller than the literal (e.g. "true"/"null" via a 2-byte
		// buffer dropped the trailing separator).
		//
		// Unreachable once WithBufferSize floors the window, but correct at any size.
		copy(buffer[n:minReadSize], in.Buffer[in.Consumed:in.Consumed+need])
		in.Consumed += need
		in.Offset += uint64(need)

		break
	}

	return nil
}

package input

import (
	"bytes"

	codes "github.com/go-openapi/core/json/lexers/error-codes"
	"github.com/go-openapi/core/json/lexers/token"
)

func (in *Input) ConsumeBoolean(start byte) token.T {
	var buf [3]byte
	if err := in.consumeN(buf[:]); err != nil {
		in.Err = codes.ErrInvalidToken

		return token.None
	}

	var value bool

	// t[rue] | f[als][e]
	switch {
	case start == startOfTrue && bytes.Equal(buf[:], []byte("rue")):
		value = true
	case start == startOfFalse && bytes.Equal(buf[:], []byte("als")):
		if in.Consumed >= in.Bufferized {
			if err := in.ReadMore(); err != nil {
				in.Err = codes.ErrInvalidToken

				return token.None
			}
		}
		next := in.Buffer[in.Consumed]
		in.Consumed++
		in.Offset++

		if next != 'e' {
			in.Err = codes.ErrInvalidToken

			return token.None
		}

		value = false
	default:
		in.Err = codes.ErrInvalidToken

		return token.None
	}

	return token.MakeBoolean(value)
}

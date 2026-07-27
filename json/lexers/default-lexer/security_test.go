package lexer

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	codes "github.com/go-openapi/core/json/lexers/error-codes"
)

func TestMaxValueBytes(t *testing.T) {
	const limit = 100

	bigString := `"` + strings.Repeat("a", 10*limit) + `"`
	bigNumber := "1" + strings.Repeat("0", 10*limit)
	smallString := `"` + strings.Repeat("a", limit/2) + `"`

	t.Run("L trips on oversized string", func(t *testing.T) {
		err := drainErr(NewWithBytes([]byte(bigString), WithMaxValueBytes(limit)))
		require.ErrorIs(t, err, codes.ErrMaxValueBytes)
	})

	t.Run("L trips on oversized number", func(t *testing.T) {
		err := drainErr(NewWithBytes([]byte(bigNumber), WithMaxValueBytes(limit)))
		require.ErrorIs(t, err, codes.ErrMaxValueBytes)
	})

	// Regression: VL embedded a shadow options struct that was never populated, so its value cap was dead.
	// It must now trip like L.
	t.Run("VL trips on oversized string", func(t *testing.T) {
		err := drainVErr(NewVerbatimWithBytes([]byte(bigString), WithMaxValueBytes(limit)))
		require.ErrorIs(t, err, codes.ErrMaxValueBytes)
	})

	t.Run("VL trips on oversized number", func(t *testing.T) {
		err := drainVErr(NewVerbatimWithBytes([]byte(bigNumber), WithMaxValueBytes(limit)))
		require.ErrorIs(t, err, codes.ErrMaxValueBytes)
	})

	t.Run("value within the limit is accepted", func(t *testing.T) {
		require.NoError(t, drainErr(NewWithBytes([]byte(smallString), WithMaxValueBytes(limit))))
		require.NoError(
			t,
			drainVErr(NewVerbatimWithBytes([]byte(smallString), WithMaxValueBytes(limit))),
		)
	})

	// Slow-path coverage: a leading escape forces the unescape path, where the bound is checked against len + the
	// clean-run width *before* the bulk append.
	// These pin that the batched copy still rejects an over-long value mid run.
	bigEscaped := `"\n` + strings.Repeat("a", 10*limit) + `"`
	smallEscaped := `"\n` + strings.Repeat("a", limit/2) + `"`

	t.Run("L trips on oversized escaped string (slow-path clean run)", func(t *testing.T) {
		err := drainErr(NewWithBytes([]byte(bigEscaped), WithMaxValueBytes(limit)))
		require.ErrorIs(t, err, codes.ErrMaxValueBytes)
	})

	t.Run("escaped value within the limit is accepted", func(t *testing.T) {
		require.NoError(t, drainErr(NewWithBytes([]byte(smallEscaped), WithMaxValueBytes(limit))))
	})
}

func TestMaxValueBytesBoundsVerbatimBlanks(t *testing.T) {
	const limit = 100

	// a flood of insignificant whitespace ahead of a token: harmless for the semantic lexer (blanks are skipped), but the
	// verbatim lexer accumulates it.
	flood := []byte(strings.Repeat(" ", 10*limit) + "1")

	t.Run("VL trips on whitespace flood", func(t *testing.T) {
		err := drainVErr(NewVerbatimWithBytes(flood, WithMaxValueBytes(limit)))
		require.ErrorIs(t, err, codes.ErrMaxValueBytes)
	})

	t.Run("L is unaffected by whitespace", func(t *testing.T) {
		require.NoError(t, drainErr(NewWithBytes(flood, WithMaxValueBytes(limit))))
	})
}

// limitReaderTermination shows the idiomatic way to bound total bytes consumed from a stream: wrap the reader with
// io.LimitReader.
//
// The lexer simply sees EOF at the cap and never reads beyond it.
func TestLimitReaderBoundsConsumption(t *testing.T) {
	const limit = 1000

	// a large, otherwise well-formed array; far bigger than the cap
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i := 0; buf.Len() < 100*limit; i++ {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteByte('1')
	}
	buf.WriteByte(']')

	lex := New(io.LimitReader(&buf, limit))
	err := drainErr(lex)

	// the stream was truncated mid-array, so lexing fails ...
	require.Error(t, err)
	// ... and crucially, no more than the cap was ever consumed
	assert.LessOrEqual(t, lex.Offset(), uint64(limit))
}

func TestMaxContainerStackStreaming(t *testing.T) {
	// the depth breaker also works in streaming mode, across the internal buffer
	const maxDepth = 20
	deep := strings.Repeat("[", maxDepth+5) + strings.Repeat("]", maxDepth+5)

	lex := New(strings.NewReader(deep), WithBufferSize(8), WithMaxContainerStack(maxDepth))
	require.ErrorIs(t, drainErr(lex), codes.ErrMaxContainerStack)
}

// drainErr runs a semantic lexer to completion and returns its final error.
func drainErr(lex *L) error {
	for {
		tok := lex.NextToken()
		if !lex.Ok() {
			return lex.Err()
		}
		if tok.IsEOF() {
			return nil
		}
	}
}

// drainVErr runs a verbatim lexer to completion and returns its final error.
func drainVErr(vl *VL) error {
	for {
		tok := vl.NextToken()
		if !vl.Ok() {
			return vl.Err()
		}
		if tok.IsEOF() {
			return nil
		}
	}
}

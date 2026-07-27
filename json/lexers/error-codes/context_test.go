package codes

import (
	"errors"
	"testing"

	"github.com/go-openapi/testify/v2/require"
)

func TestPretty(t *testing.T) {
	e := ErrContext{
		Err:      errors.New("test"),
		Buffer:   "123456789012345678901234567890",
		Offset:   5000,
		Position: 5,
	}

	require.Equal(t,
		`1234567890
    ^
    |
    test
`,
		e.Pretty(10),
	)

	e.Position = 3
	require.Equal(t,
		`1234567890
  ^
  |
  test
`,
		e.Pretty(10),
	)

	e.Position = 25
	require.Equal(t,
		`0123456789
     ^
     |
     test
`,
		e.Pretty(10),
	)

	e.Position = 28
	require.Equal(t,
		`34567890
     ^
     |
     test
`,
		e.Pretty(10),
	)
}

package types

import (
	"fmt"
	"math/big"
)

// TODO: type conversion
// TODO: arithmetics  / comparison
// marshal/unmarshal

func CompareNumbers(a, b Number) int {
	return compareNumbers(a, b)
}

func Equal(a, b Number) bool {
	return compareNumbers(a, b) == 0
}

func Greater(a, b Number) bool {
	return compareNumbers(a, b) > 0
}

func GreaterOrEqual(a, b Number) bool {
	return compareNumbers(a, b) >= 0
}

func Less(a, b Number) bool {
	return compareNumbers(a, b) < 0
}

func LessOrEqual(a, b Number) bool {
	return compareNumbers(a, b) <= 0
}

func compareNumbers(a, b Number) int {
	var x, y big.Rat

	if err := x.UnmarshalText(a.Value); err != nil {
		panic(fmt.Errorf("invalid number encoding: %s", string(a.Value)))
	}
	if err := y.UnmarshalText(b.Value); err != nil {
		panic(fmt.Errorf("invalid number encoding: %s", string(b.Value)))
	}

	return x.Cmp(&y)
}

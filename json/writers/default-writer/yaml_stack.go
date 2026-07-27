package writer

import (
	"math/bits"
)

const (
	maxStack = 1 << 63
	// seed values for a fresh stack word when the current one overflows: a sentinel bit
	// (bit 1) plus the level bit at bit 0 (1 for array, 0 for object).
	stackedArray  = 0b11 // sentinel + array bit
	stackedObject = 0b10 // sentinel + object bit
	stackScale    = 63   // levels encoded per stack word
)

/*
func (w *YAML) isInObject() bool {
	stack := w.nestingLevel[len(w.nestingLevel)-1]

	return stack > 1 && (stack&1 == 0)
}
*/

func (w *YAML) isInArray() bool {
	stack := w.nestingLevel[len(w.nestingLevel)-1]

	return stack > 1 && (stack&1 == 1)
}

/*
func (w *YAML) isInContainer() bool {
	if len(w.nestingLevel) > 1 {
		return true
	}

	stack := w.nestingLevel[len(w.nestingLevel)-1]

	return stack > 1
}
*/

// IndentLevel yields the current depth of the container stack.
//
// Example: the following JSON fragment returns 4 tokens, with the
// IndentLevel evolving like so:
//
//	Input: [ { "a": 1 } ]
//	Level: 1 2   22 2 1 0
func (w *YAML) IndentLevel() int {
	size := len(w.nestingLevel) - 1
	level := size * stackScale
	var stack uint64
	if w.lastStack > 0 {
		stack = w.lastStack
	} else {
		stack = w.nestingLevel[size]
	}
	level += bits.Len64(stack) - 1

	return level
}

// pushObject moves up the stack with an even bit
func (w *YAML) pushObject() {
	stack := w.nestingLevel[len(w.nestingLevel)-1]

	if stack >= maxStack {
		// the current word is full: start a new word seeded with a fresh object level.
		// (shifting further would push the sentinel bit out of the 64-bit word).
		w.nestingLevel = append(w.nestingLevel, stackedObject)

		return
	}

	w.nestingLevel[len(w.nestingLevel)-1] = stack << 1
}

// pushArray moves up the stack with an odd bit
func (w *YAML) pushArray() {
	stack := w.nestingLevel[len(w.nestingLevel)-1]

	if stack >= maxStack {
		// the current word is full: start a new word seeded with a fresh array level.
		w.nestingLevel = append(w.nestingLevel, stackedArray)

		return
	}

	w.nestingLevel[len(w.nestingLevel)-1] = stack<<1 + 1
}

// popContainer moves down the stack
func (w *YAML) popContainer() {
	stack := w.nestingLevel[len(w.nestingLevel)-1]
	if stack <= 1 {
		if len(w.nestingLevel) <= 1 {
			panic(
				"dev error: nestingLevel should be initialized with a single element with value 1",
			)
		}

		w.nestingLevel = w.nestingLevel[:len(w.nestingLevel)-1]
		w.popContainer()

		return
	}

	stack >>= 1
	w.nestingLevel[len(w.nestingLevel)-1] = stack

	// if an extra word is spent down to its bare sentinel, drop it so the top word always
	// reflects the real innermost container (keeps isInArray/IndentLevel correct across
	// word boundaries). Word 0 legitimately holds the bare sentinel at the top level.
	if stack == 1 && len(w.nestingLevel) > 1 {
		w.nestingLevel = w.nestingLevel[:len(w.nestingLevel)-1]
	}
}

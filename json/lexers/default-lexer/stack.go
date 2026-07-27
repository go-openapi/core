package lexer

import (
	"math/bits"

	codes "github.com/go-openapi/core/json/lexers/error-codes"
)

// The nesting stack is a slice of uint64 words.
//
// Each word packs up to stackScale (63) container levels as bits, plus a sentinel marking the depth:
//
//   - bit 0 (the lowest bit) holds the type of the innermost container:
//     0 for an object, 1 for an array;
//   - pushing a container shifts the word left and writes the new type bit at
//     bit 0; popping shifts right;
//   - the highest set bit is a sentinel: bits.Len64(word)-1 is the number of
//     levels held in the word.
//
// The base word starts at 1 (sentinel only, i.e. top level).
// When a word fills up (sentinel reaches bit 63), an additional word is appended; when a word is emptied by a pop, it
// is dropped again.
//
// This keeps the invariant that the last word always describes the innermost container (or, for the base word alone,
// the top level), so isInObject/isInArray only ever need to look at the last word.
const (
	maxStack   = 1 << 63 // a word is full when its sentinel reaches bit 63
	stackScale = 63      // container levels packed per word
)

func (l *L) isInObject() bool {
	stack := l.nestingLevel[len(l.nestingLevel)-1]

	return stack > 1 && (stack&1 == 0)
}

func (l *L) isInArray() bool {
	stack := l.nestingLevel[len(l.nestingLevel)-1]

	return stack > 1 && (stack&1 == 1)
}

func (l *L) isInContainer() bool {
	if len(l.nestingLevel) > 1 {
		return true
	}

	stack := l.nestingLevel[len(l.nestingLevel)-1]

	return stack > 1
}

// IndentLevel yields the current depth of the container stack.
//
// Example: the following JSON fragment returns 4 tokens, with the IndentLevel evolving like so:
//
//	Input: [ { "a": 1 } ]
//	Level: 1 2   22 2 1 0
func (l *L) IndentLevel() int {
	// Both L and VL drive the unified pull/push cores one token at a time (no look-ahead), so the container stack always
	// reflects the current token and the depth is the indent level for both.
	return l.depth()
}

// pushObject enters an object: writes a 0 type bit at bit 0.
func (l *L) pushObject() {
	l.push(0)
}

// pushArray enters an array: writes a 1 type bit at bit 0.
func (l *L) pushArray() {
	l.push(1)
}

// push enters a new container, with typeBit being 0 for an object or 1 for an array.
func (l *L) push(typeBit uint64) {
	if l.maxContainerStack > 0 && l.depth() >= l.maxContainerStack {
		// circuit breaker: the maximum nesting depth has been reached
		l.in.Err = codes.ErrMaxContainerStack

		return
	}

	last := len(l.nestingLevel) - 1
	stack := l.nestingLevel[last]

	if stack >= maxStack {
		// current word is full: open a new word holding its sentinel plus this level
		const mask = 0b10
		l.nestingLevel = append(l.nestingLevel, mask|typeBit)

		return
	}

	l.nestingLevel[last] = stack<<1 | typeBit
}

// popContainer leaves the innermost container.
//
// Callers must ensure a container is open (see isInContainer).
func (l *L) popContainer() {
	last := len(l.nestingLevel) - 1
	stack := l.nestingLevel[last] >> 1

	if last > 0 && stack <= 1 {
		// the current word is exhausted: drop it so the enclosing word becomes the innermost container again.
		l.nestingLevel = l.nestingLevel[:last]

		return
	}

	l.nestingLevel[last] = stack
}

// depth returns the current nesting depth (0 at top level).
func (l *L) depth() int {
	last := len(l.nestingLevel) - 1

	return last*stackScale + bits.Len64(l.nestingLevel[last]) - 1
}

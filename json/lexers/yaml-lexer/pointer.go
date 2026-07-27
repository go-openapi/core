package lexer

import (
	"github.com/go-openapi/core/json/expressions"
	"github.com/go-openapi/core/json/lexers/token"
)

// JSONPointer returns the RFC 6901 JSON pointer of the most-recently-returned token as an
// [expressions.Pointer] — interned string keys and raw integer array indices.
//
// It is meaningful only when the lexer was constructed with [WithJSONPointer]; otherwise it is
// the empty pointer. The pointer addresses the current token's position:
//
//   - a scalar value → the pointer to that value (10 in {"a":[10]} → /a/0);
//   - an object key → the pointer to the member it names (key "a" → /a);
//   - a container delimiter { [ } ] → the pointer to the container itself.
//
// The returned pointer shares the lexer's backing storage and is valid only until the next
// token is read. Use [expressions.Pointer.Clone] to retain it.
func (l *YL) JSONPointer() expressions.Pointer {
	return l.ptr
}

// ptrFrame is one entry in the JSON-pointer tracker's container stack: exactly one frame per
// open container.
//
// For an array it carries the running element index (index, -1 before the first element).
//
// For an object the current member key lives directly as the top part of l.ptr (set by
// ptrSetKey), so the frame only needs to remember that it owns that part. hasSlot reports
// whether this frame currently owns the last part of l.ptr — an object frame owns it once a
// key has been seen, an array frame once its first element has started — so replacement (a new
// key / next index) and ascent (a closing delimiter) know whether to drop that part first.
type ptrFrame struct {
	index   int  // current array element index (-1 before the first element); unused for objects
	isArray bool // object vs array (drives AppendKey vs AppendElem)
	hasSlot bool // this frame currently owns the last part of l.ptr
}

// updatePointer advances the JSON-pointer tracker for the token just emitted. It is called
// from NextToken / Tokens ONLY when l.trackPtr is set. It is driven purely by token kinds,
// so it is independent of the source syntax (JSON or YAML).
func (l *YL) updatePointer(tok token.T) {
	switch {
	case tok.IsStartObject():
		l.ptrBumpArrayElem() // the container may itself be an element of an enclosing array
		l.ptrFrames = append(l.ptrFrames, ptrFrame{index: -1, isArray: false})
	case tok.IsStartArray():
		l.ptrBumpArrayElem()
		l.ptrFrames = append(l.ptrFrames, ptrFrame{index: -1, isArray: true})
	case tok.IsEndObject(), tok.IsEndArray():
		l.ptrPopFrame()
	case tok.IsKey():
		l.ptrSetKey(string(tok.Value()))
	case tok.IsScalar(), tok.IsNull():
		l.ptrBumpArrayElem() // a direct array element advances the index; a no-op inside an object
	}
}

// ptrBumpArrayElem advances the innermost array frame's element index and rewrites the top
// pointer part accordingly. It is a no-op at the top level or when the innermost container is
// an object (object member values keep the key set by ptrSetKey).
func (l *YL) ptrBumpArrayElem() {
	n := len(l.ptrFrames)
	if n == 0 {
		return
	}
	f := &l.ptrFrames[n-1]
	if !f.isArray {
		return
	}
	if f.hasSlot {
		l.ptr = l.ptr[:len(l.ptr)-1] // drop the previous element's index
	}
	f.index++
	l.ptr = l.ptr.AppendElem(f.index)
	f.hasSlot = true
}

// ptrSetKey sets the innermost object frame's member key as the top pointer part, replacing
// the previous key of the same object.
func (l *YL) ptrSetKey(key string) {
	f := &l.ptrFrames[len(l.ptrFrames)-1]
	if f.hasSlot {
		l.ptr = l.ptr[:len(l.ptr)-1] // drop the previous member key
	}
	l.ptr = l.ptr.AppendKey(key)
	f.hasSlot = true
}

// ptrPopFrame leaves the innermost container, dropping its pointer part if it owned one, so
// the pointer of the closing delimiter is the enclosing container's path.
func (l *YL) ptrPopFrame() {
	n := len(l.ptrFrames)
	if n == 0 {
		return
	}
	if l.ptrFrames[n-1].hasSlot {
		l.ptr = l.ptr[:len(l.ptr)-1]
	}
	l.ptrFrames = l.ptrFrames[:n-1]
}

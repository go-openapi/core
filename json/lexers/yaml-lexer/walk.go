// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package lexer

import (
	"bytes"
	"strings"
	"unicode/utf8"

	codes "github.com/go-openapi/core/json/lexers/error-codes"
	"github.com/go-openapi/core/json/lexers/token"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	yamltoken "github.com/goccy/go-yaml/token"
)

// build parses the buffered YAML source and walks the resolved AST into the materialised
// token stream l.toks. It runs exactly once per input; parse and semantic errors are stored
// in l.err (the tokens gathered before the error are kept).
func (l *YL) build() {
	l.built = true

	if l.err != nil { // a reader error was already recorded
		return
	}

	src := l.stripBOM()

	f, err := safeParse(src)
	if err != nil {
		l.err = err

		return
	}

	l.indexLines(src)
	defer func() { l.lineStarts, l.lineASCII = nil, nil }()

	switch len(f.Docs) {
	case 0:
		// empty input: a YAML document with no content resolves to null.
		l.putValue(token.NullToken, nil, 0)

		return
	case 1:
		// the single-document case
	default:
		l.err = ErrMultipleDocuments

		return
	}

	l.anchors = map[string]ast.Node{}
	l.expanding = map[string]bool{}
	l.merging = map[ast.Node]bool{}
	l.walkValue(f.Docs[0].Body, 0)
	l.anchors = nil
	l.expanding = nil
	l.merging = nil
}

// indexLines records where each line of src starts and whether it is pure ASCII, so a
// (line, column) pair can be turned back into a byte offset. Built once per build and dropped
// when it ends; the ASCII flag rides along for free, since this pass already reads every byte.
func (l *YL) indexLines(src []byte) {
	lines := bytes.Count(src, newline) + 1
	l.lineStarts = make([]int, 1, lines) // line 1 starts at 0
	l.lineASCII = make([]bool, 0, lines)

	ascii := true
	for i, b := range src {
		switch {
		case b == '\n':
			l.lineASCII = append(l.lineASCII, ascii)
			l.lineStarts = append(l.lineStarts, i+1)
			ascii = true
		case b >= utf8.RuneSelf:
			ascii = false
		}
	}
	l.lineASCII = append(l.lineASCII, ascii) // the last line, which need not be terminated
}

// byteOffset converts a goccy (line, column) pair into a byte offset into the parsed source.
//
// We do NOT use goccy's own Position.Offset, for two independent reasons:
//
//   - it is a 1-based RUNE index, not a byte offset (goccy's scanner holds the source as
//     []rune), so it cannot address the bytes of any document containing non-ASCII text, and
//     drifts further with every multi-byte character before the token;
//   - it loses one more per comment line preceding the token (goccy #856).
//
// Those two errors have opposite signs, so on an ASCII document with exactly one comment they
// cancel and pos.Offset looks right. It is not: patching either one alone leaves the other.
//
// Line and Column are correct under both defects, so deriving from them is the only thing that
// can be made correct without a fix upstream. See PROPOSALS-go-openapi.md §1b in the goccy
// checkout for the reproducers and what we intend to ask for.
func (l *YL) byteOffset(line, col int) uint64 {
	if line < 1 || line > len(l.lineStarts) || col < 1 {
		return 0 // no position we can honestly claim; put() reports the start of the input
	}

	src := l.data[l.bomBytes:]
	start := l.lineStarts[line-1]
	end := len(src)
	if line < len(l.lineStarts) {
		end = l.lineStarts[line]
	}

	// A column counts characters, not bytes, so in general we have to walk the line. On a pure
	// ASCII line -- nearly all of them, in nearly every document -- the two coincide and we can
	// index straight to it.
	i := start + col - 1
	if !l.lineASCII[line-1] {
		i = start
		for range col - 1 {
			if i >= end {
				break
			}
			i++
			for i < end && src[i]&0xC0 == 0x80 { // skip the rune's continuation bytes
				i++
			}
		}
	}

	return uint64(min(i, end)) //nolint:gosec // an index into a []byte is never negative
}

// newline is the line separator scanned for by indexLines.
var newline = []byte{'\n'} //nolint:gochecknoglobals // immutable 1-byte constant

// bomUTF8 is the UTF-8 encoding of U+FEFF, the byte order mark.
var bomUTF8 = []byte{0xEF, 0xBB, 0xBF} //nolint:gochecknoglobals // immutable 3-byte constant

// stripBOM returns the input with a leading UTF-8 byte order mark removed, recording its width
// so reported positions can be put back on the caller's coordinates.
//
// YAML 1.2 allows a document to be prefixed by a BOM and it is not part of the content, but
// goccy does not strip it: it becomes the first character of the first token, which does not
// merely dirty a value -- it changes the parse. A BOM followed by "{}" comes back as the
// SCALAR "<BOM>{}" rather than an empty mapping, and a BOM followed by "a: 1" yields the key
// "<BOM>a". So the mark has to go before the parser sees it, not be trimmed off afterwards.
//
// The JSON lexer L consumes a leading BOM the same way (see input.CheckBOM), which is what the
// FuzzYL JSON-subset differential compares against.
func (l *YL) stripBOM() []byte {
	if !bytes.HasPrefix(l.data, bomUTF8) {
		return l.data
	}

	l.bomBytes = len(bomUTF8)

	return l.data[len(bomUTF8):]
}

// safeParse runs the goccy parser, converting a recoverable panic into an error so a
// malformed input never crashes a YL consumer. (A stack overflow from pathological nesting
// is fatal and cannot be recovered — bound it with WithMaxContainerStack instead.)
func safeParse(data []byte) (f *ast.File, err error) {
	defer func() {
		if r := recover(); r != nil {
			f, err = nil, parsePanic(r)
		}
	}()

	f, err = parser.ParseBytes(data, 0)
	if err != nil {
		return nil, parseError(err)
	}

	return f, nil
}

// walkValue emits the tokens for a value node sitting at container level lvl (0 at the
// document root). Scalars are emitted at lvl; a container emits its delimiters, keys and
// children at lvl+1.
func (l *YL) walkValue(node ast.Node, lvl int) {
	if l.err != nil {
		return
	}
	if node == nil {
		l.putValue(token.NullToken, nil, lvl)

		return
	}

	switch n := node.(type) {
	case *ast.NullNode:
		l.putValue(token.NullToken, posOf(n.Token), lvl)
	case *ast.BoolNode:
		l.put(token.MakeBoolean(n.Value), posOf(n.Token), lvl)
	case *ast.IntegerNode:
		l.walkInteger(n, lvl)
	case *ast.FloatNode:
		l.walkFloat(n, lvl)
	case *ast.StringNode:
		l.walkString(n, lvl)
	case *ast.LiteralNode:
		// block scalar (| or >): always a string, never promoted to a number.
		l.putValue(token.MakeWithValue(token.String, []byte(n.Value.Value)), posOf(n.Start), lvl)
	case *ast.InfinityNode, *ast.NanNode:
		l.err = ErrUnsupportedScalar
	case *ast.MappingNode:
		l.walkMapping(n, lvl)
	case *ast.MappingValueNode:
		// a single-entry mapping that goccy did not wrap in a MappingNode; only block style
		// produces one, and its token is the pair's ":" (see patchBlockSpan)
		l.walkMappingEntries(
			[]*ast.MappingValueNode{n},
			posOf(n.GetToken()),
			posOf(n.GetToken()),
			false,
			lvl,
		)
	case *ast.SequenceNode:
		l.walkSequence(n, lvl)
	case *ast.TagNode:
		l.walkTag(n, lvl)
	case *ast.AnchorNode:
		// D4: register the anchor, then emit its value inline.
		if name := anchorName(n.Name); name != "" {
			l.anchors[name] = n.Value
		}
		l.walkValue(n.Value, lvl)
	case *ast.AliasNode:
		l.walkAlias(n, lvl)
	case *ast.MergeKeyNode:
		// D5 (deferred): merge-key resolution is a later increment.
		l.err = codes.ErrInvalidToken
	default:
		l.err = ErrUnsupportedNode
	}
}

// walkMapping emits an object: { key : value , … }.
func (l *YL) walkMapping(n *ast.MappingNode, lvl int) {
	l.walkMappingEntries(n.Values, posOf(n.Start), posOf(n.End), n.IsFlowStyle, lvl)
}

// walkMappingEntries emits the object delimiters around a list of key/value entries.
// Separators (':' and ',') are elided, matching the JSON lexer's default token stream.
func (l *YL) walkMappingEntries(
	values []*ast.MappingValueNode,
	start, end *yamltoken.Position,
	flow bool,
	lvl int,
) {
	inner := lvl + 1
	if l.overContainerStack(inner) {
		return
	}

	openIdx := len(l.toks)
	l.emitDelim(token.OpeningBracket, start, inner)
	if hasMergeKey(values) {
		// D5: resolve "<<" merge keys with RFC precedence (explicit keys win, earlier merges
		// win) into a flat, de-duplicated entry list.
		for _, e := range l.resolveMapping(values) {
			if l.err != nil {
				return
			}
			l.emitKey(e.key, inner)
			l.walkValue(e.val, inner)
		}
	} else {
		for _, mv := range values {
			if l.err != nil {
				return
			}
			l.emitKey(mv.Key, inner)
			l.walkValue(mv.Value, inner)
		}
	}
	// The closing delimiter reports the ENCLOSING level (the level it returns to), matching
	// the JSON lexer L, which pops the container before emitting the closer.
	l.emitDelim(token.ClosingBracket, end, lvl)

	if !flow {
		l.patchBlockSpan(openIdx)
	}
}

// entryKV is a resolved mapping member (after merge-key expansion).
type entryKV struct {
	key ast.MapKeyNode
	val ast.Node
}

// hasMergeKey reports whether any entry is a "<<" merge key.
func hasMergeKey(values []*ast.MappingValueNode) bool {
	for _, mv := range values {
		if _, ok := mv.Key.(*ast.MergeKeyNode); ok {
			return true
		}
	}

	return false
}

// resolveMapping flattens a mapping's entries, expanding "<<" merge keys. Precedence per the
// YAML merge-key spec: an explicitly-defined key always wins over a merged one (regardless of
// position), and among merges the first occurrence of a key wins. Merged members appear at the
// position of their "<<" entry; explicit members at their own position.
func (l *YL) resolveMapping(values []*ast.MappingValueNode) []entryKV {
	explicit := make(map[string]bool, len(values))
	for _, mv := range values {
		if _, isMerge := mv.Key.(*ast.MergeKeyNode); isMerge {
			continue
		}
		if ks, _, ok := l.scalarKeyResolved(mv.Key); ok {
			explicit[ks] = true
		}
	}

	seen := make(map[string]bool, len(values))
	out := make([]entryKV, 0, len(values))

	var add func(vals []*ast.MappingValueNode, fromMerge bool)
	add = func(vals []*ast.MappingValueNode, fromMerge bool) {
		for _, mv := range vals {
			if l.err != nil {
				return
			}
			if _, isMerge := mv.Key.(*ast.MergeKeyNode); isMerge {
				// The cycle guard has to span the EXPANSION of the merged entries, not just the
				// resolution of the alias to a node list: a mapping whose own merge key aliases
				// itself (`e: &b\n  <<: *b`) resolves to its own entries, which contain that same
				// merge key, so add() would recurse forever. mergeEntries' anchor guard is already
				// released by then — it only covers the lookup, which does not recurse.
				//
				// Keying on the merge source node rather than the anchor name also catches cycles
				// formed through a chain of aliases or a merge sequence, and lets the same anchor be
				// merged twice in sibling positions, which is redundant but legal.
				if l.merging[mv.Value] {
					l.err = ErrAliasCycle

					return
				}
				l.merging[mv.Value] = true
				add(l.mergeEntries(mv.Value), true)
				delete(l.merging, mv.Value)

				continue
			}
			ks, _, ok := l.scalarKeyResolved(mv.Key)
			if !ok {
				l.err = ErrComplexKey

				return
			}
			if fromMerge && explicit[ks] {
				continue // an explicit key shadows a merged one
			}
			if seen[ks] {
				continue // earlier occurrence wins
			}
			seen[ks] = true
			out = append(out, entryKV{key: mv.Key, val: mv.Value})
		}
	}
	add(values, false)

	return out
}

// mergeEntries resolves the value of a "<<" merge key into the list of mapping members it
// contributes: a mapping directly, an alias to a mapping, or a sequence of those (earlier
// entries take precedence). The alias-cycle guard is shared with walkAlias.
func (l *YL) mergeEntries(src ast.Node) []*ast.MappingValueNode {
	switch s := src.(type) {
	case *ast.MappingNode:
		return s.Values
	case *ast.MappingValueNode:
		return []*ast.MappingValueNode{s}
	case *ast.AliasNode:
		name := anchorName(s.Value)
		target, ok := l.anchors[name]
		if !ok {
			l.err = ErrUnknownAnchor

			return nil
		}
		if l.expanding[name] {
			l.err = ErrAliasCycle

			return nil
		}
		l.expanding[name] = true
		entries := l.mergeEntries(target)
		delete(l.expanding, name)

		return entries
	case *ast.SequenceNode:
		var all []*ast.MappingValueNode
		for _, e := range s.Values {
			all = append(all, l.mergeEntries(e)...)
		}

		return all
	default:
		l.err = codes.ErrInvalidToken

		return nil
	}
}

// keyString returns the string form of a scalar mapping key (matching emitKey's token value),
// used for merge de-duplication. ok is false for a non-scalar (complex) key.
// maxKeyUnwrap bounds the resolveKey recursion. A key can legitimately stack node properties
// ("? !!str &a foo"), but only a handful deep; the bound stops an alias chain that
// resolves back into itself from recursing forever.
const maxKeyUnwrap = 16

// scalarKey reduces a mapping key to the scalar it denotes, returning its text and the token to
// report a position at.
//
// A JSON object key is a string, so YL only accepts a key that resolves to a scalar. "Resolves"
// is doing real work, because YAML lets a key carry node properties and indirection that the
// JSON data model has no place for but that do not stop it being a string:
//
//	? explicit key : v     an explicit key (MappingKeyNode) wrapping any node
//	!!str key : v          a tagged key (TagNode)
//	&anchor key : v        an anchored key (AnchorNode)
//	*alias : v             an alias to a scalar defined elsewhere (AliasNode)
//
// and combinations of those ("? !!str foo"). Each wrapper is peeled until a scalar is reached;
// what comes out is an ordinary string key, which is exactly what the YAML Test Suite's own JSON
// equivalent shows for these documents.
//
// A key that resolves to a sequence or a mapping is genuinely outside the JSON data model and
// still fails with ErrComplexKey.
//
// Aliases are resolved against the anchor table, so this is a method: an alias key can only be
// resolved once the anchor it names has been walked, which the single forward pass guarantees
// (YAML requires an anchor to be defined before it is used).
func (l *YL) resolveKey(key ast.Node, depth int) (ast.Node, bool) {
	if depth > maxKeyUnwrap {
		return nil, false
	}

	switch k := key.(type) {
	case *ast.MappingKeyNode: // "? key"
		return l.resolveKey(k.Value, depth+1)

	case *ast.TagNode: // "!!str key"
		return l.resolveKey(k.Value, depth+1)

	case *ast.AnchorNode: // "&a key" -- also registers the anchor, so a later "*a" resolves
		if name := anchorName(k.Name); name != "" {
			l.anchors[name] = k.Value
		}

		return l.resolveKey(k.Value, depth+1)

	case *ast.AliasNode: // "*a"
		name := anchorName(k.Value)
		target, ok := l.anchors[name]
		if !ok {
			return nil, false
		}
		if l.expanding[name] {
			return nil, false // a key aliasing its own definition
		}
		l.expanding[name] = true
		node, resolved := l.resolveKey(target, depth+1)
		delete(l.expanding, name)

		return node, resolved

	default:
		return key, true
	}
}

// scalarKey is [YL.resolveKey] followed by the scalar test: it returns the key's text and the
// token whose position the Key token should report, or ok=false when the key is not a scalar.
//
// The position is the resolved scalar's own token, so the reported location always holds the
// text the token carries. For an alias that is the anchor DEFINITION site, consistent with how
// alias-expanded values are positioned (see walkAlias).
func (l *YL) scalarKeyResolved(key ast.MapKeyNode) (string, *yamltoken.Token, bool) {
	node, ok := l.resolveKey(key, 0)
	if !ok {
		return "", nil, false
	}

	return scalarKey(node)
}

// scalarKey reads a scalar node's text and token. It does no unwrapping: callers that may see a
// wrapped key go through [YL.scalarKeyResolved].
func scalarKey(node ast.Node) (string, *yamltoken.Token, bool) {
	switch k := node.(type) {
	case *ast.StringNode:
		return k.Value, k.Token, true
	case *ast.IntegerNode:
		return k.Token.Value, k.Token, true
	case *ast.FloatNode:
		return k.Token.Value, k.Token, true
	case *ast.BoolNode:
		return k.Token.Value, k.Token, true
	case *ast.NullNode:
		return k.Token.Value, k.Token, true
	case *ast.LiteralNode:
		// a block scalar used as an explicit key ("? |" ...): its text is the folded content
		return k.Value.Value, k.Start, true
	default:
		return "", nil, false
	}
}

// walkTag handles a tagged node (D6). Standard YAML type tags coerce the underlying scalar
// (goccy does NOT apply "!!str"/"!!null" at parse time); any other tag (including custom
// application tags) is stripped and the underlying value emitted as goccy typed it.
func (l *YL) walkTag(n *ast.TagNode, lvl int) {
	switch tagName(n.Start) {
	case "str", "binary":
		// force a string from the underlying scalar's source text
		l.putValue(
			token.MakeWithValue(token.String, []byte(scalarText(n.Value))),
			posOf(n.Start),
			lvl,
		)
	case "null":
		l.putValue(token.NullToken, posOf(n.Start), lvl)
	case "bool":
		b, ok := parseYAMLBool(scalarText(n.Value))
		if !ok {
			l.err = ErrUnsupportedScalar

			return
		}
		l.put(token.MakeBoolean(b), posOf(n.Start), lvl)
	default:
		// !!int, !!float (number-type coercion we don't apply) and custom application tags:
		// strip the tag and emit the underlying value as goccy typed it.
		l.walkValue(n.Value, lvl)
	}
}

// parseYAMLBool recognises the YAML boolean spellings (1.1 + core), for use when a "!!bool"
// tag forces a plain scalar to a boolean.
func parseYAMLBool(s string) (bool, bool) {
	switch strings.ToLower(s) {
	case "true", "yes", "on":
		return true, true
	case "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

// walkSequence emits an array: [ value , … ].
func (l *YL) walkSequence(n *ast.SequenceNode, lvl int) {
	inner := lvl + 1
	if l.overContainerStack(inner) {
		return
	}

	openIdx := len(l.toks)
	l.emitDelim(token.OpeningSquareBracket, posOf(n.Start), inner)
	for _, v := range n.Values {
		if l.err != nil {
			return
		}
		l.walkValue(v, inner)
	}
	// The closing delimiter reports the ENCLOSING level, matching L (see walkMappingEntries).
	l.emitDelim(token.ClosingSquareBracket, posOf(n.End), lvl)

	if !n.IsFlowStyle {
		l.patchBlockSpan(openIdx)
	}
}

// walkAlias resolves an alias to its anchored value and emits its tokens inline, guarding
// against cycles (an alias resolving into its own ancestor).
//
// The emitted tokens are re-walked from the original anchored node, so their Offset/Line/
// Column intentionally point at the anchor DEFINITION site, not this alias (see [YL.Offset]).
func (l *YL) walkAlias(n *ast.AliasNode, lvl int) {
	name := anchorName(n.Value)
	target, ok := l.anchors[name]
	if !ok {
		l.err = ErrUnknownAnchor

		return
	}
	if l.expanding[name] {
		l.err = ErrAliasCycle

		return
	}

	l.expanding[name] = true
	l.walkValue(target, lvl)
	delete(l.expanding, name)
}

// emitKey emits a mapping key as a Key token. Scalar keys are coerced to their string form;
// complex keys (sequences or mappings) have no JSON representation.
func (l *YL) emitKey(key ast.MapKeyNode, lvl int) {
	if l.err != nil {
		return
	}

	if _, isMerge := key.(*ast.MergeKeyNode); isMerge {
		// a "<<" reaching here was not resolved away by resolveMapping
		l.err = codes.ErrInvalidToken

		return
	}

	text, tok, ok := l.scalarKeyResolved(key)
	if !ok {
		l.err = ErrComplexKey

		return
	}

	l.putValue(token.MakeWithValue(token.Key, []byte(text)), posOf(tok), lvl)
}

// walkInteger emits an integer, unconverted when its spelling is already a JSON number and
// normalised via math/big otherwise (hex/octal/binary/underscores/sign).
func (l *YL) walkInteger(n *ast.IntegerNode, lvl int) {
	raw := n.Token.Value
	if isJSONNumber(raw) {
		l.putValue(token.MakeWithValue(token.Number, []byte(raw)), posOf(n.Token), lvl)

		return
	}

	var base int
	switch n.Token.Type {
	case yamltoken.HexIntegerType:
		base = 16
	case yamltoken.OctetIntegerType:
		base = 8
	case yamltoken.BinaryIntegerType:
		base = 2
	default:
		base = 10
	}

	b, ok := convertYAMLInt(raw, base)
	if !ok {
		l.err = ErrInvalidNumber

		return
	}
	l.putValue(token.MakeWithValue(token.Number, b), posOf(n.Token), lvl)
}

// walkFloat emits a float, unconverted when already a JSON number and normalised textually
// otherwise (underscores/sign/leading or trailing dot).
func (l *YL) walkFloat(n *ast.FloatNode, lvl int) {
	raw := n.Token.Value
	if isJSONNumber(raw) {
		l.putValue(token.MakeWithValue(token.Number, []byte(raw)), posOf(n.Token), lvl)

		return
	}

	b, ok := convertYAMLFloat(raw)
	if !ok {
		l.err = ErrInvalidNumber

		return
	}
	l.putValue(token.MakeWithValue(token.Number, b), posOf(n.Token), lvl)
}

// walkString emits a string value, promoting a PLAIN (unquoted) scalar whose text is a JSON
// number to a Number token (working around goccy resolving "1e3" and over-large numbers to
// String). Quoted and block scalars are never promoted.
func (l *YL) walkString(n *ast.StringNode, lvl int) {
	if n.Token.Type == yamltoken.StringType && isJSONNumber(n.Value) {
		l.putValue(token.MakeWithValue(token.Number, []byte(n.Value)), posOf(n.Token), lvl)

		return
	}

	l.putValue(token.MakeWithValue(token.String, []byte(n.Value)), posOf(n.Token), lvl)
}

// emitDelim appends a delimiter token.
func (l *YL) emitDelim(d token.KindDelimiter, pos *yamltoken.Position, lvl int) {
	l.put(token.MakeDelimiter(d), pos, lvl)
}

// putValue appends a scalar/key token, enforcing the WithMaxValueBytes circuit breaker.
func (l *YL) putValue(tok token.T, pos *yamltoken.Position, lvl int) {
	if l.maxValueBytes > 0 && len(tok.Value()) > l.maxValueBytes {
		l.err = codes.ErrMaxValueBytes

		return
	}
	l.put(tok, pos, lvl)
}

// put appends a token with its position metadata, enforcing the WithMaxTokens circuit breaker.
func (l *YL) put(tok token.T, pos *yamltoken.Position, lvl int) {
	if l.err != nil {
		return
	}
	if l.maxTokens > 0 && len(l.toks) >= l.maxTokens {
		l.err = ErrMaxTokens

		return
	}

	e := emit{tok: tok, lvl: lvl}
	switch {
	case pos != nil:
		// derived from line/column rather than taken from pos.Offset -- see byteOffset
		e.off = l.byteOffset(pos.Line, pos.Column)
		e.line = pos.Line
		e.col = pos.Column
		if l.bomBytes > 0 {
			// the parser saw the input without its byte order mark; put the reported position
			// back on the caller's coordinates. The mark is 3 bytes and one character, all of
			// it on line 1, so only that line's columns shift.
			e.off += uint64(l.bomBytes)
			if e.line == 1 {
				e.col++
			}
		}

	case len(l.toks) > 0:
		// No source position: an implicit value the document does not spell out (a "key:" with
		// nothing after it). It belongs where the construct that implies it left off, so it
		// inherits the preceding token's position rather than reporting none.
		prev := l.toks[len(l.toks)-1]
		e.off, e.line, e.col = prev.off, prev.line, prev.col

	default:
		// Nothing precedes it either: an empty, comment-only or header-only document, whose
		// single implicit null value is the whole token stream. The start of the input is the
		// only position it can honestly claim.
		e.off, e.line, e.col = 0, 1, 1
	}
	l.toks = append(l.toks, e)
}

// patchBlockSpan gives a BLOCK collection's delimiters real positions, openIdx being the index
// at which its opening delimiter was appended.
//
// Block style has no "{" / "[" / "}" / "]" characters, so goccy has no token to point the
// delimiters at. It sets the node's Start to the first entry's SEPARATOR — the ":" of the first
// pair, the "-" of the first item — and its End to nil. Taken literally that is worse than
// imprecise:
//
//   - the opening delimiter lands AFTER the key it precedes ("info:" reports the ":" of the
//     nested "title:" a line below), so the token stream contradicts its own order and a
//     consumer laying tokens out by position has to sort them back;
//   - the closing delimiter gets no position at all and surfaces as line 0, column 0, which is
//     not a position — lines are 1-based — so a consumer can only discard it.
//
// Instead the delimiters take the SPAN of what they enclose: the opening one reports the first
// token inside the container, the closing one the last. Both are real, in-range positions, and
// an alias-free document then reads monotonically.
//
// They are EQUAL to their neighbour's position rather than strictly before/after it, because in
// block style there is no character of their own to point at: a consumer must order by
// non-decreasing position, not strictly increasing. (An alias-BEARING document can still go
// backwards, by design — expanded tokens report the anchor definition site, see walkAlias.)
//
// The span is read back off the emitted stream rather than computed from the AST so that it
// composes with everything the walk may have done to the children — merge-key resolution, alias
// expansion, tags, nested containers — none of which the node's own tokens know about.
//
// It is a no-op for a flow collection (the caller checks), for an empty one (nothing to span,
// so goccy's own tokens stand), and when a circuit breaker cut the walk short and the indices
// no longer line up.
func (l *YL) patchBlockSpan(openIdx int) {
	closeIdx := len(l.toks) - 1
	if l.err != nil || openIdx < 0 || closeIdx <= openIdx || closeIdx >= len(l.toks) {
		return
	}
	if closeIdx == openIdx+1 {
		return // empty container: no children to take a span from
	}

	first, last := l.toks[openIdx+1], l.toks[closeIdx-1]
	l.toks[openIdx].off, l.toks[openIdx].line, l.toks[openIdx].col = first.off, first.line, first.col
	l.toks[closeIdx].off, l.toks[closeIdx].line, l.toks[closeIdx].col = last.off, last.line, last.col
}

// overContainerStack reports whether opening a container at depth would exceed the
// WithMaxContainerStack circuit breaker, setting the error if so.
func (l *YL) overContainerStack(depth int) bool {
	if l.maxContainerStack > 0 && depth > l.maxContainerStack {
		l.err = codes.ErrMaxContainerStack

		return true
	}

	return false
}

// posOf returns a token's position, nil-safe (goccy leaves some synthetic tokens without one).
func posOf(tok *yamltoken.Token) *yamltoken.Position {
	if tok == nil {
		return nil
	}

	return tok.Position
}

// tagName extracts the YAML type name from a tag token: "!!str" → "str",
// "tag:yaml.org,2002:int" → "int", "!custom" → "custom".
func tagName(tok *yamltoken.Token) string {
	if tok == nil {
		return ""
	}
	s := tok.Value
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		return s[i+1:]
	}

	return strings.TrimLeft(s, "!")
}

// scalarText returns the source text of a scalar node (used to coerce a tagged scalar to a
// string).
func scalarText(node ast.Node) string {
	if node == nil {
		return ""
	}
	if s, ok := node.(*ast.StringNode); ok {
		return s.Value
	}
	if tok := node.GetToken(); tok != nil {
		return tok.Value
	}

	return ""
}

// anchorName extracts the textual name from an anchor-name / alias-name node.
func anchorName(node ast.Node) string {
	if node == nil {
		return ""
	}
	if s, ok := node.(*ast.StringNode); ok {
		return s.Value
	}
	if tok := node.GetToken(); tok != nil {
		return tok.Value
	}

	return ""
}

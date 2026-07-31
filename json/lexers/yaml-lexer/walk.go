package lexer

import (
	"strings"

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

	f, err := safeParse(l.data)
	if err != nil {
		l.err = err

		return
	}

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
		// a single-entry mapping that goccy did not wrap in a MappingNode
		l.walkMappingEntries(
			[]*ast.MappingValueNode{n},
			posOf(n.GetToken()),
			posOf(n.GetToken()),
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
	l.walkMappingEntries(n.Values, posOf(n.Start), posOf(n.End), lvl)
}

// walkMappingEntries emits the object delimiters around a list of key/value entries.
// Separators (':' and ',') are elided, matching the JSON lexer's default token stream.
func (l *YL) walkMappingEntries(
	values []*ast.MappingValueNode,
	start, end *yamltoken.Position,
	lvl int,
) {
	inner := lvl + 1
	if l.overContainerStack(inner) {
		return
	}

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
		if ks, ok := keyString(mv.Key); ok {
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
			ks, ok := keyString(mv.Key)
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
func keyString(key ast.MapKeyNode) (string, bool) {
	switch k := key.(type) {
	case *ast.StringNode:
		return k.Value, true
	case *ast.IntegerNode:
		return k.Token.Value, true
	case *ast.FloatNode:
		return k.Token.Value, true
	case *ast.BoolNode:
		return k.Token.Value, true
	case *ast.NullNode:
		return k.Token.Value, true
	default:
		return "", false
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

	l.emitDelim(token.OpeningSquareBracket, posOf(n.Start), inner)
	for _, v := range n.Values {
		if l.err != nil {
			return
		}
		l.walkValue(v, inner)
	}
	// The closing delimiter reports the ENCLOSING level, matching L (see walkMappingEntries).
	l.emitDelim(token.ClosingSquareBracket, posOf(n.End), lvl)
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

	switch k := key.(type) {
	case *ast.StringNode:
		l.putValue(token.MakeWithValue(token.Key, []byte(k.Value)), posOf(k.Token), lvl)
	case *ast.IntegerNode:
		l.putValue(token.MakeWithValue(token.Key, []byte(k.Token.Value)), posOf(k.Token), lvl)
	case *ast.FloatNode:
		l.putValue(token.MakeWithValue(token.Key, []byte(k.Token.Value)), posOf(k.Token), lvl)
	case *ast.BoolNode:
		l.putValue(token.MakeWithValue(token.Key, []byte(k.Token.Value)), posOf(k.Token), lvl)
	case *ast.NullNode:
		l.putValue(token.MakeWithValue(token.Key, []byte(k.Token.Value)), posOf(k.Token), lvl)
	case *ast.MergeKeyNode:
		// D5 (deferred): the "<<" merge key is a later increment.
		l.err = codes.ErrInvalidToken
	default:
		l.err = ErrComplexKey
	}
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
	if pos != nil {
		e.off = uint64(pos.Offset) //nolint:gosec // no overflow, no negative values
		e.line = pos.Line
		e.col = pos.Column
	}
	l.toks = append(l.toks, e)
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

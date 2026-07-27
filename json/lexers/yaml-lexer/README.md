# YAML lexer

A lexer that reads **YAML** and emits the **same JSON-compatible token stream** as the
[default JSON lexer](../default-lexer), so every consumer built on `json/lexers` (the
`json.Document` builder, validators, code generators) works on YAML sources unchanged.

It is a thin, correctness-first layer on top of
[`goccy/go-yaml`](https://github.com/goccy/go-yaml): parse the whole document into an AST,
then walk it into `token.T` values in document order.

```go
l := lexer.NewWithBytes([]byte("a: 1\nb: [true, null]"))
for tok := range l.Tokens() {
    // {  key"a"  number"1"  key"b"  [  bool(true)  null  ]  }
}
if !l.Ok() { /* l.Err() */ }
```

## What it is (and is not)

1. **Same tokens as the JSON lexer.** The output is the exact stream the semantic JSON
   lexer `L` would produce for the equivalent JSON — object/array delimiters, keys and
   scalar values, with `:` and `,` **always elided**. `IndentLevel()` matches `L` too. This
   is verified by a differential test: for any JSON document, `YL(json) == L(json)`.

2. **Semantic only.** String and key values are decoded. There is **no verbatim variant**:
   faithfully round-tripping YAML (comments, tags, block-vs-flow style, anchors) from tokens
   alone is out of scope.

3. **Numbers stay unconverted.** A number whose YAML spelling is already a valid JSON number
   is emitted **verbatim** (no float/int round-trip, no loss of precision). YAML-only
   spellings are normalized in-house (see below), never through goccy's numeric conversion.

4. **Buffered, not streaming.** An `io.Reader` is accepted but fully read before parsing
   (goccy has no streaming parser). There is therefore no benefit in wrapping the reader.

5. **No performance contract.** Sitting on goccy, YL does not chase the throughput/zero-alloc
   guarantees of the JSON lexer. It optimizes for correctness and clarity.

## Options

| Option | Effect |
|---|---|
| `WithMaxContainerStack(n)` | Circuit breaker on nesting depth (also bounds the recursive walk). |
| `WithMaxValueBytes(n)` | Circuit breaker on the size of a single scalar. |
| `WithMaxTokens(n)` | Circuit breaker on the total token count — the **billion-laughs** defense for alias expansion (neither of the two above bounds exponential fan-out). |
| `WithJSONPointer(true)` | Track the RFC 6901 pointer of the current token, exposed as an [`expressions.Pointer`](../../expressions) via `JSONPointer()`. Off by default; allocates when on. |

The lexer implements `json/lexers.Lexer` and is poolable (`Reset` / `ResetWithBytes` /
`ResetWithReader`).

## YAML → JSON mapping decisions

YAML is a superset of JSON, so several constructs need a defined projection. The rationale
for each is in **[DESIGN.md](DESIGN.md)**; in short:

- **Numbers** — raw when already valid JSON; hex/octal/binary/underscore/`+`/leading- or
  trailing-dot normalized in-house via `math/big` (exact, overflow-safe); a **plain** scalar
  that is a JSON number is promoted to a number (works around goccy typing `1e3` and
  over-large integers as strings).
- **Anchors & aliases** — resolved: an alias is expanded to its anchored value inline,
  guarded against cycles and (via `WithMaxTokens`) against billion-laughs.
- **Merge keys (`<<`)** — resolved into the parent mapping (explicit keys win; among merges
  the earlier wins).
- **Tags** — `!!str` / `!!null` / `!!bool` coerce the value; other tags (incl. `!!int` /
  `!!float` and custom application tags) are stripped and the underlying value emitted.
- **Duplicate map keys** — rejected (goccy's default).
- **Multiple documents (`---`)** — rejected: a JSON token stream has a single root.
- **`.inf` / `-.inf` / `.nan`** — rejected: no JSON representation.
- **Non-string keys** — scalar keys coerced to strings; complex (`? …`) keys rejected.
- **Empty document** — a single `null` token.

## Caveats

- **Positions on expanded content.** `Offset` / `Line` / `Column` for tokens produced by
  expanding an alias or a merge key point at the anchor **definition** site (expansion
  re-walks the original node), not the usage site.
- **Block-style closers have no position.** goccy records no position for block `}` / `]`
  (only flow-style closers carry one), so they report `0/0/0`.
- **Invalid UTF-8 in strings.** The JSON lexer `L` rejects unescaped control characters, but
  does *not* validate UTF-8 well-formedness — it passes malformed high bytes (e.g. a lone
  `0xA2`) through raw. goccy instead replaces them with U+FFFD. This is the one place the two
  token streams legitimately differ (so the differential fuzz oracle is gated on valid UTF-8).
- **Parse time.** YL inherits goccy's parser characteristics; bound untrusted input with
  `io.LimitReader` and `WithMaxContainerStack`.

## Design

For the layering, the walker, the number-recognition rule, anchor/merge resolution and the
security model, see the companion **[DESIGN.md](DESIGN.md)**.

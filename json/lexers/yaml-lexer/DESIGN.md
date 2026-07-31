# Design notes — YAML lexer (`YL`)

This document explains how `YL` turns a YAML document into the JSON-compatible token
stream of `json/lexers/token.T`, the reasoning behind each projection decision, and how the
package is meant to be maintained. The user-facing summary is in
[README.md](README.md); this is the "why".

`YL` is deliberately a **thin, correctness-first** layer over
[`goccy/go-yaml`](https://github.com/goccy/go-yaml). Where the default JSON lexer is an
exercise in throughput (devirtualized scan cores, SWAR/AVX2, zero allocation), `YL`
inherits goccy's parser and spends its effort on getting the YAML→JSON projection right.

## 1. Guiding principles

- **One output contract: the JSON lexer's token stream.** The north star is that a
  consumer cannot tell whether a token came from `L` (JSON) or `YL` (YAML). That is what
  lets the whole `json/lexers`-based stack (documents, validators, codegen) accept YAML for
  free. It is enforced, not aspirational: a differential test asserts `YL(json) == L(json)`
  — including `IndentLevel` — for a corpus of JSON documents, and the fuzzer asserts it for
  arbitrary valid-UTF-8 inputs that both lexers accept.
- **Lean on goccy for YAML, own the JSON projection.** Tokenizing, escaping, anchor
  bookkeeping and duplicate-key detection are goccy's job. Deciding how a YAML construct
  maps to JSON tokens — and refusing the ones that have no JSON meaning — is ours.
- **Correctness over speed.** No zero-alloc or throughput contract. The code is a plain
  recursive tree walk; clarity wins over micro-optimization.
- **Security is explicit.** Because anchors expand and readers are fully buffered, the
  attack surface (billion-laughs, deep nesting, huge scalars) is covered by opt-in circuit
  breakers and a defensive parser recover.

## 2. Layering

```
[]byte  /  io.Reader ──io.ReadAll──►  l.data
                                        │
                                        ▼
                         parser.ParseBytes(data, 0)          (goccy; mode 0 = no comments)
                                        │  *ast.File
                                        ▼
                         build(): walk Docs[0].Body          (walk.go, one pass)
                                        │  []emit{tok, off, line, col, lvl}
                                        ▼
             NextToken() indexes the slice   ·   Tokens() ranges it   (lexer.go)
                                        │
                        per served token: updatePointer(tok)           (pointer.go, opt-in)
```

The whole document is parsed up front, then a **single recursive walk** materializes every
token — with its position and nesting level — into a slice. `NextToken`/`Tokens` then serve
from that slice.

Why materialize instead of walking lazily? goccy already holds the entire document in an
AST in memory, so laziness would save nothing meaningful while complicating everything: the
walk is naturally recursive, the pull API (`NextToken`) is flat, and `Offset` / `IndentLevel`
/ `Line` / `Column` / `JSONPointer` all need to be trivially consistent with the token just
returned. Materializing once decouples the recursive producer from the flat consumer and
makes that consistency automatic. With no performance contract, the extra transient slice is
a non-issue.

## 3. Relationship to the JSON lexer

`YL` targets the **semantic** lexer `L`, not the verbatim `VL`:

- **Values are decoded.** goccy hands us decoded scalar text; `String`/`Key` tokens carry
  it directly, exactly as `L` (which unescapes as it scans) would.
- **Separators are always elided.** `L` elides `:` and `,` by default; `YL` never emits
  them. Object = `{ key value key value }`, array = `[ value value ]`. (Unlike `L`, this is
  not an option — a verbatim/separator mode would only matter to a round-tripping consumer,
  which `YL` explicitly is not.)

  This is a decision, not a limitation, and it has been revisited: goccy exposes every
  separator we would need — `MappingValueNode.Start` is the `:` of each pair, and
  `SequenceEntryNode.Start` the `-` of each block item — so emitting them would be easy.
  It is declined because a separator mode is error-prone for no gain: consumers index by
  the *value* tokens (JSON pointer, position, colour by kind), and YAML's separators do not
  map onto JSON's anyway. A block `-` has no JSON counterpart, so it would need either a
  new delimiter kind in the shared `token` package — a JSON-side change for a YAML-only
  need — or a `Comma` token whose `String()` reports the wrong character. Neither is worth
  it. (For the record, goccy also does not expose the `,` between the pairs of a *flow*
  mapping at all, so even a separator mode could not be complete.)
- **`IndentLevel` matches `L`'s convention.** Openers and interior tokens sit at the
  container's level; a **closing** `}` / `]` reports the *enclosing* level (the level it
  returns to), because `L` pops the container before emitting the closer. `YL` mirrors that
  by emitting closers at the parent level.

There is deliberately **no verbatim `YL`**: preserving YAML byte-for-byte from tokens (with
its comments, tags, anchors, block/flow styles and significant indentation) is a different,
much harder problem than the JSON verbatim case, and outside this lexer's remit.

## 4. The walker

`walkValue(node, lvl)` is the core: it emits the tokens for one AST value at container level
`lvl` (0 at the document root), recursing for containers.

- Scalars emit a single token at `lvl`.
- A container emits its delimiters, keys and children at `lvl+1`, and its **closer at
  `lvl`** (see §3).
- Every emission goes through `put` (token-budget breaker) / `putValue` (value-size breaker),
  so the circuit breakers cover every path uniformly and off-path work is nil.

`build()` handles the document envelope: a parse error or panic (via `safeParse`, §9) stops
with an error; zero documents → a single `null` token; more than one document → an error; one
document → walk its body.

## 5. Numbers — YL owns recognition

The requirement is "keep numbers unconverted", but YAML admits spellings JSON does not, and
goccy's own number typing has gaps. So `YL` does **not** trust goccy's numeric typing as
authority; it recognizes numbers itself (`number.go`):

1. **Already a valid JSON number** (`isJSONNumber`) → emit the source bytes **verbatim**.
   This is the overwhelmingly common path and preserves precision exactly.
2. **YAML-only integer spelling** — hex `0x1F`, octal `0o17` / legacy `012`, binary `0b101`,
   underscores `1_000`, leading `+` — → normalize to canonical JSON via **`math/big`**
   (`convertYAMLInt`). big-int makes the conversion exact and overflow-proof: integers too
   large for `int64`/`uint64` round-trip losslessly.
3. **YAML-only float spelling** — leading dot `.5`, trailing dot `5.`, underscores, leading
   `+` — → normalize **textually** (`convertYAMLFloat`, `.5`→`0.5`, `5.`→`5.0`), preserving
   the digits exactly with no float64 round-trip.
4. **A plain (unquoted) string that is a JSON number** → **promote** to a number token.

Point 4 exists because goccy has two relevant bugs vs the YAML 1.2 core schema: it demotes a
bare-exponent float like `1e3` to a *string* (it wrongly requires a `.`), and it demotes
integers that overflow `int64` to a *string*. Both should be numbers; promoting any plain
scalar that matches the JSON number grammar restores exact `YL(json) == L(json)` fidelity.
Quoted (`"123"`) and block scalars are never promoted — quoting is the author's explicit "this
is a string".

`!!int` / `!!float` type **coercion** is intentionally not applied: if goccy already typed a
scalar, we take that typing; forcing a number type onto a differently-typed scalar is a
YAML-semantics rabbit hole we don't need.

## 6. Anchors, aliases and merge keys

goccy resolves neither aliases nor merge keys at parse time; it leaves `AliasNode` /
`MergeKeyNode` in the tree. `YL` resolves both during the walk.

- **Anchors** (`&x`) — recorded in a name→node table as they are encountered (they precede
  their aliases in document order), and their value is emitted inline like any other.
- **Aliases** (`*x`) — expanded by re-walking the anchored node. A per-name "currently
  expanding" set rejects a cycle (an alias resolving into its own ancestor) with
  `ErrAliasCycle`.
- **Merge keys** (`<<`) — `resolveMapping` flattens a mapping's members, expanding each `<<`
  source (a mapping, an alias to one, or a sequence of those). Precedence follows the merge
  spec: an **explicit key always wins** over a merged one (position-independent), and among
  merges the **earlier occurrence wins**. Non-merge mappings skip this machinery entirely
  (a fast path with no maps).

### Billion-laughs

Alias expansion is inlining, so a small document can fan out into an exponential number of
tokens — the classic YAML bomb. Neither `WithMaxContainerStack` (depth) nor
`WithMaxValueBytes` (single scalar) bounds *fan-out*; **`WithMaxTokens`** does, and is the
primary defense for untrusted input. The cycle guard covers the infinite case; the token
budget covers the merely-enormous one.

## 7. Tags

`walkTag` (D6) coerces the standard type tags goccy does not apply itself:

- `!!str` / `!!binary` → force a string from the scalar's source text;
- `!!null` → a null token;
- `!!bool` → a boolean (recognizing the YAML boolean spellings);
- everything else — `!!int`, `!!float`, and custom application tags — is **stripped** and
  the underlying value emitted as goccy typed it.

## 8. Other projections

- **Multiple documents (`---`)** — `ErrMultipleDocuments`. A JSON token stream has one root.
- **`.inf` / `-.inf` / `.nan`** — `ErrUnsupportedScalar`. No JSON representation.
- **Keys** — scalar keys are coerced to their string form (`123:` → key `"123"`); complex
  keys (a sequence or mapping via `? …`) are `ErrComplexKey`. Key text is taken from the same
  source as the emitted `Key` token, so merge de-duplication and the token agree.
- **Empty document** — a single `null` token (YAML empty = null).

## 9. Security and robustness

- **`safeParse`** wraps the goccy call in a `recover`, turning a *recoverable* parser panic
  into an error so a malformed input never crashes a consumer. A **stack overflow** from
  pathological nesting is fatal and cannot be recovered — the guard for that is
  `WithMaxContainerStack`, which bounds the recursive walk (it errors *before* recursing
  deeper), and, upstream, keeping untrusted input small (`io.LimitReader`).
- **Circuit breakers** — depth (`WithMaxContainerStack`), single-scalar size
  (`WithMaxValueBytes`), and total tokens (`WithMaxTokens`) are all off by default and
  should be set for untrusted YAML.
- **Buffer aliasing** — `NewWithBytes` does not copy; emitted values may alias the caller's
  buffer, which must stay stable. `Reset` drops the reference so a pooled lexer never pins
  caller memory.
- **Parse time** — YL inherits goccy's parser performance; some adversarial inputs parse
  slowly. Bound input size for untrusted sources.

## 10. Testing

- **`lexer_test.go`** — golden YAML→token cases (numbers in every base, anchors, merges,
  tags, keys), the **JSON-subset differential** (`YL(json) == L(json)`, token **and**
  `IndentLevel`), the error conditions, and the `WithMaxTokens` breaker.
- **`pointer_test.go`** — `JSONPointer()` at every token: nesting, arrays, RFC 6901
  `~0`/`~1` escaping, alias (structural pointer), off-by-default, and `Clone` retention.
- **`fuzz_test.go`** (`FuzzYL`) — per input: no panic / bounded budget, no buffer mutation,
  determinism, reset-equivalence, well-formed-on-accept (a pushdown grammar check), and the
  JSON-subset differential (gated on valid UTF-8 — the JSON lexer rejects control characters
  but does not validate UTF-8 well-formedness, so it passes malformed high bytes through raw
  while goccy substitutes U+FFFD; that is the one place the two streams legitimately diverge).

## 11. Map of the source

| File | Responsibility |
|---|---|
| `lexer.go` | `YL` type, constructors, `NextToken`/`Tokens`, position accessors, `Reset*`, `lexers.Lexer` conformance. |
| `walk.go` | The AST walker: `build`, `safeParse`, `walkValue` and per-node emitters, anchor/alias/merge resolution, tags, the circuit breakers. |
| `number.go` | `isJSONNumber` + `convertYAMLInt`/`convertYAMLFloat` (the §5 rule). |
| `options.go` | The four options. |
| `errors.go` | YAML-specific sentinel errors + goccy parse/panic wrappers. |
| `pointer.go` | The opt-in JSON-pointer tracker (copied from `default-lexer`; driven purely by token kinds). |
| `*_test.go`, `testdata/fuzz/` | The tests above and the fuzz corpus. |

## 12. Maintenance playbook

- **Adding a mapping decision** — implement it in the relevant `walkValue` arm (or
  `walkTag` / `resolveMapping`), give it a golden case in `lexer_test.go`, and — if it can
  affect a JSON-shaped input — confirm the differential still holds.
- **Adding an option** — a field on `options`, a `With…` constructor, and mirror it into the
  hot path in `reset()` if it needs per-input state; enforce it in `put`/`putValue`/the walk.
- **Bumping goccy** — re-run the fuzzer (`go test -run '^$' -fuzz FuzzYL -fuzztime 60s`): its
  job is precisely to catch a change in goccy's tokenizing, number typing or escaping that
  would break the JSON-subset contract. Re-verify the number-typing assumptions in §5 if the
  bump touches scalar resolution.

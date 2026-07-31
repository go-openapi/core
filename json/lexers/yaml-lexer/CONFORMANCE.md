# YAML conformance: where `YL` stands

Baseline measured against the vendored [YAML Test Suite](testdata/yaml-test-suite/SOURCE.md)
(revision `da267a5c`, 351 files → **406 cases**), by `TestConformanceYAML`.

```
accept + token stream matches the expected JSON   226 / 272   (83%)
invalid document rejected                          85 /  94   (90%)
recorded only (no single-root JSON equivalent)     63
known divergences (conformanceXFail)               32
  of which out of scope by design                  14
```

Resolved-scalar mapping keys landed (22 cases): a key carrying an explicit-key marker,
a tag, an anchor or an alias now resolves to the string it denotes. See §"Resolved keys"
below for what remains.

## What is being measured

`YL` reads YAML **as JSON**, so the suite's `json` field — the JSON a document is
equivalent to — is exactly the right expectation. Each case is lexed twice: the YAML
with `YL`, the expected JSON with the JSON lexer `L`. Conformance means the two token
streams are identical.

That framing matters for reading the numbers: a case `YL` fails is not necessarily a
YAML bug. It may be a construct JSON cannot express, in which case being unable to lex
it is correct behavior. The 32 xfail entries below are split accordingly — 14 of them are the design boundary
rather than work.

Three buckets, mirroring the JSON suite's `y_`/`n_`/`i_` split:

| bucket | expectation |
|---|---|
| `json` is one JSON document | must accept, and produce `L`'s token stream for it |
| `fail: true` | must reject |
| anything else | no single-root JSON equivalent — behavior only recorded |

The third bucket exists because a multi-document stream's `json` field holds several
values in sequence, and an empty document's is empty. Neither is a JSON token stream,
so there is nothing to compare — only the accept/reject verdict, which
`testdata/conformance_recorded.golden` pins so out-of-scope cannot quietly become
unnoticed regression.

## How that compares to other implementations

The [YAML Test Matrix](https://matrix.yaml.info/) publishes a **JSON output** comparison
over the same suite — "is the loaded data equal to the `json` field" — which is the same
question we ask. Its denominator is 401 (308 valid + 93 invalid).

Our comparable slice is 366 cases (272 with a single-root JSON expectation + 94 invalid),
scoring **311 → 85%**. Counting the 40 cases with no JSON equivalent as misses instead —
a stricter reading — gives **311/406 → 77%**. So we sit somewhere in **77–85%** depending
on how the out-of-scope cases are counted.

| implementation | JSON comparison |
|---|---|
| C libfyaml | 93% |
| JS yaml | 92% |
| Perl YAML::PP | 92% |
| Haskell HsYAML | 91% |
| **`YL` (this lexer)** | **77–85%** |
| Python ruamel.yaml | 78% |
| Perl YAML::PP+libyaml | 78% |
| JS js-yaml | 76% |
| Ruby psych | 76% |
| Go go-yaml | 76% |
| Python PyYAML | 75% |
| Perl YAML::XS | 75% |
| Lua lyaml | 72% |
| C# YamlDotNet | 64% |
| Perl Syck / YAML.pm / YAML::Tiny | 52% / 44% / 31% |

Read that carefully rather than as a ranking — three things make it not apples-to-apples:

- **Different subject.** The matrix measures *loaders*: parse to a data structure, serialize
  to JSON, compare. We measure a *lexer's token stream*. Ours is the narrower claim.
- **We forfeit points by design.** Multi-document streams and complex keys are deliberately
  unsupported, and they count against us here. A loader has no such excuse.
- **Our floor is goccy's.** We are built on `goccy/go-yaml`, which the matrix does not test
  (its `go-yaml` entry is a different project). 12 of our 54 divergences — the 9
  over-permissive accepts and the 3 parse failures — are goccy's behavior, not ours, and we
  cannot beat it without pre-validating ourselves.

The useful takeaway: **we are now above the mainstream loaders** (PyYAML 75%, psych 76%,
go-yaml 76%, ruamel 78%) and below the four leaders. Resolved-scalar keys were the single
biggest gap and are now closed, which is what moved 79% → 85%.

## The 32 remaining entries

### Resolved keys — done (22 cases)

A JSON object key is a string, so `YL` only accepts a key denoting a scalar. YAML lets a key
carry node properties and indirection that JSON has no place for but that do not stop it being
a string:

```yaml
? explicit key : v     # MappingKeyNode
!!str key : v          # TagNode
&anchor key : v        # AnchorNode
*alias : v             # AliasNode -- resolves through the anchor table
```

and combinations (`? !!str &a foo`). These were rejected as "complex". `walk.go:resolveKey`
now peels each wrapper until a scalar is reached, which is exactly what the suite's own JSON
equivalent shows for these documents. An anchor written on a *key* is registered too, so a
later `*a` resolves to it.

What stays rejected: a key that really denotes a sequence or mapping. Note that written out
directly (`? [a, b] : v`) goccy refuses the document before we see it — our own `ErrComplexKey`
is reached only through indirection, an alias naming a collection.

### 1. Multiple documents — 14 cases (out of scope by design)

A JSON token stream has one root, so a `%YAML`/`---` multi-document stream is rejected.
`YL`'s model is structurally single-document; this is an acknowledged boundary, not a
defect, and these 14 are listed in the xfail table only so the suite stays green and their
verdict cannot drift unnoticed. Counting divergences we might actually act on, the number
is **40, not 54**.

(Note these are cases where the *expectation* is still single-root — a directive plus
one document. Streams whose expectation is genuinely several JSON values fall into the
recorded bucket instead.)

### 2. Invalid documents we accept — 9 cases

The most valuable group: we are **more permissive than YAML allows**.

```
9C9N  Wrong indented flow sequence
9JBA  Invalid comment after end of flow sequence
CVW2  Invalid comment after comma
G5U8  Plain dashes in flow sequence
QB6E  Wrong indented multiline quoted scalar
SU5Z  Comment without whitespace after doublequoted scalar
U99R  Invalid comma in tag
Y79Y/3 Tabs in various contexts
YJV2  Dash in flow sequence
```

All are inherited from the underlying `goccy/go-yaml` parser rather than introduced by
our walk — flow collections, comment placement and tabs. Each needs confirming against
goccy upstream before deciding whether to report it there or pre-validate ourselves.

### 3. Accepted, but the token stream differs — 6 cases

```
565N   Construct Binary                   (!!binary tag)
L24T/1 Trailing line of spaces
LE5A   Spec Example 7.24. Flow Nodes
S4JQ   Spec Example 6.28. Non-Specific Tags
UGM3   Spec Example 2.27. Invoice
RR7F   Mixed Block Mapping                (see below -- not ours)
```

The first five are scalar-resolution divergences, and the subtlest group: nothing errors,
the document is accepted and the values are simply not the ones the suite expects.

`RR7F` is a **defect in the fixture**. Its YAML is `a: 4.2 / ? d / : 23`; its own event
stream (`+MAP =VAL :a =VAL :4.2 =VAL :d =VAL :23`) and its canonical `dump` both order the
keys `a, d`, but its `json` field writes `d` first. The suite compares loaded data as
unordered maps, so nothing there ever checked its JSON text against its own tree. We keep
key order — a JSON token stream is ordered, and so is our model — so we match the event
stream and diverge from the JSON text. Worth reporting upstream to yaml-test-suite.

### 4. Valid documents the parser rejects — 3 cases

`goccy` fails to parse these at all, so the fix is upstream (or a pre-pass of our own):

- `4MUZ/2` (`{foo\n: bar}`) and `VJP3/1` (a flow mapping spread over five lines) are the
  same defect twice — inside a **flow** mapping a line break between the key and its `:`
  is legal, but the parser applies the block-context rule that they share a line;
- `DK95/4` (`foo: 1\n\t\nbar: 2`) is a line holding only a tab between two entries.

## Divergences by suite tag

Which YAML *features* are involved (a case carries several tags, so these overlap):

```
spec 14   tag 11   directive 8   flow 7   error 6   header 6   sequence 6
indent 5   unknown-tag 5   whitespace 5   double 4   alias 3   comment 3   …
```

`mapping`, `explicit-key` and `anchor` have dropped out of the top of this list entirely —
that is the resolved-keys work showing up.

## Reading this as a work list

In rough order of value per unit of effort:

1. ~~**Resolved-scalar keys**~~ — **done**, 22 cases, 79% → 85%.
2. **Over-permissiveness** (group 2) — 9 cases, and the only group where we accept
   documents we should reject. All goccy's behaviour; confirm upstream before working
   around them (see `PROPOSALS-go-openapi.md` in the goccy checkout).
3. **Scalar resolution** (group 3) — 5 cases, each needing individual reading. Closing
   these and group 2 would reach roughly **89%**, within reach of the leaders.
4. ~~**Multi-document** (group 1)~~ — not on the list: out of scope by design (see above).
5. **Upstream parse failures** (group 4) — 3 cases, not ours to fix directly.

## Maintaining this

`conformanceXFail` in `conformance_xfail_test.go` lists every divergence with its
reason. The suite is green while they stay divergent, and an entry that starts passing
is reported as an error, so the list cannot rot into an excuse.

```sh
go test ./json/lexers/yaml-lexer -run TestConformanceYAML -v   # full report
go test ./json/lexers/yaml-lexer -run TestConformanceYAML -update-golden
```

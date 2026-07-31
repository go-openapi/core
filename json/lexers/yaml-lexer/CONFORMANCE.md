# YAML conformance: where `YL` stands

Baseline measured against the vendored [YAML Test Suite](testdata/yaml-test-suite/SOURCE.md)
(revision `da267a5c`, 351 files → **406 cases**), by `TestConformanceYAML`.

```
accept + token stream matches the expected JSON   204 / 272   (75%)
invalid document rejected                          85 /  94   (90%)
recorded only (no single-root JSON equivalent)     63
known divergences (conformanceXFail)               54
```

## What is being measured

`YL` reads YAML **as JSON**, so the suite's `json` field — the JSON a document is
equivalent to — is exactly the right expectation. Each case is lexed twice: the YAML
with `YL`, the expected JSON with the JSON lexer `L`. Conformance means the two token
streams are identical.

That framing matters for reading the numbers: a case `YL` fails is not necessarily a
YAML bug. It may be a construct JSON cannot express, in which case being unable to lex
it is correct behavior. The 54 divergences below are split accordingly.

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
scoring **289 → 79%**. Counting the 40 cases with no JSON equivalent as misses instead —
a stricter reading — gives **289/406 → 71%**. So we sit somewhere in **71–79%** depending
on how the out-of-scope cases are counted.

| implementation | JSON comparison |
|---|---|
| C libfyaml | 93% |
| JS yaml | 92% |
| Perl YAML::PP | 92% |
| Haskell HsYAML | 91% |
| Python ruamel.yaml | 78% |
| Perl YAML::PP+libyaml | 78% |
| **`YL` (this lexer)** | **71–79%** |
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

The useful takeaway: **we are in the same band as the mainstream loaders** (PyYAML, psych,
go-yaml, ruamel, js-yaml) and clearly behind the four leaders. And the single biggest gap is
ours to close: fixing resolved-scalar keys (group 1 below, 23 cases) would put the
comparable slice at roughly **85%**, above every implementation in the middle band.

## The 54 divergences

### 1. Non-scalar mapping keys — 23 cases

`YL` requires an object key to be a scalar, because JSON keys are strings. Most of
these, however, are keys that *resolve* to a string:

```yaml
top3: &node3
  *alias1 : scalar3      # the suite expects {"scalar1": "scalar3"}
```

An alias, an anchored scalar (`&a key : v`) or a tagged one (`!!str key : v`) is
rejected today, though the resolved key is an ordinary string and the suite's own JSON
shows it as such. **This is a feature gap, not a scope boundary**, and it is the
largest single group.

Genuinely out of scope within this group: explicit complex keys (`? [a, b] : c`),
which JSON cannot express at all.

### 2. Multiple documents — 14 cases

A JSON token stream has one root, so a `%YAML`/`---` multi-document stream is rejected.
Out of scope until the ND-JSON work on the roadmap lands, at which point these become
an NDJSON mode rather than an error.

(Note these are cases where the *expectation* is still single-root — a directive plus
one document. Streams whose expectation is genuinely several JSON values fall into the
recorded bucket instead.)

### 3. Invalid documents we accept — 9 cases

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

### 4. Accepted, but the token stream differs — 5 cases

```
565N  Construct Binary                   (!!binary tag)
L24T/1 Trailing line of spaces
LE5A  Spec Example 7.24. Flow Nodes
S4JQ  Spec Example 6.28. Non-Specific Tags
UGM3  Spec Example 2.27. Invoice
```

Scalar-resolution divergences. The subtlest group, because nothing errors — the
document is accepted and the values are simply not the ones the suite expects.

### 5. Valid documents the parser rejects — 3 cases

`goccy` fails to parse these at all, so the fix is upstream (or a pre-pass of our own):

- `4MUZ/2` (`{foo\n: bar}`) and `VJP3/1` (a flow mapping spread over five lines) are the
  same defect twice — inside a **flow** mapping a line break between the key and its `:`
  is legal, but the parser applies the block-context rule that they share a line;
- `DK95/4` (`foo: 1\n\t\nbar: 2`) is a line holding only a tab between two entries.

## Divergences by suite tag

Which YAML *features* are involved (a case carries several tags, so these overlap):

```
spec 22   mapping 21   tag 16   explicit-key 12   flow 10   sequence 10
directive 8   alias 7   comment 7   unknown-tag 7   whitespace 7   error 6
header 6   indent 6   anchor 5   1.3-err 4   double 4   …
```

## Reading this as a work list

In rough order of value per unit of effort:

1. **Resolved-scalar keys** (group 1) — 23 cases, one coherent feature: resolve an
   alias/anchor/tag on the key side and use the resulting scalar. Takes the comparable
   slice from 79% to **85%**, above every implementation in the matrix's middle band.
   Adding groups 3 and 4 on top would reach **89%**, within reach of the leaders.
2. **Over-permissiveness** (group 3) — 9 cases, and the only group where we accept
   documents we should reject. Worth confirming against goccy first.
3. **Scalar resolution** (group 4) — 5 cases, each needing individual reading.
4. **Multi-document** (group 2) — 14 cases, blocked on the ND-JSON decision.
5. **Upstream parse failures** (group 5) — 3 cases, not ours to fix directly.

## Maintaining this

`conformanceXFail` in `conformance_xfail_test.go` lists every divergence with its
reason. The suite is green while they stay divergent, and an entry that starts passing
is reported as an error, so the list cannot rot into an excuse.

```sh
go test ./json/lexers/yaml-lexer -run TestConformanceYAML -v   # full report
go test ./json/lexers/yaml-lexer -run TestConformanceYAML -update-golden
```

# `go-openapi/go-yaml` — forking the YAML parser

Status legend: ✅ done · 🚧 in progress · ⬜ todo · ⏸️ deferred

Target repo: `github.com/go-openapi/go-yaml` (does not exist yet) ·
upstream `github.com/goccy/go-yaml` @ `v1.19.2` (`edee2f9`) · MIT.

Consumer: `json/lexers/yaml-lexer` (`YL`) in this repo — today the only go-openapi
consumer of goccy.

## 0. Goals

Fred's, 2026-07-31, in priority order:

- **G1 — simplify `YL`,** our JSON-compatible ordered-document lexer. It is the *first*
  use case, and its requirements are **fast, strict, accurate**.
- **G2 — stream large documents with a low memory footprint.** The headline goal. No Go
  YAML library offers it.
- **G5 — serve the go-openapi ecosystem's YAML needs.** Not a side effect: the ecosystem
  wants YAML, and `go.yaml.in/yaml/v3` has proved *awkward to use, slow, and without easy
  low-level access*. Their always-in-progress v4 makes the UX worse and improves none of
  what we care about, so waiting is not a strategy.

And two that follow from those rather than motivating them:

- **G3** — fix the 12 conformance divergences that are upstream's behaviour, not ours.
- **G4** — delete the workarounds `YL` carries for upstream defects (85 loc).

**Why goccy specifically.** It was chosen *because it is a low-level YAML library*:
`scanner`, `lexer`, `parser`, `ast` and `token` are public, separable packages rather than
a `Marshal`/`Unmarshal` facade over a hidden parser. Every other Go YAML library exposes
only the decode surface. That is the substrate G2 needs, and the fork is about exploiting
it rather than replacing it.

**Why fork rather than send PRs.** Upstream is dormant, not hostile: **14 commits in 12
months**, last 2026-04-07. At that rate a PR queue cannot carry an architectural change.
There is no quality complaint about goccy — its low-level API is the reason we are here.

**Why a tracking fork and not a hard fork.** The `testify` → `testify/v2` hard fork was the
right call there and has paid off over nine months: a complete rewrite, better
maintainability, near-zero bugs, far more internal testing, a v1→v2 migration tool and real
documentation. But it was warranted by a bad starting position and an upstream in
architectural lock-down. **goccy's starting position is better**, so the same medicine is
not indicated. What carries over from testify is the *operating model*, not the fork depth —
see §9.

### What forking does *not* buy

Recorded so the case is not oversold:

- **Multi-document streams** stay rejected by `YL` (14 xfail entries) — a design boundary
  of projecting onto a single-root JSON token stream, not an upstream defect.
- **`RR7F`** stays failing: a defect in the *fixture*
  ([yaml-test-suite#179](https://github.com/yaml/yaml-test-suite/issues/179)).
- **Panic-freedom** is not a driver. 14 pathological inputs (deep nesting, alias cycles,
  NUL, truncated flow) were probed against the raw parser and **none panicked**; our
  `safeParse` recover is defensive. The one fatal we ever hit was a stack overflow in *our*
  `resolveMapping`.

## 1. Decisions taken

| id | decision | chosen |
|---|---|---|
| **F1** | scope | **Keep the whole tree.** `decode`/`encode`/`printer` stay, dormant — not developed, but not deleted, so upstream changes to them apply cleanly. |
| **F2** | tracking | **Tracking fork; rebase stays possible.** Our changes are a patch series over upstream, not a divergent rewrite of the repository. |
| **F3** | module path | **`github.com/go-openapi/go-yaml`** — keeps the upstream name, as `go-openapi/testify` kept stretchr's. |
| **F4** | architecture | **Iterator-first internals.** The streaming iterator is the primitive; the existing slice-returning API (`token.Tokens`, `Scan`, `Tokenize`) becomes a thin wrapper that collects it. Existing callers keep working unchanged. |

F1 reverses an earlier draft that deleted the 17.8k loc we do not use. Fred's reasoning:
**deletion is what makes rebase expensive.** A deletion commit re-conflicts with every
upstream commit touching those files, and the decoder is where upstream activity actually
lives. Keeping the files dormant costs nothing and keeps the merge cheap. Measured:

| upstream commits, last 12 months | 14 |
|---|---|
| …touching `parser`/`scanner`/`lexer`/`ast`/`token` | **5** |
| …touching `scanner` alone (our deepest changes) | **1** |
| …touching `decode`/`encode`/root (kept dormant, so conflict-free) | 9 |

So the majority of incoming change lands in files we keep and never edit, and the area we
rewrite hardest sees roughly one upstream commit a year.

F4 is the key design move, and it dissolves the apparent conflict between F2 and G2:
streaming does not become a parallel implementation to be kept in sync, it becomes the
*implementation*, with the old API expressed in terms of it. One scanner, one grammar.

## 2. Where the current pipeline stands

```
[]byte ──string()──► []rune ──Scanner.Scan()──► token.Tokens (ALL) ──►
    CreateGroupedTokens: 9 sequential whole-slice passes ──►
        parser.parse: per document group ──► *ast.File
```

Measured on a 0.32 MB OpenAPI-shaped document (live heap, result held):

| stage | retained | vs source |
|---|---|---|
| `[]rune(src)` | 1.2 MB | **4×** — the scanner indexes runes, not bytes |
| all tokens | 7.4 MB | 23× |
| `*ast.File` (holds the tokens) | 10.0 MB | **32×** |
| `YL`'s materialised token stream | 2.8 MB | 9× |

Peak through a full `YL` lex is on the order of **40× the source**. A 10 MB OpenAPI
document costs ~400 MB, and nothing can be emitted until all of it is parsed.

Three properties of the existing code decide the work:

1. **`Scanner.Scan()` is already incremental in output** — it returns a token batch per
   call and signals `io.EOF`. Good news: the scanning loop does not need re-inventing.
2. **`Scanner.Init(text string)` is not incremental in input** — `src := []rune(text)`
   materialises the whole document as 4-byte runes, and there is no reader entry point.
   This is also the *root cause* of the offset defect we worked around on 2026-07-31:
   `Position.Offset` is a rune index because the scanner indexes runes.
3. **`CreateGroupedTokens` is the real barrier** — nine sequential passes over the complete
   token slice (line comments, literal/folded, anchor/alias, scalar tags, anchor+tag, map
   keys, key/value, directives, documents) before parsing can begin.
   `parser.parse` itself already loops document-at-a-time.

## 2b. Performance and memory baseline

We are replacing something, so measure against it. Same 0.32 MB OpenAPI-shaped document,
source → node tree, on this machine:

| | time | throughput | retained |
|---|---|---|---|
| goccy → `ast.File` | 47.1 ms | 7.0 MB/s | **31×** source |
| `go.yaml.in/yaml/v3` → `yaml.Node` | 23.5 ms | 14.1 MB/s | **15×** source |
| `go.yaml.in/yaml/v3` → `map[string]any` | — | — | 10× source |

**Read honestly: on today's numbers goccy is 2× slower and 2× heavier than the library we
find unsatisfactory.** That is not a reason to abandon the substrate, but it is a fact the
plan has to answer rather than skip: "fast, strict, accurate" (G1) is currently not true of
the base we build on.

Where goccy's 47 ms goes:

| stage | time | share |
|---|---|---|
| `[]rune(src)` conversion | 0.5 ms | 1% |
| scan → tokens (`lexer.Tokenize`) | 14.0 ms | 30% |
| `CreateGroupedTokens` (9 passes) | 4.5 ms | 10% |
| AST construction | ~18.5 ms | **39%** |

Three conclusions that shape the work:

1. **The `[]rune` conversion is not a speed problem** (1%). Its costs are the 4× memory and
   the rune-indexed `Position.Offset`. S1 is justified by memory and correctness, not
   throughput — do not sell it as a performance fix.
2. **The dominant cost is AST construction, which a streaming `YL` never pays.** Scanning
   alone runs at 23.6 MB/s against yaml.v3's 14.1 MB/s for a complete tree, so a streaming
   projection starts with real headroom — the right comparison is our scanner-plus-projection
   against their whole pipeline.
3. **The AST path stays slower until separately optimised.** Anything in the ecosystem (G5)
   that wants a document tree rather than a token stream keeps paying the 2×. Closing that
   is its own work item, not a by-product of Phase S.

**Target to hold ourselves to:** streaming `YL` beats yaml.v3's 14.1 MB/s end-to-end while
holding memory at O(window) instead of 15× the source. Both halves must be measured; a
streaming implementation that is slower than the thing it replaces has not delivered G2.

## 3. Phase S — streaming (G2) ⬜

The target, expressed in the F4 shape:

```go
// new primitives
func (s *Scanner) InitReader(r io.Reader)                       // byte sliding window
func (s *Scanner) All() iter.Seq2[*token.Token, error]           // streaming tokens

// existing API, preserved, now wrappers
func (s *Scanner) Init(text string)  { s.InitReader(strings.NewReader(text)) }
func (s *Scanner) Scan() (token.Tokens, error)                   // one batch, as today
func Tokenize(src string) token.Tokens                           // collects All()
```

Three pieces of work, in dependency order:

1. ⬜ **S1 — byte-based, reader-fed scanner.** Replace `source []rune` with a byte buffer
   over an `io.Reader` and a sliding window. Kills the 4× blow-up, makes
   `Position.Offset` a true byte offset (so **P2 below disappears rather than being
   fixed**), and is the prerequisite for everything else. Confined to `scanner`; upstream
   touched that package once in the last year.
2. ⬜ **S2 — grouping as an iterator pipeline.** The nine passes are transformations over a
   token sequence, which is exactly what chains of `iter.Seq` express. Each becomes a stage
   with **bounded lookahead** instead of a whole-slice pass. Needs a per-pass audit of how
   far each must look ahead — recorded as an open question below, because a pass needing
   unbounded lookahead would have to stay buffering.
3. ⬜ **S3 — per-document parse.** `parser.parse` already loops per document group; expose
   that as `iter.Seq2[*ast.DocumentNode, error]` so a consumer can process and discard one
   document at a time.

**What streaming cannot do, and must be documented as a contract, not discovered later:**

- **Aliases pin their anchors.** `*x` re-emits the anchored node, so anchored subtrees must
  be retained until the document ends. Memory becomes O(anchored content), not O(1). A
  document that anchors its root streams no better than today. For OpenAPI this is
  cheap — anchors are rare and small — but the bound belongs in the API docs.
- **Merge keys (`<<`)** likewise retain the merged mapping.
- **Duplicate-key rejection** needs the keys of every open mapping — O(open mapping size),
  bounded by nesting depth, acceptable.

## 4. Phase Y — what `YL` becomes (G1) ⬜

Today `YL` reads a whole `*ast.File` and materialises every token into a slice
(`walk.go`, 856 loc). On top of Phase S it becomes a **projection over a streaming token
source**: pull YAML tokens, track container state, emit JSON tokens. What that removes:

| removed from `YL` | why |
|---|---|
| the materialised `[]emit` slice | tokens are produced on demand (9× source, gone) |
| `byteOffset` + `indexLines` (49 loc) | S1 gives true byte offsets natively |
| `stripBOM` + position correction (9 loc) | fixed in the fork (P3) |
| `patchBlockSpan` (13 loc) | **see the ordering constraint below** |

> ⚠ **`patchBlockSpan` is a hard dependency, not a cleanup.** It works today by
> *back-patching* a block container's opening delimiter after the contents have been seen.
> A streaming `YL` cannot retract a token it has already emitted. So **P1 (real spans for
> block collections) must land in the fork before the streaming `YL`**, or the streaming
> path cannot report block-container positions correctly. This ordering is the one place
> where the defect series and the streaming work are coupled.

## 5. Phase B — the defect patch series (G3, G4) ⬜

Each is one atomic, upstreamable commit with a test carrying its YAML Test Suite id. The
write-up exists as `PROPOSALS-go-openapi.md` in the goccy checkout and should move into the
fork to become the PR queue.

| # | fix | upstream ref | note |
|---|---|---|---|
| **P1** | Block collections have no usable span: `Start` is a separator *inside* the first entry, `End` is nil. | §1, rel. #733 | **blocks Phase Y** |
| **P2** | `Position.Offset` is a 1-based rune index, undocumented, and loses one more per preceding comment line. | §1b, #856 | **subsumed by S1** — becomes a doc fix upstream only |
| **P3** | A leading UTF-8 BOM is not stripped, changing the parse (`<BOM>{}` lexes as a scalar). | new | |
| **P4** | 9 documents accepted that YAML 1.2 forbids — comment placement, plain `-` in flow, tabs in flow. | §2 | 9 xfail entries |
| **P5** | 3 valid documents rejected: line break between key and `:` in a **flow** mapping (`4MUZ/2`, `VJP3/1`); a tab-only line between block entries (`DK95/4`). | §3 | 3 xfail entries |
| **P6** | Comments in flow maps fail to parse. | #903 | not yet a divergence for us |
| **P7** | Wrong line for a multiline token at end of input. | #813 | |

### Expected conformance outcome

Baseline (measured, `TestConformanceYAML`): **226/249 accepted-and-matching (91%)**,
85/94 rejected, 32 xfail entries.

| after | xfail | note |
|---|---|---|
| today | 32 | |
| P4 + P5 | **20** | removes all 12 that are upstream's behaviour; reject rate 94/94, match 229/249 (**92%**) |
| \+ the 5 scalar-resolution cases | 15 | need individual reading; not yet attributed |
| floor | **15** | 14 multi-document (design) + `RR7F` (fixture defect) |

The goal is an xfail list containing nothing that is merely someone else's backlog.

## 6. Phase A — stand up the fork ⬜

1. ⬜ Create the repo; push `upstream` = goccy `edee2f9` verbatim; tag `upstream/v1.19.2`.
2. ⬜ Keep `LICENSE` unmodified (MIT requires the notice survive). Add `NOTICE.md`: what
   this is, forked from where and at which commit, and why.
3. ⬜ `go.mod` → `module github.com/go-openapi/go-yaml`, `go 1.25.0` (needed for `iter`).
   Mechanical import rewrite. Nothing deleted (F1).
4. ⬜ CI mirroring this repo's: build/test/`-race`, `golangci-lint`, CodeQL, vuln scan.
   **Add fuzzing of `parser.ParseBytes` and the scanner.** Upstream fuzzes only
   `Unmarshal` (`FuzzUnmarshalToMap`), one level above the code Phase S rewrites.
5. ⬜ Keep the dormant packages' tests **running**: free regression cover proving the
   Phase S internals did not change public behaviour.
6. ⬜ Point `core` at it (4 import sites plus a test helper) via `replace`; drop at first tag.

**Exit criterion:** `core`'s full suite, the conformance harness and the `FuzzYL` corpus
pass against the fork with *zero* behavioural change. The fork is a no-op before it is an
improvement.

```
upstream   pristine mirror. Never edited. `git fetch upstream && git merge --ff-only`.
master     ours = upstream + patch series (Phase B, then S, then Y).
```

## 7. Phase C — percolate upstream ⏸️

One PR per Phase B commit, on their timetable. No coupling: the fork ships regardless.
Phase S is not upstreamable and no attempt should be made — it is an architectural change
a dormant project cannot absorb. If upstream later adopts one of our fixes, the merge
conflicts with our version; resolve by taking theirs and dropping ours. That shrinks the
patch series, which is the success condition.

## 8. Risks

- **Phase S is a real rewrite of the most subtle code in the library.** 1.8k loc of
  context-sensitive scanning. Mitigations: upstream's own scanner/lexer/parser test suites
  are retained and must stay green throughout; the conformance harness (406 cases) and
  `FuzzYL` are the outer net; fuzzing is added *before* S1 starts, not after.
- **We now own a YAML parser's security surface.** Mitigated by the fuzzing above, which is
  a net gain over depending on an unfuzzed upstream.
- **Patch-series discipline.** Cherry-pickability survives only if behavioural commits stay
  free of drive-by refactors.
- **G5 is a much bigger commitment than G1/G2**, and the plan should not let it arrive by
  accident. Serving the ecosystem means a decode/encode surface people can migrate *to*,
  which is why F1 keeps those packages dormant rather than deleted — they are the seed, not
  dead weight. But sequencing matters: G1 and G2 first, on their own merits. The ecosystem
  migration gets its own plan, including whether the 2× AST gap must close first.
- **The substrate is currently slower than what it replaces.** See §2b. Acceptable for the
  streaming path, unresolved for the tree path.

## 9. Operating model — what carries over from `testify/v2`

The fork *depth* does not carry over (§0), but the way that fork was run does. Nine months
of it produced: a complete rewrite, better maintainability, near-zero bugs, far more
internal testing, a v1→v2 migration tool, and documentation people actually use. The
transferable parts, as commitments rather than aspirations:

- ⬜ **Internal testing beyond upstream's.** `scanner/` ships **0 lines of test code**, and
  the only fuzz target is at the `Unmarshal` level — neither reaches what Phase S rewrites.
  We add parser- and scanner-level fuzzing *before* Phase S touches anything, and the
  406-case conformance suite moves into the fork's own CI rather than living only
  downstream in `core`.
- ⬜ **Documentation as a deliverable**, not a README afterthought — especially the contracts
  that are easy to get wrong: what streaming costs when anchors are present, what `Offset`
  means, what block-collection spans report.
- ⬜ **A migration tool** if and when G5 is pursued: moving the ecosystem off
  `go.yaml.in/yaml/v3` means rewriting `yaml.Node` tree walks, which is mechanical enough to
  automate and miserable enough by hand that nobody will do it otherwise.
- ⬜ **Stay in touch with upstream.** The testify pattern — implement what *their* users want
  but their architecture cannot deliver — applies directly: goccy is not in lock-down, it is
  short of time, so our fixes are worth offering and may well be taken.

### What G5 would actually require

Recorded now so the size is known before it is agreed. The ecosystem's usage of
`go.yaml.in/yaml/v3`, measured across the go-openapi repos:

```
yaml.Node 68 · yaml.ScalarNode 28 · yaml.Unmarshal 24 · yaml.Marshal 15
yaml.MappingNode 11 · yaml.TypedExtensions 9 · yaml.SequenceNode 5 · yaml.DocumentNode 4
```

The ecosystem is **already reaching for the low-level node API** — which is precisely what
is awkward about yaml.v3, and precisely what goccy does better with a typed AST. That is
the strongest argument that this fork can serve G5, and it also sets the migration surface:
a `yaml.Node`-tree → `ast.Node` translation, plus `Marshal`/`Unmarshal`, plus the 2× AST
performance gap from §2b.

## 10. Open questions

- ⬜ **Lookahead audit of the nine grouping passes** (blocks S2). Which are bounded? A pass
  needing unbounded lookahead must stay buffering, and would cap what streaming can
  deliver. This is the single biggest unknown in the plan and should be answered before
  committing to S2's design.
- ⬜ **Iterator signature**: `iter.Seq2[*token.Token, error]` per token, or per batch as
  `Scan()` does today? Per-token is the cleaner primitive; per-batch matches the existing
  shape and may avoid re-plumbing the grouping stages.
- ⬜ **Version policy**: `v0.x` while `core` tracks by `replace`, or `v1.0.0` at the first
  green conformance run?
- ⬜ Do we keep `ParseFile`/`os` access in the fork, or is a reader-only surface preferable
  for a library go-openapi embeds?

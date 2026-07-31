# `go-openapi/go-yaml` — forking the YAML parser

Status legend: ✅ done · 🚧 in progress · ⬜ todo · ⏸️ deferred

Target repo: `github.com/go-openapi/go-yaml` (does not exist yet) ·
upstream `github.com/goccy/go-yaml` @ `v1.19.2` (`edee2f9`) · MIT.

Consumer: `json/lexers/yaml-lexer` (`YL`) in this repo — today the **only** go-openapi
consumer of goccy.

## 0. Why fork

Fred's call, 2026-07-31: *"our YAML support is starting to cost a lot more in various
workarounds… we'll fix it to our liking and percolate improvements upstream as they see
fit and at their own pace (like we did last year for testify)."*

The supporting measurements:

| | |
|---|---|
| upstream commits, last 12 months | **14** (last one 2026-04-07) |
| upstream external dependencies | **0** |
| licence | MIT — fork-friendly, retain copyright |
| total upstream size | 30.6k loc |
| the part `YL` uses (`parser`/`ast`/`scanner`/`lexer`/`token`) | **12.8k loc, self-contained** |
| the part it does not (`decode`/`encode`/`printer`) | 17.8k loc |
| workaround code in `walk.go` written *because of* upstream | 85 loc (of 856) |
| conformance divergences that are upstream's, not ours | **12 of 32** xfail entries |

Upstream is not hostile, it is **dormant**. At 14 commits a year, a PR queue is not a
viable path for a dozen fixes we need now. That is the whole argument; there is no
quality complaint about goccy, which is a good parser we are building on precisely
because it is the best available in Go.

### What forking does *not* buy us

Stated up front so the plan is not oversold:

- **Multi-document streams** (14 xfail entries) stay rejected. That is `YL`'s design
  boundary — a JSON token stream has one root — not an upstream defect. Same class of
  decision as eliding separators: settled, not blocked.
- **`RR7F`** stays failing. It is a defect in the *test fixture*
  ([yaml-test-suite#179](https://github.com/yaml/yaml-test-suite/issues/179)), unfixable
  from our side and unlikely to be fixed upstream (that repo looks unmaintained too).
- **Panic-freedom** is not a driver. Our `safeParse` recover is defensive: 14 pathological
  inputs (deep nesting, alias cycles, NUL, truncated flow) were probed against the raw
  parser and **none panicked**. The one fatal we ever hit was a stack overflow in *our*
  `resolveMapping`, not goccy's.
- **Performance** is not a driver. No measurement suggests goccy's parser is our bottleneck.

## 1. Decisions taken

| id | decision | chosen |
|---|---|---|
| **F1** | scope | **parser subtree only** — `parser`, `ast`, `scanner`, `lexer`, `token`. Drop `decode`, `encode`, `printer`. |
| **F2** | relationship to upstream | **tracking fork, minimal diff** — keep our changes a small, rebase-friendly patch set so each fix cherry-picks cleanly upstream. |
| **F3** | module path | **`github.com/go-openapi/go-yaml`** — keeps the upstream name, exactly as `go-openapi/testify` kept stretchr's. |

F1 is safe: the subtree is genuinely self-contained. `parser`'s only import of the root
package is in `parser_test.go`; no non-test file in the subtree reaches outside it, and the
module has zero external dependencies.

### ⚠ F1 and F2 pull against each other — how that is resolved

Deleting 58% of the tree is, mechanically, an enormous diff. "Minimal diff" and "delete
`decode`/`encode`" cannot both be satisfied naively. The reconciliation:

**Track upstream by `merge`, never by `rebase`.**

- With **rebase**, our patch series is replayed onto each new upstream tip. The deletion
  commit would re-conflict with *every* upstream commit touching those files — and since
  most upstream activity is in the decoder, that is most commits. This is the combination
  that would hurt.
- With **merge**, the deletion is recorded **once** in history. A later upstream commit
  that modifies a file we deleted raises a single modify/delete conflict, resolved as
  "stay deleted" once, and never again for that commit.

So: the diff that matters for F2 is not "our tree vs upstream's tree" (large, and
irrelevant), it is **"our behavioural changes vs upstream's behaviour"** (small, and the
thing we actually want to keep cherry-pickable). Every fix in §3 is authored as one
self-contained commit touching only the parser subtree, so
`git format-patch` against upstream produces a clean PR for each.

Branch layout in the fork:

```
upstream   pristine mirror of goccy/go-yaml. Never edited. Advanced by
           `git fetch upstream && git merge --ff-only`.
master     ours. = upstream + [one deletion commit] + [the §3 patch series].
```

## 2. Phase A — stand up the fork ⬜

1. ⬜ Create `github.com/go-openapi/go-yaml`, push `upstream` branch = goccy `edee2f9`
   verbatim, tag it `upstream/v1.19.2` for provenance.
2. ⬜ Retain goccy's `LICENSE` unmodified (MIT requires the copyright notice survive).
   Add `NOTICE.md`: what this is, what it forked from, at which commit, and why.
3. ⬜ One commit, `chore!: reduce to the parser subtree`, deleting `decode`/`encode`/
   `printer` and the root package, plus their tests. Keep the upstream test suites for
   everything retained — they are the regression net for §3.
4. ⬜ `go.mod` → `module github.com/go-openapi/go-yaml`, `go 1.25.0`. Mechanical import
   rewrite across the retained tree.
5. ⬜ CI mirroring this repo's: build/test/`-race`, `golangci-lint`, CodeQL, vulnerability
   scan. **Add fuzzing of `parser.ParseBytes`** — upstream has none, and every defect in
   §3 is a parser-input defect.
6. ⬜ Point `core` at it: 4 import sites in `walk.go`/`lexer.go` plus one test helper.
   Develop against a `replace` directive; drop it at the first tag.

**Exit criterion:** `core`'s full test suite, conformance harness and `FuzzYL` corpus pass
against the fork with *zero* behavioural change. The fork is a no-op before it is an
improvement.

## 3. Phase B — the patch series ⬜

Ordered by value to us. Each is one atomic, upstreamable commit with a test derived from
the YAML Test Suite id. The write-up already exists as `PROPOSALS-go-openapi.md` in the
goccy checkout and should move into the fork as the PR queue.

| # | fix | upstream ref | what `core` deletes |
|---|---|---|---|
| **P1** | Block collections have no usable span: `Start` is a separator token *inside* the first entry, `End` is nil. Add `Span()`, or make `Start`/`End` mean what they say. | §1, rel. #733 | `patchBlockSpan` (13 loc) + its ordering caveat in the API docs |
| **P2** | `Position.Offset` is a 1-based **rune** index, undocumented; and loses one more per preceding comment line. | §1b, #856 | `byteOffset` + `indexLines` (49 loc) |
| **P3** | A leading UTF-8 BOM is not stripped, changing the parse (`<BOM>{}` lexes as a scalar). | new | `stripBOM` + position correction (9 loc) |
| **P4** | 9 documents accepted that YAML 1.2 forbids — comment placement, plain `-` in flow, tabs/indent in flow. | §2 | 9 xfail entries |
| **P5** | 3 valid documents rejected: line break between key and `:` inside a **flow** mapping (`4MUZ/2`, `VJP3/1`); a tab-only line between block entries (`DK95/4`). | §3 | 3 xfail entries |
| **P6** | Comments in flow maps fail to parse. | #903 | — (not yet a divergence for us) |
| **P7** | Wrong line for a multiline token at end of input. | #813 | — |

P1–P3 are *our* workarounds: ~71 loc plus tests, and the removal of two documented
caveats from `YL`'s public contract. P4–P5 are the conformance win.

Note P2 has an upstream-compatibility wrinkle worth deciding in the fork: changing
`Offset`'s meaning is breaking for goccy's own users, which is exactly why upstream may
prefer the documentation-only fix. In *our* fork we can simply make it a 0-based byte
offset. The upstreamable version of the commit should therefore add a `ByteOffset` field
rather than redefine `Offset`, so the PR is acceptable as-is.

### Expected conformance outcome

Baseline today (measured, `TestConformanceYAML`): **226/249 accepted-and-matching (91%)**,
85/94 rejected, 32 xfail entries.

| after | xfail entries | note |
|---|---|---|
| today | 32 | |
| P4 + P5 | **20** | removes all 12 that are upstream's behaviour |
| \+ the 5 scalar-resolution cases | **15** | those need individual reading; not yet attributed |
| floor | **15** | 14 multi-document (design) + `RR7F` (fixture defect) |

At 20 the reject rate becomes 94/94 and accepted-and-matching 229/249 (**92%**). Everything
still failing is then *by construction* either our design boundary or a broken fixture —
which is the real goal here: an xfail list with no entries that are merely someone else's
backlog.

## 4. Phase C — percolate upstream ⏸️

Open one PR per §3 commit against goccy, on their timetable, not ours. No coupling: the
fork ships whether or not they land. Keep `PROPOSALS-go-openapi.md` as the tracking index
with a status per item.

If upstream later adopts a fix, our merge from `upstream` will conflict with our own
version of it; resolve by taking theirs and dropping ours, shrinking the patch series. That
is the success condition, not a cost.

## 5. Risks

- **Divergence drift.** The patch series only stays cherry-pickable if it stays disciplined
  — no drive-by refactors mixed into behavioural commits. Enforced by review, and by the
  `format-patch` check being part of CI.
- **We inherit maintenance of a YAML parser.** 12.8k loc with no external deps, and we now
  own its security surface. Mitigated by the fuzzing added in Phase A step 5 — which is a
  net *gain* in assurance over depending on an unfuzzed upstream.
- **Scope creep toward a full YAML library.** F1 deliberately excludes decode/encode. If
  go-openapi later wants to consolidate `swag/yamlutils` (and the 7 repos on
  `go.yaml.in/yaml/v3`) onto this fork, that is a **separate decision with its own plan** —
  it would mean either re-adding goccy's reflection-based decoder or building one on our
  own JSON tooling. Not in scope here.

## 6. Open questions

- ⬜ Tag/version policy for the fork: start at `v0.1.0` while `core` tracks it via
  `replace`, or go straight to `v1.0.0` on the first green conformance run?
- ⬜ Should `lexer`/`scanner` be demoted to `internal/` so the fork's public surface is
  just `parser`/`ast`/`token` (all `YL` imports)? They must be *retained* either way —
  `parser` depends on both — so this is purely about what we commit to supporting.
  Leaning **no**: moving packages is a large, permanent diff against upstream for a
  cosmetic gain, and F2 says keep the diff cherry-pickable.

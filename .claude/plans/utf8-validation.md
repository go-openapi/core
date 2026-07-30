# UTF-8 validation in the JSON lexers (L & VL)

> Branch `fix/utf8-validation-prototype`. Supersedes the prototype commit `fb066a1`.
> Companion to [fredbi/core `.claude/plans/default-lexer-roadmap.md`] §0.4/§3.1 and
> `string-unescape-optimization.md` (which listed UTF-8 validation as *explicitly out
> of scope*: "we deliberately don't validate on the alias path" — this plan closes
> that hole).
> Status legend: ✅ done · ⏳ in progress · ⬜ todo · 🔬 measure first.

## OUTCOME — round 2 shipped (2026-07-30)

The simdutf/Keiser–Lemire `lookup4` validator is ported to avo AVX2 (`json/internal/utf8x/_asm` →
`validate_amd64.s`), 32 B/iter, and the BOM and VL-contract items are done. **The gate is now met.**

`UTF8Strict` vs no validation, benchstat 10×300 ms:

| workload | round 1 (L) | round 2 (L) | round 1 (VL) | round 2 (VL) |
|---|---|---|---|---|
| canada_geometry | ~ | ~ | ~ | ~ |
| citm_catalog | ~ | ~ | ~ | ~ |
| citm_catalog_min | ~ | ~ | +5.0% | ~ |
| golang_source | ~ | ~ | ~ | ~ |
| twitter_status | **−10.8%** | **−6.3%** | **−8.2%** | **−1.7%** |

Geomean −1.4% → **−0.36%**. Raw validator throughput (`BenchmarkValid`): 5–12× the stdlib once the kernel engages —
`cjk/4096` 248 ns vs 2420 ns (~16 GB/s), `cjk/128` 12.5 ns vs 80 ns.

`avx2Min` settled at **32** (one block, the kernel's hard minimum). The micro-benchmark shows a clear win from 32
bytes up (`latin/32`: 6.7 ns vs 18.1 ns); end-to-end the 32-vs-64 choice is not statistically distinguishable, so the
micro-benchmark decided it.

**How the kernel is trusted.** Hand-written vector code that accepts ill-formed input fails silently, so the
differential suite is the deliverable, not a formality:

- exhaustive over **all 65 536 byte pairs** (which is exactly what the three VPSHUFB tables encode) at 10 alignments;
- exhaustive over **every lead byte C0–FF × all 2nd × all 3rd bytes** (4.2 M) at 3 alignments — this is what exercises
  the prev2/prev3 continuation logic the pairwise sweep cannot reach;
- every 4-byte lead F0–FF × every 2nd byte × 5 tails × 8 alignments (TOO_LARGE / OVERLONG_4 live here);
- every length 0…256, every truncation shape pinned at the end, seam straddles across the last block boundary;
- 40 k random/structured-noise cases; 9.9 M fuzz executions, each re-checked at 32 shifted alignments (~325 M
  validations) — all against `utf8.Valid`;
- `TestKernelIsActuallyExercised` fails the suite from becoming vacuous if AVX2 is absent or the length gate keeps
  everything scalar.

**Mutation-tested:** three single-bit corruptions of the lookup tables (dropping SURROGATE from the `1110____` lead,
dropping it from the `____1101` low nibble, flipping a bit in `byte_2_high`) were each caught. The suite has teeth.

**Design note — the tail.** The kernel validates whole 32-byte blocks only and says nothing about a sequence crossing
the end; `Valid` rewinds to the last sequence boundary and finishes with `utf8.Valid`. That is simpler than an in-asm
zero-padded tail and mirrors what simdutf does to locate errors. It does mean a value just past a block boundary pays
a scalar tail of up to 31 bytes, which the micro-benchmark shows costing more than the vector part for such lengths.
Closing that would need the kernel to accept a carried-in `prev` block so the final partial block can be revalidated
from an overlapping offset — a signature change plus its own differential pass. Not done; the gate is met without it.

**BOM (§2.8) — now shipped**, the way it was originally scoped minus one thing: the mark is consumed at offset 0 and
is **not** re-emitted by `VL`. Carrying it in `LeadingSpace()` would need `blankStart` changes across 6 core sites (4
generated) plus ~20 streaming resets; consuming it costs one comparison per document and touches no scan loop. The
`FuzzLexer` round-trip invariant is correspondingly relaxed to "byte-exact except a leading BOM", which is the
direction RFC 8259 §8.1 asks for. `i_structure_UTF-8_BOM_empty_object` flips to accept; the two `n_structure_*BOM*`
cases stay rejected, and the `i_` golden diff showed exactly that one line changing.

## OUTCOME — round 1 shipped (2026-07-30)

All ten ill-formed-UTF-8 conformance cases now reject, in every mode; the L/VL surrogate-escape divergence is gone;
`y_ 95/95, n_ 188/188, xfail 0` still holds. 2052 new subtests, 2.5M fuzz executions clean, lint clean.

**Cost of the default policy vs no validation** (benchstat, 10×300 ms, MB/s):

| workload | L | VL |
|---|---|---|
| canada_geometry | ~ (p=0.68) | ~ (p=0.44) |
| citm_catalog | ~ (p=0.25) | ~ (p=0.74) |
| citm_catalog_min | ~ (p=0.53) | +5.0% (p=0.009) |
| golang_source | ~ (p=0.35) | ~ (p=0.58) |
| twitter_status | **−10.8%** (p=0.000) | **−8.2%** (p=0.000) |

Geomean −1.4%. The four ASCII-dominated workloads are statistically indistinguishable from no validation — the fused
detection works as designed, and the prototype's AVX2 hole (§4.2) is closed: `golang_source` went from −6.6% to ~0 and
`citm_catalog_min` from −10.0% to ~0.

**Gate result: ASCII target met (≤3%), `twitter_status` missed (≤8% wanted, −10.8% measured).** The arithmetic says
this is exactly the validator pass and nothing else: 109 KB of non-ASCII string bytes at `utf8.Valid`'s ~1.2 GB/s is
~90 µs against a 651 µs baseline ≈ 14%, observed 10.8%. So **round 2 (§5.4) is justified** — an AVX2 `lookup4`
validator at ~20 GB/s takes that to ~5 µs, i.e. under 1%. Awaiting the go-ahead.

**Deviations from the plan as written:**

1. **BOM trimming (§2.8) is NOT shipped** — only the UTF-16 diagnostic (`ErrNotUTF8`) is. Accepting a leading UTF-8
   BOM breaks VL's byte-exact round-trip, which `FuzzLexer` enforces (seed #62, `"﻿{}"`), and carrying the mark
   in `LeadingSpace()` means `blankStart` must start at 0 on the first token: 6 sites, 4 of them in generated
   `scan_gen.go`, plus ~20 per-token `l.blanks[:0]` resets in the streaming cores. That is its own commit with a full
   codegen verification, not a rider on this one.
2. **Replacement granularity (§2.4) was corrected during review** to one U+FFFD per *invalid byte* — `utf8.DecodeRune`
   always advances 1 on error, so the original "maximal subpart, matching DecodeRune's advance" was self-contradictory.
   The deciding argument is that `writer.escapedBytes` already does per-byte, so lexer, writer and caller-side
   `range value` now agree.
3. **`ensureWindow` was added to `refill.go`** (not in the plan): the surrogate lookahead must *peek* the following
   `\uXXXX` rather than consume it, so that `\uD800\uD800` yields two U+FFFD instead of swallowing the second escape.
   `consumeN` cannot serve that — a read that straddles a refill cannot be given back — so the cold path compacts the
   window instead. Safe there because the scanners that reach it hold no live buffer index.

## 0. TL;DR

**The bug.** Neither `L` nor `VL` validates UTF-8 in string bodies. The string
scanners look for exactly three stop bytes (`"`, `\`, `< 0x20`); every byte
`>= 0x80` is waved through and aliased straight into the token value. So
`["\xff"]`, `["\xc0\xaf"]` (overlong), `["\xed\xa0\x80"]` (encoded surrogate) and
`["\xf4\xbf\xbf\xbf"]` (> U+10FFFF) are all **accepted**, and the invalid bytes
land in the caller's `[]byte`. RFC 8259 §8.1 requires JSON text to be UTF-8, so
this is a genuine conformance bug — 10 `i_string_*` conformance cases, plus a
secondary escape-level variant (§2.3).

**The fix.** One user-visible knob, `WithUTF8Policy`, with three values:
`UTF8Strict` (default — error), `UTF8Replace` (mute the error, substitute U+FFFD),
`UTF8Passthrough` (today's unvalidated behavior, documented as unsafe).
The prototype's `fused`/`naive` are **implementation strategies**, not policies:
they disappear from the API and are chosen internally from CPU/build.

**The performance answer.** The concern that this "wipes out our vast string-scan
improvements" is real for a naive `utf8.Valid` at the funnel (**−10% to −32%**
measured, §5.1) but *not* for the fused design, provided one hole in the prototype
is plugged: the AVX2-scanned region is not accumulated, so it conservatively forces
validation on **every long string** — including the 10905 pure-ASCII long strings of
`golang_source`. Plugging it costs ~4 instructions per 32-byte iteration
(`VPMOVMSKB` of the raw data, already loaded, masked below the stop lane). Real-world
JSON is 96–100% ASCII strings by count (§5.2), so after the fix the overwhelming
majority of values are proven valid UTF-8 **inside the existing pass, with no second
read and no call**.

**Also in scope** (settled on review): a leading UTF-8 BOM is trimmed (§2.8, carried in
`VL.LeadingSpace()` so round-trip stays exact), and the shared primitives land in a new
`json/internal/utf8x` (§7.1) — the one package location reachable from **both** the
lexers and the writers, because the writers have the mirror-image bug and will reuse
them (§10 round 3).

**Sequencing.** Round 1 = correctness + fused detection + the AVX2 kernel flag +
`utf8.Valid` as the validator. Round 2 = port the simdutf/Keiser–Lemire `lookup4`
validator to avo AVX2 — **only if** round 1's numbers on `twitter_status` justify it.
Round 3 = the writers.

---

## 1. What is actually broken

### 1.1 The alias path never looks at high bytes

`swar.StringStopMask` (`internal/swar/swar.go:47`) and the AVX2 kernel
(`internal/strscan/_asm/asm.go`) both flag exactly `< 0x20`, `0x22`, `0x5c`. Both
are documented as "multibyte-safe", which is true in the sense that they never
*mis*-flag a continuation byte — but that is precisely why nothing ever inspects
those bytes. `consumeStringWhole` then does `value := data[start:i:i]` and hands the
raw window to the caller.

### 1.2 Everything downstream inherits it

`L` decodes escapes into `CurrentValue`; raw non-ASCII bytes are `append`ed
verbatim. `VL` aliases the source. Both therefore emit `token.T` / `token.VT` values
that a caller will `string(...)`-convert, at which point Go silently produces U+FFFD
runes on iteration — the corruption becomes invisible rather than absent.

### 1.3 Evidence (JSONTestSuite, current `master` behavior)

All ten of these are **accepted** by `L/bytes`, `L/reader` and `VL/bytes` today:

| file | bytes | why it is invalid |
|---|---|---|
| `i_string_invalid_utf-8` | `5b 22 ff 22 5d` | `0xff` is never a valid lead |
| `i_string_lone_utf8_continuation_byte` | `81` | continuation with no lead |
| `i_string_iso_latin_1` | `e9` | latin-1 `é`, truncated lead |
| `i_string_truncated-utf-8` | `e0 ff` | 3-byte lead, bad continuation |
| `i_string_UTF-8_invalid_sequence` | `e6 97 a5 d1 88 fa` | valid prefix then `0xfa` |
| `i_string_UTF8_surrogate_U+D800` | `ed a0 80` | encoded UTF-16 surrogate |
| `i_string_not_in_unicode_range` | `f4 bf bf bf` | U+13FFFF > U+10FFFF |
| `i_string_overlong_sequence_2_bytes` | `c0 af` | overlong `/` |
| `i_string_overlong_sequence_6_bytes` | `fc 83 bf bf bf bf` | obsolete 6-byte form |
| `i_string_overlong_sequence_6_bytes_null` | `fc 80 80 80 80 80` | overlong NUL |

They are `i_` (implementation-defined) in the suite, so the conformance test stays
green — that is why this went unnoticed. Under `UTF8Strict` all ten become
**rejected**; under `UTF8Replace` they are accepted with the offending bytes
substituted.

---

## 2. Semantics (settled)

### 2.1 The policy

```go
// UTF8Policy governs what the lexer does with input that cannot be represented
// as a Unicode scalar value.
type UTF8Policy int

const (
    UTF8Strict      UTF8Policy = iota // default: error out (codes.ErrInvalidUTF8)
    UTF8Replace                       // substitute U+FFFD, no error
    UTF8Passthrough                   // no validation at all — UNSAFE, trusted input only
)

func WithUTF8Policy(p UTF8Policy) Option
```

- **Default is `UTF8Strict` for both `L` and `VL`.** This is a behavior change:
  documents previously accepted are now rejected. It is the whole point of the fix
  and belongs in the changelog as a breaking-ish conformance change.
- `UTF8Passthrough` exists as an escape hatch for callers who have already validated
  their input (and for A/B benchmarking of the validation cost). Its godoc must say
  plainly that invalid bytes will reach the caller's `[]byte`.
- `WithUTF8ValidationMode` / `WithUTF8Validation` from the prototype are **deleted**.
  Nothing about `fused` / `naive` / SWAR / AVX2 is user-visible; `WithoutAVX2` stays
  the single vector-path escape hatch and now also disables the vector validator.

### 2.2 What counts as invalid (raw bytes)

Exactly Go's `utf8.Valid` semantics, which is also RFC 3629 + Unicode:
truncated sequences, over-long encodings, lead/continuation mismatches, encoded
surrogates `U+D800..U+DFFF`, and anything above `U+10FFFF`. No exceptions: we do
**not** accept WTF-8 or CESU-8.

### 2.3 What counts as invalid (`\u` escapes) — L/VL divergence to fix

The same policy governs `\u` escapes that do not form a Unicode scalar value. This
is a second, independent way an invalid character enters a value, and today the two
lexers disagree about it:

| input | `L` today | `VL` today | strict (new) | replace (new) |
|---|---|---|---|---|
| `["\uD888ሴ"]` | **accept** → U+FFFD | reject | reject | `"�ሴ"` |
| `["\uD800\uD800\n"]` | **accept** → U+FFFD + swallows the 2nd escape | reject | reject | U+FFFD, U+FFFD, `\n` |
| `["\uDd1e\uD834"]` | **accept** → U+FFFD | reject | reject | U+FFFD, U+FFFD |
| `["\uDADA"]` (no 2nd) | reject | reject | reject | U+FFFD |

Root cause in `L`: `input.unescapeUnicodeSequence` (`string.go:659`) calls
`utf16.DecodeRune`, which *returns* `utf8.RuneError` for a broken pair, and the
subsequent `utf8.ValidRune(0xFFFD)` is **true** — so the failure is laundered into a
silent replacement. `VL`'s `validateUnicodeWhole` (`string_raw.go`) checks
`DecodeRune(...) == utf8.RuneError` explicitly and errors.

> Historical note: `default-lexer-roadmap.md` §0.1 recorded this divergence in the
> *opposite* direction ("L rejects, VL accepts"). Conformance fix group E made `VL`
> validate its escapes, which inverted the asymmetry rather than removing it.

Fixes:
- **strict** — `unescapeUnicodeSequence` must treat `DecodeRune == RuneError` as
  `ErrSurrogateEscape`, matching `validateUnicodeWhole`.
- **replace** — `validateUnicodeWhole` must *stop* erroring on a broken pair;
  `unescapeUnicodeSequence` must emit U+FFFD and **not consume the second escape**
  (today it does, so `\uD800\uD800` yields one U+FFFD instead of two).
  `token.decodeUnicode` (`token/unescape.go:108`) already has the correct
  maximal-subpart behavior — copy its shape.

### 2.4 Replacement granularity

**One U+FFFD per invalid byte** — i.e. `utf8.DecodeRune`'s error advance, which is
always width 1. `\xe0\xa0\xff` → three replacements.

The alternatives were considered and rejected:

| rule | `\xe0\xa0\xff` | who does it |
|---|---|---|
| **per invalid byte** ✅ | 3 × U+FFFD | Go: `range` over a string, `utf8.DecodeRune`, **and our own `writer.escapedBytes`** |
| maximal subpart (Unicode TR §3.9) | 2 × U+FFFD | browsers / WHATWG |
| per run | 1 × U+FFFD | `bytes.ToValidUTF8` |

Decider: `writers/default-writer/escape.go:escapedBytes` **already** substitutes
U+FFFD per invalid byte (`utf8.DecodeRune` → `utf8.AppendRune`, the comment says
"invalid runes are represented as �"). Picking the same rule means the lexer and
the writer agree, and `string(value)` / `range value` in caller code agrees too. It is
also the cheapest.

Independent of the byte rule: each **broken `\u` escape** is one U+FFFD, so
`\uD800\uD800` → two. That is already `token.decodeUnicode`'s behavior.

### 2.5 The verbatim pledge (TODO §4 — resolved)

`VL` **does** substitute U+FFFD in the value under `UTF8Replace`. Restated pledge:

> `VL` reproduces its input byte-for-byte **for input that is valid UTF-8**. Under
> `UTF8Replace` an ill-formed sequence is replaced in the emitted value; the caller
> explicitly asked for that trade. Offsets, `Line()`, `Column()` and `LeadingSpace()`
> are **unaffected** — they are derived from the scan cursor over the source, never
> from the value — so positions stay exact even for a mangled token.

Justification: a document containing invalid UTF-8 is not valid JSON text, so a
byte-faithful round-trip of it re-emits invalid JSON. A caller who enables
replacement wants clean bytes out. Callers who need absolute byte fidelity keep the
default `UTF8Strict` (which never copies and never mangles — it only errors) or opt
into `UTF8Passthrough`.

**Escapes are the exception**, and this falls out naturally: `VL` keeps string bodies
raw, so a broken `\uD800\uD800` stays *as source text* in the value; the U+FFFD
substitution happens at decode time in `token.Unescape`, which already does exactly
that. So under `UTF8Replace`, `VL` mangles *bytes* but never rewrites *escape text* —
the value stays a faithful raw string, and `Unescape` yields the sanitized form.

### 2.6 Zero-copy invariants preserved

| policy | value | copy? |
|---|---|---|
| `UTF8Strict`, valid input | aliased as today | no |
| `UTF8Strict`, invalid input | n/a — error | no |
| `UTF8Replace`, valid input | aliased as today | no |
| `UTF8Replace`, invalid input | rewritten into `CurrentValue` | only the offending value |
| `UTF8Passthrough` | aliased as today | no |

Validation is read-only. **No policy adds a copy to a valid document.**

### 2.7 `UTF8Passthrough` and escapes (settled)

> **FRED** agree (should be documented): explicitly escaped UTF-8 MUST produce some
> rune, so U+FFFD when broken and policy is lenient.

`UTF8Passthrough` skips **raw-byte** validation only. A `\uXXXX` escape must always
decode to *some* rune — there is no "pass the bytes through" option for an escape,
because the escape text is not the value. So:

| | raw bytes | broken `\u` pair |
|---|---|---|
| `UTF8Strict` | error | error |
| `UTF8Replace` | U+FFFD per invalid byte | U+FFFD per broken escape |
| `UTF8Passthrough` | untouched, uninspected | U+FFFD per broken escape |

Passthrough and Replace therefore share the whole escape path; only Strict differs.
This must be stated explicitly in the `UTF8Passthrough` godoc, not left implied.

### 2.8 Leading byte-order mark (settled — now in scope)

> **FRED** we'll add support for leading BOM trimming

A UTF-8 BOM (`EF BB BF`) at **offset 0 only** is consumed as insignificant lead-in
before the top-level value. RFC 8259 §8.1 forbids *emitting* one but explicitly
permits ignoring one on input. `i_structure_UTF-8_BOM_empty_object` (`EF BB BF 7B 7D`)
flips from reject to **accept**.

Rules, with the traps:

- **Exactly one, exactly at offset 0.** A second BOM, or a BOM after any token, is
  not special: it is U+FEFF, valid UTF-8, and therefore an invalid *token* between
  values (rejected) or an ordinary character inside a string (kept). No change there.
- **`n_structure_UTF8_BOM_no_data`** (`EF BB BF`, nothing else) must **stay rejected**:
  trimming leaves an empty document → `ErrNoData`. Falls out for free.
- **`n_structure_incomplete_UTF8_BOM`** (`EF BB 7B 7D` — a *truncated* BOM) must
  **stay rejected**: only the full 3-byte sequence is trimmed, and `EF BB` is then an
  invalid token. Falls out for free, and is now also caught as invalid UTF-8.
- **`VL` must not lose it.** Trimming the BOM would silently break round-trip. It is
  accumulated into `l.blanks`, exactly like leading whitespace, so `LeadingSpace()`
  on the first token replays it and a re-serialized document is byte-identical.
  This is the one place the implementation differs between L and VL.
- **Streaming.** The check runs on the first fill, before the first token scan, and
  must handle a window that starts with a *partial* BOM (buffer sizes are ≥ 32 bytes
  and aligned, so a 3-byte prefix cannot be split — assert it rather than assume it).
- **Bonus (cheap, do it):** `FF FE` / `FE FF` at offset 0 is a UTF-16 BOM. Today it
  fails as a generic invalid token. Report a dedicated `ErrNotUTF8` ("input appears to
  be UTF-16, only UTF-8 is supported") — it turns a baffling parse error into an
  actionable one, and costs one comparison on a cold path.
  (`i_string_UTF-16LE_with_BOM`, `i_string_utf16BE_no_BOM`, `i_string_utf16LE_no_BOM`
  stay rejected either way.)

Not an option knob: trimming only ever *accepts* input that is rejected today, so
there is nothing to opt out of.

---

## 3. Where validation hooks in

`finishStringValue` (`string.go:461`) is a true funnel: all eight string paths reach
it. That is where the decision is taken; the *detection* is what varies per path.

| # | path | file:func | round-1 detection |
|---|---|---|---|
| 1 | L whole, clean | `string.go:consumeStringWhole` | fused SWAR + AVX2 flag |
| 2 | L whole, escaped | `string.go:consumeStringEscaped` | 🔬 assume non-ASCII (validate) |
| 3 | L stream, fast | `string.go:consumeStringStreamFast` | fused SWAR + AVX2 flag |
| 4 | L stream, byte-wise | `string.go:consumeStringStreaming` | 🔬 assume non-ASCII |
| 5 | VL whole, clean | `string_raw.go:consumeStringRawWhole` | fused SWAR + AVX2 flag |
| 6 | VL whole, escaped | `string_raw.go:consumeStringRawEscaped` | 🔬 assume non-ASCII |
| 7 | VL stream, fast | `string_raw.go:consumeStringRawStreamFast` | fused SWAR + AVX2 flag |
| 8 | VL stream, byte-wise | `string_raw.go:consumeStringRawStreaming` | 🔬 assume non-ASCII |

"assume non-ASCII" = correct but pessimistic: those values always pay a validation
pass. Escaped/streaming strings are the minority; **measure before optimizing**
(`golang_source` is escape-heavy and is the workload to watch — if it shows, the same
`hi |= w` accumulation goes into the clean-run scans of paths 2/4/6/8).

The prototype forked `consumeStringWhole`/`consumeStringRawWhole` into `…V` twins
selected at dispatch. **Do not keep the fork.** The accumulator is one `OR` on a word
that is already in a register; a duplicated scanner doubles the surface where the
inliner/register-allocator can regress (the roadmap's §4.2 lesson: a fast-path change
regressed the slow path by 12% when they shared a frame). Land the accumulator in the
single existing function and verify with the existing devirt/inline tests.

Non-string tokens need nothing: invalid bytes in numbers/literals/structure already
fail the grammar (`n_number_invalid-utf-8-in-int` etc. all pass today).

---

## 4. Detection: fusing the non-ASCII test into the existing scan

### 4.1 SWAR region — free

Already right in the prototype:

```go
w := binary.LittleEndian.Uint64(data[i:])
hi |= w                                   // <- the whole cost
if m := swar.StringStopMask(w); m != 0 { ... }
```

`hi & swar.HighBits != 0` ⇒ some byte in the scanned words was `>= 0x80`.
Over-reporting (bytes past the stop lane in the matching word) is safe: it can only
trigger a redundant validation, never skip a needed one. The scalar tail ORs each
byte it inspects.

### 4.2 AVX2 region — the prototype's hole (the important fix)

Today `strscan.ScanStop` returns only an index, so the prototype sets
`hi = ^uint64(0)` for anything it delegated — i.e. **every string longer than
`guessLong` (16) clean bytes is force-validated**. `golang_source` has 10905 such
strings and zero non-ASCII bytes; it pays a full `utf8.Valid` over 858 KB for
nothing. This, not UTF-8 itself, is most of the measured regression.

Fix — the kernel already has the data in a YMM register:

```
VPMOVMSKB acc, mask          // existing: stop-byte lanes
VPMOVMSKB data, hmask        // NEW: sign bit of every byte == "byte >= 0x80"
ORL       hmask, nonascii    // NEW: accumulate
```

and on the `found` branch, zero the lanes at/above the stop before OR-ing
(`MOVL $1,cl-shift; SHLL; DECL` — plain shifts, no BMI2 needed, so the existing
AVX2-only CPUID gate stays valid). The scalar tail loop ORs each byte.

Signature becomes:

```go
func stringStopIndexAVX2(data []byte) (idx int, nonASCII bool)
func ScanStop(data []byte) (idx int, nonASCII bool)   // SWAR fallback accumulates too
```

`ScanStop` has four other call sites (the clean-run scans in paths 2/4/6/8); they can
ignore the second result in round 1.

**Cost:** one `VPMOVMSKB` + one `ORL` per 32 bytes, off the critical dependency chain
(the loop's exit test is unchanged). Expected ≈ 0 measurable — must be confirmed by
re-running the `strscan` micro-benchmarks and the corpus sweep.

### 4.3 Result

A pure-ASCII string is proven valid UTF-8 **without a second read of its bytes and
without a call**. Since 96–100% of corpus strings are pure ASCII (§5.2), the common
case is genuinely free, which is the whole basis for claiming this does not undo the
string-scan work.

---

## 5. Validation: the second pass, and how expensive it may stay

### 5.1 Measured cost of the prototype (baseline for the work)

`BenchmarkUTF8Validate`, MB/s, median of 6 × 200 ms, vs `off`:

| workload | L naive | L fused | VL naive | VL fused |
|---|---|---|---|---|
| canada_geometry | −1.0% | −0.3% | +5.0% | +3.8% |
| citm_catalog | −19.9% | −1.0% | −16.4% | −1.0% |
| citm_catalog_min | −23.2% | −10.0% | −9.8% | +1.0% |
| golang_source | −26.0% | −6.6% | −25.5% | −13.3% |
| twitter_status | −31.9% | −23.8% | −25.2% | −19.6% |

Reading: `naive` (validate every value) is unacceptable. `fused` is already close to
free where strings are short (`citm_catalog`), and loses where strings are *long*
(`golang_source`, `citm_catalog_min`, `twitter_status`) — the §4.2 hole — plus a
genuine non-ASCII cost on `twitter_status`.

### 5.2 Why the fused design can work — corpus profile

`TestCorpusStringProfile`:

| workload | strings | ≥16 B | string bytes | non-ASCII strings | non-ASCII bytes |
|---|---|---|---|---|---|
| canada_geometry | 12 | 1 | 90 (0.0%) | 0 (0.00%) | 0 |
| citm_catalog | 26604 | 1360 | 221 KB (12.8%) | 108 (0.41%) | 2.4 KB |
| citm_catalog_min | 26604 | 1360 | 221 KB (44.2%) | 108 (0.41%) | 2.4 KB |
| golang_source | 102451 | 10905 | 858 KB (44.2%) | 0 (0.00%) | 0 |
| twitter_status | 18099 | 6405 | 368 KB (58.3%) | 755 (4.17%) | 109 KB |

So: detection must be free (it is, §4), and the validator only ever sees 0–4% of
values — but on `twitter_status` those carry 109 KB, 30% of all string bytes.

### 5.3 Round-1 validator: `utf8.Valid`

`utf8.Valid` has an 8-byte ASCII fast path then a scalar `acceptRanges` loop
(~1–1.5 GB/s on genuinely multibyte data). For four of five workloads it will be
invoked on ~0 bytes. Only `twitter_status` pays.

Error position: `utf8.Valid` gives none. For strict mode we want the offset of the
offending byte in the error. Use a small `validateIndex(b []byte) int` (returns −1 or
the first bad index) built on `utf8.DecodeRune`, called **only on the failing value** —
the fast answer stays `utf8.Valid`. This mirrors simdutf, which likewise SIMD-detects
block-wise and then `rewind_and_validate_with_errors` scalar to locate the byte.

### 5.4 Round-2 (🔬 gated on round-1 numbers): the simdutf port

What `simdutf::validate_utf8_with_errors` actually does
(`src/generic/utf8_validation/utf8_lookup4_algorithm.h`) — the Keiser–Lemire
`lookup4` algorithm, per 32-byte block:

1. `prev1 = concat_shift(prev_block, block, 1)` — `VPALIGNR` + `VPERM2I128`.
2. Three `VPSHUFB` table lookups, `AND`-ed together (`check_special_cases`):
   `prev1 >> 4` (16-entry table), `prev1 & 0x0F`, `block >> 4`. The AND of the three
   is a per-byte bitset of violated rules: TOO_SHORT / TOO_LONG / OVERLONG_2 /
   OVERLONG_3 / OVERLONG_4 / SURROGATE / TOO_LARGE / TWO_CONTS. This single step
   catches every 2-byte-sequence error class, including surrogates and overlongs.
3. `check_multibyte_lengths`: `prev2`/`prev3` saturating-subtract to assert that
   positions requiring a 2nd/3rd continuation actually have one; `XOR` with (2).
4. `error |= …`; an all-ASCII block short-circuits to just `error |= prev_incomplete`.
5. `is_incomplete` on the last block catches a sequence truncated at the end.
6. Errors are **block-granular**; the exact offset comes from a scalar rewind.

Port scope for us: AVX2 only (no AVX-512/ICL paths, no NEON), 32 bytes/iter, one
`YMM` of carried state (`prev_block`) plus an error accumulator. ~120 lines of avo.
`VPSHUFB` is per-128-bit-lane on AVX2, which the algorithm already accounts for
(the tables are duplicated across lanes). Expected 15–25 GB/s, i.e. the
`twitter_status` cost collapses from ~100 µs to ~5 µs.

Portable/SWAR fallback: **do not** attempt a SWAR `lookup4`. Follow the classic
branch-per-sequence range table (the Trifunović article's approach) or simply keep
`utf8.Valid`, which already is that, well-tuned, in the stdlib. Recommendation: keep
`utf8.Valid` as the non-amd64 / `WithoutAVX2` validator and add only the AVX2 kernel.

**Decision gate:** if round 1 lands every workload within ~3% and `twitter_status`
within ~8%, round 2 is optional polish and can be deferred behind the roadmap's
Phase 4. Re-measure before writing any assembly.

---

## 6. Replacement (`UTF8Replace`) implementation

Only reached when detection says non-ASCII **and** validation says invalid — i.e.
never on the hot path.

```go
// in the shared package (§7.1): append src to dst, U+FFFD per invalid byte.
func utf8x.Sanitize(dst, src []byte) []byte
```

- Copies the valid prefix in bulk (up to `utf8x.FirstInvalid`), then per-rune from
  there.
- The lexer wraps it: writes into `in.CurrentValue` — the existing scratch — so no new allocation and the
  reuse contract is unchanged. Careful: on the L escaped path the value *is*
  `in.CurrentValue`, so sanitize in place / via the second scratch, not aliasing
  itself. (`PreviousBuffer` is not free for this; add a small `sanitizeScratch` or do
  a right-to-left in-place rewrite — replacement is 3 bytes and the shortest
  ill-formed subpart is 1, so the value can *grow*: in-place needs care, prefer the
  scratch.)
- `MaxValueBytes` is re-checked **after** rewriting (a value can grow by up to 3×).
- `VL`: applies to raw bytes only; escape text is never rewritten (§2.5).

---

## 7. Plumbing

### 7.1 Where the primitives live (revised — writers will reuse them)

> **FRED** we'll add a check on writers too. We might reuse the same primitive
> functions for fast-checking this so we might reconsider a refactor to higher level
> of "internal" package for those.

Import reachability decides this, so it is worth getting right on the first commit
rather than moving code later:

| package | reachable from |
|---|---|
| `json/lexers/default-lexer/internal/{swar,strscan}` | `default-lexer` only |
| `json/lexers/internal/scan` | anything under `json/lexers/…` |
| **`json/internal/…`** | **anything under `json/…` — lexers *and* writers** |
| — | `json/lexers/yaml-lexer` is a **separate module**: cannot import any of these |

So the new, shareable code goes in a new **`json/internal/utf8x`**:

```go
package utf8x

func Valid(b []byte) bool            // fast yes/no; AVX2 kernel in round 2, utf8.Valid now
func FirstInvalid(b []byte) int      // -1, or the index of the first bad byte (cold, error path)
func Sanitize(dst, src []byte) []byte // append src to dst, U+FFFD per invalid byte (§2.4)
```

`writers/default-writer/escape.go` then adopts `utf8x.Sanitize`'s rule instead of its
open-coded `DecodeRune`/`AppendRune` loop, and gains the strict variant it lacks today
(see §10 round 3).

What does **not** move: the *fused detection* is inline SWAR in the lexer's scanners
and stays there — it is not a callable primitive, it is an `OR` in an existing loop.

**Deliberately deferred:** hoisting `swar` and `strscan` themselves to `json/internal/`
so the writers can use the SWAR masks too. It is a pure move, but it touches the
hottest, most codegen-sensitive files in the tree. Do it as its **own commit**, before
or after this work but never inside it, gated on `TestInlinable` + the devirt tests +
a full corpus sweep showing byte-identical performance.

### 7.2 Lexer-side plumbing

- `options.utf8Policy UTF8Policy` replaces `validateMode int`; `L.reset()` mirrors it
  into `in.UTF8Policy` next to `NoAVX2` (keep it in the cold tail of the struct so hot
  cursor field offsets are untouched — the prototype already respects this).
- `in.SawNonASCII` stays, but must be **reset per value** (the prototype leaves it
  set once any pessimistic path runs; harmless today because it only over-triggers,
  but it becomes a real correctness trap the moment anything else reads it).
  Prefer passing it as a local/return value rather than as `Input` state, if that
  survives the inliner.
- New error code `ErrInvalidUTF8 = "invalid UTF-8 sequence in string"` in
  `error-codes/errors.go`. Do **not** reuse `ErrInvalidRune`, whose text says
  "in unicode escape sequence" and whose meaning is the escape-level failure.
- Pooled lexers (`pools.go`): confirm the policy survives `Reset*` the way
  `elideSeparator` does (`VL.reset()` has a comment about exactly this trap).

---

## 8. Test plan

- **Conformance** — the 10 files of §1.3 flip to reject under strict; add a
  `UTF8Replace` mode to `conformanceModes()` where they flip back to accept.
  Add the missing **`VL/reader`** mode (today only `L/bytes`, `L/reader`, `VL/bytes`
  run — VL's streaming paths 7/8 are unexercised by conformance).
- **Golden table** — per path (§3, all 8) × per policy (3): a table test with
  hand-built inputs that force each path (whole/stream × clean/escaped × L/VL),
  asserting accept/reject, the exact value bytes, **and the error offset**.
- **Escape semantics** — the §2.3 table verbatim, both lexers, all three policies,
  including `\uD800\uD800\n` yielding *two* replacements.
- **Boundary** — an ill-formed sequence straddling a refill boundary in streaming
  mode (`WithBufferSize(64)` and sizes that split a 3/4-byte sequence).
- **BOM** (§2.8) — accepted at offset 0; `VL` replays it via `LeadingSpace()` and a
  round-trip of `EF BB BF {}` is byte-identical; BOM-only and truncated-BOM still
  rejected; a BOM *after* the first token is still an error; UTF-16 BOM yields
  `ErrNotUTF8`.
- **`utf8x` unit + fuzz** — the primitives tested on their own, before the lexer:
  `Valid` must agree with `utf8.Valid` on random input, `Sanitize` must be idempotent
  and always produce `utf8.Valid` output. This is what makes the round-2 AVX2 kernel
  swap-in safe.
- **Differential** — corpus + fuzz corpus: for every string token, our verdict must
  agree with `utf8.Valid`, and under replace our output must equal a reference
  maximal-subpart sanitizer.
- **Fuzz** — extend `FuzzLexer`/`FuzzYL` seeds with invalid-UTF-8 bodies; assert the
  invariant "a token value emitted under Strict or Replace is always `utf8.Valid`".
  This is the single strongest guard and directly encodes TODO §1.
- **Inline/devirt guards** — `TestInlinable` (swar) and the existing devirt tests must
  stay green; re-check `strscan`'s micro-benchmarks after the kernel change.
- **Bench** — `BenchmarkUTF8Validate` (currently in
  `json/benchmarks/lexers/lanes/utf8scratch_test.go`, a throwaway compass instrument)
  gets rewritten against the policy API and kept until the numbers are settled.

---

## 9. Conformance audit (TODO §6)

Current run: **y_ 95/95, n_ 188/188, xfail 0, 35 `i_` cases.** Full `i_` inventory
with current verdicts and the expected post-change verdict:

| group | files | now | strict | note |
|---|---|---|---|---|
| **raw invalid UTF-8** | the 10 of §1.3 | accept | **reject** | the bug being fixed |
| **broken surrogate escapes, L≠VL** | `1st_valid_surrogate_2nd_invalid`, `incomplete_surrogates_escape_valid`, `inverted_surrogates_U+1D11E` | L accept / VL reject | **reject** (both) | §2.3 — real divergence |
| **broken surrogate escapes, agreed** | `1st_surrogate_but_2nd_missing`, `incomplete_surrogate_pair`, `incomplete_surrogate_and_escape_valid`, `invalid_lonely_surrogate`, `invalid_surrogate`, `lone_second_surrogate`, `object_key_lone_2nd_surrogate` | reject | reject | already strict |
| **UTF-16 input** | `utf16BE_no_BOM`, `utf16LE_no_BOM`, `UTF-16LE_with_BOM` | reject | reject | documented: UTF-8 only |
| **BOM** | `structure_UTF-8_BOM_empty_object` | reject | **accept** | §2.8 — leading BOM is now consumed; NOT carried in `VL.LeadingSpace()`, see the round-2 outcome |
| **number magnitude** | 10 `i_number_*` (huge exp, overflow, underflow) | accept | accept | correct by design — we don't evaluate numbers |
| **depth** | `structure_500_nested_arrays` | accept | accept | correct since the stack rewrite |

**Assessment of "how many similar bugs remain hidden".** The conformance harness
itself has two blind spots, and both are worth closing as part of this work:

1. `i_` cases are recorded but never asserted, so a *change* in implementation-defined
   behavior is invisible. Fix: snapshot the `i_` verdict table as a golden file and
   fail on drift. That would have caught this bug the day the alias fast path landed.
2. `VL/reader` is not a conformance mode, so `consumeStringRawStreamFast` /
   `consumeStringRawStreaming` are conformance-untested. Two of the eight string
   paths have no coverage here.

Beyond UTF-8 and the BOM (§2.8, now handled), the surviving `i_` divergences are all
*deliberate*: number magnitude (we don't evaluate numbers, so nothing overflows),
UTF-16 input (documented UTF-8-only), and deep nesting (correct since the stack
rewrite). So after this work the honest statement is: **the lexers are conformant on
`y_`/`n_`, and every implementation-defined behavior is deliberate, documented and
pinned against drift.** No further hidden bugs of this class are visible in the
parsing suite — which is exactly why blind spot #1 above matters: it is the only
thing that would catch the *next* one.

---

## 10. Work breakdown

**Round 1 — correctness + free detection**

- ✅ 1.0 New package `json/internal/utf8x` (§7.1): `Valid` / `FirstInvalid` /
  `Sanitize` + its own unit + fuzz tests, independent of the lexer.
- ✅ 1.1 `ErrInvalidUTF8` error code (+ `ErrNotUTF8` for the UTF-16 BOM, §2.8).
- ✅ 1.2 `UTF8Policy` + `WithUTF8Policy`; delete `WithUTF8ValidationMode` /
  `WithUTF8Validation`; plumb through `options` → `L.reset()` → `Input`.
- ✅ 1.3 Revert the prototype's `…WholeV` forks; fold `hi |= w` into
  `consumeStringWhole` / `consumeStringRawWhole` / the two `…StreamFast` paths.
- ✅ 1.4 `ScanStop` + AVX2 kernel return `nonASCII` (avo regen, `go generate`).
  Verify `TestInlinable` + `strscan` micro-benchmarks.
- ✅ 1.5 Validation at `finishStringValue`: `utf8x.Valid` gated on detection, plus
  `utf8x.FirstInvalid` for the error offset.
- ✅ 1.6 Escape-level unification (§2.3): strict rejects broken pairs in `L`; stop
  swallowing the second escape; passthrough/replace share the escape path (§2.7).
- ✅ 1.7 `utf8x.Sanitize` + `UTF8Replace` wiring (L values, VL raw bytes).
- ✅ 1.8 Leading BOM (§2.8): trim at offset 0; `VL` carries it in `blanks`; UTF-16 BOM
  diagnostic. Guard `n_structure_UTF8_BOM_no_data` / `n_structure_incomplete_UTF8_BOM`.
- ✅ 1.9 Tests §8; conformance updates + `VL/reader` mode + `i_` golden snapshot.
- ✅ 1.10 Re-measure the §5.1 matrix. **Gate:** ≤3% on ASCII workloads, ≤8% on
  `twitter_status`.
- ✅ 1.11 Docs: `DESIGN.md`, `README.md`, package godoc, and the verbatim-pledge
  restatement (§2.5).

**Round 2 — ✅ shipped; gate now met (twitter_status −6.3% L / −1.7% VL)**

- ✅ 2.1 Port `check_special_cases` + `check_multibyte_lengths` + `is_incomplete` to
  avo AVX2 (`validateUTF8AVX2`), 32 B/iter, carried `prev_block`.
- ✅ 2.2 Scalar rewind for the error offset (simdutf's `rewind_and_validate_with_errors`).
- ✅ 2.3 Differential fuzz kernel-vs-`utf8.Valid` over random byte strings — mandatory
  before trusting hand-written vector validation.
- ✅ 2.4 Re-measure.

**Round 3 — writers (own commit, after round 1 lands `utf8x`)**

The writers have the mirror-image bug, already half-handled:
`writers/default-writer/escape.go:escapedBytes` silently substitutes U+FFFD for
invalid input bytes ("invalid runes are represented as �"), i.e. it hard-codes
`UTF8Replace` with **no way to get an error**. RFC 8259 says we must not emit invalid
UTF-8, and silently mangling a caller's payload without telling them is the same class
of complaint as the lexer's silent acceptance.

- ⬜ 3.1 Re-express `escapedBytes`' default branch on `utf8x` so lexer and writer share
  one rule (§2.4) and one implementation.
- ⬜ 3.2 Give the writers the same `UTF8Policy` knob. Open: what default? Symmetry with
  the lexer says Strict; today's behavior is Replace. **Strict** is the recommendation
  — a writer that silently corrupts is worse than one that refuses — but it is a
  louder breaking change than the lexer's, so it wants its own decision.
- ⬜ 3.3 The `\u` escape emission path needs the same audit (surrogate pairs on output).

**Follow-ups (out of scope, filed)**

- ⬜ The YAML lexer (`json/lexers/yaml-lexer`, `YL`) has its own scanner and is not
  covered by this plan — same audit needed. Note it is a **separate module**, so it
  cannot import `json/internal/utf8x`; sharing would need the primitives published or
  vendored.

> **FRED** yes. We have another plan to run a full YAML conformance suite on that one, and possibly
> contribute fixes to goccy/go-yaml. Another story, another time.

- ⬜ Hoist `swar` / `strscan` to `json/internal/` so writers can use the SWAR masks
  (§7.1) — pure move, own commit, codegen-verified.

---

## 11. Open questions

*(Both original questions resolved 2026-07-30 — kept for the record.)*

1. ~~**Should `UTF8Passthrough` also skip the escape-level checks?**~~ **Resolved:**
   no — an escape must always produce a rune. Settled and specified in §2.7.

> **FRED** agree (should be documented): explicitly escaped UTF-8 MUST produce some rune, so U+FFFD when broken and
> policy is lenient.

2. ~~**Do we want `UTF8Replace` to be observable?**~~ **Resolved: no.** No counter, no
   token flag; replacement is silent by design. Do not add hot-path state for it.

> **FRED** no. 

**Still open (needs a decision before it is implemented, not before round 1 starts):**

3. The writers' default policy (3.2 above): Strict — matching the lexer and refusing to
   emit corrupted output — or Replace, preserving today's behavior?

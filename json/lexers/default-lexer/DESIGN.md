# Design notes — default JSON lexer

Companion to the [README](README.md).

The README says *what* the lexer does and how to use it; this document explains *how it is built and why*:
the layering, the design choices behind the hot paths and heuristics, and — importantly — the **maintenance**
surface (the two code generators: `lexgen` for devirtualized scan cores, and `avo` for the AVX2 assembly kernel).

Audience: contributors changing the lexer, and readers who want to understand the trade-offs behind the numbers.

---

## 1. Guiding principles

Everything below follows from five constraints, in tension with each other:

1. **Lex, don't evaluate.** Values stay as `[]byte` slices of the input; no numeric
   conversion, no premature string materialization. The lexer validates the JSON
   grammar (it is "almost a parser") but never interprets a value.
2. **Zero *unamortized* allocation.** The fast paths allocate nothing per token: values
   alias the input buffer (whole-buffer mode) or a single reused scratch buffer
   (streaming). Tokens are returned **by value** (a 32-byte `token.T`), never by pointer,
   so they never escape to the heap.
3. **Bounded peak memory.** Peak ≈ `max(read-buffer window, longest single value)`.
   Streaming never holds more than the window plus the value currently being assembled.
4. **High throughput on realistic payloads**, biased toward large strings (GB/s, not
   MB/s) — with SWAR everywhere and AVX2 where it pays.
5. **One source of truth for the scan logic**, even though there are two lexers × two
   iteration styles × two input lanes (= eight concrete scan loops). We write the logic
   once as a generic core and generate the eight variants.

Constraints (2) and (5) fight each other — generics reintroduce an indirect call that
breaks the zero-alloc, fully-inlined fast path. §4 is how that fight is resolved.

---

## 2. Layering

Dependency arrows point downward (a package depends on what is below it). Everything is
internal except `token` and the top-level `lexer` package.

```
                      ┌───────────────────────────────────────────┐
   public API  ───▶   │  lexer  (package lexer)                    │
                      │  L, VL, options, pools, nesting stack,     │
                      │  dispatch (pull.go/push.go), core_*.go,    │
                      │  scan_gen.go (generated), pointer.go       │
                      └───────────────┬───────────────┬───────────┘
                                      │               │
              ┌───────────────────────▼──┐         ┌──▼───────────────────────┐
              │ internal/input           │         │ json/lexers/internal/scan│
              │ Input: scan cursor +     │         │ whitespace skip, hex/\u   │
              │ value scanners           │         │ (shared by all lexers)    │
              │ string, string_raw,      │         └───────────────────────────┘
              │ number, boolean, refill  │
              └───┬───────────────┬──────┘
                  │               │
        ┌─────────▼──────┐  ┌─────▼───────────────────────────────┐
        │ internal/swar  │  │ internal/strscan                    │
        │ SWAR masks     │  │ AVX2 string-stop + CPUID detect;    │
        │ (portable)     │  │ generated stringstop_amd64.s        │
        └────────────────┘  │ + _asm/ (avo generator, own module) │
                            └─────────────────────────────────────┘

        ┌───────────────────────────────────────────────────────┐
        │ token   token.T (32B value type), Unescape             │  ← leaf, used everywhere
        └───────────────────────────────────────────────────────┘

        ┌───────────────────────────────────────────────────────┐
        │ internal/lexgen   build-time only; parses core_*.go    │  ← not imported at runtime
        │ and emits scan_gen.go (see §4)                         │
        └───────────────────────────────────────────────────────┘
```

Notes:

- **`token`** is a leaf. `token.T` is a 32-byte value: `kind`, a `[]byte` value slice, a
  delimiter enum, a bool. See §6 for why 32 bytes and by-value.
- **`json/lexers/internal/scan`** is shared with *other* lexer implementations under
  `json/lexers` (not just this one): stateless primitives `ConsumeWhitespace`,
  `ConsumeWhitespaceTracked`, `Unhex`, `Hex4`. They inline into the hot cores.
- **`internal/input`** owns the scan cursor (`Input`) and every value scanner (strings,
  numbers, booleans, null) plus buffer refill. It is the cohesive, `L`-independent state
  the cores drive. Extracted here so the cursor and the value scanners live together and
  the top-level package stays about *dispatch and grammar* (see the git history "Step
  A–D" for the extraction).
- **`internal/swar` / `internal/strscan`** are the two acceleration tiers for string
  scanning (§7.3). `strscan` is self-contained (no `avo`/`x/sys` at runtime) and carries
  its own generator in a `_`-prefixed sub-module so the parent never pulls `avo`.
- **`internal/lexgen`** is a `main` package run by `go generate`; it never ships in the
  binary.

---

## 3. The two lexers and the "token-vs-state arbitrage"

There are two lexers, but **one token type** (`token.T`, 32 bytes):

| Lexer | Type | Strings | Whitespace / position | Separators (default) |
|-------|------|---------|------------------------|----------------------|
| **`L`** semantic | `*L` | **decoded** (escapes resolved, UTF-16 pairs joined) | dropped | elided |
| **`VL`** verbatim | `*VL` (embeds `*L`) | **raw** (escapes intact, decode on demand via `token.Unescape`) | preserved | emitted |

`VL` embeds `*L`, so it reuses all of `L`'s machinery and adds only the verbatim
behavior. The verbatim extras — the run of insignificant whitespace before a token, and
its 1-based line/column — are **not baked into the token**. That is the *token-vs-state
arbitrage*: rather than widen the token (more memory traffic on every token, including
the 99% of consumers that never ask), the extras are kept as **lexer state**, valid until
the next token, read back through accessors:

- `VL.LeadingSpace() []byte` — the preceding whitespace run (zero-copy in buffer mode).
- `VL.Line() / VL.Column() int` — position of the current token's start.
- `L.JSONPointer() expressions.Pointer` — the RFC 6901 path (opt-in, both lexers; §7.6).

The semantic lexer deliberately exposes **no line/column** — tracking position forces
per-newline bookkeeping in the whitespace skip and cannot be recovered lazily from a
streaming buffer (past bytes are gone). Byte `Offset()` is always available. This
asymmetry is load-bearing for performance (§9): dropping position from the semantic core
freed registers that paid for the whitespace batch-skip.

---

## 4. The unified core, policies, and devirtualization

### 4.1 Why a generic core

The eight concrete scan loops (2 lexers × {pull, push} × {buffer, stream}) share one
grammar. Duplicating it eight times is a maintenance and bug-surface disaster. So the
logic is written once, generic over an **emit policy**:

```go
type emitPolicy[T any] interface {
    emit(t token.T, blanks []byte, line, col int) T
    none() T
    eof(blanks []byte) T
    tracksPosition() bool   // constant per policy → constant-folds
    storesBlanks()   bool   // constant per policy → constant-folds
}
```

Two zero-size policies implement it: `semanticPolicy` (identity emit; `tracksPosition`
and `storesBlanks` both return the constant `false`) and `verbatimPolicy` (both `true`,
plus it stashes blanks/position into lexer state). Because those two predicates are
constant per policy, the monomorphized cores **constant-fold and dead-code-eliminate**
all position/blank bookkeeping in the semantic variant — its hot loop does no per-newline
work at all, matching `jsontext`'s offset-only model.

### 4.2 The generic-dictionary tax

Go does **not** devirtualize a call through a type parameter's method set: `p.emit(...)`
routes through the generics dictionary — an indirect call the compiler cannot inline.
Measured cost: **~5% on the semantic lexer**, because `emit` is called once per token on
the hottest path. Left as-is, generics would defeat constraint (1)'s fully-inlined fast
path.

### 4.3 `lexgen`: monomorphize to devirtualize

`internal/lexgen` (run via `//go:generate go run ./internal/lexgen`, see `core.go`) lifts
each generic core into a plain, non-generic function **per policy**, so the `p.emit`
calls become direct and inline. It is deliberately dumb and drift-proof:

- It globs `*.go` (skipping `_test.go` and the output), parses each file, and records the
  **verbatim source text** of each named generic core.
- The five cores it knows about — `scanTokenBufferG`, `scanTokenStreamG`, `scanPushG`,
  `scanPushStreamG`, `errCheckG` — are keyed **by name**, so splitting them across
  `core_*.go` files does not matter; output order is a fixed `variants × cores` product.
- For each `(variant, core)` it rewrites only the **signature line** and the intra-core
  `errCheckG(` → `errCheck<Variant>(` calls; the body is copied byte-for-byte.
- A **drift guard**: if the exact expected generic signature is no longer present in the
  source, generation fails loudly (`signature drift for …`), forcing `lexgen` to be
  updated in lockstep with a core signature change.
- Output: `scan_gen.go` — 2 variants × 5 cores = **10 concrete functions**
  (`scanTokenBufferSemantic`, `scanPushVerbatimCore`, …). No build tags, no dispatch
  layer: the generic cores *and* the generated concrete cores both stay in the package
  and are both reachable, so a benchmark can drive each and measure the devirt gap in one
  binary (see `BenchmarkDevirt` and the equivalence guards in `devirt_test.go`).

**The invariant that makes this safe:** because bodies are copied verbatim and keyed by
name, reorganizing the `core_*.go` sources (or reformatting them with `gofumpt`) produces
a **byte-identical** `scan_gen.go`. Any diff there means a real logic change. Always
re-run `go generate` after touching a core and confirm the diff is intentional.

### 4.4 Pull vs push, and the "yield seam"

- **Pull** (`NextToken() token.T`) returns a token by value — no closure, nothing to keep
  on the stack — so `NextToken` calls the generated core directly.
- **Push** (`Tokens() iter.Seq[token.T]`) is range-over-func: the loop body is a `yield`
  closure. To keep that closure **stack-allocated** (zero-alloc iteration), `Tokens`
  returns an `iter.Seq` whose body calls a real `//go:noinline` wrapper
  (`scanPushSemantic`, `scanPushStreamVerbatim`, …). This "yield seam" is **not** the
  devirtualizer (the generated `*Core` functions already did that); its job is the
  range-over-func contract, verifiable with `-gcflags=-m`:
  - it keeps `Tokens` (and its returned `Seq`) inlinable at the call site; and
  - the wrapper's escape summary leaks only `l`, **not** `yield`, so the closure never
    reaches the heap.

  Inline any wrapper and the ~400-line core resurfaces in the `Seq` body; the range
  desugaring then heap-allocates `yield` in external callers (+2 allocs/call).

### 4.5 Buffer vs stream lanes

`NextToken`/`Tokens` dispatch once on `l.in.WholeBuffer`:

- **Whole-buffer lane** (`scanTokenBufferG` / `scanPushG`): the cursor is a pure local
  `i`, there is no `readMore`, and preceding blanks are a zero-copy slice of the input.
  This is the **champion** — the source of the corpus wins — and is kept frozen.
- **Stream lane** (`scanTokenStreamG` / `scanPushStreamG`): refills the fixed window as it
  scans, appends blanks byte-by-byte across refills, and copies values into a scratch
  buffer. Optimized separately so stream work never perturbs the champion (§9).

A streaming source whose entire input fits in the first read is **promoted** to
whole-buffer mode (`Input.FirstFill`), so small streams get the champion path for free.

---

## 5. Grammar state

The cores are almost a parser. Two pieces of state drive validation:

- **`l.current` (`token.T`)** is the grammar memory: the loop reads its kind/delimiter to
  validate the *next* byte (e.g. a value may not directly follow another value; a `,` may
  not follow `[`).
- **The nesting stack** (`stack.go`) tracks container depth and type as a slice of
  `uint64` words. Each word packs up to 63 levels as bits: bit 0 is the innermost
  container type (`0`=object, `1`=array); pushing shifts left and writes the new type bit,
  popping shifts right; the highest set bit is a sentinel giving the depth. The last word
  always describes the innermost container, so `isInObject`/`isInArray` read one word. A
  full word appends another (8 bytes per extra 63 levels); `WithMaxContainerStack` caps
  it as a circuit breaker against malicious deep nesting.

Object-key position is tracked on `Input` (`ExpectKey`/`AfterKey`): a string in key
position becomes a `token.Key` and requires a following `:`.

---

## 6. The token, and the "tiny-token floor"

`token.T` is a **32-byte value** carrying kind + value slice + delimiter + bool, built via
`MakeWithValue` / `MakeDelimiter` / `MakeBoolean`. Two deliberate choices:

- **By value, not by pointer.** A pointer token would let the token escape to the heap,
  breaking zero-alloc. By-value keeps tokens on the stack and in registers.
- **32 bytes, not 16.** A smaller token was measured: it trades *copy* cost for
  *value-access* cost. Smaller helps tiny-token workloads (e.g. `mesh`: 56% short integers
  at 2× density) but hurts the string/structure-heavy majority we win. 32-byte by-value is
  the price of zero-alloc value tokens, and it is what buys the broad corpus wins.

This is the **tiny-token floor**: on payloads dominated by 1–2 byte scalars, a measurable
fraction of time is the by-value 32-byte construct-and-copy on emit/yield. It is a
*property of the design*, not a missing fast path (the integer fast path exists).

We accept it because the same choice buys near-zero allocations and the wins everywhere else.

---

## 7. Heuristics and fast paths

The whole game is: **stay in a tight, branch-light, inlined loop as long as the bytes are
"boring", and only pay for the slow path at the exact byte that needs it.**

### 7.1 Whitespace batch-skip

In the semantic core (`tracksPosition()==false`) a run of whitespace is skipped in one
call to `scan.ConsumeWhitespace` (a SWAR scan) instead of byte-by-byte — this was the
`citm` bottleneck. The verbatim core keeps a position-tracking variant
(`ConsumeWhitespaceTracked` + `skipBlanksRestStream`) that also counts newlines and
bulk-appends the run into `l.blanks`; short runs stay on the cheap inline path so the
common single-space case pays no call.

### 7.2 Number full-grammar inline fast path

For an unbounded (`maxValueBytes==0`) whole-buffer number, the core scans the **entire**
JSON number grammar inline — integer digits, then an optional fractional part
(`. 1*DIGIT`), then an optional exponent (`(e|E) [+|-] 1*DIGIT`) — and emits
`token.MakeWithValue(Number, data[start:end])` directly, without ever calling a value
scanner. It only bails to `Input.ConsumeNumberWhole` for the error/edge cases (leading
zero, trailing dot, malformed exponent). This keeps the number-dense hot loop free of
call overhead. (Bounded numbers route to `ConsumeNumberStreaming`, which enforces the
cap.)

### 7.3 String scanning: fast/slow split + three tiers

A string body is scanned to its first "stop" byte (`"`, `\`, or a control `< 0x20`):

1. **Scalar probe** — a short inline byte loop catches short/clean strings with no setup.
2. **SWAR** (`internal/swar`) — 8 bytes per word via `StringStopMask` + `FirstByte`
   (`TrailingZeros`), for medium runs.
3. **AVX2** (`internal/strscan.ScanStop`) — 32 bytes per YMM iteration, for long clean
   runs.

The split matters because a *clean* string (no escapes) can be aliased zero-copy; only
when a `\` is seen do we fall into `consumeStringEscaped` (semantic: decode into the
scratch buffer) or `consumeStringRawEscaped` (verbatim: validate but keep raw, zero-copy
alias). There are dedicated whole-buffer, stream-fast, and streaming variants of each.

### 7.4 The AVX2 gate

AVX2 is not free: it has a 3-constant broadcast setup and a **non-inlinable** call. So it
is gated:

- Enabled per-CPU by runtime CPUID detection (`internal/strscan.detectAVX2`, via `cpuid`/
  `xgetbv` in assembly — checks OSXSAVE + AVX2). `ScanStop` delegates to the kernel only
  when `useAVX2 && len(data) >= avx2Min` (crossover **32 bytes**); otherwise SWAR.
- The delegation is triggered by a **clean-bytes signal** (the scanner has already seen
  ~16 clean bytes, so the string is likely long), and the call is **hoisted out** of the
  per-word loop — a lesson from the register-pressure work (§9): a call inside the word
  loop spills the loop's live registers every iteration.
- `WithoutAVX2` makes the caller simply never delegate (deterministic, CPU-independent
  runs); it does not change the token stream, only how the scan is performed.

### 7.5 Separator elision

`L` validates `,` and `:` but does not emit them (`WithElideSeparator`, default on),
matching `jsontext`: the structure is unambiguous from values + container delimiters, and
the consumer walks fewer tokens. `VL` defaults to emitting them (round-trippable). The
pointer tracker (§7.6) is derived so it is **independent of elision**.

### 7.6 JSON pointer tracker (opt-in)

`WithJSONPointer` maintains the RFC 6901 path to the current token as an
`expressions.Pointer` (interned string keys + **raw integer** array indices — no decimal
formatting until `.String()`), exposed by `JSONPointer()`. Design points:

- **RFC "container's own path" semantics**, forced by the builder API
  (`Pointer.AppendKey`/`AppendElem` — there is no "append a placeholder", so a segment
  appears only once its key/index is known): opening `[` at `/a` → `/a`; a root `}` → `""`.
- **Key form is the lexer's own** — formed directly from `token.Value()`, so `L` gives the
  decoded logical name and `VL` gives the raw bytes, with no per-lexer branch. RFC `~`/`/`
  escaping is applied by `Pointer.String`, so the segments passed in are logical.
- **Driven *after* the core returns**, from `NextToken` / the push seam, by a tiny frame
  stack (one frame per open container). The champion cores never see it: the new `L`
  fields sit *after* the embedded `options` so no hot-core field offset moves and
  `scan_gen.go` stays byte-identical; when the option is off the pull path adds one
  predictable branch and the push fast paths are untouched (`yield` still non-escaping),
  so the off path keeps its zero-alloc guarantee. Enabling the knob forfeits zero-alloc
  (the frame stack and retained keys allocate) — a documented, opt-in cost.

---

## 8. Memory model and lifetimes

- **Whole-buffer mode** (`NewWithBytes`/`ResetWithBytes`): token values **alias the
  caller's buffer**; it must stay stable until the lexer is done. Zero-copy, zero-alloc.
- **Streaming mode** (`New`/`ResetWithReader`): a fixed window (`WithBufferSize`, default
  4 KB, rounded up to a 32-byte AVX2 stride) is refilled; values that span refills are
  copied into a single reused scratch buffer. `WithMaxValueBytes` bounds that buffer (and
  the verbatim blanks buffer) against a value/whitespace flood; `WithEnsureErrorContext`
  optionally keeps the previous window for richer error context.
- **Accessor lifetime.** Every "state" accessor — `LeadingSpace`, `Line`, `Column`,
  `JSONPointer` — and the token's value slice are valid **only until the next token**.
  Retain by cloning (`token.T.Clone`, `expressions.Pointer.Clone`).
- **Pooling.** `BorrowLexerWithBytes`/`BorrowLexerWithReader` return a lexer and a
  `redeem` closure; `Reset` is idempotent and drops every reference to caller memory (so
  the pool never pins/exposes user bytes) while keeping a streaming-owned buffer's
  capacity for reuse. A warm borrow→lex→redeem cycle of a small doc allocates nothing (see
  `TestBorrowRedeemAllocFree`).

---

## 9. Performance, alignment, and register pressure

Two hard-won facts govern how the cores are edited. See the [README PGO
section](README.md#performance-and-pgo) for the user-facing version.

- **Hot-loop alignment is PGO-gated.** On amd64 the Go compiler emits `PCALIGN` for hot
  blocks only under `-d=alignhot`, which currently requires `-pgo`. Without PGO the tiny
  number/whitespace loops fall wherever linear code size places them, so an *unrelated*
  code-size change can shift a hot loop across a 32-byte boundary and swing
  `numbers`/`mesh` by ~±10% **with the same instructions executed**. Rule when comparing
  non-PGO builds: treat ≤10% moves on those two workloads as alignment noise; trust the
  broader geomean, the string-heavy workloads, and — for a claim that a change is
  *neutral* — a **byte-identical core diff** (`go build -gcflags=-S`, normalize, compare
  the mnemonic histogram). Byte-identical asm ⇒ the code cannot have regressed.
- **The semantic push loop is register-saturated.** It uses all 14 general-purpose
  registers (only `R14`/`g` reserved), zero headroom. This is *why* the semantic lexer
  dropped line/column: when position was tracked, `l.line`/`l.lineStart` were live across
  every arm, and adding the whitespace batch-skip spilled them and regressed numbers.
  Dropping position freed those registers and made the batch-skip register-clean. **The
  discipline that follows:** do not add per-token state that becomes live across the main
  loop of a champion core. New optional state (like the pointer tracker) is driven from
  the *dispatch wrapper*, after the core returns, and its fields are placed after the
  embedded `options` so no hot-core field offset moves.

We deliberately do **not** contort the source to chase alignment (e.g. forcing a loop
body into a `//go:noinline` function): inlining does not carry alignment, and a
non-inlined call reintroduces the very overhead the inline fast paths avoid. **PGO is the
supported lever.**

---

## 10. Maintenance playbook

### 10.1 Changing a scan core

1. Edit the generic core in its `core_*.go` source (`scanTokenBufferG` in
   `core_pull_buffer.go`, `scanPushG` in `core_push_buffer.go`, the stream variants in the
   `*_stream.go` files, `errCheckG` in `core.go`).
2. Run `go generate ./...` (or `go run ./internal/lexgen` from the package dir) to
   regenerate `scan_gen.go`. **Never hand-edit `scan_gen.go`.**
3. `git diff scan_gen.go` — the diff must reflect exactly your logic change and nothing
   else. A change to the semantic variant should not touch the verbatim variant unless you
   intended it.
4. If you changed a core's *signature*, update the matching `genSig`/`concSig` in
   `internal/lexgen/main.go` (the drift guard will otherwise fail generation).
5. Build/vet/test **and** `-race`. For any claim of perf neutrality, diff the core asm or
   run a fresh PGO A/B — not a stale-baseline non-PGO `benchstat` (see §9).
6. Preserve the zero-alloc guards: `TestBorrowRedeemAllocFree` and the pointer off-path
   `TestTokensPushAllocFreePointerOff` are gated to `!race` builds because
   `testing.AllocsPerRun` is unreliable under `-race`.

### 10.2 Changing the AVX2 kernel

The kernel is `stringstop_amd64.s`, **generated by `avo`**, and its generator lives in a
`_`-prefixed sub-module so the parent `json` module never depends on `avo`/`x/sys`:

1. Edit `internal/strscan/_asm/asm.go` (the `avo` program).
2. From `internal/strscan/_asm`, regenerate: `go run . -out ../stringstop_amd64.s`
   (this is exactly what `//go:generate` in `scan_amd64.go` runs). The generated `.s` has
   **no `avo` import**, so the parent stays clean.
3. Keep the three implementations in agreement — `stringStopIndexAVX2` (asm),
   `scanStopSWAR` (portable, `scan_amd64.go`), and the `scan_noasm.go` fallback for
   non-amd64 — they must return the same index for the same input. `strscan_test.go`
   differential-tests them; `bench_amd64_test.go` sizes the `avx2Min` crossover.
4. Detection (`detect_amd64.go`, `cpuid_amd64.s`) must not be bypassed: the kernel uses
   AVX2/BMI and is only safe when `detectAVX2()` (OSXSAVE + AVX2) is true.
5. The `_asm` sub-module has its own `go.mod`/`go.sum`; update them there, not in the
   parent, when bumping `avo`.

There is a **second** `avo` kernel outside this package: the UTF-8 validator in
`json/internal/utf8x` (`validate_amd64.s`, generator in `json/internal/utf8x/_asm`), a port
of simdutf's `lookup4` algorithm. It follows the same rules, and carries its own CPUID
detection because it is a different module path. Its differential suite
(`validate_amd64_test.go`) is exhaustive over all byte pairs and over every lead byte against
all 2nd/3rd bytes — a hand-written validator that wrongly ACCEPTS is silent, so that suite is
the safety net and must stay green (it is mutation-tested: corrupting one table byte fails it).

### 10.3 Adding an option

1. Add the field to `options` (`options.go`) and a `With…` constructor following the
   existing style. Keep the zero value the safe/off default.
2. Wire it into `L.reset()` (mirror the option into any hot-path gate field, as
   `trackBlanks`/`trackPtr` do) and, if it affects streaming setup, `ResetWithReader`.
3. If it must be honored by `VL`, remember `VL` seeds its own defaults via `verbatimOpts`
   before the caller's options apply.
4. If the option adds per-token work, gate it so the **off** path is unchanged, and place
   any new `L` fields **after** the embedded `options` (§9). Add an alloc guard if the off
   path must stay zero-alloc.

### 10.4 Conformance and equivalence

- The full JSON conformance suite must pass (`conformance_test.go`) — no leniency.
- `devirt_test.go` guards that the **generic** and **generated** cores produce identical
  token streams and terminal errors (pull + push, buffer + stream, `L` + `VL`).
- `push_pull_equivalence_test.go` guards that pull and push agree.
- `pointer_test.go` guards pointer semantics across all four lanes; `zerocopy_test.go`
  guards value aliasing; `security_test.go` guards the circuit breakers.

---

## 11. Map of the source

| File | Role |
|------|------|
| `semantic_lexer.go` | `L` type, constructors, `Reset*`, error context |
| `verbatim_lexer.go` | `VL` type (embeds `*L`), verbatim accessors |
| `pull.go` | `NextToken` dispatch (buffer/stream lane, pointer hook) |
| `push.go` | `Tokens` iterator + the `//go:noinline` yield-seam wrappers |
| `core.go` | policies (`emitPolicy`, semantic/verbatim), `errCheckG`, `//go:generate` |
| `core_pull_buffer.go` / `core_pull_stream.go` | generic pull cores (champion / stream) |
| `core_push_buffer.go` / `core_push_stream.go` | generic push cores |
| `scan_gen.go` | **generated** concrete cores (do not edit) |
| `stack.go` | nesting-depth bitset stack |
| `options.go` | option knobs |
| `pools.go` | borrow/redeem pool |
| `pointer.go` | JSON pointer tracker (post-core) |
| `constants.go` / `eof_reader.go` / `doc.go` | byte constants, no-op reader, package doc |
| `internal/input/*` | `Input` cursor + value scanners + refill |
| `internal/swar/*` | SWAR masks |
| `internal/strscan/*` | AVX2 string-stop + CPUID + `_asm/` generator |
| `internal/lexgen/*` | the devirtualization code generator |

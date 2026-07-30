# Default implementation of a JSON lexer

A low-level JSON building block that **lexes without evaluating**. Concretely:

1. **No value evaluation.** Numbers and strings stay as `[]byte`; no numeric
   conversion, so no loss of resolution. Comparable in spirit to the experimental
   `encoding/json/v2` + `encoding/json/jsontext`.

2. **Zero unamortized allocation, bounded peak memory.** Everything lives in short-lived,
   recycled buffers. The only hard-coded literals are `null`/`true`/`false`.
   Peak memory ≈ max(read-buffer window, longest single value).

3. **Pluggable behind a common interface** (`json/lexers.Lexer`), so alternative
   implementations may be injected (incl. one on top of `encoding/json/v2`).

4. **Two flavors:**
   - **Semantic** (`L` → `token.T`): drops insignificant whitespace, normalizes UTF8 pairs.
   - **Verbatim** (`VL` → `token.T`): preserves whitespace/escapes; for
     linters / LSPs / formatters sensitive to exact positions.

5. **Streaming or buffered:** `io.Reader` (internally buffered) or `[]byte`.

## Design goals

We want a fast JSON lexer to feed our [json.Document] with the following requirements:

* support oldstable go - no GOEXPERIMENT, GODEBUG etc toggles
* zero allocation
* high throughput optimized for large strings (e.g. in GB/s, not MB/s)
* bounded memory (up to the largest ingested token)
* support for streams
* accurate error reporting and location (offset, error with surrounding text)
* short lived token, recycle any memory
* token knows its kind and value - other information (location, pointer, leading blanks...) are stored
  in the lexer's state
* JSON string escaping and UTF8 normalization:
  * `L`: the caller doesn't have to know these rules - strings are directly usable
  * `VL`: the caller wants unaltered strings, including escapes
* no loss of numeric precision: no conversion to native types
* push & pull iterators

Since we want it to be flexible, there are a few available options:

* optional knobs: some are available a toggles, some result from the choice of lexer `L` vs `VL`
  * security guards against overflows (max. depth, max. token length)
  * in streaming mode, ability to set the memory window being used
  * ability to elide semantically redundant separators (",", ":") from iterated tokens
  * ability to switch to "verbatim" mode, preserving non-significant blank, not escaping strings (that's our `VL` lexer)
  * verbatim mode tracks a token's line and column in the input text
  * option to track the JSON pointer of the current token (`WithJSONPointer`, both `L` and `VL`): exposes
    the RFC 6901 path from the document root to the current token as an `expressions.Pointer` via `JSONPointer()`
    (interned string keys, raw integer array indices). Opt-in and off by default — enabling it forfeits the
    zero-allocation guarantee (the path stack and retained keys allocate).
  * tunable context window for reporting errors

Additional objectives:

* reusable internal core for scanning tokens

Non-goals / out-of-scope:

* non-UTF8 encoding
* JSON canonicalization (RFC 8785)
* full SIMD implementation (à la simd-json)

## Design

Various design aspects considered:

  * push vs pull loops
  * heuristics for fast paths (for numbers, strings, white space)
  * inlining
  * generics & devirtualization
  * SWAR scanners
  * SIMD acceleration (AVX2): usage limited to fast-parse large strings (amd64 arch)

For the full picture — layering, the reasoning behind each heuristic, and the maintenance
of the two code generators (`lexgen` for the devirtualized scan cores, `avo` for the AVX2
kernel) — see the companion **[DESIGN.md](DESIGN.md)**.

Differences with `encoding/json/v2`

* ❌ : no, never
* ⏸️ : yes, always
* ✅ : opt-in, enabled by default
* ⬜ : opt-in, disabled by default
 
|                | L   | VL  | jsontext |
|----------------|-----|-----|----------|
| token size     | 32b      ||  16b?    |
| only UTF8      | ⏸️  |  ⏸️ |  ⏸️      |
| number as bytes| ⏸️  |  ⏸️ |  ⏸️      |
| token has value| ⏸️  |  ⏸️ |  ⏸️      |
| sep. elide     | ✅  |  ⬜ |  ⏸️      |
| string escape  | ⏸️  |  ❌ |  ❌      |
| track ns space | ❌  |  ⏸️ |  ❌      |
| track line/col | ❌  |  ⏸️ |  ❌      |
| track pointer  | ⬜       ||  ⏸️      |
| AVX2 acceler.  | ✅       ||  ❌      |
| push iterator  | ✅       ||  ❌      |    
| pull iterator  | ✅       ||  ⏸️      |
| limit stack    | ✅       ||  ✅      |
| limit tok size | ✅       ||  ❌      |    

Trade-offs when comparing to `github.com/go-json-experiment/json/jsontext` (stdlib `json/v2`).

* we favor string escaping as we scan, not later. This involves more lexer work, less consumer's work
* we indulge maintaining more code, possibly generated code

* our token is larger (more memory traffic) but cheaper to consume (no extra indirection or escpaping)
* actual token usage is lighter (no indirection, no escaping: all done in the `L` lexer most efficiently)
* our fast-path is zero-alloc, zero-copy
* our heuristics are less efficient for:
  * small values (single digits, booleans only...)
  * densely escaped strings 
* other workloads generally show higher performances, sometimes much higher

Our lexer's fastest path is to use the push iterator (`Tokens()`) from a buffer of bytes.

Streaming mode degrades speed by 15-20%.

Pull iterator (`NextToken`) mode degrades speed by 10-15%.
  
## UTF-8

RFC 8259 §8.1 requires JSON text to be UTF-8, and both lexers enforce that on string values. This covers both ways a
value can end up holding a non-character:

* an ill-formed UTF-8 byte sequence in the source (truncated, overlong, an encoded surrogate, beyond U+10FFFF);
* a `\uXXXX` escape that does not denote a Unicode scalar value — an unpaired, lone or inverted surrogate.

`WithUTF8Policy` picks what happens:

| policy | ill-formed bytes | broken `\u` escape |
|---|---|---|
| `UTF8Strict` (default) | `ErrInvalidUTF8` | `ErrSurrogateEscape` |
| `UTF8Replace` | U+FFFD per invalid byte | U+FFFD per broken escape |
| `UTF8Passthrough` | passed through untouched | U+FFFD per broken escape |

An escape always produces *some* rune — the escape text is not the value — which is why `UTF8Passthrough` still
substitutes there.

`VL` keeps escapes as source text, so under `UTF8Replace` it rewrites ill-formed *bytes* but never rewrites escape
text; the substitution for a broken escape appears when the value is decoded with `token.Unescape`.

### What mangling costs `VL`

U+FFFD encodes as **three** bytes and replaces **one**, so a mangled value is 2 bytes longer per ill-formed byte than
the text it came from — and a multi-byte fault is several ill-formed bytes, not one (a truncated 3-byte sequence
becomes three replacements: 9 bytes where the source had 3). For such a value:

* `len(token.Value())` is **not** the width of its source span;
* an index into the value **cannot** be added to the token's start to get a source offset;
* the original bytes are **not** recoverable from the token.

Token *positions* are unaffected: `Line()`, `Column()`, `Offset()` and `LeadingSpace()` derive from the scan cursor
walking the source, never from the value, so they stay exact under every policy. A formatter or linter can still point
at the right place — it just cannot assume the value it holds is the text that was there. Callers who need the source
bytes should use `UTF8Strict` (and handle the error) or `UTF8Passthrough` (and validate downstream themselves).

A leading UTF-8 BOM is consumed before any token exists, so it is not re-emitted either — the other documented
exception to byte-exact round-tripping (RFC 8259 §8.1 asks implementations not to emit one).

**Cost.** Detection is fused into the string scan already being performed — every SWAR word and every AVX2 block is
OR-accumulated, so "did this value contain a byte >= 0x80" is answered for free — and only values that actually carry
one reach the validator. On the reference corpus, `UTF8Strict` versus no validation is statistically indistinguishable
on the four ASCII-dominated workloads and costs ~10% on `twitter_status`, which is 30% non-ASCII by string bytes.

The input must be UTF-8: a UTF-16 document is rejected with `ErrNotUTF8`. A leading UTF-8 BOM is currently rejected as
an invalid token rather than skipped.

## Conformance tests

Our implementation of the JSON lexers pass the full JSON conformance suite. No compromise on strictness.

Beyond the suite's `y_`/`n_` contract, the implementation-defined (`i_`) cases are snapshotted in
`testdata/conformance_i_behavior.golden`, so a change in *implementation-defined* behavior shows up as a reviewable
diff instead of silence. Regenerate it with:

```
go test ./lexers/default-lexer -run TestConformanceParsing -update-golden
```

## Benchmarks

See [a comparison](../benchmark/benchviz/README.md)

## Performance and PGO

The scanning cores are dominated by a few very small, very hot loops (number and
whitespace scanning above all). On amd64 the Go compiler does **not** align inner
loops unless it is building with a profile — hot-block alignment (`PCALIGN`) is
gated behind PGO (`go tool compile -d=alignhot`, which "currently requires -pgo").

The practical consequences:

* **Build with PGO for production.** A representative CPU profile lets the compiler
  align the hot number/whitespace loops, which is worth a substantial, stable margin
  on number-dense inputs (~10% on our `numbers`/`mesh` corpora) and amplifies the
  string-heavy wins. Drop a `default.pgo` in the consuming `main` package, or pass
  `go build -pgo=<profile>`. See the [Go PGO guide](https://go.dev/doc/pgo).

* **Number-dense microbenchmarks are alignment-fragile without PGO.** Because those
  loops fall wherever linear code size places them, an *unrelated* source change that
  shifts code size by a few bytes can move a hot loop across a 32-byte boundary and
  swing `numbers`/`mesh` throughput by ~±10% in either direction — with no change to
  the instructions executed. When comparing non-PGO builds, treat ≤10% moves on those
  two workloads as alignment noise, not signal; read the broader corpus geomean and
  the string-heavy workloads instead. Under PGO this fragility disappears.

We deliberately do **not** contort the source to chase alignment (e.g. forcing loop
bodies into `//go:noinline` functions): inlining does not carry alignment with it,
and a non-inlined call reintroduces exactly the overhead the inline fast paths exist
to avoid. PGO is the supported lever.

## Roadmap

* AVX2 support is currently provided as assembly kernels for amd64 only
* AVX512 is likely overkill for our usage and I don't have the hardware to test it thoroughly
* AVX support this will be eventually replaced by go native support for AV2 & AVX512 (currently experimental)

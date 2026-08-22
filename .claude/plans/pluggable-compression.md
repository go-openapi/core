# Pluggable compression codec for `json/stores/default-store`

> Status: **parked 2026-08-22** — investigated, not started. No code written.
> Raised after the go1.27 flate work on branch `test/fix-assertions-for-go127` (commit `2516540`),
> That work exposed how much of the store's sizing logic assumes DEFLATE.
> Status legend: ✅ done · ⏳ in progress · ⬜ todo · 🔬 measure first.

## 0. TL;DR

The plumbing is easy: the store already talks to two interfaces, and there are exactly two places
that name a concrete flate type. The reasons to do it are **decompression speed** and **trained zstd
dictionaries**, not compression ratio — at this store's operating point (strings just over 128 bytes)
flate beats zstd and brotli on three of four samples, and it is the only codec that can reach the
inlined-compressed handle at all.

The risk sits in one place: **gob round-trip identity**. Arena bytes are codec-specific and the
serialized form records no codec name, so loading a store under the wrong codec corrupts data.

Recommendation: phase it (§7). Phase one adds `WithCodec` and no registry, which needs no format
change and lets a zstd codec be prototyped out-of-tree.

## 1. Why this is not a ratio win

Measured 2026-08-22 with `klauspost/compress v1.19.0` (zstd, `EncodeAll`, default level) and
`andybalholm/brotli v1.2.1` (level 6), against `compress/flate` level 6 from go1.27.0. Samples are
the ones `alloc_test.go` and `compress_test.go` already use.

| sample | raw | flate-6 | zstd | brotli-6 |
|---|---|---|---|---|
| 129 × `"a"` | 129 | **6** | 14 | 10 |
| repetitive 440 B (`"abcdefghij " × 40`) | 440 | **18** | 34 | 20 |
| shuffled alphabet 350 B (incompressible) | 350 | 357 | 365 | **244** |
| json-ish 207 B (`{"name":"value","x":1},` × 9) | 207 | **29** | 45 | 30 |

Two things follow.

**flate wins where this store lives.** `defaultCompressionThreshold = 128`
(`compress_options.go:10`), so the store compresses *short* strings — precisely where zstd's and
brotli's frame headers cost the most.

**Only flate can reach `headerInlinedCompressedString`.** That branch fires when the compressed form
is ≤ `maxInlineBytes` = 7 bytes (`store.go:15`, `store.go:515`). go1.27's flate rewrite made it
reachable for the first time; zstd's smallest output here is 14 bytes and brotli's is 10. Under
either alternative the branch is dead code and every compressed value takes an arena slot.

So the honest case for pluggability:

- **speed** — `Get` on a compressed value is inflate-bound; zstd and s2 decompress several times
  faster than flate.
- **trained dictionaries at scale** — zstd dictionaries are far more effective than DEFLATE preset
  dicts across many small, similar payloads. An OpenAPI corpus is exactly that shape, and
  `WithCompressionDict` (`compress_options.go:120`) already documents the train-offline/inject-per-
  generation lifecycle.
- **larger values** — raise `compressionThreshold` and zstd/brotli pull ahead.
- **avoiding a second codec** for callers who already link zstd.

Brotli's 244 B on incompressible input is genuinely better than flate's 357 B, but since commit
`2516540` `putCompressedString` keeps the raw 350 bytes in that case anyway (`store.go:509-512`), so
that column does not matter.

## 2. What is already abstract

The hot paths in `compress.go` name no concrete type. They use two interfaces plus `io.Copy`:

```go
// compress_options.go:46-54
type flateReader interface {
	io.ReadCloser
	flate.Resetter                 // Reset(io.Reader, dict []byte) error
}

type flateWriter interface {
	io.WriteCloser
	Reset(io.Writer)
}
```

Concrete flate appears in exactly **two** construction sites:

- `compress_options.go:69` — `flate.NewWriterDict(&wrt, co.compressionLevel, co.dict)`
- `pools.go:87` — `flate.NewReader(&dummyReader).(flateReader)`

The repo also already has the plugin-module pattern to copy: `json/lexers/yaml-lexer/go.mod` is a
separate module requiring `github.com/go-openapi/core/json v0.0.3` with `replace ... => ../..`. And
`json/stores/doc.go` already states the intent: *"Package contrib holds possible alternate
implementations."*

## 3. The one interface mismatch

Checked against the real APIs in the module cache:

| | writer `Reset` | reader `Reset` |
|---|---|---|
| `compress/flate` | `Reset(io.Writer)` | `Reset(io.Reader, []byte) error` |
| `zstd.Encoder`/`Decoder` (klauspost) | `Reset(io.Writer)` ✅ | `Reset(io.Reader) error` — no dict |
| `brotli.Writer`/`Reader` (andybalholm) | `Reset(io.Writer)` ✅ | `Reset(io.Reader) error` — no dict |

`flateWriter` already matches both alternatives unchanged. The reader differs only in the dict
argument: zstd binds dictionaries at construction (`zstd.WithDecoderDicts`), and so does brotli.

**Fix:** bind the dict when the reader is built, drop it from `Reset`. That is the whole interface
change. Note `zstd.Decoder` exposes `IOReadCloser()` rather than implementing `Close() error`
directly, so a codec adapter has one line of shimming.

## 4. What is flate-specific and must move behind the interface

| what | where | why it is codec-specific |
|---|---|---|
| `compressionRatio(level)` | `compress.go:7-34` | table tuned for DEFLATE on text; sizes every scratch buffer |
| `compressBound(size)` + stored-block constants | `compress_options.go:22-37` | pure DEFLATE stored-block framing (5-byte header, 65535-byte blocks) |
| `minCompressedSize = 6` | `compress_options.go:20` | shortest DEFLATE stream go1.27 emits |
| level clamping to −2..9 | `compress_options.go:82` | `flate.HuffmanOnly`..`BestCompression`; zstd is 1..4 in klauspost's enum, brotli 0..11 |
| `poolOfFlateReaders` | `pools.go:79-96` | package global with `flate.NewReader` baked into its `New` |

## 5. The three hard problems

### 5.1 gob identity (the real risk)

`encodableCompressionOptions` (`gob.go:73-77`) carries threshold, level and dict — **no codec name**.
Arena bytes are codec-specific, so a zstd-written store loaded under flate panics in
`assertCompressInflateError` (`guards.go:28`), or silently returns garbage with guards off.

Adding a `codec Codec` field to `options` does not work directly: `options` is gob-encoded
(`gob.go:43-71`), and an interface field needs `gob.Register` plus the *decoding* side importing the
concrete codec module — which defeats the separate-module goal.

What it needs instead:

- a name → factory registry in the core,
- a codec name string in the encoded payload,
- a clear error when the named codec is not linked into the binary,
- a format-version bump, since existing payloads carry no such field.

### 5.2 Pool ownership

Today one package-global flate reader pool (`pools.go:79`). With N codecs, either the core keeps a
`map[string]*pool` (a lock on a hot path, and the core must know how to construct each codec) or each
codec module owns its pooling behind the interface. The second is right, but it means the allocation
guarantees pinned down in `TestStoreAllocations` become **per-codec**: the core can only assert them
for built-in flate, and a careless plugin allocates silently. State the contract in the interface doc
— "borrow scratch from a pool; `Get` on a compressed value must cost exactly one allocation".

### 5.3 Codec/arena mismatch on a recycled store

`Store.Reset` (`store.go:368-371`) rewinds options to `optionsWithDefaults(nil)`. A codec field would
follow the existing dict rule — reconfigure at `BorrowStore` time — but the failure mode is worse
than a wrong dict: a wrong codec against leftover arena bytes corrupts data rather than merely
failing to decode. Needs to be loud in the doc comment.

## 6. Design decision to settle early: block vs stream

The current abstraction is stream-shaped, but the store's actual usage is block-shaped — whole
strings, mostly 128 B to a few KB. zstd's `EncodeAll`/`DecodeAll` and the s2/lz4 block APIs skip
stream framing and run faster.

But `WriteTo` needs an `io.Reader` for `writers.StoreWriter.StringCopy` (`store.go:274-284`), and
wrapping a block result in `bytes.NewReader` would give back the zero-allocation streaming that
commit `2516540` just established (`inflateSession`, `compress.go:119-155`).

So the interface probably needs both operations, with the streaming reader optional and a documented
fallback for codecs that only do blocks.

Sketch, not settled:

```go
type Codec interface {
	Name() string                                  // gob identity, see §5.1
	ClampLevel(level int) int                      // codec-specific range
	Bound(size int) int                            // worst-case compressed size
	Ratio(level int) int                           // scratch-sizing hint

	Compress(dst, src []byte) []byte
	Decompress(dst, src []byte) []byte             // panics like the rest of the store
	DecompressReader(src []byte) (io.ReadCloser, func())  // optional; nil → bytes.Reader fallback
}
```

## 7. Recommended phasing

**Phase one ⬜ — the seam, no registry.**
Add `WithCodec` against a small exported interface, keep flate as the default, ship no plugin module
and no registry. A store that is never gob-round-tripped needs no codec identity, so this needs no
format change and no version bump. Lets a zstd codec be prototyped out-of-tree against a real
interface before the API is committed to.

**Phase two ⬜ — identity, gated on someone needing it.**
Registry, codec name in the gob payload, format version bump, clear error for a missing codec. Only
worth doing when `MarshalBinary` with a non-default codec is actually wanted.

**Phase three ⬜ — first plugin module.**
`json/stores/codecs/zstd` with its own `go.mod`, mirroring `json/lexers/yaml-lexer`.

## 8. Work breakdown (estimate: 1–2 focused days for phases one + three)

| item | rough size |
|---|---|
| `json/stores/codecs` package: interface + doc contract | ~150 lines |
| rework `compress_options.go`, `compress.go`, `pools.go` to route through it | ~200 lines changed, mostly mechanical |
| gob format version + registry lookup + error (phase two) | ~60 lines |
| `json/stores/codecs/zstd` plugin module + tests | ~150 lines |
| parameterize `compress_test.go` / `compress_dict_test.go` over codecs | — |
| keep `TestStoreAllocations` pinned to flate (see §5.2) | — |

## 9. Open questions

- ⬜ Does `WithCompressionLevel` keep flate's numeric range as the public contract (and each codec
  maps it), or does it become codec-defined? Numeric levels do not port: −2..9 vs 1..4 vs 0..11.
- ⬜ Should `ConcurrentStore` hold one codec instance under the write lock
  (`concurrent_store.go`, compression happens only there), or one per operation from a pool?
- 🔬 Is the zstd decompression speedup real at 128 B–2 KB payloads, or does frame parsing eat it?
  Measure before committing — §1 says ratio will not justify this, so speed has to.
- ⬜ Worth exposing `s2`/`lz4` at all, or is zstd the only alternative anyone will ask for?
- ⬜ Does `headerInlinedCompressedString` stay flate-only by design (documented), or should the
  handle record which codec produced it? Recording it costs header bits and there are only 3 free
  values left of 16 (`header.go`).

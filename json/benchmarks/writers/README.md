# Writer benchmarks

Comparative benchmarks for the in-repo `default-writer` implementations against
[mailru/easyjson](https://github.com/mailru/easyjson)'s `jwriter`, the writer our
design is inspired from.

## What is compared

| name                | implementation                                  | mode                            |
|---------------------|-------------------------------------------------|---------------------------------|
| `our-unbuffered`    | `default-writer.Unbuffered`                     | streams every token to `io.Writer` |
| `our-buffered`      | `default-writer.Buffered` (default 4 KiB)       | streams via a pooled buffer + `Flush` |
| `our-buffered-2MB`  | `default-writer.Buffered` (`WithBufferSize(2 MiB)`) | holds the whole doc, single final `Flush` (mimics easyjson; trade memory for speed) |
| `our-yaml`          | `default-writer.YAML`                           | emits YAML (different output)   |
| `easyjson`          | `jwriter.Writer` (`NoEscapeHTML: true`)         | builds the whole doc in memory, then `DumpTo` |

## Methodology

Each real-world corpus document (`workloads/testdata`, the same corpus as the lexer
benchmarks) is lexed **once**, outside the timed loop, into a stable slice of tokens
with separators included (`WithElideSeparator(false)`). The benchmark then replays
that identical token stream through each writer, so only the cost of *producing*
output is measured — not lexing.

`b.SetBytes` is set to the number of bytes each writer emits, so `MB/s` is output
throughput. easyjson is configured with `NoEscapeHTML: true` to match our writers'
escaping rules (control characters, quote and backslash only).

`TestReplayRoundTrip` validates that the JSON writers (ours + easyjson) reproduce a
document semantically equal to the original. The YAML writer emits YAML rather than
JSON, so it is benchmarked but excluded from the round-trip check.

## Running

```sh
# correctness check
go test ./...

# full benchmark
go test -run '^$' -bench BenchmarkWriters -benchmem

# a single dataset
go test -run '^$' -bench 'BenchmarkWriters/twitter_status' -benchmem
```

## Interpreting the results

Steady-state medians (6×, `benchstat`, AMD Ryzen 7 5800X, `io.Discard` sink):

| dataset          | our-buffered | easyjson  | buffered vs easyjson | allocs (ours / easyjson) |
|------------------|-------------:|----------:|---------------------:|--------------------------|
| canada_geometry  |  1109 MiB/s  | 946 MiB/s | **+17%**             | 0 / 19                   |
| citm_catalog     |   516 MiB/s  | 395 MiB/s | **+31%**             | 0 / 27                   |
| golang_source    |   600 MiB/s  | 497 MiB/s | **+21%**             | 0 / 72                   |
| twitter_status   |   484 MiB/s  | 403 MiB/s | **+20%**             | 0 / 26                   |

- `our-buffered` streams to an `io.Writer` with a reused pooled buffer, is
  **allocation-free** (`0 B/op`), and is now **faster than easyjson on every corpus
  document** — including the number-heavy `canada_geometry`. easyjson allocates the
  entire output document in memory each run (250 KiB – 1.85 MiB per op): it is an
  in-memory builder, not a streaming writer. The lead comes from de-genericizing the
  structural hot path (see `internal/writegen`): lifting `commonWriter[T]`'s methods
  onto the concrete `*Buffered` receiver lets the compiler inline `writeSingleByte`,
  which it cannot do through a generic type-parameter dictionary call.
- `our-unbuffered` writes straight through to the target `io.Writer`, holding nothing.
  It is also **0 allocs** against a plain `io.Writer` (a reusable single-byte scratch
  field avoids the per-write boxing). It pays a real call per flush, so on string- and
  struct-heavy data it trails buffered/easyjson; on `canada_geometry` it still edges
  easyjson out (999 vs 946 MiB/s). Use it for a true streaming sink (file/socket) where
  holding the document is undesirable.
- `our-yaml` produces YAML (larger output, indentation + structural bookkeeping), so its
  `MB/s` is not directly comparable to the JSON writers — it is included to track its own
  throughput. Still **0 allocs/op**.
- easyjson shows high run-to-run variance (±7–10%) because it reallocates the whole
  document each iteration; our writers are tight (±1–5%).

### Caveat: the sink is `io.Discard`

The benchmark writes to `io.Discard`, whose `Write` is a no-op. Flushing is therefore
free, so `our-buffered-2MB` (one flush) performs essentially the same as the default
`our-buffered` (many free flushes): against this sink, buffer size only changes flush
*frequency*, which costs nothing. The large-buffer config is where the memory-for-speed
trade pays off against a *real* sink (file, socket, gzip), where each flush is a syscall:
there the 4 KiB buffer pays N writes and the 2 MiB buffer pays one. To compare raw
per-token CPU against easyjson on equal footing, both build the whole document before the
single (free) dump — so the remaining gap is hot-path processing, not buffering.

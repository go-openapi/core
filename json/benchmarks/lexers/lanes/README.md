# JSON lexer benchmarks

Throughput and allocation comparison of the in-repo `default-lexer` — the semantic
lexer `L` and the verbatim lexer `VL` — against two external JSON tokenizers:

- [`mailru/easyjson`](https://github.com/mailru/easyjson) `jlexer` — a pull-style,
  `[]byte`-only lexer; the original inspiration for `default-lexer`.
- [`go-json-experiment/json`](https://github.com/go-json-experiment/json) `jsontext`
  (`encoding/json/v2`) — a fully RFC 8259-validating streaming tokenizer; the
  closest peer to `L`.

Each real-world corpus document (canada / citm / twitter / golang) is tokenized
end-to-end; `b.SetBytes` is the input size, so the reported MB/s is input
throughput.

```sh
go test -run '^$' -bench BenchmarkLexers -benchmem .
```

`TestWalkersAgree` sanity-checks that all four tokenizers accept every corpus
document.

## Chart

A rendered throughput chart (median of 6 runs, four tokenizers side by side per
workload) lives in [`benchviz/`](benchviz/) — see [`benchviz/README.md`](benchviz/README.md)
for the picture, the allocation table, and how to regenerate it.

This module is self-contained (its own `go.mod`) so the easyjson and json/v2
dependencies stay out of the main `json` module.

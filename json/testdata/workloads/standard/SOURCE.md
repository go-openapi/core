# Vendored JSON benchmark corpus

These are canonical JSON benchmark documents, widely used to compare JSON
parsers (nativejson-benchmark, simdjson, easyjson, encoding/json/v2, ...).

They are vendored here gzip-compressed (decompressed in-memory via the standard
library `compress/gzip`, so no extra dependency is pulled in) from the
`encoding/json/v2` reference implementation test corpus:

- Upstream: https://github.com/go-json-experiment/json
  (`internal/jsontest/testdata`, originally `.zst`; re-compressed to `.gz` here)
- License: BSD-style, Copyright The Go Authors (see that repository's LICENSE).

| file | shape |
|------|-------|
| `canada_geometry.json.gz` | number-heavy (geo coordinates) |
| `citm_catalog.json.gz` | objects and strings (real-world catalog) |
| `twitter_status.json.gz` | unicode-rich strings (social API payload) |
| `golang_source.json.gz` | deeply structured (Go AST dump) |

## Updating

Re-fetch from a fresh checkout of the upstream corpus and re-compress:

    zstd -dc <name>.json.zst | gzip -9 > <name>.json.gz

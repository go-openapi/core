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

## Extended corpus (simdjson-go)

To measure the lexer on "untrained" ground — payloads it was NOT tuned against,
especially string-value-heavy documents the parity 4-set under-represents — the
following are vendored from the [minio/simdjson-go](https://github.com/minio/simdjson-go)
testdata (originally `.zst`, re-compressed to `.gz`). The %≥32 column is the
fraction of bytes living in string *values* of length ≥ 32 (the AVX2-string prize
metric; see the plan §9.3 / ramblings).

| file | shape | %bytes in str-values ≥32 |
|------|-------|--------------------------|
| `gsoc-2018.json.gz` | huge text fields (GSoC dump) | 80% |
| `github_events.json.gz` | event payloads, long text/URLs | 52% |
| `update-center.json.gz` | Jenkins plugin metadata | 37% |
| `apache_builds.json.gz` | build logs (32–63 B strings) | 37% |
| `twitterescaped.json.gz` | twitter with escaped unicode | 29% |
| `payload-large.json.gz` | mixed API payload | 22% |
| `random.json.gz` | mixed random | 2.5% |
| `marine_ik.json.gz` | number/structure-heavy | ~0% |
| `mesh.json.gz` | number-heavy (3D mesh) | 0% |
| `mesh.pretty.json.gz` | mesh, pretty-printed (whitespace) | 0% |
| `numbers.json.gz` | pure number array | 0% |
| `instruments.json.gz` | small structured | 0% |

Excluded: `parking-citations` (NDJSON — multiple top-level objects, not a single
JSON document); `payload-small`/`payload-medium` (< 1 KB, too small to benchmark);
`canada`/`citm_catalog`/`twitter` (duplicates of the parity set above).

## OpenAPI / go-openapi specs (long-description workload)

The lexer's low-memory design targets slurping large OpenAPI/Swagger specs, whose
string *values* (descriptions, documentation) are long while object keys stay
short — the profile the AVX2 long-string gate (plan §9.3) is built for. These two
combine every Azure Network Swagger fixture from
[`go-openapi/analysis`](https://github.com/go-openapi/analysis)
(`internal/testintegration/fixtures/azure`, 16 specs) into one document, keyed by
filename, so a single workload exercises a realistic mammoth spec:

| file | shape | %bytes in str-values ≥32 |
|------|-------|--------------------------|
| `azure_swagger.json.gz` | 16 Azure specs merged, compact | 30% |
| `azure_swagger.pretty.json.gz` | same, indented (whitespace-heavy) | 30% |

Regenerate with the merge script in the plan's ramblings, or:

    python3 -c 'import json,glob,gzip; d={f:json.load(open(f)) for f in sorted(glob.glob("*.json"))}; gzip.open("azure_swagger.json.gz","wb").write(json.dumps(d,separators=(",",":")).encode())'

## Updating

Re-fetch from a fresh checkout of the upstream corpus and re-compress:

    zstd -dc <name>.json.zst | gzip -9 > <name>.json.gz

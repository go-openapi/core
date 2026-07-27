# Vendored JSON Test Suite

These fixtures are vendored from Nicolas Seriot's **JSONTestSuite**:

- Upstream: https://github.com/nst/JSONTestSuite
- Companion article: https://seriot.ch/projects/parsing_json.html
  (security analysis: https://seriot.ch/security/parsing_json.html)
- Vendored revision: `1ef36fa`
- License: MIT (see `LICENSE` in this directory)

## File naming convention (`test_parsing/`)

- `y_*` — **must be accepted** by a conforming parser.
- `n_*` — **must be rejected** by a conforming parser.
- `i_*` — **implementation-defined**; either outcome is conformant. Our chosen
  behavior for each `i_` case is recorded in the conformance test's expectation
  table so the suite stays deterministic.

`test_transform/` holds documents whose *transformed/normalized* output is the
subject (number/string canonicalization). Used by the canonicalization phase.

## Updating

Re-copy from a fresh checkout of the upstream repo and bump the revision above.
Do not edit the fixtures in place.

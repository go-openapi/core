# Vendored YAML Test Suite

Fixtures vendored from the community **YAML Test Suite**:

- Upstream: https://github.com/yaml/yaml-test-suite
- Vendored revision: `da267a5c` (2025-12-25, `main` branch)
- License: MIT (see `LICENSE` in this directory)

## Layout

`src/*.yaml` — each file is a YAML *sequence* of one or more related test cases
sharing an id (the file name). Only the first case in a file carries `name`,
`from` and `tags`; later cases inherit them.

Fields we consume:

- `yaml` — the input document (see "Special characters" below).
- `json` — the equivalent JSON. Present when the document has a JSON
  representation; this is the expectation our lexer is measured against, since
  `YL` deliberately trims YAML down to JSON semantics.
- `fail: true` — the document is invalid and must be rejected.

Fields we ignore: `tree` (event stream), `dump`/`emit` (canonical YAML output),
`from`, `also`, `note` — they describe what a *YAML emitter* must do, not a
lexer reduced to the JSON data model.

A case with neither `json` nor `fail` is valid YAML with no JSON equivalent
(non-string keys, tags, aliases to non-JSON structures, …). Those are
implementation-defined for us and only have their behavior recorded.

## Special characters

The suite writes otherwise-invisible characters as visible ones. They MUST be
translated before use (upstream `bin/YAMLTestSuite.pm`, `sub unescape`):

| in the fixture | means |
|---|---|
| `␣` | a trailing space |
| `»`, optionally preceded by em-dashes (`—»`, `——»`, `———»`) | a tab |
| `←` | a carriage return |
| `⇔` | a byte order mark (U+FEFF) |
| `↵` | marks a trailing newline; removed |
| `∎` at end of input | the input has NO final newline; the mark and its newline are removed |

Getting this wrong is silent: a case that hinges on a trailing space simply
becomes a different (usually valid) document.

## Updating

Re-copy `src/*.yaml` from a fresh checkout and bump the revision above. Do not
edit the fixtures in place.

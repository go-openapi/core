# lexers

This package exposes an interface `Lexer` and a token type `token.T` to parse JSON input.

Notice that the same interface may be used to parse `YAML` or any other serialization format
that converts to JSON (e.g. a hierarchy of scalars, arrays and dictionaries).

Several lexers implementations are proposed:

* [x] a semantic JSON lexer
* [x] a verbatim JSON lexer, that preserves blank space and unicode escaped sequences
* [x] a YAML lexer
* [ ] a ND-JSON lexer (TODO)

Acknowlegements and credits
===========================

Our JSON lexer is an original research and development: it is not a copy of another software.

However, we are greatly grateful to the following projects that have provided inspiration,
astute methods and algorithms that we could study, reproduce or try to improve further.

* https://github.com/mailru/easyjson (MIT) - The first pionner for a "fast" low-level JSON encoder/decoder
* https://github.com/go-json-experiment/json (BSD 3-Clause) - go standard library encoding/json/v2
* https://github.com/simdutf/simdutf (MIT+Apache) - extremely fast C++ UTF-8 validator
* https://github.com/minio/simdjson-go (Apache) - pionnered go & assembly with a go port of simdjson

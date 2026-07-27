# lexers

This package exposes an interface `Lexer` and a token type `token.T` to parse JSON input.

Notice that the same interface may be used to parse `YAML`, or any other serialization format
that converts to JSON (e.g. a hierarchy of scalars, arrays and dictionaries).

Several lexers implementations are proposed:

* a semantic JSON lexer
* a verbatim JSON lexer
* a ND-JSON lexer
* a YAML lexer

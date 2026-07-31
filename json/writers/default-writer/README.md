# Default JSON writers

## Functionality

A JSON writer implements one or several [writer interfaces](https://github.com/go-openapi/core/blob/master/json/writers/writers.go).

These interfaces build a stream of JSON output into some underlying `io.Writer` from JSON token, JSON values, Store values or native go values.

This package exposes several such writers:
* an unbuffered writer
* a buffered writer
* an indented writer (to output "pretty JSON") - buffered -
* a YAML writer, that outputs JSON tokens and values as a YAML document

TODOs:

* [] YAML output is not great, because at this moment, strings remain JSON strings, without accounting for YAML escaping rules.

## UTF-8

RFC 8259 §8.1 requires JSON text to be UTF-8. `WithUTF8Policy` selects what happens to caller-supplied data that
is not:

| policy | behavior |
|---|---|
| `UTF8Strict` (default) | refuse it: the writer errors with `ErrInvalidUTF8` and short-circuits |
| `UTF8Replace` | substitute U+FFFD, one per invalid byte — the writers' behavior before the policy existed |
| `UTF8Passthrough` | write the bytes through unchecked (produces invalid JSON; for pre-validated data) |

`Unbuffered` takes `WithUnbufferedUTF8Policy`; `Indented` and `YAML` take it through
`WithIndentBufferedOptions` / `WithYAMLBufferedOptions`. The substitution rule is shared with the lexers
(`json/internal/utf8x`), so a value sanitized on the way in and one sanitized on the way out are the same bytes.

Go strings may legally hold ill-formed UTF-8, so `String()` and `StringBytes()` can trip the default on data from a
non-Unicode source. That is the intended change: silently corrupting a payload is worse than refusing it.

### What is checked

The policy applies to what the **caller** supplies: `String`, `StringBytes`, `StringRunes`, `StringCopy`, `Raw`,
`RawCopy`. Streaming entry points validate the stream as a whole, not chunk by chunk, so a sequence split across two
reads is not mistaken for a fault and a fault spanning the boundary is not missed.

It does **not** apply to values arriving in a lexer token (`Token`, `VerbatimToken`): the lexers already validate
string values, and re-checking them would cost the token-copy path its reason for existing. The loophole that leaves
— and it is deliberate — is that a document lexed with `UTF8Passthrough` can carry ill-formed bytes into the writer,
where `Token` silently substitutes U+FFFD (the escaper decodes as it goes) and `VerbatimToken` writes the bytes out
byte-for-byte, as verbatim requires. `VerbatimValue` is not in that group: it renders through the ordinary string
path and is checked. Numbers (`NumberBytes`, `NumberCopy`) are unchecked, as their documentation already says.

## Performance

* Allocations

The exposed writers generally amortize all internal allocations. So most standard benchmarks would show up zero allocation.

There is an exception: when writing numerical values using the `Number()` method with types
from the `math/big` standard library, serialization occurs using their `AppendText` method.

Since `math/big` is not optimized for zero-allocation, there are a few buffered allocated internally by this library.

TODOs:

* [] there are still some learnings to be learnt from easyjson/jwriter


## Background and credits

This JSON writer has been largely inspired by the work from https://github.com/mailru/easyjson.

We've kept the concept of a writer to proces JSON tokens and escape strings, 
very much like in https://github.com/mailru/easyjson/blob/master/jwriter/writer.go.

However, this implementation introduces a few significant differences:

  * several implementations of the writers interfaces may be proposed, possibly optimized for different use-cases
  * unlike the easyjson version, we don't want to support complex types such as objects or arrays, only scalar values
  * this implementation supports the types defined to support JSON et JSON tokens by the other modules exposed
    in [github.com/go-openapi/core](https://github.com/go-openapi/core)
  * this makes the writer suitable for:
    * writing directly tokens produced by a [github.com/go-openapi/core/json/lexers/Lexer](https://github.com/go-openapi/core/blob/master/json/lexers/lexers.go)
    * writing values stored in a [github.com/go-openapi/core/json/stores/Store](https://github.com/go-openapi/core/blob/master/json/stores/stores.go)
    * writing JSON types defined in [github.com/go-openapi/core/json/types](https://github.com/go-openapi/core/blob/master/json/types/types.go)
  * the idea of a "chunked buffer" has been revisited and reimplementented. It may or may not be a good option, depending on the use-case.
    So we propose an unbuffered alternative.
  * this implementation leverages memory pools more systematically

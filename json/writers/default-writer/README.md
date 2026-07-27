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

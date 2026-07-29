# UTF-8 validation

1. We need the JSON lexer to be able to valide UTF-8 sequences:
   invalid UTF-8 should not leak in output strings
2. The default behavior of L and VL is to error on such invalid input
3. As an option (to both lexers), the error can be swallowed and the invalid UTF-8 sequence
   be in this case mangled as U+FFFD (error rune)
4. For VL, we pledge _verbatim_. This is important to maintain original offset/positioning.
   But this pledge is broken when mangling an invalid sequence. TO BE DECIDED.

Classic algorithm for invalid sequence detection: https://nemanjatrifunovic.substack.com/p/decoding-utf-8-part-vii-validation.

Advanced SIMD-enabled C++ library for validating UTF-8: https://github.com/simdutf/simdutf
(available locally for convenient inspection at /home/fred/src/github.com/fredbi/simdutf).
I am worried that this new requirement wipes out our vast performance improvements with string scanning.

I think we should analyze the methods used at simdutf (e.g. function simdtuf::validate_utf8_with_errors).
We don't have to port their full support of AVX512 etc, just understand their general approach and
port it to our more modest SWAR & AV2 support.


6. Final point: recheck the conformance suite test and list all "implementation-dependent point"
   where L or VL don't pass the test, so we can get a more complete assessment of how many similar bugs
   remain hidden in our cupboard.

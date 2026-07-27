# Default JSON store

## Summary

The Default JSON Store is designed to efficiently pack values in memory,
allowing shared memory for JSON documents and their clones.

When a value is added, a `Handle` (a 64-bit integer) is returned as a reference,
which can either inline small values or point to larger ones stored in a separate slice.

The store uses various packing methods, including BCD encoding for numbers and DEFLATE compression for large strings.

It also optimizes storage for blank spaces in JSON.
The store supports serialization for later reuse through gob encoding.

## Goals

A JSON store aims to pack values in memory so that a JSON document
and its clones may share the same memory place.

## Principle

Whenever the caller `Put` a value, the `Store` returns a `Handle` as a reference for future use.

`Handle`s are not pointers, but `uint64` integers (so that's the size of a pointer on 64-bit systems).

The idea is that all values do not result in some memory being used by the `Store` but may be packed in the
`Handle` itself.

In that case, the `Store` will honor `Get` requests to restore the original value, but no extra memory is needed.

So the store may not really keep track of most small values: the conveyed `Handle` will.

The driving principle is to favor a lazy evaluation of values: packing data as much as possible early on,
then leaving up the extra CPU decoding work to callers at a later time,
on values that actually require to be resolved.

## Packing methods

The `Handle` is 64 bits large. We reserve a 4 bits header to determine the kind of encoding (16 possible methods).

The remaining 60 bits are splits in two different ways:

* either the value is inlined: 3 bits to store the length in bytes, and the remaining 56 bits to store the payload of up to 7 bytes
* or the value is stored separately in a single inner `[]byte` slice (the "arena"): 20 bits to store the length, 40 bits used for the 
  offset in the inner slice

This way, the adressable space in the arena is quite large with about 1TB. The length of a single value is also rather large (1 MB).

Numbers are always encoded in BCD (a modified version of the standard BCD, to accomodate for a decimal separator and 
the exponent (`e` or `E`) used in scientific notation.

There are reserved `Handle` values for `null`, ` true` and `false` so these never take additional memory beyond the `uint64` handle.

Inlined values support payloads of up to 7 bytes. So we may inline numbers with up to 14 digits (resulting in 7 BCD nibbles) or
strings of up to 7 bytes.

There is a special handling for ASCII only short strings. When a string is ASCII only, we may pack up to 8 original bytes
in the payload (each ASCII character is encoded on 7 bits).

Very large numbers or strings of moderate length do require memory allocation in the inner area.

Here we may apply DEFLATE compression for large strings (by default, more than 128 bytes).

At the moment, we don't apply DEFLATE compression for XXL numbers (dealing with more than 128 BCD nibbles should be a rare event).

## Packing blank space

Valid blank space characters in JSON are: blank (0x32), tab, carriageReturn and lineFeed.

Our default lexer interprets those as non-significant blank space that occur before a token.

```go
const (
	blank          = ' '
	tab            = '\t'
	lineFeed       = '\n'
	carriageReturn = '\r'
)
```

Any blank string is therefore an arbitrary mix of these 4 characters.
There is a potential to get a higher compression ratio than a standard DEFLATE.

* ascii-only string => up to 8 bytes may be packed in the inline 7-bytes payload
* blanks-only (4 values -> 2 bits) => 7x4=28 blank characters may be packed in the inline payload
* the length field requires 2 extra bits (4 -> 6 bits), so the payload is slightly reduced to 54 bits instead of 56:
  up to 27 blanks may be inlined.

That means that many ordinary JSON (i.e. with up to 27 indentation strings between tokens) would not require any extra-memory in the arena to store blanks.

* blank strings with a length greater than 27 bytes are compressed using DEFLATE and stored in an "arena" dedicated to such compressed blanks.


There are probably many special cases for optimizing the storage of blanks (e.g. "well-formed indentation: \n followed by n spaces").

### Getting blank as a `stores.Value`

Non-significant space is, er, not significant and therefore not really a value.

Since the `Store` leaves a lot to the caller, we'll return a `String` value and leave it to the caller to 
know when this is a blank.

The `VerbatimStore` supports `VerbatimValue` s, which keep both `Value` and blanks.

The `Write` call will know the difference and send a raw string instead of a JSON string.

Callers should be aware that verbatim tokens not holdings values (such as separators or EOF) may also come with
non-significant blank space. For these, the `VerbatimStore` may just store blanks with the `Blanks` method.

## Serialization

We might want to save a given store on disk for reuse at a later time.

The `Store` supports gob encoding with `MarshalBinary`/`UnmarshalBinary`.

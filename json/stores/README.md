# Stores

Packing JSON values in a reusable memory arena.

### Store handles

A `Store` holds the JSON scalar values in a JSON document.

It does not represent the structure of the document
(i.e. the hierarchy of JSON container types, such as objects and arrays).

Stores values may be retrieved later from the `Handle` (a 64 bit integer) provided by the `Store`.

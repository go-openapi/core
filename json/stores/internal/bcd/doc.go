// Package bcd is a codec for our internal modified BCD encoding scheme.
//
// This is modified standard 8-4-2-1 BCD to account for decimal point and scientific notation in JSON numbers.
//
// The codec knows how to transform an ASCII decimal representation (include {eE]{+|t}nnn) to a packed representation
// using half-byte BCD nibbles. The transform keeps the ASCII representation verbatim.
package bcd

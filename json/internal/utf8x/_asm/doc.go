// Package _asm is a generator-only module for the AVX2 UTF-8 validation kernel.
//
// It isolates the avo build-time dependency: only `go generate` (see ../validate_amd64.go) runs it, and the generated
// ../validate_amd64.s has no avo import, so the parent json module never pulls avo.
//
// Lives under a `_`-prefixed directory so the go tool ignores it when listing/building the parent module.
package main

// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

// Package _adm is a generator-only module for the AVX2 string-stop kernel. It isolates the avo
// build-time dependency: only `go generate` (see ../scan_amd64.go) runs this, and
// the generated ../stringstop_amd64.s has no avo import, so the parent json module
// never pulls avo.
// Lives under a `_`-prefixed directory so the go tool ignores it
// when listing/building the parent module.
package main

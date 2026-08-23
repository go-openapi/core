// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

// Package benchmarks hosts comparative benchmarks for the json building blocks.
//
// It is a separate Go module so that the heavy external dependencies pulled in
// by the reference implementations (standard library variants, mailru/easyjson,
// and others) never leak into the dependency graph of the core json module.
//
// The lexers subtree compares implementations of [github.com/go-openapi/core/json/lexers.Lexer]:
// the in-repo default-lexer against baselines built on top of third-party lexers.
package benchmarks

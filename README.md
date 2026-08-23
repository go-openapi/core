# core

<!-- Badges: status  -->
[![Tests][test-badge]][test-url] [![Coverage][cov-badge]][cov-url] [![CI vuln scan][vuln-scan-badge]][vuln-scan-url] [![CodeQL][codeql-badge]][codeql-url]
<!-- Badges: release & docker images  -->
<!-- Badges: code quality  -->
<!-- Badges: license & compliance -->
[![Release][release-badge]][release-url] [![CodeFactor Grade][codefactor-badge]][codefactor-url] [![License][license-badge]][license-url]
<!-- Badges: documentation & support -->
<!-- Badges: others & stats -->
[![GoDoc][godoc-badge]][godoc-url] [![Discord Channel][discord-badge]][discord-url] [![go version][goversion-badge]][goversion-url] ![Top language][top-badge] ![Commits since latest release][commits-badge]

---

Core openapi functionality: json, json schema &amp; OAI specs

## Status

Experimental. API is not stable yet. Releases v0.0.x are experimental and may bring some breaking changes.

## Content

- json: infrastructure to parse, dump JSON & YAML as compact immutable documents
- jsonschema: ser/deser jsonschema specifications + analyzers for codegen and validation (supports overlays)
- oai: ser/deser openapi specifications + analyzers (supports overlays)

> NOTES:
>
> - deser includes validation
> - json document + core implementation

## Change log

See <https://github.com/go-openapi/core/releases>

## Licensing

This library ships under the [SPDX-License-Identifier: Apache-2.0](./LICENSE).

See the license [NOTICE](./NOTICE), which recalls the licensing terms of all the pieces of software
on top of which it has been built.


## Other documentation

* [All-time contributors](./CONTRIBUTORS.md)
* [Contributing guidelines][contributing-doc-site]
* [Maintainers documentation][maintainers-doc-site]
* [Code style][style-doc-site]

## Cutting a new release

Maintainers can cut a new release by either:

* running [this workflow](https://github.com/go-openapi/core/actions/workflows/bump-release.yml)
* or pushing a semver tag
  * signed tags are preferred
  * The tag message is prepended to release notes

<!-- Badges: status  -->
[test-badge]: https://github.com/go-openapi/core/actions/workflows/go-test.yml/badge.svg
[test-url]: https://github.com/go-openapi/core/actions/workflows/go-test.yml
[cov-badge]: https://codecov.io/gh/go-openapi/core/branch/master/graph/badge.svg
[cov-url]: https://codecov.io/gh/go-openapi/core
[vuln-scan-badge]: https://github.com/go-openapi/core/actions/workflows/scanner.yml/badge.svg
[vuln-scan-url]: https://github.com/go-openapi/core/actions/workflows/scanner.yml
[codeql-badge]: https://github.com/go-openapi/core/actions/workflows/codeql.yml/badge.svg
[codeql-url]: https://github.com/go-openapi/core/actions/workflows/codeql.yml
<!-- Badges: release & docker images  -->
[release-badge]: https://badge.fury.io/gh/go-openapi%2Fcore.svg
[release-url]: https://badge.fury.io/gh/go-openapi%2Fcore
<!-- Badges: code quality  -->
[codefactor-badge]: https://img.shields.io/codefactor/grade/github/go-openapi/core
[codefactor-url]: https://www.codefactor.io/repository/github/go-openapi/core
<!-- Badges: documentation & support -->
[godoc-badge]: https://pkg.go.dev/badge/github.com/go-openapi/core
[godoc-url]: http://pkg.go.dev/github.com/go-openapi/core
[discord-badge]: https://img.shields.io/discord/1446918742398341256?logo=discord&label=discord&color=blue
[discord-url]: https://discord.gg/FfnFYaC3k5

<!-- Badges: license & compliance -->
[license-badge]: http://img.shields.io/badge/license-Apache%20v2-orange.svg
[license-url]: https://github.com/go-openapi/core/?tab=Apache-2.0-1-ov-file#readme
<!-- Badges: others & stats -->
[goversion-badge]: https://img.shields.io/github/go-mod/go-version/go-openapi/core
[goversion-url]: https://github.com/go-openapi/core/blob/master/go.mod
[top-badge]: https://img.shields.io/github/languages/top/go-openapi/core
[commits-badge]: https://img.shields.io/github/commits-since/go-openapi/core/latest
[RFC6901]: https://www.rfc-editor.org/rfc/rfc6901
<!-- Organization docs -->
[contributing-doc-site]: https://go-openapi.github.io/doc-site/contributing/contributing/index.html
[maintainers-doc-site]: https://go-openapi.github.io/doc-site/maintainers/index.html
[style-doc-site]: https://go-openapi.github.io/doc-site/contributing/style/index.html
  

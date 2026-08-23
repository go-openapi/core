// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package lexer_test

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"testing"

	jsonlexer "github.com/go-openapi/core/json/lexers/default-lexer"
	yamllexer "github.com/go-openapi/core/json/lexers/yaml-lexer"
)

// Conformance of YL against the vendored YAML Test Suite (testdata/yaml-test-suite/SOURCE.md).
//
// What "conformant" means here is narrower than for a YAML processor, and deliberately so: YL exists to read YAML
// **as JSON**, so the suite's `json` field — the JSON its input is equivalent to — is exactly our expectation. A case
// is measured by lexing the YAML with YL and the expected JSON with the JSON lexer L, then comparing the two token
// streams. Anything the JSON data model cannot express is not a failure, it is out of scope.
//
// Three buckets, mirroring the JSON suite's y_/n_/i_ split:
//
//   - `json` is ONE JSON document → MUST accept, and produce L's token stream for it.
//   - `fail: true`                 → MUST reject.
//   - anything else                → no single-root JSON equivalent: behavior is only recorded.
//
// That last bucket is not a weasel: a multi-document YAML stream has a `json` field holding several values in
// sequence, and an empty document has an empty one. Neither is a JSON token stream, so there is nothing to compare
// against — only YL's accept/reject to record, which the golden snapshot pins so it cannot drift unnoticed.
//
// Known divergences are listed in conformanceXFail so the suite stays green and guards against NEW regressions. That
// list is the point of this test: it is the honest inventory of where YL stands.

// conformanceModes are the ways a document is driven through YL. Both must agree with the expectation; a divergence
// between them is a bug regardless of what the suite says.
type conformanceMode struct {
	name string
	run  func(t *testing.T, data []byte) ([]fzTok, error)
}

func conformanceModes() []conformanceMode {
	opts := []yamllexer.Option{
		// bound the walk so a pathological fixture fails the test instead of taking the process down
		yamllexer.WithMaxContainerStack(512),
		yamllexer.WithMaxTokens(1 << 20),
	}

	return []conformanceMode{
		{
			name: "YL/bytes",
			run: func(t *testing.T, data []byte) ([]fzTok, error) {
				return fzDrain(yamllexer.NewWithBytes(data, opts...), conformanceBudget(data), t)
			},
		},
		{
			name: "YL/reader",
			run: func(t *testing.T, data []byte) ([]fzTok, error) {
				return fzDrain(
					yamllexer.New(bytes.NewReader(data), opts...),
					conformanceBudget(data),
					t,
				)
			},
		},
	}
}

func conformanceBudget(data []byte) int { return 16*len(data) + 4096 }

// jsonTokens lexes the suite's expected JSON with the JSON lexer, giving the token stream YL must reproduce.
func jsonTokens(t *testing.T, want string) ([]fzTok, error) {
	t.Helper()

	l := jsonlexer.NewWithBytes([]byte(want))
	var out []fzTok
	for tok := range l.Tokens() {
		out = append(out, fzRec(tok, l.IndentLevel()))
	}

	return out, l.Err()
}

func TestConformanceYAML(t *testing.T) {
	cases := loadSuite(t)
	xfail := conformanceXFail()
	modes := conformanceModes()

	var (
		seenXFail = map[string]bool{}
		// records holds the accept/reject verdict for every case we can only observe, snapshotted so a change in
		// out-of-scope behavior shows up as a reviewable diff instead of silence
		records = map[string]string{}
		// failures by suite tag, so the report says which YAML FEATURES we do not cover rather than just how many
		// cases are red
		failsByTag = map[string]int{}
		report     []string

		passAccept, passReject, recordOnly int
		xfailCount, unexpectedOK           int
	)

	for _, c := range cases {
		t.Run(c.label(), func(t *testing.T) {
			data := []byte(c.yaml)

			// every mode must agree with itself before it can be compared to the suite
			results := make([]conformanceVerdict, 0, len(modes))
			for _, m := range modes {
				toks, err := m.run(t, data)
				results = append(results, conformanceVerdict{mode: m.name, toks: toks, err: err})
			}
			for _, r := range results[1:] {
				if (r.err == nil) != (results[0].err == nil) {
					t.Errorf("%s and %s disagree on accept/reject: %v vs %v",
						results[0].mode, r.mode, results[0].err, r.err)
				}
			}
			got := results[0]

			switch {
			case c.fail:
				if got.err == nil {
					recordFailure(failsByTag, c)
					reportf(&report, "%s ACCEPTED an invalid document (%s)", c.label(), c.name)
					markXFail(t, c, xfail, seenXFail, &xfailCount,
						"accepted a document the suite marks invalid")

					return
				}
				passReject++

			case c.hasJSON:
				want, jerr := jsonTokens(t, c.json)
				if jerr != nil {
					// Not one JSON document: an empty expectation (an empty YAML document) or several values in
					// sequence (a multi-document stream). Our contract is a single JSON token stream, so there is
					// no comparison to make — record the verdict instead.
					recordOnly++
					recordVerdict(records, c, got.err)

					return
				}

				switch {
				case got.err != nil:
					recordFailure(failsByTag, c)
					reportf(
						&report,
						"%s REJECTED a valid document: %v (%s)",
						c.label(),
						got.err,
						c.name,
					)
					markXFail(t, c, xfail, seenXFail, &xfailCount, "rejected: "+got.err.Error())
				case !fzToksEqual(got.toks, want):
					recordFailure(failsByTag, c)
					reportf(
						&report,
						"%s token stream differs from the expected JSON (%s)",
						c.label(),
						c.name,
					)
					markXFail(
						t,
						c,
						xfail,
						seenXFail,
						&xfailCount,
						"token stream differs:\n  yaml: "+summarize(
							got.toks,
						)+"\n  json: "+summarize(
							want,
						),
					)
				default:
					passAccept++
					if xfail[c.label()] != "" {
						seenXFail[c.label()] = true
						unexpectedOK++
						t.Errorf(
							"now conforms but is listed in conformanceXFail: remove %q",
							c.label(),
						)
					}
				}

			default:
				recordOnly++
				recordVerdict(records, c, got.err)
			}
		})
	}

	for label := range xfail {
		if !seenXFail[label] {
			t.Errorf("conformanceXFail lists %q but it was not found/exercised", label)
		}
	}

	sort.Strings(report)
	t.Logf("=== divergences (%d) ===", len(report))
	for _, line := range report {
		t.Logf("  %s", line)
	}

	tags := make([]string, 0, len(failsByTag))
	for tag := range failsByTag {
		tags = append(tags, tag)
	}
	sort.Slice(tags, func(i, j int) bool {
		if failsByTag[tags[i]] != failsByTag[tags[j]] {
			return failsByTag[tags[i]] > failsByTag[tags[j]]
		}

		return tags[i] < tags[j]
	})
	t.Logf("=== divergences by suite tag (which YAML features we do not cover) ===")
	for _, tag := range tags {
		t.Logf("  %-24s %d", tag, failsByTag[tag])
	}

	t.Logf(
		"=== summary: accept+match=%d, reject=%d, record-only=%d, xfail=%d, unexpected-pass=%d, total=%d ===",
		passAccept,
		passReject,
		recordOnly,
		xfailCount,
		unexpectedOK,
		len(cases),
	)

	checkRecordedGolden(t, records)
}

// recordVerdict notes what YL did with a case we cannot measure against a JSON token stream.
func recordVerdict(records map[string]string, c suiteCase, err error) {
	verdict := "accept"
	if err != nil {
		verdict = "reject"
	}
	records[c.label()] = verdict + "\t" + c.name
}

type conformanceVerdict struct {
	mode string
	toks []fzTok
	err  error
}

func recordFailure(byTag map[string]int, c suiteCase) {
	for _, tag := range c.tags {
		byTag[tag]++
	}
	if len(c.tags) == 0 {
		byTag["(untagged)"]++
	}
}

func reportf(report *[]string, format string, args ...any) {
	*report = append(*report, fmt.Sprintf(format, args...))
}

// markXFail turns a divergence into a hard failure unless it is a known one.
func markXFail(
	t *testing.T,
	c suiteCase,
	xfail map[string]string,
	seen map[string]bool,
	count *int,
	detail string,
) {
	t.Helper()

	if _, known := xfail[c.label()]; known {
		seen[c.label()] = true
		*count++
		t.Logf("xfail (known): %s — %s", c.label(), detail)

		return
	}

	t.Errorf("conformance divergence: %s (%s)\n  %s", c.label(), c.name, detail)
}

// summarize renders a token stream compactly enough to read in a failure message.
func summarize(toks []fzTok) string {
	const maximum = 12

	parts := make([]string, 0, maximum)
	for i, tk := range toks {
		if i == maximum {
			parts = append(parts, fmt.Sprintf("... (%d more)", len(toks)-maximum))

			break
		}
		if tk.val != "" {
			parts = append(parts, fmt.Sprintf("%v(%q)", tk.kind, tk.val))

			continue
		}
		parts = append(parts, fmt.Sprint(tk.kind))
	}

	return strings.Join(parts, " ")
}

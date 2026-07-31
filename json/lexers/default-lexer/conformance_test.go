package lexer

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Conformance tests against the vendored JSONTestSuite (see ../testdata/JSONTestSuite/SOURCE.md).
//
// Naming convention of test_parsing/:
//   - y_*  must be accepted
//   - n_*  must be rejected
//   - i_*  implementation-defined (we only record our behavior)
//
// Each document is run through several lexer configurations ("modes") so that buffer-boundary handling in streaming
// mode is exercised too.

func TestConformanceParsing(t *testing.T) {
	dir := filepath.Join(currentDir(), "..", "testdata", "JSONTestSuite", "test_parsing")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("cannot read conformance fixtures: %v", err)
	}

	modes := conformanceModes()
	knownFailures := conformanceXFail()

	var (
		iReport      []string // recorded behavior for i_ cases
		seenXFail    = map[string]bool{}
		passYes      int
		passNo       int
		xfailCount   int
		iCount       int
		unexpectedOK int
	)

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}

		raw, rerr := os.ReadFile(filepath.Join(dir, name))
		if rerr != nil {
			t.Fatalf("cannot read %s: %v", name, rerr)
		}
		// A fixture's exact bytes ARE the test, so a checkout that translated them must not change the verdict:
		// see normalizeLineEndings and TestConformanceFixturesAreStoredWithLF.
		data := normalizeLineEndings(raw)
		maximum := len(data) + 16 // generous upper bound on token count

		var want byte
		switch {
		case strings.HasPrefix(name, "y_"):
			want = 'y'
		case strings.HasPrefix(name, "n_"):
			want = 'n'
		default:
			want = 'i'
		}

		t.Run(name, func(t *testing.T) {
			// Aggregate verdicts across modes; a file "conforms" only if every mode agrees with the expectation.
			conforms := true
			detail := make([]string, 0, len(modes))
			byMode := make(map[string]bool, len(modes))
			for _, m := range modes {
				v := m.run(data, maximum)
				detail = append(detail, m.name+"="+verdictStr(v))
				byMode[m.name] = v.accepted

				switch want {
				case 'y':
					if !v.accepted {
						conforms = false
					}
				case 'n':
					if v.accepted {
						conforms = false
					}
				}
			}

			// UTF8Replace may only turn a rejection into an acceptance, and only for the ill-formed-UTF-8 /
			// broken-escape cases it is designed to sanitize. Anything else means replacement relaxed the grammar.
			if byMode["L/replace"] != byMode["L/bytes"] {
				switch {
				case !byMode["L/replace"]:
					t.Errorf("UTF8Replace REJECTED a document the default policy accepts: %s\n  %s",
						name, strings.Join(detail, " "))
				case !conformanceUTF8Relaxed()[name]:
					t.Errorf("UTF8Replace accepted %s, which the default policy rejects, "+
						"but it is not a known ill-formed-UTF-8 case: add it to conformanceUTF8Relaxed "+
						"or fix the relaxation\n  %s", name, strings.Join(detail, " "))
				}
			} else if conformanceUTF8Relaxed()[name] {
				t.Errorf(
					"conformanceUTF8Relaxed lists %q but UTF8Replace no longer changes its verdict: "+
						"remove it\n  %s",
					name,
					strings.Join(detail, " "),
				)
			}

			switch want {
			case 'i':
				iCount++
				iReport = append(iReport, name+": "+strings.Join(detail, " "))
				return
			case 'y':
				if conforms {
					passYes++
				}
			case 'n':
				if conforms {
					passNo++
				}
			}

			isXFail := knownFailures[name]
			if isXFail {
				seenXFail[name] = true
			}

			switch {
			case conforms && isXFail:
				unexpectedOK++
				t.Errorf("file now conforms but is listed in conformanceXFail: remove %q\n  %s",
					name, strings.Join(detail, " "))
			case conforms:
				// expected pass, nothing to do
			case isXFail:
				xfailCount++
				t.Logf("xfail (known): %s\n  %s", name, strings.Join(detail, " "))
			default:
				t.Errorf("conformance mismatch (want %c): %s\n  %s",
					want, name, strings.Join(detail, " "))
			}
		})
	}

	// Report any xfail entries that no longer exist / were not exercised.
	for name := range knownFailures {
		if !seenXFail[name] {
			t.Errorf("conformanceXFail lists %q but it was not found/exercised", name)
		}
	}

	// Emit the implementation-defined behavior table for the record, and pin it against drift.
	sort.Strings(iReport)
	t.Logf("=== implementation-defined (i_) behavior: %d cases ===", iCount)
	for _, line := range iReport {
		t.Logf("  %s", line)
	}
	t.Logf("=== summary: y_ pass=%d, n_ pass=%d, xfail=%d, unexpected-pass=%d ===",
		passYes, passNo, xfailCount, unexpectedOK)

	checkIBehaviorGolden(t, iReport)
}

//nolint:gochecknoglobals // the standard golden-file switch must be a package-level flag
var updateGolden = flag.Bool(
	"update-golden",
	false,
	"rewrite the conformance i_ behavior snapshot from the current run",
)

// checkIBehaviorGolden pins how the lexers treat every implementation-DEFINED (i_) case.
//
// These cases are outside the suite's y_/n_ contract, so nothing asserted them before — which is exactly how the
// UTF-8 bug survived: ten i_string_* documents carrying ill-formed UTF-8 were silently ACCEPTED, and the suite stayed
// green from the day the zero-copy string fast path landed. Snapshotting the table turns any future change in
// implementation-defined behavior into a reviewable diff instead of silence.
//
// Run `go test ./lexers/default-lexer -run TestConformanceParsing -update-golden` after an intentional change.
func checkIBehaviorGolden(t *testing.T, iReport []string) {
	t.Helper()

	golden := filepath.Join(currentDir(), "testdata", "conformance_i_behavior.golden")
	got := strings.Join(iReport, "\n") + "\n"

	if *updateGolden {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatalf("cannot write %s: %v", golden, err)
		}
		t.Logf("updated %s", golden)

		return
	}

	raw, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("cannot read %s (run with -update-golden to create it): %v", golden, err)
	}
	want := string(normalizeLineEndings(raw)) // the golden records verdicts, not bytes on a line

	if want == got {
		return
	}

	wantLines := strings.Split(strings.TrimRight(want, "\n"), "\n")
	gotLines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	t.Errorf("implementation-defined behavior changed (%d recorded cases, %d now).\n"+
		"If the change is intended, re-run with -update-golden and review the diff.", len(wantLines), len(gotLines))
	for _, line := range diffLines(wantLines, gotLines) {
		t.Errorf("  %s", line)
	}
}

// diffLines reports the entries that differ between two sorted "name: verdicts" snapshots.
func diffLines(want, got []string) []string {
	index := func(lines []string) map[string]string {
		m := make(map[string]string, len(lines))
		for _, line := range lines {
			name, verdicts, _ := strings.Cut(line, ": ")
			m[name] = verdicts
		}

		return m
	}

	wantByName, gotByName := index(want), index(got)

	var out []string
	for name, w := range wantByName {
		switch g, ok := gotByName[name]; {
		case !ok:
			out = append(out, "gone:    "+name+" (was "+w+")")
		case g != w:
			out = append(out, "changed: "+name+"\n      was: "+w+"\n      now: "+g)
		}
	}
	for name, g := range gotByName {
		if _, ok := wantByName[name]; !ok {
			out = append(out, "new:     "+name+" ("+g+")")
		}
	}
	sort.Strings(out)

	return out
}

// conformanceXFail lists test_parsing files whose current behavior diverges from the JSONTestSuite expectation (y_/n_).
//
// Listed entries are reported as expected failures (xfail) rather than hard test failures, so the suite stays green and
// guards against *new* regressions while we work through the backlog.
//
// An entry that starts passing is reported as an error ("remove from xfail").
//
// NOTE: populated from the 2026-06-21 baseline run.
// Grouped by root cause.
func conformanceXFail() map[string]bool {
	// currently, no registered expected failures
	return map[string]bool{}
}

// conformanceVerdict is the outcome of draining a lexer over a whole document.
type conformanceVerdict struct {
	accepted bool // reached EOF with no error
	tokens   int  // number of non-EOF tokens produced
}

// drainL runs a semantic lexer to completion and reports whether the document was accepted (EOF reached with Ok()) or
// rejected (error tripped first).
func drainL(lex *L, maximum int) conformanceVerdict {
	n := 0
	for n <= maximum {
		tok := lex.NextToken()
		if !lex.Ok() {
			return conformanceVerdict{accepted: false, tokens: n}
		}
		if tok.IsEOF() {
			return conformanceVerdict{accepted: true, tokens: n}
		}
		n++
	}

	// Safety valve: the lexer kept emitting non-EOF tokens without error.
	return conformanceVerdict{accepted: true, tokens: n}
}

// drainVL runs a verbatim lexer to completion.
func drainVL(lex *VL, maximum int) conformanceVerdict {
	n := 0
	for n <= maximum {
		tok := lex.NextToken()
		if !lex.Ok() {
			return conformanceVerdict{accepted: false, tokens: n}
		}
		if tok.IsEOF() {
			return conformanceVerdict{accepted: true, tokens: n}
		}
		n++
	}

	return conformanceVerdict{accepted: true, tokens: n}
}

type conformanceMode struct {
	name string
	run  func(data []byte, maximum int) conformanceVerdict
}

func conformanceModes() []conformanceMode {
	return []conformanceMode{
		{
			name: "L/bytes",
			run: func(data []byte, maximum int) conformanceVerdict {
				return drainL(NewWithBytes(data), maximum)
			},
		},
		{
			// small buffer stresses buffer-crossing / readMore paths
			name: "L/reader",
			run: func(data []byte, maximum int) conformanceVerdict {
				return drainL(New(bytes.NewReader(data), WithBufferSize(64)), maximum)
			},
		},
		{
			name: "VL/bytes",
			run: func(data []byte, maximum int) conformanceVerdict {
				return drainVL(NewVerbatimWithBytes(data), maximum)
			},
		},
		{
			// VL's streaming string scanners (consumeStringRawStreamFast / consumeStringRawStreaming) had no
			// conformance coverage at all until this mode was added.
			name: "VL/reader",
			run: func(data []byte, maximum int) conformanceVerdict {
				return drainVL(NewVerbatim(bytes.NewReader(data), WithBufferSize(64)), maximum)
			},
		},
		{
			// UTF8Replace must not change what is ACCEPTED except for ill-formed UTF-8, which it sanitizes instead of
			// rejecting. Running it over the whole suite pins that it relaxes nothing else.
			name: "L/replace",
			run: func(data []byte, maximum int) conformanceVerdict {
				return drainL(NewWithBytes(data, WithUTF8Policy(UTF8Replace)), maximum)
			},
		},
	}
}

// conformanceUTF8Relaxed lists the test_parsing files that UTF8Replace accepts although the default policy rejects
// them: exactly the ill-formed-UTF-8 and broken-surrogate-escape cases, which replacement sanitizes to U+FFFD.
//
// Any OTHER file whose verdict differs between the two policies is a bug — replacement must not relax the grammar.
func conformanceUTF8Relaxed() map[string]bool {
	return map[string]bool{
		// ill-formed UTF-8 bytes
		"i_string_UTF-8_invalid_sequence.json":         true,
		"i_string_UTF8_surrogate_U+D800.json":          true,
		"i_string_invalid_utf-8.json":                  true,
		"i_string_iso_latin_1.json":                    true,
		"i_string_lone_utf8_continuation_byte.json":    true,
		"i_string_not_in_unicode_range.json":           true,
		"i_string_overlong_sequence_2_bytes.json":      true,
		"i_string_overlong_sequence_6_bytes.json":      true,
		"i_string_overlong_sequence_6_bytes_null.json": true,
		"i_string_truncated-utf-8.json":                true,
		// \u escapes that do not denote a scalar value
		"i_object_key_lone_2nd_surrogate.json":                true,
		"i_string_1st_surrogate_but_2nd_missing.json":         true,
		"i_string_1st_valid_surrogate_2nd_invalid.json":       true,
		"i_string_incomplete_surrogate_and_escape_valid.json": true,
		"i_string_incomplete_surrogate_pair.json":             true,
		"i_string_incomplete_surrogates_escape_valid.json":    true,
		"i_string_invalid_lonely_surrogate.json":              true,
		"i_string_invalid_surrogate.json":                     true,
		"i_string_inverted_surrogates_U+1D11E.json":           true,
		"i_string_lone_second_surrogate.json":                 true,
	}
}

func verdictStr(v conformanceVerdict) string {
	if v.accepted {
		return "accept"
	}
	return "reject"
}

package lexer

import (
	"bytes"
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

		data, rerr := os.ReadFile(filepath.Join(dir, name))
		if rerr != nil {
			t.Fatalf("cannot read %s: %v", name, rerr)
		}
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
			for _, m := range modes {
				v := m.run(data, maximum)
				detail = append(detail, m.name+"="+verdictStr(v))

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

	// Emit the implementation-defined behavior table for the record.
	sort.Strings(iReport)
	t.Logf("=== implementation-defined (i_) behavior: %d cases ===", iCount)
	for _, line := range iReport {
		t.Logf("  %s", line)
	}
	t.Logf("=== summary: y_ pass=%d, n_ pass=%d, xfail=%d, unexpected-pass=%d ===",
		passYes, passNo, xfailCount, unexpectedOK)
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
	}
}

func verdictStr(v conformanceVerdict) string {
	if v.accepted {
		return "accept"
	}
	return "reject"
}

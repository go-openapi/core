package lexer_test

import (
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// updateGolden regenerates the recorded-behavior snapshot instead of asserting against it.
//
//nolint:gochecknoglobals // the standard golden-file switch must be a package-level flag
var updateGolden = flag.Bool(
	"update-golden",
	false,
	"rewrite the YAML conformance recorded-behavior snapshot from the current run",
)

// checkRecordedGolden pins what YL does with every suite case that has no single-root JSON equivalent — multi-document
// streams, empty documents, and valid YAML the JSON data model cannot express.
//
// Nothing asserts these, because there is no expectation to assert against: they are out of scope by construction.
// Snapshotting them is what stops "out of scope" from quietly becoming "unnoticed regression" — the same blind spot
// the JSON suite had with its i_ cases, where ten documents carrying ill-formed UTF-8 were accepted for months
// without a single test going red.
//
// Run with -update-golden after an intentional change and review the diff.
func checkRecordedGolden(t *testing.T, records map[string]string) {
	t.Helper()

	golden := filepath.Join(currentDir(), "testdata", "conformance_recorded.golden")

	labels := make([]string, 0, len(records))
	for label := range records {
		labels = append(labels, label)
	}
	sort.Strings(labels)

	var b strings.Builder
	for _, label := range labels {
		b.WriteString(label)
		b.WriteString("\t")
		b.WriteString(records[label])
		b.WriteString("\n")
	}
	got := b.String()

	if *updateGolden {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatalf("cannot write %s: %v", golden, err)
		}
		t.Logf("updated %s (%d cases)", golden, len(labels))

		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("cannot read %s (run with -update-golden to create it): %v", golden, err)
	}

	// a golden checked out with CRLF must still compare equal: the file records verdicts, not bytes on a line
	if normalizeLineEndings(string(want)) == got {
		return
	}

	t.Errorf(
		"recorded behavior changed for out-of-scope cases.\n"+
			"If the change is intended, re-run with -update-golden and review the diff.\n%s",
		diffRecorded(
			strings.Split(strings.TrimRight(normalizeLineEndings(string(want)), "\n"), "\n"),
			strings.Split(strings.TrimRight(got, "\n"), "\n"),
		),
	)
}

func diffRecorded(want, got []string) string {
	index := func(lines []string) map[string]string {
		m := make(map[string]string, len(lines))
		for _, line := range lines {
			label, rest, _ := strings.Cut(line, "\t")
			m[label] = rest
		}

		return m
	}

	wantBy, gotBy := index(want), index(got)

	var out []string
	for label, w := range wantBy {
		switch g, ok := gotBy[label]; {
		case !ok:
			out = append(out, "  gone:    "+label)
		case g != w:
			out = append(out, "  changed: "+label+"\n      was: "+w+"\n      now: "+g)
		}
	}
	for label := range gotBy {
		if _, ok := wantBy[label]; !ok {
			out = append(out, "  new:     "+label)
		}
	}
	sort.Strings(out)

	return strings.Join(out, "\n")
}

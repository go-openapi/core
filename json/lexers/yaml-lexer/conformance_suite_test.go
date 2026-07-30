package lexer_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

// tabRun matches the suite's rendering of a hard tab: "»" preceded by any number of em-dashes used to show its
// width. Mirrors upstream's s/—*»/\t/g.
var tabRun = regexp.MustCompile("—*»")

// Loader for the vendored YAML Test Suite (see testdata/yaml-test-suite/SOURCE.md).
//
// Each src/<ID>.yaml is a YAML sequence of one or more related cases. Only the first carries the descriptive fields;
// the rest inherit them, so a file is really "one scenario, several inputs".

// suiteCase is one test case from the suite, with the suite's visible-character encoding already translated.
type suiteCase struct {
	id   string // the file name without extension, e.g. "2G84"
	sub  int    // index within the file (0 when the file holds a single case)
	name string
	tags []string

	yaml string // the input document
	json string // the equivalent JSON, empty when the case has none
	// hasJSON distinguishes "no JSON equivalent" from "the JSON equivalent is the empty document".
	hasJSON bool
	fail    bool // the document is invalid and must be rejected
}

// label identifies a case in test output and in the expectation tables.
func (c suiteCase) label() string {
	if c.sub == 0 {
		return c.id
	}

	return c.id + "/" + itoa(c.sub)
}

// rawCase mirrors the on-disk shape. Every field is a pointer or a slice so an ABSENT field can be told from an empty
// one — the difference between "no JSON equivalent" and "the JSON equivalent is empty" decides which bucket a case
// lands in.
type rawCase struct {
	Name *string `yaml:"name"`
	Tags *string `yaml:"tags"`
	YAML *string `yaml:"yaml"`
	JSON *string `yaml:"json"`
	Fail *bool   `yaml:"fail"`
}

// suiteUnescape translates the suite's visible stand-ins back into the characters they represent.
//
// This is the Go equivalent of upstream's bin/YAMLTestSuite.pm `sub unescape`, and it is not optional: several cases
// hinge on a trailing space or a hard tab that would otherwise be invisible (and would be stripped by an editor).
func suiteUnescape(s string) string {
	s = strings.ReplaceAll(s, "␣", " ")

	// A hard tab, written as "»" preceded by ANY number of em-dashes for width. Upstream's rule is the regexp
	// /—*»/ — matching a fixed set of widths instead silently leaves stray dashes behind for a wider run, which
	// turns the fixture into a different (and usually invalid) document.
	s = tabRun.ReplaceAllString(s, "\t")

	s = strings.ReplaceAll(s, "←", "\r")
	s = strings.ReplaceAll(s, "⇔", "\uFEFF")
	s = strings.ReplaceAll(s, "↵", "") // marks a trailing newline that is already there

	// "∎" marks an input with NO final newline: drop the mark and the newline the fixture had to end with.
	if cut, ok := strings.CutSuffix(s, "∎\n"); ok {
		s = cut
	}
	s = strings.TrimSuffix(s, "∎")

	return s
}

// loadSuite reads every vendored case, applying the suite's inheritance rule and unescaping.
func loadSuite(t *testing.T) []suiteCase {
	t.Helper()

	dir := filepath.Join(currentDir(), "testdata", "yaml-test-suite", "src")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("cannot read the vendored YAML test suite: %v", err)
	}

	var out []suiteCase
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}

		data, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			t.Fatalf("cannot read %s: %v", e.Name(), rerr)
		}

		var raws []rawCase
		if uerr := yaml.Unmarshal(data, &raws); uerr != nil {
			t.Fatalf("cannot parse the suite fixture %s: %v", e.Name(), uerr)
		}

		id := strings.TrimSuffix(e.Name(), ".yaml")
		var name string
		var tags []string
		for i, raw := range raws {
			// only the first case in a file carries the descriptive fields; the others inherit
			if raw.Name != nil {
				name = *raw.Name
			}
			if raw.Tags != nil {
				tags = strings.Fields(*raw.Tags)
			}
			if raw.YAML == nil {
				// a case with no input of its own is a description-only entry
				continue
			}

			sub := 0
			if len(raws) > 1 {
				sub = i
			}

			c := suiteCase{
				id:   id,
				sub:  sub,
				name: name,
				tags: tags,
				yaml: suiteUnescape(*raw.YAML),
				fail: raw.Fail != nil && *raw.Fail,
			}
			if raw.JSON != nil {
				c.json = suiteUnescape(*raw.JSON)
				c.hasJSON = true
			}

			out = append(out, c)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].id != out[j].id {
			return out[i].id < out[j].id
		}

		return out[i].sub < out[j].sub
	})

	return out
}

func currentDir() string {
	_, filename, _, _ := runtime.Caller(1) //nolint:dogsled // only the file name is needed

	return filepath.Dir(filename)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}

	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}

	return string(digits)
}

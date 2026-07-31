package lexer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"
)

// Line-ending independence for the JSON conformance fixtures.
//
// A conformance fixture's exact bytes ARE the test. A checkout with core.autocrlf=true — the Windows default —
// rewrites every LF to CRLF, and then a document that must be REJECTED can start being accepted (or the reverse),
// with the failure reading as a lexer bug rather than a checkout artefact.
//
// .gitattributes marks testdata/** as -text so git never translates them, and that is the real fix. These tests
// cover the other half: a suite that arrived some other way — an unmarked clone, a zip, a copy through a Windows
// tool — must still measure the same documents.

// normalizeLineEndings turns CRLF into LF.
//
// A lone CR is deliberately left alone: it is a meaningful byte inside a JSON document (an unescaped control
// character, which a conforming parser must reject), so collapsing it would change the very thing several n_ cases
// test. TestConformanceFixturesAreStoredWithLF pins that no vendored fixture carries a raw CR, which is what makes
// this safe: every CR that could appear has been introduced by translation.
func normalizeLineEndings(data []byte) []byte {
	if !bytes.Contains(data, []byte("\r")) {
		return data
	}

	return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
}

// TestConformanceFixturesAreStoredWithLF guards the fixtures as committed. It is also what licenses the
// normalisation above: if upstream ever ships a fixture containing a genuine CR, this fails and the normalisation
// has to be revisited rather than silently eating it.
func TestConformanceFixturesAreStoredWithLF(t *testing.T) {
	root := filepath.Join(currentDir(), "..", "testdata", "JSONTestSuite")

	var withCR []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}

		//nolint:gosec // path comes from walking our own vendored fixtures
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if bytes.Contains(data, []byte("\r")) {
			rel, _ := filepath.Rel(root, path)
			withCR = append(withCR, rel)
		}

		return nil
	})
	require.NoError(t, err)

	assert.Emptyf(
		t,
		withCR,
		"fixtures must be stored without carriage returns (see .gitattributes); found CR in %v",
		withCR,
	)
}

func TestNormalizeLineEndings(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "no CR at all", in: "{\n}\n", want: "{\n}\n"},
		{name: "CRLF", in: "{\r\n}\r\n", want: "{\n}\n"},
		{name: "mixed", in: "[\r\n1,\n2\r\n]", want: "[\n1,\n2\n]"},
		{name: "lone CR is preserved", in: "[\"a\rb\"]", want: "[\"a\rb\"]"},
		{name: "empty", in: "", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, string(normalizeLineEndings([]byte(tc.in))))
		})
	}
}

// TestConformanceVerdictsSurviveCRLF is the property that matters: converting every fixture to CRLF must not change
// a single accept/reject verdict, in any lexer mode.
//
// Without the normalisation this fails — a document whose only fault is an unescaped newline inside a string, for
// instance, is a different document once that newline becomes CRLF.
func TestConformanceVerdictsSurviveCRLF(t *testing.T) {
	dir := filepath.Join(currentDir(), "..", "testdata", "JSONTestSuite", "test_parsing")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	modes := conformanceModes()

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}

		raw, rerr := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, rerr)

		lf := normalizeLineEndings(raw)
		crlf := normalizeLineEndings(bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n")))

		require.Equalf(t, string(lf), string(crlf),
			"%s: normalising a CRLF copy must reproduce the LF original", name)

		maximum := len(lf) + 16
		for _, m := range modes {
			assert.Equalf(t, m.run(lf, maximum).accepted, m.run(crlf, maximum).accepted,
				"%s/%s: the verdict changed for a CRLF checkout", name, m.name)
		}
	}
}

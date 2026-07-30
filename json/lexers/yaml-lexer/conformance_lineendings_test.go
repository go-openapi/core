package lexer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"
)

// Line-ending independence.
//
// The vendored fixtures are stored with LF and .gitattributes marks them -text, but a checkout can still arrive with
// CRLF — an unmarked clone, a zip, a copy through a Windows tool. When that happens the harness must test the same
// documents, not silently different ones: a YAML fixture read as CRLF is a different document, and the failure would
// look like a lexer bug rather than a checkout artefact.
//
// These tests assert the property directly instead of trusting the .gitattributes to be present and correct.

// TestSuiteFixturesAreStoredWithLF guards the fixtures as committed: if one ever lands with CRLF, the harness would
// still cope, but the repository would have drifted from what upstream published.
func TestSuiteFixturesAreStoredWithLF(t *testing.T) {
	dir := filepath.Join(currentDir(), "testdata", "yaml-test-suite", "src")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	var withCRLF []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, rerr)
		if strings.Contains(string(data), "\r\n") {
			withCRLF = append(withCRLF, e.Name())
		}
	}

	assert.Emptyf(t, withCRLF,
		"fixtures must be stored with LF (see .gitattributes); found CRLF in %v", withCRLF)
}

// TestNormalizeLineEndings pins the narrow contract: CRLF collapses to LF, and a lone CR is left alone so the
// suite's "←" (an intentional carriage return) survives.
func TestNormalizeLineEndings(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "no CR at all", in: "a\nb\n", want: "a\nb\n"},
		{name: "CRLF", in: "a\r\nb\r\n", want: "a\nb\n"},
		{name: "mixed", in: "a\r\nb\nc\r\n", want: "a\nb\nc\n"},
		{name: "lone CR is preserved", in: "a\rb", want: "a\rb"},
		{name: "CR at end preserved", in: "a\r", want: "a\r"},
		{name: "empty", in: "", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, normalizeLineEndings(tc.in))
		})
	}
}

// TestSuiteUnescapeIsLineEndingIndependent is the property that matters: a fixture converted to CRLF must yield
// exactly the same case data as the LF original, INCLUDING the carriage returns the suite asks for explicitly.
func TestSuiteUnescapeIsLineEndingIndependent(t *testing.T) {
	cases := []struct {
		name string
		lf   string
	}{
		{name: "plain", lf: "a: 1\nb: 2\n"},
		{name: "trailing space", lf: "a: 1␣\nb: 2\n"},
		{name: "hard tab", lf: "a:\t1\nfoo:———»bar\n"},
		{name: "no final newline", lf: "a: 1∎\n"},
		{name: "explicit carriage return", lf: "a: 1←\nb: 2\n"},
		{name: "byte order mark", lf: "⇔a: 1\n"},
		{name: "trailing newline marker", lf: "a: 1↵\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			crlf := strings.ReplaceAll(tc.lf, "\n", "\r\n")

			fromLF := suiteUnescape(normalizeLineEndings(tc.lf))
			fromCRLF := suiteUnescape(normalizeLineEndings(crlf))

			assert.Equalf(t, fromLF, fromCRLF,
				"a CRLF fixture must yield the same case as its LF original\n LF  =%q\n CRLF=%q",
				fromLF, fromCRLF)
		})
	}

	t.Run("an explicit CR survives normalisation", func(t *testing.T) {
		// "←" is the ONLY way a carriage return should enter a case; normalising must not have eaten it
		assert.Equal(t, "a: 1\r\nb\n", suiteUnescape(normalizeLineEndings("a: 1←\nb\n")))
	})
}

// TestLoadSuiteIsLineEndingIndependent runs the real loader over a CRLF copy of the whole vendored suite and asserts
// every case comes out byte-identical. This is the end-to-end version of the property: it would catch a CRLF leak
// anywhere in the path, including inside the YAML parser we read the fixtures with.
func TestLoadSuiteIsLineEndingIndependent(t *testing.T) {
	if testing.Short() {
		t.Skip("copies and re-parses the whole suite")
	}

	lf := loadSuite(t)
	require.NotEmpty(t, lf)

	crlf := loadSuiteFrom(t, crlfCopyOfSuite(t))
	require.Len(t, crlf, len(lf), "the CRLF copy must yield the same number of cases")

	for i := range lf {
		require.Equalf(t, lf[i].label(), crlf[i].label(), "case %d", i)
		assert.Equalf(
			t,
			lf[i].yaml,
			crlf[i].yaml,
			"%s: yaml input differs between LF and CRLF checkouts",
			lf[i].label(),
		)
		assert.Equalf(
			t,
			lf[i].json,
			crlf[i].json,
			"%s: expected JSON differs between LF and CRLF checkouts",
			lf[i].label(),
		)
		assert.Equalf(t, lf[i].fail, crlf[i].fail, "%s: fail flag differs", lf[i].label())
	}
}

// crlfCopyOfSuite writes a CRLF-converted copy of the vendored fixtures into a temp dir and returns its path.
func crlfCopyOfSuite(t *testing.T) string {
	t.Helper()

	src := filepath.Join(currentDir(), "testdata", "yaml-test-suite", "src")
	dst := filepath.Join(t.TempDir(), "src")
	require.NoError(t, os.MkdirAll(dst, 0o750))

	entries, err := os.ReadDir(src)
	require.NoError(t, err)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(src, e.Name()))
		require.NoError(t, rerr)

		converted := strings.ReplaceAll(string(data), "\n", "\r\n")
		out := filepath.Join(dst, filepath.Base(e.Name()))
		//nolint:gosec // out is t.TempDir() joined with a base name from ReadDir over our own vendored fixtures
		require.NoError(t, os.WriteFile(out, []byte(converted), 0o600))
	}

	return dst
}

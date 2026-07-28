package lexer_test

import (
	"fmt"
	"testing"

	jsonlexer "github.com/go-openapi/core/json/lexers/default-lexer"
	"github.com/go-openapi/core/json/lexers/token"
	yamllexer "github.com/go-openapi/core/json/lexers/yaml-lexer"
	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"
)

// repr renders a token as a compact, comparable string.
func repr(t token.T) string {
	switch t.Kind() {
	case token.Delimiter:
		return "D:" + t.Delimiter().String()
	case token.String:
		return "S:" + string(t.Value())
	case token.Key:
		return "K:" + string(t.Value())
	case token.Number:
		return "N:" + string(t.Value())
	case token.Boolean:
		return fmt.Sprintf("B:%v", t.Bool())
	case token.Null:
		return "null"
	default:
		return "?:" + t.Kind().String()
	}
}

func yamlToks(t *testing.T, src string, opts ...yamllexer.Option) ([]string, error) {
	t.Helper()
	l := yamllexer.NewWithBytes([]byte(src), opts...)
	var out []string
	for tok := range l.Tokens() {
		out = append(out, repr(tok))
	}

	return out, l.Err()
}

// leveledJSON / leveledYAML capture each token as "repr@IndentLevel", so the differential
// test locks not just the token stream but the nesting level convention against L.
func leveledJSON(t *testing.T, src string) []string {
	t.Helper()
	l := jsonlexer.NewWithBytes([]byte(src))
	var out []string
	for tok := range l.Tokens() {
		out = append(out, fmt.Sprintf("%s@%d", repr(tok), l.IndentLevel()))
	}
	require.NoErrorf(t, l.Err(), "json lexer should accept %q", src)

	return out
}

func leveledYAML(t *testing.T, src string) ([]string, error) {
	t.Helper()
	l := yamllexer.NewWithBytes([]byte(src))
	var out []string
	for tok := range l.Tokens() {
		out = append(out, fmt.Sprintf("%s@%d", repr(tok), l.IndentLevel()))
	}

	return out, l.Err()
}

// TestJSONSubset checks the core contract: because JSON is (nearly) a subset of YAML,
// lexing a JSON document as YAML must yield the SAME token stream (and IndentLevel) as the
// JSON lexer.
func TestJSONSubset(t *testing.T) {
	cases := []string{
		`42`,
		`-1.5e3`,
		`"just a string"`,
		`true`,
		`null`,
		`{}`,
		`[]`,
		`[1,2,3]`,
		`{"a":1,"b":[true,false,null],"c":"hello"}`,
		`{"nested":{"x":[{"y":1}]}}`,
		`{"empty_obj":{},"empty_arr":[],"n":0}`,
		`[[[1]]]`,
	}

	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			want := leveledJSON(t, src)
			got, err := leveledYAML(t, src)
			require.NoErrorf(t, err, "yaml lexer should accept JSON %q", src)
			assert.Equalf(t, want, got, "YL(json) must equal L(json) (token+level) for %q", src)
		})
	}
}

// TestYAMLGolden covers YAML-specific behaviour with hand-written expectations.
func TestYAMLGolden(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"decimal-int", "v: 123", []string{"D:{", "K:v", "N:123", "D:}"}},
		{"hex", "v: 0x1F", []string{"D:{", "K:v", "N:31", "D:}"}},
		{"octal-0o", "v: 0o17", []string{"D:{", "K:v", "N:15", "D:}"}},
		{"octal-legacy", "v: 012", []string{"D:{", "K:v", "N:10", "D:}"}},
		{"binary", "v: 0b101", []string{"D:{", "K:v", "N:5", "D:}"}},
		{"underscore", "v: 1_000", []string{"D:{", "K:v", "N:1000", "D:}"}},
		{"plus", "v: +5", []string{"D:{", "K:v", "N:5", "D:}"}},
		{"lead-dot", "v: .5", []string{"D:{", "K:v", "N:0.5", "D:}"}},
		{"trail-dot", "v: 5.", []string{"D:{", "K:v", "N:5.0", "D:}"}},
		{"exp-no-dot", "v: 1e3", []string{"D:{", "K:v", "N:1e3", "D:}"}},
		{
			"bignum",
			"v: 99999999999999999999999999",
			[]string{"D:{", "K:v", "N:99999999999999999999999999", "D:}"},
		},
		{"quoted-number-stays-string", `v: "123"`, []string{"D:{", "K:v", "S:123", "D:}"}},
		{"plain-string", "v: yes", []string{"D:{", "K:v", "S:yes", "D:}"}},
		{"block-seq", "- 1\n- 2", []string{"D:[", "N:1", "N:2", "D:]"}},
		{"anchor-alias", "a: &x 1\nb: *x", []string{"D:{", "K:a", "N:1", "K:b", "N:1", "D:}"}},
		{"tag-str-coerces", `v: !!str 7`, []string{"D:{", "K:v", "S:7", "D:}"}},
		{"tag-bool-coerces", `v: !!bool yes`, []string{"D:{", "K:v", "B:true", "D:}"}},
		{"numeric-key-coerced", "123: ok", []string{"D:{", "K:123", "S:ok", "D:}"}},
		// An empty node is a legal YAML value and resolves to null. This is NOT a JSON
		// subset violation: the same bytes are invalid JSON, and L rejects them with
		// codes.ErrColonValue, so the differential contract never covers them.
		{"empty-flow-value-is-null", `{"0":}`, []string{"D:{", "K:0", "null", "D:}"}},
		{"empty-block-value-is-null", "v:", []string{"D:{", "K:v", "null", "D:}"}},
		// D5 merge keys
		{
			"merge-simple",
			"base: &b\n  a: 1\n  b: 2\nderived:\n  <<: *b\n  c: 3",
			[]string{
				"D:{",
				"K:base", "D:{", "K:a", "N:1", "K:b", "N:2", "D:}",
				"K:derived", "D:{", "K:a", "N:1", "K:b", "N:2", "K:c", "N:3", "D:}",
				"D:}",
			},
		},
		{
			"merge-explicit-wins",
			"base: &b\n  a: 1\nderived:\n  <<: *b\n  a: 99",
			[]string{
				"D:{",
				"K:base", "D:{", "K:a", "N:1", "D:}",
				"K:derived", "D:{", "K:a", "N:99", "D:}",
				"D:}",
			},
		},
		{
			"merge-sequence-first-wins",
			"x: &x {a: 1}\ny: &y {a: 2, b: 3}\nz:\n  <<: [*x, *y]",
			[]string{
				"D:{",
				"K:x", "D:{", "K:a", "N:1", "D:}",
				"K:y", "D:{", "K:a", "N:2", "K:b", "N:3", "D:}",
				"K:z", "D:{", "K:a", "N:1", "K:b", "N:3", "D:}",
				"D:}",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamlToks(t, tc.src)
			require.NoErrorf(t, err, "unexpected error for %q", tc.src)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestYAMLErrors covers the conditions that must be rejected.
func TestYAMLErrors(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr error
	}{
		{"multi-doc", "a: 1\n---\nb: 2", yamllexer.ErrMultipleDocuments},
		{"infinity", "v: .inf", yamllexer.ErrUnsupportedScalar},
		{"nan", "v: .nan", yamllexer.ErrUnsupportedScalar},
		{"unknown-anchor", "b: *missing", yamllexer.ErrUnknownAnchor},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := yamlToks(t, tc.src)
			require.Error(t, err)
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}
}

// TestMaxTokens exercises the billion-laughs circuit breaker.
func TestMaxTokens(t *testing.T) {
	// a small doc that expands via nested aliases
	src := "" +
		"a: &a [1,1,1,1,1]\n" +
		"b: &b [*a,*a,*a,*a,*a]\n" +
		"c: [*b,*b,*b,*b,*b]\n"
	_, err := yamlToks(t, src, yamllexer.WithMaxTokens(50))
	require.ErrorIs(t, err, yamllexer.ErrMaxTokens)
}

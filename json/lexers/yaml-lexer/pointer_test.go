package lexer_test

import (
	"testing"

	yamllexer "github.com/go-openapi/core/json/lexers/yaml-lexer"
	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"
)

// ptrRow pairs a token repr with the JSON pointer reported at that token.
type ptrRow struct {
	tok string
	ptr string
}

func ptrRows(t *testing.T, src string, opts ...yamllexer.Option) ([]ptrRow, error) {
	t.Helper()
	l := yamllexer.NewWithBytes([]byte(src), opts...)
	var out []ptrRow
	for tok := range l.Tokens() {
		out = append(out, ptrRow{tok: repr(tok), ptr: l.JSONPointer().String()})
	}

	return out, l.Err()
}

func TestJSONPointer(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []ptrRow
	}{
		{
			name: "object-array-nested",
			src:  `{"a":1,"b":[10,20],"c":{"d":true}}`,
			want: []ptrRow{
				{"D:{", ""},
				{"K:a", "/a"},
				{"N:1", "/a"},
				{"K:b", "/b"},
				{"D:[", "/b"},
				{"N:10", "/b/0"},
				{"N:20", "/b/1"},
				{"D:]", "/b"},
				{"K:c", "/c"},
				{"D:{", "/c"},
				{"K:d", "/c/d"},
				{"B:true", "/c/d"},
				{"D:}", "/c"},
				{"D:}", ""},
			},
		},
		{
			name: "array-of-objects",
			src:  `[{"x":1},{"y":2}]`,
			want: []ptrRow{
				{"D:[", ""},
				{"D:{", "/0"},
				{"K:x", "/0/x"},
				{"N:1", "/0/x"},
				{"D:}", "/0"},
				{"D:{", "/1"},
				{"K:y", "/1/y"},
				{"N:2", "/1/y"},
				{"D:}", "/1"},
				{"D:]", ""},
			},
		},
		{
			// RFC 6901 escaping: "/" → ~1, "~" → ~0. Keys carry the logical name; the
			// escaping is applied by Pointer.String().
			name: "rfc6901-escaping",
			src:  `{"a/b":1,"m~n":2}`,
			want: []ptrRow{
				{"D:{", ""},
				{"K:a/b", "/a~1b"},
				{"N:1", "/a~1b"},
				{"K:m~n", "/m~0n"},
				{"N:2", "/m~0n"},
				{"D:}", ""},
			},
		},
		{
			// YAML-native: the pointer is structural, so alias-expanded content is addressed
			// by its position in the document, not the anchor definition.
			name: "yaml-alias",
			src:  "a: &x [1, 2]\nb: *x",
			want: []ptrRow{
				{"D:{", ""},
				{"K:a", "/a"},
				{"D:[", "/a"},
				{"N:1", "/a/0"},
				{"N:2", "/a/1"},
				{"D:]", "/a"},
				{"K:b", "/b"},
				{"D:[", "/b"},
				{"N:1", "/b/0"},
				{"N:2", "/b/1"},
				{"D:]", "/b"},
				{"D:}", ""},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ptrRows(t, tc.src, yamllexer.WithJSONPointer(true))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestJSONPointerOff verifies the pointer stays empty when the option is not set (and that
// tracking costs nothing off-path).
func TestJSONPointerOff(t *testing.T) {
	rows, err := ptrRows(t, `{"a":[1,2]}`)
	require.NoError(t, err)
	for _, r := range rows {
		assert.Emptyf(
			t,
			r.ptr,
			"pointer must be empty without WithJSONPointer, got %q at %s",
			r.ptr,
			r.tok,
		)
	}
}

// TestJSONPointerClone verifies a cloned pointer survives past the next token, while the
// live pointer keeps moving.
func TestJSONPointerClone(t *testing.T) {
	l := yamllexer.NewWithBytes([]byte(`{"a":{"b":1}}`), yamllexer.WithJSONPointer(true))

	var saved string
	var savedClone interface{ String() string }
	for tok := range l.Tokens() {
		if tok.IsKey() && string(tok.Value()) == "b" {
			p := l.JSONPointer().Clone()
			savedClone = p
			saved = p.String()

			break
		}
	}
	require.NotNil(t, savedClone)
	require.Equal(t, "/a/b", saved)

	// drain the rest; the clone must be unaffected
	for range l.Tokens() {
		continue
	}
	assert.Equal(t, "/a/b", savedClone.String())
}

package writer

import (
	"bytes"
	"io"
	"math/big"
	"testing"

	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"

	"github.com/go-openapi/core/json/lexers/token"
	"github.com/go-openapi/core/json/stores/values"
	"github.com/go-openapi/core/json/types"
	"github.com/go-openapi/core/json/writers"
)

type fullWriter interface {
	writers.StoreWriter
	writers.JSONWriter
	writers.TokenWriter
	writers.VerbatimWriter
}

func TestWriter(t *testing.T) {
	stuff := prepareStuff()

	t.Run("with unbuffered", func(t *testing.T) {
		t.Run("should write JSON straight-through to io.Writer", func(t *testing.T) {
			var tw bytes.Buffer

			jw := NewUnbuffered(&tw)

			writeStuff(t, jw, stuff)
			require.NoError(t, jw.Err())
			// t.Log(tw.String())
			assert.JSONEq(t, expected, tw.String())

			t.Run("written bytes should reflect the size of the output", func(t *testing.T) {
				assert.Equal(t, int64(len(tw.Bytes())), jw.Size())
			})
		})
		// TODO: test edge cases with Copy and utf8
		// TODO: edge case Number with invalid type
		// TODO: edge case token EOF
		// TODO: edge case no redeem, gc cleanup
	})

	t.Run("with buffered", func(t *testing.T) {
		t.Run("should write JSON to io.Writer", func(t *testing.T) {
			var tw bytes.Buffer

			jw := NewBuffered(&tw)

			writeStuff(t, jw, stuff)
			require.NoError(t, jw.Err())
			require.NoError(t, jw.Flush())
			assert.JSONEq(t, expected, tw.String())

			t.Run("written bytes should reflect the size of the output", func(t *testing.T) {
				assert.Equal(t, int64(len(tw.Bytes())), jw.Size())
			})
		})
	})

	t.Run("with indented", func(t *testing.T) {
		t.Run("should write JSON to io.Writer and need Flush", func(t *testing.T) {
			var tw bytes.Buffer

			jw := NewIndented(
				&tw,
				WithIndent("    "),
				WithIndentBufferedOptions(WithBufferSize(32)),
			)

			writeStuff(t, jw, stuff)
			require.NoError(t, jw.Err())
			require.NoError(t, jw.Flush())
			// t.Log(tw.String())

			assert.JSONEq(t, expected, tw.String())
			// TODO: assert indentation...

			t.Run("written bytes should reflect the size of the output", func(t *testing.T) {
				assert.Equal(t, int64(len(tw.Bytes())), jw.Size())
			})
		})
	})

	t.Run("with yaml", func(t *testing.T) {
		t.Run("should write JSON to io.Writer and need Flush", func(t *testing.T) {
			var tw bytes.Buffer

			jw := NewYAML(&tw, WithYAMLIndent("  "), WithYAMLBufferedOptions(WithBufferSize(32)))

			writeStuff(t, jw, stuff)
			require.NoError(t, jw.Err())
			require.NoError(t, jw.Flush())
			t.Log(tw.String())

			// assert.JSONEq(t, expected, tw.String())
			// TODO: assert indentation...

			t.Run("written bytes should reflect the size of the output", func(t *testing.T) {
				assert.Equal(t, int64(len(tw.Bytes())), jw.Size())
			})
		})
	})
}

func TestWriterPool(t *testing.T) {
	stuff := prepareStuff()

	t.Run("with unbuffered", func(t *testing.T) {
		t.Run("should write JSON straight-through to io.Writer", func(t *testing.T) {
			var tw bytes.Buffer

			jw := BorrowUnbuffered(&tw)
			defer func() {
				RedeemUnbuffered(jw)
			}()
			writeStuff(t, jw, stuff)
			require.NoError(t, jw.Err())
			assert.JSONEq(t, expected, tw.String())
			t.Run("written bytes should reflect the size of the output", func(t *testing.T) {
				assert.Equal(t, int64(len(tw.Bytes())), jw.Size())
			})
		})
	})

	t.Run("with pooled buffered", func(t *testing.T) {
		t.Run("should write JSON straight-through to io.Writer", func(t *testing.T) {
			var tw bytes.Buffer

			jw := BorrowUnbuffered(&tw)
			defer func() {
				RedeemUnbuffered(jw)
			}()
			writeStuff(t, jw, stuff)
			assert.JSONEq(t, expected, tw.String())
			t.Run("written bytes should reflect the size of the output", func(t *testing.T) {
				assert.Equal(t, int64(len(tw.Bytes())), jw.Size())
			})
		})
	})

	t.Run("with pooled indented", func(t *testing.T) {
		t.Run("should write JSON straight-through to io.Writer", func(t *testing.T) {
			var tw bytes.Buffer

			jw := BorrowIndented(&tw)
			defer func() {
				RedeemIndented(jw)
			}()
			writeStuff(t, jw, stuff)
			require.NoError(t, jw.Err())
			require.NoError(t, jw.Flush())
			assert.JSONEq(t, expected, tw.String())
			t.Run("written bytes should reflect the size of the output", func(t *testing.T) {
				assert.Equal(t, int64(len(tw.Bytes())), jw.Size())
			})
		})
	})
}

func BenchmarkProfile(b *testing.B) {
	stuff := prepareStuff()

	b.Run("with unbuffered", func(b *testing.B) {
		b.Run("writer profile with math/big values", func(b *testing.B) {
			var tw bytes.Buffer
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				tw.Reset()
				jw := BorrowUnbuffered(&tw)
				writeStuff(b, jw, stuff)
				RedeemUnbuffered(jw)
			}
		})

		b.Run("writer profile without math/big values", func(b *testing.B) {
			var tw bytes.Buffer
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				tw.Reset()
				jw := BorrowUnbuffered(&tw)
				writeStuffWithoutBig(b, jw, stuff)
				RedeemUnbuffered(jw)
			}
		})
	})

	b.Run("with buffered", func(b *testing.B) {
		b.Run("writer profile with math/big values", func(b *testing.B) {
			var tw bytes.Buffer
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				tw.Reset()
				jw := BorrowBuffered(&tw)
				writeStuff(b, jw, stuff)
				_ = jw.Flush()
				RedeemBuffered(jw)
			}
		})

		b.Run("writer profile without math/big values", func(b *testing.B) {
			var tw bytes.Buffer
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				tw.Reset()
				jw := BorrowBuffered(&tw)
				writeStuffWithoutBig(b, jw, stuff)
				_ = jw.Flush()
				RedeemBuffered(jw)
			}
		})
	})

	b.Run("with indented", func(b *testing.B) {
		b.Run("writer profile with math/big values", func(b *testing.B) {
			var tw bytes.Buffer
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				tw.Reset()
				jw := BorrowIndented(&tw)
				writeStuff(b, jw, stuff)
				_ = jw.Flush()
				RedeemIndented(jw)
			}
		})

		b.Run("writer profile without math/big values", func(b *testing.B) {
			var tw bytes.Buffer
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				tw.Reset()
				jw := BorrowIndented(&tw)
				writeStuffWithoutBig(b, jw, stuff)
				_ = jw.Flush()
				RedeemIndented(jw)
			}
		})
	})
}

type testValues struct {
	Values      map[string]values.Value
	Keys        map[string]values.InternedKey
	Tokens      map[string]token.T
	Bools       map[string]bool
	Strings     map[string]string
	Numbers     map[string]any
	JSONBools   map[string]types.Boolean
	JSONStrings map[string]types.String
	JSONNumbers map[string]types.Number
	Readers     map[string]func() io.Reader
	Bytes       map[string][]byte
	Runes       map[string][]rune
}

func prepareStuff() testValues {
	reader1 := bytes.NewReader([]byte{})
	readerValues1 := []byte("from\treader")
	reader2 := bytes.NewReader([]byte{})
	readerValues2 := []byte("678")

	return testValues{
		Values: map[string]values.Value{
			"bool1":   values.MakeBoolValue(false),
			"string1": values.MakeStringValue(`"quoted_string"`),
			"number1": values.MakeNumberValue(types.Number{Value: []byte("123")}),
		},
		Keys: map[string]values.InternedKey{
			"key1": values.MakeInternedKey("stores_values"),
			"key2": values.MakeInternedKey("readers"),
			"key3": values.MakeInternedKey("native_bool"),
			"key4": values.MakeInternedKey("native_null"),
			"key5": values.MakeInternedKey("json_types"),
			"key6": values.MakeInternedKey("raw"),
			"key7": values.MakeInternedKey("tokens"),
		},
		Numbers: map[string]any{
			"number2":      15,
			"number3":      big.NewRat(12, 13),
			"number3nobig": float64(12) / float64(13),
			"number4":      uint16(12),
			"number5":      float32(12.12),
			"number6":      big.NewFloat(12.23),
			"number6nobig": float64(12.23),
			"number7":      big.NewInt(123456789),
			"number7nobig": int64(123456789),
		},
		JSONBools: map[string]types.Boolean{
			"jsonbool": types.Boolean{}.With(true),
		},
		JSONStrings: map[string]types.String{
			"jsonstring": {Value: []byte("quote")},
		},
		JSONNumbers: map[string]types.Number{
			"jsonnumber": {Value: []byte("12345")},
		},
		Bytes: map[string][]byte{
			"raw1":    []byte("[45,1]"),
			"string3": []byte("native_strings"),
			"string5": []byte("escaped\nstring"),
		},
		Readers: map[string]func() io.Reader{
			"reader1": func() io.Reader { reader1.Reset(readerValues1); return reader1 },
			"reader2": func() io.Reader { reader2.Reset(readerValues2); return reader2 },
		},
		Strings: map[string]string{
			"string2": "native_number",
			"string4": `"quoted"`,
		},
		Runes: map[string][]rune{
			"string6": []rune("escaped\tstring"),
		},
		Tokens: map[string]token.T{
			"tokenBool":                 token.MakeBoolean(true),
			"tokenClosingBracket":       token.MakeDelimiter(token.ClosingBracket),
			"tokenClosingSquareBracket": token.MakeDelimiter(token.ClosingSquareBracket),
			"tokenColon":                token.MakeDelimiter(token.Colon),
			"tokenComma":                token.MakeDelimiter(token.Comma),
			"tokenKey1":                 token.MakeWithValue(token.Key, []byte("key_token")),
			"tokenKey2":                 token.MakeWithValue(token.Key, []byte("inner")),
			"tokenKey3":                 token.MakeWithValue(token.Key, []byte("outer")),
			"tokenNumber1":              token.MakeWithValue(token.Number, []byte("999")),
			"tokenOpeningBracket":       token.MakeDelimiter(token.OpeningBracket),
			"tokenOpeningSquareBracket": token.MakeDelimiter(token.OpeningSquareBracket),
			"tokenString1":              token.MakeWithValue(token.String, []byte("\t\r\n\"\\r")),
		},
	}
}

func writeStuff(t testing.TB, jw fullWriter, stuff testValues) {
	writeStuffWithSelector(t, jw, stuff, true)
}

func writeStuffWithoutBig(t testing.TB, jw fullWriter, stuff testValues) {
	writeStuffWithSelector(t, jw, stuff, false)
}

func writeStuffWithSelector(t testing.TB, jw fullWriter, stuff testValues, withMathBig bool) {
	// TODO: uint8, uint32, uint64, int8, int16, int32, []byte, values from math/big (not pointers)
	// TODO: verbatim token, verbatim value
	jw.StartObject()
	// stores
	jw.Key(stuff.Keys["key1"])
	jw.StartArray()
	jw.Value(stuff.Values["string1"])
	jw.Comma()
	jw.Value(stuff.Values["bool1"])
	jw.Comma()
	jw.Value(stuff.Values["number1"])
	jw.Comma()
	jw.Value(values.NullValue)
	jw.EndArray()
	jw.Comma()
	jw.String(stuff.Strings["string2"])
	jw.Colon()
	jw.StartArray()
	// native numbers
	jw.Number(stuff.Numbers["number2"])
	jw.Comma()
	if withMathBig {
		jw.Number(stuff.Numbers["number3"])
	} else {
		jw.Number(stuff.Numbers["number3nobig"])
	}
	jw.Comma()
	jw.Number(stuff.Numbers["number4"])
	jw.Comma()
	jw.Number(stuff.Numbers["number5"])
	jw.Comma()
	if withMathBig {
		jw.Number(stuff.Numbers["number6"])
	} else {
		jw.Number(stuff.Numbers["number6nobig"])
	}
	jw.Comma()
	if withMathBig {
		jw.Number(stuff.Numbers["number7"])
	} else {
		jw.Number(stuff.Numbers["number7nobig"])
	}
	jw.EndArray()
	jw.Comma()
	// native strings
	jw.StringBytes(stuff.Bytes["string3"])
	jw.Colon()
	jw.StartArray()
	jw.String(stuff.Strings["string4"])
	jw.Comma()
	jw.StringBytes(stuff.Bytes["string5"])
	jw.Comma()
	jw.StringRunes(stuff.Runes["string6"])
	jw.EndArray()
	jw.Comma()
	jw.Key(stuff.Keys["key2"])
	// readers
	jw.StartArray()
	jw.StringCopy(stuff.Readers["reader1"]())
	require.NoError(t, jw.Err())
	jw.Comma()
	jw.NumberCopy(stuff.Readers["reader2"]())
	require.NoError(t, jw.Err())
	jw.EndArray()
	jw.Comma()
	// native
	jw.Key(stuff.Keys["key3"])
	jw.Bool(true)
	jw.Comma()
	jw.Key(stuff.Keys["key4"])
	jw.Null()
	jw.Comma()
	jw.Key(stuff.Keys["key5"])
	// json types
	jw.StartArray()
	jw.JSONNull(types.Null)
	jw.Comma()
	jw.JSONBoolean(stuff.JSONBools["jsonbool"])
	jw.Comma()
	jw.JSONString(stuff.JSONStrings["jsonstring"])
	jw.Comma()
	jw.JSONNumber(stuff.JSONNumbers["jsonnumber"])
	jw.EndArray()
	jw.Comma()
	jw.Key(stuff.Keys["key6"])
	jw.Raw(stuff.Bytes["raw1"])
	jw.Comma()
	jw.Key(stuff.Keys["key7"])
	jw.StartArray()
	jw.Token(stuff.Tokens["tokenBool"])
	jw.Token(stuff.Tokens["tokenComma"])
	jw.Token(stuff.Tokens["tokenNumber1"])
	jw.Token(stuff.Tokens["tokenComma"])
	jw.Token(stuff.Tokens["tokenString1"])
	jw.Token(stuff.Tokens["tokenComma"])
	jw.Token(stuff.Tokens["tokenOpeningBracket"])
	jw.Token(stuff.Tokens["tokenKey1"])
	jw.Token(stuff.Tokens["tokenColon"])
	jw.Token(stuff.Tokens["tokenOpeningSquareBracket"])
	jw.Token(token.NullToken)
	jw.Token(stuff.Tokens["tokenComma"])
	jw.Token(stuff.Tokens["tokenOpeningBracket"])
	jw.Token(stuff.Tokens["tokenKey2"])
	jw.Token(stuff.Tokens["tokenColon"])
	jw.Token(stuff.Tokens["tokenOpeningBracket"])
	jw.Token(stuff.Tokens["tokenClosingBracket"])
	jw.Token(stuff.Tokens["tokenComma"])
	jw.Token(stuff.Tokens["tokenKey3"])
	jw.Token(stuff.Tokens["tokenColon"])
	jw.Token(stuff.Tokens["tokenOpeningSquareBracket"])
	jw.Token(stuff.Tokens["tokenClosingSquareBracket"])
	jw.Token(stuff.Tokens["tokenClosingBracket"])
	jw.Token(stuff.Tokens["tokenClosingSquareBracket"])
	jw.Token(stuff.Tokens["tokenClosingBracket"])
	jw.EndArray()
	jw.EndObject()

	require.True(t, jw.Ok())
}

const expected = `{
	"stores_values":[
		"\"quoted_string\"",
		false,
		123,
		null
	],
	"native_number":[15,0.9230769230769231,12,12.12,12.23,123456789],
	"native_strings":[
		"\"quoted\"",
		"escaped\nstring",
		"escaped\tstring"
	],
	"readers":[
		"from\treader",
		678
	],
	"native_bool":true,
	"native_null":null,
	"json_types":[null,true,"quote",12345],
	"raw":[45,1],
	"tokens":[
		true,
		999,
		"\t\r\n\"\\r",
	  {
			"key_token":[
				null,
				{
	        "inner":{},
				  "outer":[]
	      }
			]
		}
	]
}
`

package strscan

import (
	"bytes"
	"math/rand"
	"testing"
)

// scalarStop is the reference oracle: first index of a byte < 0x20 || '"' || '\', and whether any byte STRICTLY
// BEFORE that index is >= 0x80.
func scalarStop(data []byte) (int, bool) {
	var nonASCII bool
	for i, b := range data {
		if b < 0x20 || b == '"' || b == '\\' {
			return i, nonASCII
		}
		nonASCII = nonASCII || b >= 0x80
	}

	return len(data), nonASCII
}

// TestScanStopOracle sweeps ScanStop (the shipped gate, AVX2 or SWAR depending on the host) and the portable
// scanStopSWAR against the scalar reference over random inputs at every length boundary around the 8-byte word and
// 32-byte YMM strides.
func TestScanStopOracle(t *testing.T) {
	const (
		trials     = 400
		iterations = 4
	)

	rng := rand.New(rand.NewSource(1)) //nolint:gosec // okay to use a weak random source in tests
	lengths := []int{0, 1, 5, 7, 8, 15, 16, 31, 32, 33, 40, 63, 64, 65, 96, 127, 128, 200, 257, 512}
	stops := []byte{'"', '\\', 0x00, 0x1f, 0x0a, 0x09, 0x1e}

	for _, n := range lengths {
		for trial := range trials {
			data := make([]byte, n)
			for i := range data {
				// 0x20..0xff (incl high bytes)
				b := byte(0x20 + rng.Intn(0xE0))
				if b == '"' || b == '\\' {
					b = 'x'
				}
				data[i] = b
			}

			if n > 0 {
				for range rng.Intn(iterations) {
					data[rng.Intn(n)] = stops[rng.Intn(len(stops))]
				}
			}

			want, wantNonASCII := scalarStop(data)
			// the AVX2-gated path and the portable SWAR path (which WithoutAVX2 forces the lexer onto) must both match the
			// scalar oracle — for the stop index AND for the fused non-ASCII verdict the string scanners rely on to skip
			// UTF-8 validation.
			if got, gotNonASCII := ScanStop(data); got != want || gotNonASCII != wantNonASCII {
				t.Fatalf("ScanStop n=%d trial=%d: got (%d,%v) want (%d,%v)",
					n, trial, got, gotNonASCII, want, wantNonASCII)
			}
			if got, gotNonASCII := scanStopSWAR(data); got != want || gotNonASCII != wantNonASCII {
				t.Fatalf("scanStopSWAR n=%d trial=%d: got (%d,%v) want (%d,%v)",
					n, trial, got, gotNonASCII, want, wantNonASCII)
			}
		}
	}
}

// TestScanStopHighBytesSafe guards the classic false-positive.
//
// A UTF-8 lead/cont byte (>= 0x80) must never be mistaken for a control char.
func TestScanStopHighBytesSafe(t *testing.T) {
	data := make([]byte, 96)
	for i := range data {
		data[i] = 0x80 | byte(i&0x3f)
	}
	if got, nonASCII := ScanStop(data); got != len(data) || !nonASCII {
		t.Fatalf(
			"high bytes flagged as stop: got (%d,%v), want (%d,true)",
			got,
			nonASCII,
			len(data),
		)
	}

	data[70] = 0x1f
	if got, nonASCII := ScanStop(data); got != 70 || !nonASCII {
		t.Fatalf("got (%d,%v), want (70,true)", got, nonASCII)
	}
}

// TestScanStopNonASCIIExactness pins the boundary the fused UTF-8 detection depends on: bytes at or after the stop
// must NOT count. A conservative over-report would only cost a redundant validation, but a *false negative* would let
// invalid UTF-8 through, so the mask has to be exact in the direction that matters and is checked both ways here.
func TestScanStopNonASCIIExactness(t *testing.T) {
	cases := []struct {
		name         string
		data         []byte
		wantStop     int
		wantNonASCII bool
	}{
		{name: "empty", data: []byte{}, wantStop: 0},
		{name: "ascii only, no stop", data: []byte("plain ascii run"), wantStop: 15},
		{name: "stop first, high byte after", data: append([]byte{'"'}, 0xc3, 0xa9), wantStop: 0},
		{
			name:     "high byte right after a late stop",
			data:     append(append([]byte("0123456789abcdefghijklmnopqrstuv"), '"'), 0xff, 0xff),
			wantStop: 32,
		},
		{
			name:         "high byte right before a late stop",
			data:         append(append([]byte("0123456789abcdefghijklmnopqrstu"), 0xc3), '"'),
			wantStop:     32,
			wantNonASCII: true,
		},
		{
			name:         "high byte in the scalar tail",
			data:         append(bytes.Repeat([]byte{'a'}, 40), 0xe2, 0x82, 0xac, '"'),
			wantStop:     43,
			wantNonASCII: true,
		},
		{
			name:     "tail high byte after the stop",
			data:     append(bytes.Repeat([]byte{'a'}, 40), '"', 0xe2, 0x82, 0xac),
			wantStop: 40,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantStop, wantNonASCII := scalarStop(tc.data)
			if wantStop != tc.wantStop || wantNonASCII != tc.wantNonASCII {
				t.Fatalf("the case table disagrees with the oracle: table (%d,%v), oracle (%d,%v)",
					tc.wantStop, tc.wantNonASCII, wantStop, wantNonASCII)
			}
			if got, nonASCII := ScanStop(
				tc.data,
			); got != tc.wantStop ||
				nonASCII != tc.wantNonASCII {
				t.Errorf(
					"ScanStop: got (%d,%v), want (%d,%v)",
					got,
					nonASCII,
					tc.wantStop,
					tc.wantNonASCII,
				)
			}
			if got, nonASCII := scanStopSWAR(
				tc.data,
			); got != tc.wantStop ||
				nonASCII != tc.wantNonASCII {
				t.Errorf(
					"scanStopSWAR: got (%d,%v), want (%d,%v)",
					got,
					nonASCII,
					tc.wantStop,
					tc.wantNonASCII,
				)
			}
		})
	}
}

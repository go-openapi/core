package strscan

import (
	"math/rand"
	"testing"
)

// scalarStop is the reference oracle: first index of a byte < 0x20 || '"' || '\'.
func scalarStop(data []byte) int {
	for i, b := range data {
		if b < 0x20 || b == '"' || b == '\\' {
			return i
		}
	}

	return len(data)
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

			want := scalarStop(data)
			// the AVX2-gated path and the portable SWAR path (which WithoutAVX2 forces the lexer onto) must both match the
			// scalar oracle.
			if got := ScanStop(data); got != want {
				t.Fatalf("ScanStop n=%d trial=%d: got %d want %d", n, trial, got, want)
			}
			if got := scanStopSWAR(data); got != want {
				t.Fatalf("scanStopSWAR n=%d trial=%d: got %d want %d", n, trial, got, want)
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
	if got := ScanStop(data); got != len(data) {
		t.Fatalf("high bytes flagged as stop: got %d, want %d", got, len(data))
	}

	data[70] = 0x1f
	if got := ScanStop(data); got != 70 {
		t.Fatalf("got %d, want 70", got)
	}
}

//go:build amd64

package strscan

import (
	"fmt"
	"io"
	"testing"
)

// BenchmarkStringStop is the isolated SWAR-vs-AVX2 head-to-head that sizes the crossover the gate is built on (plan
// §9.3): AVX2 loses to the 8-byte SWAR loop below ~32 bytes (YMM broadcast setup) and wins 6–14x for long clean
// runs.
//
// The stop sits only at the end, so each variant scans the whole slice.
func BenchmarkStringStop(b *testing.B) {
	var sink int

	for _, n := range []int{8, 16, 32, 64, 128, 256, 512, 1024, 4096} {
		data := make([]byte, n)
		for i := range data {
			data[i] = 'x'
		}
		data[n-1] = '"'
		b.Run(fmt.Sprintf("len=%04d/SWAR", n), func(b *testing.B) {
			b.SetBytes(int64(n))
			for b.Loop() {
				sink += scanStopSWAR(data)
			}
		})
		b.Run(fmt.Sprintf("len=%04d/AVX2", n), func(b *testing.B) {
			b.SetBytes(int64(n))
			for b.Loop() {
				sink += stringStopIndexAVX2(data)
			}
		})
	}

	fmt.Fprintf(io.Discard, "%d", sink)
}

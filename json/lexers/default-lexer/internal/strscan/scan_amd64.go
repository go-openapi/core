//go:build amd64

package strscan

//go:generate sh -c "cd _asm && go run . -out ../stringstop_amd64.s"

// stringStopIndexAVX2 is the avo-generated AVX2 kernel (stringstop_amd64.s): it returns the index of the first byte <
// 0x20, '"' (0x22) or '\' (0x5c), or len(data) if none, scanning 32 bytes per YMM iteration.
//
// It uses AVX/AVX2/BMI instructions and must only be called when [useAVX2] is true.
func stringStopIndexAVX2(data []byte) int

// avx2Min is the minimum remaining-byte count at which the AVX2 kernel beats the 8-byte SWAR loop, once its 3-constant
// broadcast setup and the (non-inlinable) call are accounted for.
//
// Below it, ScanStop stays on SWAR.
// Tunable — set from the internal/simd head-to-head and the end-to-end sweep (plan §9.3).
const avx2Min = 32

// ScanStop returns the index of the first JSON string-stop byte (a control char < 0x20, '"', or '\') in data, or
// len(data) if none.
//
// It is called only for the long-string case (the caller found a clean first word), so it dispatches to the AVX2 kernel
// when the CPU supports it and enough bytes remain, and to SWAR otherwise.
// The WithoutAVX2 knob is handled by the caller (it simply never delegates here), so ScanStop always tries the kernel.
func ScanStop(data []byte) int {
	if useAVX2 && len(data) >= avx2Min {
		return stringStopIndexAVX2(data)
	}

	return scanStopSWAR(data)
}

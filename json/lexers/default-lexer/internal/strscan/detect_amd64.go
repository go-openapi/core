// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

//go:build amd64

package strscan

// cpuid executes CPUID with the given leaf (EAX) and subleaf (ECX) and returns the four result registers.
//
// Implemented in cpuid_amd64.s.
func cpuid(eaxArg, ecxArg uint32) (eax, ebx, ecx, edx uint32)

// xgetbv reads the extended control register XCR0 (ECX=0) and returns its low and high halves.
//
// Implemented in cpuid_amd64.s. Only call after CPUID reports OSXSAVE, or XGETBV itself faults.
func xgetbv() (eax, edx uint32)

// useAVX2 records, once at package init, whether this CPU supports AVX2 with the OS having enabled YMM state.
//
// This is the precondition for calling stringStopIndexAVX2.
// On any machine that fails the check, ScanStop stays on the SWAR path and the AVX2 instructions are never executed.
var useAVX2 = detectAVX2() //nolint:gochecknoglobals // private immutable runtime environment detection

//nolint:mnd // avx offsets
func detectAVX2() bool {
	const (
		osxsave = uint32(1) << 27 // CPUID.1:ECX.OSXSAVE — OS enabled XSAVE/XGETBV
		avx     = uint32(1) << 28 // CPUID.1:ECX.AVX
		avx2bit = uint32(1) << 5  // CPUID.(7,0):EBX.AVX2
		xcr0YMM = uint32(0x6)     // XCR0 bits 1 (XMM) + 2 (YMM) must both be set
	)

	if maxLeaf, _, _, _ := cpuid(0, 0); maxLeaf < 7 {
		return false
	}
	if _, _, ecx1, _ := cpuid(1, 0); ecx1&osxsave == 0 || ecx1&avx == 0 {
		return false
	}
	if xcr0, _ := xgetbv(); xcr0&xcr0YMM != xcr0YMM {
		return false
	}
	_, ebx7, _, _ := cpuid(7, 0) //nolint:dogsled // we need 4 registers

	return ebx7&avx2bit != 0
}

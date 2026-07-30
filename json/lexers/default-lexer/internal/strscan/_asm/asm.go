// Command asm generates the AVX2 string-stop kernel (../stringstop_amd64.s) via avo.
//
// Run through `go generate ./internal/strscan` (see ../scan_amd64.go).
// The emitted .s has no avo import; the func declaration is hand-written and documented in ../scan_amd64.go, so this
// generator writes only the .s (no -stubs).
//
//nolint:revive,mnd
package main

import (
	. "github.com/mmcloughlin/avo/build"
	. "github.com/mmcloughlin/avo/operand"
	"github.com/mmcloughlin/avo/reg"
)

func main() {
	stringStop()

	Generate()
}

func stringStop() {
	TEXT("stringStopIndexAVX2", NOSPLIT, "func(data []byte) (int, bool)")
	Doc(
		"stringStopIndexAVX2 returns the index of the first byte that is < 0x20, '\"' (0x22) or '\\' (0x5c), or len(data) if none, and whether any byte BEFORE that index has its high bit set (i.e. the scanned run is not pure ASCII). AVX2, 32 bytes/iter.",
	)
	ptr := Load(Param("data").Base(), GP64())
	n := Load(Param("data").Len(), GP64())

	// nonascii accumulates "some scanned byte was >= 0x80". In the vector loop it collects VPMOVMSKB sign-bit masks;
	// in the scalar tail it collects the byte's 0x80 bit. Only its zero/non-zero state is ever read, so mixing the two
	// encodings is safe.
	nonascii := GP32()
	XORL(nonascii, nonascii)

	c1f := YMM()
	VPBROADCASTB(ConstData("c1f", U8(0x1f)), c1f)
	c22 := YMM()
	VPBROADCASTB(ConstData("c22", U8(0x22)), c22)
	c5c := YMM()
	VPBROADCASTB(ConstData("c5c", U8(0x5c)), c5c)

	i := GP64()
	XORQ(i, i)

	Label("loop32")
	t := GP64()
	LEAQ(Mem{Base: i, Disp: 32}, t)
	CMPQ(t, n)
	JG(LabelRef("tail"))

	data := YMM()
	VMOVDQU(Mem{Base: ptr, Index: i, Scale: 1}, data)
	mn := YMM()
	VPMINUB(data, c1f, mn) // min(b, 0x1f)
	ctrl := YMM()
	VPCMPEQB(mn, data, ctrl) // b <= 0x1f (unsigned; no false positive on >=0x80)
	q := YMM()
	VPCMPEQB(data, c22, q)
	bs := YMM()
	VPCMPEQB(data, c5c, bs)
	acc := YMM()
	VPOR(ctrl, q, acc)
	VPOR(acc, bs, acc)
	// the sign bit of every lane IS "byte >= 0x80", so the non-ASCII test is one extra VPMOVMSKB on the block already
	// loaded — off the loop's exit-test dependency chain.
	hmask := GP32()
	VPMOVMSKB(data, hmask)
	mask := GP32()
	VPMOVMSKB(acc, mask)
	TESTL(mask, mask)
	JNZ(LabelRef("found"))
	ORL(hmask, nonascii) // whole block is string content: account for all 32 lanes
	ADDQ(Imm(32), i)
	JMP(LabelRef("loop32"))

	Label("found")
	off := GP64()
	TZCNTL(mask, off.As32()) // bit index 0..31, zero-extends to 64
	// Only the lanes BEFORE the stop belong to the value, so keep the bits of hmask strictly below mask's lowest set
	// bit: (mask-1) &^ mask. Four plain ALU ops — deliberately not BMI's BLSMSK/ANDN, since the CPUID gate here checks
	// AVX2 only (TZCNT above is safe because it decodes as BSF, which agrees for a non-zero operand; ANDN has no such
	// fallback).
	below := GP32()
	MOVL(mask, below)
	DECL(below)
	notMask := GP32()
	MOVL(mask, notMask)
	NOTL(notMask)
	ANDL(notMask, below)
	ANDL(below, hmask)
	ORL(hmask, nonascii)
	ADDQ(off, i)
	Store(i, ReturnIndex(0))
	storeNonASCII(nonascii)
	VZEROUPPER()
	RET()

	Label("tail")
	Label("tailloop")
	CMPQ(i, n)
	JGE(LabelRef("notfound"))
	b := GP32()
	MOVBLZX(Mem{Base: ptr, Index: i, Scale: 1}, b)
	CMPL(b, Imm(0x20))
	JL(LabelRef("foundtail"))
	CMPL(b, Imm(0x22))
	JE(LabelRef("foundtail"))
	CMPL(b, Imm(0x5c))
	JE(LabelRef("foundtail"))
	ANDL(Imm(0x80), b) // keep only "is this byte >= 0x80"; b is dead after the comparisons
	ORL(b, nonascii)
	INCQ(i)
	JMP(LabelRef("tailloop"))

	Label("foundtail")
	Store(i, ReturnIndex(0))
	storeNonASCII(nonascii)
	VZEROUPPER()
	RET()

	Label("notfound")
	Store(n, ReturnIndex(0))
	storeNonASCII(nonascii)
	VZEROUPPER()
	RET()
}

// storeNonASCII writes (nonascii != 0) to the bool result.
func storeNonASCII(nonascii reg.GPVirtual) {
	TESTL(nonascii, nonascii)
	flag := GP8()
	SETNE(flag)
	Store(flag, ReturnIndex(1))
}


// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package swar

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// word builds a little-endian word from 8 lane bytes (lane[0] is the lowest address / lowest-order byte, matching
// binary.LittleEndian.Uint64).
func word(lanes [8]byte) uint64 {
	var w uint64
	for i, b := range lanes {
		w |= uint64(b) << (8 * i)
	}

	return w
}

// checkMask verifies that f flags exactly the lanes for which pred is true, by sweeping every byte value through every
// lane position over a non-matching background.
//
// The background byte 'A' (0x41) is not flagged by any predicate under test, so a stray cross-lane bit would be caught.
func checkMask(t *testing.T, name string, f func(uint64) uint64, pred func(byte) bool) {
	t.Helper()

	const bg byte = 'A'
	for lane := range 8 {
		for v := range 256 {
			b := byte(v)
			var lanes [8]byte
			for i := range lanes {
				lanes[i] = bg
			}
			lanes[lane] = b
			w := word(lanes)

			mask := f(w)
			for i := range 8 {
				got := mask&(0x80<<(8*i)) != 0
				wantByte := bg
				if i == lane {
					wantByte = b
				}
				want := pred(wantByte)
				if got != want {
					t.Fatalf("%s: lane %d byte %#02x (mask %#016x): got flagged=%v want %v",
						name, i, b, mask, got, want)
				}
			}
		}
	}
}

func TestStringStopMask(t *testing.T) {
	checkMask(t, "StringStopMask", StringStopMask, func(b byte) bool {
		return b < 0x20 || b == '"' || b == '\\'
	})
}

func TestMaskEqual(t *testing.T) {
	for _, needle := range []byte{0, '"', '\\', '0', '9', 0x7f, 0x80, 0xff} {
		n := needle
		checkMask(t, "MaskEqual", func(w uint64) uint64 { return MaskEqual(w, n) },
			func(b byte) bool { return b == n })
	}
}

func TestMaskLess(t *testing.T) {
	for _, n := range []byte{0x01, 0x20, '0', 0x80, 0xff} {
		nn := n
		checkMask(t, "MaskLess", func(w uint64) uint64 { return MaskLess(w, nn) },
			func(b byte) bool { return b < nn })
	}
}

func TestMaskGreater(t *testing.T) {
	for _, n := range []byte{0x00, 0x20, '9', 0x7f, 0xfe} {
		nn := n
		checkMask(t, "MaskGreater", func(w uint64) uint64 { return MaskGreater(w, nn) },
			func(b byte) bool { return b > nn })
	}
}

func TestDigitMask(t *testing.T) {
	checkMask(t, "DigitMask", DigitMask, func(b byte) bool {
		return b >= '0' && b <= '9'
	})
}

func TestNonDigitMask(t *testing.T) {
	checkMask(t, "NonDigitMask", NonDigitMask, func(b byte) bool {
		return b < '0' || b > '9'
	})
}

func TestFirstByte(t *testing.T) {
	// a stop byte placed at each lane (with clean bytes before it) must be located
	for lane := range 8 {
		var lanes [8]byte
		for i := range lanes {
			lanes[i] = 'x'
		}
		lanes[lane] = '"'
		mask := StringStopMask(word(lanes))
		if got := FirstByte(mask); got != lane {
			t.Fatalf("FirstByte: stop at lane %d, got %d (mask %#016x)", lane, got, mask)
		}
	}

	// with two stops, the lowest-address one wins
	mask := StringStopMask(word([8]byte{'x', '\\', 'x', '"', 'x', 'x', 'x', 'x'}))
	if got := FirstByte(mask); got != 1 {
		t.Fatalf("FirstByte: two stops, want lowest lane 1, got %d", got)
	}
}

// TestInlinable is the inline gate (plan §3): every exported mask primitive must stay within the inliner budget,
// otherwise the call sites pay a call per word and the consolidation is a net loss.
//
// It asks the compiler which funcs it can inline and fails if any primitive is missing.
func TestInlinable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping inline gate in -short")
	}

	ctx := context.Background()
	out, err := exec.CommandContext(ctx, "go", "build", "-gcflags=-m", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go build -gcflags=-m: %v\n%s", err, out)
	}

	must := []string{
		"FirstByte", "Broadcast", "StringStopMask",
		"MaskEqual", "MaskLess", "MaskGreater", "DigitMask", "NonDigitMask",
	}
	text := string(out)
	for _, fn := range must {
		if !strings.Contains(text, "can inline "+fn) {
			t.Errorf(
				"inline gate: compiler will NOT inline %q\n--- gcflags -m output ---\n%s",
				fn,
				text,
			)
		}
	}
}

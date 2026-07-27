//go:build amd64

package strscan

import (
	"os"
	"strings"
	"testing"
)

// TestDetectAVX2AgainstCPUInfo cross-checks our CPUID-based detection against the kernel's own view (/proc/cpuinfo) so
// a wrong bit test can't silently ship — a false positive would SIGILL on a non-AVX2 machine.
func TestDetectAVX2AgainstCPUInfo(t *testing.T) {
	raw, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		t.Skipf("no /proc/cpuinfo: %v", err)
	}
	var kernelAVX2 bool
	for line := range strings.SplitSeq(string(raw), "\n") {
		if strings.HasPrefix(line, "flags") && strings.Contains(line, " avx2") {
			kernelAVX2 = true

			break
		}
	}
	if got := detectAVX2(); got != kernelAVX2 {
		t.Fatalf("detectAVX2()=%v but /proc/cpuinfo avx2=%v", got, kernelAVX2)
	}
	t.Logf("AVX2 detected: %v (useAVX2=%v)", kernelAVX2, useAVX2)
}

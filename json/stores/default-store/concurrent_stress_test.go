// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-openapi/core/json/stores"
	"github.com/go-openapi/core/json/stores/values"
)

// TestConcurrentStoreStress is a high-intensity guard against concurrent corruption in
// [ConcurrentStore]. It hammers the store with a mixed workload that deliberately exercises the paths
// most prone to aliasing bugs:
//
//   - large floats with e+NN / e-NN exponents (BCD-encoded into the arena — the '+' nibble was the
//     signature of an earlier corruption),
//   - long strings (both stored verbatim in the arena and DEFLATE-compressed),
//   - concurrent putters that register (handle, expected) pairs and concurrent getters that resolve
//     random previously-registered handles and verify them.
//
// It is intentionally cheap enough to run in the normal suite, and most valuable under `-race`. On a
// mismatch it dumps the offending handle and bytes (and a second read, to tell a stable corruption
// from a transient one) so a recurrence is actionable rather than a one-off glimpse.
//
// Background: a single, never-reproduced failure with the `e+20`→`e020` signature motivated a full
// audit of the concurrent paths (all arena writes are under the write lock, all large-value reads
// decode into fresh buffers, and the slice pool hands distinct buffers to concurrent borrowers). The
// audit found the synchronization correct; this test exists to catch any regression, or that elusive
// failure, with diagnostics.
func TestConcurrentStoreStress(t *testing.T) {
	const (
		putters       = 16
		getters       = 16
		opsPerPutter  = 400
		opsPerGetter  = 400
		distinctValue = 64
	)

	s := NewConcurrent()
	samples := stressSamples(distinctValue)

	type registered struct {
		h    stores.Handle
		want string
	}

	var (
		mu       sync.RWMutex
		registry []registered
		mismatch atomic.Int64
		firstBad sync.Once
		report   string
	)

	record := func(h stores.Handle, want string) {
		mu.Lock()
		registry = append(registry, registered{h, want})
		mu.Unlock()
	}

	pick := func() (registered, bool) {
		mu.RLock()
		defer mu.RUnlock()
		if len(registry) == 0 {
			return registered{}, false
		}
		return registry[rand.IntN(len(registry))], true //nolint:gosec // G404: rand is okay for tests
	}

	var wg sync.WaitGroup

	for range putters {
		wg.Go(func() {
			for range opsPerPutter {
				sample := samples[rand.IntN(len(samples))] //nolint:gosec // G404: rand is okay for tests
				h := s.PutValue(sample.v)
				record(h, sample.want)
			}
		})
	}

	for range getters {
		wg.Go(func() {
			var scratch []byte
			for range opsPerGetter {
				r, ok := pick()
				if !ok {
					continue
				}
				got := stressString(s.Get(r.h))

				var v values.Value
				v, scratch = s.AppendValueBytes(scratch[:0], r.h)
				gotAppend := string(v.Bytes())

				if got != r.want || gotAppend != r.want {
					mismatch.Add(1)
					firstBad.Do(func() {
						reread := stressString(s.Get(r.h))
						report = fmt.Sprintf(
							"handle=%#x\n want      =%q\n Get       =%q\n Append    =%q\n Get(retry)=%q",
							uint64(r.h),
							r.want,
							got,
							gotAppend,
							reread,
						)
					})
				}
			}
		})
	}

	wg.Wait()

	if n := mismatch.Load(); n > 0 {
		t.Fatalf("concurrent corruption: %d mismatches\n%s", n, report)
	}
}

type stressSample struct {
	want string
	v    values.Value
}

//nolint:gosec // G404: rand is okay for tests
func stressSamples(n int) []stressSample {
	out := make([]stressSample, 0, n)
	for i := range n {
		var v values.Value
		switch i % 4 {
		case 0:
			// large float: produces e+NN / e-NN exponents that go through the BCD arena path
			v = values.MakeFloatValue(rand.Float64() * 1e21)
		case 1:
			// long string, compressible -> DEFLATE path (> default threshold 128)
			v = values.MakeStringValue(stressRepeat(rand.IntN(200) + 130))
		case 2:
			// medium string, stored verbatim in the arena (9..128)
			v = values.MakeStringValue(randomAlphanumeric(rand.IntN(100) + 20))
		default:
			// small / inlined values
			v = values.MakeUintegerValue(rand.Uint64N(1 << 40))
		}
		out = append(out, stressSample{want: stressString(v), v: v})
	}
	return out
}

func stressString(v values.Value) string {
	if s := v.String(); s != "" {
		return s
	}
	return string(v.Bytes())
}

func stressRepeat(size int) string {
	const seed = "the quick brown fox jumps over the lazy dog "
	b := make([]byte, 0, size)
	for len(b) < size {
		b = append(b, seed...)
	}
	return string(b[:size])
}

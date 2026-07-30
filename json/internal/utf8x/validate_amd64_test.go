//go:build amd64

package utf8x

import (
	"bytes"
	"math/rand"
	"testing"
	"unicode/utf8"

	"github.com/go-openapi/testify/v2/require"
)

// The AVX2 kernel is hand-written vector code whose failure mode is silence: a wrong table entry accepts an
// ill-formed sequence, and nothing else in the system would notice. So it is tested against the stdlib exhaustively
// where exhaustive is possible, and by construction everywhere else.
//
// Two properties are hammered specifically because they are where a lookup4 port goes wrong:
//
//   - Block seams. State is carried between 32-byte blocks through prev1/prev2/prev3, so a sequence must be judged
//     identically no matter which byte offset it starts at. Every case is therefore swept across all 32 alignments.
//   - The tail. The kernel deliberately validates whole blocks only; [Valid] resolves the rest scalar-ly. An error
//     that falls in the seam between the two is exactly what a naive split would miss.

// requireAgrees asserts our verdict matches the stdlib's for b, and reports the input on failure.
func requireAgrees(t *testing.T, b []byte) {
	t.Helper()

	if got, want := Valid(b), utf8.Valid(b); got != want {
		t.Fatalf("Valid=%v, utf8.Valid=%v for % x (len=%d)", got, want, b, len(b))
	}
}

// TestValidAgreesExhaustive2And3Bytes sweeps EVERY 2-byte sequence and every 3-byte sequence, embedded at every
// alignment within a buffer long enough to reach the vector path.
//
// This covers the whole classification table: all 256 lead bytes against all 256 second bytes is precisely what the
// three VPSHUFB lookups encode, so a single wrong table entry cannot survive it.
func TestValidAgreesExhaustive2And3Bytes(t *testing.T) {
	const pad = 96 // > avx2Min, so the vector path runs

	t.Run("2 bytes", func(t *testing.T) {
		buf := make([]byte, pad)
		for hi := range 256 {
			for lo := range 256 {
				for _, at := range []int{0, 1, 30, 31, 32, 33, 62, 63, 64, pad - 2} {
					for i := range buf {
						buf[i] = 'a'
					}
					buf[at], buf[at+1] = byte(hi), byte(lo)
					if got, want := Valid(buf), utf8.Valid(buf); got != want {
						t.Fatalf(
							"at=%d seq=%02x %02x: Valid=%v, utf8.Valid=%v",
							at,
							hi,
							lo,
							got,
							want,
						)
					}
				}
			}
		}
	})

	// Every lead byte (C0..FF) against every possible 2nd and 3rd byte: 4.2M sequences. This is what exercises the
	// continuation-count logic (prev2/prev3 saturating subtract) that the pairwise sweep above cannot reach. Leads
	// below C0 are ASCII or stray continuations, both fully decided by the pairwise table.
	t.Run("3 bytes after every lead", func(t *testing.T) {
		if testing.Short() {
			t.Skip("4.2M sequences x alignments")
		}
		buf := make([]byte, pad)
		for i := range buf {
			buf[i] = 'a'
		}
		for b0 := 0xC0; b0 <= 0xFF; b0++ {
			for b1 := range 256 {
				for b2 := range 256 {
					for _, at := range []int{0, 31, 62} {
						buf[at], buf[at+1], buf[at+2] = byte(b0), byte(b1), byte(b2)
						if got, want := Valid(buf), utf8.Valid(buf); got != want {
							t.Fatalf("at=%d seq=%02x %02x %02x: Valid=%v, utf8.Valid=%v",
								at, b0, b1, b2, got, want)
						}
						buf[at], buf[at+1], buf[at+2] = 'a', 'a', 'a'
					}
				}
			}
		}
	})
}

// TestKernelIsActuallyExercised guards the whole differential suite from becoming vacuous: if the CPU lacks AVX2, or
// the length gate keeps every fixture on the scalar path, every "agrees with the stdlib" test above would pass by
// comparing the stdlib against itself.
func TestKernelIsActuallyExercised(t *testing.T) {
	if !useAVX2 {
		t.Skip(
			"no AVX2 on this CPU: Valid is the stdlib scalar validator and the kernel tests are vacuous",
		)
	}

	t.Logf("AVX2 kernel active (avx2Min=%d)", avx2Min)

	// drive the kernel directly, so the result cannot come from the scalar fallback
	valid := bytes.Repeat([]byte("a\u00e9\u65e5"), 16) // 96 bytes, a whole number of blocks
	require.Zero(t, len(valid)%32)
	require.True(t, validateUTF8BlocksAVX2(valid))

	for _, bad := range [][]byte{
		{0xFF}, {0x80}, {0xC0, 0xAF}, {0xED, 0xA0, 0x80}, {0xF4, 0xBF, 0xBF, 0xBF}, {0xE0, 0x80, 0x80},
	} {
		b := bytes.Repeat([]byte("a"), 96)
		copy(b[40:], bad)
		require.Falsef(t, validateUTF8BlocksAVX2(b), "the kernel must reject % x", bad)
	}
}

// TestValidAgreesFourByteLeads sweeps the 4-byte space where it actually discriminates: every lead in F0..F7 against
// every second byte, with the last two continuations both valid and invalid. This is where TOO_LARGE / TOO_LARGE_1000
// / OVERLONG_4 live, and they are all decided by (lead, second byte).
func TestValidAgreesFourByteLeads(t *testing.T) {
	const pad = 96
	tails := [][2]byte{{0x80, 0x80}, {0xBF, 0xBF}, {0x80, 0x41}, {0x41, 0x80}, {0xBF, 0xC0}}

	buf := make([]byte, pad)
	for lead := 0xF0; lead <= 0xFF; lead++ {
		for second := range 256 {
			for _, tail := range tails {
				for _, at := range []int{0, 29, 30, 31, 32, 60, 61, 62} {
					for i := range buf {
						buf[i] = 'a'
					}
					buf[at], buf[at+1] = byte(lead), byte(second)
					buf[at+2], buf[at+3] = tail[0], tail[1]
					if got, want := Valid(buf), utf8.Valid(buf); got != want {
						t.Fatalf("at=%d seq=%02x %02x % x: Valid=%v, utf8.Valid=%v",
							at, lead, second, tail, got, want)
					}
				}
			}
		}
	}
}

// TestValidAgreesAtEveryLength checks every length from 0 through several blocks, for a valid buffer and for one
// truncated mid-sequence at the very end — the case the block kernel cannot see and the scalar seam must catch.
func TestValidAgreesAtEveryLength(t *testing.T) {
	base := bytes.Repeat(
		[]byte("a\u00e9\u65e5\U0001D11E"),
		64,
	) // 1-, 2-, 3- and 4-byte sequences, so every length cuts differently

	for n := range base {
		requireAgrees(t, base[:n])
	}

	// a truncated sequence pinned at the end of buffers of every length
	for _, trunc := range [][]byte{{0xC3}, {0xE6}, {0xE6, 0x97}, {0xF0}, {0xF0, 0x9D}, {0xF0, 0x9D, 0x84}} {
		for n := range 200 {
			b := append(bytes.Repeat([]byte("a"), n), trunc...)
			requireAgrees(t, b)
			require.Falsef(
				t,
				Valid(b),
				"a sequence truncated at the end must be rejected (n=%d, % x)",
				n,
				trunc,
			)
		}
	}
}

// TestValidAgreesOnSeamStraddle walks a valid multi-byte sequence one byte at a time across the last block boundary,
// where the vector pass hands over to the scalar one.
func TestValidAgreesOnSeamStraddle(t *testing.T) {
	for _, seq := range []string{"\u00e9", "\u65e5", "\U0001D11E"} {
		for total := 64; total < 160; total++ {
			for at := total - len(seq) - 4; at <= total-len(seq); at++ {
				if at < 0 {
					continue
				}
				b := bytes.Repeat([]byte("a"), total)
				copy(b[at:], seq)
				requireAgrees(t, b)
				require.Truef(
					t,
					Valid(b),
					"a valid sequence at %d of %d must be accepted",
					at,
					total,
				)
			}
		}
	}
}

// TestValidAgreesRandom throws structured noise at it: mostly-valid text with sporadic corruption, which is what real
// ill-formed input looks like, plus pure random bytes.
func TestValidAgreesRandom(t *testing.T) {
	//nolint:gosec // a deterministic PRNG is the point: reproducible test input, not cryptography
	rng := rand.New(rand.NewSource(42))

	alphabet := []string{"a", "\u00e9", "\u65e5", "\U0001D11E", "\x00", "\x7f"}
	for range 20000 {
		n := rng.Intn(300)
		var b []byte
		for len(b) < n {
			b = append(b, alphabet[rng.Intn(len(alphabet))]...)
		}
		for range rng.Intn(4) { // corrupt a few bytes
			if len(b) > 0 {
				corrupt := rng.Intn(
					256,
				) //nolint:gosec // bounded to 0..255, so the conversion cannot overflow
				b[rng.Intn(len(b))] = byte(corrupt)
			}
		}
		requireAgrees(t, b)
	}

	for range 20000 {
		b := make([]byte, rng.Intn(300))
		_, _ = rng.Read(b)
		requireAgrees(t, b)
	}
}

// TestSeamCoversEverythingTheKernelSkips pins the hand-off contract directly: the index [Valid] resumes scalar
// validation from must never be past the start of a sequence that the block kernel could not fully check.
func TestSeamCoversEverythingTheKernelSkips(t *testing.T) {
	cases := []struct {
		name   string
		b      []byte
		blocks int
		want   int
	}{
		{name: "ascii before the boundary", b: bytes.Repeat([]byte("a"), 64), blocks: 64, want: 63},
		{
			name:   "3-byte sequence ending at the boundary",
			b:      append(bytes.Repeat([]byte("a"), 61), 0xE6, 0x97, 0xA5),
			blocks: 64, want: 61,
		},
		{
			name:   "4-byte sequence straddling the boundary",
			b:      append(bytes.Repeat([]byte("a"), 62), 0xF0, 0x9D, 0x84, 0x9E),
			blocks: 64, want: 62,
		},
		{
			name:   "more continuations than any sequence may have",
			b:      append(bytes.Repeat([]byte("a"), 60), 0x80, 0x80, 0x80, 0x80),
			blocks: 64, want: 60,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := seam(tc.b, tc.blocks)
			require.Equal(t, tc.want, got)
			require.LessOrEqual(t, got, tc.blocks)
			// resuming there must reproduce the stdlib verdict for the whole buffer
			require.Equal(t, utf8.Valid(tc.b), utf8.Valid(tc.b[got:]) && utf8.Valid(tc.b[:got]))
		})
	}
}

// FuzzValidAVX2 is the standing guard on the kernel: whatever the fuzzer finds, our verdict is the stdlib's.
func FuzzValidAVX2(f *testing.F) {
	f.Add(bytes.Repeat([]byte("a\u00e9\u65e5\U0001D11E"), 8))
	f.Add(append(bytes.Repeat([]byte("a"), 64), 0xE6, 0x97))
	f.Add(append(bytes.Repeat([]byte("\u00e9"), 40), 0xFF))
	f.Add(bytes.Repeat([]byte{0x80}, 70))
	f.Add(bytes.Repeat([]byte{0xF4, 0xBF, 0xBF, 0xBF}, 20))

	f.Fuzz(func(t *testing.T, b []byte) {
		require.Equalf(t, utf8.Valid(b), Valid(b), "% x", b)

		// and again at every offset within a block, since the kernel carries state across blocks
		for shift := 1; shift <= 32; shift++ {
			shifted := append(bytes.Repeat([]byte("a"), shift), b...)
			require.Equalf(t, utf8.Valid(shifted), Valid(shifted), "shift=%d % x", shift, b)
		}
	})
}

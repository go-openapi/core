//go:build !amd64

package strscan

// ScanStop returns the index of the first JSON string-stop byte (a control char < 0x20, '"', or '\') in data, or
// len(data) if none, together with whether any byte before that index is >= 0x80.
//
// On non-amd64 there is no vector kernel, so the long-string path is the same 8-byte SWAR scan the inline fast path
// uses.
func ScanStop(data []byte) (int, bool) {
	return scanStopSWAR(data)
}

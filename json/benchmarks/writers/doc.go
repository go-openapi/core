// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

// Package writers compares the throughput and allocations of the in-repo
// default-writer implementations (unbuffered, buffered, YAML) against
// mailru/easyjson's jwriter, the writer our design is inspired from.
//
// Methodology: each real-world corpus document is lexed once (outside the timed
// loop) into a stable slice of tokens, separators included. The benchmark then
// replays that identical token stream through each writer and measures the cost
// of *producing* the output. b.SetBytes is the number of bytes each writer
// emits, so the reported MB/s is output throughput.
//
// The JSON writers (ours + easyjson) are validated to round-trip the corpus in
// TestReplayRoundTrip. The YAML writer emits YAML, not JSON, so it is benchmarked
// but excluded from the round-trip check.
package writers

#!/usr/bin/env bash
# Regenerate the Azure-lanes benchmark data (benchmark.txt) driving the benchviz chart.
#
# Runs BenchmarkAzureLanes twice — a PLAIN build and a PGO build with hot-loop
# alignment (-pgo + -gcflags=all=-d=alignhot=32; PCALIGN is PGO-gated, see the lexer
# README "Performance and PGO") — median-collapses each to one line per lane, tags them
# /base/ vs /pgo/, and concatenates.
#
# Run from the benchmark module root:  ./benchviz/azure_lanes/regen.sh
set -euo pipefail
cd "$(dirname "$0")/../.."                       # -> benchmark module root
OUT=benchviz/azure_lanes
COUNT=${COUNT:-6}
BENCH='BenchmarkAzureLanes'

echo ">> [1/4] plain build: $COUNT runs"
go test -run '^$' -bench "$BENCH" -benchmem -count="$COUNT" . > "$OUT/raw_base.txt"

echo ">> [2/4] CPU profile for PGO"
go test -run '^$' -bench "$BENCH" -benchtime=300x -count=1 -cpuprofile="$OUT/azure.pgo" . >/dev/null

echo ">> [3/4] PGO build (alignhot): $COUNT runs"
go test -run '^$' -bench "$BENCH" -benchmem -count="$COUNT" \
    -pgo="$OUT/azure.pgo" -gcflags=all=-d=alignhot=32 . > "$OUT/raw_pgo.txt"

echo ">> [4/4] median-collapse + tag + concat -> benchmark.txt"
awk -v tag=base -f "$OUT/median.awk" "$OUT/raw_base.txt"  > "$OUT/benchmark.txt"
awk -v tag=pgo  -f "$OUT/median.awk" "$OUT/raw_pgo.txt"  >> "$OUT/benchmark.txt"
sort -o "$OUT/benchmark.txt" "$OUT/benchmark.txt"
echo ">> done: $OUT/benchmark.txt ($(wc -l < "$OUT/benchmark.txt") lines)"

# Render (from $OUT):  benchviz -c azure_lanes.yaml -png -o azure_lanes.png benchmark.txt
# In a sandboxed browser, render HTML + inline echarts + screenshot (see README.md).

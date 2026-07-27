// Package workloads vendors the real-world JSON datasets used to drive the
// lexer benchmarks (the same corpus the writer benchmarks use; see
// testdata/SOURCE.md), decompressed in memory.
package workloads

import (
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

//go:embed testdata/*.json.gz
var corpusFS embed.FS

// Workload is a named JSON payload.
type Workload struct {
	Name string
	Data []byte
}

// Corpus returns the vendored real-world JSON datasets, decompressed in memory,
// sorted by name.
func Corpus() ([]Workload, error) {
	entries, err := corpusFS.ReadDir("testdata")
	if err != nil {
		return nil, err
	}

	var out []Workload
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json.gz") {
			continue
		}

		data, err := readGzip(path.Join("testdata", name))
		if err != nil {
			return nil, fmt.Errorf("loading corpus %s: %w", name, err)
		}

		out = append(out, Workload{
			Name: strings.TrimSuffix(name, ".json.gz"),
			Data: data,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

func readGzip(name string) ([]byte, error) {
	f, err := corpusFS.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()

	return io.ReadAll(gz)
}

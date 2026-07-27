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

//go:embed standard/*.json.gz
var corpusFS embed.FS

//
//go:embed additional/*.json.gz
var additionCorpusFS embed.FS

// Corpus returns the vendored real-world JSON datasets (see standard/SOURCE.md),
// decompressed in memory. These complement the synthetic workloads from [All]
// with documents whose shapes mirror production payloads.
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

// Suite returns the full benchmark suite: the synthetic workloads from [All]
// followed by the vendored real-world [Corpus].
func Suite() ([]Workload, error) {
	corpus, err := Corpus()
	if err != nil {
		return nil, err
	}

	return append(All(), corpus...), nil
}

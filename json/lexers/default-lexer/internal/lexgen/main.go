// Command lexgen monomorphizes the lexer's generic scan cores onto each concrete policy, so the per-token policy calls
// devirtualize and inline.
//
// Why: the cores scanTokenG / scanPushG / errCheckG are parameterized over [T any, P emitPolicy[T]] and call p.emit /
// p.none / p.eof through the type parameter.
// Go does not devirtualize a type-parameter method call: it routes through the generics dictionary (an indirect call
// the compiler cannot inline), costing ~5% on the semantic lexer.
//
// Bound to a concrete policy value, the same calls become direct and inline. lexgen lifts each generic core into a
// plain, non-generic function per policy — the generic cores stay the single source of truth, and the generated
// copies are verbatim bodies (only the signature and the intra-core errCheckG call are rewritten), so they cannot
// drift.
//
// Output: scan_gen.go in the package directory.
// No build tags and no dispatch layer: the generic cores and the generated concrete cores both stay in the package and
// are both reachable, so a benchmark can drive each and measure the devirtualization gap in one binary.
//
// Usage (via go:generate, run from the package directory):
//
//	//go:generate go run ./internal/lexgen
//
//nolint:err113 // not relevant for an internal codegen utility.
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const (
	outFile    = "scan_gen.go"
	goFilePerm = 0o644
)

// variant is one concrete policy instantiation: the function-name suffix, the emitted token type and the policy type
// substituted for [T] and [P].
type variant struct{ suffix, tok, pol string }

var variants = []variant{ //nolint:gochecknoglobals // static lookup
	{suffix: "Semantic", tok: "token.T", pol: "semanticPolicy"},
	// Verbatim is the state-based verbatim lexer VL: emits the light token.T (like Semantic) but tracks position and
	// stashes blanks in lexer state (§10.5b).
	// It replaced the original token.VT-based verbatim lexer.
	{suffix: "Verbatim", tok: "token.T", pol: "verbatimPolicy"},
}

// core describes a generic function to monomorphize: its name, the exact generic signature line to replace (a drift
// guard — if the source no longer contains it verbatim, generation fails loudly), and the concrete signature + name
// per variant.
type core struct {
	name     string
	file     string // the core_*.go source the generic core lives in (for the edit hint)
	genSig   string
	concName func(variant) string
	concSig  func(variant) string
}

var cores = []core{ //nolint:gochecknoglobals // static lookup
	{
		name:     "scanTokenBufferG",
		file:     "core_pull_buffer.go",
		genSig:   "func scanTokenBufferG[T any, P emitPolicy[T]](l *L, p P) T {",
		concName: func(v variant) string { return "scanTokenBuffer" + v.suffix },
		concSig: func(v variant) string {
			return fmt.Sprintf("func scanTokenBuffer%s(l *L, p %s) %s {", v.suffix, v.pol, v.tok)
		},
	},
	{
		name:     "scanTokenStreamG",
		file:     "core_pull_stream.go",
		genSig:   "func scanTokenStreamG[T any, P emitPolicy[T]](l *L, p P) T {",
		concName: func(v variant) string { return "scanTokenStream" + v.suffix },
		concSig: func(v variant) string {
			return fmt.Sprintf("func scanTokenStream%s(l *L, p %s) %s {", v.suffix, v.pol, v.tok)
		},
	},
	{
		name:     "scanPushG",
		file:     "core_push_buffer.go",
		genSig:   "func scanPushG[T any, P emitPolicy[T]](l *L, p P, yield func(T) bool) {",
		concName: func(v variant) string { return "scanPush" + v.suffix + "Core" },
		concSig: func(v variant) string {
			return fmt.Sprintf(
				"func scanPush%sCore(l *L, p %s, yield func(%s) bool) {",
				v.suffix,
				v.pol,
				v.tok,
			)
		},
	},
	{
		name:     "scanPushStreamG",
		file:     "core_push_stream.go",
		genSig:   "func scanPushStreamG[T any, P emitPolicy[T]](l *L, p P, yield func(T) bool) {",
		concName: func(v variant) string { return "scanPushStream" + v.suffix + "Core" },
		concSig: func(v variant) string {
			return fmt.Sprintf(
				"func scanPushStream%sCore(l *L, p %s, yield func(%s) bool) {",
				v.suffix,
				v.pol,
				v.tok,
			)
		},
	},
	{
		name:     "errCheckG",
		file:     "core.go",
		genSig:   "func errCheckG[T any, P emitPolicy[T]](l *L, p P, err error) T {",
		concName: func(v variant) string { return "errCheck" + v.suffix },
		concSig: func(v variant) string {
			return fmt.Sprintf(
				"func errCheck%s(l *L, p %s, err error) %s {",
				v.suffix,
				v.pol,
				v.tok,
			)
		},
	},
}

// rewriteBody rewrites the intra-core generic calls to their concrete names for the variant.
//
// Only errCheckG is actually called inside the cores; scanTokenG / scanPushG are handled for safety should a future
// core call one.
func rewriteBody(body string, v variant) string {
	r := strings.NewReplacer(
		"errCheckG(", "errCheck"+v.suffix+"(",
		"scanTokenG(", "scanToken"+v.suffix+"(",
		"scanPushG(", "scanPush"+v.suffix+"Core(",
	)

	return r.Replace(body)
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("lexgen: ")

	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fset := token.NewFileSet()

	src := map[string]string{} // generic func name -> verbatim source text

	goFiles, err := filepath.Glob("*.go")
	if err != nil {
		return err
	}
	for _, path := range goFiles {
		if strings.HasSuffix(path, "_test.go") || path == outFile {
			continue
		}
		buf, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, buf, parser.ParseComments)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			for _, c := range cores {
				if fn.Name.Name == c.name {
					src[c.name] = string(
						buf[fset.Position(fn.Pos()).Offset:fset.Position(fn.End()).Offset],
					)
				}
			}
		}
	}

	var out bytes.Buffer
	out.WriteString(header)

	for _, v := range variants {
		for _, c := range cores {
			text, ok := src[c.name]
			if !ok {
				return fmt.Errorf("generic core %q not found in package", c.name)
			}
			if !strings.Contains(text, c.genSig) {
				return fmt.Errorf(
					"signature drift for %q: expected to find\n\t%s\nin source — update lexgen",
					c.name,
					c.genSig,
				)
			}
			text = strings.Replace(text, c.genSig, c.concSig(v), 1)
			text = rewriteBody(text, v)

			fmt.Fprintf(
				&out,
				"\n// %s is generated by lexgen from %s; DO NOT EDIT\n// (edit %s in %s, then re-run go generate).\n//\n//nolint:gocognit,gocyclo\n%s\n",
				c.concName(v),
				c.name,
				c.name,
				c.file,
				text,
			)
		}
	}

	formatted, err := format.Source(out.Bytes())
	if err != nil {
		return fmt.Errorf("formatting generated source: %w\n%s", err, out.Bytes())
	}

	if err := os.WriteFile(outFile, formatted, goFilePerm); err != nil {
		return err
	}

	log.Printf("generated %d functions into %s", len(variants)*len(cores), outFile)

	return nil
}

const header = `// Code generated by lexgen from the core_*.go sources; DO NOT EDIT.
//
// These are the generic scan cores (scanTokenBufferG / scanTokenStreamG /
// scanPushG / scanPushStreamG / errCheckG) monomorphized onto each concrete policy:
// the type parameters are erased and the policy is a concrete value, so the
// per-token p.emit / p.none / p.eof calls are direct and inline (no
// generics-dictionary indirection). Edit the generic cores in their core_*.go
// source (see core.go) and re-run go generate; do not edit this file.

package lexer

import (
	"errors"
	"io"

	codes "github.com/go-openapi/core/json/lexers/error-codes"
	scan "github.com/go-openapi/core/json/lexers/internal/scan"
	"github.com/go-openapi/core/json/lexers/token"
)
`

// gen-collections writes the Postman and Bruno client collections,
// generated from the embedded OpenAPI spec, into the repository. Run it
// from the repo root (or pass -out) after changing the API:
//
//	go run ./services/api/cmd/gen-collections        # writes ./clients/**
//	make collections
//
// The committed output is asserted byte-identical to this generator's
// output by internal/collections' sync test, so it can never drift.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/your-org/deployserver/api/internal/apispec"
	"github.com/your-org/deployserver/api/internal/collections"
)

func main() {
	out := flag.String("out", ".", "repository root to write clients/ into")
	flag.Parse()

	files, err := collections.Generate(apispec.YAML())
	if err != nil {
		fmt.Fprintln(os.Stderr, "generate:", err)
		os.Exit(1)
	}

	// Remove any stale generated files first, so a removed operation does
	// not leave an orphaned .bru behind.
	for _, dir := range []string{"clients/postman", "clients/bruno"} {
		_ = os.RemoveAll(filepath.Join(*out, dir))
	}

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		dst := filepath.Join(*out, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "mkdir:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(dst, files[rel], 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "write:", err)
			os.Exit(1)
		}
		fmt.Println("wrote", rel)
	}
}

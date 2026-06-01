// Command gen_settlement produces a paired internal ledger export (JSONL) and
// external NIBSS settlement file (CSV) with K injected discrepancies spanning
// every reconciliation category. It is seeded so fixtures are reproducible
// (NS-407).
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/reconcile/adapters"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/reconcile/fixture"
)

func main() {
	internalOut := flag.String("internal-out", "out/internal.jsonl", "internal ledger export path (JSONL)")
	externalOut := flag.String("external-out", "out/settlement.csv", "external settlement path (NIBSS CSV)")
	count := flag.Int("count", 100, "number of clean matched transfers")
	perCategory := flag.Int("discrepancies", 3, "discrepancies injected per category")
	seed := flag.Int64("seed", 1, "RNG seed for reproducible output")
	flag.Parse()

	f := fixture.Generate(fixture.Spec{Count: *count, PerCategory: *perCategory, Seed: *seed})

	if err := writeFile(*internalOut, func(w *os.File) error {
		return fixture.WriteJSONL(w, f.Internal)
	}); err != nil {
		log.Fatalf("gen_settlement: write internal: %v", err)
	}
	if err := writeFile(*externalOut, func(w *os.File) error {
		return adapters.WriteNIBSS(w, f.External)
	}); err != nil {
		log.Fatalf("gen_settlement: write external: %v", err)
	}

	fmt.Printf("gen_settlement: wrote %d internal + %d external records (seed=%d)\n",
		len(f.Internal), len(f.External), *seed)
	fmt.Printf("  internal: %s\n  external: %s\n", *internalOut, *externalOut)
	fmt.Printf("  injected discrepancies: %d per category across %d categories\n",
		*perCategory, len(f.ExpectedByCategory))
}

func writeFile(path string, fn func(*os.File) error) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return fn(f)
}

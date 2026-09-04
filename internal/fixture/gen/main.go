// Command gen materialises uzushio's lint fixtures under a directory.
//
// It is the entry point `make generate` calls:
//
//	go run ./internal/fixture/gen -out lint
//
// It writes only the directories internal/fixture declares; the fixtures copied
// from DocDag for the preset's own rules are left where they are.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Kaikei-e/uzushio/internal/fixture"
)

func main() {
	out := flag.String("out", "lint", "directory to write the fixture corpora under")
	flag.Parse()
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "gen: unexpected argument %q\n", flag.Arg(0))
		os.Exit(2)
	}
	if err := fixture.Write(*out); err != nil {
		fmt.Fprintf(os.Stderr, "gen: %v\n", err)
		os.Exit(1)
	}
	names, err := fixture.Names()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen: %v\n", err)
		os.Exit(1)
	}
	for _, name := range names {
		fmt.Fprintf(os.Stdout, "%s/%s\n", *out, name)
	}
}

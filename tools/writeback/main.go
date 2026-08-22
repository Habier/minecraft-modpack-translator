// Writeback generates final translated files from the translation cache + catalog.
//
// Usage:
//
//	go run ./tools/writeback "C:\path\to\modpack"
package main

import (
	"fmt"
	"os"

	"modpack-translator/internal/writeback"
)

const outputDirectory = writeback.OutputDirectory

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: go run ./tools/writeback <modpack-path>\n")
		os.Exit(1)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(modpackPath string) error {
	_, err := writeback.Workspace(modpackPath)
	return err
}

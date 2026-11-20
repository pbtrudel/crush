package main

import (
	"fmt"
	"io"

	"github.com/charmbracelet/crush/internal/eval/catalog"
	"github.com/charmbracelet/crush/internal/eval/home"
)

const catalogUsage = `usage:
  crush-eval catalog freeze`

// catalogCmd pins the model catalog a batch's runs read. The children have
// provider auto-update off, so they serve the catalog the eval home's data
// dir holds; freeze copies the user's cached one there rather than fetching
// it.
func catalogCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "freeze" {
		fmt.Fprintln(stderr, catalogUsage)
		return 2
	}
	h, err := home.Default()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := h.Ensure(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	from, err := catalog.UserDataDir()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	frozen, err := catalog.Freeze(h, from)
	if err != nil {
		fmt.Fprintf(stderr, "freeze the catalog from %s: %v\n", from, err)
		return 1
	}
	fmt.Fprintf(stdout, "%s sha256 %s\n", catalog.ProvidersFile, frozen.Providers)
	if frozen.Hyper != "" {
		fmt.Fprintf(stdout, "%s sha256 %s\n", catalog.HyperFile, frozen.Hyper)
	}
	return 0
}

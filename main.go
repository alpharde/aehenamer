// Command aehenamer renames the files of a folder in bulk, from a terminal
// interface: pick a folder, stack up rename operations, preview the result and
// apply it.
//
// Usage:
//
//	aehenamer [folder]
//
// The folder defaults to the current working directory. Only the files
// directly inside it are listed; subdirectories are never entered.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"aehenamer/internal/ui"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: %s [folder]\n\nBulk file renamer. The folder defaults to the current directory.\n", filepath.Base(os.Args[0]))
	}
	flag.Parse()

	dir := "."
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := ui.New(dir).Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "aehenamer: %v\n", err)
		os.Exit(1)
	}
}

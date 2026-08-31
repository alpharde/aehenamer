// Package files lists the files a rename operates on and applies a finished
// rename plan to disk.
package files

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Entry pairs a file currently on disk with the name proposed for it.
type Entry struct {
	Original string
	Proposed string
}

// pending is one file staged under a temporary name during a rename.
type pending struct {
	original string
	temp     string
	proposed string
}

// Changed reports whether the entry would actually be renamed.
func (e Entry) Changed() bool { return e.Original != e.Proposed }

// List returns the names of the files directly inside dir, sorted by name.
// Subdirectories are ignored: the listing is one level deep and never
// recursive. The returned names are base names, not paths.
func List(ctx context.Context, dir string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading directory %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	return names, nil
}

// Conflicts checks a rename plan and returns, keyed by entry index, the reason
// each problematic entry cannot be renamed. An empty result means the plan is
// safe to apply. dir is inspected for filesystem entries that are not part of
// the plan but would block a destination; a directory that cannot be read
// simply contributes no such conflicts.
func Conflicts(ctx context.Context, dir string, entries []Entry) map[int]string {
	problems := make(map[int]string)

	targets := make(map[string][]int, len(entries))
	for i, e := range entries {
		switch {
		case strings.TrimSpace(e.Proposed) == "":
			problems[i] = "new name is empty"
		case e.Proposed == "." || e.Proposed == "..":
			problems[i] = "new name is a directory reference"
		case strings.ContainsRune(e.Proposed, os.PathSeparator), strings.ContainsRune(e.Proposed, '/'):
			problems[i] = "new name contains a path separator"
		}
		targets[e.Proposed] = append(targets[e.Proposed], i)
	}

	for _, indexes := range targets {
		if len(indexes) < 2 {
			continue
		}
		for _, i := range indexes {
			if _, taken := problems[i]; !taken {
				problems[i] = "several files would get this name"
			}
		}
	}

	planned := make(map[string]bool, len(entries))
	for _, e := range entries {
		planned[e.Original] = true
	}
	existing, err := os.ReadDir(dir)
	if err != nil {
		return problems
	}
	onDisk := make(map[string]bool, len(existing))
	for _, entry := range existing {
		if err := ctx.Err(); err != nil {
			return problems
		}
		onDisk[entry.Name()] = true
	}
	for i, e := range entries {
		if _, taken := problems[i]; taken || !e.Changed() {
			continue
		}
		if onDisk[e.Proposed] && !planned[e.Proposed] {
			problems[i] = "a filesystem entry with this name already exists"
		}
	}
	return problems
}

// Rename applies the plan inside dir and returns the number of files renamed.
// Entries whose name is unchanged are skipped. Renaming happens in two passes
// through temporary names so that files can swap or rotate names, and a
// failure in the first pass is rolled back before the error is returned.
func Rename(ctx context.Context, dir string, entries []Entry) (int, error) {
	problems := Conflicts(ctx, dir, entries)
	for i, e := range entries {
		if reason, bad := problems[i]; bad {
			return 0, fmt.Errorf("cannot rename %s: %s", e.Original, reason)
		}
	}

	var staged []pending

	rollbackStaged := func() {
		for i := len(staged) - 1; i >= 0; i-- {
			_ = os.Rename(filepath.Join(dir, staged[i].temp), filepath.Join(dir, staged[i].original))
		}
	}

	for i, e := range entries {
		if !e.Changed() {
			continue
		}
		if err := ctx.Err(); err != nil {
			rollbackStaged()
			return 0, err
		}
		temp, err := tempName(dir, i, e.Original)
		if err != nil {
			rollbackStaged()
			return 0, fmt.Errorf("preparing rename of %s: %w", e.Original, err)
		}
		if err := os.Rename(filepath.Join(dir, e.Original), filepath.Join(dir, temp)); err != nil {
			rollbackStaged()
			return 0, fmt.Errorf("preparing rename of %s: %w", e.Original, err)
		}
		staged = append(staged, pending{original: e.Original, temp: temp, proposed: e.Proposed})
	}

	renamed := 0
	for i, p := range staged {
		if err := ctx.Err(); err != nil {
			rollbackCommitted(dir, staged, i)
			return renamed, err
		}
		if err := os.Rename(filepath.Join(dir, p.temp), filepath.Join(dir, p.proposed)); err != nil {
			rollbackCommitted(dir, staged, i)
			return renamed, fmt.Errorf("renaming to %s: %w", p.proposed, err)
		}
		renamed++
	}
	return renamed, nil
}

// maxNameLen is the longest file name accepted by common file systems.
const maxNameLen = 255

// rollbackCommitted moves completed destinations back to temporary names,
// then restores every staged file to its original name. The first phase is
// necessary for swaps, where an original name may currently be a destination.
func rollbackCommitted(dir string, staged []pending, committed int) {
	for i := committed - 1; i >= 0; i-- {
		_ = os.Rename(filepath.Join(dir, staged[i].proposed), filepath.Join(dir, staged[i].temp))
	}
	for i := len(staged) - 1; i >= 0; i-- {
		_ = os.Rename(filepath.Join(dir, staged[i].temp), filepath.Join(dir, staged[i].original))
	}
}

// tempName returns an unused, hard-to-guess intermediate name, short enough
// to stay valid on common file systems.
func tempName(dir string, i int, original string) (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generating temporary name: %w", err)
	}
	prefix := fmt.Sprintf(".aehenamer-%d-%s-", i, hex.EncodeToString(random[:]))
	remaining := maxNameLen - len(prefix)
	if remaining < 0 {
		remaining = 0
	}
	if len(original) > remaining {
		original = original[:remaining]
	}
	name := prefix + original
	if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
		return "", fmt.Errorf("temporary name %q already exists", name)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("checking temporary name %q: %w", name, err)
	}
	return name, nil
}

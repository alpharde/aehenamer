package files_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"aehenamer/internal/files"
)

// makeDir creates a temporary directory containing the given files.
func makeDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatalf("creating %s: %v", name, err)
		}
	}
	return dir
}

// namesOn returns the sorted file names currently in dir.
func namesOn(t *testing.T, dir string) []string {
	t.Helper()
	got, err := files.List(context.Background(), dir)
	if err != nil {
		t.Fatalf("listing %s: %v", dir, err)
	}
	return got
}

func TestList(t *testing.T) {
	dir := makeDir(t, "b.txt", "a.txt", ".hidden")
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o750); err != nil {
		t.Fatalf("creating subdirectory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "deep.txt"), nil, 0o600); err != nil {
		t.Fatalf("creating nested file: %v", err)
	}

	got := namesOn(t, dir)
	want := []string{".hidden", "a.txt", "b.txt"}
	if !slices.Equal(got, want) {
		t.Errorf("List() = %v, want %v (directories and nested files must be skipped)", got, want)
	}
}

func TestListErrors(t *testing.T) {
	t.Run("missing directory", func(t *testing.T) {
		_, err := files.List(context.Background(), filepath.Join(t.TempDir(), "nope"))
		if err == nil {
			t.Fatal("List() of a missing directory returned no error")
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("error %v does not wrap os.ErrNotExist", err)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := files.List(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
			t.Errorf("List() = %v, want context.Canceled", err)
		}
	})
}

func TestConflicts(t *testing.T) {
	tests := []struct {
		name        string
		onDisk      []string
		directories []string
		entries     []files.Entry
		want        map[int]string
	}{
		{
			name:   "clean plan",
			onDisk: []string{"a.txt", "b.txt"},
			entries: []files.Entry{
				{Original: "a.txt", Proposed: "1.txt"},
				{Original: "b.txt", Proposed: "2.txt"},
			},
			want: map[int]string{},
		},
		{
			name:   "swapping names is allowed",
			onDisk: []string{"a.txt", "b.txt"},
			entries: []files.Entry{
				{Original: "a.txt", Proposed: "b.txt"},
				{Original: "b.txt", Proposed: "a.txt"},
			},
			want: map[int]string{},
		},
		{
			name:   "duplicate targets",
			onDisk: []string{"a.txt", "b.txt"},
			entries: []files.Entry{
				{Original: "a.txt", Proposed: "same.txt"},
				{Original: "b.txt", Proposed: "same.txt"},
			},
			want: map[int]string{0: "several files would get this name", 1: "several files would get this name"},
		},
		{
			name:   "empty name",
			onDisk: []string{"a.txt"},
			entries: []files.Entry{
				{Original: "a.txt", Proposed: "  "},
			},
			want: map[int]string{0: "new name is empty"},
		},
		{
			name:   "path separator",
			onDisk: []string{"a.txt"},
			entries: []files.Entry{
				{Original: "a.txt", Proposed: "sub/a.txt"},
			},
			want: map[int]string{0: "new name contains a path separator"},
		},
		{
			name:   "directory reference",
			onDisk: []string{"a.txt"},
			entries: []files.Entry{
				{Original: "a.txt", Proposed: ".."},
			},
			want: map[int]string{0: "new name is a directory reference"},
		},
		{
			name:   "would overwrite a file that is not part of the plan",
			onDisk: []string{"a.txt", "keep.txt"},
			entries: []files.Entry{
				{Original: "a.txt", Proposed: "keep.txt"},
			},
			want: map[int]string{0: "a filesystem entry with this name already exists"},
		},
		{
			name:        "would collide with an existing directory",
			onDisk:      []string{"a.txt"},
			directories: []string{"archive"},
			entries: []files.Entry{
				{Original: "a.txt", Proposed: "archive"},
			},
			want: map[int]string{0: "a filesystem entry with this name already exists"},
		},
		{
			name:   "unchanged names never conflict with themselves",
			onDisk: []string{"a.txt"},
			entries: []files.Entry{
				{Original: "a.txt", Proposed: "a.txt"},
			},
			want: map[int]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := makeDir(t, tt.onDisk...)
			for _, name := range tt.directories {
				if err := os.Mkdir(filepath.Join(dir, name), 0o750); err != nil {
					t.Fatalf("creating directory %s: %v", name, err)
				}
			}
			got := files.Conflicts(context.Background(), dir, tt.entries)
			if len(got) != len(tt.want) {
				t.Fatalf("Conflicts() = %v, want %v", got, tt.want)
			}
			for index, reason := range tt.want {
				if got[index] != reason {
					t.Errorf("entry %d: got %q, want %q", index, got[index], reason)
				}
			}
		})
	}
}

func TestRename(t *testing.T) {
	t.Run("renames and skips unchanged files", func(t *testing.T) {
		dir := makeDir(t, "a.txt", "b.txt")
		renamed, err := files.Rename(context.Background(), dir, []files.Entry{
			{Original: "a.txt", Proposed: "1.txt"},
			{Original: "b.txt", Proposed: "b.txt"},
		})
		if err != nil {
			t.Fatalf("Rename() error: %v", err)
		}
		if renamed != 1 {
			t.Errorf("Rename() = %d, want 1", renamed)
		}
		if got, want := namesOn(t, dir), []string{"1.txt", "b.txt"}; !slices.Equal(got, want) {
			t.Errorf("directory holds %v, want %v", got, want)
		}
	})

	t.Run("swaps names", func(t *testing.T) {
		dir := makeDir(t, "a.txt", "b.txt")
		if _, err := files.Rename(context.Background(), dir, []files.Entry{
			{Original: "a.txt", Proposed: "b.txt"},
			{Original: "b.txt", Proposed: "a.txt"},
		}); err != nil {
			t.Fatalf("Rename() error: %v", err)
		}
		content, err := os.ReadFile(filepath.Join(dir, "a.txt"))
		if err != nil {
			t.Fatalf("reading a.txt: %v", err)
		}
		if string(content) != "b.txt" {
			t.Errorf("a.txt holds %q, want the contents of the old b.txt", content)
		}
	})

	t.Run("rotates names", func(t *testing.T) {
		dir := makeDir(t, "a.txt", "b.txt", "c.txt")
		if _, err := files.Rename(context.Background(), dir, []files.Entry{
			{Original: "a.txt", Proposed: "b.txt"},
			{Original: "b.txt", Proposed: "c.txt"},
			{Original: "c.txt", Proposed: "a.txt"},
		}); err != nil {
			t.Fatalf("Rename() error: %v", err)
		}
		for name, want := range map[string]string{"b.txt": "a.txt", "c.txt": "b.txt", "a.txt": "c.txt"} {
			content, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("reading %s: %v", name, err)
			}
			if string(content) != want {
				t.Errorf("%s holds %q, want %q", name, content, want)
			}
		}
	})

	t.Run("refuses a conflicting plan", func(t *testing.T) {
		dir := makeDir(t, "a.txt", "b.txt")
		_, err := files.Rename(context.Background(), dir, []files.Entry{
			{Original: "a.txt", Proposed: "same.txt"},
			{Original: "b.txt", Proposed: "same.txt"},
		})
		if err == nil {
			t.Fatal("Rename() accepted a conflicting plan")
		}
		if !strings.Contains(err.Error(), "a.txt") {
			t.Errorf("error %v does not name the offending file", err)
		}
		if got, want := namesOn(t, dir), []string{"a.txt", "b.txt"}; !slices.Equal(got, want) {
			t.Errorf("directory holds %v, want the untouched %v", got, want)
		}
	})

	t.Run("rolls back when a file disappears", func(t *testing.T) {
		dir := makeDir(t, "a.txt", "b.txt")
		if err := os.Remove(filepath.Join(dir, "b.txt")); err != nil {
			t.Fatalf("removing b.txt: %v", err)
		}
		_, err := files.Rename(context.Background(), dir, []files.Entry{
			{Original: "a.txt", Proposed: "1.txt"},
			{Original: "b.txt", Proposed: "2.txt"},
		})
		if err == nil {
			t.Fatal("Rename() succeeded although a file was missing")
		}
		if got, want := namesOn(t, dir), []string{"a.txt"}; !slices.Equal(got, want) {
			t.Errorf("directory holds %v, want the rolled back %v", got, want)
		}
	})

	t.Run("rolls back when the final pass fails", func(t *testing.T) {
		dir := makeDir(t, "a.txt", "b.txt")
		tooLong := strings.Repeat("x", 256)

		renamed, err := files.Rename(context.Background(), dir, []files.Entry{
			{Original: "a.txt", Proposed: "1.txt"},
			{Original: "b.txt", Proposed: tooLong},
		})
		if err == nil {
			t.Fatal("Rename() succeeded with an overlong destination name")
		}
		if renamed != 1 {
			t.Errorf("Rename() = %d completed renames before rollback, want 1", renamed)
		}
		if got, want := namesOn(t, dir), []string{"a.txt", "b.txt"}; !slices.Equal(got, want) {
			t.Errorf("directory holds files %v, want the rolled back %v", got, want)
		}
	})

	t.Run("cancelled context stops before touching anything", func(t *testing.T) {
		dir := makeDir(t, "a.txt")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := files.Rename(ctx, dir, []files.Entry{{Original: "a.txt", Proposed: "1.txt"}}); !errors.Is(err, context.Canceled) {
			t.Errorf("Rename() = %v, want context.Canceled", err)
		}
		if got, want := namesOn(t, dir), []string{"a.txt"}; !slices.Equal(got, want) {
			t.Errorf("directory holds %v, want %v", got, want)
		}
	})
}

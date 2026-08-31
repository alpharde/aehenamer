package ops_test

import (
	"strings"
	"testing"

	"aehenamer/internal/ops"
)

// apply builds the operation and applies it to a single name.
func apply(t *testing.T, cfg ops.Config, name string, index int) string {
	t.Helper()
	op, err := ops.New(cfg)
	if err != nil {
		t.Fatalf("ops.New(%+v): %v", cfg, err)
	}
	return op.Apply(name, index)
}

func TestReplace(t *testing.T) {
	base := func() ops.Config {
		cfg := ops.DefaultConfig(ops.KindReplace)
		cfg.Target = "foo"
		cfg.Replacement = "baz"
		return cfg
	}

	tests := []struct {
		name string
		cfg  func(c *ops.Config)
		in   string
		want string
	}{
		{name: "all occurrences", in: "foo bar foo.txt", want: "baz bar baz.txt"},
		{
			name: "limit from start",
			cfg:  func(c *ops.Config) { c.Limit = "1" },
			in:   "foo bar foo.txt", want: "baz bar foo.txt",
		},
		{
			name: "limit from end",
			cfg:  func(c *ops.Config) { c.Limit = "1"; c.FromStart = false },
			in:   "foo bar foo.txt", want: "foo bar baz.txt",
		},
		{
			name: "limit larger than matches",
			cfg:  func(c *ops.Config) { c.Limit = "9" },
			in:   "foo.txt", want: "baz.txt",
		},
		{name: "case insensitive by default", in: "FoO.txt", want: "baz.txt"},
		{
			name: "case sensitive",
			cfg:  func(c *ops.Config) { c.CaseSensitive = true },
			in:   "FoO.txt", want: "FoO.txt",
		},
		{
			name: "extension preserved",
			cfg:  func(c *ops.Config) { c.Target = "txt"; c.Replacement = "md" },
			in:   "txt.txt", want: "md.txt",
		},
		{
			name: "extension included when not preserved",
			cfg:  func(c *ops.Config) { c.Target = "txt"; c.Replacement = "md"; c.PreserveExt = false },
			in:   "txt.txt", want: "md.md",
		},
		{
			name: "regex with capture group",
			cfg:  func(c *ops.Config) { c.Regex = true; c.Target = `(\d+)`; c.Replacement = "[$1]" },
			in:   "episode 12.mkv", want: "episode [12].mkv",
		},
		{
			name: "regex is quoted in literal mode",
			cfg:  func(c *ops.Config) { c.Target = `a.c`; c.Replacement = "x" },
			in:   "abc a.c.txt", want: "abc x.txt",
		},
		{
			name: "empty target is a no-op",
			cfg:  func(c *ops.Config) { c.Target = "" },
			in:   "foo.txt", want: "foo.txt",
		},
		{
			name: "no match leaves the name alone",
			cfg:  func(c *ops.Config) { c.Target = "zzz" },
			in:   "foo.txt", want: "foo.txt",
		},
		{
			name: "dotfile has no extension to preserve",
			cfg:  func(c *ops.Config) { c.Target = "bashrc"; c.Replacement = "zshrc" },
			in:   ".bashrc", want: ".zshrc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base()
			if tt.cfg != nil {
				tt.cfg(&cfg)
			}
			if got := apply(t, cfg, tt.in, 0); got != tt.want {
				t.Errorf("Apply(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRemove(t *testing.T) {
	tests := []struct {
		name string
		cfg  func(c *ops.Config)
		in   string
		want string
	}{
		{
			name: "literal text",
			cfg:  func(c *ops.Config) { c.Target = " - copy" },
			in:   "report - copy.pdf", want: "report.pdf",
		},
		{
			name: "regular expression",
			cfg:  func(c *ops.Config) { c.Target = `\s*\(\d+\)`; c.Regex = true },
			in:   "photo (1).jpg", want: "photo.jpg",
		},
		{
			name: "last match only",
			cfg:  func(c *ops.Config) { c.Target = "x"; c.Limit = "1"; c.FromStart = false },
			in:   "xaxbx.txt", want: "xaxb.txt",
		},
		{
			name: "extension untouched",
			cfg:  func(c *ops.Config) { c.Target = "a" },
			in:   "banana.aac", want: "bnn.aac",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ops.DefaultConfig(ops.KindRemove)
			tt.cfg(&cfg)
			if got := apply(t, cfg, tt.in, 0); got != tt.want {
				t.Errorf("Apply(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestInsert(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		index string
		ext   bool
		in    string
		want  string
	}{
		{name: "at the beginning", text: "2024-", index: "0", ext: true, in: "trip.jpg", want: "2024-trip.jpg"},
		{name: "in the middle", text: "-", index: "2", ext: true, in: "abcd.jpg", want: "ab-cd.jpg"},
		{name: "to-last", text: "-end", index: ops.ToLast, ext: true, in: "abcd.jpg", want: "abcd-end.jpg"},
		{name: "to-last without preserving extension", text: "!", index: ops.ToLast, ext: false, in: "abcd.jpg", want: "abcd.jpg!"},
		{name: "index beyond the name is clamped", text: "!", index: "99", ext: true, in: "ab.jpg", want: "ab!.jpg"},
		{name: "negative index is clamped", text: "!", index: "-3", ext: true, in: "ab.jpg", want: "!ab.jpg"},
		{name: "empty index defaults to the front", text: "!", index: "", ext: true, in: "ab.jpg", want: "!ab.jpg"},
		{name: "counts characters not bytes", text: "-", index: "2", ext: true, in: "héllo.txt", want: "hé-llo.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ops.DefaultConfig(ops.KindInsert)
			cfg.Text, cfg.Index, cfg.PreserveExt = tt.text, tt.index, tt.ext
			if got := apply(t, cfg, tt.in, 0); got != tt.want {
				t.Errorf("Apply(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestIncrement(t *testing.T) {
	cfg := ops.DefaultConfig(ops.KindIncrement)
	cfg.Prefix = "_"
	cfg.Start = "1"
	cfg.Step = "2"

	names := []string{"a.txt", "b.txt", "c.txt"}
	op, err := ops.New(cfg)
	if err != nil {
		t.Fatalf("ops.New: %v", err)
	}
	got := ops.Preview(names, []ops.Operation{op})
	want := []string{"a_1.txt", "b_3.txt", "c_5.txt"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("file %d = %q, want %q", i, got[i], want[i])
		}
	}

	t.Run("defaults", func(t *testing.T) {
		cfg := ops.DefaultConfig(ops.KindIncrement)
		if got := apply(t, cfg, "a.txt", 3); got != "a3.txt" {
			t.Errorf("got %q, want %q", got, "a3.txt")
		}
	})
}

func TestCase(t *testing.T) {
	tests := []struct {
		name string
		kind ops.Kind
		ext  bool
		in   string
		want string
	}{
		{name: "lower keeps extension", kind: ops.KindLower, ext: true, in: "MyFile.TXT", want: "myfile.TXT"},
		{name: "lower whole name", kind: ops.KindLower, ext: false, in: "MyFile.TXT", want: "myfile.txt"},
		{name: "upper keeps extension", kind: ops.KindUpper, ext: true, in: "MyFile.txt", want: "MYFILE.txt"},
		{name: "upper whole name", kind: ops.KindUpper, ext: false, in: "MyFile.txt", want: "MYFILE.TXT"},
		{name: "dotfile", kind: ops.KindUpper, ext: true, in: ".bashrc", want: ".BASHRC"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ops.DefaultConfig(tt.kind)
			cfg.PreserveExt = tt.ext
			if got := apply(t, cfg, tt.in, 0); got != tt.want {
				t.Errorf("Apply(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name        string
		first, last string
		keepBetween bool
		ext         bool
		in          string
		want        string
	}{
		{name: "remove range", first: "0", last: "2", ext: true, in: "abcdef.txt", want: "def.txt"},
		{name: "keep range", first: "0", last: "2", keepBetween: true, ext: true, in: "abcdef.txt", want: "abc.txt"},
		{name: "remove to the end", first: "2", last: ops.ToLast, ext: true, in: "abcdef.txt", want: "ab.txt"},
		{name: "keep to the end", first: "2", last: ops.ToLast, keepBetween: true, ext: true, in: "abcdef.txt", want: "cdef.txt"},
		{name: "single character", first: "1", last: "1", ext: true, in: "abc.txt", want: "ac.txt"},
		{name: "swapped indexes", first: "4", last: "1", ext: true, in: "abcdef.txt", want: "af.txt"},
		{name: "indexes beyond the name are clamped", first: "0", last: "99", ext: true, in: "abc.txt", want: ".txt"},
		{name: "extension included", first: "0", last: "0", ext: false, in: "abc.txt", want: "bc.txt"},
		{name: "counts characters not bytes", first: "0", last: "1", ext: true, in: "héllo.txt", want: "llo.txt"},
		{name: "dotfile is truncated as a whole", first: "0", last: "0", ext: true, in: ".txt", want: "txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ops.DefaultConfig(ops.KindTruncate)
			cfg.First, cfg.Last = tt.first, tt.last
			cfg.KeepBetween, cfg.PreserveExt = tt.keepBetween, tt.ext
			if got := apply(t, cfg, tt.in, 0); got != tt.want {
				t.Errorf("Apply(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNewRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		cfg  ops.Config
		want string
	}{
		{
			name: "unknown kind",
			cfg:  ops.Config{Kind: "Frobnicate"},
			want: "unknown operation kind",
		},
		{
			name: "invalid regular expression",
			cfg:  ops.Config{Kind: ops.KindReplace, Target: "([a-z", Regex: true},
			want: "invalid regular expression",
		},
		{
			name: "limit is not a number",
			cfg:  ops.Config{Kind: ops.KindReplace, Target: "a", Limit: "many"},
			want: "limit",
		},
		{
			name: "negative limit",
			cfg:  ops.Config{Kind: ops.KindRemove, Target: "a", Limit: "-2"},
			want: "must not be negative",
		},
		{
			name: "insert index is not a number",
			cfg:  ops.Config{Kind: ops.KindInsert, Index: "somewhere"},
			want: "insert index",
		},
		{
			name: "start is not a number",
			cfg:  ops.Config{Kind: ops.KindIncrement, Start: "one"},
			want: "number to start",
		},
		{
			name: "step is not a number",
			cfg:  ops.Config{Kind: ops.KindIncrement, Step: "two"},
			want: "incremental step",
		},
		{
			name: "truncate index is not a number",
			cfg:  ops.Config{Kind: ops.KindTruncate, First: "x"},
			want: "first character index",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, err := ops.New(tt.cfg)
			if err == nil {
				t.Fatalf("ops.New(%+v) = %v, want an error", tt.cfg, op)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

func TestPreviewAppliesOperationsInOrder(t *testing.T) {
	remove := ops.DefaultConfig(ops.KindRemove)
	remove.Target = "IMG_"
	lower := ops.DefaultConfig(ops.KindUpper)
	increment := ops.DefaultConfig(ops.KindIncrement)
	increment.Prefix = "-"
	increment.Start = "10"

	list := make([]ops.Operation, 0, 3)
	for _, cfg := range []ops.Config{remove, lower, increment} {
		op, err := ops.New(cfg)
		if err != nil {
			t.Fatalf("ops.New(%v): %v", cfg.Kind, err)
		}
		list = append(list, op)
	}

	names := []string{"IMG_one.jpg", "IMG_two.jpg"}
	got := ops.Preview(names, list)
	want := []string{"ONE-10.jpg", "TWO-11.jpg"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("file %d = %q, want %q", i, got[i], want[i])
		}
	}
	if names[0] != "IMG_one.jpg" {
		t.Errorf("Preview modified its input: %q", names[0])
	}
}

func TestPreviewWithoutOperations(t *testing.T) {
	names := []string{"a.txt", "b.txt"}
	got := ops.Preview(names, nil)
	for i := range names {
		if got[i] != names[i] {
			t.Errorf("file %d = %q, want %q", i, got[i], names[i])
		}
	}
}

func TestSummaryMentionsTheSettings(t *testing.T) {
	cfg := ops.DefaultConfig(ops.KindReplace)
	cfg.Target = "a"
	cfg.Replacement = "b"
	cfg.Limit = "2"
	cfg.Regex = true

	op, err := ops.New(cfg)
	if err != nil {
		t.Fatalf("ops.New: %v", err)
	}
	summary := op.Summary()
	for _, want := range []string{`"a"`, `"b"`, "first 2", "regex", "keep extension"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary %q does not mention %q", summary, want)
		}
	}
	if op.Config().Target != "a" {
		t.Errorf("Config() lost the entered values: %+v", op.Config())
	}
}

func TestDefaultConfig(t *testing.T) {
	tests := []struct {
		kind ops.Kind
		want ops.Config
	}{
		{kind: ops.KindIncrement, want: ops.Config{Kind: ops.KindIncrement, Start: "0", Step: "1", FromStart: true, PreserveExt: true}},
		{kind: ops.KindTruncate, want: ops.Config{Kind: ops.KindTruncate, First: "0", Last: "0", FromStart: true, PreserveExt: true}},
		{kind: ops.KindInsert, want: ops.Config{Kind: ops.KindInsert, Index: "0", FromStart: true, PreserveExt: true}},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			if got := ops.DefaultConfig(tt.kind); got != tt.want {
				t.Errorf("DefaultConfig(%q) = %+v, want %+v", tt.kind, got, tt.want)
			}
		})
	}
}

// Package ops implements the file name transformations that a bulk rename is
// built from. Operations are pure functions of a name and its position in the
// file list, which makes a rename easy to preview before anything is renamed
// on disk.
package ops

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Kind identifies a type of rename operation.
type Kind string

// The supported operation kinds.
const (
	KindReplace   Kind = "Replace"
	KindRemove    Kind = "Remove"
	KindInsert    Kind = "Insert"
	KindIncrement Kind = "Increment"
	KindLower     Kind = "To lowercase"
	KindUpper     Kind = "To uppercase"
	KindTruncate  Kind = "Truncate"
)

// Kinds lists every supported operation kind, in menu order.
var Kinds = []Kind{
	KindReplace,
	KindRemove,
	KindInsert,
	KindIncrement,
	KindLower,
	KindUpper,
	KindTruncate,
}

// ToLast is the sentinel accepted by index fields to address the last
// character of a name instead of a fixed position.
const ToLast = "to-last"

// Config holds the raw, user-entered settings of a single operation. Values
// are kept as typed by the user so that an operation can be re-opened for
// editing exactly as it was created. Fields not used by Config.Kind are
// ignored by [New].
type Config struct {
	Kind Kind

	Target      string // Replace, Remove: the text or pattern to look for.
	Replacement string // Replace: the text to put in its place.
	Limit       string // Replace, Remove: max number of matches, empty or 0 for all.
	Text        string // Insert: the text to insert.
	Index       string // Insert: character position, or ToLast for the end.
	Prefix      string // Increment: text placed before the counter.
	Start       string // Increment: first counter value.
	Step        string // Increment: counter increment per file.
	First       string // Truncate: first character index, or ToLast.
	Last        string // Truncate: last character index, or ToLast.

	FromStart     bool // Replace, Remove: apply Limit to the first matches instead of the last.
	CaseSensitive bool // Replace, Remove: match letter case exactly.
	Regex         bool // Replace, Remove: treat Target as a regular expression.
	KeepBetween   bool // Truncate: keep the selected range instead of removing it.
	PreserveExt   bool // All kinds: leave the file extension untouched.
}

// DefaultConfig returns the configuration a new operation of the given kind
// starts with, including the documented default values.
func DefaultConfig(kind Kind) Config {
	cfg := Config{Kind: kind, FromStart: true, PreserveExt: true}
	switch kind {
	case KindInsert:
		cfg.Index = "0"
	case KindIncrement:
		cfg.Start = "0"
		cfg.Step = "1"
	case KindTruncate:
		cfg.First = "0"
		cfg.Last = "0"
	}
	return cfg
}

// Operation transforms file names as one step of a rename pipeline.
type Operation interface {
	// Config returns the settings the operation was built from.
	Config() Config
	// Summary returns a short one-line description for the operation list.
	Summary() string
	// Apply returns name transformed. index is the zero-based position of the
	// file within the list being renamed; only counter-based operations use it.
	Apply(name string, index int) string
}

// New builds an operation from cfg. It returns an error if a numeric field
// cannot be parsed or a regular expression does not compile.
func New(cfg Config) (Operation, error) {
	switch cfg.Kind {
	case KindReplace, KindRemove:
		return newSubstitute(cfg)
	case KindInsert:
		return newInsert(cfg)
	case KindIncrement:
		return newIncrement(cfg)
	case KindLower, KindUpper:
		return &caseOp{cfg: cfg}, nil
	case KindTruncate:
		return newTruncate(cfg)
	default:
		return nil, fmt.Errorf("unknown operation kind %q", cfg.Kind)
	}
}

// Preview applies every operation, in order, to each of names and returns the
// resulting names. The input slice is not modified.
func Preview(names []string, list []Operation) []string {
	out := make([]string, len(names))
	for i, name := range names {
		for _, op := range list {
			name = op.Apply(name, i)
		}
		out[i] = name
	}
	return out
}

// splitExt splits name into a stem and an extension. When preserve is false,
// or when name has no extension to speak of (such as ".bashrc"), the whole
// name is returned as the stem.
func splitExt(name string, preserve bool) (stem, ext string) {
	if !preserve {
		return name, ""
	}
	ext = filepath.Ext(name)
	if ext == "" || ext == name {
		return name, ""
	}
	return name[:len(name)-len(ext)], ext
}

// parseInt parses a decimal integer, returning def for an empty string. The
// field name is used to give the error context.
func parseInt(field, s string, def int) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a number", field, s)
	}
	return n, nil
}

// parseIndex parses a character index that may also be the [ToLast] sentinel,
// in which case toLast is true and n is meaningless.
func parseIndex(field, s string, def int) (n int, toLast bool, err error) {
	if strings.EqualFold(strings.TrimSpace(s), ToLast) {
		return 0, true, nil
	}
	n, err = parseInt(field, s, def)
	return n, false, err
}

// compileTarget turns the user's search text into a regular expression,
// quoting it unless cfg asks for regex mode. It returns a nil expression for
// an empty target, which makes the operation a no-op.
func compileTarget(cfg Config) (*regexp.Regexp, error) {
	if cfg.Target == "" {
		return nil, nil
	}
	pattern := cfg.Target
	if !cfg.Regex {
		pattern = regexp.QuoteMeta(pattern)
	}
	if !cfg.CaseSensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression: %w", err)
	}
	return re, nil
}

// substituteOp implements both [KindReplace] and [KindRemove]; a removal is a
// replacement with empty text.
type substituteOp struct {
	cfg   Config
	re    *regexp.Regexp
	repl  string
	limit int
}

func newSubstitute(cfg Config) (Operation, error) {
	re, err := compileTarget(cfg)
	if err != nil {
		return nil, err
	}
	limit, err := parseInt("limit", cfg.Limit, 0)
	if err != nil {
		return nil, err
	}
	if limit < 0 {
		return nil, fmt.Errorf("limit: must not be negative")
	}
	op := &substituteOp{cfg: cfg, re: re, limit: limit}
	if cfg.Kind == KindReplace {
		op.repl = cfg.Replacement
	}
	return op, nil
}

func (o *substituteOp) Config() Config { return o.cfg }

func (o *substituteOp) Apply(name string, _ int) string {
	if o.re == nil {
		return name
	}
	stem, ext := splitExt(name, o.cfg.PreserveExt)
	return o.substitute(stem) + ext
}

// substitute replaces the selected matches in s. In regex mode the
// replacement may refer to capture groups with $1 or ${name}.
func (o *substituteOp) substitute(s string) string {
	matches := o.re.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return s
	}
	if o.limit > 0 && o.limit < len(matches) {
		if o.cfg.FromStart {
			matches = matches[:o.limit]
		} else {
			matches = matches[len(matches)-o.limit:]
		}
	}
	var b []byte
	last := 0
	for _, m := range matches {
		b = append(b, s[last:m[0]]...)
		if o.cfg.Regex {
			b = o.re.ExpandString(b, o.repl, s, m)
		} else {
			b = append(b, o.repl...)
		}
		last = m[1]
	}
	return string(append(b, s[last:]...))
}

func (o *substituteOp) Summary() string {
	var b strings.Builder
	if o.cfg.Kind == KindRemove {
		fmt.Fprintf(&b, "Remove %q", o.cfg.Target)
	} else {
		fmt.Fprintf(&b, "Replace %q with %q", o.cfg.Target, o.cfg.Replacement)
	}
	flags := []string{}
	if o.limit > 0 {
		where := "last"
		if o.cfg.FromStart {
			where = "first"
		}
		flags = append(flags, fmt.Sprintf("%s %d", where, o.limit))
	}
	if o.cfg.CaseSensitive {
		flags = append(flags, "case sensitive")
	}
	if o.cfg.Regex {
		flags = append(flags, "regex")
	}
	return b.String() + flagSuffix(flags, o.cfg.PreserveExt)
}

// insertOp implements [KindInsert].
type insertOp struct {
	cfg    Config
	at     int
	toLast bool
}

func newInsert(cfg Config) (Operation, error) {
	at, toLast, err := parseIndex("insert index", cfg.Index, 0)
	if err != nil {
		return nil, err
	}
	return &insertOp{cfg: cfg, at: at, toLast: toLast}, nil
}

func (o *insertOp) Config() Config { return o.cfg }

func (o *insertOp) Apply(name string, _ int) string {
	stem, ext := splitExt(name, o.cfg.PreserveExt)
	r := []rune(stem)
	at := o.at
	if o.toLast || at > len(r) {
		at = len(r)
	}
	if at < 0 {
		at = 0
	}
	return string(r[:at]) + o.cfg.Text + string(r[at:]) + ext
}

func (o *insertOp) Summary() string {
	where := strconv.Itoa(o.at)
	if o.toLast {
		where = "the end"
	}
	return fmt.Sprintf("Insert %q at %s", o.cfg.Text, where) + flagSuffix(nil, o.cfg.PreserveExt)
}

// incrementOp implements [KindIncrement]. The counter is appended to the name,
// preceded by the configured prefix.
type incrementOp struct {
	cfg   Config
	start int
	step  int
}

func newIncrement(cfg Config) (Operation, error) {
	start, err := parseInt("number to start", cfg.Start, 0)
	if err != nil {
		return nil, err
	}
	step, err := parseInt("incremental step", cfg.Step, 1)
	if err != nil {
		return nil, err
	}
	return &incrementOp{cfg: cfg, start: start, step: step}, nil
}

func (o *incrementOp) Config() Config { return o.cfg }

func (o *incrementOp) Apply(name string, index int) string {
	stem, ext := splitExt(name, o.cfg.PreserveExt)
	return stem + o.cfg.Prefix + strconv.Itoa(o.start+index*o.step) + ext
}

func (o *incrementOp) Summary() string {
	return fmt.Sprintf("Append %q + counter from %d step %d", o.cfg.Prefix, o.start, o.step) +
		flagSuffix(nil, o.cfg.PreserveExt)
}

// caseOp implements [KindLower] and [KindUpper].
type caseOp struct {
	cfg Config
}

func (o *caseOp) Config() Config { return o.cfg }

func (o *caseOp) Apply(name string, _ int) string {
	stem, ext := splitExt(name, o.cfg.PreserveExt)
	if o.cfg.Kind == KindUpper {
		return strings.ToUpper(stem) + ext
	}
	return strings.ToLower(stem) + ext
}

func (o *caseOp) Summary() string {
	return string(o.cfg.Kind) + flagSuffix(nil, o.cfg.PreserveExt)
}

// truncateOp implements [KindTruncate].
type truncateOp struct {
	cfg        Config
	first      int
	last       int
	firstLast  bool
	lastIsLast bool
}

func newTruncate(cfg Config) (Operation, error) {
	first, firstLast, err := parseIndex("first character index", cfg.First, 0)
	if err != nil {
		return nil, err
	}
	last, lastIsLast, err := parseIndex("last character index", cfg.Last, 0)
	if err != nil {
		return nil, err
	}
	return &truncateOp{cfg: cfg, first: first, last: last, firstLast: firstLast, lastIsLast: lastIsLast}, nil
}

func (o *truncateOp) Config() Config { return o.cfg }

func (o *truncateOp) Apply(name string, _ int) string {
	stem, ext := splitExt(name, o.cfg.PreserveExt)
	r := []rune(stem)
	if len(r) == 0 {
		return name
	}
	first, last := o.first, o.last
	if o.firstLast {
		first = len(r) - 1
	}
	if o.lastIsLast {
		last = len(r) - 1
	}
	first = clamp(first, 0, len(r)-1)
	last = clamp(last, 0, len(r)-1)
	if first > last {
		first, last = last, first
	}
	if o.cfg.KeepBetween {
		return string(r[first:last+1]) + ext
	}
	return string(r[:first]) + string(r[last+1:]) + ext
}

func (o *truncateOp) Summary() string {
	verb := "Remove"
	if o.cfg.KeepBetween {
		verb = "Keep"
	}
	return fmt.Sprintf("%s characters %s..%s", verb, indexLabel(o.first, o.firstLast), indexLabel(o.last, o.lastIsLast)) +
		flagSuffix(nil, o.cfg.PreserveExt)
}

// indexLabel renders a character index for display.
func indexLabel(n int, toLast bool) string {
	if toLast {
		return ToLast
	}
	return strconv.Itoa(n)
}

// clamp confines n to the inclusive range [lo, hi].
func clamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// flagSuffix renders the parenthesised flag list shown after a summary.
func flagSuffix(flags []string, preserveExt bool) string {
	if preserveExt {
		flags = append(flags, "keep extension")
	}
	if len(flags) == 0 {
		return ""
	}
	return " (" + strings.Join(flags, ", ") + ")"
}

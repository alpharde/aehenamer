package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"aehenamer/internal/files"
	"aehenamer/internal/ops"
)

// waitTimeout bounds how long a test waits for the interface to catch up.
const waitTimeout = 5 * time.Second

// startApp runs the application on a simulation screen and returns helpers to
// drive it. The application is stopped when the test ends.
func startApp(t *testing.T, dir string) (*App, tcell.SimulationScreen, func(func())) {
	t.Helper()

	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(140, 30)

	app := New(dir)
	app.screen = screen

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	<-app.ready

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("Run() = %v, want nil or context.Canceled", err)
			}
		case <-time.After(waitTimeout):
			t.Error("Run() did not return after the context was cancelled")
		}
	})

	// onLoop runs fn on the event loop goroutine, so that tests can touch the
	// application state without racing the interface.
	onLoop := func(fn func()) {
		t.Helper()
		finished := make(chan struct{})
		app.app.QueueUpdateDraw(func() {
			defer close(finished)
			fn()
		})
		select {
		case <-finished:
		case <-time.After(waitTimeout):
			t.Fatal("the event loop did not run the queued function")
		}
	}
	return app, screen, onLoop
}

// waitForState polls cond on the event loop until it holds, so that tests can
// wait for injected keys to be processed without racing the interface.
func waitForState(t *testing.T, onLoop func(func()), what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		held := false
		onLoop(func() { held = cond() })
		if held {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// screenText returns everything currently drawn on the screen, one line per
// screen row.
func screenText(screen tcell.SimulationScreen) string {
	cells, width, height := screen.GetContents()
	var b strings.Builder
	for row := range height {
		for column := range width {
			runes := cells[row*width+column].Runes
			if len(runes) == 0 {
				b.WriteRune(' ')
				continue
			}
			b.WriteRune(runes[0])
		}
		b.WriteRune('\n')
	}
	return b.String()
}

// waitForText fails the test unless want shows up on the screen.
func waitForText(t *testing.T, screen tcell.SimulationScreen, onLoop func(func()), want string) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		var contents string
		onLoop(func() { contents = screenText(screen) })
		if strings.Contains(contents, want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	var contents string
	onLoop(func() { contents = screenText(screen) })
	t.Fatalf("%q never appeared on screen. Screen was:\n%s", want, contents)
}

// makeDir creates a temporary directory holding the given files.
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

func TestAppListsTheFolderOnStart(t *testing.T) {
	dir := makeDir(t, "one.txt", "two.txt")
	_, screen, onLoop := startApp(t, dir)

	waitForText(t, screen, onLoop, "one.txt")
	waitForText(t, screen, onLoop, "two.txt")
	waitForText(t, screen, onLoop, "No operations yet")
}

func TestAppDisplaysColorTagsInUserTextLiterally(t *testing.T) {
	dir := makeDir(t, "report[red].txt")
	app, screen, onLoop := startApp(t, dir)
	ctx := context.Background()

	waitForText(t, screen, onLoop, "report[red].txt")

	onLoop(func() {
		cfg := ops.DefaultConfig(ops.KindReplace)
		cfg.Target = "[red]"
		cfg.Replacement = "[blue]"
		op, err := ops.New(cfg)
		if err != nil {
			t.Fatalf("ops.New: %v", err)
		}
		app.operations = append(app.operations, op)
		app.refresh(ctx)
	})

	waitForText(t, screen, onLoop, `Replace "[red]" with`)
	waitForText(t, screen, onLoop, "report[blue].txt")
}

func TestAppPreviewsAndAppliesAnOperation(t *testing.T) {
	dir := makeDir(t, "one.txt", "two.txt")
	app, screen, onLoop := startApp(t, dir)
	ctx := context.Background()

	// Open the dialog the way the "Add operation" drop-down does.
	onLoop(func() {
		app.openOperationDialog(ctx, ops.KindUpper, ops.DefaultConfig(ops.KindUpper), -1)
	})
	waitForText(t, screen, onLoop, "Add operation")

	// The preview is live: the new names show up before the dialog is closed.
	waitForText(t, screen, onLoop, "ONE.txt")

	// Tab from the checkbox to the OK button and confirm.
	screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitForState(t, onLoop, "the operation to be added", func() bool {
		return len(app.operations) == 1
	})

	onLoop(func() {
		if app.pending != nil {
			t.Error("the preview operation outlived the dialog")
		}
		if got := app.entries[0].Proposed; got != "ONE.txt" {
			t.Errorf("proposed name = %q, want %q", got, "ONE.txt")
		}
	})

	// Apply the rename and confirm the dialog.
	onLoop(func() { app.confirmApply(ctx) })
	waitForText(t, screen, onLoop, "Rename 2 file(s)")
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitForState(t, onLoop, "the rename to finish", func() bool {
		return len(app.names) == 2 && app.names[0] == "ONE.txt"
	})

	got, err := files.List(ctx, dir)
	if err != nil {
		t.Fatalf("listing the folder: %v", err)
	}
	if want := []string{"ONE.txt", "TWO.txt"}; !slices.Equal(got, want) {
		t.Errorf("folder holds %v, want %v", got, want)
	}

	onLoop(func() {
		if len(app.operations) != 0 {
			t.Errorf("operations = %d, want the list to be cleared after a rename", len(app.operations))
		}
	})
}

func TestAppCancelledDialogLeavesNoOperation(t *testing.T) {
	dir := makeDir(t, "one.txt")
	app, screen, onLoop := startApp(t, dir)
	ctx := context.Background()

	onLoop(func() {
		app.openOperationDialog(ctx, ops.KindUpper, ops.DefaultConfig(ops.KindUpper), -1)
	})
	waitForText(t, screen, onLoop, "ONE.txt")

	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	waitForState(t, onLoop, "the dialog to close", func() bool {
		name, _ := app.pages.GetFrontPage()
		return name == pageMain
	})

	onLoop(func() {
		if len(app.operations) != 0 {
			t.Errorf("operations = %d, want 0", len(app.operations))
		}
		if got := app.entries[0].Proposed; got != "one.txt" {
			t.Errorf("proposed name = %q, want the preview to be undone", got)
		}
	})
}

func TestAppRemovesFilesAndOperations(t *testing.T) {
	dir := makeDir(t, "one.txt", "two.txt")
	app, screen, onLoop := startApp(t, dir)
	ctx := context.Background()

	// Drop the first file with the keyboard.
	onLoop(func() { app.app.SetFocus(app.filePane) })
	screen.InjectKey(tcell.KeyRune, 'x', tcell.ModNone)
	waitForState(t, onLoop, "the file to leave the list", func() bool {
		return slices.Equal(app.names, []string{"two.txt"})
	})

	// Reloading brings it back.
	screen.InjectKey(tcell.KeyCtrlR, 0, tcell.ModNone)
	waitForState(t, onLoop, "the folder to be reloaded", func() bool {
		return slices.Equal(app.names, []string{"one.txt", "two.txt"})
	})

	// Add two operations, then remove the first one with the keyboard.
	onLoop(func() {
		for _, kind := range []ops.Kind{ops.KindUpper, ops.KindLower} {
			op, err := ops.New(ops.DefaultConfig(kind))
			if err != nil {
				t.Fatalf("ops.New(%q): %v", kind, err)
			}
			app.operations = append(app.operations, op)
		}
		app.refresh(ctx)
		app.app.SetFocus(app.opPane)
	})
	waitForText(t, screen, onLoop, "To lowercase")

	screen.InjectKey(tcell.KeyRune, 'x', tcell.ModNone)
	waitForState(t, onLoop, "the operation to be removed", func() bool {
		return len(app.operations) == 1
	})
	onLoop(func() {
		if got := app.operations[0].Config().Kind; got != ops.KindLower {
			t.Errorf("remaining operation = %q, want %q", got, ops.KindLower)
		}
	})
}

func TestEmptyPanesRemainResponsiveAfterNavigation(t *testing.T) {
	dir := makeDir(t, "only.txt")
	app, screen, onLoop := startApp(t, dir)

	onLoop(func() { app.app.SetFocus(app.filePane) })
	screen.InjectKey(tcell.KeyDelete, 0, tcell.ModNone)
	waitForState(t, onLoop, "the last file to leave the list", func() bool {
		return len(app.names) == 0
	})

	// tview searches for the next selectable cell on navigation. A table made
	// only of non-selectable header/placeholder cells must itself be marked
	// non-selectable, or this search can loop forever.
	screen.InjectKey(tcell.KeyUp, 0, tcell.ModNone)
	screen.InjectKey(tcell.KeyDown, 0, tcell.ModNone)
	screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	waitForState(t, onLoop, "the event loop to remain responsive", func() bool {
		return app.app.GetFocus() == app.opPane
	})

	// The empty operation pane has the same protection.
	screen.InjectKey(tcell.KeyUp, 0, tcell.ModNone)
	screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	waitForState(t, onLoop, "focus to leave the empty operation pane", func() bool {
		return app.app.GetFocus() == app.addOp
	})
}

func TestTabClosesTheOperationMenuBeforeChangingPanes(t *testing.T) {
	dir := makeDir(t, "one.txt")
	app, screen, onLoop := startApp(t, dir)

	onLoop(func() { app.app.SetFocus(app.addOp) })
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitForState(t, onLoop, "the operation menu to open", func() bool {
		return app.addOp.IsOpen()
	})

	screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	waitForState(t, onLoop, "the operation menu to close and focus to move", func() bool {
		return !app.addOp.IsOpen() && app.app.GetFocus() == app.applyBtn
	})
}

func TestAppMovesOperations(t *testing.T) {
	dir := makeDir(t, "one.txt")
	app, _, onLoop := startApp(t, dir)
	ctx := context.Background()

	onLoop(func() {
		for _, kind := range []ops.Kind{ops.KindUpper, ops.KindLower} {
			op, err := ops.New(ops.DefaultConfig(kind))
			if err != nil {
				t.Fatalf("ops.New(%q): %v", kind, err)
			}
			app.operations = append(app.operations, op)
		}
		app.moveOperation(ctx, 1, -1)

		if got := app.operations[0].Config().Kind; got != ops.KindLower {
			t.Errorf("first operation = %q, want %q", got, ops.KindLower)
		}
		// Moving past either end does nothing.
		app.moveOperation(ctx, 0, -1)
		app.moveOperation(ctx, 1, 1)
		if got := app.operations[0].Config().Kind; got != ops.KindLower {
			t.Errorf("first operation = %q, want %q", got, ops.KindLower)
		}
	})
}

func TestAppReportsConflicts(t *testing.T) {
	dir := makeDir(t, "one.txt", "ONE.txt")
	app, screen, onLoop := startApp(t, dir)
	ctx := context.Background()

	onLoop(func() {
		op, err := ops.New(ops.DefaultConfig(ops.KindUpper))
		if err != nil {
			t.Fatalf("ops.New: %v", err)
		}
		app.operations = append(app.operations, op)
		app.refresh(ctx)

		if len(app.conflicts) != 2 {
			t.Errorf("conflicts = %v, want both files flagged", app.conflicts)
		}
		app.confirmApply(ctx)
	})
	waitForText(t, screen, onLoop, "Cannot rename")

	got, err := files.List(ctx, dir)
	if err != nil {
		t.Fatalf("listing the folder: %v", err)
	}
	if want := []string{"ONE.txt", "one.txt"}; !slices.Equal(got, want) {
		t.Errorf("folder holds %v, want the untouched %v", got, want)
	}
}

func TestAppFollowsTheFolderInput(t *testing.T) {
	dir := makeDir(t, "one.txt")
	other := makeDir(t, "elsewhere.txt")
	app, screen, onLoop := startApp(t, dir)

	onLoop(func() { app.dirInput.SetText(other) })
	waitForText(t, screen, onLoop, "elsewhere.txt")
	waitForState(t, onLoop, "the folder to change", func() bool {
		return app.dir == other && slices.Equal(app.names, []string{"elsewhere.txt"})
	})
}

func TestSubdirectories(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o750); err != nil {
			t.Fatalf("creating %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "afile"), nil, 0o600); err != nil {
		t.Fatalf("creating afile: %v", err)
	}

	got := subdirectories(filepath.Join(dir, "a"))
	if want := []string{filepath.Join(dir, "alpha")}; !slices.Equal(got, want) {
		t.Errorf("subdirectories() = %v, want %v (files must be skipped)", got, want)
	}
	if got := subdirectories(""); got != nil {
		t.Errorf("subdirectories(\"\") = %v, want nil", got)
	}
	if got := subdirectories(filepath.Join(dir, "nothing", "here")); got != nil {
		t.Errorf("subdirectories() of a missing folder = %v, want nil", got)
	}
}

func TestAcceptIndex(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{text: "", want: true},
		{text: "-", want: true},
		{text: "12", want: true},
		{text: "-3", want: true},
		{text: "t", want: true},
		{text: "to-l", want: true},
		{text: ops.ToLast, want: true},
		{text: "to-lastly", want: false},
		{text: "x", want: false},
		{text: "1a", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			if got := acceptIndex(tt.text, 0); got != tt.want {
				t.Errorf("acceptIndex(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

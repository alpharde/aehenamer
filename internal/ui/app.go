// Package ui implements the terminal interface of the bulk file renamer.
package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"aehenamer/internal/files"
	"aehenamer/internal/ops"
)

// pageMain is the name of the page holding the main layout. Dialogs are added
// as further pages on top of it.
const pageMain = "main"

// App is the interactive bulk file renamer.
type App struct {
	app   *tview.Application
	pages *tview.Pages

	dirInput *tview.InputField
	filePane *tview.Table
	opPane   *tview.Table
	addOp    *tview.DropDown
	applyBtn *tview.Button
	status   *tview.TextView

	// focusRing is the order in which Tab moves between the main widgets.
	focusRing []tview.Primitive

	dir       string
	loadErr   string
	names     []string
	entries   []files.Entry
	conflicts map[int]string

	operations []ops.Operation

	// opRows is how many operations the right pane showed when it was last
	// drawn. An empty pane has no meaningful selection to preserve.
	opRows int

	// screen, when set, replaces the terminal the interface draws on, and
	// ready is closed once the widgets exist. Only tests use them.
	screen tcell.Screen
	ready  chan struct{}

	// dialogFocus remembers which widget had the focus before a floating
	// window was opened, keyed by page name.
	dialogFocus map[string]tview.Primitive

	// pending is the operation currently being edited in a dialog. It is shown
	// in the preview before the user confirms it; pendingIndex is the position
	// it will take in the list, or -1 when it is a new operation.
	pending      ops.Operation
	pendingIndex int
}

// New returns an application that starts out browsing dir.
func New(dir string) *App {
	return &App{
		dir:          dir,
		pendingIndex: -1,
		dialogFocus:  make(map[string]tview.Primitive),
		ready:        make(chan struct{}),
	}
}

// Run builds the interface and runs the event loop until the user quits or ctx
// is cancelled.
func (a *App) Run(ctx context.Context) error {
	a.build(ctx)
	if a.screen != nil {
		a.app.SetScreen(a.screen)
	}

	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			a.app.Stop()
		case <-stopped:
		}
	}()

	a.reload(ctx)
	a.refresh(ctx)
	close(a.ready)

	if err := a.app.Run(); err != nil {
		return fmt.Errorf("running the interface: %w", err)
	}
	return ctx.Err()
}

// build creates every widget and wires the handlers together.
func (a *App) build(ctx context.Context) {
	a.app = tview.NewApplication().EnableMouse(true).EnablePaste(true)
	a.pages = tview.NewPages()

	a.buildDirInput(ctx)
	a.buildFilePane(ctx)
	a.buildOpPane(ctx)
	a.buildControls(ctx)
	a.status = tview.NewTextView().SetDynamicColors(true)

	right := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.opPane, 0, 1, false).
		AddItem(a.controls(), 3, 0, false)

	main := tview.NewFlex().
		AddItem(a.filePane, 0, 2, false).
		AddItem(right, 0, 1, false)

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.dirInput, 3, 0, true).
		AddItem(main, 0, 1, false).
		AddItem(a.status, 1, 0, false)

	a.pages.AddPage(pageMain, layout, true, true)
	a.focusRing = []tview.Primitive{a.dirInput, a.filePane, a.opPane, a.addOp, a.applyBtn}

	a.app.SetInputCapture(a.globalKeys(ctx))
	a.app.SetRoot(a.pages, true).SetFocus(a.dirInput)
}

// controls returns the row holding the "add operation" drop-down and the
// apply button, below the operation list.
func (a *App) controls() tview.Primitive {
	row := tview.NewFlex().
		AddItem(a.addOp, 0, 1, false).
		AddItem(a.applyBtn, 14, 0, false)
	row.SetBorder(true)
	return row
}

// buildDirInput creates the folder picker at the top of the screen. The file
// list follows along as the path is typed, as soon as it names a directory.
func (a *App) buildDirInput(ctx context.Context) {
	a.dirInput = tview.NewInputField().
		SetLabel(" Folder: ").
		SetText(a.dir).
		SetFieldWidth(0)
	a.dirInput.SetBorder(true).SetTitle(" Working folder ")

	a.dirInput.SetAutocompleteFunc(func(current string) []string {
		return subdirectories(current)
	})
	a.dirInput.SetChangedFunc(func(text string) {
		if info, err := os.Stat(text); err == nil && info.IsDir() {
			a.dir = text
			a.reload(ctx)
			a.refresh(ctx)
		}
	})
	a.dirInput.SetDoneFunc(func(key tcell.Key) {
		if key != tcell.KeyEnter {
			return
		}
		a.dir = a.dirInput.GetText()
		a.reload(ctx)
		a.refresh(ctx)
		if a.loadErr == "" {
			a.app.SetFocus(a.filePane)
		}
	})
	a.dirInput.SetFocusFunc(func() {
		a.setStatus("[white]Type a path (Tab completes) · [yellow]Enter[white] loads it · [yellow]Tab[white] next pane · [yellow]F1[white] help")
	})
}

// buildFilePane creates the left pane listing current and proposed names.
func (a *App) buildFilePane(ctx context.Context) {
	a.filePane = tview.NewTable().
		SetFixed(1, 0).
		SetSelectable(true, false)
	a.filePane.SetBorder(true).SetTitle(" Files ")

	a.filePane.SetFocusFunc(func() {
		a.setStatus("[white]↑↓ select · [yellow]x[white]/[yellow]Del[white] drop file from the list · [yellow]Ctrl-R[white] reload folder · [yellow]Tab[white] next pane")
		a.showRowProblem()
	})
	a.filePane.SetSelectionChangedFunc(func(row, column int) {
		if a.filePane.HasFocus() {
			a.showRowProblem()
		}
	})
	a.filePane.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch {
		case event.Key() == tcell.KeyDelete, event.Rune() == 'x':
			a.removeSelectedFile(ctx)
			return nil
		case event.Key() == tcell.KeyCtrlR:
			a.reload(ctx)
			a.refresh(ctx)
			return nil
		}
		return event
	})
}

// buildOpPane creates the right pane listing the operations to apply.
func (a *App) buildOpPane(ctx context.Context) {
	a.opPane = tview.NewTable().SetSelectable(true, false)
	a.opPane.SetBorder(true).SetTitle(" Operations ")

	a.opPane.SetFocusFunc(func() {
		a.setStatus("[white]↑↓ select · [yellow]Enter[white]/[yellow]e[white] edit · [yellow]x[white]/[yellow]Del[white] remove · [yellow]u[white]/[yellow]d[white] move up/down · [yellow]a[white] add")
	})
	a.opPane.SetSelectedFunc(func(row, column int) {
		a.editOperation(ctx, row)
	})
	a.opPane.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		row, _ := a.opPane.GetSelection()
		switch {
		case event.Key() == tcell.KeyDelete, event.Rune() == 'x':
			a.removeOperation(ctx, row)
		case event.Rune() == 'e':
			a.editOperation(ctx, row)
		case event.Rune() == 'u':
			a.moveOperation(ctx, row, -1)
		case event.Rune() == 'd':
			a.moveOperation(ctx, row, 1)
		case event.Rune() == 'a':
			a.app.SetFocus(a.addOp)
		default:
			return event
		}
		return nil
	})
}

// buildControls creates the "add operation" drop-down and the apply button.
func (a *App) buildControls(ctx context.Context) {
	labels := make([]string, len(ops.Kinds))
	for i, kind := range ops.Kinds {
		labels[i] = string(kind)
	}

	a.addOp = tview.NewDropDown().
		SetLabel(" Add operation ").
		SetOptions(labels, nil)
	a.addOp.SetSelectedFunc(func(text string, index int) {
		if index < 0 || index >= len(ops.Kinds) {
			return
		}
		kind := ops.Kinds[index]
		// Clear the choice so that the same operation can be picked again.
		a.addOp.SetCurrentOption(-1)
		a.openOperationDialog(ctx, kind, ops.DefaultConfig(kind), -1)
	})
	a.addOp.SetFocusFunc(func() {
		a.setStatus("[white]Press [yellow]Enter[white] or click to open the list of operations · [yellow]↑↓[white] choose · [yellow]Esc[white] cancel")
	})

	a.applyBtn = tview.NewButton("Apply rename").SetSelectedFunc(func() {
		a.confirmApply(ctx)
	})
	a.applyBtn.SetFocusFunc(func() {
		a.setStatus("[white]Press [yellow]Enter[white] to rename the listed files on disk · [yellow]Tab[white] next pane")
	})
}

// globalKeys returns the application wide key handler.
func (a *App) globalKeys(ctx context.Context) func(*tcell.EventKey) *tcell.EventKey {
	return func(event *tcell.EventKey) *tcell.EventKey {
		if name, _ := a.pages.GetFrontPage(); name != pageMain {
			// A dialog is open; it handles its own keys.
			return event
		}
		switch event.Key() {
		case tcell.KeyCtrlQ:
			a.app.Stop()
			return nil
		case tcell.KeyF1:
			a.showHelp()
			return nil
		case tcell.KeyCtrlR:
			a.reload(ctx)
			a.refresh(ctx)
			return nil
		case tcell.KeyTab:
			a.closeOperationMenu()
			a.cycleFocus(1)
			return nil
		case tcell.KeyBacktab:
			a.closeOperationMenu()
			a.cycleFocus(-1)
			return nil
		}
		return event
	}
}

// closeOperationMenu closes the drop-down's embedded list before focus moves
// elsewhere. Otherwise the list remains internally open after a global Tab
// shortcut pulls focus away from it.
func (a *App) closeOperationMenu() {
	if !a.addOp.IsOpen() {
		return
	}
	handler := a.addOp.InputHandler()
	if handler == nil {
		return
	}
	handler(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), func(p tview.Primitive) {
		a.app.SetFocus(p)
	})
}

// cycleFocus moves the focus by delta positions around the focus ring.
func (a *App) cycleFocus(delta int) {
	current := a.app.GetFocus()
	for i, p := range a.focusRing {
		if p != current {
			continue
		}
		next := (i + delta + len(a.focusRing)) % len(a.focusRing)
		a.app.SetFocus(a.focusRing[next])
		return
	}
	a.app.SetFocus(a.focusRing[0])
}

// reload reads the working folder again, dropping any files the user had
// removed from the list.
func (a *App) reload(ctx context.Context) {
	names, err := files.List(ctx, a.dir)
	if err != nil {
		a.loadErr = err.Error()
		a.names = nil
		return
	}
	a.loadErr = ""
	a.names = names
}

// effectiveOps returns the operation list including the operation currently
// being edited, so that a dialog can be previewed before it is confirmed.
func (a *App) effectiveOps() []ops.Operation {
	if a.pending == nil {
		return a.operations
	}
	list := make([]ops.Operation, len(a.operations))
	copy(list, a.operations)
	if a.pendingIndex >= 0 && a.pendingIndex < len(list) {
		list[a.pendingIndex] = a.pending
		return list
	}
	return append(list, a.pending)
}

// refresh recomputes the proposed names and redraws both panes.
func (a *App) refresh(ctx context.Context) {
	proposed := ops.Preview(a.names, a.effectiveOps())
	a.entries = make([]files.Entry, len(a.names))
	for i, name := range a.names {
		a.entries[i] = files.Entry{Original: name, Proposed: proposed[i]}
	}
	a.conflicts = files.Conflicts(ctx, a.dir, a.entries)

	a.drawFiles(ctx)
	a.drawOperations(ctx)
}

// drawFiles fills the left pane with the current and proposed names.
func (a *App) drawFiles(ctx context.Context) {
	selected, _ := a.filePane.GetSelection()
	a.filePane.Clear()
	a.filePane.SetSelectable(len(a.entries) > 0, false)

	header := func(column int, text string, expansion int) {
		a.filePane.SetCell(0, column, tview.NewTableCell(text).
			SetTextColor(tcell.ColorYellow).
			SetSelectable(false).
			SetExpansion(expansion))
	}
	header(0, " Current name", 1)
	header(1, " New name", 1)
	header(2, "", 0)

	for i, entry := range a.entries {
		row := i + 1
		index := i

		a.filePane.SetCell(row, 0, tview.NewTableCell(" "+tview.Escape(entry.Original)).SetExpansion(1))

		proposed := tview.NewTableCell(" " + tview.Escape(entry.Proposed)).SetExpansion(1)
		switch {
		case a.conflicts[index] != "":
			proposed.SetTextColor(tcell.ColorRed)
		case entry.Changed():
			proposed.SetTextColor(tcell.ColorGreen)
		default:
			proposed.SetTextColor(tcell.ColorGray)
		}
		a.filePane.SetCell(row, 1, proposed)

		remove := tview.NewTableCell(" ✕ ").
			SetTextColor(tcell.ColorRed).
			SetAlign(tview.AlignCenter)
		remove.Clicked = func() bool {
			a.removeFile(ctx, index)
			return true
		}
		a.filePane.SetCell(row, 2, remove)
	}

	title := fmt.Sprintf(" Files (%d) — %s ", len(a.entries), tview.Escape(a.dir))
	if a.loadErr != "" {
		title = " Files — folder cannot be read "
	}
	a.filePane.SetTitle(title)

	last := len(a.entries)
	if last == 0 {
		a.filePane.Select(0, 0)
		return
	}
	a.filePane.Select(clamp(selected, 1, last), 0)
}

// drawOperations fills the right pane with the operation list.
func (a *App) drawOperations(ctx context.Context) {
	selected := 0
	if a.opRows > 0 {
		selected, _ = a.opPane.GetSelection()
	}
	a.opRows = len(a.operations)
	a.opPane.Clear()
	a.opPane.SetSelectable(len(a.operations) > 0, false)

	if len(a.operations) == 0 {
		a.opPane.SetCell(0, 0, tview.NewTableCell(" No operations yet — use “Add operation” below ").
			SetTextColor(tcell.ColorGray).
			SetSelectable(false).
			SetExpansion(1))
		a.opPane.Select(0, 0)
		return
	}

	for i, op := range a.operations {
		index := i
		a.opPane.SetCell(i, 0, tview.NewTableCell(fmt.Sprintf(" %d.", i+1)).
			SetTextColor(tcell.ColorYellow))
		a.opPane.SetCell(i, 1, tview.NewTableCell(tview.Escape(op.Summary())).SetExpansion(1))

		remove := tview.NewTableCell(" [Remove] ").SetTextColor(tcell.ColorRed)
		remove.Clicked = func() bool {
			a.removeOperation(ctx, index)
			return true
		}
		a.opPane.SetCell(i, 2, remove)
	}

	a.opPane.Select(clamp(selected, 0, len(a.operations)-1), 0)
}

// clamp confines n to the inclusive range [low, high].
func clamp(n, low, high int) int {
	if n < low {
		return low
	}
	if n > high {
		return high
	}
	return n
}

// removeSelectedFile drops the file under the cursor from the list.
func (a *App) removeSelectedFile(ctx context.Context) {
	row, _ := a.filePane.GetSelection()
	a.removeFile(ctx, row-1)
}

// removeFile drops the file at index from the list. The file itself is left
// untouched on disk.
func (a *App) removeFile(ctx context.Context, index int) {
	if index < 0 || index >= len(a.names) {
		return
	}
	name := a.names[index]
	a.names = append(a.names[:index:index], a.names[index+1:]...)
	a.refresh(ctx)
	a.setStatus("[white]Removed [yellow]%s[white] from the list · [yellow]Ctrl-R[white] brings it back", tview.Escape(name))
}

// removeOperation deletes the operation at index.
func (a *App) removeOperation(ctx context.Context, index int) {
	if index < 0 || index >= len(a.operations) {
		return
	}
	summary := a.operations[index].Summary()
	a.operations = append(a.operations[:index:index], a.operations[index+1:]...)
	a.refresh(ctx)
	a.setStatus("[white]Removed operation: %s", tview.Escape(summary))
}

// moveOperation moves the operation at index by delta places, changing the
// order in which operations are applied.
func (a *App) moveOperation(ctx context.Context, index, delta int) {
	target := index + delta
	if index < 0 || index >= len(a.operations) || target < 0 || target >= len(a.operations) {
		return
	}
	a.operations[index], a.operations[target] = a.operations[target], a.operations[index]
	a.refresh(ctx)
	a.opPane.Select(target, 0)
}

// showRowProblem explains in the status bar why the selected file cannot be
// renamed, if it cannot.
func (a *App) showRowProblem() {
	row, _ := a.filePane.GetSelection()
	if reason, bad := a.conflicts[row-1]; bad {
		a.setStatus("[red]%s[white] — fix the operations or drop the file with [yellow]x", reason)
	}
}

// setStatus writes a message to the status bar. The text may contain tview
// colour tags.
func (a *App) setStatus(format string, args ...any) {
	a.status.SetText(" " + fmt.Sprintf(format, args...))
}

// subdirectories returns directory paths that continue the partially typed
// path, for the folder input's autocompletion.
func subdirectories(current string) []string {
	if current == "" {
		return nil
	}
	dir, prefix := filepath.Split(current)
	search := dir
	if search == "" {
		search = "."
	}
	entries, err := os.ReadDir(search)
	if err != nil {
		return nil
	}
	const maxEntries = 25
	var out []string
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		out = append(out, dir+entry.Name())
		if len(out) == maxEntries {
			break
		}
	}
	if len(out) == 1 && out[0] == current {
		return nil
	}
	return out
}

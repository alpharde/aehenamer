package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"aehenamer/internal/files"
	"aehenamer/internal/ops"
)

// Names of the floating windows shown on top of the main layout.
const (
	pageOperation = "operation"
	pageConfirm   = "confirm"
	pageMessage   = "message"
	pageHelp      = "help"
)

// fieldWidth values used by the operation dialogs.
const (
	textWidth   = 28
	numberWidth = 10
)

// editOperation opens the editor for the operation shown on the given row of
// the operation pane.
func (a *App) editOperation(ctx context.Context, row int) {
	if row < 0 || row >= len(a.operations) {
		return
	}
	cfg := a.operations[row].Config()
	a.openOperationDialog(ctx, cfg.Kind, cfg, row)
}

// openOperationDialog shows the floating window in which the values of an
// operation are entered. index is the position of the operation being edited,
// or -1 when a new one is being added. The file list previews the operation
// while the dialog is open.
func (a *App) openOperationDialog(ctx context.Context, kind ops.Kind, cfg ops.Config, index int) {
	cfg.Kind = kind
	form := tview.NewForm()

	preview := func() { a.setPending(ctx, cfg, index) }

	text := func(label, value string, set func(string)) {
		form.AddInputField(label, value, textWidth, nil, func(entered string) {
			set(entered)
			preview()
		})
	}
	number := func(label, value string, accept func(string, rune) bool, set func(string)) {
		form.AddInputField(label, value, numberWidth, accept, func(entered string) {
			set(entered)
			preview()
		})
	}
	check := func(label string, value bool, set func(bool)) {
		form.AddCheckbox(label, value, func(checked bool) {
			set(checked)
			preview()
		})
	}

	switch kind {
	case ops.KindReplace, ops.KindRemove:
		target := "Replace target"
		if kind == ops.KindRemove {
			target = "Remove target"
		}
		text(target, cfg.Target, func(v string) { cfg.Target = v })
		if kind == ops.KindReplace {
			text("Replacement", cfg.Replacement, func(v string) { cfg.Replacement = v })
		}
		number("Limit (0 = all)", cfg.Limit, acceptInt, func(v string) { cfg.Limit = v })
		check("From start", cfg.FromStart, func(v bool) { cfg.FromStart = v })
		check("Case sensitive", cfg.CaseSensitive, func(v bool) { cfg.CaseSensitive = v })
		check(strings.ToLower(string(kind))+" target is regex", cfg.Regex, func(v bool) { cfg.Regex = v })
		check("Preserve extension", cfg.PreserveExt, func(v bool) { cfg.PreserveExt = v })

	case ops.KindInsert:
		text("Text to insert", cfg.Text, func(v string) { cfg.Text = v })
		number("Insert index ("+ops.ToLast+")", cfg.Index, acceptIndex, func(v string) { cfg.Index = v })
		check("Preserve extension", cfg.PreserveExt, func(v bool) { cfg.PreserveExt = v })

	case ops.KindIncrement:
		text("Prefix", cfg.Prefix, func(v string) { cfg.Prefix = v })
		number("Number to start", cfg.Start, acceptInt, func(v string) { cfg.Start = v })
		number("Incremental step", cfg.Step, acceptInt, func(v string) { cfg.Step = v })
		check("Preserve extension", cfg.PreserveExt, func(v bool) { cfg.PreserveExt = v })

	case ops.KindLower, ops.KindUpper:
		check("Preserve extension", cfg.PreserveExt, func(v bool) { cfg.PreserveExt = v })

	case ops.KindTruncate:
		number("First character index", cfg.First, acceptIndex, func(v string) { cfg.First = v })
		number("Last character index", cfg.Last, acceptIndex, func(v string) { cfg.Last = v })
		check("Keep characters between mode", cfg.KeepBetween, func(v bool) { cfg.KeepBetween = v })
		check("Preserve extension", cfg.PreserveExt, func(v bool) { cfg.PreserveExt = v })
	}

	cancel := func() {
		a.clearPending(ctx)
		a.closeDialog(pageOperation)
	}
	form.AddButton("OK", func() {
		op, err := ops.New(cfg)
		if err != nil {
			a.showMessage("Invalid value", err.Error())
			return
		}
		if index >= 0 && index < len(a.operations) {
			a.operations[index] = op
		} else {
			a.operations = append(a.operations, op)
		}
		a.clearPending(ctx)
		a.closeDialog(pageOperation)
		a.app.SetFocus(a.opPane)
		a.setStatus("[white]%s: %s", verb(index), tview.Escape(op.Summary()))
	})
	form.AddButton("Cancel", cancel)
	form.SetCancelFunc(cancel)

	form.SetBorder(true).
		SetTitle(fmt.Sprintf(" %s — %s ", verb(index), kind)).
		SetTitleAlign(tview.AlignLeft)
	form.SetFocusFunc(func() {
		a.setStatus("[white]↑↓/[yellow]Tab[white] move between fields · [yellow]Space[white] toggles a checkbox · [yellow]Enter[white] on OK confirms · [yellow]Esc[white] cancels")
	})

	height := form.GetFormItemCount()*2 + 5
	a.showDialog(pageOperation, form, 62, height)
	preview()
}

// verb describes whether an operation is being added or edited.
func verb(index int) string {
	if index >= 0 {
		return "Edit operation"
	}
	return "Add operation"
}

// setPending previews cfg in the file list without adding it to the operation
// list. Invalid values are reported in the status bar as they are typed.
func (a *App) setPending(ctx context.Context, cfg ops.Config, index int) {
	op, err := ops.New(cfg)
	if err != nil {
		a.pending = nil
		a.refresh(ctx)
		a.setStatus("[red]%s", tview.Escape(err.Error()))
		return
	}
	a.pending = op
	a.pendingIndex = index
	a.refresh(ctx)
	a.setStatus("[white]Previewing: %s", tview.Escape(op.Summary()))
}

// clearPending drops the previewed operation and restores the file list.
func (a *App) clearPending(ctx context.Context) {
	a.pending = nil
	a.pendingIndex = -1
	a.refresh(ctx)
}

// confirmApply checks the plan and asks the user before touching the disk.
func (a *App) confirmApply(ctx context.Context) {
	if name, _ := a.pages.GetFrontPage(); name != pageMain {
		// A dialog is open and the file list only shows a preview of it.
		return
	}
	if len(a.conflicts) > 0 {
		a.showMessage("Cannot rename", fmt.Sprintf("%d file(s) have a name conflict.\nThe offending names are shown in red.", len(a.conflicts)))
		return
	}
	changed := 0
	for _, entry := range a.entries {
		if entry.Changed() {
			changed++
		}
	}
	if changed == 0 {
		a.setStatus("[yellow]Nothing to rename[white] — add an operation that changes the names")
		return
	}

	modal := tview.NewModal().
		SetText(fmt.Sprintf("Rename %d file(s) in\n%s?\n\nThe operation list is cleared afterwards.", changed, a.dir)).
		AddButtons([]string{"Rename", "Cancel"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			a.closeDialog(pageConfirm)
			if buttonLabel != "Rename" {
				return
			}
			a.applyRename(ctx)
		})
	modal.SetInputCapture(dismissOnEscape(func() { a.closeDialog(pageConfirm) }))

	a.dialogFocus[pageConfirm] = a.app.GetFocus()
	a.pages.AddPage(pageConfirm, modal, true, true)
	a.app.SetFocus(modal)
}

// applyRename performs the rename on disk and reloads the folder.
func (a *App) applyRename(ctx context.Context) {
	renamed, err := files.Rename(ctx, a.dir, a.entries)
	if err != nil {
		a.reload(ctx)
		a.refresh(ctx)
		a.showMessage("Rename failed", err.Error())
		return
	}
	a.operations = nil
	a.reload(ctx)
	a.refresh(ctx)
	a.setStatus("[green]Renamed %d file(s)[white] in %s", renamed, a.dir)
}

// showMessage reports an error or a notice in a floating window.
func (a *App) showMessage(title, body string) {
	modal := tview.NewModal().
		SetText(title + "\n\n" + body).
		AddButtons([]string{"OK"}).
		SetDoneFunc(func(int, string) { a.closeDialog(pageMessage) })
	modal.SetInputCapture(dismissOnEscape(func() { a.closeDialog(pageMessage) }))

	a.dialogFocus[pageMessage] = a.app.GetFocus()
	a.pages.AddPage(pageMessage, modal, true, true)
	a.app.SetFocus(modal)
}

// helpText lists every key binding of the interface.
const helpText = `[yellow]Anywhere[white]
  Tab / Shift-Tab   move between folder, files, operations, buttons
  F1                this help
  Ctrl-R            reload the folder (restores removed files)
  Ctrl-Q / Ctrl-C   quit

[yellow]Folder input[white]
  Tab               complete the typed path
  Enter             load the folder

[yellow]File list[white]
  Up / Down         select a file
  x or Delete       drop the selected file from the list
  click on ✕        drop that file from the list

[yellow]Operation list[white]
  Enter or e        edit the selected operation
  x or Delete       remove the selected operation
  u / d             move the operation up or down
  a                 jump to the "Add operation" drop-down

[yellow]Dialogs[white]
  Tab / Up / Down   move between fields
  Space             toggle a checkbox
  Enter             activate the focused button
  Esc               cancel

[yellow]Note[white]
  Renaming is applied only when you press "Apply rename".
  Every file is renamed through a temporary name, so names can be swapped.`

// showHelp displays the key bindings.
func (a *App) showHelp() {
	view := tview.NewTextView().
		SetDynamicColors(true).
		SetText(helpText)
	view.SetBorder(true).SetTitle(" Keyboard and mouse ").SetTitleAlign(tview.AlignLeft)
	view.SetInputCapture(dismissOnEscape(func() { a.closeDialog(pageHelp) }))
	a.showDialog(pageHelp, view, 70, 30)
}

// dismissOnEscape returns a key handler that runs close on Escape, Enter or
// F1 and passes every other key through.
func dismissOnEscape(close func()) func(*tcell.EventKey) *tcell.EventKey {
	return func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEscape, tcell.KeyF1:
			close()
			return nil
		}
		return event
	}
}

// showDialog puts p on top of the main layout as a centred floating window.
func (a *App) showDialog(name string, p tview.Primitive, width, height int) {
	a.dialogFocus[name] = a.app.GetFocus()
	a.pages.AddPage(name, center(p, width, height), true, true)
	a.app.SetFocus(p)
}

// closeDialog removes a floating window and restores the previous focus.
func (a *App) closeDialog(name string) {
	a.pages.RemovePage(name)
	if previous, ok := a.dialogFocus[name]; ok {
		delete(a.dialogFocus, name)
		if previous != nil {
			a.app.SetFocus(previous)
		}
	}
}

// center wraps p in a layout that keeps it at the given size in the middle of
// the screen.
func center(p tview.Primitive, width, height int) tview.Primitive {
	column := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(p, height, 0, true).
		AddItem(nil, 0, 1, false)
	return tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(column, width, 0, true).
		AddItem(nil, 0, 1, false)
}

// acceptInt accepts a possibly negative decimal number, or an empty field.
func acceptInt(text string, _ rune) bool {
	if text == "" || text == "-" {
		return true
	}
	_, err := strconv.Atoi(text)
	return err == nil
}

// acceptIndex accepts a character index: a number, an empty field, or the
// [ops.ToLast] keyword as it is being typed.
func acceptIndex(text string, _ rune) bool {
	if acceptInt(text, 0) {
		return true
	}
	return strings.HasPrefix(ops.ToLast, strings.ToLower(text))
}

package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"fyne.io/fyne/v2"
)

// The Linux picker is the desktop's own, asked for through whichever of the
// standard helper programs is installed. GTK and Qt both want a toolkit binding
// and a main loop of their own — the thread Fyne paints from — so the dialog is
// a process beside the client rather than a layer over it, which is the same
// arrangement the Windows one is in and for the same reason.
//
// Nothing here is a dependency: a desktop that ships none of them falls back to
// Fyne's browser, which is what reporting false means.

// pickers are the helpers, in the order they are looked for. The three zenity
// clones take the same flags; kdialog is KDE's and takes its own, so it is the
// one the caller has to branch on.
var pickers = []string{"zenity", "qarma", "matedialog", "kdialog"}

// PickFile shows the desktop's open dialog, reporting whether one was found at
// all. onDone runs on the UI thread, with an empty path and no error when the
// reader cancelled.
func PickFile(_ fyne.Window, title string, filter FileFilter, onDone func(path string, err error)) bool {
	return runPicker(title, filter, false, onDone)
}

// PickFolder shows the same dialog in folder mode, on the same terms as PickFile.
func PickFolder(_ fyne.Window, title string, onDone func(path string, err error)) bool {
	return runPicker(title, FileFilter{}, true, onDone)
}

// runPicker finds a helper, runs it on a goroutine and reports back on the UI
// thread. False means there was nothing to run and the caller still has a dialog
// to show.
func runPicker(title string, filter FileFilter, folders bool, onDone func(string, error)) bool {
	tool, args, ok := pickerCommand(title, filter, folders)
	if !ok {
		return false
	}

	go func() {
		out, err := exec.Command(tool, args...).Output()
		path := strings.TrimSpace(string(out))

		var exit *exec.ExitError
		if errors.As(err, &exit) {
			// Every one of these exits 1 for a dialog that was dismissed, which is an
			// answer rather than a failure. Anything else is a failure worth naming.
			if exit.ExitCode() == 1 {
				path, err = "", nil
			} else if message := strings.TrimSpace(string(exit.Stderr)); message != "" {
				err = fmt.Errorf("%s: %s", tool, message)
			}
		}

		DoOnUI(func() { onDone(path, err) })
	}()

	return true
}

// pickerCommand is the helper to run and how to ask it, or false where none is
// installed.
func pickerCommand(title string, filter FileFilter, folders bool) (string, []string, bool) {
	for _, name := range pickers {
		tool, err := exec.LookPath(name)
		if err != nil {
			continue
		}

		if name == "kdialog" {
			return tool, kdialogArgs(title, filter, folders), true
		}

		return tool, zenityArgs(title, filter, folders), true
	}

	return "", nil, false
}

// zenityArgs is how zenity and its two clones are asked. The filter is one line
// — a label, then the patterns behind it — and is left off entirely for a
// dialog that takes any kind, an empty one reading as "nothing".
func zenityArgs(title string, filter FileFilter, folders bool) []string {
	args := []string{"--file-selection", "--title=" + title}
	if folders {
		return append(args, "--directory")
	}

	if len(filter.Extensions) > 0 {
		args = append(args, "--file-filter="+filter.Label+" | "+strings.Join(globs(filter), " "))
	}

	return args
}

// kdialogArgs is KDE's spelling of the same question. Both of its modes take the
// directory to open in as a positional argument, and its filter puts the
// patterns before the label rather than after.
func kdialogArgs(title string, filter FileFilter, folders bool) []string {
	start, err := os.UserHomeDir()
	if err != nil {
		start = "."
	}

	if folders {
		return []string{"--title", title, "--getexistingdirectory", start}
	}

	args := []string{"--title", title, "--getopenfilename", start}
	if len(filter.Extensions) > 0 {
		args = append(args, strings.Join(globs(filter), " ")+"|"+filter.Label)
	}

	return args
}

// globs turns the extensions a filter names into the patterns both helpers match
// on.
func globs(filter FileFilter) []string {
	patterns := make([]string, len(filter.Extensions))
	for i, ext := range filter.Extensions {
		patterns[i] = "*" + ext
	}

	return patterns
}

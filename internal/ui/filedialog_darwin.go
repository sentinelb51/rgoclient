package ui

import (
	"errors"
	"os/exec"
	"strings"

	"fyne.io/fyne/v2"
)

// The macOS picker is AppKit's, asked for through osascript: NSOpenPanel has no
// binding in the standard library and cgo into AppKit would have to run on the
// process's main thread, which is the one Fyne paints from. A subprocess is a
// window beside the client rather than a layer over it, on the same terms as the
// Windows dialog, and it is the panel every other program on the machine opens.
//
// The script runs inside a `tell application "System Events"` block because a
// panel raised by a background process opens behind whatever is in front of it;
// that is what brings it forward.

// PickFile shows the standard open panel, reporting true so the caller knows not
// to fall back. onDone runs on the UI thread, with an empty path and no error
// when the reader cancelled.
func PickFile(_ fyne.Window, title string, filter FileFilter, onDone func(path string, err error)) bool {
	runOSAScript(chooseScript("choose file", title, filter), onDone)

	return true
}

// PickFolder shows the same panel in folder mode, on the same terms as PickFile.
func PickFolder(_ fyne.Window, title string, onDone func(path string, err error)) bool {
	runOSAScript(chooseScript("choose folder", title, FileFilter{}), onDone)

	return true
}

// cancelled is what osascript says when the panel was dismissed. It exits
// non-zero either way, so the sentence is the only thing telling a refusal from
// a failure.
const cancelled = "-128"

// chooseScript builds the one AppleScript line the panel is asked for with.
// POSIX path is what turns the alias the panel answers with into a path the rest
// of the client can open.
func chooseScript(verb, title string, filter FileFilter) string {
	var b strings.Builder

	b.WriteString("POSIX path of (")
	b.WriteString(verb)
	b.WriteString(" with prompt ")
	b.WriteString(quoteAppleScript(title))

	// The panel takes bare extensions as readily as UTIs, and an empty list would
	// be read as "nothing may be picked" rather than "anything".
	if len(filter.Extensions) > 0 {
		b.WriteString(" of type {")
		for i, ext := range filter.Extensions {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(quoteAppleScript(strings.TrimPrefix(ext, ".")))
		}
		b.WriteString("}")
	}

	b.WriteString(")")

	return b.String()
}

// quoteAppleScript wraps s as an AppleScript string literal. Only the backslash
// and the quote mean anything inside one.
func quoteAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\`)
	s = strings.ReplaceAll(s, `"`, `\"`)

	return `"` + s + `"`
}

// runOSAScript runs the panel on a goroutine and reports back on the UI thread.
func runOSAScript(script string, onDone func(string, error)) {
	go func() {
		out, err := exec.Command("osascript",
			"-e", `tell application "System Events"`,
			"-e", "activate",
			"-e", script,
			"-e", "end tell",
		).Output()

		path := strings.TrimSpace(string(out))

		var exit *exec.ExitError
		if errors.As(err, &exit) {
			// A dismissal is an answer, not a failure — see cancelled.
			if strings.Contains(string(exit.Stderr), cancelled) {
				path, err = "", nil
			} else if message := strings.TrimSpace(string(exit.Stderr)); message != "" {
				err = errors.New(message)
			}
		}

		DoOnUI(func() { onDone(path, err) })
	}()
}

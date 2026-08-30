package ui

import "sync"

// TrayAvailable reports whether this desktop draws a notification area, which is
// the whole of whether the client may hide its window: the icon is the only way
// back to a hidden one.
//
// Probed once. The answer is a property of the session rather than of the
// moment, and on the desktops where finding it out costs a bus call the settings
// search builds every section twice.
var TrayAvailable = sync.OnceValue(trayAvailable)

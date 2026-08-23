//go:build !windows

package ui

// There is no portable way to ask whether a key is held without canvas focus,
// and the composer holds focus for most of the client's life — see the
// modifier-key footgun in CLAUDE.md. X11 needs XQueryKeymap on a display
// connection this client does not own, and macOS needs an Accessibility grant
// the user has to give in System Settings.
//
// So push-to-talk is Windows-only for now. PushToTalkSupported is false here and
// the settings page leaves the mode out entirely rather than offering one that
// silently behaves as voice activity — which is the whole reason this pair of
// files exists rather than a stub returning false.

// PushToTalkKeys is empty where nothing can be bound.
func PushToTalkKeys() []string { return nil }

// KeyHeld always answers false. Nothing calls it: the mode it serves is not
// offered on this platform.
func KeyHeld(string) bool { return false }

// PushToTalkSupported is what keeps the mode off a page where it would not work.
const PushToTalkSupported = false

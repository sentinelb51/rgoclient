package ui

import "fyne.io/fyne/v2"

// DoOnUI schedules fn on the Fyne UI thread and returns immediately. Fyne
// widgets may only be touched from that thread, so every background callback in
// this package funnels through here rather than reaching for the driver itself.
//
// Two siblings exist for reasons the import graph forces: App.doOnUI (which can
// also block until fn returns, and which every gateway handler uses) and the one
// direct call in internal/cache, which cannot import this package.
func DoOnUI(fn func()) {
	fyne.CurrentApp().Driver().DoFromGoroutine(fn, false)
}

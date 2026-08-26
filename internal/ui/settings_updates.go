package ui

// The Updates section: whether the client asks GitHub for a newer release of
// itself, what the last answer was, and the two links that answer is worth — the
// build for this machine, and the release page listing every one of them.
//
// Nothing here downloads and nothing here replaces a binary. The browser does
// that, which is why the whole section is four rows and a fold.

import (
	"strings"

	"fyne.io/fyne/v2"

	"RGOClient/internal/config"
	"RGOClient/internal/markdown"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

func (p *SettingsPage) updatesSection() []settingsGroup {
	state := p.updateStatus()

	groups := []settingsGroup{
		p.group("Checking", "",
			p.boolRow("Check at startup",
				"Asks GitHub for the newest release once, after you sign in.",
				config.Current().Updates.Check, func(on bool) {
					p.change(func(s *config.Settings) { s.Updates.Check = on })
				}),
			p.actionRow("Check now", lastChecked(state), "Check", ToneInfo, p.checkUpdate),
		),
	}

	if group, ok := p.updateAnswer(state); ok {
		groups = append(groups, group)
	}

	return groups
}

// updateStatus is what the controller last found. The hook is optional the way
// the device hooks are — a page built to be walked rather than shown carries only
// what the walk is about.
func (p *SettingsPage) updateStatus() UpdateState {
	if p.hooks.UpdateStatus == nil {
		return UpdateState{}
	}

	return p.hooks.UpdateStatus()
}

// checkUpdate asks for an answer now. The controller single-flights the request
// and redraws this section itself — both when it claims the request and when the
// answer lands — so pressing again while one is out does nothing rather than
// asking GitHub twice.
func (p *SettingsPage) checkUpdate() {
	if p.hooks.CheckUpdate == nil {
		return
	}

	p.hooks.CheckUpdate(p.RefreshUpdates)
}

// RefreshUpdates redraws the section for an answer that landed while it was on
// screen — the startup check, which nobody on this page asked for. UI thread; a
// no-op unless that section is the one showing.
func (p *SettingsPage) RefreshUpdates() {
	if !p.IsOpen() || p.searching || p.section != SectionUpdates {
		return
	}

	p.reload()
}

// lastChecked is the Check row's own explanation. An age rather than a clock
// time: what the reader wants to know is whether the answer below is this run's.
func lastChecked(state UpdateState) string {
	switch {
	case state.Checking:
		return "Asking GitHub…"
	case state.Checked.IsZero():
		return "Not checked yet this run."
	}

	return "Last checked " + util.ShortAgo(state.Checked) + "."
}

// updateAnswer is the group the last check earned: the release and what to do
// about it, the reason there is none, or nothing at all before the first check.
//
// A failure is drawn here rather than reported as a notice — nobody asked for the
// startup check, so nobody should be interrupted by it going wrong.
func (p *SettingsPage) updateAnswer(state UpdateState) (settingsGroup, bool) {
	switch {
	case state.Err != "":
		return p.group("Latest release", "", p.note(state.Err)), true

	case state.Available:
		return p.updateGroup(state), true

	case !state.Checked.IsZero():
		return p.group("Latest release", "",
			p.note("Nothing newer than "+state.Current+" is published.")), true
	}

	return settingsGroup{}, false
}

// updateGroup is the release itself: the build for this machine, the page listing
// every build, and the notes folded under them.
func (p *SettingsPage) updateGroup(state UpdateState) settingsGroup {
	release := state.Release

	detail := "You are running " + state.Current + "."
	if !release.Published.IsZero() {
		detail = "Published " + util.FullDate(release.Published) + ". " + detail
	}

	rows := append([]fyne.CanvasObject{
		p.downloadRow(state),
		p.actionRow("Release page",
			"Every build for this release, and the notes as GitHub renders them.",
			"Open", ToneInfo, func() { p.openLink(release.PageURL) }),
	}, p.changelogRows(release.Notes)...)

	return p.group("RGOClient "+release.Version, detail, rows...)
}

// downloadRow offers the one asset built for this machine. A platform the release
// matrix does not cover has none at all, and saying so beats handing over a
// binary for something else — so that case is a note rather than a button.
func (p *SettingsPage) downloadRow(state UpdateState) fyne.CanvasObject {
	release := state.Release

	if release.AssetURL == "" {
		return p.note("No build is published for " + state.Platform +
			". The release page lists what there is.")
	}

	detail := release.AssetName + " — " + util.FormatFileSize(int(release.AssetSize)) +
		", for " + state.Platform + "."

	return p.actionRow("Download", detail, "Download", ToneInfo,
		func() { p.openLink(release.AssetURL) })
}

// changelogRows are the disclosure and, when it is open, the notes themselves.
// Drawn through the client's own Markdown rather than shown as source: a release
// body is bullets and links, and reading it as text is reading the punctuation.
func (p *SettingsPage) changelogRows(notes string) []fyne.CanvasObject {
	notes = strings.TrimSpace(notes)
	if notes == "" {
		return nil
	}

	action, detail := "Show", "The release notes, folded away."
	if p.changelog {
		action, detail = "Hide", "The release notes, as they were written."
	}

	rows := []fyne.CanvasObject{
		p.actionRow("What changed", detail, action, ToneInfo, func() {
			p.changelog = !p.changelog
			p.reload()
		}),
	}
	if !p.changelog || p.indexing {
		return rows
	}

	return append(rows, p.block(p.renderNotes(notes)))
}

// renderNotes draws a release body at the card's own width. Three wrappers, each
// answering a different thing: the width is pinned because a wrapping Label
// reports what it would take *unwrapped*, which is a paragraph wider than the
// card; NewWrapColumn is what measures a wrapping child after sizing it; and the
// flush container cancels the RichText's own inner padding, which every other
// caller of a message body cancels too — without it the notes start a step right
// of the row labels above them.
//
// The context menu is a no-op rather than nil — a body's selection catcher calls
// it on every right-click without asking whether there is one.
func (p *SettingsPage) renderNotes(notes string) fyne.CanvasObject {
	width := cardWidth() - 2*theme.Sizes.SettingsRowPaddingH
	body := renderDocument(p.hooks.Deps, markdown.Parse(notes), func(*fyne.PointEvent) {})

	return NewFixedWidthContainer(width, newFlushContainer(NewWrapColumn(body)))
}

// openLink hands a URL to the browser, if the controller gave somewhere to hand
// it to.
func (p *SettingsPage) openLink(url string) {
	if p.hooks.OpenLink == nil {
		return
	}

	p.hooks.OpenLink(url)
}

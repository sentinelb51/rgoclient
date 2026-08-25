package ui

// The settings sections. Each returns the groups its pane shows, top to bottom.
//
// Interface and Styles are the same table seen from two distances: Interface
// offers a decision ("accent colour", "density") and writes several entries from
// it, Styles offers the entries themselves. Advanced is what neither claimed,
// generated from the tables so a size added later is reachable the day it is
// declared rather than the day somebody remembers to list it.

import (
	"image/color"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"github.com/oklog/ulid/v2"

	"RGOClient/assets"
	"RGOClient/internal/cache"
	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

/* Account */

// presenceOptions are the ways to appear, in the order they are offered.
// "Offline" is Revolt's *invisible*: there is no way to be connected and offline,
// and appearing offline is what choosing it means — see client.fromPresence.
var presenceOptions = []settingsOption{
	{Label: domain.PresenceOnline.Label(), Value: "online"},
	{Label: domain.PresenceIdle.Label(), Value: "idle"},
	{Label: domain.PresenceFocus.Label(), Value: "focus"},
	{Label: domain.PresenceBusy.Label(), Value: "busy"},
	{Label: "Invisible", Value: "invisible"},
}

// presenceValues maps an option back to what it sets. Offline is the domain's
// name for the state, Invisible the user's name for choosing it — the one place
// the two vocabularies have to be bridged.
var presenceValues = map[string]domain.Presence{
	"online":    domain.PresenceOnline,
	"idle":      domain.PresenceIdle,
	"focus":     domain.PresenceFocus,
	"busy":      domain.PresenceBusy,
	"invisible": domain.PresenceOffline,
}

// presenceValue is the option matching a presence, for showing the current one.
func presenceValue(presence domain.Presence) string {
	for value, candidate := range presenceValues {
		if candidate == presence {
			return value
		}
	}

	return "online"
}

// accountRows are the Account section's late arrivals: the description a profile
// fetch fills in, and the button with nothing to take off until that fetch says
// there is a banner. Cleared on every section change, so an answer landing after
// the reader has moved on has nothing left to fill.
type accountRows struct {
	bio          *commitEntry
	removeBanner *Button
}

func (p *SettingsPage) accountSection() []settingsGroup {
	groups := []settingsGroup{
		p.group("Signed in as", "",
			p.identityRow(), p.usernameRow(), p.displayNameRow(), p.presenceRow(), p.statusRow()),
		p.profileGroup(),
	}

	var cards []fyne.CanvasObject
	for _, session := range p.hooks.Sessions() {
		forget := newRowButton("Forget", ToneWarning, func() {
			p.hooks.ForgetSession(session.UserID)
			p.reload()
		})

		control := HBoxNoSpacing(
			container.NewCenter(p.swatchlessAvatar(session.AvatarURL)),
			HorizontalSpacer(theme.Sizes.SettingsPreviewGap),
			container.NewCenter(forget),
		)
		cards = append(cards, p.row(session.Username, "Saved login", control))
	}
	if len(cards) > 0 {
		groups = append(groups, p.group("Saved logins",
			"Accounts you can sign back into on this computer without typing your password.",
			cards...))
	}

	groups = append(groups, p.group("Session", "",
		p.actionRow("Log out", "Signs out and returns to the login screen.",
			"Log out", ToneDanger, func() {
				p.hooks.Confirm(Confirm{
					Title:     "Log out",
					Body:      "You will be returned to the login screen. Saved logins are kept.",
					Action:    "Log out",
					Tone:      ToneDanger,
					OnConfirm: p.hooks.LogOut,
				})
			}),
		p.actionRow("Log out everywhere",
			"Signs out every device this account is signed in on, including this one, and removes its saved login here.",
			"Log out everywhere", ToneDanger, func() {
				p.hooks.Confirm(Confirm{
					Title:     "Log out everywhere",
					Body:      "Every device signed in as this account will be signed out, and this computer's saved login for it removed.",
					Action:    "Log out everywhere",
					Tone:      ToneDanger,
					OnConfirm: p.hooks.LogOutEverywhere,
				})
			}),
	))

	return groups
}

// identityRow names the logged-in account. Logged out — the page can be opened
// before a login lands — it says so rather than drawing an empty card.
func (p *SettingsPage) identityRow() fyne.CanvasObject {
	self, ok := p.hooks.Deps.Store.Self()
	if !ok {
		return p.readOnlyRow("Account", "Not signed in")
	}

	return p.row(self.Name, self.Handle, p.swatchlessAvatar(self.AvatarURL))
}

// displayNameRow is the name shown wherever the account is named. The field holds
// DisplayName rather than Name: Name has already fallen back to the username, and
// a field pre-filled with that would send the username back as a chosen name on
// the first blur after a keystroke. Nothing is written back — the change returns
// through the gateway.
func (p *SettingsPage) displayNameRow() fyne.CanvasObject {
	self, ok := p.hooks.Deps.Store.Self()
	if !ok {
		return nil
	}

	entry := newCommitEntry(self.DisplayName, p.hooks.SetDisplayName)
	entry.PlaceHolder = self.Username

	return p.row("Display name", "Shown instead of your username. Clear it to remove it.", textField(entry))
}

// usernameRow leads to the card that changes the handle. A row rather than a
// field: Revolt takes the account password with the new name, and two answers at
// once is what the prompt on the modal layer is for.
func (p *SettingsPage) usernameRow() fyne.CanvasObject {
	if _, ok := p.hooks.Deps.Store.Self(); !ok {
		return nil
	}

	return p.actionRow("Username", "What tells two identical display names apart. "+
		"Changing it asks for your password.", "Change", ToneInfo, p.hooks.ChangeUsername)
}

// profileGroup is what a profile shows that the account record does not carry.
// A profile is a request of its own, so the rows are built empty and filled
// through SetProfile when the fetch lands.
func (p *SettingsPage) profileGroup() settingsGroup {
	self, ok := p.hooks.Deps.Store.Self()
	if !ok {
		return settingsGroup{}
	}

	group := p.group("Profile", "", p.pictureRow(self), p.bannerRow(), p.bioRow())
	p.hooks.LoadProfile(p.SetProfile)

	return group
}

// pictureRow is the account's own picture and the two things that can be done to
// it. Remove is drawn disabled rather than left out, so the row keeps one shape
// and no button appears under the pointer the moment a picture lands.
func (p *SettingsPage) pictureRow(self domain.User) fyne.CanvasObject {
	remove := newRowButton("Remove", ToneWarning, p.hooks.RemoveAvatar)
	enableIf(remove, self.AvatarURL != "")

	control := HBoxNoSpacing(
		container.NewCenter(p.swatchlessAvatar(self.AvatarURL)),
		HorizontalSpacer(theme.Sizes.SettingsPreviewGap),
		container.NewCenter(newRowButton("Change", ToneInfo, p.hooks.ChangeAvatar)),
		HorizontalSpacer(theme.Sizes.ChipSpacing),
		container.NewCenter(remove),
	)

	return p.row("Picture", "Shown wherever you are named.", control)
}

// bannerRow is the picture behind the profile. No preview: a banner is a wide
// strip, and one shrunk into a settings row says nothing a caption does not.
func (p *SettingsPage) bannerRow() fyne.CanvasObject {
	remove := newRowButton("Remove", ToneWarning, p.hooks.RemoveBanner)
	remove.Disable() // until the profile lands and says there is one
	p.account.removeBanner = remove

	control := HBoxNoSpacing(
		container.NewCenter(newRowButton("Change", ToneInfo, p.hooks.ChangeBanner)),
		HorizontalSpacer(theme.Sizes.ChipSpacing),
		container.NewCenter(remove),
	)

	return p.row("Banner", "The picture behind your profile.", control)
}

// bioRow is the description under the name on a profile, stacked under its
// explanation rather than in the control slot: prose typed into 190 pixels is
// read one word at a time.
func (p *SettingsPage) bioRow() fyne.CanvasObject {
	entry := newCommitArea("", p.hooks.SetBio)
	entry.PlaceHolder = "Say something about yourself"
	p.account.bio = entry

	return p.stackedRow("About", "Shown on your profile. Clear it to remove it.", wideField(entry))
}

// presenceRow sets how the account appears to everybody else. Nothing is written
// back: the change returns through the gateway like anybody else's, so the page
// shows what the store last said — a presence the server refused must not be left
// on screen as though it held.
func (p *SettingsPage) presenceRow() fyne.CanvasObject {
	self, ok := p.hooks.Deps.Store.Self()
	if !ok {
		return nil
	}

	control := newOptionControl(presenceValue(self.Presence), presenceOptions, func(picked string) {
		if presence, known := presenceValues[picked]; known {
			p.hooks.SetPresence(presence)
		}
	})

	return p.row("Presence", "How you appear to everyone else.", control)
}

// statusRow is the line beside the account's name, written back no more than
// presenceRow is and for the same reason.
func (p *SettingsPage) statusRow() fyne.CanvasObject {
	self, ok := p.hooks.Deps.Store.Self()
	if !ok {
		return nil
	}

	entry := newCommitEntry(self.StatusText, p.hooks.SetStatusText)
	entry.PlaceHolder = "Say something"

	return p.row("Status", "The line beside your name. Clear it to remove it.", textField(entry))
}

// swatchlessAvatar is the round face a row shows on its trailing edge. Nothing is
// asked for while the page is being indexed: an avatar is a picture to fetch, and
// the index pass wants the row's name and nothing else.
func (p *SettingsPage) swatchlessAvatar(url string) fyne.CanvasObject {
	if p.indexing {
		return HorizontalSpacer(0)
	}

	side := theme.Sizes.SessionCardAvatarSize

	return circularAvatar(p.hooks.Deps.Images, url, fyne.NewSize(side, side))
}

/* Security */

// securitySection is how the account is reached rather than what is in it: the
// address it signs in with, the password, the second factor, and every device
// holding a login.
//
// All of it is one fetch (see SecurityState) and every action on it needs a
// proof of identity Revolt mints separately — so the section draws what it is
// told and the *controller* owns both the challenge card and the request behind
// it. Nothing here writes to config; nothing here is a setting.
func (p *SettingsPage) securitySection() []settingsGroup {
	p.loadSecurity()

	state := p.security.value

	sessions := p.groupOf("Your devices",
		"Every device holding a login for this account. Revoking one signs it out at once.",
		VBoxNoSpacing(p.spaceRows(p.loginRows())...))

	return []settingsGroup{
		p.group("Sign-in", "",
			p.emailRow(state),
			p.actionRow("Password", "Changing it does not sign your other devices out.",
				"Change", ToneWarning, p.hooks.ChangePassword),
		),
		p.group("Second factor",
			"A code from an app, asked for on top of your password when you sign in.",
			p.totpRow(state), p.recoveryRow(state),
		),
		sessions,
	}
}

// emailRow states the address rather than taking one: it is changed through a
// card, Revolt asking for the password with it, and it is not a field that can be
// left half-typed on a page that stays open.
func (p *SettingsPage) emailRow(state SecurityState) fyne.CanvasObject {
	email := state.Email
	switch {
	case p.security.err != nil:
		email = "Could not be read"
	case p.security.pending():
		email = "…"
	case email == "":
		email = "Not set"
	}

	button := newRowButton("Change", ToneWarning, p.hooks.ChangeEmail)
	enableIf(button, p.security.err == nil && !p.security.pending())

	return p.rowWith("Email",
		newText(email, theme.Colors.TimestampText, theme.Sizes.SettingsLabelSize),
		vcenter(button))
}

// totpRow offers the one second factor this client can set up. An authenticator
// is a shared secret and a code proving it was stored, so enabling is a card with
// two steps in it — see the controller.
func (p *SettingsPage) totpRow(state SecurityState) fyne.CanvasObject {
	if p.security.pending() || p.security.err != nil {
		return p.readOnlyRow("Authenticator app", securityUnknown(p.security.err))
	}

	if state.MFA.TOTP {
		return p.actionRow("Authenticator app", "On. Codes from your app are asked for at sign-in.",
			"Turn off", ToneDanger, p.hooks.DisableTOTP)
	}

	return p.actionRow("Authenticator app",
		"Off. Your password is the only thing between somebody and this account.",
		"Set up", ToneInfo, p.hooks.EnableTOTP)
}

// recoveryRow is the way back in when the authenticator is gone. Two actions on
// one row: seeing the codes that exist, and replacing them — which invalidates
// every one already written down, hence the second button rather than one that
// decides for itself.
func (p *SettingsPage) recoveryRow(state SecurityState) fyne.CanvasObject {
	if p.security.pending() || p.security.err != nil {
		return p.readOnlyRow("Recovery codes", securityUnknown(p.security.err))
	}

	detail := "None yet. Generate a set and keep them somewhere other than this computer."
	if state.MFA.Recovery {
		detail = "Generated. Each one signs you in once if you lose your authenticator."
	}

	show := newRowButton("Show", ToneInfo, p.hooks.RecoveryCodes)
	enableIf(show, state.MFA.Recovery)

	renew := newRowButton("Generate new", ToneWarning, p.hooks.RenewRecovery)

	return p.row("Recovery codes", detail,
		HBoxNoSpacing(vcenter(show), HorizontalSpacer(theme.Sizes.SettingsPreviewGap), vcenter(renew)))
}

// loginRows is the held answer drawn, or the one line standing for whichever
// state it is not in — the shape a server's fetched lists take, and for the same
// reasons.
func (p *SettingsPage) loginRows() []fyne.CanvasObject {
	switch {
	case p.security.pending():
		return []fyne.CanvasObject{p.note("Fetching…")}
	case p.security.err != nil:
		return []fyne.CanvasObject{p.note("Could not read where this account is signed in.")}
	case len(p.security.value.Logins) == 0:
		return []fyne.CanvasObject{p.note("Revolt is holding no logins for this account.")}
	}

	state := p.security.value

	rows := make([]fyne.CanvasObject, 0, len(state.Logins)+2)
	for _, login := range state.Logins {
		rows = append(rows, p.loginRow(login))
	}

	// Said once under the list rather than on every row: it is a fact about this
	// client, not about any one login.
	if !state.SelfKnown {
		rows = append(rows, p.note(
			"This client can't tell which of these is the one you're using — sign in again and it will."))
	}

	if len(state.Logins) > 1 {
		rows = append(rows, p.actionRow("Sign out everywhere else",
			"Signs out every other device and leaves this one signed in.",
			"Sign out others", ToneDanger, p.hooks.RevokeOthers))
	}

	return rows
}

// loginRow is one device. The name is the whole of what tells two apart — Revolt
// stores whatever the client that signed in called itself — so it leads the row
// and is editable, and the current one is marked because revoking it is the one
// entry here that signs *this* window out.
func (p *SettingsPage) loginRow(login domain.AccountSession) fyne.CanvasObject {
	detail := "Signed in"
	if login.Current {
		detail = "This device"
	}

	rename := NewOutlinedIconButton(
		tintedIcon(assets.ActionEditIcon, theme.Colors.SwiftActionIcon), theme.Colors.SwiftActionIcon,
		func() { p.hooks.RenameLogin(login.ID, login.Name) })

	revokeTint := theme.Colors.SwiftActionDanger
	revoke := NewOutlinedIconButton(tintedIcon(assets.ActionDeleteIcon, revokeTint), revokeTint,
		func() { p.hooks.RevokeLogin(login.ID, login.Name) })

	return p.entryRow(nil, login.Name, detail, rename, HorizontalSpacer(theme.Sizes.IconButtonGap), revoke)
}

// securityUnknown is what a row says while the section has no answer, or could
// not get one. A row that simply went missing would read as a feature this client
// does not have.
func securityUnknown(err error) string {
	if err != nil {
		return "Could not be read"
	}

	return "…"
}

// loadSecurity asks for the section's one answer, unless a request is already out
// or the held one is still fresh. Recorded whatever the reader has done
// meanwhile, and drawn into whichever list is mounted *now*.
func (p *SettingsPage) loadSecurity() {
	// The index pass builds every section twice on the first keystroke in the
	// search box, and this one is three requests: it goes in the list with
	// LoadProfile and the microphone.
	if p.indexing || p.hooks.LoadSecurity == nil || !p.security.claim() {
		return
	}

	visit := p.visit

	p.hooks.LoadSecurity(func(state SecurityState, err error) {
		if p.visit != visit {
			return // the page closed; there is nothing left to record this in
		}
		p.security.settle(state, err)

		// The rows above the list are drawn from the same answer, so the whole section
		// is rebuilt rather than only the list refilled — unlike a server's, where the
		// list is the whole of what the answer decides.
		if p.section == SectionSecurity && !p.searching {
			p.reload()
		}
	})
}

/* Interface */

func (p *SettingsPage) interfaceSection() []settingsGroup {
	settings := config.Current().Interface

	accent := settings.Accent
	if accent == "" {
		accent = theme.Hex(theme.Colors.ServerSelectedBg)
	}

	return []settingsGroup{
		p.group("Appearance", "",
			p.accentRow(accent),
			p.optionRow("Density", "How tightly messages are spaced.",
				settings.Density, densityOptions, func(s *config.Settings, value string) {
					s.Interface.Density = value
					applyDensity(s, value)
				}),
			p.fontSizeRow(settings.FontSize),
			p.styleToggleRow("Match the window title bar to the theme", "",
				settings.ThemeTitleBar, func(s *config.Settings, on bool) { s.Interface.ThemeTitleBar = on }),
		),
		p.group("Messages", "",
			p.styleToggleRow("Group consecutive messages",
				"Hides the name and avatar when the same person writes again.",
				settings.GroupMessages, func(s *config.Settings, on bool) { s.Interface.GroupMessages = on }),
			p.styleToggleRow("Show the member sidebar by default", "",
				settings.ShowMemberSidebar, func(s *config.Settings, on bool) { s.Interface.ShowMemberSidebar = on }),
		),
		p.group("Time", "",
			p.optionRow("Clock", "", settings.TimeFormat, clockOptions,
				func(s *config.Settings, value string) { s.Interface.TimeFormat = value }),
			p.styleToggleRow("Show seconds", "", settings.ShowSeconds,
				func(s *config.Settings, on bool) { s.Interface.ShowSeconds = on }),
		),
		p.preview(func() fyne.CanvasObject { return p.messagePreview() }),
	}
}

// accentRow is one colour that writes several. The entries it drives stay
// ordinary overrides, so the Advanced section can still pull any one of them
// away from the accent afterwards.
func (p *SettingsPage) accentRow(accent string) fyne.CanvasObject {
	control := p.colorControl(accent, nil, func(hex string) {
		p.restyle(func(s *config.Settings) {
			s.Interface.Accent = hex
			for field, value := range theme.AccentOverrides(hex) {
				setColorOverride(s, field, value)
			}
		})
	})

	return p.row("Accent colour", "Used for selection, focus outlines, mentions and links.", control)
}

// fontSizeRow drives Fyne's own text size, which is what the built-in widgets —
// buttons, menus, entries — draw at. The client's own text is sized by named
// entries in the table, under Styles.
func (p *SettingsPage) fontSizeRow(size float32) fyne.CanvasObject {
	control := newWideNumberControl(float64(size), minFontSize, maxFontSize, 1, "pt", func(v float64) {
		p.restyle(func(s *config.Settings) { s.Interface.FontSize = float32(v) })
	})

	return p.stackedRow("Interface font size", "The size of text in buttons, menus and text fields.", control)
}

/* Styles */

func (p *SettingsPage) stylesSection() []settingsGroup {
	groups := make([]settingsGroup, 0, len(styleGroups)+2)

	for _, group := range styleGroups {
		// Gated whole rather than row by row: every group ends with its own reset
		// button, so dropping only the sizes would leave a card holding nothing but
		// a way to undo them.
		if group.advanced && !p.advanced {
			continue
		}

		rows := make([]fyne.CanvasObject, 0, len(group.fields))
		for _, field := range group.fields {
			if row := p.sizeRow(field.label, field.name); row != nil {
				rows = append(rows, row)
			}
		}
		rows = append(rows, p.actionRow("", "", "Reset this group", ToneInfo,
			func() { p.resetFields(group.fields) }))

		groups = append(groups, p.group(group.caption, group.detail, rows...))
		if group.preview != nil {
			groups = append(groups, p.preview(func() fyne.CanvasObject { return group.preview(p) }))
		}
	}

	groups = append(groups, p.group("Everything", "",
		p.actionRow("Reset all styles", "Returns every size and colour to the client's own.",
			"Reset", ToneWarning, func() {
				p.hooks.Confirm(Confirm{
					Title:  "Reset all styles",
					Body:   "Every size and colour returns to the client's defaults. Nothing else changes.",
					Action: "Reset",
					Tone:   ToneWarning,
					OnConfirm: func() {
						p.restyle(func(s *config.Settings) {
							s.Styles = config.Styles{}
							s.Interface.Accent = ""
							s.Interface.Density = config.DensityCosy
						})
						p.reload()
					},
				})
			}),
	))

	return groups
}

// resetFields drops one group's overrides, leaving the rest alone.
func (p *SettingsPage) resetFields(fields []styleField) {
	p.restyle(func(s *config.Settings) {
		for _, field := range fields {
			delete(s.Styles.Sizes, field.name)
		}
	})
	p.reload()
}

/* Behaviour */

func (p *SettingsPage) behaviourSection() []settingsGroup {
	settings := config.Current().Behaviour

	return []settingsGroup{
		p.group("Members", "What the member sidebar shows and how it is kept up to date.",
			p.toggleRow("Load every member",
				"Shows the whole server. Off, only people who have posted appear.",
				settings.FetchAllMembers, func(s *config.Settings, on bool) { s.Behaviour.FetchAllMembers = on }),
			p.adv(p.toggleRow("Sort members by name",
				"Orders each section alphabetically.",
				settings.SortMembers, func(s *config.Settings, on bool) { s.Behaviour.SortMembers = on })),
			p.toggleRow("Separate online and offline", "",
				settings.GroupByPresence, func(s *config.Settings, on bool) { s.Behaviour.GroupByPresence = on }),
			p.toggleRow("Give roles their own section",
				"Lists roles such as Moderator above everyone else. Needs the section above.",
				settings.HoistRoles, func(s *config.Settings, on bool) { s.Behaviour.HoistRoles = on }),
			p.toggleRow("Show yourself at the top",
				"Lists you first, under a You heading.",
				settings.ShowSelfFirst, func(s *config.Settings, on bool) { s.Behaviour.ShowSelfFirst = on }),
			p.toggleRow("Hide offline members",
				"Leaves offline members out of the list.",
				settings.HideOfflineMembers, func(s *config.Settings, on bool) { s.Behaviour.HideOfflineMembers = on }),
			p.toggleRow("Hide members without roles",
				"Shows only people the server has given a role.",
				settings.HideRolelessMembers, func(s *config.Settings, on bool) { s.Behaviour.HideRolelessMembers = on }),
			p.toggleRow("Show everyone when the list would be empty",
				"If the two settings above leave nothing to show, the whole server is listed instead.",
				settings.MemberListFallback, func(s *config.Settings, on bool) { s.Behaviour.MemberListFallback = on }),
			p.adv(p.toggleRow("Update the list as people come and go",
				"Off, the list only changes when you reopen the server.",
				settings.LiveMemberPresence, func(s *config.Settings, on bool) { s.Behaviour.LiveMemberPresence = on })),
			p.adv(p.numberRow("Extra rows drawn",
				"How far past the visible area to draw. Higher is smoother to scroll and uses more memory.",
				settings.MemberOverscan, 0, maxMemberOverscan, "",
				func(s *config.Settings, v int) { s.Behaviour.MemberOverscan = v })),
			p.note("The sidebar can be hidden from the channel header."),
		),
		p.group("Typing", "",
			p.toggleRow("Let others see when I am typing", "",
				settings.SendTyping, func(s *config.Settings, on bool) { s.Behaviour.SendTyping = on }),
			p.numberRow("Names shown when people are typing",
				"Zero turns the indicator off. Anyone past this many is counted instead.",
				settings.TypingNames, 0, maxTypingNames, "",
				func(s *config.Settings, v int) { s.Behaviour.TypingNames = v }),
			p.toggleRow("Show myself typing",
				"Adds you to the line while you are composing, as everyone else sees it.",
				settings.TypingShowSelf, func(s *config.Settings, on bool) { s.Behaviour.TypingShowSelf = on }),
			p.toggleRow("Mark typing in the channel list",
				"Marks any channel someone is typing in, other than the one you are reading.",
				settings.TypingInChannels, func(s *config.Settings, on bool) { s.Behaviour.TypingInChannels = on }),
			p.toggleRow("Show pictures beside who is typing",
				"Draws each person's avatar before their name.",
				settings.TypingAvatars, func(s *config.Settings, on bool) { s.Behaviour.TypingAvatars = on }),
			p.toggleRow("Animate the typing marks",
				"Off, the line rests still instead of sweeping.",
				settings.TypingAnimation, func(s *config.Settings, on bool) { s.Behaviour.TypingAnimation = on }),
		),
		p.group("Messages", "How much of a conversation is kept on screen at once.",
			p.adv(p.numberRow("Grouping window",
				"The longest gap between two messages that still appear under one name.",
				settings.GroupWindowSeconds, 0, maxGroupWindow, "s",
				func(s *config.Settings, v int) { s.Behaviour.GroupWindowSeconds = v })),
			p.adv(p.numberRow("Messages held on open",
				"How far back a channel reaches when opened. Only what is on screen is drawn; older messages fill in as you scroll.",
				settings.InitialMountCount, 5, maxMountedCap, "",
				func(s *config.Settings, v int) { s.Behaviour.InitialMountCount = v })),
			p.adv(p.numberRow("Maximum messages held",
				"The limit while scrolling back. Only what is on screen is drawn whatever this is.",
				settings.MountedCap, 20, maxMountedCap, "",
				func(s *config.Settings, v int) { s.Behaviour.MountedCap = max(v, s.Behaviour.InitialMountCount) })),
			p.adv(p.numberRow("Messages loaded per scroll",
				"How many older messages to fetch each time you reach the top.",
				settings.HistoryPageSize, 5, maxHistoryPage, "",
				func(s *config.Settings, v int) { s.Behaviour.HistoryPageSize = v })),
			p.adv(p.numberRow("Extra messages drawn",
				"How far past the visible area to draw. Higher is smoother to scroll and uses more memory.",
				settings.MessageOverscan, 0, maxMessageOverscan, "",
				func(s *config.Settings, v int) { s.Behaviour.MessageOverscan = v })),
		),
		p.group("Timing", "",
			p.adv(p.numberRow("Author lookup delay",
				"How long to wait for more unknown authors before looking them up together.",
				settings.AuthorFetchDelayMS, 0, maxDelayMS, "ms",
				func(s *config.Settings, v int) { s.Behaviour.AuthorFetchDelayMS = v })),
			p.adv(p.numberRow("Read receipt delay",
				"How long to wait before telling the server you have read a channel.",
				settings.AckDelayMS, 0, maxDelayMS, "ms",
				func(s *config.Settings, v int) { s.Behaviour.AckDelayMS = v })),
			p.adv(p.numberRow("Settling time",
				"How long to wait after a burst of changes before redrawing the sidebars.",
				settings.RefreshDelayMS, 0, maxRefreshDelay, "ms",
				func(s *config.Settings, v int) { s.Behaviour.RefreshDelayMS = v })),
		),
		p.group("Input", "",
			p.numberRow("Scroll speed", "How far the wheel moves the conversation.",
				settings.ScrollSpeed, 1, maxScrollSpeed, "×",
				func(s *config.Settings, v int) { s.Behaviour.ScrollSpeed = v }),
			p.toggleRow("Enter sends the message",
				"Off, Enter starts a new line and Ctrl+Enter sends.",
				settings.EnterSends, func(s *config.Settings, on bool) { s.Behaviour.EnterSends = on }),
		),
	}
}

/* Notifications */

func (p *SettingsPage) notificationsSection() []settingsGroup {
	settings := config.Current().Notifications

	groups := []settingsGroup{
		p.group("Notices", "The cards that appear in the top-right corner.",
			p.numberRow("Dismiss after", "How long a notice stays before it fades.",
				settings.LifetimeSeconds, 1, maxNoticeLifetime, "s",
				func(s *config.Settings, v int) { s.Notifications.LifetimeSeconds = v }),
			p.adv(p.numberRow("Maximum on screen", "How many notices can stack up at once.",
				settings.MaxStacked, 1, maxNoticeStack, "",
				func(s *config.Settings, v int) { s.Notifications.MaxStacked = v })),
		),
		p.group("Show", "",
			p.toggleRow("Information", "Something happened and nothing is wrong.",
				settings.ShowInfo, func(s *config.Settings, on bool) { s.Notifications.ShowInfo = on }),
			p.toggleRow("Warnings", "Something was interrupted but nothing was lost.",
				settings.ShowWarning, func(s *config.Settings, on bool) { s.Notifications.ShowWarning = on }),
			p.toggleRow("Failures", "Something did not work.",
				settings.ShowDanger, func(s *config.Settings, on bool) { s.Notifications.ShowDanger = on }),
		),
	}

	// The alert group is dropped entirely where the platform has no way to signal a
	// window that is not in front, rather than drawn as switches that do nothing.
	if alertSupported {
		groups = append(groups, p.group("When you're away",
			"The taskbar button flashes until you come back to the window.",
			p.toggleRow("Flash the taskbar", "",
				settings.FlashTaskbar,
				func(s *config.Settings, on bool) { s.Notifications.FlashTaskbar = on }),
			p.toggleRow("For a mention", "Somebody named you, or addressed everyone.",
				settings.AlertOnMention,
				func(s *config.Settings, on bool) { s.Notifications.AlertOnMention = on }),
			p.toggleRow("For a direct message", "Anything sent to you or to a group you're in.",
				settings.AlertOnDirect,
				func(s *config.Settings, on bool) { s.Notifications.AlertOnDirect = on }),
		))
	}

	return append(groups,
		p.group("Sound", "",
			p.toggleRow("Play sounds", "With this off the client never opens an audio device.",
				settings.Sounds, func(s *config.Settings, on bool) { s.Notifications.Sounds = on }),
			p.numberRow("Volume", "", settings.SoundVolume, 0, 100, "%",
				func(s *config.Settings, v int) { s.Notifications.SoundVolume = v }),
			p.toggleRow("Play while the window is in focus",
				"Off makes an incoming message silent until you look away.",
				settings.SoundsWhenFocused,
				func(s *config.Settings, on bool) { s.Notifications.SoundsWhenFocused = on }),
		),
		p.group("Play a sound for", "",
			p.toggleRow("A mention", "Somebody named you, or addressed everyone.",
				settings.PlayMention, func(s *config.Settings, on bool) { s.Notifications.PlayMention = on }),
			p.toggleRow("A direct message", "A message in a conversation you aren't reading.",
				settings.PlayDirect, func(s *config.Settings, on bool) { s.Notifications.PlayDirect = on }),
			p.toggleRow("Any other message", "A message in any channel you aren't reading.",
				settings.PlayMessage, func(s *config.Settings, on bool) { s.Notifications.PlayMessage = on }),
			p.toggleRow("A message here", "A message in the channel that's open.",
				settings.PlayAmbient, func(s *config.Settings, on bool) { s.Notifications.PlayAmbient = on }),
			p.toggleRow("Sending", "A message of yours going out.",
				settings.PlaySend, func(s *config.Settings, on bool) { s.Notifications.PlaySend = on }),
			p.toggleRow("A friend request", "Somebody asked to be friends, or accepted.",
				settings.PlayFriend, func(s *config.Settings, on bool) { s.Notifications.PlayFriend = on }),
			p.toggleRow("A reaction", "Somebody reacted to a message of yours.",
				settings.PlayReaction, func(s *config.Settings, on bool) { s.Notifications.PlayReaction = on }),
			p.toggleRow("Something failing", "An action of yours was refused.",
				settings.PlayError, func(s *config.Settings, on bool) { s.Notifications.PlayError = on }),
			p.toggleRow("The connection", "The session dropping, and a new one coming back.",
				settings.PlayConnection,
				func(s *config.Settings, on bool) { s.Notifications.PlayConnection = on }),
		),
		p.group("Typing", "A click under each keystroke in the composer.",
			p.toggleRow("Typing sounds", "",
				settings.TypingSounds,
				func(s *config.Settings, on bool) { s.Notifications.TypingSounds = on }),
			p.numberRow("Typing volume",
				"Separate from the volume above: these play far more often than anything else.",
				settings.TypingVolume, 0, 100, "%",
				func(s *config.Settings, v int) { s.Notifications.TypingVolume = v }),
		),
		p.soundFileGroup("Sounds",
			"Point one at a WAV or MP3 file of your own, or keep the built-in.", false),
		p.soundFileGroup("Typing sounds", "", true),
	)
}

// soundFileGroup lists the sounds of one kind and what can be done about each.
// Two groups rather than one list: the four keystrokes are a different question
// from the ten events, and a caption is cheaper than a divider nobody reads. The
// second carries no sentence — it sits directly under the first's, which is
// about both.
func (p *SettingsPage) soundFileGroup(caption, detail string, typing bool) settingsGroup {
	var rows []fyne.CanvasObject

	for _, sound := range p.hooks.Sounds() {
		if sound.Typing != typing {
			continue
		}
		rows = append(rows, p.soundRow(sound))
	}

	return p.group(caption, detail, rows...)
}

// soundRow is one sound: what it is for, what it is currently playing, and the
// three things that can be done to it. The file is the row's *explanation* — it
// needs the width, and shortening a path from the front hides the drive it is
// on — which is why the summary is the group's caption's job and not the row's.
func (p *SettingsPage) soundRow(sound SettingsSound) fyne.CanvasObject {
	detail := sound.Summary
	if sound.File != "" {
		detail = sound.File
	}

	label := newText(detail, theme.Colors.TimestampText, theme.Sizes.SettingsDetailSize)

	play := NewButton("Play", func() { p.hooks.PlaySound(sound.Key) })
	choose := NewButton("Change…", func() {
		p.hooks.ChooseSound(sound.Key, p.reload)
	})

	controls := []fyne.CanvasObject{play, HorizontalSpacer(theme.Sizes.ChipSpacing), choose}
	if sound.File != "" {
		// Offered only once there is something to go back from, so a row does not
		// advertise a built-in nobody has moved away from.
		reset := NewButton("Built-in", func() {
			p.hooks.ResetSound(sound.Key)
			p.reload()
		})
		controls = append(controls, HorizontalSpacer(theme.Sizes.ChipSpacing), reset)
	}

	return p.rowWith(sound.Title, NewEllipsisText(label), HBoxNoSpacing(controls...))
}

/* Cache */

func (p *SettingsPage) cacheSection() []settingsGroup {
	settings := config.Current().Cache

	disk := p.newUsageMeter("On disk", "Measuring…")
	memory := p.newUsageMeter("In memory", "Ready to draw without decoding again.")
	p.hooks.CacheStats(func(stats cache.ImageStats) {
		disk.set(stats.DiskBytes, settings.ImageDiskBytes(), fileCount(stats.Files))
		memory.set(stats.MemoryBytes, settings.ImageMemoryBytes(), "Ready to draw without decoding again.")
	})

	return []settingsGroup{
		p.group("Pictures", "Avatars, icons, attachments and emoji, kept between runs.",
			p.locationRow(settings.AssetDir),
			p.note("Each kind of picture is stored in its own folder inside it."),
			p.note("Changing the location takes effect after a restart."),
		),
		p.group("Usage", "",
			disk.block,
			memory.block,
			p.numberRow("Disk limit", "Once full, the pictures used longest ago are deleted first.",
				settings.ImageDiskMiB, minCacheMiB, maxDiskMiB, "MiB",
				func(s *config.Settings, v int) { s.Cache.ImageDiskMiB = v }),
			p.adv(p.numberRow("Memory limit", "How much of the cache is kept decoded and ready to draw.",
				settings.ImageMemoryMiB, minCacheMiB, maxMemoryMiB, "MiB",
				func(s *config.Settings, v int) { s.Cache.ImageMemoryMiB = v })),
			p.adv(p.numberRow("Maximum image size",
				"Larger pictures are scaled down to this before being stored.",
				settings.MaxImageEdge, minImageEdge, maxImageEdge, "px",
				func(s *config.Settings, v int) { s.Cache.MaxImageEdge = v })),
			p.adv(p.numberRow("Simultaneous downloads",
				"How many pictures to fetch at the same time.",
				settings.ImageLoaders, 1, maxImageLoaders, "",
				func(s *config.Settings, v int) { s.Cache.ImageLoaders = v })),
			p.adv(p.note("Changing the number of downloads takes effect after a restart.")),
			p.actionRow("Clear the image cache",
				"Pictures are downloaded again the next time they are shown.",
				"Clear", ToneDanger, func() {
					p.hooks.Confirm(Confirm{
						Title:  "Clear the image cache",
						Body:   "Every cached avatar, attachment and emoji is deleted. They will be downloaded again as they are drawn.",
						Action: "Clear",
						Tone:   ToneDanger,
						OnConfirm: func() {
							p.hooks.ClearCache()
							p.reload()
						},
					})
				}),
		),
		p.group("Messages", "",
			p.adv(p.numberRow("Messages kept per channel",
				"How much of a conversation is remembered, so reopening it is instant.",
				settings.MessagesPerChannel, minCachedMessages, maxCachedMessages, "",
				func(s *config.Settings, v int) { s.Cache.MessagesPerChannel = v })),
			p.adv(p.numberRow("Channels remembered",
				"How many channels keep their messages after you leave them.",
				settings.CachedChannels, 1, maxCachedChannels, "",
				func(s *config.Settings, v int) { s.Cache.CachedChannels = v })),
			p.adv(p.numberRow("Text previews kept",
				"How many message previews are held for the channel and reply lists.",
				settings.TextPreviews, 1, maxTextPreviews, "",
				func(s *config.Settings, v int) { s.Cache.TextPreviews = v })),
			p.adv(p.note("These changes take effect after a restart.")),
		),
	}
}

// locationRow is the root the picture caches live under and what can be done
// about it. The path is the row's *explanation* rather than its value: it needs
// the whole width, and shortening from the front hides the drive it is on.
func (p *SettingsPage) locationRow(configured string) fyne.CanvasObject {
	path := newText(p.hooks.CacheDir(), theme.Colors.TimestampText, theme.Sizes.SettingsDetailSize)

	choose := NewButton("Change…", func() {
		p.hooks.ChooseCacheDir(func(picked string) {
			p.change(func(s *config.Settings) { s.Cache.AssetDir = picked })
			p.reload()
		})
	})

	open := NewButton("Open", func() { p.hooks.OpenPath(p.hooks.CacheDir()) })

	controls := []fyne.CanvasObject{choose, HorizontalSpacer(theme.Sizes.ChipSpacing), open}
	if configured != "" {
		// Only offered once there is something to go back from, so the row does not
		// advertise a default nobody has moved away from.
		reset := NewButton("Default", func() {
			p.change(func(s *config.Settings) { s.Cache.AssetDir = "" })
			p.reload()
		})
		controls = append(controls, HorizontalSpacer(theme.Sizes.ChipSpacing), reset)
	}

	return p.rowWith("Location", NewEllipsisText(path), HBoxNoSpacing(controls...))
}

// usageMeter is one budget drawn as a bar, and the setter filling it once the
// measurement — a walk of the cache directory — comes back.
type usageMeter struct {
	block fyne.CanvasObject
	set   func(used, total int64, detail string)
}

// newUsageMeter builds the meter empty. Its figures arrive from a goroutine, so
// what it draws until then has to be something rather than nothing.
func (p *SettingsPage) newUsageMeter(label, placeholder string) *usageMeter {
	name := newText(label, theme.Colors.TextPrimary, theme.Sizes.SettingsLabelSize)
	amount := newText("", theme.Colors.TextPrimary, theme.Sizes.SettingsDetailSize)
	detail := newText(placeholder, theme.Colors.TimestampText, theme.Sizes.SettingsDetailSize)

	bar, fill := newUsageBar()
	gap := theme.Sizes.ChipSpacing

	body := VBoxNoSpacing(
		NewFillRow(0, vcenter(name), HorizontalSpacer(gap), vcenter(amount)),
		VerticalSpacer(gap),
		bar,
		VerticalSpacer(gap),
		detail,
	)

	return &usageMeter{
		block: p.block(body),
		set: func(used, total int64, note string) {
			amount.Text = util.FormatFileSize(int(used)) + " of " + util.FormatFileSize(int(total))
			amount.Refresh()

			detail.Text = note
			detail.Refresh()

			var ratio float32
			if total > 0 {
				ratio = float32(used) / float32(total)
			}
			fill(ratio)
		},
	}
}

// fileCount reads as a sentence rather than a bare number, sitting under a bar
// that is already showing a size.
func fileCount(files int) string {
	if files == 1 {
		return "1 file"
	}

	return strconv.Itoa(files) + " files"
}

/* Performance */

// performanceSection is what the client asks of the toolkit rather than of
// itself. Every row takes effect on the next frame, so the section is also the
// only place in the page where a change can be *seen* rather than described —
// which is why the note under the limit says what it cannot do on its own.
func (p *SettingsPage) performanceSection() []settingsGroup {
	settings := config.Current().Performance

	groups := []settingsGroup{
		p.group("Drawing", "How the client draws each frame.",
			p.numberRow("FPS",
				"The most frames per second the client will try to draw. Higher is smoother to scroll and animate while something moves.",
				settings.FrameRate, minFrameRate, maxFrameRate, "fps",
				func(s *config.Settings, v int) { s.Performance.FrameRate = v }),
			p.toggleRow("V-Sync",
				"Shows each frame in step with the monitor, so none is ever half-drawn. Off, frames appear as soon as they're ready, which can tear.",
				settings.VSync, func(s *config.Settings, on bool) { s.Performance.VSync = on }),
			p.note("With V-Sync on, FPS is capped to your monitor's refresh rate (Hz)."),
			p.toggleRow("Partial redraw",
				"Redraws only the parts of the window that changed, keeping the rest from the previous frame. Turn off only if part of the window ever looks stale.",
				settings.PartialRepaint, func(s *config.Settings, on bool) { s.Performance.PartialRepaint = on }),
		),
	}

	if rows := p.coreRows(settings); len(rows) > 0 {
		groups = append(groups, p.group("Processor cores",
			"Which of this machine's cores the client is allowed to run on. Everything it does moves with this, call audio included.",
			rows...))
	}

	return groups
}

// coreRows is the core group, and nothing at all where the machine's cores are
// all alike — the rule the taskbar-flash group follows, for the same reason.
//
// The two machines are offered different rows because their splits read nothing
// alike. A hybrid processor has efficiency cores, which are slower on purpose,
// so its sides are named for what they are. A chiplet processor's sides have no
// such names — the numbering is the machine's own and claims nothing — so the
// row says CCD0 and CCD1 and the note carries what is usually true of them.
func (p *SettingsPage) coreRows(settings config.Performance) []fyne.CanvasObject {
	if p.hooks.CPUCores == nil {
		return nil
	}

	cores := p.hooks.CPUCores()
	if !cores.Split() {
		return nil
	}

	if cores.CCD0 > 0 {
		detail := "This processor has two chiplets: CCD0 with " + strconv.Itoa(cores.CCD0) +
			" cores and CCD1 with " + strconv.Itoa(cores.CCD1) +
			". Staying on one keeps the client's work from crossing between them."

		return []fyne.CanvasObject{
			p.optionRow("Run on", detail, coresValue(settings.Cores, cores), []settingsOption{
				{Label: "Both CCDs", Value: config.CoresAll},
				{Label: "CCD0", Value: config.CoresCCD0},
				{Label: "CCD1", Value: config.CoresCCD1},
			}, func(s *config.Settings, v string) { s.Performance.Cores = v }),

			p.note("CCD1 is the default: games and heavier apps usually land on CCD0, which carries the stacked cache or the better-binned cores. Fewer cores can mean stutter while scrolling, or a dropout in a call."),
		}
	}

	detail := "This processor has " + strconv.Itoa(cores.Performance) + " performance cores and " +
		strconv.Itoa(cores.Efficiency) + " efficiency cores, which are slower and use less power."

	return []fyne.CanvasObject{
		p.optionRow("Run on", detail, coresValue(settings.Cores, cores), []settingsOption{
			{Label: "All cores", Value: config.CoresAll},
			{Label: "Efficiency cores", Value: config.CoresEfficiency},
			{Label: "Performance cores", Value: config.CoresPerformance},
		}, func(s *config.Settings, v string) { s.Performance.Cores = v }),

		p.note("Efficiency cores is the default: the client is idle most of the time, and they cost less power. Fewer cores can mean stutter while scrolling, or a dropout in a call."),
	}
}

// coresValue is the stored setting as one of the values the row offers on this
// machine. The controller resolves and writes the setting before this page can
// open, so the fallbacks only catch a file edited by hand since — and they
// answer what the controller would: the machine's own default.
func coresValue(cores string, machine CPUCores) string {

	if machine.CCD0 > 0 {
		switch cores {
		case config.CoresAll, config.CoresCCD0, config.CoresCCD1:
			return cores
		}

		return config.CoresCCD1
	}

	switch cores {
	case config.CoresAll, config.CoresEfficiency, config.CoresPerformance:
		return cores
	}

	return config.CoresEfficiency
}

/* Advanced */

// advancedSection lists what the curated groups did not claim. Long by
// construction — the point is that nothing is unreachable — and narrowed by the
// rail's search box, which is why it carries no field of its own: a size or a
// colour typed there is answered in the results, and what lands here is whatever
// was still being looked for on the way in.
func (p *SettingsPage) advancedSection() []settingsGroup {
	sizeFields, colorFields := uncuratedSizeFields(), theme.ColorFields()

	if p.indexing {
		return []settingsGroup{
			p.recordFields("Every other size", sizeFields, false),
			p.recordFields("Palette", colorFields, true),
		}
	}

	query := strings.ToLower(p.query)

	// Matched before it is built. A row here is a slider, a field and a swatch, and
	// there are hundreds — building every one only to throw most away is the work
	// the query exists to avoid.
	var sizeRows, colorRows []fyne.CanvasObject
	for _, field := range sizeFields {
		if !matchesField(field, query) {
			continue
		}
		if row := p.sizeRow(field, field); row != nil {
			sizeRows = append(sizeRows, row)
		}
	}
	for _, field := range colorFields {
		if !matchesField(field, query) {
			continue
		}
		if row := p.colorRow(field, field); row != nil {
			colorRows = append(colorRows, row)
		}
	}

	if len(sizeRows) == 0 && len(colorRows) == 0 {
		return []settingsGroup{p.group("", "",
			p.note("No size or colour is named that. Empty the search box for the whole list."))}
	}

	return []settingsGroup{
		p.group("Every other size", narrowed(
			"Named exactly as the client names them. The Styles section covers the rest.",
			query), sizeRows...),
		p.group("Palette", narrowed("Every colour the client draws with.", query), colorRows...),
	}
}

// narrowed says a list is showing part of itself. The search box is a column away
// from the card and holds what was typed several sections ago, so a list two rows
// long has to account for itself.
func narrowed(detail, query string) string {
	if query == "" {
		return detail
	}

	return detail + " Showing only what the search box matches."
}

// matchesField is the filter: a substring of the field's own name, which is what
// the rows are labelled with here. query arrives already folded.
func matchesField(field, query string) bool {
	return query == "" || strings.Contains(strings.ToLower(field), query)
}

/* About */

func (p *SettingsPage) aboutSection() []settingsGroup {
	return []settingsGroup{
		p.group("This build", "",
			p.readOnlyRow("Version", p.hooks.Version),
			p.readOnlyRow("Build", p.hooks.Build),
		),
		p.group("Settings file", "",
			p.readOnlyRow("Location", p.hooks.ConfigPath()),
			p.actionRow("Open the settings file",
				"Everything on these pages, as plain JSON.",
				"Open", ToneInfo, func() { p.hooks.OpenPath(p.hooks.ConfigPath()) }),
		),
		p.group("Start over", "",
			p.actionRow("Reset every setting",
				"Returns everything on these pages to its default.",
				"Reset everything", ToneDanger, func() {
					p.hooks.Confirm(Confirm{
						Title:  "Reset every setting",
						Body:   "Every setting returns to its default. Saved logins and cached images are untouched.",
						Action: "Reset everything",
						Tone:   ToneDanger,
						OnConfirm: func() {
							p.restyle(func(s *config.Settings) { *s = config.Default() })
							p.reload()
						},
					})
				}),
		),
	}
}

/* The curated style groups */

// styleField is one entry of the size table under the name a person would look
// for it by.
type styleField struct {
	name  string
	label string
}

// styleGroup is a card of related sizes, and optionally a sample of what they
// shape. A group is advanced when what it shapes is detail somebody has to go
// looking for — the hairlines, the scroll bar — rather than the proportions of
// the window they are already looking at.
type styleGroup struct {
	caption string
	detail  string
	fields  []styleField
	preview func(p *SettingsPage) fyne.CanvasObject

	advanced bool
}

var styleGroups = []styleGroup{
	{
		caption: "Message rhythm",
		detail:  "The vertical spacing of the conversation.",
		fields: []styleField{
			{"MessageVerticalPadding", "Space around a message"},
			{"MessageGroupedVerticalPadding", "Space within a group"},
			{"MessageHorizontalPadding", "Left and right margin"},
			{"MessageAvatarSize", "Avatar"},
			{"MessageAvatarColumnWidth", "Avatar column"},
			{"MessageContentPadding", "Gap under the name"},
			{"MessageTimestampSize", "Timestamp text"},
			{"MessageAttachmentSpacing", "Between attachments"},
			{"SwiftActionSize", "Hover action buttons"},
		},
		preview: func(p *SettingsPage) fyne.CanvasObject { return p.messagePreview() },
	},
	{
		caption: "Replies, days and events",
		fields: []styleField{
			{"MessageReplyBlockGap", "Above a reply"},
			{"MessageReplyLineInset", "Reply elbow inset"},
			{"MessageReplyLineThickness", "Reply elbow thickness"},
			{"MessageReplyLineGap", "Reply elbow gap"},
			{"DaySeparatorTextSize", "Day label text"},
			{"DaySeparatorThickness", "Day line"},
			{"DaySeparatorTopPadding", "Above a day line"},
			{"DaySeparatorBottomPadding", "Below a day line"},
			{"DaySeparatorGap", "Beside the day label"},
			{"SystemMessageTextSize", "System event text"},
			{"SystemMessageIconSize", "System event mark"},
			{"SystemMessagePadding", "Around a system event"},
		},
		advanced: true,
	},
	{
		caption: "Sidebars",
		detail:  "The three columns beside the conversation.",
		fields: []styleField{
			{"ServerSidebarWidth", "Server rail width"},
			{"ChannelSidebarWidth", "Channel list width"},
			{"MemberSidebarWidth", "Member list width"},
			{"ServerIconSize", "Server icon"},
			{"ServerItemHeight", "Server row"},
			{"ServerMarkerHeight", "Server selection bar"},
			{"SelectionMarkerWidth", "Selection bar width"},
			{"ChannelItemHeight", "Channel row"},
			{"ChannelLabelSize", "Channel name text"},
			{"CategoryHeight", "Category row"},
			{"CategorySpacing", "Around a category"},
			{"MemberRowHeight", "Member row"},
			{"MemberAvatarSize", "Member avatar"},
			{"MemberNameSize", "Member name text"},
			{"ConversationItemHeight", "Direct message row"},
			{"ConversationAvatarSize", "Direct message avatar"},
		},
		preview: func(p *SettingsPage) fyne.CanvasObject { return p.sidebarPreview() },
	},
	{
		caption: "Composer",
		detail:  "The card the message is typed into.",
		fields: []styleField{
			{"ComposerDockMargin", "Margin around the card"},
			{"ComposerRadius", "Corner radius"},
			{"ComposerPaddingV", "Padding, vertical"},
			{"ComposerPaddingH", "Padding, horizontal"},
			{"ComposerGutterWidth", "Gutter"},
			{"ComposerButtonSize", "Buttons"},
			{"ComposerIconSize", "Button icons"},
			{"ComposerMaxLines", "Lines before it scrolls"},
			{"SlowmodeTextSize", "Slowmode chip text"},
			{"SlowmodeGlyphSize", "Slowmode chip glyph"},
		},
	},
	{
		caption: "Buttons",
		detail:  "Every button the client draws, from a card's action to a settings row.",
		fields: []styleField{
			{"ButtonRadius", "Corner radius"},
			{"ButtonTextSize", "Label text"},
			{"ButtonPaddingV", "Padding, vertical"},
			{"ButtonPaddingH", "Padding, horizontal"},
			{"ButtonMinWidth", "Narrowest it is drawn"},
			{"ButtonMinHeight", "Shortest it is drawn"},
		},
		preview: func(p *SettingsPage) fyne.CanvasObject { return p.buttonPreview() },
	},
	{
		caption: "Cards and edges",
		detail:  "Every border the client draws is the same hairline.",
		fields: []styleField{
			{"OutlineWidth", "Hairline"},
			{"CardShadowBlur", "Composer shadow"},
			{"EmbedRadius", "Embed corner"},
			{"EmbedPaddingV", "Embed padding, vertical"},
			{"EmbedPaddingH", "Embed padding, horizontal"},
			{"InviteCardWidth", "Invite card width"},
			{"InviteIconSize", "Invite server icon"},
			{"ChipRadius", "Chip corner"},
			{"ProfileCornerRadius", "Profile card corner"},
			{"TooltipRadius", "Tooltip corner"},
			{"NoticeRadius", "Notice corner"},
			{"ConfirmRadius", "Confirmation corner"},
			{"ViewerCornerRadius", "Lightbox corner"},
		},
		advanced: true,
	},
	{
		caption: "Scroll indicator",
		detail:  "The bar beside the conversation while it moves. A width of zero removes it.",
		fields: []styleField{
			{"ScrollIndicatorWidth", "Width"},
			{"ScrollIndicatorInset", "Distance from the edge"},
			{"ScrollIndicatorMinHeight", "Shortest it is drawn"},
		},
		advanced: true,
	},
	{
		caption: "Media",
		detail:  "How large a picture is allowed to be drawn.",
		fields: []styleField{
			{"MessageImageMaxWidth", "Attachment width"},
			{"MessageImageMaxHeight", "Attachment height"},
			{"EmbedMaxWidth", "Embed width"},
			{"EmbedImageMaxHeight", "Embed picture height"},
		},
		advanced: true,
	},
}

// uncuratedSizeFields is every size-table entry no curated group claims, in
// declaration order — what the Advanced section lists, and what keeps the two
// halves of the page adding up to the whole table.
func uncuratedSizeFields() []string {
	claimed := make(map[string]bool)
	for _, group := range styleGroups {
		for _, field := range group.fields {
			claimed[field.name] = true
		}
	}

	all := theme.SizeFields()
	rest := make([]string, 0, len(all))
	for _, name := range all {
		if !claimed[name] {
			rest = append(rest, name)
		}
	}

	return rest
}

/* Previews */

// messagePreview draws two real message rows, so what a size does is answered by
// the widget that will draw it rather than an approximation. The messages are
// authored by the logged-in account, which is what lets the store resolve a name
// and a face for them.
func (p *SettingsPage) messagePreview() fyne.CanvasObject {
	deps := p.hooks.Deps

	self, ok := deps.Store.Self()
	if !ok {
		return previewPlaceholder("Sign in to preview a message.")
	}

	first := &domain.Message{ID: newPreviewID(), AuthorID: self.ID, Content: "The quick brown fox jumps over the lazy dog."}
	second := &domain.Message{ID: newPreviewID(), AuthorID: self.ID, Content: "And then it does it again, slightly lower down."}

	rows := VBoxNoSpacing(
		NewMessageWidget(deps, first, "", false, true),
		NewMessageWidget(deps, second, "", true, false),
	)

	return previewFrame(rows)
}

// sidebarPreview draws a channel row and a member row at their configured sizes.
func (p *SettingsPage) sidebarPreview() fyne.CanvasObject {
	deps := p.hooks.Deps

	channel := domain.Channel{ID: "preview", Name: "general", Kind: domain.ChannelText}

	// The member list places its rows itself, so a lone row has to be given the
	// height it would have had — which is the size this preview is here to show.
	member := newMemberRow(deps, nil)
	member.SetMember(&domain.Member{UserID: "preview", Name: "Someone", Presence: domain.PresenceOnline})

	rows := VBoxNoSpacing(
		NewFixedWidthContainer(theme.Sizes.ChannelSidebarWidth, NewChannelWidget(deps, channel, func() {})),
		VerticalSpacer(theme.Sizes.SettingsPreviewGap),
		NewFixedWidthContainer(theme.Sizes.MemberSidebarWidth,
			NewMinHeightContainer(theme.Sizes.MemberRowHeight, member)),
	)

	return previewFrame(rows)
}

// buttonPreview draws one of each weight, since what the sizes shape is the box
// around a word and a filled button proves it against a fill rather than against
// the hairline alone. Tapping one does nothing: a preview is a picture.
func (p *SettingsPage) buttonPreview() fyne.CanvasObject {
	row := HBoxNoSpacing(
		NewButton("Cancel", nil),
		HorizontalSpacer(theme.Sizes.ChipSpacing),
		NewWeightedButton("Join", ButtonPrimary, nil),
		HorizontalSpacer(theme.Sizes.ChipSpacing),
		NewWeightedButton("Leave", ButtonDanger, nil),
	)

	return previewFrame(row)
}

// previewFrame is the surface a sample sits on: the message area's own
// background, so what is drawn on it is drawn against what it will be drawn
// against.
func previewFrame(content fyne.CanvasObject) fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.MessageAreaBackground)
	background.CornerRadius = theme.Sizes.SettingsGroupRadius
	Outline(background)

	gap := theme.Sizes.SettingsPreviewGap

	return container.NewStack(background, NewInset(content, gap, gap, gap, gap))
}

func previewPlaceholder(text string) fyne.CanvasObject {
	label := newText(text, theme.Colors.TimestampText, theme.Sizes.SettingsDetailSize)

	return previewFrame(container.NewCenter(label))
}

// newPreviewID is a message ID for a sample row. A message carries its time in
// its ID, so a preview needs a real one or it draws without a timestamp.
func newPreviewID() string {
	return ulid.Make().String()
}

/* Option lists and bounds */

var densityOptions = []settingsOption{
	{"Cosy", config.DensityCosy},
	{"Compact", config.DensityCompact},
	{"Tiny", config.DensityTiny},
}

var clockOptions = []settingsOption{
	{"12-hour", config.TimeFormat12},
	{"24-hour", config.TimeFormat24},
}

// The ranges the sliders offer. They are limits on what can be *asked for*, not
// claims about what is sensible — the point of a slider is that the shape of the
// result is visible while it is being chosen.
const (
	minFontSize = 8
	maxFontSize = 28

	maxGroupWindow = 3600
	maxMountedCap  = 1000
	maxHistoryPage = 200
	maxDelayMS     = 2000
	maxScrollSpeed = 12

	maxRefreshDelay    = 5000
	maxMemberOverscan  = 50
	maxMessageOverscan = 50

	// The frame rate is floored well above zero: the slider reaches every value
	// between these two, and the ones near the bottom are indistinguishable from a
	// client that has stopped responding.
	minFrameRate = 15
	maxFrameRate = 600

	// maxTypingNames is a limit on the sentence, not on the feature: past a few
	// names the line is wider than it is worth and "and 4 others" says the same
	// thing in less.
	maxTypingNames = 5

	maxNoticeLifetime = 60
	maxNoticeStack    = 8

	minCacheMiB       = 16
	maxDiskMiB        = 8192
	maxImageLoaders   = 32
	maxMemoryMiB      = 2048
	minImageEdge      = 256
	maxImageEdge      = 8192
	minCachedMessages = 50
	maxCachedMessages = 5000
	maxCachedChannels = 50
	maxTextPreviews   = 1000
)

// densityBundles are the size overrides each density preset writes. Cosy is the
// client's own spacing, so it is expressed as "no overrides at all" rather than
// as a copy of the defaults that would drift from them.
var densityBundles = map[string]map[string]float32{
	config.DensityCompact: {
		"MessageVerticalPadding":        6,
		"MessageGroupedVerticalPadding": 1,
		"MessageAvatarSize":             32,
		"MessageAvatarColumnWidth":      38,
		"MessageContentPadding":         8,
		"ChannelItemHeight":             28,
		"MemberRowHeight":               30,
		"ConversationItemHeight":        38,
	},
	config.DensityTiny: {
		"MessageVerticalPadding":        3,
		"MessageGroupedVerticalPadding": 0,
		"MessageAvatarSize":             24,
		"MessageAvatarColumnWidth":      30,
		"MessageContentPadding":         5,
		"ChannelItemHeight":             24,
		"MemberRowHeight":               26,
		"ConversationItemHeight":        32,
	},
}

// applyDensity writes a preset's sizes, first clearing whatever the previous one
// left behind — otherwise moving from Tiny to Compact would keep every entry
// Compact does not mention.
func applyDensity(s *config.Settings, density string) {
	for _, bundle := range densityBundles {
		for field := range bundle {
			delete(s.Styles.Sizes, field)
		}
	}

	for field, value := range densityBundles[density] {
		def, ok := theme.DefaultSize(field)
		if !ok {
			continue
		}
		setSizeOverride(s, field, value, def)
	}
}

/* Small helpers */

// mustColor parses a hex the client itself produced. A value that will not parse
// falls back to the accent, so a hand-edited file cannot leave a swatch nil.
func mustColor(hex string) color.Color {
	if parsed, ok := theme.ParseHex(hex); ok {
		return parsed
	}

	return theme.Colors.ServerSelectedBg
}

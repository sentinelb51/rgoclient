package ui

// The settings sections. Each returns the groups its pane shows, top to bottom.
//
// Interface and Styles are the same table seen from two distances: Interface
// offers a decision ("accent colour", "density") and writes several entries from
// it, Styles offers the entries themselves. Advanced is what neither claimed,
// generated from the tables so a size added later is reachable the day it is
// declared rather than the day somebody remembers to list it.

import (
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/oklog/ulid/v2"

	"RGOClient/internal/cache"
	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

/* Account */

func (p *SettingsPage) accountSection() []fyne.CanvasObject {
	groups := []fyne.CanvasObject{p.group("Signed in as", "", p.identityRow())}

	var cards []fyne.CanvasObject
	for _, session := range p.hooks.Sessions() {
		forget := widget.NewButton("Forget", func() {
			p.hooks.ForgetSession(session.UserID)
			p.reload()
		})
		forget.Importance = ToneWarning.importance()

		control := HBoxNoSpacing(
			container.NewCenter(newSwatchlessAvatar(p.hooks.Deps, session.AvatarURL)),
			HorizontalSpacer(theme.Sizes.SettingsPreviewGap),
			container.NewCenter(forget),
		)
		cards = append(cards, p.row(session.Username, "Saved login", control))
	}
	if len(cards) > 0 {
		groups = append(groups, p.group("Saved logins",
			"Tokens kept on this machine so a login can be resumed without the password.",
			cards...))
	}

	groups = append(groups, p.group("Session", "",
		p.actionRow("Log out", "Ends this session and returns to the login screen.",
			"Log out", ToneDanger, func() {
				p.hooks.Confirm(Confirm{
					Title:     "Log out",
					Body:      "You will be returned to the login screen. Saved logins are kept.",
					Action:    "Log out",
					Tone:      ToneDanger,
					OnConfirm: p.hooks.LogOut,
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

	return p.row(self.Name, self.Handle, newSwatchlessAvatar(p.hooks.Deps, self.AvatarURL))
}

// newSwatchlessAvatar is the round face a row shows on its trailing edge.
func newSwatchlessAvatar(deps Deps, url string) fyne.CanvasObject {
	side := theme.Sizes.SessionCardAvatarSize

	return circularAvatar(deps.Images, url, fyne.NewSize(side, side))
}

/* Interface */

func (p *SettingsPage) interfaceSection() []fyne.CanvasObject {
	settings := config.Current().Interface

	accent := settings.Accent
	if accent == "" {
		accent = theme.Hex(theme.Colors.ServerSelectedBg)
	}

	return []fyne.CanvasObject{
		p.group("Appearance", "",
			p.accentRow(accent),
			p.optionRow("Density", "Sets the spacing of the message list in one go.",
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
				"Hides the repeated name and avatar when the same person writes again.",
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
	control := p.colorControl(accent, func(hex string) {
		p.restyle(func(s *config.Settings) {
			s.Interface.Accent = hex
			for field, value := range theme.AccentOverrides(hex) {
				setColorOverride(s, field, value)
			}
		})
	})

	return p.row("Accent colour", "Selection, focus rings, mentions and links.", control)
}

// fontSizeRow drives Fyne's own text size, which is what the built-in widgets —
// buttons, menus, entries — draw at. The client's own text is sized by named
// entries in the table, under Styles.
func (p *SettingsPage) fontSizeRow(size float32) fyne.CanvasObject {
	control := newNumberControl(float64(size), minFontSize, maxFontSize, 1, "pt", func(v float64) {
		p.restyle(func(s *config.Settings) { s.Interface.FontSize = float32(v) })
	})

	return p.row("Interface font size", "Buttons, menus and text fields.", control)
}

/* Styles */

func (p *SettingsPage) stylesSection() []fyne.CanvasObject {
	groups := make([]fyne.CanvasObject, 0, len(styleGroups)+2)

	for _, group := range styleGroups {
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
		p.actionRow("Reset all styles", "Puts every size and colour back to the client's own.",
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

func (p *SettingsPage) behaviourSection() []fyne.CanvasObject {
	settings := config.Current().Behaviour

	return []fyne.CanvasObject{
		p.group("Members", "",
			p.toggleRow("Sort the member list by name",
				"Off is cheaper: the list is re-sorted on every member event.",
				settings.SortMembers, func(s *config.Settings, on bool) { s.Behaviour.SortMembers = on }),
			p.toggleRow("Split into online and offline", "",
				settings.GroupByPresence, func(s *config.Settings, on bool) { s.Behaviour.GroupByPresence = on }),
			p.toggleRow("Hide offline members", "",
				settings.HideOfflineMembers, func(s *config.Settings, on bool) { s.Behaviour.HideOfflineMembers = on }),
		),
		p.group("Typing", "",
			p.toggleRow("Let others see when I am typing", "",
				settings.SendTyping, func(s *config.Settings, on bool) { s.Behaviour.SendTyping = on }),
			p.toggleRow("Show when others are typing", "",
				settings.ShowTyping, func(s *config.Settings, on bool) { s.Behaviour.ShowTyping = on }),
			p.toggleRow("Mark typing in the channel list", "",
				settings.TypingInChannels, func(s *config.Settings, on bool) { s.Behaviour.TypingInChannels = on }),
			p.note(notImplemented+" These are remembered and will take effect when it is."),
		),
		p.group("Messages", "How much of a conversation is kept drawn at once.",
			p.numberRow("Group window",
				"The longest gap two messages may still group across.",
				settings.GroupWindowSeconds, 0, maxGroupWindow, "s",
				func(s *config.Settings, v int) { s.Behaviour.GroupWindowSeconds = v }),
			p.numberRow("Mounted on opening a channel",
				"Fewer is a faster channel switch; scrollback fills in from cache.",
				settings.InitialMountCount, 5, maxMountedCap, "",
				func(s *config.Settings, v int) { s.Behaviour.InitialMountCount = v }),
			p.numberRow("Mounted at most",
				"The ceiling during scrollback. Every mounted message is real memory.",
				settings.MountedCap, 20, maxMountedCap, "",
				func(s *config.Settings, v int) { s.Behaviour.MountedCap = max(v, s.Behaviour.InitialMountCount) }),
			p.numberRow("Fetched per scroll-up", "",
				settings.HistoryPageSize, 5, maxHistoryPage, "",
				func(s *config.Settings, v int) { s.Behaviour.HistoryPageSize = v }),
		),
		p.group("Timing", "",
			p.numberRow("Author lookup batching",
				"How long a burst of unknown authors is collected before one request.",
				settings.AuthorFetchDelayMS, 0, maxDelayMS, "ms",
				func(s *config.Settings, v int) { s.Behaviour.AuthorFetchDelayMS = v }),
			p.numberRow("Read receipt delay",
				"How long acknowledgements are coalesced for the open channel.",
				settings.AckDelayMS, 0, maxDelayMS, "ms",
				func(s *config.Settings, v int) { s.Behaviour.AckDelayMS = v }),
		),
		p.group("Input", "",
			p.numberRow("Scroll speed", "", settings.ScrollSpeed, 1, maxScrollSpeed, "×",
				func(s *config.Settings, v int) { s.Behaviour.ScrollSpeed = v }),
			p.toggleRow("Enter sends the message",
				"Off sends on Ctrl+Enter and lets Enter start a new line.",
				settings.EnterSends, func(s *config.Settings, on bool) { s.Behaviour.EnterSends = on }),
		),
	}
}

/* Notifications */

func (p *SettingsPage) notificationsSection() []fyne.CanvasObject {
	settings := config.Current().Notifications

	return []fyne.CanvasObject{
		p.group("Notices", "The cards that appear in the top-right corner.",
			p.numberRow("Stay for", "", settings.LifetimeSeconds, 1, maxNoticeLifetime, "s",
				func(s *config.Settings, v int) { s.Notifications.LifetimeSeconds = v }),
			p.numberRow("At most", "", settings.MaxStacked, 1, maxNoticeStack, "",
				func(s *config.Settings, v int) { s.Notifications.MaxStacked = v }),
		),
		p.group("Show", "",
			p.toggleRow("Information", "Something happened and nothing is wrong.",
				settings.ShowInfo, func(s *config.Settings, on bool) { s.Notifications.ShowInfo = on }),
			p.toggleRow("Warnings", "Disruptive, but not destructive.",
				settings.ShowWarning, func(s *config.Settings, on bool) { s.Notifications.ShowWarning = on }),
			p.toggleRow("Failures", "Something did not work.",
				settings.ShowDanger, func(s *config.Settings, on bool) { s.Notifications.ShowDanger = on }),
		),
	}
}

/* Cache */

func (p *SettingsPage) cacheSection() []fyne.CanvasObject {
	settings := config.Current().Cache

	disk := p.newUsageMeter("On disk", "Measuring…")
	memory := p.newUsageMeter("In memory", "Decoded and ready to draw.")
	p.hooks.CacheStats(func(stats cache.ImageStats) {
		disk.set(stats.DiskBytes, settings.ImageDiskBytes(), fileCount(stats.Files))
		memory.set(stats.MemoryBytes, settings.ImageMemoryBytes(), "Decoded and ready to draw.")
	})

	return []fyne.CanvasObject{
		p.group("Images", "Avatars, icons and attachments, kept on disk between runs.",
			p.locationRow(settings.ImageDir),
			p.note("The location is read once at startup; a change applies after a restart."),
		),
		p.group("Usage", "",
			disk.block,
			memory.block,
			p.numberRow("Disk budget", "Trimmed oldest-first once it is exceeded.",
				settings.ImageDiskMiB, minCacheMiB, maxDiskMiB, "MiB",
				func(s *config.Settings, v int) { s.Cache.ImageDiskMiB = v }),
			p.numberRow("Memory budget", "Decoded images held for instant redraw.",
				settings.ImageMemoryMiB, minCacheMiB, maxMemoryMiB, "MiB",
				func(s *config.Settings, v int) { s.Cache.ImageMemoryMiB = v }),
			p.numberRow("Largest decoded side",
				"A photo arrives at full resolution and is never drawn that large.",
				settings.MaxImageEdge, minImageEdge, maxImageEdge, "px",
				func(s *config.Settings, v int) { s.Cache.MaxImageEdge = v }),
			p.actionRow("Clear the image cache",
				"Everything is fetched again as it is next drawn.",
				"Clear", ToneDanger, func() {
					p.hooks.Confirm(Confirm{
						Title:  "Clear the image cache",
						Body:   "Every cached avatar and attachment is deleted. They will be downloaded again as they are drawn.",
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
			p.numberRow("Kept per channel", "", settings.MessagesPerChannel, minCachedMessages, maxCachedMessages, "",
				func(s *config.Settings, v int) { s.Cache.MessagesPerChannel = v }),
			p.numberRow("Channels cached", "", settings.CachedChannels, 1, maxCachedChannels, "",
				func(s *config.Settings, v int) { s.Cache.CachedChannels = v }),
			p.numberRow("Text previews cached", "", settings.TextPreviews, 1, maxTextPreviews, "",
				func(s *config.Settings, v int) { s.Cache.TextPreviews = v }),
			p.note("These caches are built at startup, so a change applies after a restart."),
		),
	}
}

// locationRow is where the image cache lives, and the two things that can be
// done about it. The path is the row's explanation rather than its value: it is
// long enough to need the whole width, and shortening it from the front would
// hide the part that says which drive it is on.
func (p *SettingsPage) locationRow(configured string) fyne.CanvasObject {
	path := canvas.NewText(p.hooks.CacheDir(), theme.Colors.TimestampText)
	path.TextSize = theme.Sizes.SettingsDetailSize

	choose := widget.NewButton("Change…", func() {
		p.hooks.ChooseCacheDir(func(picked string) {
			p.change(func(s *config.Settings) { s.Cache.ImageDir = picked })
			p.reload()
		})
	})

	open := widget.NewButton("Open", func() { p.hooks.OpenPath(p.hooks.CacheDir()) })

	controls := []fyne.CanvasObject{choose, HorizontalSpacer(theme.Sizes.ChipSpacing), open}
	if configured != "" {
		// Only offered once there is something to go back from, so the row does not
		// advertise a default nobody has moved away from.
		reset := widget.NewButton("Default", func() {
			p.change(func(s *config.Settings) { s.Cache.ImageDir = "" })
			p.reload()
		})
		controls = append(controls, HorizontalSpacer(theme.Sizes.ChipSpacing), reset)
	}

	return p.rowWith("Location", NewEllipsisText(path), HBoxNoSpacing(controls...))
}

// usageMeter is one budget drawn as a bar, and the setter that fills it once the
// measurement — which walks the cache directory — comes back.
type usageMeter struct {
	block fyne.CanvasObject
	set   func(used, total int64, detail string)
}

// newUsageMeter builds the meter empty. Its figures arrive from a goroutine, so
// what it draws until then has to be something rather than nothing.
func (p *SettingsPage) newUsageMeter(label, placeholder string) *usageMeter {
	name := canvas.NewText(label, theme.Colors.TextPrimary)
	name.TextSize = theme.Sizes.SettingsLabelSize

	amount := canvas.NewText("", theme.Colors.TextPrimary)
	amount.TextSize = theme.Sizes.SettingsDetailSize

	detail := canvas.NewText(placeholder, theme.Colors.TimestampText)
	detail.TextSize = theme.Sizes.SettingsDetailSize

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
			amount.Text = fmt.Sprintf("%s of %s",
				util.FormatFileSize(int(used)), util.FormatFileSize(int(total)))
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

// fileCount reads as a sentence rather than a number on its own, since it sits
// under a bar that is already showing a size.
func fileCount(files int) string {
	if files == 1 {
		return "1 file"
	}

	return fmt.Sprintf("%d files", files)
}

/* Advanced */

// advancedSection lists what the curated groups did not claim. It is long by
// construction — the point is that nothing is unreachable — so it opens with a
// filter, which is what keeps the friendly sections short without hiding
// anything.
//
// The two lists are refilled in place rather than rebuilt through reload: a
// rebuild would replace the field being typed into on the first keystroke.
func (p *SettingsPage) advancedSection() []fyne.CanvasObject {
	sizes, colors := VBoxNoSpacing(), VBoxNoSpacing()

	fill := func(query string) {
		var sizeRows, colorRows []fyne.CanvasObject
		for _, field := range uncuratedSizeFields() {
			if row := p.sizeRow(field, field); row != nil && matchesField(field, query) {
				sizeRows = append(sizeRows, row)
			}
		}
		for _, field := range theme.ColorFields() {
			if row := p.colorRow(field, field); row != nil && matchesField(field, query) {
				colorRows = append(colorRows, row)
			}
		}

		sizes.Objects = separateRows(sizeRows)
		colors.Objects = separateRows(colorRows)
		sizes.Refresh()
		colors.Refresh()
	}
	fill("")

	filter := widget.NewEntry()
	filter.PlaceHolder = "Padding, Radius…"
	filter.OnChanged = fill

	return []fyne.CanvasObject{
		p.group("Find", "", p.row("Filter by name", "", textField(filter))),
		p.groupOf("Every other size",
			"Named exactly as the client names them. The Styles section covers the rest.",
			sizes),
		p.groupOf("Palette", "Every colour the client draws with.", colors),
	}
}

// matchesField is the filter: a case-insensitive substring of the field's own
// name, which is what the rows are labelled with here.
func matchesField(field, query string) bool {
	if query == "" {
		return true
	}

	return strings.Contains(strings.ToLower(field), strings.ToLower(query))
}

/* About */

func (p *SettingsPage) aboutSection() []fyne.CanvasObject {
	return []fyne.CanvasObject{
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
				"Everything on these pages returns to its default.",
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
// shape.
type styleGroup struct {
	caption string
	detail  string
	fields  []styleField
	preview func(p *SettingsPage) fyne.CanvasObject
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
		caption: "Cards and edges",
		detail:  "Every border the client draws is the same hairline.",
		fields: []styleField{
			{"OutlineWidth", "Hairline"},
			{"CardShadowBlur", "Composer shadow"},
			{"EmbedRadius", "Embed corner"},
			{"EmbedPaddingV", "Embed padding, vertical"},
			{"EmbedPaddingH", "Embed padding, horizontal"},
			{"ChipRadius", "Chip corner"},
			{"ProfileCornerRadius", "Profile card corner"},
			{"TooltipRadius", "Tooltip corner"},
			{"NoticeRadius", "Notice corner"},
			{"ConfirmRadius", "Confirmation corner"},
			{"ViewerCornerRadius", "Lightbox corner"},
		},
	},
	{
		caption: "Scroll indicator",
		detail:  "The bar beside the conversation while it moves. A width of zero removes it.",
		fields: []styleField{
			{"ScrollIndicatorWidth", "Width"},
			{"ScrollIndicatorInset", "Distance from the edge"},
			{"ScrollIndicatorMinHeight", "Shortest it is drawn"},
		},
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
	},
}

// uncuratedSizeFields is every entry of the size table that no curated group
// claims, in declaration order. It is what the Advanced section lists, and what
// keeps the two halves of the settings page adding up to the whole table.
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
// the widget that will draw it rather than by an approximation of one. The
// client behind the page is covered, so this is the only thing that can answer.
//
// The messages are authored by the logged-in account, which is what lets the
// store resolve a name and a face for them.
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
	member := domain.Member{UserID: "preview", Name: "Someone"}

	rows := VBoxNoSpacing(
		NewFixedWidthContainer(theme.Sizes.ChannelSidebarWidth, NewChannelWidget(deps, channel, func() {})),
		VerticalSpacer(theme.Sizes.SettingsPreviewGap),
		NewFixedWidthContainer(theme.Sizes.MemberSidebarWidth, NewMemberWidget(deps, member, true)),
	)

	return previewFrame(rows)
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
	label := canvas.NewText(text, theme.Colors.TimestampText)
	label.TextSize = theme.Sizes.SettingsDetailSize

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

	maxNoticeLifetime = 60
	maxNoticeStack    = 8

	minCacheMiB       = 16
	maxDiskMiB        = 8192
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

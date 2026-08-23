package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/ui/theme"
)

/* The call dock */

// CallDock is the strip at the foot of the channel column while this account is
// in a call: what the call is doing, which channel it is in, and the three
// buttons for holding the microphone, holding the speakers and leaving.
//
// It belongs in that slot and no other. The channel column is present in both
// the server and the home view and is not rebuilt by a channel-list refresh, and
// a call outlives leaving the channel *and* the server — which is why this is
// not a composer dock badge, where everything is about the open channel, and not
// a ChannelNote, which is about the channel being read.
//
// Hidden when there is no call. Hiding a child reclaims nothing on its own, so
// every Show and Hide is followed by a Relayout of the column.
type CallDock struct {
	widget.BaseWidget

	state   *canvas.Text
	channel *canvas.Text

	// channelBox is the ellipsis container around channel, and channelName what it
	// was last asked to say. ellipsisLayout rewrites the text object during layout
	// from its *own* copy, so setting channel.Text directly is overwritten on the
	// next pass — the name has to go through SetEllipsisText.
	channelBox  *fyne.Container
	channelName string

	// An OutlinedIconButton bakes its mark, its tint and its disabled state at
	// construction, so a toggle here is a button *replaced* in its slot rather
	// than one recoloured. Two slots, two states each.
	micSlot       *fyne.Container
	headphoneSlot *fyne.Container

	content fyne.CanvasObject

	onMute    func()
	onDeafen  func()
	onHangUp  func()
	onChannel func()

	muted    bool
	deafened bool
}

var _ fyne.Widget = (*CallDock)(nil)

// CallDockActions is what the dock's four targets do. Each is called on the UI
// thread.
type CallDockActions struct {
	OnMute    func()
	OnDeafen  func()
	OnHangUp  func()
	OnChannel func() // the name is tappable and goes to the call's own channel
}

// NewCallDock builds the strip. It starts hidden: a dock is only ever shown by
// a call starting.
func NewCallDock(actions CallDockActions) *CallDock {
	w := &CallDock{
		onMute:    actions.OnMute,
		onDeafen:  actions.OnDeafen,
		onHangUp:  actions.OnHangUp,
		onChannel: actions.OnChannel,
	}

	w.state = newText("", theme.Colors.CallDockStateGood, theme.Sizes.CallDockStateSize)
	w.state.Alignment = fyne.TextAlignLeading

	w.channel = newText("", theme.Colors.CallDockText, theme.Sizes.CallDockNameSize)
	w.channel.Alignment = fyne.TextAlignLeading

	// The name is the tappable half: it is the thing worth going back to, and the
	// state line above it is a report rather than a target.
	w.channelBox = NewEllipsisText(w.channel)
	name := NewTappableContainer(w.channelBox, func() {
		if w.onChannel != nil {
			w.onChannel()
		}
	})

	lines := VBoxNoSpacing(w.state, VerticalSpacer(theme.Sizes.CallDockGap), name)

	w.micSlot = container.NewStack(w.micButton())
	w.headphoneSlot = container.NewStack(w.headphoneButton())

	// Outlined rather than plain: these are targets, and an outlined destructive
	// button wears the hairline in its own tint, so hanging up needs no other
	// signal. Three words in a 240 px column would be more to read than there is.
	hangUp := NewOutlinedIconButton(
		tintedIcon(assets.CallEndIcon, theme.Colors.CallDockDanger),
		theme.Colors.CallDockDanger,
		func() {
			if w.onHangUp != nil {
				w.onHangUp()
			}
		})

	buttons := HBoxNoSpacing(
		w.micSlot,
		HorizontalSpacer(theme.Sizes.CallDockButtonGap),
		w.headphoneSlot,
		HorizontalSpacer(theme.Sizes.CallDockButtonGap),
		hangUp,
	)

	// The lines take the leftover width and the buttons keep their own: an
	// ellipsis box reports zero on purpose, so anything that hands a child its
	// minimum — a Center, an HBox — draws no channel name at all.
	padV, padH := theme.Sizes.CallDockPaddingV, theme.Sizes.CallDockPaddingH
	row := NewFillRow(0, vcenter(lines),
		HorizontalSpacer(theme.Sizes.CallDockPaddingH), vcenter(buttons))

	background := canvas.NewRectangle(theme.Colors.CallDockBackground)
	background.SetMinSize(fyne.NewSize(0, theme.Sizes.CallDockHeight))

	w.content = VBoxNoSpacing(
		NewRowDivider(),
		container.NewStack(background, NewInset(row, padV, padV, padH, padH)),
	)

	w.ExtendBaseWidget(w)
	w.Hide()

	return w
}

func (w *CallDock) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(w.content)
}

// SetChannel names the channel the call is in.
func (w *CallDock) SetChannel(name string) {
	if w.channelName == name {
		return
	}
	w.channelName = name

	SetEllipsisText(w.channelBox, name)
}

// SetState says how the call is doing, in the colour that state deserves. good
// is false for anything the reader would want to know about.
func (w *CallDock) SetState(text string, good bool) {
	tint := theme.Colors.CallDockStatePoor
	if good {
		tint = theme.Colors.CallDockStateGood
	}

	if w.state.Text == text && w.state.Color == tint {
		return
	}

	w.state.Text = text
	w.state.Color = tint
	w.state.Refresh()
}

// SetMuted and SetDeafened redraw the two toggles. Both are no-ops on an
// unchanged value — a call reports its state more often than its state changes.
func (w *CallDock) SetMuted(muted bool) {
	if w.muted == muted {
		return
	}
	w.muted = muted

	w.micSlot.Objects = []fyne.CanvasObject{w.micButton()}
	w.micSlot.Refresh()
}

func (w *CallDock) SetDeafened(deafened bool) {
	if w.deafened == deafened {
		return
	}
	w.deafened = deafened

	w.headphoneSlot.Objects = []fyne.CanvasObject{w.headphoneButton()}
	w.headphoneSlot.Refresh()

	// A deafened call is a muted one, and the mic button says so by being drawn
	// disabled rather than by pretending it can be pressed.
	w.micSlot.Objects = []fyne.CanvasObject{w.micButton()}
	w.micSlot.Refresh()
}

// micButton builds the microphone toggle in whatever state the dock is in. It is
// rebuilt rather than recoloured because an OutlinedIconButton bakes its tint at
// construction, and a disabled one is a different button rather than a different
// colour.
func (w *CallDock) micButton() *OutlinedIconButton {
	res, tint := assets.MicIcon, theme.Colors.CallDockText
	if w.muted || w.deafened {
		res, tint = assets.MicOffIcon, theme.Colors.CallDockDanger
	}

	button := NewOutlinedIconButton(tintedIcon(res, tint), tint, func() {
		if w.onMute != nil {
			w.onMute()
		}
	})

	// Deafened holds the microphone whatever the mute button says, so pressing it
	// would be a press with no effect.
	if w.deafened {
		return button.disabled()
	}

	return button
}

// headphoneButton is the same for the speakers, which have no third state.
func (w *CallDock) headphoneButton() *OutlinedIconButton {
	res, tint := assets.HeadphonesIcon, theme.Colors.CallDockText
	if w.deafened {
		res, tint = assets.HeadphonesOffIcon, theme.Colors.CallDockDanger
	}

	return NewOutlinedIconButton(tintedIcon(res, tint), tint, func() {
		if w.onDeafen != nil {
			w.onDeafen()
		}
	})
}

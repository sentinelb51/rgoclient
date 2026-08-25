// Package ui contains the reusable widgets, layouts, and theme glue for the
// client. Widgets receive everything they need through Deps rather than
// reaching for global state.
package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"log"
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"golang.design/x/clipboard"

	"RGOClient/internal/cache"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
)

/* Dependencies */

// Deps bundles everything a widget needs from the rest of the app. The
// controller is its only producer (App.deps), so every field is always set.
type Deps struct {
	Store   domain.Store      // resolves IDs into names, avatars and permissions
	Images  *cache.ImageCache // avatars, icons, attachments
	Emojis  *cache.ImageCache // custom emoji, kept in a pool of their own
	Texts   *cache.TextCache  // text-attachment previews
	Actions MessageActions    // user-interaction callbacks

	// Tooltip is the app's floating label, the one layer that can overhang a
	// widget's column. The widget's to show and hide: where a label goes is a
	// question about the thing being hovered.
	Tooltip *Tooltip
}

// MessageActions handles the user interactions that originate from widgets. It
// is implemented by the application controller.
type MessageActions interface {
	// OnUserTapped opens someone's profile. anchor is the widget that was clicked
	// — a message avatar, a member row, a mention in a body — which the compact
	// card is placed beside.
	OnUserTapped(userID string, anchor fyne.CanvasObject)

	// OnChannelTapped goes to a channel, as tapping a rendered #mention does. The
	// channel need not be in the open server, or in one at all, so the controller
	// decides what has to be switched to on the way.
	OnChannelTapped(channelID string)

	// OnServerTapped goes to a server the account is already in, as an invite
	// card's "Go to server" does. Unlike a channel there is nothing to open
	// inside it, so the controller picks the first channel it can see.
	OnServerTapped(serverID string)

	// OnJoinInvite redeems an invite code. The joined server arrives through the
	// gateway, so nothing is expected back.
	OnJoinInvite(code string)

	// ResolveInvite fills in what an invite code opens. Alone among the reads it is
	// a request rather than a lookup — an invite names a server the account is by
	// definition not in — so the answer arrives through done, on the UI thread. The
	// controller remembers it: the same card remounts on every scroll past it.
	ResolveInvite(code string, done func(domain.Invite, error))

	OnAttachmentTapped(attachment *domain.File)
	OnReply(message *domain.Message)
	OnEdit(message *domain.Message)
	OnDelete(message *domain.Message)

	// OnPin pins or unpins a message. Which of the two is asked for rather than
	// toggled: the widget has already read the state to label its own menu item,
	// and re-deriving it in the controller could only disagree.
	OnPin(message *domain.Message, pinned bool)

	// OnReact adds or removes this account's reaction, stated either way for the
	// same reason: the chip has already read whether the account is in it.
	OnReact(message *domain.Message, emoji string, add bool)

	// OnClearReactions takes every reaction off a message — a moderator's action,
	// one request where unreacting for everybody is one per person per emoji.
	OnClearReactions(message *domain.Message)

	// OnAttachFile asks for a file to hang on the next message and reports what was
	// picked, or nothing. The controller owns the ask because the picker is the OS
	// one, which needs a window this package is never handed.
	OnAttachFile(onPicked func(path string))

	// OnPickEmoji opens the emoji picker beside anchor and reports what is chosen.
	// The controller opens it because what is on offer is a walk of every server the
	// account is in, ordered around the open one, which no widget knows.
	OnPickEmoji(anchor fyne.CanvasObject, onPick func(EmojiChoice))

	// ResolveMessage looks a message up in the local cache, never the network.
	ResolveMessage(channelID, messageID string) *domain.Message

	// OnJumpToMessage brings a message into view, fetching the page around one older
	// than anything mounted. Unlike ResolveMessage the caller is not told whether it
	// worked: what a jump does about a message it cannot find is the controller's.
	OnJumpToMessage(channelID, messageID string)
}

/* UI thread */

// DoOnUI schedules fn on the Fyne UI thread and returns at once — every
// background callback in this package funnels through here. App.doOnUI is the
// controller's equivalent and can also block; internal/cache reaches the driver
// directly, being unable to import this package.
func DoOnUI(fn func()) {
	fyne.CurrentApp().Driver().DoFromGoroutine(fn, false)
}

/* Icons */

// newScaledIcon is a smoothly scaled image for a resource, pinned to a square
// minimum by a positive side and free to fill its parent at zero. Every
// icon-bearing widget here draws through it, so they share one scale policy.
func newScaledIcon(res fyne.Resource, side float32) *canvas.Image {
	icon := canvas.NewImageFromResource(res)
	icon.FillMode = canvas.ImageFillContain
	icon.ScaleMode = canvas.ImageScaleSmooth

	if side > 0 {
		icon.SetMinSize(fyne.NewSize(side, side))
	}

	return icon
}

// iconStroke is the colour the client's own marks are drawn in. They are
// outlines, and Fyne's recolouring rewrites *fills* only — theme.NewColoredResource
// hands a stroked mark back exactly as white as it found it — so tinting one is a
// substitution in the source instead.
const iconStroke = "#ffffff"

// tintedIcons memoises the substitution below — every message row asks for its
// marks as it is built. UI thread only, like the measurement caches.
var tintedIcons = map[string]fyne.Resource{}

// tintedIcon recolours one of the client's stroked marks. The colour is part of
// the resource's *name* because Fyne caches a rasterised SVG under it, so two
// resources sharing a name would share one raster. That name is also this cache's
// key, so a restyle misses rather than returning what the palette used to say.
func tintedIcon(res fyne.Resource, c color.Color) fyne.Resource {
	hex := theme.Hex(c)
	name := hex + "-" + res.Name()
	if cached, ok := tintedIcons[name]; ok {
		return cached
	}

	tinted := fyne.NewStaticResource(name,
		bytes.ReplaceAll(res.Content(), []byte(iconStroke), []byte(hex)))
	tintedIcons[name] = tinted

	return tinted
}

// CautionMark tints a mark for a menu item whose effect can be undone by doing
// the opposite. A fyne.MenuItem carries no colour of its own, so its icon is the
// only thing that can say what sort of item it is.
func CautionMark(res fyne.Resource) fyne.Resource {
	return tintedIcon(res, theme.Colors.SwiftActionCaution)
}

// DangerMark tints a mark for a menu item that takes something away for good.
func DangerMark(res fyne.Resource) fyne.Resource {
	return tintedIcon(res, theme.Colors.SwiftActionDanger)
}

/* Context menus */

// ShowContextMenu pops a menu up at pos (canvas coordinates) on the canvas that
// owns anchor. Callers assemble the item set for their own context while the
// popup mechanics live here. An empty set or an unattached anchor is a no-op.
func ShowContextMenu(anchor fyne.CanvasObject, items []*fyne.MenuItem, pos fyne.Position) {
	if len(items) == 0 {
		return
	}

	c := fyne.CurrentApp().Driver().CanvasForObject(anchor)
	if c == nil {
		return
	}

	newContextMenu(fyne.NewMenu("", items...), c).ShowAtPosition(pos)
}

// contextMenu is Fyne's menu wearing the client's hairline. widget.PopUpMenu
// paints its background inside the menu's own renderer, which nothing outside can
// reach, and NewMenu pins the impl so the renderer cannot be composed the way
// ObservableScroll composes the scroll's. The menu therefore goes in a plain PopUp
// with the border stacked over it, and what PopUpMenu did *around* the menu is
// done here: clamping into the canvas, and the key handling — exported Menu calls.
type contextMenu struct {
	widget.BaseWidget

	menu   *widget.Menu
	border *canvas.Rectangle

	popUp  *widget.PopUp
	canvas fyne.Canvas
}

var _ fyne.Focusable = (*contextMenu)(nil)

func newContextMenu(menu *fyne.Menu, c fyne.Canvas) *contextMenu {
	m := &contextMenu{
		menu:   widget.NewMenu(menu),
		border: canvas.NewRectangle(color.Transparent),
		canvas: c,
	}
	m.border.StrokeColor = theme.Colors.MenuOutline
	m.border.StrokeWidth = theme.Sizes.OutlineWidth
	m.border.CornerRadius = fynetheme.Size(fynetheme.SizeNameMenuRadius)
	m.ExtendBaseWidget(m)

	m.popUp = widget.NewPopUp(m, c)
	m.menu.OnDismiss = m.popUp.Hide

	return m
}

// CreateRenderer lays the border over the menu. The rectangle is not a widget,
// so the items underneath keep the pointer.
func (m *contextMenu) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(m.menu, m.border))
}

// ShowAtPosition drops the menu at pos, pulled back inside the canvas where it
// would otherwise hang off the right or bottom edge.
func (m *contextMenu) ShowAtPosition(pos fyne.Position) {
	size := m.MinSize()
	_, area := m.canvas.InteractiveArea()

	if pos.X+size.Width > area.Width {
		pos.X = max(area.Width-size.Width, 0)
	}
	if pos.Y+size.Height > area.Height {
		pos.Y = max(area.Height-size.Height, 0)
	}

	m.popUp.ShowAtPosition(pos)
	m.canvas.Focus(m)
}

func (m *contextMenu) FocusGained()   {}
func (m *contextMenu) FocusLost()     {}
func (m *contextMenu) TypedRune(rune) {}

// TypedKey drives the menu from the keyboard. The menu takes focus when it is
// shown, so Escape closes it rather than reaching the handler App.bindKeys left
// on the canvas for whatever is open behind it.
func (m *contextMenu) TypedKey(event *fyne.KeyEvent) {
	switch event.Name {
	case fyne.KeyDown:
		m.menu.ActivateNext()
	case fyne.KeyUp:
		m.menu.ActivatePrevious()
	case fyne.KeyRight:
		m.menu.ActivateLastSubmenu()
	case fyne.KeyLeft:
		m.menu.DeactivateLastSubmenu()
	case fyne.KeyEnter, fyne.KeyReturn, fyne.KeySpace:
		m.menu.TriggerLast()
	case fyne.KeyEscape:
		m.menu.Dismiss()
	}
}

// AnchorBelow returns the canvas position directly below obj, dropping a menu
// beneath an overflow button rather than at the cursor.
func AnchorBelow(obj fyne.CanvasObject) fyne.Position {
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(obj)
	return fyne.NewPos(pos.X, pos.Y+obj.Size().Height)
}

// showMenuHook pops the items hook supplies at the cursor — what the sidebar
// widgets' Menu fields are wired to. Building them on demand keeps the menu in
// step with state that moved since the row was mounted. A nil hook is a no-op.
func showMenuHook(anchor fyne.CanvasObject, hook func() []*fyne.MenuItem, event *fyne.PointEvent) {
	if hook == nil {
		return
	}

	ShowContextMenu(anchor, hook(), event.AbsolutePosition)
}

// CopyToClipboard puts text on the system clipboard.
func CopyToClipboard(text string) {
	fyne.CurrentApp().Clipboard().SetContent(text)
}

// CopyImageToClipboard puts a picture on the system clipboard. The PNG is
// encoded here from the decoded raster rather than passed through from what was
// downloaded, so only pixels can reach the clipboard: a file served as an image
// and carrying something else has already been through a decoder by the time it
// gets here, and what that decoder refused never arrives at all. Encoding a
// full-size picture is not free, so it runs off the UI thread.
func CopyImageToClipboard(img image.Image) {
	if img == nil {
		return
	}

	go func() {
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, img); err != nil {
			log.Printf("copy image: encode: %v", err)
			return
		}

		if err := clipboard.Init(); err != nil {
			log.Printf("copy image: clipboard unavailable: %v", err)
			return
		}

		clipboard.Write(clipboard.FmtImage, encoded.Bytes())
	}()
}

/* Links */

// openURL hands a link to the system browser. Fyne only does this for its own
// hyperlink widget and segment; an embed's title and the viewer's browser button
// are drawn from plainer parts and ask for themselves.
func openURL(raw string) {
	link, err := url.Parse(raw)
	if err == nil {
		err = fyne.CurrentApp().OpenURL(link)
	}
	if err != nil {
		log.Printf("open %s: %v", raw, err)
	}
}

/* Entry caret */

// caretWidth is the entry caret thickness. Fyne draws the caret
// SizeNameInputBorder wide, which AppTheme zeroes for flat, outline-free inputs
// — collapsing the caret to nothing with it.
const caretWidth = 2

// caretTheme restores a visible caret in one entry without bringing the outline
// back: a non-zero input border with both border strokes transparent. The caret
// keeps its accent because Fyne paints it from the *application* theme's Primary,
// where the strokes take the widget-scoped one overridden here.
type caretTheme struct{ fyne.Theme }

func (t *caretTheme) Size(name fyne.ThemeSizeName) float32 {
	if name == fynetheme.SizeNameInputBorder {
		return caretWidth
	}

	return t.Theme.Size(name)
}

func (t *caretTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if name == fynetheme.ColorNamePrimary || name == fynetheme.ColorNameInputBorder {
		return color.Transparent
	}

	return t.Theme.Color(name, variant)
}

// WithCaret wraps an entry in a scoped theme override that makes its caret
// visible. Use it wherever an entry is mounted.
func WithCaret(entry fyne.CanvasObject) fyne.CanvasObject {
	return container.NewThemeOverride(entry, &caretTheme{Theme: fyne.CurrentApp().Settings().Theme()})
}

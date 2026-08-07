// Package ui contains the reusable widgets, layouts, and theme glue for the
// client. Widgets receive everything they need through Deps rather than
// reaching for global state.
package ui

import (
	"bytes"
	"image/color"
	"log"
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

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
	Texts   *cache.TextCache  // text-attachment previews
	Actions MessageActions    // user-interaction callbacks
}

// MessageActions handles the user interactions that originate from widgets. It
// is implemented by the application controller.
type MessageActions interface {
	// OnUserTapped opens someone's profile. anchor is the widget that was clicked
	// — a message avatar, a member row — which the compact card is placed beside.
	OnUserTapped(userID string, anchor fyne.CanvasObject)
	OnAttachmentTapped(attachment *domain.File)
	OnReply(message *domain.Message)
	OnEdit(message *domain.Message)
	OnDelete(message *domain.Message)

	// ResolveMessage looks a message up in the local cache, never the network.
	ResolveMessage(channelID, messageID string) *domain.Message
}

/* UI thread */

// DoOnUI schedules fn on the Fyne UI thread and returns immediately. Widgets may
// only be touched from that thread, so every background callback in this package
// funnels through here.
//
// App.doOnUI is the controller's equivalent and can also block until fn returns;
// internal/cache reaches the driver directly, since it cannot import this
// package.
func DoOnUI(fn func()) {
	fyne.CurrentApp().Driver().DoFromGoroutine(fn, false)
}

/* Icons */

// newScaledIcon builds a smoothly scaled image for a resource. A positive side
// pins it to that square minimum; zero leaves it free to take whatever its
// parent gives. Every icon-bearing widget here draws through this, so they share
// one fill and scale policy.
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

// tintedIcons memoises the substitution below. Every message row asks for its
// marks as it is built, and the answer depends only on the pair, so a channel of
// system events would otherwise rewrite the same handful of files a hundred times.
// UI thread only, like the measurement caches.
var tintedIcons = map[string]fyne.Resource{}

// tintedIcon recolours one of the client's own stroked marks. The colour is part
// of the resource's name because Fyne caches a rasterised SVG under it: two
// resources sharing a name would share one raster, and the second colour asked
// for would come back as the first. That name is also this cache's key, so a
// restyle — which changes the colour, hence the name — misses it rather than
// handing back what the palette used to say.
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

/* Context menus */

// ShowContextMenu pops a menu up at pos (canvas coordinates) on the canvas that
// owns anchor. Callers assemble the item set for their own context while the
// popup mechanics live here. An empty set or an unattached anchor is a no-op.
func ShowContextMenu(anchor fyne.CanvasObject, items []*fyne.MenuItem, pos fyne.Position) {
	if len(items) == 0 {
		return
	}

	canvas := fyne.CurrentApp().Driver().CanvasForObject(anchor)
	if canvas == nil {
		return
	}

	widget.NewPopUpMenu(fyne.NewMenu("", items...), canvas).ShowAtPosition(pos)
}

// AnchorBelow returns the canvas position directly below obj, dropping a menu
// beneath an overflow button rather than at the cursor.
func AnchorBelow(obj fyne.CanvasObject) fyne.Position {
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(obj)
	return fyne.NewPos(pos.X, pos.Y+obj.Size().Height)
}

// showMenuHook pops the items hook supplies at the cursor. It is what the
// sidebar widgets' exported Menu fields are wired to: the items themselves are
// the controller's business, and building them on demand keeps the menu in step
// with state that changed since the row was mounted. A nil hook is a no-op.
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

/* Links */

// openURL hands a link to the system browser. Fyne only does this for its own
// hyperlink widget and segment; an embed's title and the viewer's browser button
// are drawn from plainer parts and ask for themselves.
func openURL(raw string) {
	link, err := url.Parse(raw)
	if err != nil {
		log.Printf("open %s: %v", raw, err)
		return
	}

	if err := fyne.CurrentApp().OpenURL(link); err != nil {
		log.Printf("open %s: %v", raw, err)
	}
}

/* Entry caret */

// caretWidth is the entry caret thickness. Fyne draws the caret
// SizeNameInputBorder wide, which AppTheme zeroes for flat, outline-free inputs
// — collapsing the caret to nothing with it.
const caretWidth = 2

// caretTheme restores a visible caret inside a single entry without bringing the
// outline back: it reports a non-zero input border (the caret width) and turns
// both border stroke colours transparent. The caret stays accent-coloured
// because Fyne paints it with the application theme's Primary, while the border
// strokes with the widget-scoped Primary overridden here.
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

package ui

import (
	"fmt"
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

// NewAttachmentViewer builds the card shown inside the attachment lightbox: a
// slim header (name, metadata, open-in-browser, close) over the attachment
// itself, sized to fit within bounds. Images render scaled to fit; text files get
// their full contents in a selectable, scrollable monospace pane. Anything else
// falls back to a card offering the browser.
//
// The card carries its own chrome — there is no native window here, so nothing
// has to be recoloured to match the palette.
func NewAttachmentViewer(deps Deps, attachment *revoltgo.File, bounds fyne.Size, onClose func()) fyne.CanvasObject {
	// The body gets what's left of bounds once the card's own chrome is paid
	// for: NewPadded insets all four sides, and the Border layout puts one more
	// gap between the header and the body.
	pad := fynetheme.Padding()
	body := fyne.NewSize(
		bounds.Width-2*pad,
		bounds.Height-theme.Sizes.ViewerHeaderHeight-3*pad,
	)

	var (
		content fyne.CanvasObject
		detail  string
	)
	switch {
	case util.IsImageAttachment(attachment):
		content, detail = viewerImage(deps, attachment, body)
	case util.Filetype(attachment.Filename) == util.FileTypeText:
		content = viewerText(attachment, body)
	default:
		content = viewerUnsupported(body)
	}

	card := canvas.NewRectangle(theme.Colors.ViewerCardBg)
	card.CornerRadius = theme.Sizes.ViewerCornerRadius

	well := canvas.NewRectangle(theme.Colors.ViewerBodyBg)
	well.CornerRadius = theme.Sizes.ViewerCornerRadius

	header := viewerHeader(attachment, detail, onClose)
	inner := container.NewBorder(header, nil, nil, nil, container.NewStack(well, content))
	return newTapSink(container.NewStack(card, container.NewPadded(inner)))
}

// viewerHeader is the card's title strip: filename on the left, then the file
// size (and, for images, their pixel dimensions), a browser button, and close.
func viewerHeader(attachment *revoltgo.File, detail string, onClose func()) fyne.CanvasObject {
	name := canvas.NewText(attachment.Filename, theme.Colors.TextPrimary)
	name.TextSize = theme.Sizes.ViewerTitleSize
	name.TextStyle = fyne.TextStyle{Bold: true}

	meta := util.FormatFileSize(attachment.Size)
	if detail != "" {
		meta = detail + "  ·  " + meta
	}
	info := canvas.NewText(meta, theme.Colors.TimestampText)
	info.TextSize = theme.Sizes.ViewerTitleSize

	actions := container.NewHBox(info, HorizontalSpacer(theme.Sizes.ViewerPadding))
	if link := attachment.URL(""); link != "" {
		actions.Add(NewIconButton(fynetheme.ComputerIcon(), func() {
			if u, err := url.Parse(link); err == nil {
				_ = fyne.CurrentApp().OpenURL(u)
			}
		}, nil))
	}
	actions.Add(NewCloseButton(onClose))

	strip := container.NewBorder(nil, nil,
		container.NewHBox(HorizontalSpacer(theme.Sizes.ViewerPadding), name), actions)
	return NewMinHeightContainer(theme.Sizes.ViewerHeaderHeight, strip)
}

// viewerImage renders the attachment scaled to fit within bounds, and reports its
// real pixel dimensions for the header.
func viewerImage(deps Deps, attachment *revoltgo.File, bounds fyne.Size) (fyne.CanvasObject, string) {
	pixelWidth, pixelHeight := util.AttachmentDimensions(attachment)
	size := FitWithin(pixelWidth, pixelHeight, bounds.Width, bounds.Height)
	if size.IsZero() {
		// No usable metadata: give the image the whole body and let it scale
		// itself once it arrives.
		size = bounds
	}
	size = fyne.NewSize(max(size.Width, theme.Sizes.ViewerMinWidth), max(size.Height, theme.Sizes.ViewerMinHeight))

	frame := container.NewStack()
	if link := attachment.URL(""); link != "" && attachment.ID != "" {
		deps.Images.LoadIntoContainer(attachment.ID, link, size, frame, false, nil)
	}

	detail := ""
	if pixelWidth > 0 && pixelHeight > 0 {
		detail = fmt.Sprintf("%d × %d", pixelWidth, pixelHeight)
	}
	return container.NewGridWrap(size, frame), detail
}

// viewerText shows a text attachment in full — the message preview only pulls
// the first few hundred characters — as selectable monospace text.
func viewerText(attachment *revoltgo.File, bounds fyne.Size) fyne.CanvasObject {
	body := widget.NewLabel("Loading...")
	body.TextStyle = fyne.TextStyle{Monospace: true}
	body.Wrapping = fyne.TextWrapWord
	body.Selectable = true

	go func() {
		text, err := fetchText(attachment.URL(""), attachmentViewerRead)
		DoOnUI(func() {
			if err != nil {
				body.SetText("Could not load this file.")
				return
			}
			body.SetText(text)
		})
	}()

	scroll := container.NewVScroll(body)
	return container.NewGridWrap(bounds, container.NewPadded(scroll))
}

// viewerUnsupported is the placeholder for attachments this client can't render
// inline; the header's browser button is the way out.
func viewerUnsupported(bounds fyne.Size) fyne.CanvasObject {
	label := widget.NewLabelWithStyle("No preview available for this file type.",
		fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	height := max(bounds.Height/3, theme.Sizes.ViewerMinHeight)
	return container.NewGridWrap(fyne.NewSize(bounds.Width, height), container.NewCenter(label))
}

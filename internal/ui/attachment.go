package ui

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/cache"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
	attachmentCardWidth   = 300    // width of a non-image attachment card
	attachmentTextHeight  = 150    // height of a text attachment's preview box
	attachmentFileHeight  = 64     // height of a generic file card
	attachmentBarHeight   = 28     // height of the name/size strip beneath one
	attachmentTextSize    = 12     // the name and size drawn on that strip
	attachmentFileIcon    = 32     // side length of the generic file glyph
	attachmentPreviewRead = 512    // bytes pulled from a text attachment to preview
	attachmentPreviewRune = 256    // runes of that read actually shown
	attachmentViewerRead  = 262144 // bytes pulled when opened in the viewer
)

// buildAttachments stacks each attachment with a small gap between them. onMenu
// is the message's right-click handler, which each one takes over: an attachment
// fills most of its row and, being innermost, would otherwise swallow the click.
//
// No left spacer — attachments share the body's content padding with the header
// above, so a preview lines up flush with the message text.
func buildAttachments(deps Deps, attachments []*domain.File, onMenu func(*fyne.PointEvent)) *fyne.Container {
	rows := make([]fyne.CanvasObject, 0, len(attachments))
	for _, attachment := range attachments {
		rows = append(rows, container.NewHBox(buildAttachment(deps, attachment, onMenu)))
	}

	return stackSpaced(theme.Sizes.MessageAttachmentSpacing, rows...)
}

// buildAttachment renders one attachment as an image, a text preview or a generic
// file card, with a name/size bar beneath. Images and text open in the viewer when
// tapped: the inline render of both is deliberately small.
func buildAttachment(deps Deps, attachment *domain.File, onMenu func(*fyne.PointEvent)) fyne.CanvasObject {
	isImage := attachment.Kind == domain.FileImage
	isText := attachment.Kind == domain.FileText
	bar := attachmentBar(attachment.Name, attachment.Size, nil)

	var content *fyne.Container
	switch {
	case isImage:
		content = buildImageAttachment(deps.Images, attachment, bar)
	case isText:
		content = buildTextAttachment(deps.Texts, attachment, bar)
	default:
		content = buildGenericAttachment(bar)
	}

	var onTap func()
	if isImage || isText {
		onTap = func() { deps.Actions.OnAttachmentTapped(attachment) }
	}

	// The stack frames the card, drawing the outline *over* the content — square,
	// like the picture it edges.
	stack := NewHoverableStack(content, onTap, nil)
	stack.onSecondaryTap = onMenu

	return stack
}

func buildImageAttachment(images *cache.ImageCache, attachment *domain.File, bar fyne.CanvasObject) *fyne.Container {
	bounds := fyne.NewSize(theme.Sizes.MessageImageMaxWidth, theme.Sizes.MessageImageMaxHeight)

	// No usable metadata: reserve a wide half-height box so the row barely moves
	// once the real picture arrives and the box takes its shape. It has to take it
	// — the bar is as wide as the box, and a portrait picture left in this one
	// wears one twice its own width.
	reserve := fyne.NewSize(bounds.Width, bounds.Height/2)

	picture := imageFrame(images, attachment, bounds, reserve, theme.Colors.ServerDefaultBg, nil)

	return VBoxNoSpacing(picture, bar)
}

func buildTextAttachment(texts *cache.TextCache, attachment *domain.File, bar fyne.CanvasObject) *fyne.Container {
	// Asked here rather than on a worker: the cache is a mutexed map read and never
	// the network, and a row re-mounted by the message column is a hit — hopping off
	// the thread for one only flashes the placeholder and parses the text again.
	text, ok := texts.Get(attachment.URL)

	body := "Loading preview..."
	if ok {
		body = textPreviewBlock(text)
	}

	preview := widget.NewRichTextFromMarkdown(body)
	preview.Wrapping = fyne.TextWrapWord

	background := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	background.SetMinSize(fyne.NewSize(attachmentCardWidth, attachmentTextHeight))

	content := container.NewStack(background, container.NewPadded(preview))
	if !ok {
		go fetchTextPreview(texts, attachment.URL, preview)
	}

	return VBoxNoSpacing(content, bar)
}

func buildGenericAttachment(bar fyne.CanvasObject) *fyne.Container {
	placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	placeholder.SetMinSize(fyne.NewSize(attachmentCardWidth, attachmentFileHeight))

	icon := newScaledIcon(fynetheme.FileIcon(), attachmentFileIcon)
	return VBoxNoSpacing(container.NewStack(placeholder, container.NewCenter(icon)), bar)
}

// attachmentBar renders a name/size strip. A non-nil onRemove also shows a close
// button, which the composer uses.
func attachmentBar(name string, size int, onRemove func()) fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.SwiftActionBg)
	background.SetMinSize(fyne.NewSize(0, attachmentBarHeight))

	nameLabel := newBoldText(name, theme.Colors.TextPrimary, attachmentTextSize)

	sizeLabel := newText(util.FormatFileSize(size), theme.Colors.TimestampText, attachmentTextSize)
	sizeLabel.Alignment = fyne.TextAlignTrailing

	left := container.NewHBox(HorizontalSpacer(8), nameLabel)
	right := container.NewHBox(sizeLabel, HorizontalSpacer(8))
	if onRemove != nil {
		right = container.NewHBox(sizeLabel, container.NewPadded(NewCloseButton(onRemove)), HorizontalSpacer(8))
	}

	return container.NewStack(background, container.NewBorder(nil, nil, left, right))
}

/* Text fetching */

// textPreviewBlock fences a preview: an attachment is drawn as code whatever is
// in it, so nothing in the file can restyle the card it is previewed on.
func textPreviewBlock(text string) string {
	return "```\n" + text + "\n```"
}

// fetchTextPreview loads the first few hundred characters into preview as a code
// block, for a URL the cache has not seen — the caller asks it first. Fetched once
// per URL; failures are not memoised, so they retry on the next rebuild. Call off
// the UI thread.
func fetchTextPreview(texts *cache.TextCache, url string, preview *widget.RichText) {
	full, err := fetchText(url, attachmentPreviewRead)
	if err != nil || full == "" {
		return
	}

	runes := []rune(full)
	if len(runes) > attachmentPreviewRune {
		runes = append(runes[:attachmentPreviewRune], []rune("...")...)
	}

	text := string(runes)
	texts.Set(url, text)

	// ParseMarkdown refreshes for itself.
	DoOnUI(func() { preview.ParseMarkdown(textPreviewBlock(text)) })
}

// fetchText downloads at most limit bytes of a text file — the cap is what keeps
// a "text" attachment of arbitrary size out of memory. Call off the UI thread.
func fetchText(url string, limit int) (string, error) {
	if url == "" {
		return "", errors.New("no attachment URL")
	}

	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %s: %s", url, resp.Status)
	}

	// ReadFull reports ErrUnexpectedEOF for a file shorter than the cap, the normal
	// case rather than a failure; only n matters.
	buf := make([]byte, limit)
	n, err := io.ReadFull(resp.Body, buf)
	if n == 0 && err != nil {
		return "", err
	}

	return string(buf[:n]), nil
}

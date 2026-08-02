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
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
	attachmentCardWidth   = 300    // width of a non-image attachment card
	attachmentTextHeight  = 150    // height of a text attachment's preview box
	attachmentFileHeight  = 64     // height of a generic file card
	attachmentBarHeight   = 28     // height of the name/size strip beneath one
	attachmentFileIcon    = 32     // side length of the generic file glyph
	attachmentPreviewRead = 512    // bytes pulled from a text attachment to preview
	attachmentPreviewRune = 256    // runes of that read actually shown
	attachmentViewerRead  = 262144 // bytes pulled when opened in the viewer
)

// buildAttachments stacks each attachment with a small gap between them.
// onMenu is the owning message's right-click handler, which each attachment
// takes over: an attachment fills most of the row it belongs to, and being the
// innermost object under the pointer it would otherwise swallow the click.
func buildAttachments(deps Deps, attachments []*revoltgo.File, onMenu func(*fyne.PointEvent)) *fyne.Container {
	box := container.NewVBox()

	for i, attachment := range attachments {
		if i > 0 {
			box.Add(VerticalSpacer(theme.Sizes.MessageAttachmentSpacing))
		}
		// No left spacer: attachments share the body's content padding with the
		// header text above, so the preview lines up flush with the message text.
		box.Add(container.NewHBox(buildAttachment(deps, attachment, onMenu)))
	}

	return box
}

// buildAttachment renders one attachment as an image, a text preview, or a
// generic file card, with a name/size bar beneath it. Images and text files open
// in the viewer when tapped — the inline render of both is deliberately small,
// so the full thing has to be reachable from somewhere.
func buildAttachment(deps Deps, attachment *revoltgo.File, onMenu func(*fyne.PointEvent)) fyne.CanvasObject {
	isImage := util.IsImageAttachment(attachment)
	isText := util.Filetype(attachment.Filename) == util.FileTypeText
	bar := attachmentBar(attachment.Filename, attachment.Size, nil)

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

	stack := NewHoverableStack(content, onTap, nil)
	stack.onSecondaryTap = onMenu

	return stack
}

func buildImageAttachment(images *cache.ImageCache, attachment *revoltgo.File, bar fyne.CanvasObject) *fyne.Container {
	width, height := util.AttachmentDimensions(attachment)
	size := fitWithin(width, height, theme.Sizes.MessageImageMaxWidth, theme.Sizes.MessageImageMaxHeight)
	if size.Width == 0 || size.Height == 0 {
		// No usable metadata: reserve a wide, half-height box so the row doesn't
		// jump much once the real image arrives.
		size = fyne.NewSize(theme.Sizes.MessageImageMaxWidth, theme.Sizes.MessageImageMaxHeight/2)
	}

	placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	placeholder.SetMinSize(size)
	image := container.NewStack(placeholder)

	if url := attachment.URL(""); url != "" && attachment.ID != "" {
		images.LoadIntoContainer(attachment.ID, url, size, image, false, nil)
	}

	return container.NewBorder(nil, bar, nil, nil, image)
}

func buildTextAttachment(texts *cache.TextCache, attachment *revoltgo.File, bar fyne.CanvasObject) *fyne.Container {
	preview := widget.NewRichTextFromMarkdown("Loading preview...")
	preview.Wrapping = fyne.TextWrapWord

	background := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	background.SetMinSize(fyne.NewSize(attachmentCardWidth, attachmentTextHeight))

	content := container.NewStack(background, container.NewPadded(preview))
	go fetchTextPreview(texts, attachment.URL(""), preview)

	return container.NewBorder(nil, bar, nil, nil, content)
}

func buildGenericAttachment(bar fyne.CanvasObject) *fyne.Container {
	placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	placeholder.SetMinSize(fyne.NewSize(attachmentCardWidth, attachmentFileHeight))

	icon := newScaledIcon(fynetheme.FileIcon(), attachmentFileIcon)
	return container.NewBorder(nil, bar, nil, nil,
		container.NewStack(placeholder, container.NewCenter(icon)))
}

// attachmentBar renders a name/size strip. A non-nil onRemove also shows a close
// button, which the composer uses.
func attachmentBar(name string, size int, onRemove func()) fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.SwiftActionBg)
	background.SetMinSize(fyne.NewSize(0, attachmentBarHeight))

	nameLabel := canvas.NewText(name, theme.Colors.TextPrimary)
	nameLabel.TextSize = 12
	nameLabel.TextStyle = fyne.TextStyle{Bold: true}

	sizeLabel := canvas.NewText(util.FormatFileSize(size), theme.Colors.TimestampText)
	sizeLabel.TextSize = 12
	sizeLabel.Alignment = fyne.TextAlignTrailing

	left := container.NewHBox(HorizontalSpacer(8), nameLabel)
	right := container.NewHBox(sizeLabel, HorizontalSpacer(8))
	if onRemove != nil {
		right = container.NewHBox(sizeLabel, container.NewPadded(NewCloseButton(onRemove)), HorizontalSpacer(8))
	}

	return container.NewStack(background, container.NewBorder(nil, nil, left, right))
}

/* Text fetching */

// fetchTextPreview loads the first few hundred characters of a text attachment
// into preview, formatted as a code block. Fetched once per URL; failures are
// not memoised, so they retry on the next rebuild. Call off the UI thread.
func fetchTextPreview(texts *cache.TextCache, url string, preview *widget.RichText) {
	text, ok := texts.Get(url)
	if !ok {
		full, err := fetchText(url, attachmentPreviewRead)
		if err != nil || full == "" {
			return
		}

		runes := []rune(full)
		if len(runes) > attachmentPreviewRune {
			runes = append(runes[:attachmentPreviewRune], []rune("...")...)
		}
		text = string(runes)
		texts.Set(url, text)
	}

	DoOnUI(func() {
		preview.ParseMarkdown("```\n" + text + "\n```")
		preview.Refresh()
	})
}

// fetchText downloads at most limit bytes of a text file. The cap is what keeps
// a "text" attachment of arbitrary size out of memory; the caller decides how
// much it can show. Call off the UI thread.
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

	// ReadFull reports ErrUnexpectedEOF for a file shorter than the cap, which is
	// the normal case rather than a failure; only n matters.
	buf := make([]byte, limit)
	n, err := io.ReadFull(resp.Body, buf)
	if n == 0 && err != nil {
		return "", err
	}

	return string(buf[:n]), nil
}

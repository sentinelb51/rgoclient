package ui

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
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
	attachmentBarHeight   = 28     // height of the name/size strip beneath an attachment
	attachmentFileIcon    = 32     // side length of the generic file glyph
	attachmentPreviewRead = 512    // bytes pulled from a text attachment for its preview
	attachmentPreviewRune = 256    // runes of that read actually shown
	maxCachedPreviews     = 100    // text previews kept in memory
	attachmentViewerRead  = 262144 // bytes pulled when a text attachment is opened in the viewer
)

// buildAttachments stacks each attachment with a small gap between them.
func buildAttachments(deps Deps, attachments []*revoltgo.File) *fyne.Container {
	box := container.NewVBox()
	for i, attachment := range attachments {
		if i > 0 {
			box.Add(VerticalSpacer(theme.Sizes.MessageAttachmentSpacing))
		}
		// No left spacer: attachments share the body's content padding with the
		// header text above, so the preview box lines up flush with the message
		// text instead of drifting a few px to the right.
		box.Add(container.NewHBox(buildAttachment(deps, attachment)))
	}
	return box
}

// buildAttachment renders one attachment as an image, text preview, or generic
// file card, with a name/size bar beneath it. Images and text files open in the
// viewer when tapped — the inline render of both is deliberately small (a capped
// thumbnail, the first few hundred characters), so the full thing has to be
// reachable from somewhere.
func buildAttachment(deps Deps, attachment *revoltgo.File) fyne.CanvasObject {
	isImage := util.IsImageAttachment(attachment)
	isText := util.Filetype(attachment.Filename) == util.FileTypeText
	bar := attachmentBar(attachment.Filename, attachment.Size, nil)

	var content *fyne.Container
	switch {
	case isImage:
		content = buildImageAttachment(deps.Images, attachment, bar)
	case isText:
		content = buildTextAttachment(attachment, bar)
	default:
		content = buildGenericAttachment(bar)
	}

	var onTap func()
	if (isImage || isText) && deps.Actions != nil {
		onTap = func() { deps.Actions.OnAttachmentTapped(attachment) }
	}
	return NewHoverableStack(content, onTap, nil)
}

func buildImageAttachment(images *cache.ImageCache, attachment *revoltgo.File, bar fyne.CanvasObject) *fyne.Container {
	width, height := util.AttachmentDimensions(attachment)
	size := FitWithin(width, height, theme.Sizes.MessageImageMaxWidth, theme.Sizes.MessageImageMaxHeight)
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

func buildTextAttachment(attachment *revoltgo.File, bar fyne.CanvasObject) *fyne.Container {
	preview := widget.NewRichTextFromMarkdown("Loading preview...")
	preview.Wrapping = fyne.TextWrapWord

	background := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	background.SetMinSize(fyne.NewSize(attachmentCardWidth, attachmentTextHeight))

	content := container.NewStack(background, container.NewPadded(preview))
	go fetchTextPreview(attachment.URL(""), preview)
	return container.NewBorder(nil, bar, nil, nil, content)
}

func buildGenericAttachment(bar fyne.CanvasObject) *fyne.Container {
	placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	placeholder.SetMinSize(fyne.NewSize(attachmentCardWidth, attachmentFileHeight))

	icon := newScaledIcon(fynetheme.FileIcon(), attachmentFileIcon)
	return container.NewBorder(nil, bar, nil, nil,
		container.NewStack(placeholder, container.NewCenter(icon)))
}

// previewCache memoises fetched text-attachment previews by URL: message widgets
// are rebuilt on every channel revisit, and without it each rebuild re-downloads
// every text attachment. Entries are at most attachmentPreviewRune runes and the
// map is LRU-bounded so a long session can't accumulate them without limit.
var (
	previewMu      sync.Mutex
	previewCache   = map[string]string{}
	previewRecency = cache.NewLRU()
)

// cachedPreview returns a memoised preview for url, if any.
func cachedPreview(url string) (string, bool) {
	previewMu.Lock()
	defer previewMu.Unlock()

	text, ok := previewCache[url]
	if ok {
		previewRecency.Touch(url)
	}
	return text, ok
}

// storePreview memoises a preview, evicting the least recently used entries
// past maxCachedPreviews.
func storePreview(url, text string) {
	previewMu.Lock()
	defer previewMu.Unlock()

	previewCache[url] = text
	previewRecency.Touch(url)
	for previewRecency.Len() > maxCachedPreviews {
		delete(previewCache, previewRecency.EvictOldest())
	}
}

// fetchTextPreview loads the first few hundred characters of a text attachment
// into preview, formatted as a code block. Fetched once per URL; failures are
// not cached, so they retry on the next rebuild.
func fetchTextPreview(url string, preview *widget.RichText) {
	text, ok := cachedPreview(url)
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
		storePreview(url, text)
	}

	DoOnUI(func() {
		preview.ParseMarkdown("```\n" + text + "\n```")
		preview.Refresh()
	})
}

// fetchText downloads at most limit bytes of a text file. The cap is what keeps
// a "text" attachment of arbitrary size from being pulled into memory whole; the
// caller decides how much it can show. Call off the UI thread.
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

// attachmentBar renders a name/size strip. When onRemove is non-nil it also
// shows a close button (used by the message composer).
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

package ui

import (
	"image/color"
	"io"
	"net/http"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
	maxReplyUsernameLength = 16
	maxReplyPreviewLength  = 80
	hoverHideDelay         = 50 * time.Millisecond
)

// MessageWidget renders a single chat message with a hover state that reveals
// quick-action buttons.
type MessageWidget struct {
	widget.BaseWidget
	content    fyne.CanvasObject
	background *canvas.Rectangle

	// authorText and avatar are retained so a message whose author resolves after
	// the widget is mounted (lazy per-author fetch) can be updated in place via
	// RefreshAuthor, instead of rebuilding the whole channel. Both are nil for a
	// grouped continuation message, which draws neither a name nor an avatar.
	authorText *canvas.Text
	avatar     *Avatar

	// gutterTimestamp is the small left-gutter time shown on a grouped continuation
	// message (in place of the avatar), revealed on hover. nil for a full message.
	gutterTimestamp *canvas.Text

	// bottomSpacer is the message's bottom margin, kept so SetFollowedByGroup can
	// tighten it when a continuation is appended directly beneath this message.
	bottomSpacer *canvas.Rectangle

	// The hover quick-actions are built lazily on first reveal (ensureActions):
	// the ~3 buttons and their icons aren't constructed for messages the pointer
	// never touches. deps/message are retained to build them later;
	// actionsOverlay holds them once built and is empty until then.
	deps           Deps
	message        *revoltgo.Message
	actionsOverlay *fyne.Container
	actions        *fyne.Container

	// hover tracking debounces the transition between the message body and the
	// floating action buttons so the buttons don't flicker.
	overMessage bool
	overActions bool
	hideTimer   *time.Timer
}

var (
	_ fyne.Widget            = (*MessageWidget)(nil)
	_ desktop.Hoverable      = (*MessageWidget)(nil)
	_ fyne.SecondaryTappable = (*MessageWidget)(nil)
)

// NewMessageWidget builds a message widget. When grouped is set the message is a
// continuation of the previous one from the same author (Discord/Stoat-style):
// it omits the avatar and name header and instead shows a small, hover-revealed
// timestamp in the avatar gutter. Vertical spacing is asymmetric: a head/standalone
// carries the full gap above it while a continuation carries only a tight gap, and
// followedByGroup tightens the bottom margin so a head sits flush against the
// continuations beneath it without changing the gap between separate groups.
func NewMessageWidget(deps Deps, message *revoltgo.Message, grouped, followedByGroup bool) *MessageWidget {
	w := &MessageWidget{
		background: canvas.NewRectangle(color.Transparent),
		deps:       deps,
		message:    message,
	}

	text := message.Content
	if message.System != nil {
		text = util.FormatSystemMessage(deps.Session, message.System)
	}

	var shortTime, fullTime string
	if t, err := util.Timestamp(message.ID); err == nil {
		shortTime, fullTime = util.ShortTime(t), util.NiceTime(t)
	}

	var leftColumn, body fyne.CanvasObject
	if grouped {
		// Transparent until hover; toggling colour (not visibility) keeps the
		// gutter's width fixed so the body never shifts when the time appears. The
		// gutter reports zero height so it never makes the continuation row taller
		// than its single line of text.
		w.gutterTimestamp = canvas.NewText(shortTime, color.Transparent)
		w.gutterTimestamp.TextSize = theme.Sizes.MessageTimestampSize
		gutter := &GutterLayout{Width: theme.Sizes.MessageAvatarColumnWidth, TopOffset: theme.Sizes.MessageTimestampTopOffset}
		leftColumn = container.New(gutter, w.gutterTimestamp)
		body = buildGroupedContent(deps, message, text)
	} else {
		name, nameColor, avatarID, avatarURL := resolveAuthor(deps, message)
		w.avatar = NewAvatar(deps.Images, avatarID, avatarURL, func() {
			if deps.Actions != nil {
				deps.Actions.OnAvatarTapped(message.Author)
			}
		})
		column := &FixedWidthColumnLayout{Width: theme.Sizes.MessageAvatarColumnWidth, TopAlign: true}
		leftColumn = container.New(column, w.avatar)

		w.authorText = canvas.NewText(name, nameColor)
		w.authorText.TextStyle = fyne.TextStyle{Bold: true}
		body = buildMessageContent(deps, message, w.authorText, fullTime, text)
	}

	paddedBody := container.NewBorder(nil, nil, HorizontalSpacer(theme.Sizes.MessageContentPadding), nil, body)
	row := container.NewBorder(nil, nil, leftColumn, nil, paddedBody)

	hPad := theme.Sizes.MessageHorizontalPadding
	w.bottomSpacer = canvas.NewRectangle(color.Transparent)
	w.bottomSpacer.SetMinSize(fyne.NewSize(0, verticalPad(followedByGroup)))
	inner := container.NewBorder(
		VerticalSpacer(verticalPad(grouped)), w.bottomSpacer,
		HorizontalSpacer(hPad), HorizontalSpacer(hPad),
		row,
	)

	w.actionsOverlay = container.New(&OverlayLayout{YOffset: -16, RightOffset: 6})
	messageRow := container.NewStack(inner, w.actionsOverlay)

	w.content = messageRow
	if !grouped && len(message.Replies) > 0 {
		replies := container.NewVBox()
		for _, replyID := range message.Replies {
			replies.Add(buildReplyPreview(deps, message.Channel, replyID))
			replies.Add(VerticalSpacer(-15))
		}
		w.content = container.NewVBox(replies, messageRow)
	}

	w.ExtendBaseWidget(w)
	return w
}

// buildActions creates the hidden, rounded group of quick-action buttons. The
// set is dynamic: reply is always offered, edit only on your own (non-system)
// message, and delete on your own message or where you can manage messages.
func (w *MessageWidget) buildActions(deps Deps) *fyne.Container {
	onHover := func(hovering bool) {
		w.overActions = hovering
		w.updateHover()
	}

	buttons := []fyne.CanvasObject{
		newIconButton(fynetheme.MailReplyIcon(), func() {
			if deps.Actions != nil {
				deps.Actions.OnReply(w.message)
			}
		}, onHover),
	}

	if w.canEdit() {
		buttons = append(buttons, newIconButton(fynetheme.DocumentCreateIcon(), func() {
			if deps.Actions != nil {
				deps.Actions.OnEdit(w.message)
			}
		}, onHover))
	}
	if w.canDelete() {
		buttons = append(buttons, newIconButton(fynetheme.DeleteIcon(), func() {
			if deps.Actions != nil {
				deps.Actions.OnDelete(w.message)
			}
		}, onHover))
	}

	// Overflow button: always last, opens the full context menu (the same one
	// right-clicking the message shows) beneath itself.
	more := newIconButton(fynetheme.MoreVerticalIcon(), nil, onHover)
	more.onTap = func() { ShowContextMenu(more, w.menuItems(), AnchorBelow(more)) }
	buttons = append(buttons, more)

	background := canvas.NewRectangle(theme.Colors.SwiftActionBg)
	background.CornerRadius = 4

	group := container.NewStack(background, HBoxNoSpacing(buttons...))
	group.Hide()
	return group
}

// menuItems builds the message's context-menu entries, mirroring the hover
// quick-actions (reply/edit/delete, gated the same way) plus copy helpers. Used
// by both the overflow button and the right-click handler.
func (w *MessageWidget) menuItems() []*fyne.MenuItem {
	act := w.deps.Actions

	items := []*fyne.MenuItem{
		fyne.NewMenuItemWithIcon("Reply", fynetheme.MailReplyIcon(), func() {
			if act != nil {
				act.OnReply(w.message)
			}
		}),
	}
	if w.canEdit() {
		items = append(items, fyne.NewMenuItemWithIcon("Edit", fynetheme.DocumentCreateIcon(), func() {
			if act != nil {
				act.OnEdit(w.message)
			}
		}))
	}

	items = append(items, fyne.NewMenuItemSeparator())
	if w.message.Content != "" {
		items = append(items, fyne.NewMenuItemWithIcon("Copy message", fynetheme.ContentCopyIcon(), func() {
			copyToClipboard(w.message.Content)
		}))
	}
	items = append(items,
		fyne.NewMenuItemWithIcon("Copy message ID", fynetheme.ContentCopyIcon(), func() {
			copyToClipboard(w.message.ID)
		}),
		fyne.NewMenuItemWithIcon("Copy author ID", fynetheme.AccountIcon(), func() {
			copyToClipboard(w.message.Author)
		}),
	)

	if w.canDelete() {
		del := fyne.NewMenuItemWithIcon("Delete", fynetheme.DeleteIcon(), func() {
			if act != nil {
				act.OnDelete(w.message)
			}
		})
		items = append(items, fyne.NewMenuItemSeparator(), del)
	}
	return items
}

// TappedSecondary opens the message context menu at the cursor on right-click.
func (w *MessageWidget) TappedSecondary(e *fyne.PointEvent) {
	ShowContextMenu(w, w.menuItems(), e.AbsolutePosition)
}

// isOwnMessage reports whether the message was authored by the logged-in user.
func (w *MessageWidget) isOwnMessage() bool {
	if w.deps.Session == nil {
		return false
	}
	self := w.deps.Session.State.Self()
	return self != nil && self.ID == w.message.Author
}

// canEdit reports whether the edit action should be offered: only your own
// regular messages (system messages have no editable content).
func (w *MessageWidget) canEdit() bool {
	return w.message.System == nil && w.isOwnMessage()
}

// canDelete reports whether the delete action should be offered: your own
// message, or any message in a channel where you hold ManageMessages.
func (w *MessageWidget) canDelete() bool {
	if w.isOwnMessage() {
		return true
	}
	if w.deps.Session == nil {
		return false
	}
	state := w.deps.Session.State
	self, channel := state.Self(), state.Channel(w.message.Channel)
	if self == nil || channel == nil {
		return false
	}
	perms, err := state.ChannelPermissions(self, channel)
	return err == nil && perms&revoltgo.PermissionManageMessages != 0
}

func (w *MessageWidget) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(w.background, w.content))
}

// ensureActions builds the hover quick-action buttons on first reveal, mounting
// them into the (until now empty) overlay. Subsequent reveals reuse them.
func (w *MessageWidget) ensureActions() {
	if w.actions != nil {
		return
	}
	w.actions = w.buildActions(w.deps)
	w.actionsOverlay.Objects = []fyne.CanvasObject{w.actions}
	w.actionsOverlay.Refresh()
}

// setGutterShown reveals or hides the grouped continuation's gutter timestamp by
// toggling its colour (kept at a fixed width, so the body never shifts). A no-op
// for full messages, which have no gutter timestamp.
func (w *MessageWidget) setGutterShown(shown bool) {
	if w.gutterTimestamp == nil {
		return
	}
	if shown {
		w.gutterTimestamp.Color = theme.Colors.TimestampText
	} else {
		w.gutterTimestamp.Color = color.Transparent
	}
	w.gutterTimestamp.Refresh()
}

// updateHover shows the action buttons while the pointer is over the message or
// the buttons, hiding them after a short grace period otherwise.
func (w *MessageWidget) updateHover() {
	if w.overMessage || w.overActions {
		if w.hideTimer != nil {
			w.hideTimer.Stop()
			w.hideTimer = nil
		}
		w.ensureActions()
		w.background.FillColor = theme.Colors.MessageHoverBackground
		w.background.Refresh()
		w.actions.Show()
		w.setGutterShown(true)
		return
	}

	if w.hideTimer != nil {
		return
	}
	w.hideTimer = time.AfterFunc(hoverHideDelay, func() {
		fyne.CurrentApp().Driver().DoFromGoroutine(func() {
			if w.overMessage || w.overActions {
				return
			}
			w.background.FillColor = color.Transparent
			w.background.Refresh()
			if w.actions != nil {
				w.actions.Hide()
			}
			w.setGutterShown(false)
			w.hideTimer = nil
		}, false)
	})
}

func (w *MessageWidget) MouseIn(*desktop.MouseEvent) {
	w.overMessage = true
	w.updateHover()
}

func (w *MessageWidget) MouseMoved(*desktop.MouseEvent) {}

func (w *MessageWidget) MouseOut() {
	w.overMessage = false
	w.updateHover()
}

// resolveAuthor resolves the display name, role colour, and avatar for a
// message's author. Shared by widget construction and RefreshAuthor so a lazily
// fetched author renders identically whether it was known up front or filled in
// later.
func resolveAuthor(deps Deps, message *revoltgo.Message) (name string, nameColor color.Color, avatarID, avatarURL string) {
	author := util.MessageAuthor(deps.Session, message)
	nameColor = theme.Colors.TextPrimary
	if author.Color != nil {
		nameColor = author.Color
	}
	return author.Name, nameColor, util.IDFromAttachmentURL(author.AvatarURL), author.AvatarURL
}

// verticalPad returns a message's top or bottom margin: tight when it abuts a
// same-author continuation, the full gap otherwise (which separates groups).
func verticalPad(tight bool) float32 {
	if tight {
		return theme.Sizes.MessageGroupedVerticalPadding
	}
	return theme.Sizes.MessageVerticalPadding
}

// Author returns the message author's user ID.
func (w *MessageWidget) Author() string { return w.message.Author }

// SetFollowedByGroup tightens (or restores) the bottom margin when a same-author
// continuation is appended directly beneath this message after it was mounted.
func (w *MessageWidget) SetFollowedByGroup(followed bool) {
	w.bottomSpacer.SetMinSize(fyne.NewSize(0, verticalPad(followed)))
	w.bottomSpacer.Refresh()
	w.Refresh()
}

// Message returns the message this widget renders.
func (w *MessageWidget) Message() *revoltgo.Message { return w.message }

// RefreshAuthor re-resolves the author's name, role colour, and avatar and
// applies them in place. Called when a previously-unknown author is fetched, or
// a member updates, after the widget was mounted — avoiding a full re-render of
// the channel. A grouped continuation shows neither name nor avatar, so there's
// nothing to update.
func (w *MessageWidget) RefreshAuthor() {
	if w.authorText == nil {
		return
	}
	name, nameColor, avatarID, avatarURL := resolveAuthor(w.deps, w.message)
	if w.authorText.Text != name || w.authorText.Color != nameColor {
		w.authorText.Text = name
		w.authorText.Color = nameColor
		w.authorText.Refresh()
	}
	w.avatar.SetSource(w.deps.Images, avatarID, avatarURL)
}

// buildMessageContent assembles the author/text header plus any attachments.
func buildMessageContent(deps Deps, message *revoltgo.Message, author *canvas.Text, timestamp, text string) fyne.CanvasObject {
	header := buildMessageHeader(author, text, timestamp)
	if len(message.Attachments) == 0 {
		return header
	}
	return container.NewVBox(header, buildAttachments(deps, message.Attachments))
}

// buildGroupedContent renders a grouped continuation message: just the body text
// (and any attachments), with no author/timestamp header.
func buildGroupedContent(deps Deps, message *revoltgo.Message, text string) fyne.CanvasObject {
	body := NewFlushContainer(renderMessageBody(text))
	if len(message.Attachments) == 0 {
		return body
	}
	return container.NewVBox(body, buildAttachments(deps, message.Attachments))
}

// buildMessageHeader renders the author line (the bold name in its role colour
// followed by a baseline-aligned timestamp) above the message text. Keeping the
// timestamp inline on the name line — rather than overlaid on the whole body —
// aligns it with the username and stops long body text from running under it.
func buildMessageHeader(author *canvas.Text, text, timestamp string) fyne.CanvasObject {
	ts := canvas.NewText(timestamp, theme.Colors.TimestampText)
	ts.TextSize = theme.Sizes.MessageTimestampSize
	// Drop the smaller timestamp so its baseline lines up with the bold name.
	tsAligned := VBoxNoSpacing(VerticalSpacer(theme.Sizes.MessageTimestampTopOffset), ts)

	nameLine := container.NewHBox(author, HorizontalSpacer(theme.Sizes.MessageContentPadding), tsAligned)
	return VBoxNoSpacing(nameLine, NewFlushContainer(renderMessageBody(text)))
}

// buildAttachments stacks each attachment with a small gap between them.
func buildAttachments(deps Deps, attachments []*revoltgo.Attachment) *fyne.Container {
	box := container.NewVBox()
	for i, attachment := range attachments {
		if i > 0 {
			box.Add(VerticalSpacer(theme.Sizes.MessageAttachmentSpacing))
		}
		item := buildAttachment(deps, attachment)
		// No left spacer: attachments share the body's content padding with the
		// header text above, so the preview box lines up flush with the message
		// text instead of drifting a few px to the right.
		box.Add(container.NewHBox(item))
	}
	return box
}

// buildAttachment renders one attachment as an image, text preview, or generic
// file card, with a name/size bar beneath it.
func buildAttachment(deps Deps, attachment *revoltgo.Attachment) fyne.CanvasObject {
	isImage := attachment.Metadata.Type == revoltgo.AttachmentMetadataTypeImage
	bar := attachmentBar(attachment.Filename, attachment.Size, nil)

	var content *fyne.Container
	switch {
	case isImage:
		content = buildImageAttachment(deps.Images, attachment, bar)
	case util.Filetype(attachment.Filename) == util.FileTypeText:
		content = buildTextAttachment(attachment, bar)
	default:
		content = buildGenericAttachment(bar)
	}

	return NewHoverableStack(content, func() {
		if isImage && deps.Actions != nil {
			deps.Actions.OnImageTapped(attachment)
		}
	}, nil)
}

func buildImageAttachment(images *cache.ImageCache, attachment *revoltgo.Attachment, bar fyne.CanvasObject) *fyne.Container {
	size := imageDisplaySize(attachment.Metadata.Width, attachment.Metadata.Height)
	placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	placeholder.SetMinSize(size)
	image := container.NewStack(placeholder)

	if url := attachment.URL(""); url != "" && attachment.ID != "" {
		images.LoadIntoContainer(attachment.ID, url, size, image, false, nil)
	}
	return container.NewBorder(nil, bar, nil, nil, image)
}

func buildTextAttachment(attachment *revoltgo.Attachment, bar fyne.CanvasObject) *fyne.Container {
	width := min(float32(300), theme.Sizes.MessageImageMaxWidth)

	preview := widget.NewRichTextFromMarkdown("Loading preview...")
	preview.Wrapping = fyne.TextWrapWord

	background := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	background.SetMinSize(fyne.NewSize(width, 150))

	content := container.NewStack(background, container.NewPadded(preview))
	go fetchTextPreview(attachment.URL(""), preview)
	return container.NewBorder(nil, bar, nil, nil, content)
}

func buildGenericAttachment(bar fyne.CanvasObject) *fyne.Container {
	width := min(float32(300), theme.Sizes.MessageImageMaxWidth)

	placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	placeholder.SetMinSize(fyne.NewSize(width, 64))

	icon := newIconImage(fynetheme.FileIcon())
	icon.FillMode = canvas.ImageFillContain
	icon.SetMinSize(fyne.NewSize(32, 32))

	return container.NewBorder(nil, bar, nil, nil, container.NewStack(placeholder, container.NewCenter(icon)))
}

// previewCache memoises fetched text-attachment previews by URL: message
// widgets are rebuilt on every channel revisit, and without it each rebuild
// re-downloads every text attachment. Entries are at most ~256 runes.
var (
	previewMu    sync.Mutex
	previewCache = map[string]string{}
)

// fetchTextPreview loads the first few hundred characters of a text attachment
// into preview, formatted as a code block. Fetched once per URL; failures are
// not cached, so they retry on the next rebuild.
func fetchTextPreview(url string, preview *widget.RichText) {
	previewMu.Lock()
	text, ok := previewCache[url]
	previewMu.Unlock()

	if !ok {
		client := http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			return
		}
		defer resp.Body.Close()

		buf := make([]byte, 512)
		n, _ := io.ReadFull(resp.Body, buf)
		if n == 0 {
			return
		}

		runes := []rune(string(buf[:n]))
		if len(runes) > 256 {
			runes = append(runes[:256], []rune("...")...)
		}
		text = string(runes)

		previewMu.Lock()
		previewCache[url] = text
		previewMu.Unlock()
	}

	fyne.CurrentApp().Driver().DoFromGoroutine(func() {
		preview.ParseMarkdown("```\n" + text + "\n```")
		preview.Refresh()
	}, false)
}

// attachmentBar renders a name/size strip. When onRemove is non-nil it also
// shows a close button (used by the message composer).
func attachmentBar(name string, size int, onRemove func()) fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.SwiftActionBg)
	background.SetMinSize(fyne.NewSize(0, 28))

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

// imageDisplaySize scales an image to fit the configured maximum dimensions
// while preserving aspect ratio.
func imageDisplaySize(width, height int) fyne.Size {
	maxW := theme.Sizes.MessageImageMaxWidth
	maxH := theme.Sizes.MessageImageMaxHeight
	if width == 0 || height == 0 {
		return fyne.NewSize(maxW, maxH/2)
	}

	w, h := float32(width), float32(height)
	if w > maxW {
		h *= maxW / w
		w = maxW
	}
	if h > maxH {
		w *= maxH / h
		h = maxH
	}
	return fyne.NewSize(w, h)
}

// buildReplyPreview renders the small quoted line shown above a message that
// replies to another.
func buildReplyPreview(deps Deps, channelID, messageID string) fyne.CanvasObject {
	author, content, avatarURL, _ := resolveReply(deps, channelID, messageID)

	avatar := circularAvatar(deps.Images, avatarURL, fyne.NewSize(16, 16))

	authorLabel := canvas.NewText(author, theme.Colors.TextPrimary)
	authorLabel.TextStyle.Bold = true
	authorLabel.TextSize = 12

	contentLabel := canvas.NewText(content, theme.Colors.TimestampText)
	contentLabel.TextSize = 12

	row := HBoxNoSpacing(
		container.NewCenter(avatar),
		HorizontalSpacer(8),
		container.NewCenter(authorLabel),
		HorizontalSpacer(5),
		container.NewCenter(contentLabel),
	)
	padded := container.NewBorder(VerticalSpacer(3), VerticalSpacer(3), HorizontalSpacer(3), HorizontalSpacer(3), row)

	// TODO: navigate to the referenced message on tap.
	// Indent to the message content column so the quoted line sits directly
	// above the message body rather than under the avatar gutter.
	indent := theme.Sizes.MessageHorizontalPadding + theme.Sizes.MessageAvatarColumnWidth + theme.Sizes.MessageContentPadding
	tappable := NewTappableContainer(padded, func() {})
	return container.NewHBox(HorizontalSpacer(indent), tappable)
}

// resolveReply looks up a referenced message and returns its author, truncated
// content, avatar URL, and the author's role colour (nil when none). Missing
// references yield a placeholder.
func resolveReply(deps Deps, channelID, messageID string) (author, content, avatarURL string, accent color.Color) {
	if deps.Actions == nil {
		return "", "Unknown message reference", "", nil
	}
	msg := deps.Actions.ResolveMessage(channelID, messageID)
	if msg == nil {
		return "", "Unknown message reference", "", nil
	}

	a := util.MessageAuthor(deps.Session, msg)
	author = util.Truncate(a.Name, maxReplyUsernameLength)
	content = util.Truncate(msg.Content, maxReplyPreviewLength)
	return author, content, a.AvatarURL, a.Color
}

// circularAvatar builds a circular avatar of the given size, loading the image
// from avatarURL when present.
func circularAvatar(images *cache.ImageCache, avatarURL string, size fyne.Size) *fyne.Container {
	placeholder := canvas.NewCircle(theme.Colors.ServerDefaultBg)
	avatar := container.NewGridWrap(size, placeholder)

	if avatarURL != "" {
		id := util.IDFromAttachmentURL(avatarURL)
		if id == "" {
			id = avatarURL
		}
		images.LoadIntoContainer(id, avatarURL, size, avatar, true, nil)
	}
	return avatar
}

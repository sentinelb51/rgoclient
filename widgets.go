package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"
)

// getAvatarInfo returns the ID and URL for a user's avatar, or empty strings if none.
func getAvatarInfo(user *revoltgo.User) (id, url string) {
	if user == nil || user.Avatar == nil {
		return "", ""
	}
	return user.Avatar.ID, user.Avatar.URL("64")
}

// getServerIconInfo returns the ID and URL for a server's icon, or empty strings if none.
func getServerIconInfo(server *revoltgo.Server) (id, url string) {
	if server == nil || server.Icon == nil {
		return "", ""
	}
	return server.Icon.ID, server.Icon.URL("64")
}

// Ensure widgets implement necessary interfaces at compile time.
var (
	_ fyne.Widget       = (*MessageWidget)(nil)
	_ desktop.Hoverable = (*MessageWidget)(nil)
	_ fyne.Widget       = (*ChannelWidget)(nil)
	_ fyne.Tappable     = (*ChannelWidget)(nil)
	_ desktop.Hoverable = (*ChannelWidget)(nil)
	_ fyne.Widget       = (*ServerWidget)(nil)
	_ fyne.Tappable     = (*ServerWidget)(nil)
	_ desktop.Hoverable = (*ServerWidget)(nil)
	_ fyne.Widget       = (*CategoryWidget)(nil)
	_ fyne.Tappable     = (*CategoryWidget)(nil)
	_ desktop.Hoverable = (*CategoryWidget)(nil)
)

// MessageWidget displays a chat message with hover effects.
type MessageWidget struct {
	widget.BaseWidget
	content    fyne.CanvasObject
	background *canvas.Rectangle
}

// MessageAttachment holds display data for a message attachment.
type MessageAttachment struct {
	ID     string
	URL    string
	Width  int
	Height int
}

// NewMessageWidget creates a new message widget displaying the author and content.
// If avatarID and avatarURL are provided, it loads the avatar image asynchronously.
func NewMessageWidget(username, message, avatarID, avatarURL string, attachments []MessageAttachment) *MessageWidget {
	pfpSize := fyne.NewSize(AppSizes.AvatarSize, AppSizes.AvatarSize)
	pfp := canvas.NewCircle(AppColors.AvatarPlaceholder)
	pfpWrapper := container.NewGridWrap(pfpSize, pfp)
	pfpContainer := container.NewCenter(pfpWrapper)

	// Load avatar asynchronously if URL is provided (circular, no background needed)
	if avatarURL != "" && avatarID != "" {
		GetImageCache().LoadImageToContainer(avatarID, avatarURL, pfpSize, pfpWrapper, true, nil)
	}

	// Markdown formatted text with bold username
	md := "**" + username + "**  \n\n" + message
	text := widget.NewRichTextFromMarkdown(md)
	text.Wrapping = fyne.TextWrapWord

	// Build the main content with text
	textContent := container.NewBorder(nil, nil, pfpContainer, nil, text)

	var content fyne.CanvasObject
	if len(attachments) > 0 {
		// Create a container for images
		imagesContainer := container.NewVBox()
		for _, att := range attachments {
			imgSize := calculateImageSize(att.Width, att.Height)
			placeholder := canvas.NewRectangle(AppColors.ServerDefaultBg)
			placeholder.SetMinSize(imgSize)
			imgContainer := container.NewGridWrap(imgSize, placeholder)

			// Load image asynchronously
			if att.URL != "" && att.ID != "" {
				GetImageCache().LoadImageToContainer(att.ID, att.URL, imgSize, imgContainer, false, nil)
			}
			imagesContainer.Add(imgContainer)
		}
		// Combine text and images vertically
		content = container.NewVBox(textContent, imagesContainer)
	} else {
		content = textContent
	}

	m := &MessageWidget{
		content:    container.NewPadded(content),
		background: canvas.NewRectangle(color.Transparent),
	}
	m.ExtendBaseWidget(m)
	return m
}

// calculateImageSize calculates the display size for an image, respecting max dimensions.
func calculateImageSize(width, height int) fyne.Size {
	maxWidth := AppSizes.MessageImageMaxWidth
	maxHeight := AppSizes.MessageImageMaxHeight

	if width == 0 || height == 0 {
		return fyne.NewSize(maxWidth, maxHeight/2)
	}

	w := float32(width)
	h := float32(height)

	// Scale down if exceeds max dimensions while preserving aspect ratio
	if w > maxWidth {
		ratio := maxWidth / w
		w = maxWidth
		h = h * ratio
	}
	if h > maxHeight {
		ratio := maxHeight / h
		h = maxHeight
		w = w * ratio
	}

	return fyne.NewSize(w, h)
}

func (m *MessageWidget) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(m.background, m.content))
}

func (m *MessageWidget) MouseIn(*desktop.MouseEvent) {
	m.background.FillColor = AppColors.MessageHoverBackground
	m.background.Refresh()
}

func (m *MessageWidget) MouseMoved(*desktop.MouseEvent) {}

func (m *MessageWidget) MouseOut() {
	m.background.FillColor = color.Transparent
	m.background.Refresh()
}

// ChannelWidget displays a channel in the sidebar with selection state.
type ChannelWidget struct {
	widget.BaseWidget
	channel    *revoltgo.Channel
	onTap      func()
	background *canvas.Rectangle
	selected   bool
}

// NewChannelWidget creates a new channel widget.
func NewChannelWidget(channel *revoltgo.Channel, onTap func()) *ChannelWidget {
	c := &ChannelWidget{
		channel:    channel,
		onTap:      onTap,
		background: canvas.NewRectangle(color.Transparent),
	}
	c.ExtendBaseWidget(c)
	return c
}

// SetSelected updates the selection state and refreshes appearance.
func (c *ChannelWidget) SetSelected(selected bool) {
	c.selected = selected
	c.updateBackground()
}

func (c *ChannelWidget) updateBackground() {
	if c.selected {
		c.background.FillColor = AppColors.ChannelSelectedBg
	} else {
		c.background.FillColor = color.Transparent
	}
	c.background.Refresh()
}

func (c *ChannelWidget) CreateRenderer() fyne.WidgetRenderer {
	// Add left spacer to push hashtag icon more to the right (customizable)
	leftSpacer := canvas.NewRectangle(color.Transparent)
	leftSpacer.SetMinSize(fyne.NewSize(AppSizes.ChannelLeftPadding, 0))
	icon := getHashtagIcon()
	label := widget.NewLabel(c.channel.Name)
	content := container.NewHBox(leftSpacer, icon, label)
	return widget.NewSimpleRenderer(container.NewStack(c.background, content))
}

func (c *ChannelWidget) Tapped(*fyne.PointEvent) {
	if c.onTap != nil {
		c.onTap()
	}
}

func (c *ChannelWidget) MouseIn(*desktop.MouseEvent) {
	if !c.selected {
		c.background.FillColor = AppColors.ChannelHoverBackground
		c.background.Refresh()
	}
}

func (c *ChannelWidget) MouseMoved(*desktop.MouseEvent) {}

func (c *ChannelWidget) MouseOut() {
	c.updateBackground()
}

// ServerWidget displays a server icon with selection and hover states.
type ServerWidget struct {
	widget.BaseWidget
	server        *revoltgo.Server
	onTap         func()
	background    *canvas.Circle
	iconContainer *fyne.Container
	iconWrapper   *fyne.Container
	selected      bool
	hovered       bool
	baseSize      float32
	grownSize     float32
}

// NewServerWidget creates a new server widget.
func NewServerWidget(server *revoltgo.Server, onTap func()) *ServerWidget {
	baseSize := AppSizes.ServerIconSize
	grownSize := baseSize * 1.1 // 10% larger on hover/select

	s := &ServerWidget{
		server:     server,
		onTap:      onTap,
		background: canvas.NewCircle(AppColors.ServerDefaultBg),
		baseSize:   baseSize,
		grownSize:  grownSize,
	}
	s.ExtendBaseWidget(s)
	return s
}

// SetSelected updates the selection state and refreshes appearance.
func (s *ServerWidget) SetSelected(selected bool) {
	s.selected = selected
	s.updateAppearance()
}

func (s *ServerWidget) updateAppearance() {
	// Update background colour
	if s.selected {
		s.background.FillColor = AppColors.ServerSelectedBg
	} else {
		s.background.FillColor = AppColors.ServerDefaultBg
	}
	s.background.Refresh()

	// Update size based on hover/selected state
	s.updateSize()
}

func (s *ServerWidget) updateSize() {
	if s.iconWrapper == nil {
		return
	}

	var newSize float32
	if s.selected || s.hovered {
		newSize = s.grownSize
	} else {
		newSize = s.baseSize
	}

	size := fyne.NewSize(newSize, newSize)
	s.iconWrapper.Layout = container.NewGridWrap(size).Layout
	s.iconWrapper.Refresh()
}

func (s *ServerWidget) CreateRenderer() fyne.WidgetRenderer {
	iconSize := fyne.NewSize(s.baseSize, s.baseSize)

	// Server initial as fallback placeholder
	initial := ""
	if len(s.server.Name) > 0 {
		initial = string(s.server.Name[0])
	}
	initialLabel := canvas.NewText(initial, AppColors.TextPrimary)
	initialLabel.TextStyle = fyne.TextStyle{Bold: true}
	initialLabel.Alignment = fyne.TextAlignCenter

	// Create icon content container - background is always present for hover effects
	s.iconContainer = container.NewStack(s.background, container.NewCenter(initialLabel))

	// Load server icon asynchronously if available
	iconID, iconURL := getServerIconInfo(s.server)
	if iconURL != "" {
		GetImageCache().LoadImageToContainer(iconID, iconURL, iconSize, s.iconContainer, true, s.background)
	}

	s.iconWrapper = container.NewGridWrap(iconSize, s.iconContainer)

	// Centre the icon wrapper for consistent positioning
	centered := container.NewCenter(s.iconWrapper)

	return widget.NewSimpleRenderer(centered)
}

func (s *ServerWidget) Tapped(*fyne.PointEvent) {
	if s.onTap != nil {
		s.onTap()
	}
}

func (s *ServerWidget) MouseIn(*desktop.MouseEvent) {
	s.hovered = true
	s.updateAppearance()
}

func (s *ServerWidget) MouseMoved(*desktop.MouseEvent) {}

func (s *ServerWidget) MouseOut() {
	s.hovered = false
	s.updateAppearance()
}

// hashtagIcon caches the hashtag icon to avoid recreating it for each channel.
// NOTE: We can't share a single instance since Fyne widgets can only be in one container.
// Instead, we create a new icon each time but keep the function simple.

// getHashtagIcon returns a new hashtag (#) icon for channel display.
func getHashtagIcon() fyne.CanvasObject {
	col := AppColors.HashtagIcon
	size := AppSizes.HashtagIconSize

	// Scale factor based on icon size (designed for size 20)
	scale := size / 20

	// Draw hashtag centered within bounds
	// Vertical lines
	v1 := canvas.NewLine(col)
	v1.Position1 = fyne.NewPos(7*scale, 2*scale)
	v1.Position2 = fyne.NewPos(7*scale, 18*scale)
	v1.StrokeWidth = 2 * scale

	v2 := canvas.NewLine(col)
	v2.Position1 = fyne.NewPos(13*scale, 2*scale)
	v2.Position2 = fyne.NewPos(13*scale, 18*scale)
	v2.StrokeWidth = 2 * scale

	// Horizontal lines
	h1 := canvas.NewLine(col)
	h1.Position1 = fyne.NewPos(2*scale, 7*scale)
	h1.Position2 = fyne.NewPos(18*scale, 7*scale)
	h1.StrokeWidth = 2 * scale

	h2 := canvas.NewLine(col)
	h2.Position1 = fyne.NewPos(2*scale, 13*scale)
	h2.Position2 = fyne.NewPos(18*scale, 13*scale)
	h2.StrokeWidth = 2 * scale

	icon := container.NewWithoutLayout(v1, v2, h1, h2)
	wrapper := container.NewGridWrap(fyne.NewSize(size, size), icon)
	return container.NewCenter(wrapper)
}

// CategoryWidget displays a collapsible category header in the channel sidebar.
type CategoryWidget struct {
	widget.BaseWidget
	title              string
	collapsed          bool
	indicatorContainer *fyne.Container
	background         *canvas.Rectangle
	onToggle           func(collapsed bool)
	channelWidgets     []fyne.CanvasObject
	channelContainer   *fyne.Container
	isFirstCategory    bool
}

// MinSize returns the minimum size for the category widget, including top spacing.
func (c *CategoryWidget) MinSize() fyne.Size {
	height := AppSizes.CategoryHeight
	if !c.isFirstCategory {
		height += AppSizes.CategorySpacing
	}
	return fyne.NewSize(0, height)
}

// SetIsFirstCategory sets whether this is the first category (no top spacing).
func (c *CategoryWidget) SetIsFirstCategory(isFirst bool) {
	c.isFirstCategory = isFirst
}

// getCategoryExpandedIndicator returns a drawn minus sign (-) for expanded state.
// The minus is drawn centered vertically to align with the plus sign.
func getCategoryExpandedIndicator() fyne.CanvasObject {
	size := AppSizes.CategoryIndicatorSize
	strokeWidth := AppSizes.CategoryIndicatorStroke
	col := AppColors.CategoryIndicator
	padding := float32(3)

	// Horizontal line (minus) - centered both horizontally and vertically
	h := canvas.NewLine(col)
	h.Position1 = fyne.NewPos(padding, size/2)
	h.Position2 = fyne.NewPos(size-padding, size/2)
	h.StrokeWidth = strokeWidth

	icon := container.NewWithoutLayout(h)
	wrapper := container.NewGridWrap(fyne.NewSize(size, size), icon)
	return container.NewCenter(wrapper)
}

// getCategoryCollapsedIndicator returns a drawn plus sign (+) for collapsed state.
func getCategoryCollapsedIndicator() fyne.CanvasObject {
	size := AppSizes.CategoryIndicatorSize
	strokeWidth := AppSizes.CategoryIndicatorStroke
	col := AppColors.CategoryIndicator
	padding := float32(3)

	// Horizontal line
	h := canvas.NewLine(col)
	h.Position1 = fyne.NewPos(padding, size/2)
	h.Position2 = fyne.NewPos(size-padding, size/2)
	h.StrokeWidth = strokeWidth

	// Vertical line
	v := canvas.NewLine(col)
	v.Position1 = fyne.NewPos(size/2, padding)
	v.Position2 = fyne.NewPos(size/2, size-padding)
	v.StrokeWidth = strokeWidth

	icon := container.NewWithoutLayout(h, v)
	wrapper := container.NewGridWrap(fyne.NewSize(size, size), icon)
	return container.NewCenter(wrapper)
}

// NewCategoryWidget creates a new category widget with the given title.
func NewCategoryWidget(title string, onToggle func(collapsed bool)) *CategoryWidget {
	c := &CategoryWidget{
		title:              title,
		collapsed:          false,
		indicatorContainer: container.NewCenter(getCategoryExpandedIndicator()),
		background:         canvas.NewRectangle(color.Transparent),
		onToggle:           onToggle,
		isFirstCategory:    false,
	}
	c.ExtendBaseWidget(c)
	return c
}

// SetCollapsed updates the collapsed state.
func (c *CategoryWidget) SetCollapsed(collapsed bool) {
	c.collapsed = collapsed
	c.updateArrow()
	c.updateChannelVisibility()
}

// IsCollapsed returns the current collapsed state.
func (c *CategoryWidget) IsCollapsed() bool {
	return c.collapsed
}

// SetChannelWidgets sets the channel widgets that belong to this category.
func (c *CategoryWidget) SetChannelWidgets(widgets []fyne.CanvasObject, container *fyne.Container) {
	c.channelWidgets = widgets
	c.channelContainer = container
}

func (c *CategoryWidget) updateArrow() {
	c.indicatorContainer.RemoveAll()
	if c.collapsed {
		c.indicatorContainer.Add(getCategoryCollapsedIndicator())
	} else {
		c.indicatorContainer.Add(getCategoryExpandedIndicator())
	}
	c.indicatorContainer.Refresh()
}

func (c *CategoryWidget) updateChannelVisibility() {
	for _, w := range c.channelWidgets {
		if c.collapsed {
			w.Hide()
		} else {
			w.Show()
		}
	}
	if c.channelContainer != nil {
		c.channelContainer.Refresh()
	}
}

func (c *CategoryWidget) CreateRenderer() fyne.WidgetRenderer {
	titleLabel := canvas.NewText(c.title, AppColors.CategoryText)
	titleLabel.TextStyle = fyne.TextStyle{Bold: true}
	titleLabel.TextSize = 13

	// Add a small spacer to the right of the indicator to push it slightly left from the edge
	rightSpacer := canvas.NewRectangle(color.Transparent)
	rightSpacer.SetMinSize(fyne.NewSize(8, 0))
	indicatorWithSpacer := container.NewHBox(c.indicatorContainer, rightSpacer)

	content := container.NewBorder(nil, nil, titleLabel, indicatorWithSpacer, nil)
	padded := container.NewPadded(content)
	inner := container.NewStack(c.background, padded)

	return &categoryRenderer{
		widget:  c,
		inner:   inner,
		objects: []fyne.CanvasObject{inner},
	}
}

func (c *CategoryWidget) Tapped(*fyne.PointEvent) {
	c.collapsed = !c.collapsed
	c.updateArrow()
	c.updateChannelVisibility()
	if c.onToggle != nil {
		c.onToggle(c.collapsed)
	}
}

func (c *CategoryWidget) MouseIn(*desktop.MouseEvent) {
	c.background.FillColor = AppColors.ChannelHoverBackground
	c.background.Refresh()
}

func (c *CategoryWidget) MouseMoved(*desktop.MouseEvent) {}

func (c *CategoryWidget) MouseOut() {
	c.background.FillColor = color.Transparent
	c.background.Refresh()
}

// categoryRenderer handles layout for CategoryWidget with optional top margin.
type categoryRenderer struct {
	widget  *CategoryWidget
	inner   *fyne.Container
	objects []fyne.CanvasObject
}

func (r *categoryRenderer) Layout(size fyne.Size) {
	topMargin := float32(0)
	if !r.widget.isFirstCategory {
		topMargin = AppSizes.CategorySpacing
	}
	innerHeight := size.Height - topMargin
	r.inner.Move(fyne.NewPos(0, topMargin))
	r.inner.Resize(fyne.NewSize(size.Width, innerHeight))
}

func (r *categoryRenderer) MinSize() fyne.Size {
	return r.widget.MinSize()
}

func (r *categoryRenderer) Refresh() {
	r.inner.Refresh()
}

func (r *categoryRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *categoryRenderer) Destroy() {}

// TappableContainer wraps a container to make it tappable with hover effects.
type TappableContainer struct {
	widget.BaseWidget
	content    fyne.CanvasObject
	background *canvas.Rectangle
	onTap      func()
	hovered    bool
}

// Ensure TappableContainer implements necessary interfaces.
var (
	_ fyne.Widget       = (*TappableContainer)(nil)
	_ fyne.Tappable     = (*TappableContainer)(nil)
	_ desktop.Hoverable = (*TappableContainer)(nil)
)

// NewTappableContainer creates a new tappable container with the given content and tap handler.
func NewTappableContainer(content fyne.CanvasObject, onTap func()) *TappableContainer {
	t := &TappableContainer{
		content:    content,
		background: canvas.NewRectangle(color.Transparent),
		onTap:      onTap,
	}
	t.ExtendBaseWidget(t)
	return t
}

func (t *TappableContainer) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(t.background, t.content))
}

func (t *TappableContainer) Tapped(*fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap()
	}
}

func (t *TappableContainer) MouseIn(*desktop.MouseEvent) {
	t.hovered = true
	t.background.FillColor = color.RGBA{R: 70, G: 70, B: 70, A: 255}
	t.background.Refresh()
}

func (t *TappableContainer) MouseMoved(*desktop.MouseEvent) {}

func (t *TappableContainer) MouseOut() {
	t.hovered = false
	t.background.FillColor = color.Transparent
	t.background.Refresh()
}

// XButton is a simple drawn X button for removing items.
type XButton struct {
	widget.BaseWidget
	onTap   func()
	hovered bool
}

// Ensure XButton implements necessary interfaces.
var (
	_ fyne.Widget       = (*XButton)(nil)
	_ fyne.Tappable     = (*XButton)(nil)
	_ desktop.Hoverable = (*XButton)(nil)
)

// NewXButton creates a new X button with the given tap handler.
func NewXButton(onTap func()) *XButton {
	x := &XButton{onTap: onTap}
	x.ExtendBaseWidget(x)
	return x
}

func (x *XButton) CreateRenderer() fyne.WidgetRenderer {
	return &xButtonRenderer{button: x}
}

func (x *XButton) Tapped(*fyne.PointEvent) {
	if x.onTap != nil {
		x.onTap()
	}
}

func (x *XButton) MouseIn(*desktop.MouseEvent) {
	x.hovered = true
	x.Refresh()
}

func (x *XButton) MouseMoved(*desktop.MouseEvent) {}

func (x *XButton) MouseOut() {
	x.hovered = false
	x.Refresh()
}

func (x *XButton) MinSize() fyne.Size {
	return fyne.NewSize(24, 24)
}

type xButtonRenderer struct {
	button *XButton
}

func (r *xButtonRenderer) Layout(size fyne.Size) {}

func (r *xButtonRenderer) MinSize() fyne.Size {
	return r.button.MinSize()
}

func (r *xButtonRenderer) Refresh() {}

func (r *xButtonRenderer) Objects() []fyne.CanvasObject {
	size := r.button.MinSize()
	padding := float32(6)

	// Draw X lines
	lineColor := color.RGBA{R: 150, G: 150, B: 150, A: 255}
	if r.button.hovered {
		lineColor = color.RGBA{R: 255, G: 100, B: 100, A: 255}
	}

	line1 := canvas.NewLine(lineColor)
	line1.StrokeWidth = 2
	line1.Position1 = fyne.NewPos(padding, padding)
	line1.Position2 = fyne.NewPos(size.Width-padding, size.Height-padding)

	line2 := canvas.NewLine(lineColor)
	line2.StrokeWidth = 2
	line2.Position1 = fyne.NewPos(size.Width-padding, padding)
	line2.Position2 = fyne.NewPos(padding, size.Height-padding)

	return []fyne.CanvasObject{line1, line2}
}

func (r *xButtonRenderer) Destroy() {}

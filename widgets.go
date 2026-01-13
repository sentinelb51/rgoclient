package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// MessageWidget is a custom widget that supports hover effects and displays a message
type MessageWidget struct {
	widget.BaseWidget
	content    fyne.CanvasObject
	background *canvas.Rectangle
}

func NewMessageWidget(username, message, avatarURL string) *MessageWidget {
	// PFP - In future use avatarURL
	pfp := canvas.NewCircle(color.RGBA{R: 100, G: 100, B: 200, A: 255})
	// Enforce size using GridWrap
	pfpWrapper := container.NewGridWrap(fyne.NewSize(40, 40), pfp)

	// Wrap pfp in a Centre container so it stays in the middle vertically
	pfpContainer := container.NewCenter(pfpWrapper)

	// Use Markdown to format the username (bold) and message in a single block.
	md := "**" + username + "**  \n\n" + message
	text := widget.NewRichTextFromMarkdown(md)
	text.Wrapping = fyne.TextWrapWord

	content := container.NewBorder(nil, nil, pfpContainer, nil, text)

	m := &MessageWidget{
		content:    container.NewPadded(content),
		background: canvas.NewRectangle(color.Transparent),
	}
	m.ExtendBaseWidget(m)
	return m
}

func (m *MessageWidget) CreateRenderer() fyne.WidgetRenderer {
	// SimpleRenderer layouts all objects to fill the space, effectively stacking them.
	return widget.NewSimpleRenderer(container.NewStack(m.background, m.content))
}

func (m *MessageWidget) MouseIn(*desktop.MouseEvent) {
	m.background.FillColor = color.RGBA{R: 45, G: 45, B: 45, A: 255}
	m.background.Refresh()
}

func (m *MessageWidget) MouseMoved(*desktop.MouseEvent) {
}

func (m *MessageWidget) MouseOut() {
	m.background.FillColor = color.Transparent
	m.background.Refresh()
}

// ChannelWidget represents a channel in the sidebar
type ChannelWidget struct {
	widget.BaseWidget
	channel    *Channel
	onTap      func()
	background *canvas.Rectangle
	selected   bool
}

func NewChannelWidget(channel *Channel, onTap func()) *ChannelWidget {
	c := &ChannelWidget{
		channel:    channel,
		onTap:      onTap,
		background: canvas.NewRectangle(color.Transparent),
	}
	c.ExtendBaseWidget(c)
	return c
}

func (c *ChannelWidget) SetSelected(selected bool) {
	c.selected = selected
	c.updateBackground()
}

func (c *ChannelWidget) updateBackground() {
	if c.selected {
		c.background.FillColor = color.RGBA{R: 80, G: 80, B: 80, A: 255}
	} else {
		c.background.FillColor = color.Transparent
	}
	c.background.Refresh()
}

func (c *ChannelWidget) CreateRenderer() fyne.WidgetRenderer {
	icon := createHashtagIcon()
	label := widget.NewLabel(c.channel.Name)

	// Content with icon and label
	// Wrap icon in Center to align it with text if heights differ
	content := container.NewHBox(container.NewCenter(icon), label)

	return widget.NewSimpleRenderer(container.NewStack(c.background, content))
}

func (c *ChannelWidget) Tapped(*fyne.PointEvent) {
	if c.onTap != nil {
		c.onTap()
	}
}

func (c *ChannelWidget) MouseIn(*desktop.MouseEvent) {
	if !c.selected {
		c.background.FillColor = color.RGBA{R: 60, G: 60, B: 60, A: 255}
		c.background.Refresh()
	}
}

func (c *ChannelWidget) MouseMoved(*desktop.MouseEvent) {
}

func (c *ChannelWidget) MouseOut() {
	c.updateBackground()
}

// ServerWidget represents a server icon in the sidebar
type ServerWidget struct {
	widget.BaseWidget
	server     *Server
	onTap      func()
	background *canvas.Circle // Using circle for server icon background
	indicator  *canvas.Circle // Hover/Active indicator
}

func NewServerWidget(server *Server, onTap func()) *ServerWidget {
	s := &ServerWidget{
		server:     server,
		onTap:      onTap,
		background: canvas.NewCircle(color.RGBA{R: 60, G: 60, B: 60, A: 255}),
		indicator:  canvas.NewCircle(color.Transparent),
	}
	s.ExtendBaseWidget(s)
	return s
}

func (s *ServerWidget) CreateRenderer() fyne.WidgetRenderer {
	// 30x30 icon size
	size := float32(40) // slightly larger to look good in 60px sidebar

	// Initials for the server name (basic placeholder for image)
	initial := ""
	if len(s.server.Name) > 0 {
		initial = string(s.server.Name[0])
	}
	label := canvas.NewText(initial, color.White)
	label.TextStyle = fyne.TextStyle{Bold: true}
	label.Alignment = fyne.TextAlignCenter

	// Centre the label over the background circle
	iconContent := container.NewStack(s.background, container.NewCenter(label))

	// Enforce size
	iconWrapper := container.NewGridWrap(fyne.NewSize(size, size), iconContent)

	return widget.NewSimpleRenderer(iconWrapper)
}

func (s *ServerWidget) Tapped(*fyne.PointEvent) {
	if s.onTap != nil {
		s.onTap()
	}
}

func (s *ServerWidget) MouseIn(*desktop.MouseEvent) {
	// Hover effect: lighter gray and change shape slightly if we could, but here just color
	s.background.FillColor = color.RGBA{R: 114, G: 137, B: 218, A: 255} // blurryple-ish
	s.background.Refresh()

	// Basic tooltip hack: we can't easily pop a tooltip here without custom overlay logic,
	// but the visual feedback satisfies "hovered over".
}

func (s *ServerWidget) MouseMoved(*desktop.MouseEvent) {
}

func (s *ServerWidget) MouseOut() {
	s.background.FillColor = color.RGBA{R: 60, G: 60, B: 60, A: 255}
	s.background.Refresh()
}

func createHashtagIcon() fyne.CanvasObject {
	// Create a 3x3 line grid icon (tic-tac-toe style)
	col := color.RGBA{R: 180, G: 180, B: 180, A: 255}
	iconSize := float32(24)

	// Vertical lines
	v1 := canvas.NewLine(col)
	v1.Position1 = fyne.NewPos(9, 3)
	v1.Position2 = fyne.NewPos(9, 21)
	v1.StrokeWidth = 2

	v2 := canvas.NewLine(col)
	v2.Position1 = fyne.NewPos(15, 3)
	v2.Position2 = fyne.NewPos(15, 21)
	v2.StrokeWidth = 2

	// Horizontal lines
	h1 := canvas.NewLine(col)
	h1.Position1 = fyne.NewPos(3, 9)
	h1.Position2 = fyne.NewPos(21, 9)
	h1.StrokeWidth = 2

	h2 := canvas.NewLine(col)
	h2.Position1 = fyne.NewPos(3, 15)
	h2.Position2 = fyne.NewPos(21, 15)
	h2.StrokeWidth = 2

	// Use WithoutLayout to keep absolute positions of lines
	icon := container.NewWithoutLayout(v1, v2, h1, h2)

	// Enforce size using GridWrap on the icon directly, avoiding extra spacer
	return container.NewPadded(container.NewGridWrap(fyne.NewSize(iconSize, iconSize), icon))
}

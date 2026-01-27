package input

import (
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	appTheme "RGOClient/internal/ui/theme"
	"RGOClient/internal/ui/widgets"
	"RGOClient/internal/util"
)

// Attachment represents a file attached to the message.
type Attachment struct {
	Path string
	Name string
}

// AddAttachment adds a file to the attachment list and updates the UI.
func (m *MessageInput) AddAttachment(path string) {
	name := filepath.Base(path)
	att := Attachment{Path: path, Name: name}
	m.Attachments = append(m.Attachments, att)
	m.rebuildAttachmentUI()
}

// RemoveAttachment removes a file from the attachment list.
func (m *MessageInput) RemoveAttachment(path string) {
	for i, a := range m.Attachments {
		if a.Path == path {
			m.Attachments = append(m.Attachments[:i], m.Attachments[i+1:]...)
			m.rebuildAttachmentUI()
			return
		}
	}
}

// ClearAttachments clears all attachments.
func (m *MessageInput) ClearAttachments() {
	m.Attachments = []Attachment{}
	m.AttachmentContainer.Objects = nil
	m.AttachmentContainer.Refresh()
}

// rebuildAttachmentUI rebuilds the attachment UI.
func (m *MessageInput) rebuildAttachmentUI() {
	m.AttachmentContainer.Objects = nil
	for _, att := range m.Attachments {
		size := 0
		if info, err := os.Stat(att.Path); err == nil {
			size = int(info.Size())
		}

		capturedPath := att.Path
		onRemove := func() {
			m.RemoveAttachment(capturedPath)
		}

		preview := m.createAttachmentPreview(att.Path)
		bar := m.createAttachmentMetadataBar(att.Name, size, onRemove)

		main := container.NewBorder(nil, bar, nil, nil, preview)

		bg := canvas.NewRectangle(appTheme.Colors.ServerDefaultBg)
		bg.CornerRadius = 8
		card := container.NewStack(bg, container.NewPadded(main))

		m.AttachmentContainer.Add(container.NewPadded(card))
	}
	m.AttachmentContainer.Refresh()
	m.Refresh()
}

func (m *MessageInput) createAttachmentMetadataBar(name string, size int, onRemove func()) fyne.CanvasObject {
	barBg := canvas.NewRectangle(appTheme.Colors.SwiftActionBg)
	barBg.SetMinSize(fyne.NewSize(0, attBarHeight))

	nameLabel := canvas.NewText(name, appTheme.Colors.TextPrimary)
	nameLabel.TextSize = attNameTextSize
	nameLabel.TextStyle = fyne.TextStyle{Bold: true}
	nameLabel.Alignment = fyne.TextAlignLeading

	sizeLabel := canvas.NewText(widgets.FormatFileSize(size), appTheme.Colors.TimestampText)
	sizeLabel.TextSize = attSizeTextSize
	sizeLabel.Alignment = fyne.TextAlignTrailing

	closeBtn := widgets.NewCloseButton(onRemove)

	barContent := container.NewBorder(nil, nil,
		container.NewHBox(widgets.HorizontalSpacer(attSpacerSize), nameLabel),
		container.NewHBox(sizeLabel, container.NewPadded(closeBtn), widgets.HorizontalSpacer(attSpacerSize)),
	)

	return container.NewStack(barBg, barContent)
}

func (m *MessageInput) createAttachmentPreview(path string) fyne.CanvasObject {
	if util.Filetype(path) == util.FileTypeImage {
		return m.createImagePreview(path)
	}
	return m.createGenericPreview()
}

func (m *MessageInput) createImagePreview(path string) fyne.CanvasObject {
	img := canvas.NewImageFromFile(path)
	img.FillMode = canvas.ImageFillContain
	img.ScaleMode = canvas.ImageScaleFastest
	img.SetMinSize(fyne.NewSize(attPreviewWidth, attPreviewImgHeight))
	return img
}

func (m *MessageInput) createGenericPreview() fyne.CanvasObject {
	placeholder := canvas.NewRectangle(appTheme.Colors.ServerDefaultBg)
	placeholder.SetMinSize(fyne.NewSize(attPreviewWidth, attPreviewFileHeight))
	return placeholder
}

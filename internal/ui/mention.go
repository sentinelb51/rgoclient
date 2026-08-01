package ui

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
	// mentionMaxRows bounds both the picker's height and its per-keystroke work:
	// filtering stops counting matches past this and the surplus is reported as a
	// "+N more" hint instead of a scrolling list. A picker you have to scroll is
	// slower to use than one that tells you to keep typing.
	mentionMaxRows = 8

	// mentionNameMaxRunes keeps one very long display name from stretching a row
	// wider than the composer. The picker's rows are packed left, so unlike a
	// sidebar row there is no column to ellipsise against.
	mentionNameMaxRunes = 32

	mentionRowInset = 8 // left/right breathing room inside a row
	mentionRowGap   = 8 // between avatar, name and handle
)

// MentionCandidate is one person the mention picker can insert. Name and
// Username are both matched against what the user has typed, so either the
// nickname shown in chat or the underlying @handle finds someone.
type MentionCandidate struct {
	UserID    string
	Name      string // nickname / display name, as chat shows it
	Username  string // the @handle, without the @
	AvatarURL string
	Color     color.Color // role colour; nil falls back to the standard text colour

	// Lowercased match keys, computed once when the candidate is built. The
	// picker filters the whole candidate set on every keystroke, so folding case
	// here rather than per keystroke is what keeps a 2000-member server cheap.
	nameKey, userKey string
}

// NewMentionCandidate builds a candidate with its match keys precomputed.
func NewMentionCandidate(userID, name, username, avatarURL string, roleColor color.Color) MentionCandidate {
	return MentionCandidate{
		UserID:    userID,
		Name:      name,
		Username:  username,
		AvatarURL: avatarURL,
		Color:     roleColor,
		nameKey:   strings.ToLower(name),
		userKey:   strings.ToLower(username),
	}
}

// rank scores a candidate against an already-lowercased query: 0 when the
// display name or handle starts with it, 1 when either merely contains it, -1
// for no match. An empty query (the bare "@") matches everyone at rank 0, so
// typing @ alone opens the picker on the full list.
func (c MentionCandidate) rank(query string) int {
	switch {
	case query == "",
		strings.HasPrefix(c.nameKey, query), strings.HasPrefix(c.userKey, query):
		return 0
	case strings.Contains(c.nameKey, query), strings.Contains(c.userKey, query):
		return 1
	}
	return -1
}

// MentionPicker is the autocomplete list the composer shows while an @mention is
// being typed. It lives inside the composer card rather than floating over the
// message area: a Fyne pop-up takes canvas focus, which would pull it away from
// the entry and stop the typing that drives the picker in the first place.
//
// Its row widgets are pooled — mentionMaxRows of them, built once and re-set as
// the query changes — so a keystroke re-labels existing widgets instead of
// building and discarding a list of new ones.
type MentionPicker struct {
	widget.BaseWidget
	images   *cache.ImageCache
	onAccept func(MentionCandidate)

	all      []MentionCandidate
	matches  []MentionCandidate
	overflow int // matches beyond mentionMaxRows, reported by the footer
	selected int

	rows      []*mentionRow
	footer    *canvas.Text
	footerRow *fyne.Container // the footer's padded wrapper, shown/hidden as a unit
	content   fyne.CanvasObject
}

var _ fyne.Widget = (*MentionPicker)(nil)

// NewMentionPicker builds an empty, hidden picker. onAccept receives the chosen
// candidate; the composer turns it into a mention token.
func NewMentionPicker(images *cache.ImageCache, onAccept func(MentionCandidate)) *MentionPicker {
	p := &MentionPicker{images: images, onAccept: onAccept}

	rowBox := VBoxNoSpacing()
	for i := range mentionMaxRows {
		row := newMentionRow(images, func() { p.selectRow(i) }, func() { p.selectRow(i); p.Accept() })
		row.Hide()
		p.rows = append(p.rows, row)
		rowBox.Add(row)
	}

	p.footer = canvas.NewText("", theme.Colors.MentionHandleText)
	p.footer.TextSize = theme.Sizes.MentionHandleSize
	p.footerRow = NewInset(p.footer, 2, 4, mentionRowInset, mentionRowInset)
	p.footerRow.Hide()

	rule := canvas.NewRectangle(theme.Colors.DaySeparatorLine)
	rule.SetMinSize(fyne.NewSize(0, theme.Sizes.DaySeparatorThickness))

	p.content = VBoxNoSpacing(rowBox, p.footerRow, rule)
	p.Hide()
	p.ExtendBaseWidget(p)
	return p
}

func (p *MentionPicker) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(p.content)
}

// SetCandidates replaces the pool the picker filters. Call it when the open
// channel changes or its membership does; the picker snapshots this list and
// never goes to the network itself.
func (p *MentionPicker) SetCandidates(candidates []MentionCandidate) {
	p.all = candidates
}

// Update refilters against query — the text between the "@" and the caret — and
// reports whether anything matched. A false result means the caller should hide
// the picker: there is nobody to offer.
func (p *MentionPicker) Update(query string) bool {
	p.filter(strings.ToLower(query))
	if len(p.matches) == 0 {
		return false
	}

	p.selected = min(p.selected, len(p.matches)-1)
	for i, row := range p.rows {
		if i >= len(p.matches) {
			row.Hide()
			continue
		}
		row.set(p.matches[i], i == p.selected)
		row.Show()
	}

	if p.overflow > 0 {
		p.footer.Text = fmt.Sprintf("+%d more — keep typing", p.overflow)
		p.footer.Refresh()
		p.footerRow.Show()
	} else {
		p.footerRow.Hide()
	}

	p.Refresh()
	return true
}

// filter collects the best mentionMaxRows matches, prefix hits before substring
// hits. Two passes over the candidates beat one pass plus a sort: the set is
// walked at most twice with a string comparison per entry and nothing is
// allocated, which is what lets this run on every keystroke.
func (p *MentionPicker) filter(query string) {
	p.matches, p.overflow = p.matches[:0], 0
	for pass := range 2 {
		for _, candidate := range p.all {
			if candidate.rank(query) != pass {
				continue
			}
			if len(p.matches) < mentionMaxRows {
				p.matches = append(p.matches, candidate)
			} else {
				p.overflow++
			}
		}
	}
}

// Step moves the highlight by delta, wrapping at both ends so Up from the first
// row lands on the last. Named Step rather than Move because a fyne.Widget's
// Move is the one that positions it on the canvas.
func (p *MentionPicker) Step(delta int) {
	if len(p.matches) == 0 {
		return
	}
	n := len(p.matches)
	p.selectRow(((p.selected+delta)%n + n) % n)
}

// selectRow highlights row i, repainting only the two rows that changed.
func (p *MentionPicker) selectRow(i int) {
	if i == p.selected || i >= len(p.matches) {
		return
	}
	p.rows[p.selected].setSelected(false)
	p.selected = i
	p.rows[i].setSelected(true)
}

// Accept hands the highlighted candidate to the composer.
func (p *MentionPicker) Accept() {
	if p.selected < len(p.matches) && p.onAccept != nil {
		p.onAccept(p.matches[p.selected])
	}
}

// Reset clears the highlight so the next mention starts at the top of its list.
func (p *MentionPicker) Reset() {
	if p.selected != 0 && p.selected < len(p.rows) {
		p.rows[p.selected].setSelected(false)
	}
	p.selected = 0
}

// mentionRow is one pooled row of the picker: avatar, display name in the
// author's role colour, and the dim @handle.
type mentionRow struct {
	tapBase
	images *cache.ImageCache

	background  *canvas.Rectangle
	avatar      *fyne.Container
	placeholder *canvas.Circle
	name        *canvas.Text
	handle      *canvas.Text
	content     fyne.CanvasObject

	// generation guards a reused row against a slow avatar load: by the time an
	// image arrives the row may already show somebody else.
	generation int

	onHover func()
}

var (
	_ fyne.Tappable     = (*mentionRow)(nil)
	_ desktop.Hoverable = (*mentionRow)(nil)
)

func newMentionRow(images *cache.ImageCache, onHover, onTap func()) *mentionRow {
	size := fyne.NewSize(theme.Sizes.MentionAvatarSize, theme.Sizes.MentionAvatarSize)

	r := &mentionRow{
		images:      images,
		background:  canvas.NewRectangle(color.Transparent),
		placeholder: canvas.NewCircle(theme.Colors.AvatarPlaceholder),
		name:        canvas.NewText("", theme.Colors.TextPrimary),
		handle:      canvas.NewText("", theme.Colors.MentionHandleText),
		onHover:     onHover,
	}
	r.avatar = container.NewGridWrap(size, r.placeholder)
	r.name.TextSize = theme.Sizes.MentionNameSize
	r.name.TextStyle = fyne.TextStyle{Bold: true}
	r.handle.TextSize = theme.Sizes.MentionHandleSize

	// As on the reply card, container.NewCenter is what vertically centres each
	// element inside the row's full height; HBoxNoSpacing keeps the horizontal
	// gaps explicit rather than inheriting theme padding.
	row := HBoxNoSpacing(
		HorizontalSpacer(mentionRowInset),
		container.NewCenter(r.avatar),
		HorizontalSpacer(mentionRowGap),
		container.NewCenter(r.name),
		HorizontalSpacer(mentionRowGap),
		container.NewCenter(r.handle),
	)
	r.content = container.NewStack(r.background, row)

	r.onTap = onTap
	r.ExtendBaseWidget(r)
	return r
}

func (r *mentionRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.content)
}

func (r *mentionRow) MinSize() fyne.Size {
	return fyne.NewSize(0, theme.Sizes.MentionRowHeight)
}

func (r *mentionRow) MouseIn(*desktop.MouseEvent) {
	if r.onHover != nil {
		r.onHover()
	}
}

func (r *mentionRow) MouseOut() {}

// set re-labels the row for a candidate. Only the avatar can be slow, and it is
// fetched through the shared cache, so a row that scrolls past under a fast
// typist costs one map lookup.
func (r *mentionRow) set(candidate MentionCandidate, selected bool) {
	r.generation++
	generation := r.generation

	r.name.Text = util.Truncate(candidate.Name, mentionNameMaxRunes)
	r.name.Color = theme.Colors.TextPrimary
	if candidate.Color != nil {
		r.name.Color = candidate.Color
	}
	r.handle.Text = "@" + candidate.Username
	r.name.Refresh()
	r.handle.Refresh()
	r.setSelected(selected)

	// Back to the placeholder first: the row may be showing the previous
	// candidate's face, and this one may have no avatar at all.
	r.avatar.Objects = []fyne.CanvasObject{r.placeholder}
	r.avatar.Refresh()
	if candidate.AvatarURL == "" || r.images == nil {
		return
	}

	id := util.IDFromAttachmentURL(candidate.AvatarURL)
	if id == "" {
		id = candidate.AvatarURL
	}
	size := fyne.NewSize(theme.Sizes.MentionAvatarSize, theme.Sizes.MentionAvatarSize)
	r.images.LoadAsync(id, candidate.AvatarURL, true, func(img image.Image) {
		if r.generation != generation {
			return
		}
		face := canvas.NewImageFromImage(img)
		face.FillMode = canvas.ImageFillContain
		face.SetMinSize(size)
		r.avatar.Objects = []fyne.CanvasObject{face}
		r.avatar.Refresh()
	})
}

func (r *mentionRow) setSelected(selected bool) {
	r.background.FillColor = color.Transparent
	if selected {
		r.background.FillColor = theme.Colors.MentionRowSelectedBg
	}
	r.background.Refresh()
}

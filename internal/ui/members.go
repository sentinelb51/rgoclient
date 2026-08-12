package ui

// The member sidebar's list. A server can hold thousands of members whose
// presence changes continuously, and everything in this file follows from those
// two numbers:
//
//   - The model is flat and each of its two kinds of entry is one fixed height,
//     so an entry's position is a prefix sum and the visible range is two binary
//     searches. Nothing here is ever measured.
//   - Only what the viewport shows is mounted, and those widgets are recycled —
//     the same MemberRow draws a different person as it scrolls past. SetMember
//     no-ops on unchanged state, which is what makes an overlapping scroll
//     window and a whole-model repaint alike cost nothing per row that did not
//     move.
//   - A picture arriving for a row that has since been recycled is dropped by a
//     generation counter, which is the one thing recycling cannot do without.

import (
	"fmt"
	"image"
	"image/color"
	"sort"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
)

/* The model */

// MemberEntryKind is the two things the list holds. Each has one fixed height,
// which is what lets a position be arithmetic rather than a measurement.
type MemberEntryKind uint8

const (
	MemberEntrySection MemberEntryKind = iota
	MemberEntryRow
)

// MemberEntry is one line of the list: a section header, or a member.
type MemberEntry struct {
	Kind MemberEntryKind

	Title  string        // section only, e.g. "Moderators — 12"
	Member domain.Member // row only
}

// MemberListOptions is what the settings decide about the shape of the list.
type MemberListOptions struct {
	GroupByPresence bool
	HoistRoles      bool
	HideOffline     bool
	HideRoleless    bool

	// FallbackToAll draws everybody when the two hiding settings between them
	// have left nothing at all — see NewMemberModel.
	FallbackToAll bool
}

// NewMemberModel flattens members into the order the list draws them: each
// hoisted role a section of its own in rank order, then Online, then Offline.
//
// It takes members in the order Store.Members handed them back and never
// re-orders within a bucket — that walk has already resolved and sorted every
// name, and doing it again here would be the expensive half of a rebuild done
// twice. Bucketing is stable, so each section comes out in the store's order,
// which is also what makes turning the sort off actually save anything.
//
// An **offline member never appears in their role's section**, which is Revolt's
// own rule and the one most easily got wrong: a hoisted section is a list of who
// is *here*.
//
// It reads no theme sizes, so it is safe to call off the UI thread — heights are
// applied when the model is installed.
//
// A server whose members are all hidden by the settings comes back empty, and an
// empty sidebar reads exactly like one whose fetch failed — the settings that
// caused it are two pages away and were set for some other server. FallbackToAll
// answers that by drawing everybody rather than nobody. The second pass is only
// ever reached when the first produced no rows, which is the case where the walk
// cost nothing to begin with, and only when a setting was hiding somebody: a
// server that really is empty must stay empty.
func NewMemberModel(members []domain.Member, hoisted []domain.Role, opts MemberListOptions) []MemberEntry {
	if len(members) == 0 {
		return nil
	}

	entries := memberModel(members, hoisted, opts)
	if len(entries) > 0 || !opts.FallbackToAll || !(opts.HideOffline || opts.HideRoleless) {
		return entries
	}

	opts.HideOffline, opts.HideRoleless = false, false

	return memberModel(members, hoisted, opts)
}

// memberModel is the flattening itself, with the hiding settings taken as given.
func memberModel(members []domain.Member, hoisted []domain.Role, opts MemberListOptions) []MemberEntry {
	// Ungrouped is one run with no headers and no hoisting, which is what turning
	// the presence split off has always meant.
	if !opts.GroupByPresence {
		entries := make([]MemberEntry, 0, len(members))
		for i := range members {
			if opts.hides(members[i]) {
				continue
			}
			entries = append(entries, MemberEntry{Kind: MemberEntryRow, Member: members[i]})
		}

		return entries
	}

	titles, index := memberSections(hoisted, opts)
	online, offline := len(titles)-2, len(titles)-1

	buckets := make([][]domain.Member, len(titles))
	for i := range members {
		if opts.hides(members[i]) {
			continue
		}
		if !members[i].Presence.IsOnline() {
			buckets[offline] = append(buckets[offline], members[i])
			continue
		}

		at, hoistedRole := index[members[i].HoistRoleID]
		if !hoistedRole {
			at = online
		}
		buckets[at] = append(buckets[at], members[i])
	}

	entries := make([]MemberEntry, 0, len(members)+len(titles))
	for i, rows := range buckets {
		// An empty bucket emits nothing at all, header included: a server with no
		// moderator online has no Moderators section, not an empty one.
		if len(rows) == 0 {
			continue
		}

		entries = append(entries, MemberEntry{
			Kind:  MemberEntrySection,
			Title: fmt.Sprintf("%s — %d", titles[i], len(rows)),
		})
		for j := range rows {
			entries = append(entries, MemberEntry{Kind: MemberEntryRow, Member: rows[j]})
		}
	}

	return entries
}

// hides is whether a member is left out of the list altogether, asked before
// anything decides where they would have gone. Both branches of the model ask
// it, so the two settings cannot drift apart between them.
func (o MemberListOptions) hides(member domain.Member) bool {
	if o.HideOffline && !member.Presence.IsOnline() {
		return true
	}

	return o.HideRoleless && !member.HasRoles
}

// memberSections names the buckets in order and maps a hoisted role ID onto its
// own. Online and Offline are always the last two, so their indices are derived
// from the length rather than carried around.
func memberSections(hoisted []domain.Role, opts MemberListOptions) ([]string, map[string]int) {
	titles := make([]string, 0, len(hoisted)+2)
	index := make(map[string]int, len(hoisted))

	if opts.HoistRoles {
		for _, role := range hoisted {
			// A role listed twice would give the second one a bucket nothing reaches.
			if _, seen := index[role.ID]; seen || role.ID == "" {
				continue
			}
			index[role.ID] = len(titles)
			titles = append(titles, role.Name)
		}
	}

	return append(titles, "Online", "Offline"), index
}

/* Geometry */

// memberOffsets is the top of every entry, plus the total as a final element, so
// a range is two binary searches over one slice. Call on the UI thread: it reads
// the theme's two heights.
func memberOffsets(entries []MemberEntry) []float32 {
	rowHeight, sectionHeight := theme.Sizes.MemberRowHeight, theme.Sizes.MemberSectionHeight

	offsets := make([]float32, len(entries)+1)
	var y float32
	for i := range entries {
		offsets[i] = y
		if entries[i].Kind == MemberEntrySection {
			y += sectionHeight
			continue
		}
		y += rowHeight
	}
	offsets[len(entries)] = y

	return offsets
}

// visibleRange is the half-open range of entries touching a viewport of height
// at y, widened by overscan at each end and clamped to the model. offsets is
// memberOffsets' output, so its length is one more than the entry count.
//
// Two heights rule out a closed form, and the search is what makes headers and
// rows interchangeable at no cost.
func visibleRange(offsets []float32, y, height float32, overscan int) (first, last int) {
	n := len(offsets) - 1
	if n <= 0 || height <= 0 {
		return 0, 0
	}

	first = sort.Search(n, func(i int) bool { return offsets[i+1] > y })
	last = sort.Search(n, func(i int) bool { return offsets[i] >= y+height })

	return max(first-overscan, 0), min(last+overscan, n)
}

// memberSlot is where one mounted entry goes. Index-aligned with the container's
// Objects, which is why the two are always rebuilt together.
type memberSlot struct{ top, height float32 }

// memberListLayout places the mounted entries at the absolute position their
// index has in the whole model, and reports the whole model's height.
//
// MinSize is O(1) and **must stay so**: container.Scroll asks its content for a
// minimum on every offset write, so a walk of the children here would put the
// cost of the list straight back onto the scroll path — the same rule
// app/messages.go's contentHeight exists to honour. The reported width is zero
// for the sidebar reason: a vertical scroll takes its content's minimum width as
// its own, so one long name would otherwise widen the column.
type memberListLayout struct {
	slots []memberSlot
	total float32
}

func (l *memberListLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for i, child := range objects {
		if i >= len(l.slots) {
			return
		}

		child.Resize(fyne.NewSize(size.Width, l.slots[i].height))
		child.Move(fyne.NewPos(0, l.slots[i].top))
	}
}

func (l *memberListLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, l.total)
}

/* The list */

// MemberList is the member sidebar: a scrolling list that mounts only what is on
// screen. It owns its own scroller, so a caller places it as one column and
// nothing else has to know it is virtual.
type MemberList struct {
	widget.BaseWidget

	// RowMenu supplies the items right-clicking a row offers. It is set once on
	// the list rather than per row, and is passed the user the row is *currently*
	// drawing: a closure capturing a member ID would kick the wrong person the
	// first time its row was recycled.
	RowMenu func(userID string) []*fyne.MenuItem

	deps    Deps
	scroll  *ObservableScroll
	content *fyne.Container
	layout  *memberListLayout
	status  *memberStatus

	// column is the strip stacked over the scroller, held because showing or
	// hiding the strip changes the room the scroller has.
	column *fyne.Container

	entries []MemberEntry
	offsets []float32

	// The mounted window: a half-open range, the object drawing each entry in it,
	// and the rows by user so one member can be repainted without a rebuild.
	first, last int
	mounted     map[int]fyne.CanvasObject
	rows        map[string]*MemberRow

	rowPool     []*MemberRow
	sectionPool []*MemberSectionRow
}

// NewMemberList creates an empty member list.
func NewMemberList(deps Deps) *MemberList {
	w := &MemberList{
		deps:    deps,
		layout:  &memberListLayout{},
		status:  newMemberStatus(),
		mounted: make(map[int]fyne.CanvasObject),
		rows:    make(map[string]*MemberRow),
	}
	w.content = container.New(w.layout)
	w.offsets = []float32{0}

	w.scroll = NewObservableVScroll(w.content)
	w.scroll.OnScroll = func(fyne.Position) { w.mount(false) }

	// The strip takes its own height off the top and the scroller absorbs the
	// rest. Laid *over* the list it read as a glitch rather than as a message: the
	// first row is drawn from the column's own origin, so a strip on top of it cut
	// the avatar and the name in half.
	w.column = NewFillColumn(1, w.status.root, w.scroll)
	w.ExtendBaseWidget(w)

	return w
}

func (w *MemberList) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(w.column)
}

// Resize re-mounts for the new viewport. Fyne fires no scroll event on a resize
// and the content's height does not change with it, so this is the only hook
// that catches the window being resized or the sidebar being shown again.
func (w *MemberList) Resize(size fyne.Size) {
	w.BaseWidget.Resize(size)
	w.mount(false)
}

// SetModel replaces what the list draws.
//
// The scroll offset is clamped and *kept* rather than reset: this runs every
// time somebody's presence changes, and a list that jumped to the top each time
// would be unusable on the servers it exists for.
func (w *MemberList) SetModel(entries []MemberEntry) {
	w.entries = entries
	w.offsets = memberOffsets(entries)
	w.layout.total = w.offsets[len(w.offsets)-1]

	// The scroll re-reads the content's minimum and resizes it; it does not walk
	// the children, and our minimum is a field read.
	w.clampOffset()
	w.scroll.Refresh()

	w.mount(true)
}

// Reset empties the list and returns to the top, which is what changing server
// asks for — unlike a repaint of the same one, where the offset is kept.
func (w *MemberList) Reset() {
	w.SetModel(nil)
	w.scroll.ScrollToTop()
}

// RefreshMember redraws one member in place, for a change that does not move
// them. Somebody not currently mounted is a silent no-op: their row is built
// from the store's own value when it scrolls into view.
func (w *MemberList) RefreshMember(member domain.Member) {
	if row, ok := w.rows[member.UserID]; ok {
		row.SetMember(member)
	}
}

// Empty reports that the list has nothing to draw, which is what decides whether
// its status is about a first load or a refresh.
func (w *MemberList) Empty() bool { return len(w.entries) == 0 }

// SetStatus says what the rows cannot. The strip appearing or disappearing
// changes the viewport, so the window is re-mounted after the column is laid out
// again — Fyne reclaims nothing for a child that has merely been hidden. Call on
// the UI thread.
func (w *MemberList) SetStatus(status MemberListStatus) {
	if !w.status.set(status) {
		return
	}

	Relayout(w.column)
	w.mount(false)
}

// SetSweeping stops or restarts the status mark for a list whose column has been
// hidden, or which is about to be dropped by a rebuild.
//
// It is the caller's to say because neither event reaches the widget: Fyne's
// Visible() answers for one object rather than for a tree, so the mark cannot
// ask whether an ancestor took it off screen, and a discarded widget is told
// nothing at all. An animation nobody can see is a repaint request a frame for
// the life of the process. Call on the UI thread.
func (w *MemberList) SetSweeping(on bool) {
	sweeping := on && w.status.shown.Busy
	w.status.mark.SetActive(sweeping, sweeping)
}

// mount brings the window in line with the viewport. Unless force is set it
// returns as soon as it finds the range unchanged, which an ordinary wheel tick
// usually leaves it — that early return is what makes scrolling free.
func (w *MemberList) mount(force bool) {
	first, last := visibleRange(w.offsets, w.scroll.Offset.Y, w.scroll.Size().Height, memberOverscan())
	if !force && first == w.first && last == w.last {
		return
	}
	w.first, w.last = first, last

	// Released before anything is acquired, so the pools hand the same objects
	// straight back to the range that overlaps.
	for i, obj := range w.mounted {
		if i < first || i >= last {
			w.release(i, obj)
		}
	}

	objects := make([]fyne.CanvasObject, 0, last-first)
	slots := make([]memberSlot, 0, last-first)
	for i := first; i < last; i++ {
		objects = append(objects, w.acquire(i))
		slots = append(slots, memberSlot{top: w.offsets[i], height: w.offsets[i+1] - w.offsets[i]})
	}

	w.layout.slots = slots
	w.content.Objects = objects
	Relayout(w.content)
}

// acquire returns the object drawing entry i, reusing what is already in that
// slot where it can. Keying the mounted map by *index* is what lets it: an
// overlapping window leaves the same object on the same entry, so the common
// case reaches SetMember's no-op rather than building anything.
func (w *MemberList) acquire(i int) fyne.CanvasObject {
	entry := &w.entries[i]

	if obj, ok := w.mounted[i]; ok {
		switch drawn := obj.(type) {
		case *MemberRow:
			if entry.Kind == MemberEntryRow {
				w.draw(drawn, entry.Member)
				return drawn
			}
		case *MemberSectionRow:
			if entry.Kind == MemberEntrySection {
				drawn.SetTitle(entry.Title)
				return drawn
			}
		}

		// The entry under this index changed kind, so what is here is the wrong
		// shape whatever it says.
		w.release(i, obj)
	}

	if entry.Kind == MemberEntrySection {
		section := w.takeSection()
		section.SetTitle(entry.Title)
		w.mounted[i] = section

		return section
	}

	row := w.takeRow()
	w.draw(row, entry.Member)
	w.mounted[i] = row

	return row
}

// draw points a row at a member and keeps the by-user index in step.
func (w *MemberList) draw(row *MemberRow, member domain.Member) {
	if row.userID != "" && row.userID != member.UserID && w.rows[row.userID] == row {
		delete(w.rows, row.userID)
	}

	row.SetMember(member)
	w.rows[member.UserID] = row
}

// release takes an object out of the window and back into its pool. The by-user
// entry is only dropped when it still names this row: a member who moved to
// another index has already registered the row drawing them there.
func (w *MemberList) release(i int, obj fyne.CanvasObject) {
	delete(w.mounted, i)

	switch drawn := obj.(type) {
	case *MemberRow:
		if w.rows[drawn.userID] == drawn {
			delete(w.rows, drawn.userID)
		}
		drawn.release()
		w.rowPool = append(w.rowPool, drawn)
	case *MemberSectionRow:
		w.sectionPool = append(w.sectionPool, drawn)
	}
}

func (w *MemberList) takeRow() *MemberRow {
	if n := len(w.rowPool); n > 0 {
		row := w.rowPool[n-1]
		w.rowPool = w.rowPool[:n-1]

		return row
	}

	// The hook reads RowMenu through the list rather than closing over it, so a
	// menu set after the first rows were built still reaches them.
	return newMemberRow(w.deps, func(userID string) []*fyne.MenuItem {
		if w.RowMenu == nil {
			return nil
		}

		return w.RowMenu(userID)
	})
}

func (w *MemberList) takeSection() *MemberSectionRow {
	if n := len(w.sectionPool); n > 0 {
		section := w.sectionPool[n-1]
		w.sectionPool = w.sectionPool[:n-1]

		return section
	}

	return newMemberSectionRow()
}

// clampOffset pulls the view back inside a model that has shrunk under it, which
// is what a hidden offline section or a server emptying out does.
func (w *MemberList) clampOffset() {
	limit := max(w.layout.total-w.scroll.Size().Height, 0)
	if w.scroll.Offset.Y > limit {
		w.scroll.Offset.Y = limit
	}
}

// memberOverscan is how many entries are mounted past each edge of the viewport.
// Read per mount rather than held, so the setting applies to the next scroll.
func memberOverscan() int { return config.Current().Behaviour.MemberOverscan }

/* Status */

// MemberListStatus is what the sidebar says when its rows cannot: the membership
// is on its way, or the request for it never arrived.
//
// It is a strip *above* the list rather than a message in place of it. The list
// is paint-then-fill — re-entering a server draws what is already known while the
// fetch runs — so saying "refreshing" must not take the members already there
// away. It costs its own height rather than being laid over the top row, which is
// the one placement that reads the same whether there is anything under it or
// not: over the rows it cut the first avatar and name in half.
type MemberListStatus struct {
	Text string // "" draws nothing at all

	// Busy runs the sweeping mark above the text. It is the glyph the typing
	// indicator uses, which is what keeps "something is happening" one shape in
	// this client rather than two.
	Busy bool

	// Action labels the button under the text and Retry is what it does. Both or
	// neither: a button with nothing to say is not one.
	Action string
	Retry  func()
}

// drawnAs reports whether two statuses draw the same thing. The callback is
// compared only for presence — it is rebuilt per call, closing over the server it
// would retry, and Go does not compare functions.
func (s MemberListStatus) drawnAs(other MemberListStatus) bool {
	return s.Text == other.Text && s.Busy == other.Busy && s.Action == other.Action &&
		(s.Retry == nil) == (other.Retry == nil)
}

// memberStatus is the strip itself.
//
// It is built once and shown or hidden rather than rebuilt per status, because
// it holds a TypingMark: Fyne tells a discarded widget nothing, so a strip
// replaced on every change would leave a sweep running against a rectangle
// nothing draws — one repaint request a frame, for the life of the process.
type memberStatus struct {
	root  *fyne.Container // the strip and its backing, hidden when there is nothing to say
	strip *fyne.Container

	// The two halves that come and go, held as the boxes carrying their own gap:
	// hiding the mark alone would leave the space under it.
	markBox  *fyne.Container
	retryBox *fyne.Container

	mark  *TypingMark
	label *canvas.Text
	retry *widget.Button

	shown MemberListStatus
}

func newMemberStatus() *memberStatus {
	pad, gap := theme.Sizes.MemberStatusPadding, theme.Sizes.MemberStatusGap

	label := canvas.NewText("", theme.Colors.MemberStatusText)
	label.TextSize = theme.Sizes.MemberStatusTextSize
	label.Alignment = fyne.TextAlignCenter

	s := &memberStatus{
		mark:  NewTypingMark(theme.Sizes.MemberStatusMarkSize, theme.Colors.MemberStatusText),
		label: label,
		retry: widget.NewButton("", nil),
	}

	s.markBox = VBoxNoSpacing(container.NewCenter(s.mark), VerticalSpacer(gap))
	s.retryBox = VBoxNoSpacing(VerticalSpacer(gap), container.NewCenter(s.retry))
	s.strip = VBoxNoSpacing(s.markBox, label, s.retryBox)

	// The list's own background rather than none at all: the strip is the top of
	// the column, and a transparent one would show the window through it.
	s.root = container.NewStack(canvas.NewRectangle(theme.Colors.MemberListBackground),
		NewInset(s.strip, pad, pad, pad, pad))
	s.root.Hide()

	return s
}

// set draws status, or nothing at all when it has no text, reporting whether
// anything moved — the caller re-lays the column out only when it did.
//
// Refresh rather than Relayout: the strip's own height changes with it — a
// button appearing is a taller strip — and re-running one layout at the size it
// already has cannot express that. It is a five-object tree changed only when
// the fetch is.
func (s *memberStatus) set(status MemberListStatus) bool {
	if s.shown.drawnAs(status) {
		s.retry.OnTapped = status.Retry // the label is the same; the server may not be
		return false
	}
	s.shown = status

	if status.Text == "" {
		s.mark.SetActive(false, false)
		s.root.Hide()

		return true
	}

	s.label.Text = status.Text
	s.mark.SetActive(status.Busy, status.Busy)
	s.retry.SetText(status.Action)
	s.retry.OnTapped = status.Retry

	showIf(s.markBox, status.Busy)
	showIf(s.retryBox, status.Retry != nil)

	s.root.Show()
	s.root.Refresh()

	return true
}

// showIf shows or hides an object from a condition, which is what every one of
// these is really saying.
func showIf(obj fyne.CanvasObject, visible bool) {
	if visible {
		obj.Show()
		return
	}

	obj.Hide()
}

/* Rows */

// MemberRow is one person in the member sidebar: their avatar with a presence
// dot, their name in their role's colour, and a bot mark.
//
// It is a widget with an in-place updater rather than something built per member
// because the list recycles it — see the file comment. Every field below records
// what is currently *drawn*, so SetMember can touch only what moved.
type MemberRow struct {
	tapBase

	deps   Deps
	onMenu func(userID string) []*fyne.MenuItem

	background  *canvas.Rectangle
	avatar      *fyne.Container
	placeholder *canvas.Circle // the one the slot falls back to; see newAvatarSlot
	dot         *canvas.Circle
	name        *canvas.Text
	nameBox     *fyne.Container
	botMark     fyne.CanvasObject

	// row is the assembled tree, built once here rather than in CreateRenderer,
	// which Fyne may run again after a renderer is dropped — by then the name box
	// holds a shortened name, and rebuilding around it would take that for the
	// name. It is held because the bot mark appearing moves the layout rather than
	// repainting in place.
	row *fyne.Container

	userID    string
	fullName  string
	avatarURL string
	presence  domain.Presence
	fill      color.Color
	bot       bool

	// generation is the recycling guard. Every SetMember and every release bumps
	// it, and an image load captures it: a picture arriving after the row has
	// moved on to somebody else has no other way to know it is not wanted. It is
	// UI-thread only — LoadAsync delivers there — so a plain counter, not an
	// atomic.
	generation uint64
}

var (
	_ fyne.Tappable          = (*MemberRow)(nil)
	_ fyne.SecondaryTappable = (*MemberRow)(nil)
	_ desktop.Hoverable      = (*MemberRow)(nil)
)

// newMemberRow builds an empty row. It draws nobody until SetMember is called.
// A nil onMenu is a row that offers no menu, which is what a settings preview
// wants and what the list itself never passes.
func newMemberRow(deps Deps, onMenu func(userID string) []*fyne.MenuItem) *MemberRow {
	name := canvas.NewText("", theme.Colors.TextPrimary)
	name.TextSize = theme.Sizes.MemberNameSize

	side := theme.Sizes.MemberAvatarSize
	avatar, placeholder := newAvatarSlot(fyne.NewSize(side, side))

	w := &MemberRow{
		deps:        deps,
		onMenu:      onMenu,
		background:  canvas.NewRectangle(color.Transparent),
		avatar:      avatar,
		placeholder: placeholder,
		dot:         newPresenceDot(),
		name:        name,
		nameBox:     NewEllipsisText(name),
		botMark:     NewBotMark(theme.Sizes.MemberBotMarkSize),

		// The recorded state has to match what was just built, or the first
		// SetMember will no-op over a difference that is really there.
		fill:     theme.Colors.TextPrimary,
		presence: domain.PresenceOffline,
	}
	w.botMark.Hide()

	leading := HBoxNoSpacing(
		HorizontalSpacer(theme.Sizes.ChannelLeftPadding),
		container.NewCenter(container.New(&memberPresenceLayout{}, w.avatar, w.dot)),
		HorizontalSpacer(theme.Sizes.ChannelLeftPadding),
	)

	// The name takes the leftover width in a Border's centre rather than its
	// natural width: an HBox hands a zero-minimum child zero width, and the
	// ellipsis box reports zero on purpose so no name can widen the column.
	w.row = container.NewStack(w.background, container.NewBorder(nil, nil, leading,
		HBoxNoSpacing(w.botMark, HorizontalSpacer(theme.Sizes.ChannelLeftPadding)),
		w.nameBox,
	))

	// The row is its own anchor, so the profile card opens beside the name that
	// was clicked. Both hooks read userID at the moment of the click rather than
	// capturing it, which is the whole discipline of a recycled widget.
	w.onTap = func() { w.deps.Actions.OnUserTapped(w.userID, w) }
	w.onSecondaryTap = func(e *fyne.PointEvent) {
		if w.onMenu == nil {
			return
		}

		showMenuHook(w, func() []*fyne.MenuItem { return w.onMenu(w.userID) }, e)
	}
	w.ExtendBaseWidget(w)

	return w
}

func (w *MemberRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(w.row)
}

// SetMember draws member, touching only what moved. Unchanged state costing
// nothing is not polish here: a repaint of the whole model calls this once per
// mounted row, and a scroll calls it for every row the window still holds.
func (w *MemberRow) SetMember(member domain.Member) {
	w.userID = member.UserID

	w.setName(member.Name, memberNameColor(member))
	w.setPresence(member.Presence)
	w.setBot(member.Bot)
	w.setAvatar(member.AvatarURL)
}

// release takes the row out of use. The generation bump is what drops a picture
// still in flight for whoever it was drawing.
func (w *MemberRow) release() {
	w.generation++
	w.userID = ""
	w.avatarURL = ""
	w.showPlaceholder()
}

// setName re-labels the row. The comparison is against fullName rather than the
// text object, which ellipsisLayout rewrites during layout — reading it back
// would take the name to be whatever fitted the column.
func (w *MemberRow) setName(text string, fill color.Color) {
	if w.fullName == text && sameColor(w.fill, fill) {
		return
	}

	if w.fullName != text {
		w.fullName = text
		SetEllipsisText(w.nameBox, text)
	}

	// A role colour can be a domain.Gradient, and a gradient must never reach a
	// canvas.Text: Fyne keys its glyph-run cache on the text object's fields,
	// colour included, and a fill that cannot be a map key panics the painter on
	// the frame it is drawn. AccentText is the wrong answer here — one text object
	// per rune, on a row recycled thousands of times.
	w.fill = fill
	w.name.Color = solidColor(fill)
	w.name.Refresh()
}

func (w *MemberRow) setPresence(presence domain.Presence) {
	if w.presence == presence {
		return
	}

	w.presence = presence
	w.dot.FillColor = presenceColor(presence)
	w.dot.Refresh()
}

func (w *MemberRow) setBot(bot bool) {
	if w.bot == bot {
		return
	}

	w.bot = bot
	if bot {
		w.botMark.Show()
	} else {
		w.botMark.Hide()
	}

	// Showing or hiding neither lays a container out nor repaints it, and the
	// slot the mark vacates stays reserved until something does.
	Relayout(w.row)
}

// setAvatar loads url into the row's slot. An unchanged URL is left alone, so a
// row scrolled back into view keeps its picture instead of flashing the
// placeholder; anything else resets to the placeholder and bumps the generation,
// which is what the callback checks itself against.
func (w *MemberRow) setAvatar(url string) {
	if w.avatarURL == url {
		return
	}

	w.avatarURL = url
	w.generation++
	w.showPlaceholder()

	if url == "" {
		return
	}

	side := theme.Sizes.MemberAvatarSize
	size := fyne.NewSize(side, side)

	generation := w.generation
	w.deps.Images.LoadAsync(imageCacheID(url), url, true, func(img image.Image) {
		w.paintAvatar(generation, img, size)
	})
}

// showPlaceholder empties the avatar slot back to the circle it was built with.
// It restores that same object rather than a new one, and only refreshes when
// the slot actually held something else: a canvas.Circle nobody has drawn is
// invisible until the container it is in is refreshed, so putting a fresh one in
// on every release left every recycled row with no avatar at all.
func (w *MemberRow) showPlaceholder() {
	if len(w.avatar.Objects) == 1 && w.avatar.Objects[0] == w.placeholder {
		return
	}

	w.avatar.Objects = []fyne.CanvasObject{w.placeholder}
	w.avatar.Refresh()
}

// paintAvatar puts a loaded picture in the slot, unless the row has been
// recycled since it was asked for. That check is the whole of what makes an
// off-thread load safe on a widget the list reuses, so it is named rather than
// left inside the callback. Call on the UI thread.
func (w *MemberRow) paintAvatar(generation uint64, img image.Image, size fyne.Size) {
	if w.generation != generation || img == nil {
		return
	}

	picture := canvas.NewImageFromImage(img)
	picture.FillMode = canvas.ImageFillContain
	picture.SetMinSize(size)

	w.avatar.Objects = []fyne.CanvasObject{picture}
	w.avatar.Refresh()
}

func (w *MemberRow) MouseIn(*desktop.MouseEvent) {
	w.background.FillColor = theme.Colors.TappableHoverBg
	w.background.Refresh()
}

func (w *MemberRow) MouseOut() {
	w.background.FillColor = color.Transparent
	w.background.Refresh()
}

// memberNameColor is what a member's name is drawn in: their most-senior
// coloured role, dimmed to a single grey while they are offline. The dimming is
// what lets one list hold both halves without reading as two columns.
func memberNameColor(member domain.Member) color.Color {
	if !member.Presence.IsOnline() {
		return theme.Colors.MemberNameOffline
	}
	if member.Color != nil {
		return member.Color
	}

	return theme.Colors.TextPrimary
}

// newPresenceDot is the small filled circle over the avatar's corner. It wears a
// ring in the list's own background rather than the shared hairline, so it reads
// as punched out of the avatar rather than laid on top of it.
func newPresenceDot() *canvas.Circle {
	dot := canvas.NewCircle(presenceColor(domain.PresenceOffline))
	dot.StrokeColor = theme.Colors.MemberListBackground
	dot.StrokeWidth = theme.Sizes.MemberPresenceDotRing

	return dot
}

// memberPresenceLayout stacks the presence dot on the avatar's bottom-right
// corner. The dot is placed rather than centred because a row layout stretches
// what it is given, and a stretched circle is an ellipse.
type memberPresenceLayout struct{}

func (l *memberPresenceLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	avatar, dot := objects[0], objects[1]

	avatar.Resize(size)
	avatar.Move(fyne.Position{})

	side := theme.Sizes.MemberPresenceDotSize
	dot.Resize(fyne.NewSize(side, side))
	dot.Move(fyne.NewPos(size.Width-side, size.Height-side))
}

func (l *memberPresenceLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return objects[0].MinSize()
}

/* Section headers */

// MemberSectionRow is the small bold header grouping members, e.g. "Online — 5".
// A widget with a setter for the same reason MemberRow is one: the list recycles
// it, and one that rebuilt itself per scroll would undo the point of the pool.
type MemberSectionRow struct {
	widget.BaseWidget

	label *canvas.Text
	title string
}

func newMemberSectionRow() *MemberSectionRow {
	label := canvas.NewText("", theme.Colors.MemberSectionText)
	label.TextStyle = fyne.TextStyle{Bold: true}
	label.TextSize = theme.Sizes.MemberSectionTextSize

	w := &MemberSectionRow{label: label}
	w.ExtendBaseWidget(w)

	return w
}

// SetTitle re-labels the header, no-op on an unchanged one.
func (w *MemberSectionRow) SetTitle(title string) {
	if w.title == title {
		return
	}

	w.title, w.label.Text = title, title
	w.label.Refresh()
}

func (w *MemberSectionRow) CreateRenderer() fyne.WidgetRenderer {
	// The top padding is inside the section's own height rather than a spacer
	// beside it, so the list still has exactly two heights to add up.
	return widget.NewSimpleRenderer(
		NewInset(w.label, theme.Sizes.MemberSectionTopPad, 0, theme.Sizes.ChannelLeftPadding, 0))
}

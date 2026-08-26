package ui

// The message column. The window the controller keeps — up to MountedCap
// messages — is data here; only the rows the viewport touches, and a few past
// each edge, have widgets. Two costs of the flat column decided that: the driver
// asks every mounted object its MinSize on every dirty frame, and the scroller
// asks its content's MinSize on every offset write. Both are now O(rows on
// screen), and the content's height is a field.
//
// Rows are variable-height, so nothing is fixed the way the member list's rows
// are. A row's height is an estimate from what the message carries until its
// widget has been laid out at the column's width. Measuring happens in the
// layout — which is also where a row that grew after mounting, an editor or an
// invite card resolving, is noticed, since the driver re-runs a layout whenever
// a child's minimum moves. A height changing above the viewport shifts the
// offset by the same amount, so what is on screen stays put; a column sitting
// at its bottom stays there.

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

/* The model */

// windowRow is one message in the window and what its neighbours decide about
// how it is drawn. height is measured once a widget has been laid out at the
// column's width, an estimate until then.
type windowRow struct {
	message *domain.Message

	grouped  bool   // a continuation of the row above: no name, no avatar
	followed bool   // the row below continues it: a tight bottom margin
	dayLabel string // the separator above it, when it opens a day

	// The invite links the body carries, scanned when the row was estimated. The
	// scan is a markdown parse and a row is re-estimated whenever its neighbours
	// move, so the answer is kept; scanned separates "none" — what almost every
	// body answers — from "not looked at yet".
	codes   []string
	scanned bool

	height   float32
	measured bool
}

// messageGroupWindow is the largest gap a message may follow the previous one by
// and still group under it.
func messageGroupWindow() time.Duration { return config.Current().Behaviour.GroupWindow() }

// messageOverscan is how many rows are mounted past each edge of the viewport.
// Read per mount rather than held, so the setting applies to the next scroll.
func messageOverscan() int { return config.Current().Behaviour.MessageOverscan }

// continuesGroup reports whether curr should render as a continuation of prev:
// same author, neither system, webhook nor masqueraded, same calendar day, and
// within messageGroupWindow. A reply starts a fresh group, and so does a message
// across a day separator — a pair minutes apart over midnight must not render as
// one headerless block.
//
// A pinned message starts one too: its mark rides the name line, the one thing a
// continuation does not draw, so grouping it would be the way to pin a message
// and see nothing happen.
func continuesGroup(prev, curr *domain.Message) bool {
	if !config.Current().Interface.GroupMessages {
		return false
	}
	if prev == nil || curr == nil || curr.AuthorID == "" || prev.AuthorID != curr.AuthorID {
		return false
	}
	if curr.System != nil || prev.System != nil ||
		curr.Webhook != nil || prev.Webhook != nil ||
		curr.Masquerade || prev.Masquerade {
		return false
	}
	if len(curr.Replies) > 0 || curr.Pinned {
		return false
	}

	pt, errPrev := util.Timestamp(prev.ID)
	ct, errCurr := util.Timestamp(curr.ID)
	if errPrev != nil || errCurr != nil || !util.SameDay(pt, ct) {
		return false
	}

	gap := ct.Sub(pt)
	return gap >= 0 && gap <= messageGroupWindow()
}

// dayLabel returns the day separator label for curr — "" when it belongs to the
// same calendar day as the message above it. A message with no predecessor is
// treated as opening its day, so loaded history always starts with a date.
func dayLabel(prev, curr *domain.Message) string {
	ct, err := util.Timestamp(curr.ID)
	if err != nil {
		return ""
	}

	if prev != nil {
		if pt, err := util.Timestamp(prev.ID); err == nil && util.SameDay(pt, ct) {
			return ""
		}
	}

	return util.DayLabel(ct)
}

/* The list */

// MessageList is the message column: a scrolling list of a channel's window that
// mounts only what is on screen. It owns its scroller, so the controller places
// it as one object and asks it where the reader is.
//
// The window is addressed by message — Message(i), Index(id), Mounted(id) — and
// every mutation keeps what is on screen where it was: a page prepended above
// the viewport moves the offset down by its own height, a row trimmed above it
// moves it up. Nothing here touches the network or the cache.
type MessageList struct {
	widget.BaseWidget

	// OnMount is told about every row as its widget is built, before it is drawn.
	// It is the controller's hook for what a row names and does not have — an
	// author or a quoted message still to fetch — and fires again each time a row
	// scrolls back in, its widget having been dropped in between.
	OnMount func(message *domain.Message, grouped bool)

	// OnScroll fires after every scroll the reader made, once the window has been
	// re-mounted for it. Programmatic scrolls do not report.
	OnScroll func()

	// OnSelectionChanged reports the size of the selection every time it or the
	// mode moves, so the bar over the composer can say how many without asking. It
	// fires for a delete landing too: a removed row leaves the set with it.
	OnSelectionChanged func(count int)

	deps    Deps
	dock    fyne.CanvasObject // what floats over the column's bottom, see NewDockReserve
	scroll  *ObservableScroll
	reserve *fyne.Container // the scroller's content: rows plus the room under the dock
	content *fyne.Container // the rows themselves, placed by layout
	layout  *slotLayout

	rows    []windowRow
	offsets []float32      // the top of each row, plus the total, so a range is two binary searches
	index   map[string]int // message ID -> row

	// The mounted window: a half-open range, and the widget drawing each row in it
	// by message ID — an index would move under a prepend.
	first, last int
	mounted     map[string]*MessageWidget

	// status is the one line standing in for rows — loading, empty, refused. While
	// it is up there are no rows.
	status fyne.CanvasObject

	// The selection a bulk delete is built from. It is keyed by message ID for the
	// reason mounted is: an index moves under a prepend, and history keeps arriving
	// while the reader picks. anchor is the last row picked, which a Shift-click
	// extends from.
	selecting bool
	selected  map[string]bool
	anchor    string

	// width is the column width the measured heights are good for. A different one
	// re-wraps every body, so it sends every row back to an estimate.
	width float32
}

// NewMessageList creates an empty column. dock is what floats over its bottom;
// the room it takes is held under the last row so nothing ends up beneath it.
func NewMessageList(deps Deps, dock fyne.CanvasObject) *MessageList {
	l := &MessageList{
		deps:    deps,
		dock:    dock,
		offsets: []float32{0},
		index:   make(map[string]int),
		mounted: make(map[string]*MessageWidget),
	}
	l.layout = &slotLayout{measure: l.measure}
	l.content = container.New(l.layout)
	l.reserve = NewDockReserve(l.content, dock)

	l.scroll = NewObservableVScroll(l.reserve)
	l.scroll.OnScroll = func(fyne.Position) {
		l.mount(false)
		if l.OnScroll != nil {
			l.OnScroll()
		}
	}
	l.ExtendBaseWidget(l)

	return l
}

func (l *MessageList) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(l.scroll)
}

// Resize re-mounts for the new viewport. The scroller lays its content out on
// the way, which measures every mounted row at the new width; the range is then
// re-read for the new height. A column at its bottom stays there — the scroller
// zeroes an offset while its content is still unsized, which is every first
// layout, and the tail is what the column opened at.
func (l *MessageList) Resize(size fyne.Size) {
	pinned := l.FromBottom() <= 0.5

	l.BaseWidget.Resize(size)

	if l.status != nil {
		l.sizeStatus()
		return
	}
	if pinned {
		l.scroll.Offset.Y = l.bottomOffset()
		l.syncScroll()
	}
	l.mount(false)
}

/* Reading the window */

// Len is how many messages the window holds.
func (l *MessageList) Len() int { return len(l.rows) }

// Message is the message at row i, or nil out of range.
func (l *MessageList) Message(i int) *domain.Message {
	if i < 0 || i >= len(l.rows) {
		return nil
	}

	return l.rows[i].message
}

// Index is the row holding messageID, or -1.
func (l *MessageList) Index(messageID string) int {
	if i, ok := l.index[messageID]; ok {
		return i
	}

	return -1
}

// Mounted is the widget drawing messageID, or nil when the row is not on screen
// — which is most of the window most of the time.
func (l *MessageList) Mounted(messageID string) *MessageWidget {
	return l.mounted[messageID]
}

// EachMounted visits every widget currently built, in no particular order.
func (l *MessageList) EachMounted(fn func(*MessageWidget)) {
	for _, w := range l.mounted {
		fn(w)
	}
}

/* Selecting */

// Selecting reports whether the column is picking messages for a bulk delete.
func (l *MessageList) Selecting() bool { return l.selecting }

// SetSelecting turns the mode on or off. Leaving it empties the set — a
// selection nothing is drawing is one the reader cannot see they still have.
func (l *MessageList) SetSelecting(on bool) {
	if on == l.selecting {
		return
	}

	l.selecting = on
	l.anchor = ""
	if on {
		l.selected = make(map[string]bool)
	} else {
		l.selected = nil
	}

	for id, w := range l.mounted {
		w.SetSelecting(on, l.selected[id])
	}
	l.reportSelection()
}

// Select adds one message to the set and, with extend, everything between it and
// the last row picked. Only rows the route would accept are taken — see
// selectable — so an extend across a week-old message steps over it rather than
// poisoning the batch.
//
// A row already picked is dropped again, unless the reader is extending: a Shift
// that both grew and shrank the set would be unreadable.
func (l *MessageList) Select(messageID string, extend bool) {
	if !l.selecting {
		return
	}

	to, ok := l.index[messageID]
	if !ok {
		return
	}

	from := to
	if extend {
		if i, ok := l.index[l.anchor]; ok {
			from = i
		}
	}

	if from == to && !extend {
		// Turning one *off* is always allowed: a row that has aged past the window
		// while it sat in the set has to be removable, and only adding can poison a
		// batch.
		picked := !l.selected[messageID]
		if picked && !l.selectable(l.rows[to].message) {
			return
		}

		l.setSelected(messageID, picked)
		l.anchor = messageID
		l.reportSelection()

		return
	}

	lo, hi := min(from, to), max(from, to)
	for i := lo; i <= hi; i++ {
		if message := l.rows[i].message; l.selectable(message) {
			l.setSelected(message.ID, true)
		}
	}
	l.anchor = messageID
	l.reportSelection()
}

// Selected is the picked messages in the column's own order, oldest first, which
// is the order the request is built in.
func (l *MessageList) Selected() []string {
	ids := make([]string, 0, len(l.selected))
	for i := range l.rows {
		if id := l.rows[i].message.ID; l.selected[id] {
			ids = append(ids, id)
		}
	}

	return ids
}

// selectable is whether a row may join a bulk delete, asked of the *model* rather
// than of a widget: an extend spans rows that are not mounted. It reads the same
// two rules MessageWidget.canBulkSelect does.
func (l *MessageList) selectable(message *domain.Message) bool {
	return withinBulkWindow(message.ID) &&
		l.deps.Store.Permissions(message.ChannelID).Has(domain.PermissionManageMessages)
}

// setSelected writes one entry and repaints the row if it happens to be mounted.
func (l *MessageList) setSelected(messageID string, on bool) {
	if on {
		l.selected[messageID] = true
	} else {
		delete(l.selected, messageID)
	}

	if w, ok := l.mounted[messageID]; ok {
		w.SetSelected(on)
	}
}

// forgetSelected drops the messages that have just left the window, so a delete
// landing empties the set that caused it. Reports whether anything went.
func (l *MessageList) forgetSelected(doomed map[string]bool) bool {
	dropped := false
	for id := range l.selected {
		if doomed[id] {
			delete(l.selected, id)
			dropped = true
		}
	}
	if doomed[l.anchor] {
		l.anchor = ""
	}

	return dropped
}

func (l *MessageList) reportSelection() {
	if l.OnSelectionChanged != nil {
		l.OnSelectionChanged(len(l.selected))
	}
}

/* Changing the window */

// SetMessages replaces the window and opens it at its bottom, oldest first.
func (l *MessageList) SetMessages(messages []*domain.Message) {
	l.clearStatus()
	l.dropRows()

	l.rows = make([]windowRow, len(messages))
	for i, message := range messages {
		l.rows[i].message = message
	}
	for i := range l.rows {
		l.derive(i)
		l.rows[i].height = l.estimate(&l.rows[i])
	}
	l.reindex()

	l.scroll.Offset.Y = l.bottomOffset()
	l.settle()
}

// Prepend adds older messages above the window, keeping the viewport on the
// rows it was showing: the offset moves down by what was added.
func (l *MessageList) Prepend(older []*domain.Message) {
	if len(older) == 0 {
		return
	}
	l.clearStatus()

	rows := make([]windowRow, len(older), len(older)+len(l.rows))
	for i, message := range older {
		rows[i].message = message
	}
	l.rows = append(rows, l.rows...)

	n := len(older)
	var added float32
	for i := range n {
		l.derive(i)
		l.rows[i].height = l.estimate(&l.rows[i])
		added += l.rows[i].height
	}
	l.rederive(n, n) // the old top row has a predecessor now
	l.reindex()

	l.scroll.Offset.Y += added
	l.settle()
}

// Append adds newer messages below the window. The viewport does not move; a
// caller wanting to follow the tail scrolls to it.
func (l *MessageList) Append(newer []*domain.Message) {
	if len(newer) == 0 {
		return
	}
	l.clearStatus()

	n := len(l.rows)
	for _, message := range newer {
		l.rows = append(l.rows, windowRow{message: message})
	}
	for i := n; i < len(l.rows); i++ {
		l.derive(i)
		l.rows[i].height = l.estimate(&l.rows[i])
	}
	l.rederive(n-1, n-1) // the old bottom row may be followed now
	l.reindex()

	l.settle()
}

// TrimTop drops the oldest n rows, keeping the viewport where it was.
func (l *MessageList) TrimTop(n int) {
	n = min(n, len(l.rows))
	if n <= 0 {
		return
	}

	var removed float32
	for i := range n {
		removed += l.rows[i].height
		delete(l.mounted, l.rows[i].message.ID)
	}
	l.rows = slices.Delete(l.rows, 0, n)

	l.rederive(0, 0) // the new top row has nothing above it
	l.reindex()

	l.scroll.Offset.Y = max(l.scroll.Offset.Y-removed, 0)
	l.settle()
}

// TrimBottom drops the newest n rows.
func (l *MessageList) TrimBottom(n int) {
	n = min(n, len(l.rows))
	if n <= 0 {
		return
	}

	keep := len(l.rows) - n
	for i := keep; i < len(l.rows); i++ {
		delete(l.mounted, l.rows[i].message.ID)
	}
	l.rows = slices.Delete(l.rows, keep, len(l.rows))

	l.rederive(keep-1, keep-1) // the new bottom row is followed by nothing
	l.reindex()

	l.settle()
}

// Remove takes the doomed messages out of the window in one pass, re-deriving
// the grouping at every seam a removal leaves — a continuation whose group head
// went regains its header — and reports how many went. Rows removed above the
// viewport move the offset up by their height, so what is on screen stays put.
func (l *MessageList) Remove(doomed map[string]bool) int {
	kept := l.rows[:0]
	var seams []int
	var shift float32
	view := l.scroll.Offset.Y

	for i := range l.rows {
		row := l.rows[i]
		if !doomed[row.message.ID] {
			kept = append(kept, row)
			continue
		}

		delete(l.mounted, row.message.ID)
		seams = append(seams, len(kept))
		if l.offsets[i]+row.height <= view {
			shift -= row.height
		}
	}
	if len(seams) == 0 {
		return 0
	}
	if l.forgetSelected(doomed) {
		l.reportSelection()
	}

	removed := len(l.rows) - len(kept)
	clear(l.rows[len(kept):])
	l.rows = kept

	// The row that moved up into a seam sees a new message above it, and the row
	// above it a new one below. Seams repeat where a run was deleted.
	last := -1
	for _, i := range seams {
		if i == last {
			continue
		}
		last = i
		l.rederive(i-1, i)
	}
	l.reindex()

	l.scroll.Offset.Y = max(l.scroll.Offset.Y+shift, 0)
	l.settle()

	return removed
}

// Replace swaps the row holding message's ID for message and rebuilds its
// widget if it is on screen. A row off screen is simply drawn from the new
// message when it scrolls in.
func (l *MessageList) Replace(message *domain.Message) {
	i, ok := l.index[message.ID]
	if !ok {
		return
	}

	delete(l.mounted, message.ID)
	l.rows[i].message = message
	l.rows[i].measured = false // the old height stands as the estimate
	l.rows[i].codes, l.rows[i].scanned = nil, false

	l.rederive(i-1, i+1)
	l.mount(true)
}

// SetReactions redraws the chip row of the row holding message's ID and leaves
// the widget standing, for the one update that changes nothing else: rebuilding
// the row for a reaction re-parses the body, re-requests its pictures and drops
// whatever the pointer was over. It answers false when the row is not on screen,
// or when the chip row itself has to appear or go — both of which are Replace's
// work, and the caller's fallback.
//
// Nothing derive reads can have moved, so no seam is re-derived; the height can,
// a chip being added or taken away, so the row goes back to an estimate and the
// column settles.
func (l *MessageList) SetReactions(message *domain.Message) bool {
	i, ok := l.index[message.ID]
	if !ok {
		return false
	}

	w := l.mounted[message.ID]
	if w == nil || !w.SetReactions(message) {
		return false
	}

	l.rows[i].message = message
	l.rows[i].measured = false
	l.settle()

	return true
}

// ShowStatus replaces the rows with one line, centred on what can be seen.
func (l *MessageList) ShowStatus(line fyne.CanvasObject) {
	l.dropRows()
	l.status = NewMinHeightContainer(0, line)
	l.content.Objects = []fyne.CanvasObject{l.status}

	l.scroll.Offset = fyne.Position{}
	l.sizeStatus()
}

// Clear empties the column.
func (l *MessageList) Clear() {
	l.dropRows()
	l.clearStatus()

	l.scroll.Offset = fyne.Position{}
	l.settle()
}

// Relayout re-reads the room the dock takes and re-mounts for it. Call when
// what floats over the column has grown or shrunk.
func (l *MessageList) Relayout() {
	if l.status != nil {
		l.sizeStatus()
		return
	}

	l.syncScroll()
	l.mount(false)
}

/* Where the reader is */

// FromBottom is how far the viewport stands above the end of the column.
func (l *MessageList) FromBottom() float32 {
	return l.contentHeight() - l.viewHeight() - l.scroll.Offset.Y
}

// AtBottom reports whether the viewport is within tolerance of the end.
func (l *MessageList) AtBottom(tolerance float32) bool {
	return l.FromBottom() < tolerance
}

// AtTop reports whether the viewport is at the start of the column.
func (l *MessageList) AtTop() bool { return l.scroll.Offset.Y <= 0 }

// ScrollToBottom shows the newest row.
func (l *MessageList) ScrollToBottom() { l.scrollTo(l.bottomOffset()) }

// ScrollToTop shows the oldest.
func (l *MessageList) ScrollToTop() { l.scrollTo(0) }

// Reveal centres the viewport on messageID, reporting whether the window holds
// it. Centred rather than pinned to the top: a message is read with what was
// said around it, and what was said before it is half of that. Placed twice —
// the rows around it are measured by the first placing and may have moved it.
func (l *MessageList) Reveal(messageID string) bool {
	i, ok := l.index[messageID]
	if !ok {
		return false
	}

	l.scrollTo(l.centredOn(i))
	l.scrollTo(l.centredOn(l.index[messageID]))

	return true
}

// InView reports whether messageID's row overlaps the part of the viewport the
// reader can actually see — the dock floats over the bottom of it.
func (l *MessageList) InView(messageID string) bool {
	i, ok := l.index[messageID]
	if !ok {
		return false
	}

	view := l.scroll.Offset.Y
	return l.offsets[i+1] > view && l.offsets[i] < view+l.viewHeight()-DockReserve(l.dock)
}

func (l *MessageList) viewHeight() float32 { return l.scroll.Size().Height }

// contentHeight is the height the scroller gives its content: the rows, the room
// under the dock, and never less than the viewport.
func (l *MessageList) contentHeight() float32 {
	return max(l.layout.total+DockReserve(l.dock), l.viewHeight())
}

func (l *MessageList) bottomOffset() float32 {
	return max(l.contentHeight()-l.viewHeight(), 0)
}

// centredOn is the offset that puts row i in the middle of what can be seen.
func (l *MessageList) centredOn(i int) float32 {
	view := l.viewHeight() - DockReserve(l.dock)

	return max(l.offsets[i]-max(view-l.rows[i].height, 0)/2, 0)
}

// scrollTo moves the viewport and mounts for where it landed.
func (l *MessageList) scrollTo(y float32) {
	l.scroll.Offset.Y = y
	l.syncScroll()
	l.mount(false)
}

/* Geometry */

// reindex rebuilds the offsets and the index after the rows changed.
func (l *MessageList) reindex() {
	clear(l.index)
	for i := range l.rows {
		l.index[l.rows[i].message.ID] = i
	}
	l.reoffset()
}

// reoffset rebuilds the offsets after heights changed. The total is what the
// layout reports as the column's minimum.
func (l *MessageList) reoffset() {
	l.offsets = slices.Grow(l.offsets[:0], len(l.rows)+1)

	var y float32
	for i := range l.rows {
		l.offsets = append(l.offsets, y)
		y += l.rows[i].height
	}
	l.offsets = append(l.offsets, y)
	l.layout.total = y
}

// derive decides how row i is drawn from the rows either side of it.
func (l *MessageList) derive(i int) {
	row := &l.rows[i]

	var prev, next *domain.Message
	if i > 0 {
		prev = l.rows[i-1].message
	}
	if i+1 < len(l.rows) {
		next = l.rows[i+1].message
	}

	row.grouped = continuesGroup(prev, row.message)
	row.followed = continuesGroup(row.message, next)
	row.dayLabel = dayLabel(prev, row.message)
}

// rederive re-decides rows lo through hi, whose neighbours changed, and brings a
// mounted widget into line: a row that gained or lost its header is rebuilt, one
// whose bottom margin moved is told.
func (l *MessageList) rederive(lo, hi int) {
	for i := max(lo, 0); i <= min(hi, len(l.rows)-1); i++ {
		row := &l.rows[i]
		was := *row
		l.derive(i)

		if row.grouped == was.grouped && row.followed == was.followed && row.dayLabel == was.dayLabel {
			continue
		}

		w := l.mounted[row.message.ID]
		switch {
		case w == nil:
			row.measured = false
			row.height = l.estimate(row)
		case row.grouped != was.grouped || row.dayLabel != was.dayLabel:
			delete(l.mounted, row.message.ID) // rebuilt by the next mount
			row.measured = false
		default:
			w.SetFollowedByGroup(row.followed) // the layout measures the new height
		}
	}
}

// dropRows forgets every row and widget.
func (l *MessageList) dropRows() {
	l.rows = nil
	clear(l.mounted)
	l.first, l.last = 0, 0
	l.content.Objects = nil
	l.reindex()

	// The window this set was picked out of has gone — a channel switch, or a jump
	// that replaced it. The *mode* is the controller's and stays; keeping the IDs
	// would be holding a selection the reader can no longer see.
	if len(l.selected) > 0 {
		clear(l.selected)
		l.anchor = ""
		l.reportSelection()
	}
}

func (l *MessageList) clearStatus() {
	if l.status == nil {
		return
	}

	l.status = nil
	l.content.Objects = nil
	l.layout.slots = nil
	l.layout.total = 0
}

// sizeStatus gives the status line the visible height, so the room held for the
// dock does not push it low and leave the column scrollable by exactly that much.
func (l *MessageList) sizeStatus() {
	height := float32(400)
	if h := l.viewHeight() - DockReserve(l.dock) - 5; h > 100 {
		height = h
	}

	l.layout.slots = []slot{{height: height}}
	l.layout.total = height
	l.settle()
}

/* Mounting */

// settle re-lays the rows out and brings the scroller into line with them, then
// mounts for wherever the viewport now stands. Every mutation ends here.
func (l *MessageList) settle() {
	l.syncSlots()
	Relayout(l.content)
	l.syncScroll()
	l.mount(true)
}

// mount brings the window in line with the viewport, returning early on an
// unchanged range unless forced — which an ordinary wheel tick usually leaves
// it, and is what makes scrolling free.
func (l *MessageList) mount(force bool) {
	if l.status != nil {
		return
	}

	first, last := visibleRange(l.offsets, l.scroll.Offset.Y, l.viewHeight(), messageOverscan())
	if !force && first == l.first && last == l.last {
		return
	}
	l.first, l.last = first, last

	// The row being edited keeps its widget wherever it is: the draft lives in it.
	for id, w := range l.mounted {
		if i, ok := l.index[id]; ok && (i >= first && i < last || w.Editing()) {
			continue
		}
		delete(l.mounted, id)
	}

	objects := make([]fyne.CanvasObject, 0, last-first+1)
	for i := first; i < last; i++ {
		row := &l.rows[i]
		w, ok := l.mounted[row.message.ID]
		if !ok {
			w = l.build(row)
			l.mounted[row.message.ID] = w
		}
		objects = append(objects, w)
	}
	for id, w := range l.mounted {
		if i := l.index[id]; i < first || i >= last {
			objects = append(objects, w)
		}
	}
	l.content.Objects = objects

	l.syncSlots()
	Relayout(l.content)
	l.syncScroll()
}

// build makes the widget for a row, telling the controller first.
func (l *MessageList) build(row *windowRow) *MessageWidget {
	if l.OnMount != nil {
		l.OnMount(row.message, row.grouped)
	}

	w := NewMessageWidget(l.deps, row.message, row.dayLabel, row.grouped, row.followed)
	if l.selecting {
		w.SetSelecting(true, l.selected[row.message.ID])
	}

	return w
}

// syncSlots points each mounted object at its row's place in the column.
func (l *MessageList) syncSlots() {
	if l.status != nil {
		return
	}

	l.layout.slots = slices.Grow(l.layout.slots[:0], len(l.content.Objects))
	for _, obj := range l.content.Objects {
		var placed slot
		if w, ok := obj.(*MessageWidget); ok {
			if i, ok := l.index[w.message.ID]; ok {
				placed = slot{top: l.offsets[i], height: l.rows[i].height}
			}
		}
		l.layout.slots = append(l.layout.slots, placed)
	}
}

// syncScroll brings the scroller into line with the column's height and offset
// through its renderer alone — never Scroll.Refresh, which repaints every child.
func (l *MessageList) syncScroll() {
	l.scroll.Offset.Y = clamp(l.scroll.Offset.Y, 0, l.bottomOffset())
	l.scroll.SyncContent()
}

// measure reads the height of every mounted row at width, replacing its estimate
// or noticing that it grew. Run from the layout, so it is also how the driver's
// own re-layout — after an editor opened, a quote filled in, a card resolved —
// reaches the column. A height moving wholly above the viewport shifts the
// offset with it, and a column at its bottom is kept there.
func (l *MessageList) measure(objects []fyne.CanvasObject, width float32) {
	if width <= 0 || l.status != nil {
		return
	}
	if width != l.width {
		// Every body re-wraps at a new width. The old heights stand as estimates.
		l.width = width
		for i := range l.rows {
			l.rows[i].measured = false
		}
	}

	view := l.scroll.Offset.Y
	pinned := l.FromBottom() <= 0.5
	var shift float32
	moved := false

	for _, obj := range objects {
		w, ok := obj.(*MessageWidget)
		if !ok {
			continue
		}
		i, ok := l.index[w.message.ID]
		if !ok {
			continue
		}
		row := &l.rows[i]

		// A wrapping body answers MinSize with the width it was last laid out at, so
		// the row is given the column's width before it is asked.
		w.Resize(fyne.NewSize(width, row.height))
		height := w.MinSize().Height
		if row.measured && height == row.height {
			continue
		}

		if delta := height - row.height; delta != 0 {
			if l.offsets[i]+row.height <= view {
				shift += delta
			}
			moved = true
		}
		row.height = height
		row.measured = true
	}
	if !moved {
		return
	}

	l.reoffset()
	l.syncSlots()

	offset := view + shift
	if pinned {
		offset = l.bottomOffset()
	}
	l.scroll.Offset.Y = clamp(offset, 0, l.bottomOffset())
}

/* Layout */

/* Estimates */

// estimatedBodyWidth stands in before the column has a width.
const estimatedBodyWidth = 600

// estimate is a row's height before its widget exists, from what the message
// carries. It only has to be close: the scroll indicator is drawn from it, and
// measurement replaces it the moment the row is mounted. Pictures are the
// outliers worth getting right, and Revolt sends their dimensions.
func (l *MessageList) estimate(row *windowRow) float32 {
	message := row.message

	var height float32
	if row.dayLabel != "" {
		height += theme.Sizes.DaySeparatorTopPadding + theme.Sizes.DaySeparatorBottomPadding +
			lineHeight(theme.Sizes.DaySeparatorTextSize)
	}
	if message.System != nil {
		return height + theme.Sizes.SystemMessagePadding*2 + lineHeight(theme.Sizes.SystemMessageTextSize)
	}

	line := messageLineHeight()
	height += rowPad(row.grouped) + rowPad(row.followed)
	if !row.grouped {
		height += line
		if len(message.Replies) > 0 {
			height += float32(len(message.Replies))*lineHeight(replyPreviewTextSize) + theme.Sizes.MessageReplyBlockGap
		}
	}
	if message.Content != "" {
		height += float32(estimateLines(message.Content, l.bodyWidth())) * line
	}

	for i, attachment := range message.Attachments {
		if i > 0 {
			height += theme.Sizes.MessageAttachmentSpacing
		}
		height += attachmentHeight(attachment)
	}
	for _, embed := range message.Embeds {
		height += theme.Sizes.EmbedSpacing + embedHeight(embed)
	}
	if !row.scanned {
		row.codes, row.scanned = inviteCodesIn(message.Content), true
	}
	if len(row.codes) > 0 {
		height += inviteCardHeight
	}
	if len(message.Reactions) > 0 {
		height += theme.Sizes.MessageAttachmentSpacing + theme.Sizes.ReactionEmojiSize + theme.Sizes.ReactionPaddingV*2
	}

	return height
}

// rowPad is a row's top or bottom margin: tight against a continuation.
func rowPad(tight bool) float32 {
	if tight {
		return theme.Sizes.MessageGroupedVerticalPadding
	}

	return theme.Sizes.MessageVerticalPadding
}

// bodyWidth is the room a message's text wraps in.
func (l *MessageList) bodyWidth() float32 {
	if l.width <= 0 {
		return estimatedBodyWidth
	}

	return l.width - theme.Sizes.MessageHorizontalPadding*2 -
		theme.Sizes.MessageAvatarColumnWidth - theme.Sizes.MessageContentPadding
}

// glyphWidths memoises the average glyph estimateLines works from, per text
// size. Every row in the window is estimated on every change to it, and Fyne's
// own measurement cache is keyed on an interface holding the whole string — a
// hash of 27 characters, per row, for an answer that moves only with the theme.
// UI thread only, hence unsynchronised.
var glyphWidths = map[float32]float32{}

func glyphWidth(textSize float32) float32 {
	w, ok := glyphWidths[textSize]
	if !ok {
		w = fyne.MeasureText("abcdefghijklmnopqrstuvwxyz ", textSize, fyne.TextStyle{}).Width / 27
		glyphWidths[textSize] = w
	}

	return w
}

// estimateLines guesses how many lines text wraps into at width, from an average
// glyph — markdown and proportional widths make anything exact a layout.
func estimateLines(text string, width float32) int {
	glyph := glyphWidth(fynetheme.TextSize())
	perLine := max(int(width/max(glyph, 1)), 1)

	lines := 0
	for line := range strings.SplitSeq(text, "\n") {
		lines += max((utf8.RuneCountInString(line)+perLine-1)/perLine, 1)
	}

	return lines
}

// attachmentHeight is what buildAttachment reserves for one.
func attachmentHeight(attachment *domain.File) float32 {
	switch attachment.Kind {
	case domain.FileImage:
		size := fitWithin(attachment.Width, attachment.Height, theme.Sizes.MessageImageMaxWidth, theme.Sizes.MessageImageMaxHeight)
		if size.Height == 0 {
			size.Height = theme.Sizes.MessageImageMaxHeight / 2
		}

		return size.Height + attachmentBarHeight
	case domain.FileText:
		return attachmentTextHeight + attachmentBarHeight
	}

	return attachmentFileHeight + attachmentBarHeight
}

// embedHeight is roughly what buildEmbed draws: a line per part and the picture.
func embedHeight(embed *domain.Embed) float32 {
	height := theme.Sizes.EmbedPaddingV * 2
	if embed.SiteName != "" {
		height += lineHeight(theme.Sizes.EmbedSiteTextSize) + theme.Sizes.EmbedRowGap
	}
	if embed.Title != "" {
		height += lineHeight(theme.Sizes.EmbedTitleTextSize) + theme.Sizes.EmbedRowGap
	}
	if embed.Description != "" {
		height += 2 * messageLineHeight()
	}
	if embed.Image != nil {
		size := fitWithin(embed.Image.Width, embed.Image.Height, theme.Sizes.EmbedMaxWidth, theme.Sizes.EmbedImageMaxHeight)
		if size.Height == 0 {
			size.Height = theme.Sizes.EmbedImageMaxHeight / 2
		}
		height += size.Height + theme.Sizes.EmbedRowGap
	}

	return height
}

// inviteCardHeight is about what an invite card stands, which nothing here can
// ask before the card exists.
const inviteCardHeight = 120

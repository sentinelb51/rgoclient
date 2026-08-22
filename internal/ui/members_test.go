package ui

import (
	"image"
	"image/color"
	"strconv"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
)

/* The model */

// member is a resolved membership at a presence, named so the ordering
// assertions read as sentences.
func member(name string, presence domain.Presence, hoistRoleID string) domain.Member {
	return domain.Member{
		UserID:      strings.ToLower(name),
		Name:        name,
		Presence:    presence,
		HoistRoleID: hoistRoleID,
	}
}

// summarise flattens a model into one string per entry, so a test says what the
// sidebar would read rather than indexing into it.
func summarise(entries []MemberEntry) []string {
	out := make([]string, len(entries))
	for i, entry := range entries {
		if entry.Kind == MemberEntrySection {
			out[i] = "# " + entry.Title
			continue
		}
		out[i] = entry.Member.Name
	}

	return out
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}

	return true
}

var hoistedRoles = []domain.Role{
	{ID: "owner", Name: "Owner", Rank: 0, Hoist: true},
	{ID: "mod", Name: "Moderators", Rank: 5, Hoist: true},
}

// TestMemberModelBuckets is the rule the whole sidebar is read by: hoisted
// sections in rank order, then Online, then Offline, with an empty bucket
// emitting nothing at all. Every one of these has a way of being wrong that
// nobody would spot from a screenshot.
func TestMemberModelBuckets(t *testing.T) {
	opts := MemberListOptions{GroupByPresence: true, HoistRoles: true}

	// Deliberately not in section order: bucketing has to do the grouping, and
	// within a bucket the store's order has to survive it.
	members := []domain.Member{
		member("Ada", domain.PresenceOnline, "mod"),
		member("Bo", domain.PresenceIdle, ""),
		member("Cy", domain.PresenceOffline, "owner"), // offline: not in their section
		member("Di", domain.PresenceBusy, "owner"),
		member("Ed", domain.PresenceOffline, ""),
		member("Fi", domain.PresenceOnline, "mod"),
	}

	want := []string{
		"# Owner — 1", "Di",
		"# Moderators — 2", "Ada", "Fi",
		"# Online — 1", "Bo",
		"# Offline — 2", "Cy", "Ed",
	}
	if got := summarise(NewMemberModel(members, hoistedRoles, opts)); !equal(got, want) {
		t.Errorf("model = %v\nwant   %v", got, want)
	}
}

// TestMemberModelDropsEmptySections covers the header without a section under
// it: a server whose moderators are all offline has no Moderators section, not
// an empty one saying zero.
func TestMemberModelDropsEmptySections(t *testing.T) {
	opts := MemberListOptions{GroupByPresence: true, HoistRoles: true}
	members := []domain.Member{
		member("Cy", domain.PresenceOffline, "owner"),
		member("Bo", domain.PresenceOnline, ""),
	}

	want := []string{"# Online — 1", "Bo", "# Offline — 1", "Cy"}
	if got := summarise(NewMemberModel(members, hoistedRoles, opts)); !equal(got, want) {
		t.Errorf("model = %v\nwant   %v", got, want)
	}
}

// TestMemberModelCountsMatchTheirSections is the invariant a count exists for:
// each header names exactly how many rows follow it before the next one.
func TestMemberModelCountsMatchTheirSections(t *testing.T) {
	members := []domain.Member{
		member("Ada", domain.PresenceOnline, "mod"),
		member("Bo", domain.PresenceOnline, ""),
		member("Cy", domain.PresenceOffline, ""),
		member("Di", domain.PresenceFocus, "mod"),
		member("Ed", domain.PresenceOffline, ""),
	}

	entries := NewMemberModel(members, hoistedRoles, MemberListOptions{GroupByPresence: true, HoistRoles: true})

	var title string
	var counted int
	check := func() {
		if title == "" {
			return
		}
		if want := title[strings.LastIndex(title, " ")+1:]; want != strconv.Itoa(counted) {
			t.Errorf("%q is followed by %d rows", title, counted)
		}
	}

	for _, entry := range entries {
		if entry.Kind == MemberEntrySection {
			check()
			title, counted = entry.Title, 0
			continue
		}
		counted++
	}
	check()
}

// TestMemberModelHidesOffline covers the setting that does the most on a large
// server, where most of a membership is offline.
func TestMemberModelHidesOffline(t *testing.T) {
	members := []domain.Member{
		member("Ada", domain.PresenceOnline, ""),
		member("Cy", domain.PresenceOffline, ""),
	}

	opts := MemberListOptions{GroupByPresence: true, HoistRoles: true, HideOffline: true}
	want := []string{"# Online — 1", "Ada"}
	if got := summarise(NewMemberModel(members, hoistedRoles, opts)); !equal(got, want) {
		t.Errorf("model = %v\nwant   %v", got, want)
	}

	// And ungrouped, where there is no section to drop — only rows.
	opts = MemberListOptions{HideOffline: true}
	if got := summarise(NewMemberModel(members, hoistedRoles, opts)); !equal(got, []string{"Ada"}) {
		t.Errorf("ungrouped model = %v, want [Ada]", got)
	}
}

// TestMemberModelHidesRoleless covers the other filter, and the one thing that
// makes it different from hiding the offline: it is not answered by HoistRoleID.
// A member holding only an unhoisted role has none, and hiding them would empty
// a list on every server that hoists nothing.
func TestMemberModelHidesRoleless(t *testing.T) {
	mod := member("Ada", domain.PresenceOnline, "mod")
	mod.HasRoles = true
	unhoisted := member("Bo", domain.PresenceOnline, "")
	unhoisted.HasRoles = true

	members := []domain.Member{mod, unhoisted, member("Cy", domain.PresenceOnline, "")}

	opts := MemberListOptions{GroupByPresence: true, HoistRoles: true, HideRoleless: true}
	want := []string{"# Moderators — 1", "Ada", "# Online — 1", "Bo"}
	if got := summarise(NewMemberModel(members, hoistedRoles, opts)); !equal(got, want) {
		t.Errorf("model = %v\nwant   %v", got, want)
	}

	// And ungrouped, which filters on its own walk.
	opts = MemberListOptions{HideRoleless: true}
	if got := summarise(NewMemberModel(members, hoistedRoles, opts)); !equal(got, []string{"Ada", "Bo"}) {
		t.Errorf("ungrouped model = %v, want [Ada Bo]", got)
	}
}

// TestMemberModelFallsBackToEveryone covers the setting that exists because an
// empty sidebar is indistinguishable from a broken one: the filters are set on
// some other server and then a server they leave nothing of is opened.
func TestMemberModelFallsBackToEveryone(t *testing.T) {
	members := []domain.Member{
		member("Ada", domain.PresenceOffline, ""),
		member("Bo", domain.PresenceOffline, ""),
	}

	opts := MemberListOptions{GroupByPresence: true, HideOffline: true, HideRoleless: true}
	if got := summarise(NewMemberModel(members, hoistedRoles, opts)); len(got) != 0 {
		t.Fatalf("model = %v, want nothing without the fallback", got)
	}

	opts.FallbackToAll = true
	want := []string{"# Offline — 2", "Ada", "Bo"}
	if got := summarise(NewMemberModel(members, hoistedRoles, opts)); !equal(got, want) {
		t.Errorf("model = %v\nwant   %v", got, want)
	}

	// It is the filters it undoes, not the shape of the list: an ungrouped list
	// falls back to an ungrouped one.
	opts = MemberListOptions{HideOffline: true, FallbackToAll: true}
	if got := summarise(NewMemberModel(members, hoistedRoles, opts)); !equal(got, []string{"Ada", "Bo"}) {
		t.Errorf("ungrouped model = %v, want [Ada Bo]", got)
	}

	// A server that really is empty stays empty — the fallback answers a filter,
	// not a fetch that has not landed.
	if got := NewMemberModel(nil, hoistedRoles, opts); len(got) != 0 {
		t.Errorf("model = %v, want nothing for a membership nobody has", summarise(got))
	}

	// And one nothing was hiding is not walked twice to arrive at what it already
	// had.
	opts = MemberListOptions{GroupByPresence: true, FallbackToAll: true}
	if got := summarise(NewMemberModel(members, hoistedRoles, opts)); !equal(got, want) {
		t.Errorf("model = %v\nwant   %v", got, want)
	}
}

// TestMemberModelUngroupedHasNoSections covers what turning the presence split
// off has always meant — one run, hoisting included, which is otherwise easy to
// leave half-applied.
func TestMemberModelUngroupedHasNoSections(t *testing.T) {
	members := []domain.Member{
		member("Ada", domain.PresenceOnline, "mod"),
		member("Cy", domain.PresenceOffline, "owner"),
	}

	opts := MemberListOptions{HoistRoles: true}
	if got := summarise(NewMemberModel(members, hoistedRoles, opts)); !equal(got, []string{"Ada", "Cy"}) {
		t.Errorf("model = %v, want [Ada Cy]", got)
	}
}

// TestMemberModelWithoutHoistingKeepsTwoSections covers the hoisting toggle on
// its own: the role sections collapse into Online, and nothing else moves.
func TestMemberModelWithoutHoistingKeepsTwoSections(t *testing.T) {
	members := []domain.Member{
		member("Ada", domain.PresenceOnline, "mod"),
		member("Bo", domain.PresenceOnline, ""),
	}

	opts := MemberListOptions{GroupByPresence: true}
	want := []string{"# Online — 2", "Ada", "Bo"}
	if got := summarise(NewMemberModel(members, hoistedRoles, opts)); !equal(got, want) {
		t.Errorf("model = %v\nwant   %v", got, want)
	}
}

/* Geometry */

// TestVisibleRangeWindowsTheModel covers the arithmetic the whole list rests on.
// It is index arithmetic over two different heights, which is exactly the kind
// of thing that is off by one in a way nobody sees until a row is missing at the
// top of a fling.
func TestVisibleRangeWindowsTheModel(t *testing.T) {
	entries := []MemberEntry{
		{Kind: MemberEntrySection},
		{Kind: MemberEntryRow}, {Kind: MemberEntryRow}, {Kind: MemberEntryRow},
		{Kind: MemberEntrySection},
		{Kind: MemberEntryRow}, {Kind: MemberEntryRow},
	}
	offsets := memberOffsets(entries)

	section, row := theme.Sizes.MemberSectionHeight, theme.Sizes.MemberRowHeight
	total := offsets[len(offsets)-1]

	cases := []struct {
		name               string
		y, height          float32
		overscan           int
		wantFirst, wantEnd int
	}{
		{"the header alone", 0, section, 0, 0, 1},
		{"a taller viewport pulls in the row under it", 0, row, 0, 0, 2},
		{"a viewport taller than the model", 0, total * 2, 0, 0, len(entries)},
		{"inside the second header", section + row*2.5, row, 0, 3, 5},
		{"overscan widens both ends", section + row*2.5, row, 2, 1, 7},
		{"overscan clamps to the model", 0, row, 10, 0, len(entries)},
		{"past the end", total + row, row, 0, len(entries), len(entries)},
		{"a viewport of no height", 0, 0, 0, 0, 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			first, end := visibleRange(offsets, c.y, c.height, c.overscan)
			if first != c.wantFirst || end != c.wantEnd {
				t.Errorf("visibleRange = (%d, %d), want (%d, %d)", first, end, c.wantFirst, c.wantEnd)
			}
		})
	}

	if first, end := visibleRange(memberOffsets(nil), 0, 100, 4); first != 0 || end != 0 {
		t.Errorf("empty model: visibleRange = (%d, %d), want (0, 0)", first, end)
	}
}

/* Row recycling */

// TestMemberRowDropsAPictureForSomebodyElse is the guard recycling cannot do
// without. A picture is fetched off the UI thread and delivered later; by then
// the row that asked for it may be drawing a different person, and painting it
// anyway puts one member's face beside another's name.
func TestMemberRowDropsAPictureForSomebodyElse(t *testing.T) {
	test.NewTempApp(t)

	side := fyne.NewSize(theme.Sizes.MemberAvatarSize, theme.Sizes.MemberAvatarSize)
	picture := image.NewRGBA(image.Rect(0, 0, 4, 4))

	row := newMemberRow(testDeps(), nil)
	row.SetMember(&domain.Member{UserID: "ada", Name: "Ada", AvatarURL: "https://cdn.invalid/a"})
	stale := row.generation

	row.SetMember(&domain.Member{UserID: "bo", Name: "Bo", AvatarURL: "https://cdn.invalid/b"})
	if row.generation == stale {
		t.Fatal("recycling the row onto somebody else did not bump the generation")
	}

	// Ada's picture, arriving after the row moved on.
	row.paintAvatar(stale, picture, side)
	if _, painted := row.avatar.Objects[0].(*canvas.Image); painted {
		t.Error("a picture for the previous member was drawn into the recycled row")
	}

	// Bo's own picture still lands.
	row.paintAvatar(row.generation, picture, side)
	if _, painted := row.avatar.Objects[0].(*canvas.Image); !painted {
		t.Error("the row's own picture was dropped too")
	}

	// So does releasing the row back to the pool: a load still in flight for it
	// must not paint into whoever it is handed to next.
	pooled := row.generation
	row.release()
	row.paintAvatar(pooled, picture, side)
	if _, painted := row.avatar.Objects[0].(*canvas.Image); painted {
		t.Error("a picture landed in a row that had been released")
	}
}

// TestMemberRowKeepsOnePlaceholder covers what a recycled row cannot do: swap in
// a *new* placeholder circle. Fyne only learns of an object when the container
// holding it is refreshed, so a row that quietly replaced the circle drew no
// avatar at all — which is every row on a server where nobody has one.
func TestMemberRowKeepsOnePlaceholder(t *testing.T) {
	test.NewTempApp(t)

	side := fyne.NewSize(theme.Sizes.MemberAvatarSize, theme.Sizes.MemberAvatarSize)

	row := newMemberRow(testDeps(), nil)
	row.SetMember(&domain.Member{UserID: "ada", Name: "Ada", AvatarURL: "https://cdn.invalid/a"})
	row.paintAvatar(row.generation, image.NewRGBA(image.Rect(0, 0, 4, 4)), side)

	row.release()
	row.SetMember(&domain.Member{UserID: "bo", Name: "Bo"}) // no avatar of their own

	if len(row.avatar.Objects) != 1 || row.avatar.Objects[0] != fyne.CanvasObject(row.placeholder) {
		t.Fatal("a recycled row without an avatar does not hold the placeholder it was built with")
	}
}

// TestMemberRowSetMemberNoOps covers what makes both scrolling and a whole-model
// repaint cheap: a row asked to draw the person it is already drawing must not
// touch anything, and in particular must not restart its picture.
func TestMemberRowSetMemberNoOps(t *testing.T) {
	test.NewTempApp(t)

	row := newMemberRow(testDeps(), nil)
	ada := domain.Member{
		UserID:    "ada",
		Name:      "Ada",
		AvatarURL: "https://cdn.invalid/a",
		Presence:  domain.PresenceOnline,
		Color:     color.NRGBA{R: 255, A: 255},
	}

	row.SetMember(&ada)
	settled := row.generation

	row.SetMember(&ada)
	if row.generation != settled {
		t.Error("an unchanged member restarted the avatar load")
	}

	// A presence change alone must not restart it either — that is the event this
	// list sees most.
	ada.Presence = domain.PresenceIdle
	row.SetMember(&ada)
	if row.generation != settled {
		t.Error("a presence change restarted the avatar load")
	}
	if row.presenceBar.FillColor != presenceColor(domain.PresenceIdle) {
		t.Error("the presence bar did not follow the change")
	}
}

// TestMemberRowNameSurvivesTruncation covers the trap in re-labelling a recycled
// row: ellipsisLayout rewrites the text object during layout, so a row that
// compared against what the object says would take a shortened name for the
// real one and then refuse to redraw it.
func TestMemberRowNameSurvivesTruncation(t *testing.T) {
	test.NewTempApp(t)

	row := newMemberRow(testDeps(), nil)
	long := strings.Repeat("Alexandra", 6)

	row.SetMember(&domain.Member{UserID: "ada", Name: long})
	row.nameBox.Resize(fyne.NewSize(40, theme.Sizes.MemberRowHeight))

	if row.name.Text == long {
		t.Fatal("the name was not shortened to the column, so the trap is not being tested")
	}
	if row.fullName != long {
		t.Errorf("fullName = %q, want the whole name", row.fullName)
	}

	row.SetMember(&domain.Member{UserID: "bo", Name: "Bo"})
	if row.fullName != "Bo" {
		t.Errorf("fullName = %q after recycling, want Bo", row.fullName)
	}
}

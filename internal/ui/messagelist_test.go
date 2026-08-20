package ui

// The message column: the grouping rules a row is derived by, exercised as the
// pure functions they are, and what every mutation of the window owes the
// reader — whatever they are looking at stays where it was.

import (
	"image/color"
	"math"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"github.com/oklog/ulid/v2"

	"RGOClient/internal/domain"
)

// at builds a message ID whose ULID timestamp is t — which is where every
// grouping and day decision below actually comes from, messages carrying no
// timestamp of their own.
func at(t time.Time) string {
	return ulid.MustNew(ulid.Timestamp(t), ulid.DefaultEntropy()).String()
}

// message is a plain message by author, sent at t.
func message(author string, t time.Time) *domain.Message {
	return &domain.Message{ID: at(t), ChannelID: "01CHANNEL", AuthorID: author, Content: "hi"}
}

func TestContinuesGroup(t *testing.T) {
	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.Local)
	prev := message("01ELYNN", base)
	window := messageGroupWindow()

	cases := []struct {
		name string
		curr *domain.Message
		want bool
	}{
		{"same author, moments later", message("01ELYNN", base.Add(time.Minute)), true},
		{"same author, at the window's edge", message("01ELYNN", base.Add(window)), true},
		{"same author, past the window", message("01ELYNN", base.Add(window+time.Minute)), false},
		{"a different author", message("01SAREN", base.Add(time.Minute)), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := continuesGroup(prev, tc.curr); got != tc.want {
				t.Errorf("continuesGroup = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("nothing above", func(t *testing.T) {
		if continuesGroup(nil, message("01ELYNN", base)) {
			t.Error("a message with no predecessor cannot continue a group")
		}
	})

	// A reply always opens a group: it carries a quote above it, which a headerless
	// continuation has nowhere to put.
	t.Run("a reply", func(t *testing.T) {
		reply := message("01ELYNN", base.Add(time.Minute))
		reply.Replies = []string{"01OTHER"}
		if continuesGroup(prev, reply) {
			t.Error("a reply should start its own group")
		}
	})

	// Neither side may be anything but a person: a system line, a webhook post and
	// a masquerade are all somebody else wearing the author's ID.
	t.Run("not a person", func(t *testing.T) {
		system := message("01ELYNN", base.Add(time.Minute))
		system.System = &domain.SystemMessage{Kind: domain.SystemUserJoined}

		webhook := message("01ELYNN", base.Add(time.Minute))
		webhook.Webhook = &domain.Webhook{Name: "CI"}

		masked := message("01ELYNN", base.Add(time.Minute))
		masked.Masquerade = true

		for name, curr := range map[string]*domain.Message{
			"system": system, "webhook": webhook, "masquerade": masked,
		} {
			if continuesGroup(prev, curr) {
				t.Errorf("%s should start its own group", name)
			}
			if continuesGroup(curr, message("01ELYNN", base.Add(2*time.Minute))) {
				t.Errorf("nothing should continue a %s", name)
			}
		}
	})

	// A pinned message opens a group because its mark rides the name line, which is
	// the one thing a continuation does not draw — grouped, pinning it would show
	// nothing. Only the pinned message itself is affected: the row after it groups
	// or not on its own merits.
	t.Run("a pinned message", func(t *testing.T) {
		pinned := message("01ELYNN", base.Add(time.Minute))
		pinned.Pinned = true
		if continuesGroup(prev, pinned) {
			t.Error("a pinned message should start its own group")
		}
		if !continuesGroup(pinned, message("01ELYNN", base.Add(2*time.Minute))) {
			t.Error("a pin should not stop the next message continuing under it")
		}
	})

	// Midnight breaks the group even inside the time window, because the day
	// separator is drawn between the two and a headerless block cannot straddle it.
	t.Run("across midnight", func(t *testing.T) {
		before := message("01ELYNN", time.Date(2026, 7, 14, 23, 59, 0, 0, time.Local))
		after := message("01ELYNN", time.Date(2026, 7, 15, 0, 1, 0, 0, time.Local))
		if continuesGroup(before, after) {
			t.Error("a day separator must break the group")
		}
	})
}

func TestDayLabel(t *testing.T) {
	noon := time.Date(2026, 7, 14, 12, 0, 0, 0, time.Local)
	first := message("01ELYNN", noon)

	if dayLabel(nil, first) == "" {
		t.Error("a message with no predecessor opens its day and must be dated")
	}
	if got := dayLabel(first, message("01ELYNN", noon.Add(time.Hour))); got != "" {
		t.Errorf("same day got the label %q, want none", got)
	}

	next := message("01ELYNN", time.Date(2026, 7, 15, 9, 0, 0, 0, time.Local))
	if dayLabel(first, next) == "" {
		t.Error("a new calendar day must be dated")
	}
}

/* Anchoring */

// column is n messages in runs of three per author, a minute apart from base and
// of varying length, so no two rows stand the same height: an offset that moved
// by the wrong row is otherwise easy to mistake for the right one.
func column(base time.Time, n int, authors ...string) []*domain.Message {
	messages := make([]*domain.Message, n)
	for i := range n {
		messages[i] = message(authors[(i/3)%len(authors)], base.Add(time.Duration(i)*time.Minute))
		messages[i].Content = strings.Repeat("a line of something somebody said ", 1+i%5)
	}

	return messages
}

// talk is one message per author named, a minute apart from base — a window
// small enough to write every seam in it out by hand.
func talk(base time.Time, authors ...string) []*domain.Message {
	messages := make([]*domain.Message, len(authors))
	for i, author := range authors {
		messages[i] = message(author, base.Add(time.Duration(i)*time.Minute))
	}

	return messages
}

// mountList opens messages in a column with a real viewport. Nothing below works
// without one: which rows are mounted, and so which heights are measured and
// which are still estimates, is what all of this is arithmetic over.
func mountList(t *testing.T, messages []*domain.Message) *MessageList {
	t.Helper()

	test.NewTempApp(t)

	dock := canvas.NewRectangle(color.Transparent)
	dock.SetMinSize(fyne.NewSize(400, 80))

	list := NewMessageList(testDeps(), dock)
	window := test.NewWindow(list)
	window.SetPadded(false)
	window.Resize(fyne.NewSize(700, 500))
	t.Cleanup(window.Close)

	list.SetMessages(messages)

	return list
}

// anchored is where the reader is: the message the top of the viewport stands
// in, and how far into it. A mutation that adds or removes nothing on screen has
// to leave both alone.
func anchored(l *MessageList) (id string, into float32) {
	first, _ := visibleRange(l.offsets, l.scroll.Offset.Y, l.viewHeight(), 0)
	if first >= len(l.rows) {
		return "", 0
	}

	return l.rows[first].message.ID, l.scroll.Offset.Y - l.offsets[first]
}

// stillAnchored fails unless the viewport stands where it did. Half a pixel of
// tolerance: the offset and the row tops are the same float32s added up in a
// different order, and nothing a reader could see hides under it.
func stillAnchored(t *testing.T, l *MessageList, id string, into float32) {
	t.Helper()

	gotID, gotInto := anchored(l)
	if gotID != id {
		t.Errorf("the viewport stands in row %d, not the one now at row %d", l.Index(gotID), l.Index(id))
		return
	}
	if drift := gotInto - into; math.Abs(float64(drift)) > 0.5 {
		t.Errorf("the column slid %.1f under the reader", drift)
	}
}

// TestMessageListPrependKeepsTheViewport is what paging history in has to do: a
// page lands above the reader and nothing they are reading moves. The offset
// owes the page its own height — and then the difference again when a row above
// them turns out to stand taller or shorter than it was estimated at, which the
// row the page groups onto always does.
func TestMessageListPrependKeepsTheViewport(t *testing.T) {
	base := time.Date(2026, 7, 14, 9, 0, 0, 0, time.Local)
	list := mountList(t, column(base, 60, "Ada", "Bo"))

	// Where the loader fires: a few rows short of the top.
	list.scrollTo(list.offsets[3] + 10)
	id, into := anchored(list)

	// 39 messages ends the page on Ada a minute before the row that was at the
	// top, so that row loses both its header and its date — a height change of its
	// own for the offset to absorb along with the page's.
	list.Prepend(column(base.Add(-39*time.Minute), 39, "Ada", "Bo"))

	if list.Len() != 99 {
		t.Fatalf("window holds %d rows, want 99", list.Len())
	}
	if list.rows[39].dayLabel != "" || !list.rows[39].grouped {
		t.Fatal("the page did not land on the seam this is about")
	}
	stillAnchored(t, list, id, into)
}

// TestMessageListRemoveMovesTheColumnUnderTheReader covers the other direction:
// a message deleted above the viewport takes its height out of the column, and
// the offset has to come with it or everything on screen slides down by that
// row. One deleted below the reader must not move the column at all — the same
// comparison, read the other way.
func TestMessageListRemoveMovesTheColumnUnderTheReader(t *testing.T) {
	base := time.Date(2026, 7, 14, 9, 0, 0, 0, time.Local)
	list := mountList(t, column(base, 60, "Ada", "Bo"))

	list.scrollTo(list.offsets[30] + 10)
	id, into := anchored(list)

	// Both are the middle of a run, so the seam each leaves groups exactly as it
	// did and what moves is the row's own height and nothing else.
	above, below := list.Message(13).ID, list.Message(58).ID

	if removed := list.Remove(map[string]bool{above: true}); removed != 1 {
		t.Fatalf("Remove reported %d rows, want 1", removed)
	}
	if list.Index(above) != -1 {
		t.Error("the deleted message is still in the window")
	}
	stillAnchored(t, list, id, into)

	list.Remove(map[string]bool{below: true})
	stillAnchored(t, list, id, into)
}

// shape flattens the window into what each row draws: the author, "+" when the
// row is a continuation, ">" when the row under it continues this one, and a
// leading "#" when it opens a day. Those four are the whole of what a seam
// decides.
func shape(l *MessageList) []string {
	out := make([]string, len(l.rows))
	for i := range l.rows {
		row := &l.rows[i]

		s := row.message.AuthorID
		if row.grouped {
			s = "+" + s
		}
		if row.followed {
			s += ">"
		}
		if row.dayLabel != "" {
			s = "# " + s
		}
		out[i] = s
	}

	return out
}

func shapedLike(t *testing.T, l *MessageList, want []string) {
	t.Helper()

	if got := shape(l); !equal(got, want) {
		t.Errorf("window = %v\nwant     %v", got, want)
	}
}

// TestMessageListRederivesSeams covers the half of a mutation that is not
// arithmetic. A row's header, date and bottom margin are decided by the rows
// either side of it, so every edge the window opens or closes has to be decided
// again — and only that edge, the rest of the window having no idea anything
// happened. A seam missed here is a headerless message under a stranger's name,
// or a day that starts twice.
func TestMessageListRederivesSeams(t *testing.T) {
	base := time.Date(2026, 7, 14, 9, 0, 0, 0, time.Local)

	t.Run("a page above regroups the row it lands on", func(t *testing.T) {
		list := mountList(t, talk(base, "Ada", "Ada", "Bo"))
		shapedLike(t, list, []string{"# Ada>", "+Ada", "Bo"})

		list.Prepend(talk(base.Add(-2*time.Minute), "Ada"))
		shapedLike(t, list, []string{"# Ada>", "+Ada>", "+Ada", "Bo"})
	})

	t.Run("a message below extends the group above it", func(t *testing.T) {
		list := mountList(t, talk(base, "Ada", "Bo"))
		shapedLike(t, list, []string{"# Ada", "Bo"})

		// The row that was last is now followed, which is a margin and nothing else
		// — the one seam with no header or date to give it away.
		list.Append(talk(base.Add(2*time.Minute), "Bo"))
		shapedLike(t, list, []string{"# Ada", "Bo>", "+Bo"})
	})

	t.Run("a group head that goes hands its header on", func(t *testing.T) {
		list := mountList(t, talk(base, "Ada", "Ada", "Ada", "Bo"))
		shapedLike(t, list, []string{"# Ada>", "+Ada>", "+Ada", "Bo"})

		list.Remove(map[string]bool{list.Message(0).ID: true})
		shapedLike(t, list, []string{"# Ada>", "+Ada", "Bo"})
	})

	t.Run("a run that goes closes the gap it leaves", func(t *testing.T) {
		list := mountList(t, talk(base, "Ada", "Bo", "Bo", "Ada"))
		shapedLike(t, list, []string{"# Ada", "Bo>", "+Bo", "Ada"})

		// Both removals leave the same seam, and the rows either side of it are a
		// group they were never in.
		list.Remove(map[string]bool{list.Message(1).ID: true, list.Message(2).ID: true})
		shapedLike(t, list, []string{"# Ada>", "+Ada"})
	})

	t.Run("the row trimming leaves at the top", func(t *testing.T) {
		list := mountList(t, talk(base, "Ada", "Ada", "Bo"))

		list.TrimTop(1)
		shapedLike(t, list, []string{"# Ada", "Bo"})
	})
}

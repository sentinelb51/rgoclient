# internal/ui

Widgets, layouts and theme. Imports `cache`, `config`, `domain`, `markdown`,
`util` — never `revoltgo`, never `app`. See the root `CLAUDE.md` for the DAG,
naming and the test policy.

## Fyne footguns

- **What a `Refresh()` actually costs, and what `Relayout` doesn't.**
  `Container.Refresh` recurses into every child, so each descendant is queued
  and its per-object texture freed — every `canvas.Image` under it re-uploads.
  `ui.Relayout` queues only the container: the walk that frees still descends,
  but skips images, and a `canvas.Text`'s glyph texture is **content-keyed**
  (`cache.GetTextTexture`), so no refresh of any ancestor frees it — only the
  text itself changing does. Consequences: prefer `Relayout` after writing
  `Objects` or flipping a child's visibility (the emoji picker, the mention
  picker, `ChannelWidget.SetState` are the worked examples); guard leaf
  refreshes on the value they are about to write (`setHighlighted`,
  `searchChip.paint`); and never follow `widget.Entry`'s own typing methods
  with a second refresh (`MessageInput` and `EditEntry` both say why).
- **Innermost object wins.** Fyne delivers hover and pointer events to the
  deepest object that accepts them. Do *not* implement `desktop.Hoverable` with
  no-op methods — an inner widget that accepts hover steals it from its parent
  row (why `ui.Avatar` isn't hoverable). A control that *has* to sit inside a
  hovering card hands the hover back: the pins card's unpin button re-lights the
  card through `OutlinedIconButton.reporting`, or the card goes out from under
  the hand reaching for it. Anything interactive inside a message
  row is passed `MessageWidget.TappedSecondary` at construction: the avatar, each
  attachment, the reply preview, the embed title. The body is the awkward one — a
  selectable `widget.Label` mounts an unexported selection overlay that answers
  right-clicks itself, so `ui.bodyText` lays a `selectionCatcher` over it
  (right-clicks stop there, press/drag/tap forward down). If a future Fyne stops
  exposing the overlay, `newSelectionCatcher` returns nil and the body is a plain
  selectable Label.
- **Embed cards are inert containers, not widgets**, so hover and right-click
  reach the message row underneath. Only the title (`embedLink`, its own type
  because `TappableContainer` is hoverable) and the picture are widgets. Wrapping
  text can't be asked how wide it wants to be — it answers with whatever it was
  last given — so `embedContentWidth` measures it as one unbroken line and caps
  it at `EmbedMaxWidth`.
- **One hairline draws every edge.** `theme.Colors.Outline` at
  `theme.Sizes.OutlineWidth` is the *only* border in the client. A card sized by
  its own padding wears it on its background (`ui.Outline(rect)`); a card whose
  content reaches its edge — a picture — needs it on a rectangle stacked *over*
  the content, which is what `HoverableStack`'s rectangle is for. Columns carry
  theirs as a `ui.NewColumnDivider` *inside* their own fixed width, because the
  main row addresses children by position to find the one that stretches (the
  member sidebar's sits on its left so it disappears with the column). The colour
  must stay darker than every surface it is laid against, including
  `MessageHoverBackground`, since a row's hover fill paints *under* any card the
  row contains; because the outline is drawn at rest, hover must *lift* it —
  `AttachmentHoverBorder` is lighter than what it replaces. Fyne's *ambient*
  shadow is off (`AppTheme` answers `ColorNameShadow` with `color.Transparent`):
  a scroll paints it as a gradient along whichever edge has more content past it,
  a smear rather than a line.
- **A context menu is the client's own pop-up.** `widget.PopUpMenu` paints its
  background inside `widget.Menu`'s renderer, and `NewMenu` pins the widget's
  impl, so neither the stroke nor a composed renderer can reach it.
  `ui.contextMenu` puts the menu in a plain `widget.PopUp` with the hairline
  stacked over it, and carries what `PopUpMenu` did around the menu: clamping the
  position into the canvas, and the arrow/Enter/Escape handling — all exported
  `Menu` calls. It takes canvas focus while it is up, which keeps Escape off
  `App.bindKeys`. Its edge is `Colors.MenuOutline`, *lighter* than the client's
  one hairline for the same reason the settings island's is: a menu hangs over
  whatever was being read rather than meeting a surface, so a groove around it is
  an edge nobody sees.
- **A settings dropdown is not a menu at all.** `widget.Menu` lays its items out
  at their own minimum whatever the pop-up around them is resized to — the flag
  that would stretch them, `customSized`, is unexported and set only by
  `NewPopUpMenu` — so a menu held open to a wider control drew rows the width of
  its longest label, and a hovered one highlighted a strip adrift inside the box.
  `ui.dropdownList` is the client's own rows in a plain `widget.PopUp` instead,
  which is also what lets them alternate (`Colors.MenuStripeBg`). It carries the
  same clamping, focus and key handling `contextMenu` does. Its width is set by
  `MinSize` — the pop-up takes its size from what it holds — and its corners take
  `SizeNamePopupRadius`, the radius of the surface the pop-up paints underneath:
  a row fill outside that curve is a square nub the border then draws around.
  The rows carry it per corner (`canvas.Rectangle.TopLeftCornerRadius`), first
  and last only.
- **`ColorNameFocus` is a menu's hover fill, not a focus ring.** Nothing the
  client mounts draws a focus ring, and `widget.Menu` paints the item under the
  pointer with it, so `AppTheme` answers it with `Colors.MenuHoverBg` rather than
  the accent `ColorNamePrimary` still gets: an accent block following the pointer
  down a menu reads as the option already chosen.
- **A submenu is drawn by the menu, not by the pop-up around it.** `fyne.MenuItem`
  with a `ChildMenu` works inside `ui.contextMenu` — `widget.Menu` appends the
  child to its *own* renderer objects and places it beside the item — but the
  child is outside the `widget.PopUp` the parent was put in, so the hairline
  `newContextMenu` stacks over that pop-up does not reach it. A submenu therefore
  wears Fyne's own menu background and no border; the member menu's roles and
  timeout spans are the two that use one, both being lists too long to flatten
  into the menu itself. A **range** is not a list and has no menu shape at all,
  so the item opens a card instead: `NewSliderCard`, hung beside the row through
  `App.showPopover`. Its title is the fill slot and ellipsises, being somebody's
  name as often as a word, and its `Icon` says what *kind* of value the card is
  about where the title can only say whose. A range with a natural resting point
  takes `Pivot` (`Slider.SetPivot`): each side of it gets half the travel and a
  notch marks it, so unity on a −40..+20 gain sits in the middle rather than two
  thirds along, and a drag within half a knob of it snaps there. Its track is recoloured (`Slider.SetTrack`) because the
  default one is `ChannelSelectedBg`, the very colour a lifted card is — on
  `NoticeBg` the unfilled travel vanished and the bottom of the range drew as a
  lone knob, which is the same failure Fyne's own slider has here.
- **Four cards are elevated.** `ui.Elevate` casts a `canvas.Shadow`: the composer
  dock, the settings page's invite card, the call island (through
  `elevate(rect, blur)`, at half again the blur — the dock meets the message
  area's own edge where the island hangs over the header row with nothing under
  it, and the halo is the only thing saying which is on
  top), and the emoji picker's island over the plain panel Fyne draws behind any
  pop-up (`ColorNameOverlayBackground` at `SizeNamePopupRadius`, which the island
  covers with its own wider corner — what the shadow falls on is the couple of
  pixels that leaves). `DropShadow` follows the corner radius and paints
  nothing under the fill, so a translucent shadow can't dirty the card.
  `CardShadowBlur` overruns `ComposerDockMargin` on purpose: what it has to
  darken is the message passing *underneath*, and a halo stopping inside the
  gutter would only outline the gutter. Deliberately weak — any stronger and the
  card reads as a bar again. At rest it swallows the hairline it sits on; focus
  still lights the outline accent.
- **Content runs under the dock.** A margin and a shadow are not what make the
  composer float — the message column being *taller than the card* is. A column
  that stops above a card stops at a hard cut through whatever glyph the viewport
  landed on, and that cut reads as the top edge of a separate bar.
  `ui.NewFloatingDock` hangs the card over the full-height `MessageList` so the
  cut lands behind it, a corner radius short of the card's bottom edge (the
  rounded corners would otherwise expose it in the two notches). Nothing shows
  beside the card because `MessageHorizontalPadding` is wider than
  `ComposerDockMargin` — that ordering is load-bearing. `ui.NewDockReserve` wraps
  the scroll's *content* — inside `MessageList`, around the rows — and reports
  `ui.DockReserve` of extra height. It measures the card on demand, so a reply
  preview, attachment row or mention picker growing the dock is accounted for
  without anything noticing; `MessageList.Relayout` re-reads it.
- **Composer geometry.** A growing entry's height is
  `lineHeight × rows + InnerPadding × 2` (`growingMinSize`) — the input border
  is *not* added on top, because `entryRenderer.Layout` pays for it out of the
  text provider's own padding. `rows` is what the text **wraps** into, not its
  newline count: an Entry scrolls whatever overflows its box and
  `entryContentRenderer.ensureCursorVisible` keeps a line of clearance around the
  caret, so a box a row short of its own content slid a wrapped line under its top
  edge on one keystroke and back on the next. Nothing reports that count — the
  text provider is unexported — so `wrapMeter` mirrors the text into a
  `widget.RichText` at the same width, which is the widget an Entry wraps in, and
  memoises per (text, width) since `MinSize` runs on every layout pass. The count
  is taken at the width of the *last* layout, hence `MessageInput.Resize` →
  `OnResize` → `App.resizeDock` when a width change re-wraps. The settings page's
  prose fields — a profile's About, a server's description — grow the same way
  (`commitEntry.MinSize`, floored and capped by `SettingsAreaMinLines` /
  `SettingsAreaMaxLines`) and need no such hook: they sit in an ordinary column,
  so the driver's `EnsureMinSize` carries the new minimum up a frame later.
  Everything above the entry — the mention picker, the reply cards, the
  attachments — is one `ui.NewGapBlock`: `ComposerRowGap` between the rows and
  around the block, and nothing at all while all three are hidden.
  `ComposerPaddingV` is sized for the entry, which brings `InnerPadding` of its
  own, and a reply card brings none. The dock hangs `ComposerDockMargin` in from three
  edges of the message area, which is why the area stacks through
  `ui.NewFillColumn`, not a `Border`: a Border would charge theme padding between
  its centre and the dock on top of that. The margin is only a gutter — widening
  it grows the strip, nothing else. `NewComposerButtonSlot` bottom-anchors the
  emoji button against the growing entry and lifts it by the entry's own
  `InnerPadding`, so it centres on the last *line* rather than on the entry's box.
- **A queued reply belongs to a channel, and the composer does not.** So both are
  pushed in: `SetPermissions` and `SetChannel`, from `App.syncComposer`. A card
  whose message is not in the open channel is drawn **stale** (sunken fill, no
  role colour, a frozen mention toggle) and left out of `RepliesHere`, which is
  what the send is built from — it is kept rather than dropped, because returning
  to that channel makes it live again. Queueing a reply from a *different*
  channel replaces the whole set: nothing sendable answers messages in two
  channels, and the clear happens before `maxReplies` is read.
- **What hangs above the card** — the slowmode chip and the typing line — is one
  row under one set of rules: a pill of its own (`newDockBadgeSurface`) sized by
  what it holds rather than by the row, accepting no pointer event so the
  messages underneath stay hoverable, an `OnResize` hook so the row can be
  re-laid out, and a change guard before any repaint. `JumpBar` is the opposite
  of that pill on every count — it spans the card's width, answers a tap, and
  says where the *column* is standing rather than something about the channel —
  but it hangs in the same stack and reports its own appearance the same way.
- **Nested `Border`s hide padding.** `container.NewBorder` inserts theme padding
  between each edge slot and its centre, so nesting them charges a row several
  helpings. Reach for `NewFillRow` / `NewFillColumn` / `HBoxNoSpacing` /
  `NewInset` when the spacing has to be exact.
- **A wrapping widget answers `MinSize` with the width it was last laid out at**,
  so a column that measures its children before sizing them stacks a one-line
  body as the five lines it wrapped into at zero width. `NewWrapColumn` resizes
  each child to the column's width first and measures after — that is what stacks
  a message body around the wells its code fences draw. Anything else stacking
  wrapping rows needs the same order.
- **A RichText segment carries a colour *name*, not a colour**, so a palette it
  draws from has to be answerable by `AppTheme.Color` — `theme.ColorNameCode…`
  are the highlighter's, sitting beside Fyne's own with an `rgo` prefix.
- **Inline code is Fyne's own chip.** What draws the fill behind a `` `span` `` is
  an unexported mark on `widget.RichTextStyleCodeInline`, so `mdBuilder.code`
  *copies* that var and sets only exported fields — a style built by hand draws
  no fill. Fyne fits the rectangle to the glyphs and gives no way in: the fill is
  `ColorNameInputBackground` and square, and the chip's breathing room is
  therefore a non-breaking space in the text (`codePad`), an ordinary one letting
  a row break between the padding and the word. A colour of its own or a corner
  radius needs a fork patch. It stays a native `TextSegment` — a widget segment
  is atomic and RichText never breaks a row before one — and a body carrying one
  is off the flattened, selectable Label path.
- **A spoiler covers everything inside it.** A construct with a segment of its
  own — a link, a mention, inline code, a custom emoji, a timestamp — has no
  cover to wear, so inside a spoiler each is rendered as covered `decoratedText`
  instead: a link as its label in the hyperlink colour, a mention as its accent
  word (`ColorNameMention`), an emoji as its shortcode, code in the body's own
  face — the mono face is taller at the same size and RichText's baseline drop
  (the `emojiSide` trap) would overlap the rows. `decoratedSegment.onTap` is
  what a word does once revealed: the first tap reveals the span, the second
  opens what the word covers.
  `canvas.Text` centres its glyphs in whatever height it is given. Don't wrap one
  in a spacer to nudge it.
- **Message row rhythm.** The avatar is centred on the block a *single-line*
  message occupies, placed by an offset from the top rather than centred on the
  row, so a longer body grows away from it. That offset and the grouped gutter
  timestamp's are *derived* from `messageLineHeight()`, never hardcoded. Anything
  a row can additionally carry (a reply preview) must move the whole row, so it
  sits *inside* the row's margins. A message with no text hides its body slot
  entirely — an empty body still renders one line tall, drawing as a gap above an
  embed.
- **A system message is not a message anybody wrote.** `NewMessageWidget`
  branches on `Message.System` before building anything: no avatar, name, body
  slot, attachments, embeds or replies — one line with the event's mark centred
  in the gutter an avatar would fill, and the time drawn beside it rather than
  revealed on hover (there is no name for it to follow, and "Someone left" with
  no *when* is half a sentence). The one thing in that line accepting a pointer
  is the name it announces, a `mentionText` opening that profile: the row has no
  author to click instead, which is why `Store.SystemTextParts` hands the name
  back apart from the sentence rather than folded into it. It carries the row's
  menu and is not hoverable — innermost wins, so anything else there would be a
  hole in the row's own hover and context menu. It keeps `SystemMessagePadding`
  whatever surrounds it — `continuesGroup` refuses either side of one, so a run
  of joins is a block — which is why `verticalPad` is a *method*:
  `SetFollowedByGroup` reaches it too. Reply is offered nowhere on one
  (`canReply`), as edit already wasn't. `systemMark` answers with the glyph
  **and** the colour: the tone is the class of event (arrival, departure,
  removal, a channel change, a call), which is what a column of these is skimmed
  by, and the glyph is which event of that class it was. An unknown kind takes
  the generic mark in neutral grey, so an event Revolt adds later still reads as
  an event.
- **A name is followed by what the name does not say.** `domain.AuthorMark` is
  decided once, by `Store.MessageAuthor`, and drawn by `ui.NewAuthorMark` — nil
  for a person, so no surface decides for itself when to draw nothing. Every
  surface naming an author wears it at its own size: the message header
  (`MessageAuthorMarkSize`), the quoted reply line and the composer's reply card
  (`ReplyAuthorMarkSize`), and an island card (`IslandAuthorMarkSize`). One tone
  — `Colors.BotMark` — three glyphs, since what is being said is the same thing.
  A card's rides at the *fixed* end of the heading rather than against the name,
  the name being the child that stretches; `MemberRow`'s mark sits the same way.
- **A webhook post is nobody's.** `Message.Webhook` carries the name and picture
  the integration chose, and `Message.AuthorID` is the hook's own ID — there is no
  account behind it. So its avatar is given no tap (`Avatar.Cursor` then keeps the
  arrow, `tapBase` promising a pointer unconditionally) and `app.onMessageMounted`
  queues no author fetch; a card opened on that ID could only say Unknown user.
  A masqueraded message is the opposite shape: the account is real and its avatar
  opens, but the name drawn is the account's rather than the mask's, which the
  client does not render (see `docs/known-gaps.md`) — the mark is what says so.
  Both go in `MessageWidget.authorMarks`, a slot that is zero-width while empty so
  a person's name pays no gap, filled once: a webhook's and a mask are known at
  the mount, and an author resolving later can only turn out to be a bot. Filling
  it re-lays `authorLine` out, the timestamp beside it having to move; the quoted
  line's slot (`replyPreview.marks`) is the same arrangement for the same reason.
- **Tinting one of the client's own marks is a substitution, not a theme name.**
  Fyne's `theme.NewColoredResource` rewrites an SVG's *fills* and leaves strokes
  alone, and every mark in `assets/` is an outline — so a stroked icon comes back
  white whatever colour name it is given. `ui.tintedIcon` replaces the source's
  one stroke colour instead, and puts the colour in the resource's **name**,
  because Fyne caches the rasterised SVG under that name and two resources
  sharing one would share a raster. That name is also the key of
  `ui.tintedIcons`, the memo that stops a channel of system events rewriting the
  same files repeatedly; a restyle changes the colour, hence the name, so it
  misses rather than returning a stale palette. Message buttons draw
  `action-*.svg` rather than Fyne's icons for the same reason: a themed resource
  takes its colour from a theme *name*, and delete reading as delete is the point.
- **A mark is centred by its path coordinates, never by its `viewBox` origin.**
  oksvg's `SetTarget` translates by `x-ViewBox.X` *before* scaling, so a viewBox
  offset moves the drawing that many **device pixels** rather than that many
  viewBox units — an offset that does nothing at the sizes a mark is drawn at and
  changes with the size. `assets/voice.svg` and `assets/system-call.svg` are both
  off-centre for this reason, having been drawn against an origin that does
  nothing. To measure one, rasterise it with the software driver and take the
  alpha bounding box — the arithmetic on a stroked bezier is not worth doing by
  hand — then delete the harness.
- **A handset has to be drawn solid.** `assets/call-end.svg` and
  `assets/call-join.svg` are the only filled marks in the set. Outlined, at
  `IconButtonGlyph` (17), a handset's inner and outer curves land a pixel apart
  and the mark reads as a bitten crescent rather than a phone — which is what the
  stroked pair before them did. `ui.tintedIcon` colours a fill as readily as a
  stroke, being a byte replace of `#ffffff` over the whole file, so nothing else
  had to change. Both are built from rounded rectangles and one annular sector
  unioned by a nonzero fill: every face stays flat and every number is mirrored
  about the box's centre line, which is what keeps the pair geometric and
  symmetric where a hand-cut bezier drifts.
- **Tap plumbing is `ui.tapBase`, not a hand-written pair of methods.** Embedding
  it supplies `Tapped`, `TappedSecondary`, `MouseMoved` and the pointer `Cursor`
  from two func fields, and every interactive widget here uses it. A widget whose
  menu is assigned *after* construction — the sidebar's server and channel rows —
  sets `onSecondaryTap` to a closure reading its own `Menu` field, so the items
  are built when the click arrives. Deliberately **not** hoverable: a no-op
  `MouseIn`/`MouseOut` here would take hover from every parent row, so a widget
  that wants hover declares it itself. `ui.decoratedText` is the one hold-out and
  stays one — its `Cursor` is conditional (a struck word is not a spoiler and must
  not read as clickable), which `tapBase`'s fixed pointer would flatten.
- **A click on nothing is caught from underneath.** `ChannelBackdrop` is stacked
  *under* the channel sidebar's scroll, not inside it: `FindObjectAtPositionMatching`
  walks in painting order and keeps the **last** match, so a row over the pointer
  wins and only the strip below the last row falls through. It is the one widget
  here **not** built on `tapBase` — that promises a pointer cursor unconditionally
  and a primary `Tapped` besides, and an empty column is neither a target nor
  something to tell a reader is one. `container.Scroll` is not itself tappable, so
  nothing in between swallows the event; `CategoryWidget` is (through `tapBase`,
  with no menu), so a right-click on a category header still stops there.
- **Repainting the message column.** `Container.Refresh` refreshes every child
  and `RichText.Refresh` re-wraps its text, so refreshing the column re-flows
  every mounted body — and `Scroll.Refresh` does exactly that to its content.
  `MessageList` therefore never calls it: every mutation ends in `ui.Relayout` of
  its own container (re-run this one layout, don't walk the children) and
  `ObservableScroll.SyncContent`, which resizes the content and re-places it
  through the scroll's renderer alone. Use `Refresh` only when what a *mounted*
  widget says has changed. For the same reason nothing on the scroll path may
  call `MinSize` on the list — `BaseWidget.MinSize` is not memoised — so a
  virtualised list's own layout reports its height from a **field**, never from a
  walk — `slotLayout.MinSize`, which both share: `container.Scroll` asks its
  content for a minimum on every offset write.
  `Container.Add` is the same trap one child at a time (it refreshes the whole
  container per call), so a list is built into a slice and written to `Objects`
  once. Nothing in this repo fills a container in a loop. One level below all of
  that, `Canvas.dirty` is a single bool: **any** `Refresh` anywhere repaints the
  whole window, framebuffer clear included. There is no such thing as a cheap
  one — see `docs/performance.md`.
- **The message column measures in its layout.** Rows are variable-height, so
  `MessageList` places a row by an estimate until its widget has been laid out at
  the column's width, and the only place that width is certain is
  the message list's `slotLayout.Layout` (the `measure` hook the member
  sidebar's leaves nil). It is also the one hook Fyne offers for a row that
  grows after mounting — an editor opening, an invite card resolving: the
  driver's `EnsureMinSize` re-runs a parent's layout when a child's minimum moves
  and carries the container's new minimum up to the scroller in the same pass. So
  the layout may write `Scroll.Offset` and must not call anything that refreshes
  the scroller. A height moving wholly above the viewport shifts the offset with
  it, and a column at its bottom (within half a pixel) is kept there; `Resize`
  keeps the bottom too, `Scroll.Resize` zeroing an offset while the content is
  still unsized, which is every first layout. A widget is dropped once its row
  leaves the overscan and rebuilt on the way back — except the one being edited
  (`MessageWidget.Editing`), which holds the draft. A row's `grouped`, `followed`
  and `dayLabel` are derived from its neighbours in the model (`rederive`), so a
  prepend, trim or delete re-decides only the rows at the seam, and a mounted row
  whose header came or went is rebuilt rather than patched.
- **The member model points at its members.** `MemberEntry.Member` is a
  `*domain.Member` into the slice `NewMemberModel` was given, not a copy: one is
  nearly 200 bytes, the model is rebuilt on every presence change, and copying
  each into the flat list cost four times what the flattening did (831µs/4.33MB
  → 202µs/0.81MB over 20,000). What that asks of the caller is that the slice
  outlive the model and never be written into — `App.memberCache` is published
  under exactly that rule, a change publishing a new slice rather than editing
  one. A row still keeps nothing but what it draws, so the pointer does not
  outlive `SetMember`.
- **A recycled widget must own nothing it captured.** `ui.MemberRow` is reused
  for a different person as the list scrolls, so every callback on it reads the
  field it needs at the moment it fires rather than closing over a value — a menu
  that captured a member ID kicks the wrong person after the first recycle. An
  asynchronous load has no such field to read, so it carries a `generation` the
  row bumps on every `SetMember` and `release`, and a picture arriving against a
  stale one is dropped. The counter is UI-thread only (`ImageCache.LoadAsync`
  delivers there), so it is a plain `uint64`. Restoring a placeholder means
  putting **the same object** back, not a new one: Fyne only learns of a canvas
  object when the container holding it is refreshed, so a row that quietly
  swapped in a fresh `canvas.Circle` drew no avatar at all — hence
  `newAvatarSlot` handing the placeholder back alongside the slot.
  `ellipsisLayout` rewrites its text object during layout, so a recycled row
  compares against its own `fullName` and re-labels through `ui.SetEllipsisText`;
  reading the object back would take a shortened name for the real one.
- **Fyne's scrollbar is a widget over the content, so the client draws its own.**
  Its `scrollBarArea` lies across the right edge of the content and accepts hover
  — innermost wins, so it stole the message row's. `AppTheme.Size` zeroes *both*
  `SizeNameScrollBar` and `SizeNameScrollBarSmall` (zeroing only the large one
  left an invisible strip still eating hover). What replaces it is
  `ObservableScroll`'s indicator: a `scrollThumb` appended to Fyne's renderer
  objects (set once at construction and never replaced, so composing the slice
  once holds). Being last in that list it is the topmost hit, so it takes the
  press and the drag that scroll by it — but it covers only the bar's own extent,
  never the whole track, and it is not `Hoverable`: either would put a strip back
  between the pointer and the message row. It is grabbed through
  `ScrollIndicatorGrabWidth`, the bar drawn against that area's right edge, and
  the fade leaves it transparent rather than hidden — a bar nobody can see is
  still where the pointer last saw it, and a press brings it back. It is placed
  from `Content.Size()` — never `MinSize` — and revealed from the *renderer's*
  `Refresh` by comparing the offset, since every offset change ends there whoever
  caused it while an unrelated repaint must not flash it. It fades through a
  `fyne.Animation` the renderer's `Destroy` stops, so a restyle's rebuild doesn't
  leave one ticking. `ScrollIndicatorWidth + ScrollIndicatorInset` must stay under
  `MessageHorizontalPadding` or the bar draws over the text, and
  `ScrollIndicatorGrabWidth + ScrollIndicatorInset` with it, or a press beside the
  text is taken by the bar rather than the row; a width of zero turns it off.
  Only the message column has one — elsewhere the right edge carries rows and
  controls a strip would obstruct, which is what `NewPlainVScroll` is for: the
  settings pane centres its cards, so an indicator pinned to the pane's edge lands
  on one whenever the window is narrow enough for the two to meet. It leaves
  `indicator` nil, so `CreateRenderer` must not append it — a typed-nil thumb in
  the renderer's object list is dereferenced by the painter. Neither `Scrolled`
  nor `Dragged` may write `Offset` and call `Refresh`: `Scroll.Refresh` walks and
  repaints every descendant, which for a pan is the whole column once per frame.
  `ScrollToOffset` clamps and refreshes only the renderer.
- **Tooltips and notices are layers over the main row, not canvas overlays.**
  Pushing an overlay routes the whole hit test into it, so the hovered widget
  would never see `MouseOut`. Confirmations *are* canvas overlays, on the modal
  layer with the lightbox — and a card *on* that layer draws over the app's
  tooltip, so one that hovers anything mounts a `NewTooltip` of its own in its own
  stack. The emoji picker and the attachment viewer each carry one.
- **A transient notice is the call island's card, not the modal notice's.** Same
  surface, same lighter-than-hairline edge (`NoticeCardOutline`), same shadow:
  they are the only two cards the client floats over what is being read. The tone
  is a **disc at the leading edge**, centred against the whole row rather than
  hung from the first line — a mark level with the heading points at the heading.
  Its plate and ring are `theme.Mix`ed from `NoticeCardBg` towards the tone, so
  they follow an accent override; the body is hand-wrapped (`newNoticeBody`)
  because `widget.Label` brings `InnerPadding` on four sides, which on a card this
  size is a line's worth of air the heading does not have.
  **A notice about a *person* takes a face in that same slot** (`noticeMark`,
  `NoticeAvatarSize` — larger than the disc, a glyph being legible at any size and
  a face not): a message is recognised by who sent it before its heading is read.
  `noticeMark` hands its width back because the sentence is wrapped by hand and the
  two marks are not the same size. Such a card **leads somewhere**: `Notice.OnTap`
  runs after the dismissal — what it opens must not be drawn under the card — and
  `Cursor` answers with a hand only where there is one, a card that merely
  dismisses being a message about to leave on its own. The close button still wins
  the tap, being deeper, so a card can be waved off without going where it leads.
  The heading is a `NewEllipsisText`: a name and a channel are as long as they are,
  and a `canvas.Text` draws straight past the card holding it. `Notice.Unfiltered`
  is how a ping escapes the tone switches, which name which *outcomes* are worth
  reporting rather than whether somebody wrote to you.
- **A translucent fill cannot be handed to a canvas object.** The palette writes
  *straight* alpha into `color.RGBA` (see `theme.toRGBA`) and every painter reads
  one back **premultiplied**, so a colour whose channels exceed its alpha
  composites as a different hue entirely — `theme.Fade(NoticeInfo, 0.2)` drew the
  notice's blue disc olive green. `theme.Mix` against the surface the object
  stands on is the fix; `dissolve` in `notice.go` is the other one, scaling every
  channel with the alpha for a fade.
- **`ModalNotice` is a layer for the same reason, in the middle rather than the
  corner.** It is what a message worth stopping for is drawn as, and the only
  thing the login and second-factor screens can report with — the notice stack
  belongs to a main UI that does not exist until Ready, so `app.mountLogin`
  stacks this layer over both. Being a layer is what keeps it *floating*: an
  overlay would take the whole hit test and stop the client answering a click for
  as long as a message nobody has to answer was up. Only the card takes a tap, and
  that tap dismisses it. One card at a time, replaced rather than queued.
  It **fades in and out, in eight steps**. Fyne caches a rendered line of text
  under a key its *colour* is part of, so every distinct alpha is a texture of its
  own — quantising means eight of them, shared by every notice the client ever
  draws, where a smooth ramp would mint a dozen per word per fade. `dissolve`
  scales every channel and not the alpha alone, `Color.RGBA` being premultiplied
  (the trap `theme.Fade` names), and everything on the card is scaled together —
  fill, outline, shadow, mark and text — so it dissolves as one object. The only
  completion hook a `fyne.Animation` has is the tick reporting exactly 1, which
  the runner calls once at the end and never again; those ticks run in the
  driver's loop, on the thread that paints.
- **A confirmation is one column down the middle.** No tone glyph and no close
  button: the confirming button already carries the tone as a fill, and a Cancel
  says everything an X would. A *statement* — a `Confirm` with no `OnConfirm` —
  takes one button across the whole card, there being nothing to cancel.
  `newModalBody` is the sentence both it and the centred notice are read by, and
  it wraps **by hand** (`wrapText`, breaking mid-word where no space will ever
  break a URL in a transport error): `widget.RichText` is what wraps text in Fyne
  and it carries a theme colour *name*, which the notice's fade cannot move.
- **One card is drawn on the modal layer, and `newDialogCard` is it.** The
  rectangle, `ConfirmRadius`, the client's one hairline and the shadow are all in
  that one constructor, so the confirmation, the join card, a prompt, a
  challenge, a ban and the screens before Ready cannot drift a corner or a shade
  apart. `dialogHeader` is the heading every one of them wears — `newBoldText` at
  `ConfirmTitleSize`, not a `widget.Label`, so it is the same type as the rest of
  the client — and the rule under it is `NewRowDivider`, never
  `widget.NewSeparator`, which draws in a Fyne colour name nothing here sets. A
  card's body is `spacedColumn(DialogFieldGap, …)` inside
  `NewInset(DialogPadding)` at `ChannelDialogWidth`, and each field is
  `dialogField(label, fieldSurface(entry))`: the name above the field,
  upper-cased, and the entry on a surface rather than bare. Anything that reaches
  for `container.NewPadded(container.NewVBox(…))` instead is reading as a
  different program's dialog, which is what the join and prompt cards used to do.
  `spacedColumn` is a `NewGapColumn`, so a row a card only sometimes has —
  the invite preview, the status line — **hides** rather than standing empty:
  a spacer either side of an empty row is a hole the reader can see, and both
  start hidden because a card is built before anything has gone wrong.
- **The screens before Ready are that same card** (`ui/login.go`). Sign-in, the
  second factor and the one held while a saved token is exchanged each *are* the
  window — there is no main UI to draw them over yet — so they are the only cards
  here with **no close button**: there is nothing behind one to go back to.
  `NewAuthCard` is the surface, `NewAuthField` / `NewAuthCaption` / `NewAuthNote`
  the rows, and `NewAuthChoice` the client's own dropdown, Fyne's `Select` being
  unusable for the reason the settings page gave up on it — `AppTheme` zeroes the
  input border it draws its field with. The pieces are exported and the screens
  are not: what is asked and in what order is the controller's, holding the
  account and the ticket, and the shape is this package's. `app.mountLogin` sizes
  the window from what the card measures, so nothing here names a login width.
- **A link in somebody else's content is not a hyperlink until it has passed
  `util.SafeLink`.** `fyne.App.OpenURL` is `rundll32 url.dll,FileProtocolHandler`
  on Windows and `xdg-open` elsewhere: both launch whatever the scheme is
  registered to, and Fyne's own comment says validating the input is the caller's
  job. So `mdBuilder.link` renders a destination outside http/https/mailto as
  **plain text carrying the label** rather than as a segment somebody can click,
  and every live tap — a body's link, an embed's title, the viewer's browser
  button — goes to `MessageActions.OnLinkTapped` so the controller can refuse it
  out loud and warn about a masked one. `ui.openURL` still checks: it is the last
  gate, not the only one, and it refuses in silence so a call site added later
  cannot open a scheme by default.
- **A box layout stretches every child across the row.** `container.NewHBox`
  hands each object its minimum *width* and the row's full height, so an icon
  button in a strip taller than itself is drawn as a tall rectangle — and two
  buttons of different minimum widths as two different rectangles. `vcenter`
  keeps one at its own square.
- **Every text button is `ui.Button`, not Fyne's.** Fyne's is themeable down to
  `SizeNameButtonRadius`, but its background is a rectangle inside a renderer
  nothing reaches, so no theme name puts the client's one hairline on it — and an
  outline is what tells a plain button from the card it sits on, its fill being a
  shade off that card by design. `ButtonWeight` decides the rest: a plain button
  wears the hairline, a weighted one drops it and fills with a notice tone
  (`Tone.weight()` is the bridge, so a confirmation's button and the notice
  reporting what it did are the same red). A filled button lifts its own colour
  under the pointer (`theme.Lighten`) rather than taking the plain one's hover
  fill, and a disabled one carries no tone at all — it is not offering anything.
  It is **focusable**: Tab reaches one and Space or Enter presses it, both going
  through `Tap` rather than the action — the action bypasses the disabled check
  and would send an in-flight request twice, which is also why an entry that
  submits through a button calls `Tap` from `OnSubmitted`. The ring
  (`ButtonFocusRing`, `ButtonFocusWidth`) *overwrites* whatever edge the button
  had settled on, a `canvas.Rectangle` having one stroke: `focusRing` is called
  last in both appearance paths. It is near-white rather than the accent so it
  reads against a Danger fill as well as against the plain surface. A disabled
  button still accepts focus — Fyne's manager walks the tree and does not ask —
  and draws no ring, pressing no more from the keyboard than from the pointer.
- **A list narrowed by typing uses `newFilterField`.** The settings rail, the
  friends page and the group picker all mount one, so the mark, the field and the
  surface under them are built in `widgets.go` rather than three times; Escape
  empties it rather than reaching the surface as "close", but only while there is
  something to empty. `matchesFilter` is the comparison all three share. The
  group picker **hides** its rows rather than rebuilding them — a rebuilt row is
  a row whose pick is gone — and `noSpacingLayout` charges no gap for a hidden
  child, which is what makes that work.
- **Fyne's form widgets do not survive `AppTheme`, and a scoped override only
  buys so much.** `AppTheme.Size` zeroes `SizeNameInputBorder`, from which a
  `widget.Slider`'s track thickness is derived (`trackWidth = inputBorder * 2`),
  and its track is filled with `ColorNameInputBackground`, which `AppTheme`
  answers with the colour a settings group is a card of — so a slider drew as a
  bare thumb on an invisible track. A `container.NewThemeOverride` fixed both and
  still left the thumb swelling into a grey hover disc over the track, that being
  Fyne's own drag affordance with no theme name reaching it. Hence
  `settings_controls.go`: a control here is canvas objects and a layout. Any Fyne
  widget mounted for the first time is worth rendering to a PNG before believing
  it.
- **A discarded widget hears nothing.** Dropping a widget out of a container
  tells it nothing at all: Fyne destroys a renderer — and with it whatever
  `Destroy` stops — only when its cache expires the widget, up to a minute after
  the last paint that used it, and hiding an ancestor is not a paint. So an
  animation is stopped by whoever drops the widget (`App.releaseChannelRows`,
  `restyle`) or by the widget being told it is hidden (`ChannelWidget.Hide`),
  never by trusting `Destroy` to arrive. `Visible()` answers for the object
  alone, so an inner widget cannot ask — which is why the member sidebar's status
  mark is stopped through `MemberList.SetSweeping`: the column is hidden by
  hiding the *container* around it, and nothing inside hears that either.
- **A `time.Timer` that has fired cannot be recalled.** `Stop` reports false and
  the callback is already on its way, so a re-arm that replaces the field leaves
  the older wake to run against it — which for `armTypingTimer` meant one timer
  orphaned and another armed on every lapse that raced an event. A wake that
  writes shared state checks it is still the current one (`a.typingTimer != timer`)
  or, where the timer is reset rather than replaced, checks what it was armed
  against (`armTypingIdle` against `lastTypedAt`).
- **A modifier key can only be read out of an event that carries one.**
  `desktop.Canvas`'s `SetOnKeyDown`/`SetOnKeyUp` fire *only* while nothing holds
  canvas focus (glfw hands the key to the focused `desktop.Keyable` instead), and
  the composer holds focus for most of the client's life — so a Shift tracked
  there is missed by every click that matters. A `desktop.MouseEvent` does carry
  `Modifier`, but a context-menu item is Fyne's own widget and delivers none.
  Hence `ui.ShiftHeld`, a Win32 `GetAsyncKeyState` asked at the moment of the
  click, with a `!windows` half answering false and `shiftSkippable` telling the
  confirmation card whether to name the key.
- **A setting this machine cannot apply is greyed, not dropped.**
  `SettingsPage.locked(row, reason)` stacks a `lockedScrim` over the row:
  `FindObjectAtPositionMatching` keeps the **last** match in painting order, so
  one object over the row takes every tap *and* every hover inside it — which is
  what makes the reason the only thing a locked row answers with, and why the
  controls under it need no disabled state of their own. The wash is
  `SettingsLockedScrim`, the card's own fill at part strength, laid over the row
  rather than a dimmed palette per object, a row being a dozen of them. The index
  pass takes the row bare, a name being what it is walked for. Two consequences
  outside this file: the tooltip layer is stacked **over** the settings pages in
  `app.buildUI` — a label about what is under the pointer is no use beneath an
  opaque page — and `SettingsPage.Close` hides the tooltip, a locked row under the
  pointer as the page goes never reporting the pointer leaving. The older rule
  still stands for a *group* with nothing to say on this platform: the
  taskbar-flash group is dropped, not greyed, there being no setting there that
  another machine would carry differently.
- **Whether the desktop draws a notification area has no toolkit answer.**
  `ui.TrayAvailable` is the probe, split by platform the way `FlashTaskbar` is
  and memoised with `sync.OnceValue` — the settings search builds every section
  twice, and on XDG the answer costs a bus call. Windows and macOS answer true;
  the XDG half asks the session bus whether `org.kde.StatusNotifierWatcher` has
  an owner, Fyne's tray being a StatusNotifierItem, which without a host
  registers and is drawn by nobody (GNOME without the AppIndicator extension is
  the common one). `app` reads it to decide whether to mount a tray at all, and
  the System section to decide whether its rows are greyed.
- **A window cannot ask for attention through Fyne.** There is no API for it, so
  `ui.FlashTaskbar` is a Win32 `FlashWindowEx` on the HWND `driver.NativeWindow`
  hands back — the same route `StyleTitlebar` takes — with a `!windows` half
  doing nothing and `alertSupported` keeping the settings group off a page where
  it would draw switches that do nothing. `FLASHW_TIMERNOFG` is what makes it a
  message waiting rather than a blink: it flashes until the window is brought
  forward, and Windows itself no-ops the call for a window that already has
  focus, so no caller checks. `fyne.App.SendNotification` is deliberately not
  used — see the known gap.
- **Fyne's file picker is drawn inside the canvas, so nothing here opens it
  first.** `dialog.NewFileOpen` is a browser of Fyne's own: no pinned or recent
  places, no cloud providers, no shell search, no typed path, and not the dialog
  the reader opens in every other program on the machine. `ui.PickFile` /
  `ui.PickFolder` open the desktop's own on all three platforms, each half in its
  own file, and all three are a **process or thread beside the client** rather
  than a layer over it — which is what keeps the client painting behind one.
  - `filedialog_windows.go` — the shell's Common Item Dialog (`IFileOpenDialog`)
    over the HWND `driver.NativeWindow` hands back, the same route `FlashTaskbar`
    takes, on a locked OS thread in a single-threaded apartment because the dialog
    pumps messages of its own. COM has no binding in the standard library: the
    vtable indices there *are* the interface contract, fixed by inheritance order
    and discoverable nowhere at runtime, and one wrong number calls a different
    method.
  - `filedialog_darwin.go` — AppKit's panel through `osascript`. NSOpenPanel
    would need cgo on the process's **main** thread, which is the one Fyne paints
    from. The script is wrapped in `tell application "System Events"` because a
    panel raised by a background process opens behind whatever is in front of it,
    and a dismissal is `-128` in the error text rather than an exit code of its
    own.
  - `filedialog_linux.go` — `zenity`, `qarma`, `matedialog` or `kdialog`,
    whichever is on PATH. None is a dependency; the three zenity clones share
    their flags and kdialog takes its own, which is the only thing the code
    branches on. All four exit 1 for a dialog that was dismissed, so that is an
    answer rather than a failure.

  Both calls report false where there is nothing native to open, which is what
  sends the caller to Fyne's — see the known gap.
- **A row's wash is an animation, never state.** `MessageWidget.Flash` (a jump)
  and `FlashEdit` (an edit landing) both go through `flashWash`: `fill()` is
  untouched, so the row answers the pointer throughout and the last tick hands
  the background back to it. It mixes two **opaque** colours — the palette writes
  straight alpha into `color.RGBA`, which Go composites as premultiplied (see
  `theme.Fade`), so fading a wash down its alpha darkens the row on the way out.
  The two differ only in colour and in the strength curve: a jump starts at full
  wash and lets go, an edit rises and falls inside its second, nobody having
  asked to be shown that row.
  **A deleted row's wash is the exception, and is a state on purpose**
  (`SetDeleted`, `MessageDeletedBackground`): the message has gone and the row is
  standing a few seconds longer so the column does not jump, so the wash has to
  hold until the row goes — which means going through `fill()` like the mention
  and selection washes, lifting under the pointer and outranking both.
  `MessageList` holds the set, so a row scrolled past and back is built already
  washed, and `Remove` is what clears it — see `internal/app/CLAUDE.md` item 43.
- **The selection tick is the whole of the target, and that is forced.** A bulk
  delete needs rows picked, and the row itself cannot take the click: its body is
  a selectable `widget.Label` behind a `selectionCatcher` that forwards a tap
  *down*, so innermost wins and a `Tapped` on `MessageWidget` would answer
  everywhere except over the words. `selectTick` is therefore a widget of its own
  in a second overlay beside the quick-actions' — same `overlayLayout`, a positive
  `yOffset` because it is up for as long as the mode is and a row bleeding above
  its own top would put two ticks in one gutter at a group seam. The two overlays
  are never populated at once: `updateHover` returns early while selecting, so the
  mode is what the row offers and the wash still follows the pointer.
  A row the route would refuse — no `ManageMessages`, or past
  `domain.MaxBulkDeleteAge` — keeps its box **dimmed and inert** rather than
  losing it, or a column with a week of history in it reads as half broken; the
  rule is asked of the *model* as well (`MessageList.selectable`), because a
  Shift-extend spans rows that are not mounted and only the model can step over
  one. Shift is `ui.ShiftHeld` read at the click, the same Windows-only answer a
  confirmation skip is. Both the mode and the set live on `MessageList`, keyed by
  message ID for the reason `mounted` is, and `build` pushes both into a row as it
  mounts so one scrolling back in does not return unticked.
- **A picture cannot be rounded off, so a card puts it inside its padding.**
  `canvas.Image` has no corner radius and Fyne clips nothing, so an image drawn to
  a rounded card's edge squares off the corners it covers — the invite card's
  banner sits within `EmbedPaddingH` instead, as the attachment viewer's picture
  sits in a padded well inside its own card.
- **Fyne's test driver runs a `DoOnUI` callback on the calling goroutine**, not
  serialised onto a UI thread as the real driver's queue does — so a widget that
  fetches at construction and paints the answer races the test, inside `go-text`'s
  unsynchronised shaper LRU (`concurrent map writes`) or `RichText`'s row bounds.
  A widget therefore should not start work it has nothing to do: `viewerText`
  answers an attachment with no URL itself rather than sending a fetch that can
  only fail.
- **`widget.Entry`'s typing methods refresh for themselves.** `TypedRune` and
  `TypedKey` both end in `e.Refresh()`, so a `MessageInput` refreshing after one
  re-wrapped the composer and dirtied the whole window a second time per
  keystroke. The branches that return early inside Entry — backspace at the start
  of an empty composer — changed nothing to draw, so they want no refresh either.
  `TypedShortcut` is left alone: what it dispatches to does not all refresh.
- Any custom widget overriding `Dragged` must also have `DragEnd`.
- **One shell draws three settings pages now.** `settings_shell.go` owns the surface —
  the layer, the rail, the pane, the group offsets, the flash, the popover — and
  the vocabulary of rows every section is assembled from. `SettingsPage` and
  `ServerSettingsPage` **embed it by value**, so a section reaches `p.group`,
  `p.row`, `p.note` and the rest by promotion and never names it; that embedding
  is also why extracting it changed no call site in `settings_sections.go`.
  Three rows the pages had a copy of each now live there too: `identityStrip`
  (the picture and name above the rail), `descriptionRowOf` and `pictureRow` —
  the last taking its Remove **button** rather than a bool, the account's banner
  holding on to one that stays disabled until a profile fetch says otherwise.
  The circle those previews are drawn as is `newInitialIcon` in `widgets.go`,
  shared with the invite card, the emoji rail and the call island.
  What a page contributes is the sections: `railEntry.section` is a plain `int`
  because the two number theirs separately, and `buildRail` is handed the
  entries, the open one and what to do about a tap. It appends sub-entries only
  when the section has **two or more** captioned groups — one place to go is not
  navigation, and the entry would repeat the section's own name. `mount`
  deliberately does *not* rebuild the rail: what it lists is the page's, and both
  callers do it after. `record` is the hook the client's search indexes through,
  nil on a page with no search, which keeps `indexing` meaningful for one page
  and inert for the other. **A nil child is not a hidden one:**
  `buildRailColumn` drops a nil `foot` rather than laying it out — a layout walks
  its children calling `Visible()`, which a nil interface answers with a panic,
  and the server page pins nothing under its rail.
  A section that **drills into one of its rows** is still one section: the page's
  `roleID` / `channelID` decide which groups the section returns, so the rail entry
  stays marked and tapping it is what goes back, and the drilldown's group captions
  become the rail's sub-entries the way any other section's do. Two sections drill,
  and Channels drills **twice** — a channel's overrides are a channel *and* a role
  — so `paneBack` steps back one level at a time while a rail tap
  (`closeDrilldown`) leaves all of them. The way in is `showRole` / `showChannel`,
  and every level must be re-derived on every build: a role can be deleted or moved
  above this account's own rank, and a channel deleted, while its editor is open. What says so is the **back button above the title**
  (`backLink`, `settingsBackLink`): `mount` is a section and empties it,
  `mountUnder` is something standing inside one and fills it, and there is no
  third way to write the pane — a way back outliving what it led out of would tap
  into somewhere the reader is not. So `paneTitle` names one thing and the button
  over it names where that lives; the results page, which the rail marks nothing
  for, takes the same button out to All settings and empties the field on the way,
  `onQuery` being what puts the section back. `mountUnder` is handed the groups
  *before* the title and the link are computed — a build that finds the role gone
  leaves the drilldown on the way past, and both have to name where the reader
  ended up. The button is the pane's **top left**, mirroring the close button's
  top right, and it is the one thing in the header that `centred` does not cap —
  so it stands at the pane's own edge while the title stays with the cards, and
  the two read diagonally rather than as a stack. Which is also why the padding
  above and below belongs to the heading *column*: hung on the title, a hidden
  back row would take the top of the header away with itself. It is a plain
  `ui.Button` in everything that can be seen — fill, hairline, radius, hover lift,
  label — and a widget of its own only because `Button` has nowhere to put the
  mark saying which way it goes.
  A card of forty rows is read by its **markers**, not its controls:
  `markPermission` paints the same bar `boolRow` uses in the accent for a grant
  and the danger tone for a denial, so a role's shape is legible without reading
  every dropdown. **One grid serves five scopes** — a server role, the server's
  default, a role in one channel, a channel's default and a group's — and `permissionScope`
  is the whole of what separates them: three states or two, which bits are drawn
  at all, whether a bit may be moved, and where the change is sent. A category
  whose bits the mask empties is dropped rather than captioned over nothing, and
  the two cards whose wording is about the server say it again for a channel
  (`captionIn` / `detailIn`).
- **The permission grid belongs to the shell, not to a page.**
  `settingsShell.permissionGroups` / `permissionRow` / `setPermissionRow` work
  from `gridAllow` / `gridDeny` on the shell, because three pages draw one grid:
  a server role and the server's default, a role in one channel and that
  channel's, and a **group's** own. `permissionScope` is still the whole of what
  separates them — and a group adds two answers to it. `group` picks the
  `groupDetails` rewording, for the entries whose line names something a group has
  not got (one: roles). And a group is the one scope that is a plain **set** with
  no deny half, so its rows are switches like the server default's — Revolt keeps
  one value on the channel and ORs it with a view-only floor, which is also why
  `groupPermissions` drops the two bits that floor already grants.
  `ui.GroupSettingsPage` is otherwise the smallest page on the shell: two
  sections, no lists, nothing fetched, so no `cachedList` and no `visit` counter —
  everything it draws arrives as a channel update.
- **A list row's own action is an outlined icon, not a filled button.** A page of
  lists with a filled `Edit` on every row is a column of accent slabs shouting
  from a surface that is mostly read — so `editButton` puts the `action-edit`
  mark in an `OutlinedIconButton`, the same target the invite list's copy and
  revoke are, and the label the button dropped is carried by the row's own name.
  Two exceptions, both deliberate: a row that is a *setting* rather than an entry
  keeps its filled button ("Create" on a New role row is what the card is for),
  and an action **no mark says** keeps its word — the ban list's "Lift", where a
  cross or a bin would read as the opposite of what it does.
- **`entryRow` is the list row, `row` is the setting row.** A setting's label is
  wrapped prose against one control; a list's is somebody's or something's name,
  so it leads with an avatar or a glyph, ellipsises rather than wrapping, and
  takes as many buttons as the entry offers. Its fill slot moves with the lead —
  second when something leads, first when nothing does — because `NewFillRow`
  addresses the stretching child by position. A row offering **more than one**
  action carries `OutlinedIconButton`s rather than labelled ones (the invite
  list's copy and revoke): two words at the end of a row whose own text is
  already a code and a destination is more to read than there is, and the marks
  are `action-*.svg`, so copy and delete mean the same thing here as on a message.
- **`ui.FriendsPage` is a view, not a card.** It stands in the message area's own
  slot with the message column hidden under it (`app.buildMessageArea`), which is
  why it is a page at all: four sections of people, each row carrying what can be
  done about somebody, is a surface a reader stays on rather than an answer to a
  question — and a dialog holding it had to cap its own height and put a wall of
  labelled buttons down its right-hand edge. It is read the way the settings page
  is, and its rows are the settings page's **invite island**: `newIslandCard`,
  which both now share so the two cannot drift a shade apart. Three differences
  from that island:
  - **The card is the row's primary action, not a button on it.**
    `FriendEntry.Open` — writing to somebody, which the controller lifts out of
    `relationshipButtons` — so what is left at the row's end is the rare and the
    destructive, and there is no high-frequency target one square from Block.
    Nil where Revolt would refuse the conversation (it opens one only between
    friends), and the card falls back to the profile.
  - **So it answers the pointer.** It fills (`FriendsCardHoverBg`) *and* lifts its
    rim by `cardHoverLift` — the outline is drawn at rest everywhere here, so hover
    has to brighten one rather than add one. Everything on the card that takes its
    own hover hands it back: the buttons through `OutlinedIconButton.reporting`,
    the picture through `islandLink`.
  - **Its marks are picked from `ProfileButton.Action`**, not chosen at the call
    site: the controller names the *kind* of action and `friendMark` decides the
    glyph and the tint, so the card's word and the page's mark cannot come to mean
    different things. What a mark means is the tooltip's to say.

  **A refill reuses the cards** (`FriendsPage.cards`, by user ID): presence is
  what refills this page while it is open, and rebuilding every card for one
  ring was an avatar, two texts and a row of buttons per person per flicker.
  `friendCard.stands` decides — anything beyond presence moving rebuilds that
  one card, so the closures wired at build always capture the snapshot the
  texts are drawn from — `update` repaints the ring and re-points the
  callbacks, and the list ends in `Relayout` rather than `Refresh`, which
  would walk every reused card and re-upload its avatar. A card leaving the
  *screen* has its hover and tooltip taken down by hand (`friendCard.drawn`),
  a widget no longer drawn never reporting the pointer out; one leaving the
  *list* is pruned in `SetSections`.

  **Every heading folds** (`friendsHeader`), because a section here only ever
  grows: nobody cleans up sent requests, and a hundred of them would stand between
  the reader and their friends. Three things about it:
  - A folded section's cards are **not built**, not built and hidden — which is
    the point of folding one. So `fold` redraws the whole list rather than
    toggling visibility, and `FriendsPage.sections` keeps the controller's last
    answer so a click on a heading is a redraw rather than a walk of every
    relationship. An unfold builds them once; `cards` keeps them across a
    refold, out of the tree.
  - `FriendsPage.folded` is what the *reader* decided, by caption, and is absent
    until they touch one — which is how a decision is told from
    `FriendSection.Folded`, the state the controller asks a section to *start* in
    (Blocked, the one section nobody opens this page to read). It outlives a refill
    and dies with the page, as `App.collapsedCategories` outlives a sidebar rebuild
    and dies with the session.
  - The strip is the column's full width and fills with
    `ChannelHoverBackground` — a channel category's fill, not `ButtonHoverBg`,
    which is a step brighter than the cards it names. Its mark is `drawIndicator`,
    the client's one collapse glyph, and it sits at a card's own left padding so it
    lines up over the pictures below it. The section's explanation is *inside* the
    heading, so the whole block is one target and a shut section costs one line.

  The picture leading a card is the one part of it that does not do what the card
  does — it opens the profile, this client's rule for an avatar everywhere. An
  `islandLink`, not a `TappableContainer`: a fill of its own inside a card that
  already fills is a second shape appearing. Its tooltip is what makes the second
  target findable — a rule is not a label.
  Its column is `cappedWidthLayout`, not the settings page's fixed width: that
  page is a layer over the whole window where this is as narrow as
  `MessageAreaMinWidth`, so the width is a ceiling to shrink under and the
  layout reports **no** width of its own — a minimum here would put the page into
  the window's.
  It carries one control of its own: `buildAsk`, the field and button that send a
  friend request to a typed handle. **One surface**, not a field on a card — the
  button is seated inside the field's trailing edge by `FriendsAskInset`, which the
  bar's height is chosen against (`ButtonMinHeight` plus twice it), and the mark
  leading it starts at a card's own left padding so the bar and the rows share an
  edge. Its focus edge is `askEntry`'s doing: `WithCaret` makes Fyne's input border
  transparent — that being what stops every entry drawing a box inside the one it
  is already in — so the surface's outline lights instead of the entry's.
  It is built **once, in the constructor**, and
  stands between the header and the scroll rather than in the list — `SetSections`
  replaces that list wholesale and presence alone refills it, so a field inside it
  would lose what somebody was typing. It reports through
  `onAsk(handle, done)` rather than clearing itself: the field is emptied by what
  took and kept by what did not, and only the controller knows which.
- **The Security section is the one that fetches, and the one that must not.**
  Its three answers arrive as one `SecurityState` held in `cachedOne` — the
  single-value twin of `cachedList`, same three rules — for the life of one
  *opening*, which `SettingsPage.visit` guards the way a server page's does. A
  late answer rebuilds the **whole section** rather than refilling a list: unlike
  a server's, every row in it is drawn from that one answer, not just the rows
  under one caption. And `loadSecurity` checks `p.indexing` before asking:
  `buildSettingsIndex` builds every section twice on the first keystroke in the
  search box, so it goes in the list with `LoadProfile` and the microphone.
- **Three cards ask for a credential, and each is its own type for one reason.**
  `ChallengeDialog` cannot be a `Prompt` with a picker bolted on: which method is
  chosen is what the field is *called*, so the label follows the pick, where a
  Prompt's fields are fixed once it is built. Its field is masked whichever method
  is chosen — a password must be, a recovery code is a secret written down once,
  and a TOTP code masked is at worst unremarkable, where a field that changed its
  mind about hiding what is in it as the picker moved would be worse than either.
  `SecretDialog` holds the TOTP key **and** the code confirming it on one card,
  the key being shown once. `NewCodesDialog` has no confirming action at all: the
  codes *are* the outcome, so its button copies. All three draw a value somebody
  has to read off the screen through `newSecretWell` — monospaced so a zero and an
  O are different shapes, selectable, on the same field surface every other value
  on a card sits on.
- **A group card's rows are that same island, answered rather than followed.**
  `ui/group.go` is one builder behind two constructors, the way `ChannelDialog` is
  — a new group and an addition to one differ in a name field and nothing else.
  Three things about a `pickRow`:
  - **Chosen outranks hovered.** Three fills, one rectangle: a pointer passing
    over must not paint over an answer already given, so `repaint` asks in that
    order and `GroupPickChosenBg` is a colour of its own rather than the hover
    fill reused.
  - **The mark at its end is not a button.** Innermost wins, so an
    `OutlinedIconButton` there would take both the tap and the hover from the row,
    and the whole row is the one answer. `pickMark` is therefore a plain widget
    that is not `Hoverable`, drawing one rim whose stroke moves and one tick that
    is hidden until it is wanted — the tick is tinted at construction, which is
    what a `tintedIcon` raster is cached under, and it is never recoloured.
  - **The list is capped and carries no indicator.** `cappedHeightLayout` +
    `NewPlainVScroll`, the arrangement `panels.go` already uses, and the ceiling
    is chosen to cut a card in half: the rows carry their own mark down the right
    edge, which is where a bar would land, so the cut is the whole of what says
    the list goes on.
- **The screenshare picker is that same island row with a radio's rule.**
  `ui/screenshare.go`'s `shareSourceRow` is `pickRow` — the same card, the same
  three fills in the same precedence, the same `pickMark` at the end — and the
  only difference is that picking one *unpicks* the rest, which the dialog
  enforces rather than the row. Its two option runs are `searchChip`s carrying
  the number they stand for (`pickChip`, in `search.go` beside the chip it
  extends, shared with the crop card): a filter chip is a bit where one of these
  is a member of a set, so `markPickChips` is what a tap runs and the chip itself
  stays the same widget. The list is `cappedHeightLayout` +
  `NewPlainVScroll`, `panels.go`'s arrangement, because a machine with thirty
  windows would otherwise open a card taller than the screen it is offering to
  share.
- **`IconButton` is a mark; `OutlinedIconButton` is a target.** The first is
  right where the row has already said what is going on and the icons are
  *revealed* — a message's hover actions — and wrong where the icon is the only
  thing offering the action at all, which reads as decoration. The second wears
  the client's one hairline in the **icon's own tint**, so a destructive one is
  outlined in the colour it is drawn in and needs no other signal; hover fills
  with the neutral `ButtonHoverBg` rather than a wash of that tint, a tinted fill
  having to be mixed against whatever card it is laid on and coming out a
  different colour on each. It is square (`IconButtonSize`) at a text button's
  own height, so a row can hold either, and its mark is a named size rather than
  a fraction of the box. `disabled()` is how one offers nothing: the tint is
  chosen by the *caller* and baked into the resource at construction (that is what
  the raster is cached under), so an unavailable action is built in
  `ButtonDisabledText` and then made inert — no tap, no hover, and the arrow back,
  `tapBase` promising a pointer to everything.
- **A place in an order is two buttons.** `moveButtons` is the pair every list
  row offering one draws — the channels and categories of a server's settings, and
  its roles — chevrons from the `action-*` set, and **both drawn whatever they can
  do**: a row at either end wears a dead one rather than a shape of its own, a
  list being read down its right-hand edge as much as down its left. The
  *setting*-shaped row inside the role editor keeps labelled `Up`/`Down` buttons
  instead, which is the same split as everywhere else here: `entryRow` takes
  marks, `row` takes words.
- **A control that owns a *device* must be stopped by `showSection` *and*
  `Close`.** The settings page has no unmount hook, and "a discarded widget hears
  nothing" is merely wasteful for an animation and a real leak for a microphone:
  the Voice section's level meter (`settings_voice.go`) holds an open capture
  stream, so `stopMeter` is called from both, exactly where `p.previews` and
  `p.account` are cleared. The controller stops it a third time in
  `resetSessionState`, signing out not being something that closes the page.
  The same rule bites one level further out: `buildSettingsIndex` builds **every
  section twice** on the first keystroke in the search box, so a hook that opens a
  device is nil'd there beside `LoadProfile` and `CacheStats`, and the row stays
  findable by returning a `newIndexRow` instead of a built one — the shape
  `p.preview` already uses.
  The meter is a row *inside* the Microphone group, directly under Sensitivity,
  because it is not a reading — it is the other half of that control, and a
  threshold cannot be aimed without seeing what your own voice measures.
  The level and the threshold arrive as **ratios on one scale** (what
  `StartInputMonitor` reports, and `SettingsHooks.GateRatio`): the scale is
  decibels and belongs to `internal/audio`, which `ui` does not import, so a
  second copy of the mapping here would be free to drift from the gate's own. A
  linear bar is what that replaced — speech sits at 0.05 of full scale and the
  default threshold at 0.0025, so both drew on the floor.
  **Sensitivity is set in dBFS**, the unit the bar under it is drawn in and the
  unit every other level on the page already used. It was an arbitrary 0-100 onto
  that very range; `InputMeter.LevelDB` is the figure the bar now says beside
  itself, so the row and the meter are read in one unit. `ui` still converts
  nothing — the ratio *and* the figure both arrive computed.
  **`echoRow` sits directly under it** and is the other half again: the bar says
  the microphone is heard, and only playing it back says what it sounds like once
  the chain has had it. A plain `boolRow`, never a setting — it is a mode
  somebody turns on to listen to, and one restored at the next launch would howl
  at whoever left it there — so it is always built at off and the controller's
  own two exits clear it, which is what keeps that honest.
  **`speechMeterRow` is the second bar and belongs to a different control**, so
  the two are a **group of their own** — *Voice activity detection*, the veto and
  the bar that makes it aimable, with what they both depend on said once in the
  caption instead of on each row. Noise suppression is split out the same way,
  and neither split costs a search term: `recordGroup` files a caption as a hit
  of its own, which is what frees the rows inside from repeating it (the toggle
  is "Remove background noise", the dial "Strength", the veto "Threshold").
  The **order of the groups is load-bearing**: the bar rides the stream
  `levelMeterRow` opened (one report, `ui.InputMeter`, both bars) and is built
  after it or not at all, so Microphone stands ahead of both — which is also why
  its two setters are filled onto `voiceLevelMeter` rather than held by the row.
  `newMeterBar` is `newLevelBar` plus the figure
  itself at the row's trailing end, and **both** voice meters are one: the bar
  answers "over the line right now", which is what aiming a threshold needs, and
  the number answers "by how much", which a strip a few pixels wide cannot. The
  caller formats it (`meterDecibels`, `meterPercent`) — the wording is the row's.
  A negative `Speech` is the model not running — drawn as an empty bar and a
  dash, never as zero.
  Two other units follow from that. The veto's own row is **`%`**, which is what
  it always was and was drawn without; `numberBox.valueText` sets a percent sign
  tight against the figure and a unit word apart from it. And **Smooth loud
  peaks** left Playback for a group of its own: `softClip` is both directions,
  so under Playback the row had to apologise for its group in its own
  description.
- **The speaking ring needs headroom or it clips.** `VoiceParticipantRow`'s ring
  is built like the presence ring — the circle exists from construction at
  `color.Transparent` and only its `FillColor` moves, nothing being added to a
  container after the fact — and shares `memberRingLayout`, which now carries its
  band rather than reading `MemberPresenceRing` directly. `VoiceRowHeight` has to
  clear `VoiceAvatarSize + 2 × VoiceSpeakingRing`; it was 26 against an 18 px
  avatar and is 28, which is why that row got taller when calls arrived.
  `SetSpeaking` no-ops on an unchanged value, the contract `MemberRow.SetMember`
  and `ChannelWidget.SetState` already keep — see `internal/app/CLAUDE.md` item 36
  for why anything else is a full window repaint per syllable.
- **The three holds on a voice row are shown, never added.** `VoiceMarks` is the
  whole of what a row says about somebody's voice, and the glyph names *what* is
  held while the colour names *who* held it: `VoiceHoldServer` for a moderator's,
  which the person cannot lift, `VoiceHoldSelf` for their own switch, and the
  row's plain `VoiceParticipantMark` for the speaker struck through, which is
  about this machine rather than about them. What a participant is *sharing*
  cannot change under a standing row — the gateway announces it and the column is
  rebuilt — so those marks are added only where they apply; a hold can, so all
  three are built hidden by `newVoiceHold`. The screenshare mark is the one that
  answers a tap (`shareWatchTap` → `Actions.OnWatchShare`, in `VoiceShareLive`
  so a target reads apart from the grey marks): a `tapBase` widget on purpose —
  not Hoverable, so the row keeps its hover, the pointer cursor being the whole
  affordance. It is built for **every** participant including this account,
  whose own tap opens a preview of what is being sent; the widget knows nothing
  of that, the controller deciding what a mark means. Each is the glyph **and the gap after
  it** in one container, hidden together: an HBox skips an invisible child when it
  measures, but a spacer beside a hidden mark is not itself hidden, and three of
  those are dead width at the end of every row that holds nothing. `SetMarks`
  ends in `Relayout` of the row's Border rather than a `Refresh` of the strip —
  the strip is the trailing slot, so its width is what the name's ellipsis box was
  measured against.
- **`ui.CallIsland` is a fourth kind of surface**, and not any of the three it
  looks like. Not a `messageIsland` (the modal layer's surface for the three
  message lists), not a composer dock badge (everything in that stack is about the
  *open channel*), not a strip under the message header (which every view without
  one would have to draw for itself). It floats on a layer of its own at the top of
  the window — `NewCallIslandLayer`, stacked under the notice and settings layers —
  because a call outlives leaving the channel, the server *and* the view. It is
  drawn as the settings page's **invite card** — `SettingsGroupRadius`,
  `SettingsRowPaddingH`, the same two text sizes and the same lifted outline —
  because it is the same shape doing the same job somewhere else, and two cards a
  shade apart read as a mistake. Five things about it are load-bearing:
  - **The layer reports no minimum** (`NewLayer`), or a card appearing would grow
    the window and Fyne would never give the room back.
  - **The card is as wide as what is in it**, and a widget does not re-measure the
    layer it floats on. Every setter is followed by `Sync` and then a `Refresh` of
    the layer — `Relayout` alone re-runs one container's layout and would not
    re-measure the nested `Center`. `app.settleCallIsland` is the pair.
  - **A part that comes and goes must not cost a gap.** `NewGapRow`/`NewGapColumn`
    charge a gap per *visible* child, so a half with no server to name hides the
    **slot** holding that line, not the text inside it.
  - **The picture is keyed on what it is drawn from, not on an ID.**
    `CallIslandWhere.icon()` joins the URL, the letter and every face; `setWhere`
    runs on every sync and re-asks only when that string moves, which catches an
    icon changed without its channel doing. The slot is *refilled* rather than
    re-pointed — a load already in flight lands in the container it was handed.
    `islandIcon` is a widget rather than a bare `GridWrap` because the row stretches
    every child to its full height and a grid pins its cell to the top of that; its
    `MinSize` width is whatever it currently holds, a cluster being wider than one
    circle.
  - **A group with no picture is the faces of the people in it.** `setFaces` +
    `facesLayout`: each face wears a `FaceRing` band of `CallIslandBackground`,
    which is what cuts it out of the one behind rather than letting two circles
    smudge into one shape — so the band has to stay the *card's* colour, not a new
    one. Objects are reversed before laying out and placed from the right, because
    Fyne paints in order and the first face is the one that must be on top.
    `FaceSize + 2×FaceRing` must not exceed `IconSize`, or a group makes the card
    taller than a server does. One face is drawn as a plain picture instead: a
    stack of one is a small circle in a slot with room for a whole one.
  - **Its fill is the darkest surface the client has** (the server rail's), not a
    lighter one. A card floating over the page is lifted by its outline and its
    shadow; a *lighter* fill reads as a hole cut in the page. Which is also why the
    rule between the halves is `CallIslandOutline` and not `NewColumnDivider` —
    the client's one hairline is darker than every surface it meets, and this card
    is darker than that hairline.
  - **Every control on it is outlined.** The three call buttons are
    `OutlinedIconButton`s carrying their tint in a hairline, as the invite list's
    copy and revoke are, and `Button.outlined(tint)` is the same treatment for the
    one text button the client has — a filled block is the loudest thing on a
    surface this dark. Unjoinable greys it (`Disable`) rather than reddening it:
    the action is unavailable, not dangerous, and red is what this client reserves
    for what cannot be undone. Greyed keeps a faint `ButtonDisabledBg`, which is
    what says the button is still there.
  - **Hover lights the text, not a rectangle behind it.** `NewTappableContainer`
    fills `TappableHoverBg`, and a fill the height of two lines on a card that *is*
    the only panel in play reads as a button nobody drew an edge on. `islandLink`
    is the same tap target reporting its hover instead, and `lightCall` brightens
    the two lines a step each — name to `CallIslandTextHover`, server to
    `CallIslandText`. The icon is outside the target: it names the server rather
    than offering anything.
  - **A 3 px bar is not a hover target.** `stateBar` reports `BarHeight + BarGap`
    as its minimum and draws the fill at the bottom of that, so the gap above the
    bar is hover room rather than a spacer between two objects. Its tooltip goes
    through `Tooltip.ShowBelow`, not `ShowAbove`: the card is at the top of the
    window, so there *is* room above it and `ShowAbove` would never fall through to
    its second placement — it would label the card by covering it.
  - **The bar's colour is a grade the controller hands over**, `ui.CallQuality`,
    the way a device list arrives as `ui.AudioDevice`: three answers, three
    colours, and no opinion here about what a bad call is. What the tooltip says
    is the controller's too — `SetState` takes the word the bar stands for, and
    the numbers behind it are appended where the tooltip is actually shown, so a
    measurement arriving every couple of seconds costs the card no relayout.
  - **The bar belongs to the live half, not to the card.** It is the last child of
    that half's own column, so it ends where the half does — a gap short of the
    rule, the margin it starts at on the other side — and goes down with it rather
    than being hidden on its own. What it reports is the running call; the other
    half is an offer with no state. The cost is `joinReserve`: the bar's height
    standing empty under the join half, shown and hidden **with the bar**, or the
    two halves' lines sit at different levels — one centres over the card's whole
    height, the other over what is left above its bar.

  It sits over the message header, which means it covers whatever is in that row's
  centre — the channel topic. That is the cost of the slot, not a bug.
  Its **three** toggles replace their buttons rather than recolouring them:
  `OutlinedIconButton` bakes its mark, its tint and its `disabled()` state at
  construction, so a slot with a rebuilt button is the only way to change any of
  the three. The share button is the third and has three states rather than two
  — sharing (`VoiceShareLive`, so the card says at a glance that this machine is
  on screen somewhere), on offer, and greyed where the call's own token does not
  grant it. Greyed rather than absent, for the reason the join offer is: a
  control that vanished reads as a client that forgot.
- **A wrapping grid reports one row until it has been laid out.**
  `layout.GridWrap`'s `MinSize` multiplies the cell by a `rowCount` computed
  during `Layout`, so a fresh grid measures one cell tall whatever is in it. The
  emoji picker survives it by only ever *shrinking* — it opens with the whole set
  and re-`Resize`s the pop-up per query. `gifColumns` has the same shape of
  problem for the same reason (it stores the width its last `Layout` was given, so
  `MinSize` has one to answer with), and the GIF picker's grid additionally starts
  empty and fills from a request — so its viewport is a fixed height
  (`GIFPickerMaxHeight`) rather than a `cappedHeightLayout`: a card measured off
  that grid would open at nothing and grow with every page. A pop-up takes its
  size once, as it is shown.
- **A picture-filled card wears its edge on top.** `gifTile` is the attachment's
  arrangement at tile size: the picture reaches the tile's edge, so the hairline
  is a rectangle stacked *over* it (`rim`) and hover lifts that as well as the
  fill under the picture's 3 px frame — a fill alone is three pixels of colour
  nobody reads as hover. The frame is what keeps the corners rounded, `canvas.Image`
  having no radius and Fyne clipping nothing.
- **`ChipBg` is the emoji picker's island colour.** So the client's chip drawn on
  that island is an invisible pill — which is what `gifChip` is for: the same
  shape, filled with the tile's colour instead. Anything else mounting a chip on
  `NoticeBg` has the same problem.
- **A GIF plays through the hover its frame already has.** `ui.gifAnimator`
  (gifanim.go) is a driver, not a widget: a Hoverable of its own inside a
  `HoverableStack` or a `gifTile` would steal their hover (innermost wins), so
  `SetPlaying` rides the stack's `onHover` and the tile's `setHovered`. Playback
  is one reusable RGBA buffer composed per tick (disposal, then the frame's
  patch) and a `Refresh` of the mounted `canvas.Image`; the frames are decoded
  per hover and freed on `MouseOut`, so rest costs nothing and every hover
  starts at frame zero. Per-frame delays mean a rescheduled `time.AfterFunc`
  through `DoOnUI`, not a `fyne.Animation` — gen-guarded, the fired-timer trap —
  and each tick asks `CanvasForObject` before painting, because a dropped row
  hears nothing and the tick is the only thing placed to stop itself.
- **The video card is pushed into, and decides nothing** (`ui/video.go`). Every
  `VideoCard` setter is UI-thread only and every decision is an `OnVideo*`
  action — the controller owns fetching, decoding and the one-playback rule
  (`internal/app/CLAUDE.md` item 45). What is this package's:
  - **The chrome is one custom layout over the picture stack**
    (`videoChromeLayout`): scrub along the bottom edge, chip lower-left above
    it, sound toggle upper-right, badge centred. It reports no minimum — the
    poster box under it is what sizes the stack, reserved and re-fitted the way
    `imageFrame` does it (`SetInfo` refits when the server sent no dimensions).
  - **`ShowFrame` copies.** The pump's scratch is reused the moment its waited
    hop returns, and Fyne's renderer uploads the mounted image on its own
    thread — so the card keeps its own `playBuf` and the copy is what keeps the
    painter off bytes the pump is about to overwrite. `EndPlayback` drops the
    buffer and restores the poster, the GIF animator's stop-frees-everything
    rule.
  - **The badge and chip are not widgets**, so a tap on either falls through to
    the `HoverableStack` whose tap is the transport toggle. The sound toggle
    and the bar's open button are `GlyphButton`s and win their own taps
    (innermost); they steal hover from the stack, which for this card costs
    only the outline lift. The toggle swaps its own `icon.Resource` — same
    package — rather than being rebuilt.
  - **The scrub strip is taller than the line it draws** (`VideoScrubHeight`
    over `VideoScrubLine`, the stateBar's arrangement): the gap above is the
    hover room a 3-pixel target does not have. `setFraction` resizes the fill
    directly — placement, not layout — and its refresh rides the frame that is
    already dirtying the canvas. Tap seeks by fraction; there is no drag.
  - **The chip is one slot with a precedence** (`syncChip`): a status outranks
    the clock, the clock exists only while playing, the duration only once
    probed, and a card with none wears no chip. Status text arrives from the
    controller (fetch progress, "Not playable") and empty restores the rest.
  - `buildEmbedVideo` is the same card inside an embed, wearing the tap and
    menu wiring an attachment's stack carries — an embed is otherwise inert —
    and seeds the unfurl's own poster so the box is filled before any decode
    has run. `VideoCard.Loop` is the GIF mark: a fact set at build, read as
    policy by the controller.

- **The crop card is one card for every picture the client uploads**
  (`ui/crop.go`). It takes an `image.Image` and answers with an
  `image.Rectangle` in the source's own pixels — no file, no format, no Autumn —
  so the five things that upload one share it the way they share a file picker.
  Its shapes arrive as `CropAspect`s the controller chose, the `ui.ShareSource`
  seam again: which frames a *server banner* is worth offering is a fact about
  Revolt.
  - **The frame is held in source pixels, not screen units.** The answer is in
    pixels and the card can be re-laid out under the reader, so `cropStage`
    keeps a `cropBox` of float64s and the layout is the only thing that knows
    the scale. Floats because a drag is a screen distance divided by that scale:
    rounding per event rather than per gesture is a frame that lags the pointer.
  - **A wash with a hole in it is not a shape Fyne draws**, so what is dropped is
    four `canvas.Rectangle` bands laid around the frame, re-placed with it. The
    frame itself is a stroked rectangle with no fill, and `Round` adds the circle
    an avatar is actually seen through inscribed in it.
  - **What a gesture is doing is decided once, on its first `Dragged`**
    (`grabAt`): a corner within a handle's reach resizes, anything else moves. A
    corner dragged past the opposite one goes on resizing rather than turning
    into a move under the pointer — and `DragEnd` is what resets it, as well as
    being what makes the driver deliver `Dragged` at all.
  - **The stage is handed a preview, not the file.** `CropRequest.Size` is the
    true pixel size the answer is in, and `Picture` is whatever the controller
    thought worth uploading to a texture — see `app/pictures.go`.

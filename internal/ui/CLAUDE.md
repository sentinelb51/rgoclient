# internal/ui

Widgets, layouts and theme. Imports `cache`, `config`, `domain`, `markdown`,
`util` — never `revoltgo`, never `app`. See the root `CLAUDE.md` for the DAG,
naming and the test policy.

## Fyne footguns

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
  `App.bindKeys`.
- **A submenu is drawn by the menu, not by the pop-up around it.** `fyne.MenuItem`
  with a `ChildMenu` works inside `ui.contextMenu` — `widget.Menu` appends the
  child to its *own* renderer objects and places it beside the item — but the
  child is outside the `widget.PopUp` the parent was put in, so the hairline
  `newContextMenu` stacks over that pop-up does not reach it. A submenu therefore
  wears Fyne's own menu background and no border; the member menu's roles and
  timeout spans are the two that use one, both being lists too long to flatten
  into the menu itself.
- **Two cards are elevated.** `ui.Elevate` casts a `canvas.Shadow`: the composer
  dock, and the emoji picker's island over the plain panel Fyne draws behind any
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
  `lineHeight × rows + InnerPadding × 2` (`composerMinSize`) — the input border
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
  `OnResize` → `App.resizeDock` when a width change re-wraps.
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
- **Mixed text sizes on one line** align by being siblings in an HBox — a
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
  walk (`memberListLayout.MinSize`, `messageListLayout.MinSize`):
  `container.Scroll` asks its content for a minimum on every offset write.
  `Container.Add` is the same trap one child at a time (it refreshes the whole
  container per call), so a list is built into a slice and written to `Objects`
  once. Nothing in this repo fills a container in a loop. One level below all of
  that, `Canvas.dirty` is a single bool: **any** `Refresh` anywhere repaints the
  whole window, framebuffer clear included. There is no such thing as a cheap
  one — see `docs/performance.md`.
- **The message column measures in its layout.** Rows are variable-height, so
  `MessageList` places a row by an estimate until its widget has been laid out at
  the column's width, and the only place that width is certain is
  `messageListLayout.Layout`. It is also the one hook Fyne offers for a row that
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
  It is **not focusable**, unlike Fyne's: an entry that submits through one does
  it from `OnSubmitted`, calling `Tap` rather than the action, since the action
  bypasses the disabled check and would send an in-flight request twice.
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
  `ui.PickFolder` are the shell's Common Item Dialog (`IFileOpenDialog`) over the
  HWND `driver.NativeWindow` hands back — the same route `FlashTaskbar` takes —
  run on a locked OS thread in a single-threaded apartment, the dialog pumping
  messages of its own, so it is a window beside the client rather than a layer
  over it and the client keeps painting behind it. COM has no binding in the
  standard library: the vtable indices in `filedialog_windows.go` *are* the
  interface contract, fixed by inheritance order and discoverable nowhere at
  runtime, and one wrong number calls a different method. Both report false where
  there is no native picker, which is what sends the caller to Fyne's.
- **A row's wash is an animation, never state.** `MessageWidget.Flash` (a jump)
  and `FlashEdit` (an edit landing) both go through `flashWash`: `fill()` is
  untouched, so the row answers the pointer throughout and the last tick hands
  the background back to it. It mixes two **opaque** colours — the palette writes
  straight alpha into `color.RGBA`, which Go composites as premultiplied (see
  `theme.Fade`), so fading a wash down its alpha darkens the row on the way out.
  The two differ only in colour and in the strength curve: a jump starts at full
  wash and lets go, an edit rises and falls inside its second, nobody having
  asked to be shown that row.
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
- **One shell draws both settings pages.** `settings_shell.go` owns the surface —
  the layer, the rail, the pane, the group offsets, the flash, the popover — and
  the vocabulary of rows every section is assembled from. `SettingsPage` and
  `ServerSettingsPage` **embed it by value**, so a section reaches `p.group`,
  `p.row`, `p.note` and the rest by promotion and never names it; that embedding
  is also why extracting it changed no call site in `settings_sections.go`.
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
  A section that **drills into one of its rows** — the role editor, the only one —
  is still one section: `ServerSettingsPage.roleID` decides which groups
  `rolesSection` returns, so the rail entry stays marked and tapping it is what
  goes back, and the editor's group captions become the rail's sub-entries the
  way any other section's do. The way in is `showRole`, and it must be re-derived
  on every build: a role can be deleted, or moved above this account's own rank,
  while its editor is open. What says so is the **back button above the title**
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
  every dropdown.
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

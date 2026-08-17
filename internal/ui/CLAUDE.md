# internal/ui

Widgets, layouts and theme. Imports `cache`, `config`, `domain`, `markdown`,
`util` — never `revoltgo`, never `app`. See the root `CLAUDE.md` for the DAG,
naming and the test policy.

## Fyne footguns

- **Innermost object wins.** Fyne delivers hover and pointer events to the deepest
  object that accepts them. Do *not* implement `desktop.Hoverable` with no-op
  methods — an inner widget that accepts hover steals it from its parent row (why
  `ui.Avatar` isn't hoverable). Anything interactive inside a message row is passed
  `MessageWidget.TappedSecondary` at construction: the avatar, each attachment, the
  reply preview, the embed title. The body is the awkward one — a selectable
  `widget.Label` mounts an unexported selection overlay that answers right-clicks
  itself, so `ui.bodyText` lays a `selectionCatcher` over it (right-clicks stop
  there, press/drag/tap forward down). If a future Fyne stops exposing the overlay,
  `newSelectionCatcher` returns nil and the body is a plain selectable Label.
- **Embed cards are inert containers, not widgets**, so hover and right-click reach
  the message row underneath. Only the title (`embedLink`, its own type because
  `TappableContainer` is hoverable) and the picture are widgets. Wrapping text
  can't be asked how wide it wants to be — it answers with whatever it was last
  given — so `embedContentWidth` measures it as one unbroken line and caps it at
  `EmbedMaxWidth`.
- **One hairline draws every edge.** `theme.Colors.Outline` at
  `theme.Sizes.OutlineWidth` is the *only* border in the client. A card sized by
  its own padding wears it on its background (`ui.Outline(rect)`); a card whose
  content reaches its edge — a picture — needs it on a rectangle stacked *over* the
  content, which is what `HoverableStack`'s rectangle is for. Columns carry theirs
  as a `ui.NewColumnDivider` *inside* their own fixed width, because the main row
  addresses children by position to find the one that stretches (the member
  sidebar's sits on its left so it disappears with the column). The colour must
  stay darker than every surface it is laid against, including
  `MessageHoverBackground`, since a row's hover fill paints *under* any card the
  row contains; because the outline is drawn at rest, hover must *lift* it —
  `AttachmentHoverBorder` is lighter than what it replaces. Fyne's *ambient* shadow
  is off (`AppTheme` answers `ColorNameShadow` with `color.Transparent`): a scroll
  paints it as a gradient along whichever edge has more content past it, a smear
  rather than a line.
- **A context menu is the client's own pop-up.** `widget.PopUpMenu` paints its
  background inside `widget.Menu`'s renderer, and `NewMenu` pins the widget's impl,
  so neither the stroke nor a composed renderer can reach it. `ui.contextMenu`
  puts the menu in a plain `widget.PopUp` with the hairline stacked over it, and
  carries what `PopUpMenu` did around the menu: clamping the position into the
  canvas, and the arrow/Enter/Escape handling — all exported `Menu` calls. It takes
  canvas focus while it is up, which is what keeps Escape off `App.bindKeys`.
- **One card is elevated.** `ui.Elevate` casts a `canvas.Shadow` and only the
  composer dock carries one. `DropShadow` follows the corner radius and paints
  nothing under the fill, so a translucent shadow can't dirty the card.
  `CardShadowBlur` overruns `ComposerDockMargin` on purpose: what it has to darken
  is the message passing *underneath*, and a halo stopping inside the gutter would
  only outline the gutter. Deliberately weak — any stronger and the card reads as a
  bar again. At rest it swallows the hairline it sits on; focus still lights the
  outline accent.
- **Content runs under the dock.** A margin and a shadow are not what make the
  composer float — the message column being *taller than the card* is. A column
  that stops above a card stops at a hard cut through whatever glyph the viewport
  landed on, and that cut is what reads as the top edge of a separate bar.
  `ui.NewFloatingDock` hangs the card over a full-height `messageScroll` so the cut
  lands behind it, a corner radius short of the card's bottom edge (the rounded
  corners would otherwise expose it in the two notches). Nothing shows beside the
  card because `MessageHorizontalPadding` is wider than `ComposerDockMargin` — that
  ordering is load-bearing. `ui.NewDockReserve` wraps the scroll's *content* (so
  `messageList.Objects` keeps 1:1 indexing) and reports `ui.DockReserve` of extra
  height. It measures the card on demand, so a reply preview, attachment row or
  mention picker growing the dock is accounted for without anything noticing.
- **Sidebar widths.** The three side columns are pinned by
  `ui.NewFixedWidthContainer`, not by a minimum-size rectangle: a minimum is a
  *floor*, and `container.NewVScroll` reports its content's minimum width as its
  own, so one long name shoved the message area sideways. Anything rendering a
  user-supplied name into a sidebar row goes in the stretching slot of a `Border`
  wrapped in `ui.NewEllipsisText` (or `Truncation = TextTruncateEllipsis`).
- **The window is grown by what it holds.** `Canvas.EnsureMinSize` resizes the
  window the frame its content's minimum outgrows it, and never gives the room
  back — so anything reporting what it happens to be holding drags the window
  about as messages arrive. The sidebars are pinned (above); the message column
  reports `MessageAreaMinWidth`/`Height` through `ui.NewFixedSizeContainer`
  instead of the widest mounted message and a composer grown by the mention
  picker. Everything stacked over the main row goes through `ui.NewLayer`, which
  reports nothing at all: a notice stack, an open settings page and a tooltip each
  reached the window's minimum, and `container.NewWithoutLayout` does not even
  skip a *hidden* child, so the tooltip kept the longest name it had ever shown.
  `app_test.go` asserts the root's minimum is the same before and after each.
- **Composer geometry.** A growing entry's height is
  `lineHeight × lines + InnerPadding × 2` (`composerMinSize`) — the input border is
  *not* added on top, because `entryRenderer.Layout` pays for it out of the text
  provider's own padding. The dock hangs `ComposerDockMargin` in from three edges of
  the message area, which is why the area stacks through `ui.NewFillColumn`, not a
  `Border`: a Border would charge theme padding between its centre and the dock on
  top of that. The margin is only a gutter — widening it grows the strip, nothing else.
- **Nested `Border`s hide padding.** `container.NewBorder` inserts theme padding
  between each edge slot and its centre, so nesting them charges a row several
  helpings. Reach for `NewFillRow` / `NewFillColumn` / `HBoxNoSpacing` / `NewInset`
  when the spacing has to be exact.
- **Mixed text sizes on one line** align by being siblings in an HBox — a
  `canvas.Text` centres its glyphs in whatever height it is given. Don't wrap one
  in a spacer to nudge it.
- **Message row rhythm.** The avatar is centred on the block a *single-line*
  message occupies, placed by an offset from the top rather than centred on the
  row, so a longer body grows away from it. That offset and the grouped gutter
  timestamp's are *derived* from `messageLineHeight()`, never hardcoded. Anything a
  row can additionally carry (a reply preview) must move the whole row, so it sits
  *inside* the row's margins. A message with no text hides its body slot entirely —
  an empty body still renders one line tall, drawing as a gap above an embed.
- **A system message is not a message anybody wrote.** `NewMessageWidget` branches
  on `Message.System` before building anything: no avatar, name, body slot,
  attachments, embeds or replies — one line with the event's mark centred in the
  gutter an avatar would fill, and the time drawn beside it rather than revealed
  on hover (there is no name for it to follow, and "Someone left" with no *when*
  is half a sentence). The one thing in that line accepting a pointer is the name
  it announces, a `mentionText` opening that profile: the row has no author to
  click instead, which is why `Store.SystemTextParts` hands the name back apart
  from the sentence rather than folded into it. It carries the row's menu and is
  not hoverable — innermost wins, so anything else there would be a hole in the
  row's own hover and context menu. It keeps `SystemMessagePadding` whatever
  surrounds it — `continuesGroup` refuses either side of one, so a run of joins is
  a block — which is why `verticalPad` is a *method*: `SetFollowedByGroup` reaches
  it too. Reply is offered nowhere on one (`canReply`), as edit already wasn't.
  `systemMark` answers with the glyph **and** the colour: the tone is the class of
  event (arrival, departure, removal, a channel change, a call), which is what a
  column of these is skimmed by, and the glyph is which event of that class it
  was. An unknown kind takes the generic mark in neutral grey, so an event Revolt
  adds later still reads as an event.
- **Tinting one of the client's own marks is a substitution, not a theme name.**
  Fyne's `theme.NewColoredResource` rewrites an SVG's *fills* and leaves strokes
  alone, and every mark in `assets/` is an outline — so a stroked icon comes back
  white whatever colour name it is given. `ui.tintedIcon` replaces the source's one
  stroke colour instead, and puts the colour in the resource's **name**, because
  Fyne caches the rasterised SVG under that name and two resources sharing one would
  share a raster. That name is also the key of `ui.tintedIcons`, the memo that stops
  a channel of system events rewriting the same files repeatedly; a restyle changes
  the colour, hence the name, so it misses rather than returning a stale palette.
  Message buttons draw `action-*.svg` rather than Fyne's icons for the same reason:
  a themed resource takes its colour from a theme *name*, and delete reading as
  delete is the point.
- **Tap plumbing is `ui.tapBase`, not a hand-written pair of methods.** Embedding
  it supplies `Tapped`, `TappedSecondary`, `MouseMoved` and the pointer `Cursor`
  from two func fields, and every interactive widget here uses it. A widget whose
  menu is assigned *after* construction — the sidebar's server and channel rows —
  sets `onSecondaryTap` to a closure reading its own `Menu` field, so the items are
  built when the click arrives. Deliberately **not** hoverable: adding a no-op
  `MouseIn`/`MouseOut` here would take hover from every parent row, so a widget
  that wants hover declares it itself. `ui.decoratedText` is the one hold-out and
  stays one — its `Cursor` is conditional (a struck word is not a spoiler and must
  not read as clickable), which `tapBase`'s fixed pointer would flatten.
- **Repainting the message column.** `Container.Refresh` refreshes every child and
  `RichText.Refresh` re-wraps its text, so `messageList.Refresh()` re-flowed every
  mounted body on every gateway message. Every mutation of the mounted window goes
  through `App.remountMessages` (`ui.Relayout`: re-run this one layout, don't walk
  the children). Use `Refresh` only when what a *mounted* widget says has changed.
  For the same reason nothing on the scroll path may call `MinSize` on the list —
  `BaseWidget.MinSize` is not memoised. A virtualised list's own layout must
  therefore report its height from a **field**, never from a walk
  (`memberListLayout.MinSize`): `container.Scroll` asks its content for a minimum
  on every offset write. `Container.Add` is the same trap one child at a time — it
  refreshes the whole container per call — so a list is built into a slice and
  written to `Objects` once. Nothing in this repo fills a container in a loop.
  One level below all of that, `Canvas.dirty` is a single bool: **any** `Refresh`
  anywhere repaints the whole window, framebuffer clear included. There is no
  such thing as a cheap one — see `docs/performance.md`.
- **A recycled widget must own nothing it captured.** `ui.MemberRow` is reused for
  a different person as the list scrolls, so every callback on it reads the field
  it needs at the moment it fires rather than closing over a value — a menu that
  captured a member ID kicks the wrong person after the first recycle. An
  asynchronous load has no such field to read, so it carries a `generation` the row
  bumps on every `SetMember` and `release`, and a picture arriving against a stale
  one is dropped. The counter is UI-thread only, `ImageCache.LoadAsync` delivering
  there, so it is a plain `uint64`.
  Restoring a placeholder means putting **the same object** back, not a new one:
  Fyne only learns of a canvas object when the container holding it is refreshed,
  so a row that quietly swapped in a fresh `canvas.Circle` drew no avatar at all —
  hence `newAvatarSlot` handing the placeholder back alongside the slot.
  `ellipsisLayout` rewrites its text object during layout, so a recycled row
  compares against its own `fullName` and re-labels through `ui.SetEllipsisText`;
  reading the object back would take a shortened name for the real one.
- **Fyne's scrollbar is a widget over the content, so the client draws its own.**
  Its `scrollBarArea` lies across the right edge of the content and accepts hover —
  innermost wins, so it stole the message row's. `AppTheme.Size` zeroes *both*
  `SizeNameScrollBar` and `SizeNameScrollBarSmall` (zeroing only the large one left
  an invisible strip still eating hover). What replaces it is `ObservableScroll`'s
  indicator: a `canvas.Rectangle` appended to Fyne's renderer objects (set once at
  construction and never replaced, so composing the slice once holds), accepting
  nothing because it is not a widget. It is placed from `Content.Size()` — never
  `MinSize` — and revealed from the *renderer's* `Refresh` by comparing the offset,
  since every offset change ends there whoever caused it while an unrelated repaint
  must not flash it. It fades through a `fyne.Animation` the renderer's `Destroy`
  stops, so a restyle's rebuild doesn't leave one ticking.
  `ScrollIndicatorWidth + ScrollIndicatorInset` must stay under
  `MessageHorizontalPadding` or the bar draws over the text; a width of zero turns
  it off. Only the message column has one — elsewhere the right edge carries rows
  and controls a strip would obstruct, which is what `NewPlainVScroll` is for: the
  settings pane centres its cards, so an indicator pinned to the pane's edge lands
  on one whenever the window is narrow enough for the two to meet. It leaves
  `indicator` nil, so `CreateRenderer` must not append it — a typed-nil rectangle in
  the renderer's object list is dereferenced by the painter.
  Neither `Scrolled` nor `Dragged` may write `Offset` and call `Refresh`:
  `Scroll.Refresh` walks and repaints every descendant, which for a pan is the whole
  column once per frame. `ScrollToOffset` clamps and refreshes only the renderer.
- **Tooltips and notices are layers over the main row, not canvas overlays.**
  Pushing an overlay routes the whole hit test into it, so the hovered widget would
  never see `MouseOut`. Confirmations *are* canvas overlays, on the modal layer
  with the lightbox.
- **Every text button is `ui.Button`, not Fyne's.** Fyne's is themeable down to
  `SizeNameButtonRadius`, but its background is a rectangle inside a renderer
  nothing reaches, so there is no theme name that puts the client's one hairline
  on it — and an outline is what tells a plain button from the card it sits on,
  its fill being a shade off that card by design. `ButtonWeight` decides the rest:
  a plain button wears the hairline, a weighted one drops it and fills with a
  notice tone (`Tone.weight()` is the bridge, so a confirmation's button and the
  notice reporting what it did are the same red). A filled button lifts its own
  colour under the pointer (`theme.Lighten`) rather than taking the plain one's
  hover fill, and a disabled one carries no tone at all — it is not offering
  anything. It is **not focusable**, unlike Fyne's: an entry that submits through
  one does it from `OnSubmitted`, calling `Tap` rather than the action, since the
  action itself bypasses the disabled check and would send an in-flight request
  twice.
- **Fyne's form widgets do not survive `AppTheme`, and a scoped override only buys
  so much.** `AppTheme.Size` zeroes `SizeNameInputBorder`, from which a
  `widget.Slider`'s track thickness is derived (`trackWidth = inputBorder * 2`), and
  its track is filled with `ColorNameInputBackground`, which `AppTheme` answers with
  the colour a settings group is a card of — so a slider drew as a bare thumb on an
  invisible track. A `container.NewThemeOverride` fixed both and still left the
  thumb swelling into a grey hover disc over the track, because that is Fyne's own
  drag affordance and no theme name reaches it. Hence `settings_controls.go`: a
  control here is canvas objects and a layout. Any Fyne widget mounted for the first
  time is worth rendering to a PNG before believing it.
- **A discarded widget hears nothing.** Dropping a widget out of a container tells
  it nothing at all: Fyne destroys a renderer — and with it whatever `Destroy`
  stops — only when its cache expires the widget, up to a minute after the last
  paint that used it, and hiding an ancestor is not a paint. So an animation is
  stopped by whoever drops the widget (`App.releaseChannelRows`, `restyle`) or by
  the widget being told it is hidden (`ChannelWidget.Hide`), never by trusting
  `Destroy` to arrive. `Visible()` answers for the object alone, so an inner
  widget cannot ask — which is why the member sidebar's status mark is stopped
  through `MemberList.SetSweeping`: the column is hidden by hiding the *container*
  around it, and nothing inside hears that either.
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
  `Modifier`, but a context-menu item is Fyne's own widget and delivers none. Hence
  `ui.ShiftHeld`, a Win32 `GetAsyncKeyState` asked at the moment of the click, with
  a `!windows` half answering false and `shiftSkippable` telling the confirmation
  card whether to name the key.
- **A window cannot ask for attention through Fyne.** There is no API for it, so
  `ui.FlashTaskbar` is a Win32 `FlashWindowEx` on the HWND `driver.NativeWindow`
  hands back — the same route `StyleTitlebar` takes — with a `!windows` half doing
  nothing and `alertSupported` keeping the settings group off a page where it
  would draw switches that do nothing. `FLASHW_TIMERNOFG` is what makes it a
  message waiting rather than a blink: it flashes until the window is brought
  forward, and Windows itself no-ops the call for a window that already has
  focus, so no caller checks.
  `fyne.App.SendNotification` is deliberately not used — see the known gap.
- Any custom widget overriding `Dragged` must also have `DragEnd`.

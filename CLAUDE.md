# rgoclient

A Fyne v2.8.0 desktop chat client (Discord-like) for Revolt, in Go 1.26.4. Uses
`github.com/sentinelb51/revoltgo` for the REST API and gateway websocket.

## Architecture

`internal/client` is the **only** package that imports `revoltgo`. It converts
wire types into `internal/domain` values on the way in; everything above is
written against the domain. The dependency graph is a strict DAG:

```
domain, markdown, config       no internal dependencies
util       -> config
cache      -> domain
client     -> cache, config, domain          (+ revoltgo)
ui         -> cache, config, domain, markdown, util
app        -> cache, client, config, domain, ui, util
```

`config` is a leaf so everything above can read a setting. `cache` deliberately
does *not* import it — budgets and directory arrive as constructor arguments, so
a cache can be built in a test with no settings file anywhere.

The seam is not tidiness: `revoltgo.State`'s caches are unexported and
`newState()` is package-private, so nothing holding a `*revoltgo.Session` can be
built in a test. `domain.Store` can — `ui/store_test.go` has a map-backed
`fakeStore`.

No globals. The `*app.App` controller owns the client, caches and widgets and
passes what widgets need through `ui.Deps` (`Store`, `Images`, `Texts`,
`Actions`). `App.deps()` is the only producer, so **every field is always set** —
widgets do not nil-check them. The only package-level mutable state is pure
measurement memoisation (`ui.lineHeights`, `ui.spaceWidths`), UI-thread only.

### The client's contract

- **`Client.Store()`** — reads, safe from any goroutine, never the network. A
  miss reports `ok=false`. Returns resolved values: a `domain.Member` already
  carries nickname, per-server avatar and role colour.
- **`Client.Events()`** — one buffered channel, gateway order. `app.pumpEvents`
  is its single reader; `dispatch` hops onto the UI thread once per event.
  `client.Event`'s marker method is unexported, so the switch is exhaustive.
- **Action methods** (`SendMessage`, `HistoryBefore`, …) — these **block**. They
  do the request and the cache update and return; they never touch a widget and
  never spawn a goroutine. The caller owns the UI thread (`App.background`).

Logged out is a valid state: reads report nothing, actions return
`client.ErrNoSession`. `Client.session` is an `atomic.Pointer` because actions
read it off-thread. `Client.epoch` counts sessions and each gateway handler
captures its own, so events from a replaced session are dropped; `App.epoch` +
`App.stale` are the same guard on the controller's side.

### revoltgo notes (inside internal/client only)

- `Session.X(...)` = network. `Session.State.X(...)` = local cache, may be nil.
- Attachments/avatars/icons are all `*revoltgo.File`, whose `Metadata` is a
  *pointer*, nil for files the server couldn't introspect — `domain.File` carries
  plain `Width`/`Height`/`Kind` so `client/convert.go` absorbs that nil check
  once. Uploads take `*revoltgo.FileParams`.
- **Known bug:** `Session.ChannelMessages(..., IncludeUsers: true)` only feeds
  Users/Members into State when the request *failed* (`if err != nil` where
  `err == nil` was meant). Hence the batched `ensureAuthor` path; when fixed, the
  batch simply finds nothing to do.
- **Missing field:** Revolt carries `slowmode` (seconds) on a text channel and in
  `ChannelUpdate`; revoltgo models neither, so the number never arrives with the
  channel and nothing announces a change. `Client.FetchSlowmode` is the one action
  that goes round the typed API — a raw `session.HTTP.Request` for
  `EndpointChannel` — and records the result for `store.Channel` to hand back.
  `BypassSlowmode` (`1 << 39`) is missing from the permission constants for the
  same reason and is named in `client/store.go`.
- **Known bug:** `MessageFlags` is a bitfield and revoltgo numbers it 1, 2, 3 —
  positions, not bits — so its `MentionsOnline` collides with
  `SuppressNotifications|MentionsEveryone` and can never be read for what it is.
  `client/convert.go` names the two bits it wants itself.
- **No `context.Context`.** revoltgo's REST layer takes none, so a superseded
  request can't be cancelled — only its result discarded. `Client.fetching`
  (per-channel in-flight dedup → `ErrBusy`) and the epoch counters do that
  instead. Don't thread a `ctx` through to look correct; it would cancel nothing.

## Project structure

Names say most of it; only the non-obvious placements are annotated.

```
cmd/rgoclient/main.go    app ID, fyneDo flag, config.Load + theme.Apply before the
                         first widget. version/build are -X link-time vars
assets/                  at the root because go:embed can't reach above its own file
  fonts.go icons.go      Montserrat cuts; the marks. All stroked outlines — that is
                         what lets ui.tintedIcon colour one

internal/
  config/config.go       Settings tree, Default, Load/Save/Path, Current/Update.
                         Styles holds *overrides* keyed by theme field name, so a
                         newly named size arrives with its default intact. Current
                         is an atomic.Pointer snapshot; writes debounced
  domain/                domain.go (value types; Embed is one shape for every kind,
                         so renderers branch on what is filled in) + store.go
  client/                client.go, convert.go, store.go, events.go, actions.go
  cache/                 cache.go (LRU + TextCache), message.go, image.go
  app/                   app.go, session.go, events.go, navigation.go, messages.go,
                         members.go, overlay.go, profile.go, notify.go, settings.go
  ui/                    ui.go, layouts.go, widgets.go, sidebar.go, message.go,
                         embed.go, markdown.go, attachment.go, input.go, modal.go,
                         profile.go, notice.go, settings*.go, theme/, titlebar_*.go
  markdown/              pure parser -> AST, no UI
  util/                  pure helpers: sizes, IDs, truncation, ULID timestamps
```

Where things live that the filename doesn't tell you:

- `app/messages.go` is the message area end to end — composer dock, submit,
  slowmode, widget construction, load/render and the mounted window.
- `app/navigation.go` holds `buildUI` (the 4-column fill row), both sidebars,
  selection, sidebar context menus and the home/DM view. The `#mention`
  candidates come off the channel sidebar's own walk, as the `@` ones come off
  the member sidebar's, and `OnChannelTapped` — following one — is why selecting
  a server is split into `enterServer` (move both sidebars) and picking a channel
  in it: going through `selectServer` would load the first channel on the way past.
- `app/members.go` holds lazy author resolution as well as the member sidebar and
  the mention candidates, since one `Store.Members` walk feeds all three.
- `ui/widgets.go` is the shared vocabulary: tapBase widgets, `Outline` +
  `NewColumnDivider`, `Elevate`, Tooltip, chips, the avatar loader,
  `ObservableScroll` + its indicator, `AccentText`, `NewEllipsisText`.
- `ui/layouts.go` holds every custom layout, `fitWithin` and `Relayout`.
- `ui/message.go` also owns the system line, the day separator and reply previews.
- `ui/settings_controls.go` holds the controls, none of them a Fyne form widget.
- `ui/theme/overrides.go` holds `Apply` — reflection over the two tables, against a
  defaults snapshot taken at init.
- `cache/message.go`: entries *and* published slices are immutable, so a UI-thread
  reader holding an older slice is safe. Find/Remove/Replace binary-search by ULID.
- `cache/image.go`: memory bounded in *bytes*, plus disk. `Get` stamps mtime, so
  `trimDiskCache` evicts by recency.

## Data flow

1. `App.Run` starts `pumpEvents` before the login screen. Login →
   `Client.Open`/`Login` drops the previous session, registers handlers against a
   fresh epoch, opens the gateway. `startWithLogin` stashes the token in
   `pendingToken` *before* Ready can land. The login screen stays up until Ready.
2. `onReady` → save token, record unreads, `showMainUI`, `refreshServerList`,
   `selectServer(first)` — or `selectHome` when the account is in no servers.
3. `selectServer` → `refreshChannelList`, `refreshMemberList` →
   `selectChannel(first)`. There is **no bulk member fetch**: Revolt's members
   endpoint has no pagination, so large servers would flood memory. Members
   resolve lazily per author.
4. `selectChannel` → cached messages, else `Client.LatestMessages` (deduped per
   channel); ack unread. Callers render from the *cache* (`displayCached`), never
   from a page captured off-thread. `displayMessages` mounts only the newest
   `initialMountCount`; `loadMoreHistory` is two-tier (unmounted cache
   synchronously, then network); the window is bounded at `mountedCap` and trims
   `clear()` vacated slots so widgets are actually released.
   All construction goes through `App.newMessageWidget(prev, curr, next)`, which
   is therefore also where `ensureAuthor` runs and where grouping
   (`continuesGroup`, within `messageGroupWindow`) and the day separator are
   decided. The separator belongs to the widget, not to a list entry of its own,
   so the window stays one object per message.
5. **Author resolution.** A message carries only an author ID. `ensureAuthor`
   checks `HasUser`/`HasMember` (which exist so this allocates nothing — it runs
   per mounted message) and queues gaps; `authorTimer` fires `authorFetchDelay`
   later and `flushAuthors` makes a **single** trip back to the UI thread.
   `fetchedAuthors` guards each (server, user) pair, released on failure so a
   later message retries. A **system** message has no author but names a target
   and reads "Someone joined" until that user is known — so it queues
   `System.Target` and `MessageWidget.Author` answers with it, which is what lets
   `refreshAuthorMessages` cover both in one pass. `RefreshAuthor` relayouts the
   line, since the name sits *inside* the sentence and the time beside it moves.
6. The client caches an incoming message (the cache returns the predecessor under
   its own lock, so grouping survives bursts) and emits `MessageCreated` with
   both. If scrollback has detached the view from the tail the append is skipped —
   it mounts on the way back down. An edit replaces the cache entry with a *copy*
   (entries are read without the cache lock, so they stay immutable). Deletes
   arrive as one `MessageDeleted` carrying a slice → one `removeMessages` pass
   with `rebuildSeams` re-grouping at each seam.
7. **In-place editing.** `startEditing` — one at a time (`App.editing`). Save
   applies optimistically and calls `Client.EditMessage` (failure reverts).
   Message-area rebuilds cancel the active edit; `refreshMessage` leaves a message
   being edited alone.
8. **Mentions.** Typing `@` or `#` at the start or after a space opens the
   picker, which gets first refusal on Up/Down/Enter/Tab/Esc. The marker decides
   which of the picker's two pools is filtered and what the span is rewritten as
   — Revolt's `<@id>` or `<#id>`, which `ui/markdown.go` renders back as `@Name`
   and `#channel`. `MentionKind.marker`/`markerKind` are the only place the two
   characters are named. A heading's `# ` opens the channel list for the one
   keystroke before the space closes it again; refusing the picker at the start
   of a line would cost every mention typed there. Candidates are **pushed** —
   `refreshMemberList` and `refreshChannelList` each build rows and candidates
   from one walk — so a keystroke is two string comparisons per candidate with
   nothing allocated. The picker mounts *inside* the composer card, not
   floating: a Fyne pop-up takes canvas focus, which would stop the typing that
   drives it. Because it is inside the card, **it must not close on blur**:
   Fyne unfocuses on the mouse *press* and re-hit-tests on the release to decide
   where the tap lands, so hiding here resized the composer out from under the
   click and the first click on anything was spent dismissing the picker.
   Visibility follows the caret instead — `syncMentions` from the typing methods
   and from `MessageInput.MouseDown`, which is where `widget.Entry` moves the
   caret. An open picker therefore outlives the entry's focus and can outlive its
   channel, so `SetCandidates` re-runs the query.
   A **rendered** mention is tappable: `mentionSegment` / `mentionText` in
   `ui/markdown.go`, reaching `Actions.OnUserTapped` (anchored on the word, so the
   card opens beside the name) or `Actions.OnChannelTapped`. It is a widget
   because a `TextSegment` carries a colour but not a tap, and that costs what
   every custom segment costs: RichText measures one only to subtract it, so it
   can neither break nor be broken before. Hence per-word splitting *and*
   `mdBuilder.reserve` — the widest mention word in the body, kept clear on the
   right, which is the only thing stopping one that lands at a line end from being
   cut off by the message column. Anything else in a body that answers a click
   (`decoratedText`) carries `onMenu` for the same reason `mentionText` does: the
   driver gives the press to the innermost object accepting one and does not walk
   back up, so a word without the message's menu is a hole in it.
   A message that names the account is washed warm instead of transparent —
   `MessageWidget.fill`, decided once at construction from
   `Message.MentionsUser(Store.SelfID())`. It is Revolt's own `mentions` plus its
   channel-wide flag, not a re-read of the content, so a reply with its mention
   toggle on counts and an `@everyone` counts without naming anybody. The colour
   is a *rest* state, so hover lifts it rather than replacing it with the ordinary
   hover fill.
9. **The home view.** `App.homeSelected` marks it open (home has no server ID).
   The list comes from `Client.Conversations`; the app keeps only the order
   (`App.dmChannels`). No gateway event maintains it, so `selectHome` paints the
   cache immediately and refreshes in the background. Ordering is a snapshot — an
   incoming message marks its row unread rather than re-sorting under the reader.
10. **Joining a server.** The join response does *not* add the server: revoltgo
    decodes it into an `Invite` whose `ServerID` is never populated. The
    `ServerJoined` event does, and `App.pendingJoin` tells that handler to select
    what it adds.
11. **Slowmode.** `selectChannel` paints what is known and fires `loadSlowmode`,
    which re-asks on *every* visit — see the revoltgo note: entering the channel is
    the only moment the client can learn the number, or that it moved.
    `App.slowmodeOf` is the cooldown as it applies *to this account*, so
    `CanBypassSlowmode` collapses it to zero and the badge never appears for a
    moderator. `handleSubmit` refuses while `slowmodeRemaining` is non-zero and
    keeps what was typed, saying nothing: the badge counting down is the answer,
    and a notice per keypress would bury it. The cooldown starts optimistically at
    submit — so a second Enter can't outrun the request — and is given back when
    the send fails; `onMessageCreated` starts it too, covering a message the same
    account sent from another client (`startSlowmode` won't restart a running one,
    so our own echo is a no-op). `refreshSlowmode` re-arms one timer a second at a
    time rather than running a ticker for the life of the app.
    The badge sits *outside* the card, above its right edge, as bare text: inside
    it was furniture the entry had to make room for, and a second filled surface
    just above the card read as a bar growing out of it. `App.composerDock` is that
    row stacked over the card and the whole stack floats, so `ui.DockReserve`
    covers the chip too. Relabelling moves only where the chip starts
    (`SlowmodeBadge.OnResize` → `ui.Relayout`); appearing or disappearing changes
    the stack's height, so `refreshSlowmode` calls `App.resizeDock` — Fyne reclaims
    nothing for a shrinking minimum.
12. **Notices and confirmations.** `App.confirm(ui.Confirm{...})` for anything
    irreversible, `App.notify(tone, …)` for an outcome the user didn't ask about.
    Both take a `ui.Tone` — that is the *only* thing deciding colour, icon and
    button weight. Destructive actions share one shape: a `can…` check decides
    whether to offer it, `confirm…` asks, the action fires through `App.background`
    and the **gateway event** updates the UI. Nothing is removed optimistically.
13. **Settings.** The page is `ui.SettingsPage`, a layer in the window's content
    stack beside `notices.Layer` and `tooltip.Layer` — **not** a canvas overlay,
    because `mountOverlay` closes whatever was there and a confirmation raised from
    settings has to draw *over* it. `App.bindKeys` decides who owns Escape
    (overlay, then settings, then nobody) and is called from all four of
    mount/close overlay and open/close settings.
    A change goes `SettingsPage.change` → `App.updateSettings` → `config.Update`,
    which is all a Behaviour flag needs: they are read where they are used
    (`store.Members`, `continuesGroup`, `messages.go`'s mount caps). A style goes
    through `restyle` → `App.applyStyles`, which rebuilds the theme tables and then
    **defers** the tree rebuild while the page is open (the page covers the client,
    and `SetContent` under a slider mid-drag would take the slider with it);
    `App.stylesDirty` carries that to `closeSettings`. What answers a drag
    meanwhile is the section's own preview, built from real widgets. Styles are
    *overrides* keyed by `theme.Sizes`/`Colors` field names and applied by
    reflection, so the curated groups and the generated Advanced list add up to the
    whole table — `settings_test.go` asserts it.
    The controls are the client's own (`settings_controls.go` — see the footgun on
    Fyne's form widgets). Each sits in a row's `fixedControl`, so a row is the same
    height whichever it holds, which is what lets `numberBox` swap its number for a
    `widget.Entry` without a layout jump. That swap is where a stale focus bites:
    focusing a second box makes the first report `FocusLost` *after* the second
    installed its field, so `numberBox.commit` ignores the reporting entry unless it
    is still the open one. The colour picker floats on `SettingsPage.popover`,
    inside the page's own layer, for the same reason the page isn't on the modal one.
14. **Profiles.** `Actions.OnUserTapped(userID, anchor)` opens the compact card
    beside the anchor; "Full profile" swaps it for the centred dialog. Both draw
    from one `domain.Profile` that `profileOf` resolves in a single pass. The bio
    and banner are one request of their own, made *after* the card is up and filled
    in through `SetProfile`. Only the dialog carries an About section — the card
    names someone, and the bio is what expanding it is for — so a bio grows the
    dialog, hence `repositionOverlay`. The
    banner *replaces* the accent strip rather than covering it: a `canvas.Image`
    takes one radius for all four corners, so the card's own corners are right at
    the top and the bottom band is laid over itself squared off to meet the body.
15. **Role colours.** A Revolt role colour is a CSS value, and the server's own
    presets are as often a gradient as a triple — hence `client.parseColor` reading
    *every* stop and `domain.Gradient` carrying them. A gradient is a `color.Color`
    answering as the mean of its stops, so a chip's dot, a reply's accent bar and a
    picker row keep filling one shape without knowing. Only `ui.AccentText` spreads
    one: a text object takes a single colour, so a gradient name is one object per
    rune, each measured off the whole name up to it (summing single glyphs drifts a
    fraction of a pixel each).
    **A gradient must never reach a `canvas.Text`.** Fyne keys its glyph-run texture
    cache on the text object's fields, colour included, so a fill that can't be a
    map key panics the painter on the frame it is first drawn — off the UI thread,
    where no recover of ours is on. Every colour of unknown origin goes through
    `ui.solidColor` on the way into a text object (`newChip`, `AccentText.newText`,
    `mentionRow.set`); a shape needs nothing, its texture being keyed by the object.
    `widgets_test.go` asserts this over the built tree, because the software painter
    a render test uses takes a different path and would not notice.

## Conventions

- **Keep revoltgo inside `internal/client`.** A new field the UI needs is a field
  on a `domain` type plus a line in `client/convert.go`; a new lookup is a
  `domain.Store` method.
- **Store methods return resolved values and never touch the network.** A miss is
  `ok=false`, not a fetch.
- Background goroutines update the UI through `App.doOnUI(fn, wait)` or
  `ui.DoOnUI(fn)`. `main.go` declares the `fyneDo` migration, so an off-thread
  widget touch is a real data race, not a logged warning.
- **A worker that outlives its session must not paint.** Capture `epoch := a.epoch`
  before leaving the UI thread, check `a.stale(epoch)` on the way back.
- Receivers: `w` widget, `a` app, `c` cache/client, `s` store. Interface
  assertions live near the type.
- **Naming.** Types are `DomainRoot + Modifier`, flat. Constructors are `newX` /
  `NewX` — never `create`/`make`/`build` (`buildX` assembles a UI subtree, which
  is not a constructor). Acronyms stay full-caps (`ID`, `URL`).
- **Structs.** Identity → descriptive data → collections → flags, sections split
  by blank lines and `/* Label */` comments. A mutex sits adjacent to what it
  guards.
- **Functions.** Blank line after the signature; guard clauses over nesting; a
  blank line before the final `return`. `defer Unlock()` by default.
- **Comments.** Doc comments restate the identifier first and stay short.
  **Comment only what the code cannot say** — the non-obvious Fyne/revoltgo
  constraint, the reason an invariant holds. Do not narrate mechanics.
- **Files.** Prefer fewer, larger files with `/* Label */` sectioning.
- Colors and sizes come from `ui/theme`, never hardcoded. Don't express one size
  as an offset from an unrelated one — add a named entry. Adding one makes it
  configurable the same day: the settings page reaches the table by reflection.
- A tunable the user should be able to change is a field on `config.Settings` read
  at its use site, not a `const`. Everything else stays a `const` — the settings
  page is not a dumping ground for every number in the client.
- Use the `log` package for diagnostics.
- Keep this file current when adding files/packages, changing data flow, adding
  widgets, modifying `App` fields, or changing event handling. Keep it *terse* —
  record the constraint and the reason, not the mechanics or the history.

### Tests

Test rules and decisions, not rendering. A test earns its place if it can fail
for a reason a person wouldn't spot immediately: parsing, ordering, caching,
conversion, the mention query, a layout that has to *react* (Relayout,
placeBeside, a card that grows). Do **not** assert that a palette constant is what
the palette says, that a widget was built out of the objects it was just built out
of, or that a hand-tuned offset is still that offset — those only make the next
visual change more expensive. To check appearance, render to a PNG with
`fyne.io/fyne/v2/driver/software`, look at it, and delete the harness.

### Fyne footguns

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
  row's own hover and context menu. It keeps `SystemMessagePadding` whatever surrounds it —
  `continuesGroup` refuses either side of one, so a run of joins is a block — which
  is why `verticalPad` is a *method*: `SetFollowedByGroup` reaches it too. Reply is
  offered nowhere on one (`canReply`), as edit already wasn't. `systemMark` answers
  with the glyph **and** the colour: the tone is the class of event (arrival,
  departure, removal, a channel change, a call), which is what a column of these is
  skimmed by, and the glyph is which event of that class it was. An unknown kind
  takes the generic mark in neutral grey, so an event Revolt adds later still reads
  as an event.
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
- **Repainting the message column.** `Container.Refresh` refreshes every child and
  `RichText.Refresh` re-wraps its text, so `messageList.Refresh()` re-flowed every
  mounted body on every gateway message. Every mutation of the mounted window goes
  through `App.remountMessages` (`ui.Relayout`: re-run this one layout, don't walk
  the children). Use `Refresh` only when what a *mounted* widget says has changed.
  For the same reason nothing on the scroll path may call `MinSize` on the list —
  `BaseWidget.MinSize` is not memoised.
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
  and controls a strip would obstruct.
- **Tooltips and notices are layers over the main row, not canvas overlays.**
  Pushing an overlay routes the whole hit test into it, so the hovered widget would
  never see `MouseOut`. Confirmations *are* canvas overlays, on the modal layer
  with the lightbox.
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
- Any custom widget overriding `Dragged` must also have `DragEnd`.

## Build / check

`go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l internal cmd assets`
(expect no output). Checked out with `core.autocrlf=true`, so write source files
with LF endings.

## Versioning / CI

Calendar versions: `YY.M.D`, UTC, no zero padding (`26.7.29`); a second release
the same day appends a counter. CI builds of `main`/PRs use the same date with
`-dev`. There is no version literal in the source — `main.version` and
`main.build` are stamped at link time with `-X`.

Two `windows-latest` workflows, both running `go test ./...` and building
`dist/RGOClient.exe` with `CGO_ENABLED=1 -H windowsgui` (the tests need cgo:
`internal/ui` mounts real widgets). In `release.yml` they run *before* the version
step, so a failing tree can't leave a tag behind. The exe is unsigned.

- `build.yml` — push/PR to `main` + manual. Uploads the exe as an artifact.
- `release.yml` — `workflow_dispatch` computes today's version, skips past any
  existing tag, pushes the tag and publishes. Pushing a `v*` tag by hand takes the
  tag verbatim — the escape hatch for off-calendar versions.

## Known gaps

Simply not built, no constraint behind it: reply-preview tap navigation
(`buildReplyPreview`), `App.createServer`, typing indicators (the three settings
toggles are honoured by nothing; revoltgo has the calls and events ready),
`ChannelCreate` (a DM opened while running appears at the next DM-list refresh),
attach/emoji buttons (files arrive by drag or paste), role mentions,
mutual friends/servers and relationships on a profile, a notice history panel,
code-block highlighting, a hue wheel/alpha/eyedropper in the colour picker,
`MessageEmbedSpecial` (YouTube, Spotify, …), and moderation beyond the three
destructive sidebar items (banning, role edits, nicknames, channel deletion are
one call away but deliberately not offered).

Where something is limited by revoltgo or Fyne rather than by effort:

- **Slowmode** runs off the client's own clock: the `InSlowmode` rejection carries
  an authoritative `retry_after`, but revoltgo surfaces failures as a formatted
  string. A send refused because the cooldown started elsewhere reports the generic
  notice, and nothing hints at a cooldown outside the open channel.
- **A composed mention** stays a visible `<@id>` until sent — Fyne can't draw a chip
  inside an entry, and mapping names back to IDs at send time breaks on duplicates.
  `markdown.PlainText` has no session, so a reply preview of a message opening with
  one starts with a lone `@`.
- **Text selection** only works on uniform-style bodies, which flatten to a
  `Selectable` Label; Fyne 2.8 has no public RichText selection, so anything
  mixed-style — including any body carrying a mention — is covered by right-click →
  Copy message instead. Selecting *across* messages isn't possible.
- **Markdown:** strike/underline/spoiler share one `decoratedSegment` (Fyne renders
  neither natively), split per word since RichText only breaks rows at spaces. The
  blockquote bar isn't drawn on wrapped continuation lines; a decoration can show a
  one-space nub at a wrap; inline `code` inside a decorated span isn't decorated.
  A decorated word landing at a line end overhangs the column and is clipped, as a
  mention would without `mdBuilder.reserve`; the reserve is not extended to them.
- **Embeds** render site line, title, description, colour and one picture. A bare
  **video** embed is dropped at the boundary (revoltgo carries only the URL, and
  there is no player); a bare **image** embed has the same missing dimensions, so it
  draws against the placeholder until the picture lands.
- **A system line** names only the subject — Revolt sends no actor, so a kick reads
  "X was kicked". A rename says only that it happened.
- **Profiles** don't refresh while open, and the banner is flat: a `canvas.Image`
  takes no gradient mask. The About section is the dialog's alone, and scrolls.
- **A gradient role colour** spreads across a *name* only; elsewhere it fills as the
  mean of its stops. `parseColor` reads hex stops only, so `rgb()` or a CSS name
  falls back to the default text colour.
- **The scroll indicator** only reports position — no drag, no track to click.
- **Settings** that are read once while the caches are built (cache directory,
  message cache caps, text-preview count) need a restart, and each row says so.
  Presence/status and "log out everywhere" have no confirmed revoltgo action. The
  Advanced filter matches field names only; the curated Styles groups aren't
  searchable. The login screen has no notice layer (it isn't built until Ready), so
  `session.go` reports a dead token through `dialog.ShowError`.
- **`domain.Message` drops** what nothing renders: reactions, role mentions,
  pins, masquerade contents (only *that* one exists survives, for grouping).
  `Mentions` and the one flag bit behind `MentionsEveryone` are kept — they are
  what warms a row. `FileKind`
  classifies video/audio/archive/PDF but only `FileImage`/`FileText` are branched on.
- `client.Client` has no test of its own — its actions want an HTTP fake, and
  revoltgo's REST layer takes no injectable transport.
- `assets/` still carries unreferenced `close.svg`, `edit.svg`, `file.svg`,
  `reply.svg`, `trash.svg`; nothing embeds them, and their mix of fills and strokes
  wouldn't take `ui.tintedIcon` anyway.

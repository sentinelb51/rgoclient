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

`internal/config` is a leaf so everything above can read a setting. `internal/cache`
deliberately does *not* import it — its budgets and directory arrive as
constructor arguments, so a cache can still be built in a test with no settings
file anywhere.

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
- Attachments/avatars/icons are all `*revoltgo.File`. Its `Metadata` is a
  *pointer*, nil for files the server couldn't introspect — `domain.File` carries
  plain `Width`/`Height`/`Kind` so `client/convert.go` absorbs that nil check
  once. Uploads take `*revoltgo.FileParams`.
- **Known bug:** `Session.ChannelMessages(..., IncludeUsers: true)` only feeds
  Users/Members into State when the request *failed* (`if err != nil` where
  `err == nil` was meant). Hence the batched `ensureAuthor` path. When fixed, the
  batch simply finds nothing to do.
- **Missing field:** Revolt carries `slowmode` (seconds) on a text channel and in
  `ChannelUpdate`; revoltgo models neither, so the number never arrives with the
  channel and nothing announces a change. `Client.FetchSlowmode` is the one action
  that goes round the typed API — a raw `session.HTTP.Request` for
  `EndpointChannel` decoded into a struct of its own — and records the result for
  `store.Channel` to hand back. When revoltgo grows the field this collapses into
  `store.go` and the request goes away. `BypassSlowmode` (`1 << 39`) is missing
  from the permission constants for the same reason and is named in `client/store.go`.
- **No `context.Context`.** revoltgo's REST layer takes none, so a superseded
  request can't be cancelled — only its result discarded. `Client.fetching`
  (per-channel in-flight dedup → `ErrBusy`) and the epoch counters do that
  instead. Don't thread a `ctx` through to look correct; it would cancel nothing.

## Project structure

```
cmd/rgoclient/main.go        Entry point: app ID, the fyneDo migration flag, theme,
                             app.New(...).Run(). version/build are link-time vars

assets/                      go:embed can't reach above its own file, hence the root
  fonts.go / icons.go        Montserrat cuts; MentionIcon, MembersIcon, AppIcon,
                             the eight settings-rail marks (account, interface,
                             styles, behaviour, notify, cache, advanced, about),
                             the system-*.svg event marks — one per SystemKind, plus
                             SystemEventIcon for a kind Revolt adds later — and the
                             action-*.svg marks a message's own buttons and menu
                             wear. All of them are stroked outlines, which is what
                             lets ui.tintedIcon colour one; Fyne's set is filled and
                             arrives already themed, so it cannot be. Everything
                             else still comes from Fyne's theme icon set

internal/
  config/                    Persisted settings: the only package below everything
    config.go                Settings tree (Interface/Styles/Behaviour/Notifications/
                             Cache), Default, Load/Save/Path, Current/Update. Styles
                             holds *overrides* keyed by theme field name, so a newly
                             named size arrives with its default intact. The current
                             value is an atomic.Pointer snapshot, read off-thread by
                             client/store.go; writes are debounced

  domain/                    The vocabulary: value types + one boundary interface
    domain.go                Gradient (a role colour of several stops; a color.Color
                             in its own right, answering as their mean), File,
                             Message, Embed, Channel (name, avatar, slowmode
                             resolved), Server, User/Member/Role, Profile +
                             UserProfile (the bio and banner fetched after it),
                             composer Attachment/Reply. Embed is one shape for every
                             kind, so renderers branch on what is filled in
    store.go                 Store — the read side of a session, returning resolved
                             values

  client/                    The only importer of revoltgo
    client.go                Client, New/Open/Login/Close/Shutdown, atomic session,
                             epoch, emit
    convert.go               revoltgo -> domain (once, on the way into the cache);
                             parseColor (every hex stop in a CSS value, so a gradient
                             role survives as one), presence/badges, toEmbed (drops
                             embeds nothing can draw)
    store.go                 domain.Store over revoltgo.State. Members() sorts, so
                             the sidebar and mention picker share one walk
    events.go                Event (closed sum type) + the gateway handlers. Each
                             updates the message cache and emits one value.
                             MessageAppend (a late link unfurl) lands as MessageUpdated
    actions.go               Every blocking network action, plus FetchSlowmode —
                             the one that bypasses revoltgo's typed API

  app/                       Controller: view state, image caches, widgets
    app.go                   App, New/Run/lifecycle, doOnUI, background, stale, deps,
                             styleNativeChrome, the ui.MessageActions impl, the one
                             active in-place edit
    session.go               Opening a session, the login screen, the saved-session
                             JSON store (~/.rgoclient_sessions.json)
    events.go                pumpEvents + dispatch, then one handler per client.Event
                             (Ready, message paths, coalesced read-ack, server/channel/
                             member events)
    navigation.go            buildUI (the 4-column fill row), server + channel
                             sidebars, settings window, selection, sidebar context
                             menus, and the home/DM view
    messages.go              The message area end to end: composer dock, submit,
                             slowmode (the gate + the badge's tick), widget
                             construction, load/render, and the mounted window
    members.go               Lazy author resolution, the member sidebar, mention
                             candidates
    overlay.go               Modal layer: showOverlay/showPopover/closeOverlay,
                             attachment lightbox, join-server dialog
    profile.go               Profile card + dialog, profileOf, loadProfile (the bio
                             and banner, fetched after the card is up),
                             openConversation
    notify.go                notify + confirm, and the destructive actions using them
    settings.go              open/closeSettings, bindKeys (who owns Escape),
                             updateSettings/applyStyles/restyle, the hooks handed to
                             ui.SettingsPage, and the cache/account actions
  cache/
    cache.go                 LRU (shared recency tracker) + TextCache
    message.go               MessageCache — per-channel, oldest->newest, capped, LRU
                             channel eviction. Entries *and* published slices are
                             immutable, so a UI-thread reader holding an older slice
                             is safe. Find/Remove/Replace = binary search by ULID
    image.go                 ImageCache — memory (bounded in *bytes*) + disk + async.
                             Get stamps mtime, so trimDiskCache evicts by recency
  ui/                        Widgets. Imports domain; never sees revoltgo
    ui.go                    Deps + MessageActions, DoOnUI, newScaledIcon, context
                             menus, WithCaret (restores the caret AppTheme collapses —
                             wrap every mounted entry)
    layouts.go               Spacers, fitWithin, Relayout, and the custom layouts:
                             noSpacingLayout (VBox/HBox/NewFillRow/NewFillColumn),
                             columnLayout,
                             overlayLayout, minSizeLayout (NewFixedWidth /
                             NewFixedHeight pin exactly),
                             insetLayout (NewInset, negative insets allowed),
                             dockLayout + dockReserveLayout (NewFloatingDock /
                             NewDockReserve / DockReserve — the composer hangs over
                             the message column, which runs under it),
                             flowLayout, popoverLayout (placeBeside)
    widgets.go               tapBase widgets (TappableContainer, HoverableStack,
                             IconButton, SidebarButton, CloseButton, roundedPanel),
                             the shared edge (Outline + NewColumnDivider) and the
                             one lift (Elevate), Tooltip, the chips (NewChip and
                             the RoleChip widget — a dot in the role's colour,
                             right-click for name/ID), the avatar loader,
                             ObservableScroll (and the position indicator it draws
                             over Fyne's own renderer),
                             AccentText (a name in a role colour,
                             split per rune when that colour is a gradient),
                             NewEllipsisText
    sidebar.go               ServerWidget, ChannelWidget + collapsible category, the
                             glyph set, member row/section, saved-session card
    message.go               MessageWidget: construction, permissions, quick actions
                             (actionMark tints the neutral ones), edit mode, hover,
                             content assembly, the system line (buildSystemLine +
                             systemMark, which decides glyph and colour together),
                             the day separator, reply previews + elbows, EditEntry,
                             and the row's vertical rhythm
    embed.go                 Embed cards: site line, embedLink title, description
                             (through renderMessageBody), picture, embedContentWidth
    markdown.go              AST -> RichText; strike/underline/spoiler custom
                             segments; uniform-style bodies flatten to a Selectable
                             Label (bodyText + selectionCatcher); mention() resolves
                             <@id> through Store.UserName
    attachment.go            Image / text preview / generic card, name+size bar,
                             fetchText (byte-capped)
    input.go                 Composer + attachments + reply cards + SlowmodeBadge +
                             the @mention half (mentionQuery/syncMentions/
                             acceptMention, MentionPicker)
    modal.go                 Overlay + tapSink, NewAttachmentViewer, JoinServerDialog
    profile.go               NewProfileCard + NewProfileDialog off one Profile value,
                             presence rings, SetProfile (the bio, and the banner
                             cropped to cover the accent strip), profileWell (the
                             hairline the handle and the bio are framed in).
                             Role/badge rows are the shared chips from widgets.go
    notice.go                Tone (info/warning/danger) + its title, Notice,
                             NoticeStack (Push / PushNotice), noticeCard with its
                             draining countdown, NewConfirmDialog
    settings.go              SettingsPage (the layer, the rail, the pane, the popover
                             slot), the row/group vocabulary, and the rows that reach
                             the tables: sizeRow, colorRow + colorControl, textField
    settings_controls.go     The controls, none of them a Fyne form widget: Toggle,
                             Slider + numberBox (newNumberControl), optionControl +
                             showDropdown, swatchButton + the palette card, the usage
                             bar, the rail button, dismissSink
    settings_sections.go     The eight sections, the curated styleGroups table, the
                             density bundles, the usage meters and the live previews
    theme/theme.go           Colors, Sizes, AppTheme. ColorNameMention is the
                             app-specific colour a RichText segment can carry
    theme/overrides.go       Apply (reflection over the two tables, defaults snapshot
                             taken at init), SizeFields/ColorFields, AccentOverrides,
                             Hex/ParseHex, SetFontSize
    titlebar_*.go            DWM title-bar recolouring (no-op off Windows)
  markdown/                  Pure parser -> AST (no UI)
    markdown.go / parser.go  Node types; block + inline parsing, PlainText,
                             DocumentText. Discord-style emphasis guards
  util/                      Pure helpers only
    file.go                  FormatFileSize, IDFromAttachmentURL
    text.go                  Truncate (rune-safe), InviteCode
    timestamp.go             ULID Timestamp, ShortTime, NiceTime, FullDate, SameDay,
                             DayLabel
```

## Data flow

1. `App.Run` starts `pumpEvents` before the login screen. Login →
   `Client.Open`/`Login`, which drops the previous session, registers handlers
   against a fresh epoch and opens the gateway. `startWithLogin` stashes the
   token in `pendingToken` *before* Ready can land. The login screen stays up
   until Ready.
2. `onReady` → save token, record unreads, `showMainUI`, `refreshServerList`,
   `selectServer(first)` — or `selectHome` when the account is in no servers.
3. `selectServer` → `refreshChannelList`, `refreshMemberList` →
   `selectChannel(first)`. There is **no bulk member fetch**: Revolt's members
   endpoint has no pagination, so large servers would flood memory. Members
   resolve lazily per author.
4. `selectChannel` → cached messages, else `Client.LatestMessages` (deduped per
   channel); ack unread. Callers render from the *cache* (`displayCached`), never
   from a page captured off-thread. `displayMessages` mounts only the newest
   `initialMountCount`. Scrolling up (`loadMoreHistory`) is two-tier: unmounted
   cache prepends synchronously, then network. The mounted window is bounded at
   `mountedCap`; trims `clear()` vacated slots so widgets are actually released.
   Sending jumps to the newest message.
   All widget construction goes through `App.newMessageWidget(prev, curr, next)`,
   which is therefore also where `ensureAuthor` is called and where grouping
   (`continuesGroup`, within `messageGroupWindow`) and the day separator
   (`dayLabel`) are decided. The separator belongs to the widget, not to a list
   entry of its own, so the window stays one object per message.
5. **Author resolution.** A message carries only an author ID. `ensureAuthor`
   checks `HasUser`/`HasMember` (which exist so this allocates nothing — it runs
   per mounted message) and queues gaps. `authorTimer` fires `authorFetchDelay`
   later; `flushAuthors` hands the whole batch to `Client.ResolveAuthors` and
   makes a **single** trip back to the UI thread. `fetchedAuthors` guards each
   (server, user) pair, released on failure so a later message retries.
   A **system** message has no author to draw but does name a target, and reads
   "Someone joined" until that user is known — so it queues `System.Target`
   instead, and `MessageWidget.Author` answers with the target for the same
   reason, which is what lets `refreshAuthorMessages` cover both in one pass.
   `RefreshAuthor` then rewrites the sentence and relayouts the line, since the
   name sits *inside* it and the time beside it has to move.
6. The client caches an incoming message (the cache returns the predecessor under
   its own lock, so grouping survives bursts) and emits `MessageCreated` with
   both. If scrollback has detached the view from the tail, the append is skipped
   — it mounts on the way back down. An edit replaces the cache entry with a
   *copy* (entries are read without the cache lock, so they stay immutable).
   Deletes arrive as one `MessageDeleted` carrying a slice → one `removeMessages`
   pass with `rebuildSeams` re-grouping at each seam.
7. **In-place editing.** `startEditing` — one at a time (`App.editing`). Save
   applies optimistically and calls `Client.EditMessage` (failure reverts).
   Message-area rebuilds cancel the active edit, and `refreshMessage` leaves a
   message being edited alone.
8. **Mentions.** Typing `@` at the start or after a space opens the picker, which
   gets first refusal on Up/Down/Enter/Tab/Esc. Accepting rewrites the span as
   Revolt's `<@id>` token; `ui/markdown.go` renders it back as `@Name`. The
   candidate list is **pushed** — `refreshMemberList` builds rows and candidates
   from one `Store.Members` result — so a keystroke is two string comparisons per
   candidate with nothing allocated. The picker is mounted *inside* the composer
   card, not floating: a Fyne pop-up takes canvas focus, which would stop the
   typing that drives it.
9. **The home view.** `App.homeSelected` marks it open (home has no server ID).
   The list comes from `Client.Conversations`, which feeds channels into State and
   resolves recipients in one pass; the app keeps only the order (`App.dmChannels`).
   No gateway event maintains it, so `selectHome` paints the cache immediately and
   refreshes in the background. Ordering is a snapshot — an incoming message marks
   its row unread rather than re-sorting under the reader.
10. **Joining a server.** The join response does *not* add the server: revoltgo
    decodes it into an `Invite` whose `ServerID` is never populated. The
    `ServerJoined` event does, and `App.pendingJoin` tells that handler to select
    what it adds.
11. **Slowmode.** `selectChannel` paints what is known and fires `loadSlowmode`,
    which re-asks on *every* visit — see the revoltgo note above: entering the
    channel is the only moment the client can learn the number, or that it moved.
    `App.slowmodeOf` is the cooldown as it applies *to this account*, so
    `CanBypassSlowmode` collapses it to zero and the badge never appears for a
    moderator. `handleSubmit` refuses while `slowmodeRemaining` is non-zero and
    keeps what was typed, saying nothing: the badge counting down over the
    conversation is the answer, and a notice per keypress would bury it. The
    badge sits *outside* the card, above its right edge — inside it, it was
    furniture the entry had to make room for. `App.composerDock` is the chip's
    row stacked over the card and that whole stack is what floats, so
    `ui.DockReserve` holds room for the chip too and the entry spans the card in
    every channel. The chip is bare text with nothing drawn behind it: a second
    filled surface a few pixels above the card read as a bar growing out of it,
    so it is set larger instead, which is what holds it apart from the messages
    passing underneath. A relabelling moves only where the
    chip starts (`SlowmodeBadge.OnResize` → that row's `ui.Relayout`); its
    appearing or disappearing changes the stack's height, so `refreshSlowmode`
    calls `App.resizeDock` to re-hang the dock and refresh the scroll — Fyne
    reclaims nothing for a shrinking minimum. The
    cooldown starts optimistically at submit — so a second Enter can't outrun the
    request — and is given back when the send fails. `onMessageCreated` starts it
    too, which is what covers a message the same account sent from another client;
    `startSlowmode` won't restart a running one, so the echo of our own send is a
    no-op. `refreshSlowmode` re-arms one timer a second at a time rather than
    running a ticker for the life of the app.
12. **Notices and confirmations.** `App.confirm(ui.Confirm{...})` for anything
    irreversible, `App.notify(tone, …)` for an outcome the user didn't ask about.
    Both take a `ui.Tone` — that is the *only* thing deciding colour, icon and
    button weight. Destructive actions all share one shape: a `can…` check decides
    whether to offer it, `confirm…` asks, the action fires through `App.background`
    and the **gateway event** updates the UI. Nothing is removed optimistically.
13. **Settings.** `config.Load` runs in `main` *before* the first widget, because
    `theme.Apply` writes the tables everything above is constructed from. The page
    is `ui.SettingsPage`, a layer in the window's content stack beside
    `notices.Layer` and `tooltip.Layer` — **not** a canvas overlay, because
    `mountOverlay` closes whatever was there and a confirmation raised from
    settings has to draw *over* it. `App.bindKeys` decides who owns Escape
    (overlay, then settings, then nobody) and is called from all four of
    mount/close overlay and open/close settings.
    A change goes `SettingsPage.change` → `App.updateSettings` → `config.Update`,
    which is all a Behaviour flag needs: they are read where they are used
    (`store.Members`, `continuesGroup`, `messages.go`'s mount caps). A style goes
    through `restyle` → `App.applyStyles`, which rebuilds the theme tables and then
    **defers** the tree rebuild while the page is open: the page covers the client,
    so nothing of the rebuild would be seen, and `SetContent` under a slider
    mid-drag would take the slider with it. `App.stylesDirty` carries that to
    `closeSettings`, which calls `restyle` — `SetTheme`, `SetContent(buildUI())`,
    `displayCached`, `styleNativeChrome`. What answers a drag meanwhile is the
    section's own preview, built from the real `MessageWidget`/`ChannelWidget`.
    Styles are stored as *overrides* keyed by `theme.Sizes`/`Colors` field names and
    applied by reflection, so the curated groups in `settings_sections.go` and the
    generated Advanced list add up to the whole table — which `settings_test.go`
    asserts.
    **The controls are the client's own** (`settings_controls.go`): every Fyne form
    widget arrives flat or invisible under `AppTheme`, so a `Toggle`, a `Slider`, a
    dropdown and a colour swatch are each a few canvas objects and a layout. Each is
    handed to a row inside `fixedControl`, so a row is the same height whichever it
    holds — and `numberBox` swaps its number for a `widget.Entry` when clicked, which
    is only free of a layout jump because the slot is pinned. That swap is also the
    one place a stale focus can bite: focusing a second box makes the first report
    `FocusLost` *after* the second has installed its field, so `numberBox.commit`
    takes the entry that is reporting and ignores it unless it is still the open one.
    The colour picker floats on `SettingsPage.popover`, a slot inside the page's own
    layer — the modal layer is not an option for the same reason the page itself
    isn't on it.
14. **Profiles.** `Actions.OnUserTapped(userID, anchor)` opens the compact card
    beside the anchor; "Full profile" swaps it for the centred dialog. Both draw
    from one `domain.Profile` that `profileOf` resolves in a single pass. The bio
    and the banner are one request of their own, made *after* the card is up and
    filled in through `SetProfile`; a bio grows the card — hence
    `repositionOverlay`. The banner *replaces* the accent strip rather than
    covering it, because what shows through the picture's rounded corners differs
    top and bottom: a `canvas.Image` takes one radius for all four, so the card's
    own corners are right at the top and the bottom band is laid over itself
    squared off to meet the body flush.
15. **Role colours.** A Revolt role colour is a CSS value, and the presets the
    server itself offers are as often a gradient as a triple — which is why
    `client.parseColor` reads *every* stop and `domain.Gradient` carries them.
    A gradient is a `color.Color` answering as the mean of its stops, so a chip's
    dot, a reply's accent bar and a picker row keep filling one shape without
    knowing. Only `ui.AccentText` spreads one: a text object takes a single
    colour, so a gradient name is one object per rune, each measured off the whole
    name up to it (summing single glyphs drifts a fraction of a pixel each) and
    filled where that glyph falls along the run.
    **A gradient must never reach a `canvas.Text`.** Fyne keys its glyph-run
    texture cache on the text object's own fields, colour included, so a fill that
    can't be a map key panics the painter on the frame it is first drawn — off the
    UI thread, in a `Load` no recover of ours is on. Every colour of unknown
    origin therefore goes through `ui.solidColor` on the way into a text object
    (`newChip`, `AccentText.newText`, `mentionRow.set`); a shape needs nothing,
    since its texture is keyed by the object. `widgets_test.go` asserts the
    invariant over the built tree, because the software painter used by a render
    test takes a different path and would not notice.

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
  widgets, modifying `App` fields, or changing event handling.

### Tests

Test rules and decisions, not rendering. A test earns its place if it can fail
for a reason a person wouldn't spot immediately: parsing, ordering, caching,
conversion, the mention query, a layout that has to *react* (Relayout,
placeBeside, a card that grows). Do **not** write tests that assert a palette
constant is what the palette says, that a widget was built out of the objects it
was just built out of, or that a hand-tuned pixel offset is still that offset —
those only make the next visual change more expensive. To check appearance,
render to a PNG with `fyne.io/fyne/v2/driver/software`, look at it, and delete
the harness.

### Fyne footguns

- **Innermost object wins.** Fyne delivers hover and pointer events to the
  deepest object that accepts them. Do *not* implement `desktop.Hoverable` with
  no-op methods — an inner widget that accepts hover steals it from its parent row
  (why `ui.Avatar` isn't hoverable). Anything interactive inside a message row is
  passed `MessageWidget.TappedSecondary` at construction: the avatar, each
  attachment, the reply preview, the embed title.
  The body is the awkward one — a selectable `widget.Label` mounts an unexported
  selection overlay that answers right-clicks itself. `ui.bodyText` lays a
  `selectionCatcher` over it: right-clicks stop at the catcher, press/drag/tap are
  forwarded down. If a future Fyne stops exposing the overlay,
  `newSelectionCatcher` returns nil and the body is a plain selectable Label.
- **Embed cards are inert containers, not widgets**, so hover and right-click
  reach the message row underneath. Only the title (`embedLink`, a type of its own
  because `TappableContainer` is hoverable) and the picture are widgets.
  Wrapping text can't be asked how wide it wants to be — it answers with whatever
  it was last given — so `embedContentWidth` measures it as one unbroken line and
  caps it at `EmbedMaxWidth`.
- **One hairline draws every edge.** `theme.Colors.Outline` at
  `theme.Sizes.OutlineWidth` is the *only* border in the client: embed cards,
  attachments, the composer dock, the column seams. A card sized by its own
  padding wears it on its background (`ui.Outline(rect)`); a card whose content
  reaches its edge — a picture — needs it on a rectangle stacked *over* the
  content, which is what `HoverableStack`'s rectangle is for. Columns carry theirs
  as a `ui.NewColumnDivider` *inside* their own fixed width, because the main row
  addresses its children by position to find the one that stretches (and the
  member sidebar's sits on its left so it disappears with the column). The colour
  is chosen by an ordering: it must stay darker than every surface it is laid
  against, including `MessageHoverBackground`, since a row's hover fill paints
  *under* any card the row contains. Because the outline is drawn at rest, hover
  must *lift* it — `AttachmentHoverBorder` is lighter than what it replaces.
  Fyne's *ambient* shadow is off: `AppTheme` answers `ColorNameShadow` with
  `color.Transparent`, because a scroll paints it as a gradient along whichever
  edge has more content past it — a wide smear on a flat surface, not a line, and
  the one under the message list landed in the composer's margin.
- **One card is elevated.** `ui.Elevate` casts a `canvas.Shadow` (2.8 renders
  these per object, in both painters; the quad grows to fit, so it draws outside
  the rect) and only the composer dock carries one. `DropShadow` follows the
  corner radius and paints nothing under the fill, so a translucent shadow can't
  dirty the card. `CardShadowBlur` overruns `ComposerDockMargin` on purpose: what
  the shadow has to darken is the message passing *underneath*, and a halo that
  stopped inside the gutter would only outline the gutter. It is deliberately
  weak — strong enough to read as a fill of its own and the card becomes a bar
  again. At rest it swallows the hairline it sits on, which is what a contact
  shadow does; focus still lights the outline accent.
- **Content runs under the dock.** A margin and a shadow are not what make the
  composer float — the message column being *taller than the card* is. A column
  that stops above a card stops at a hard cut through whatever glyph the viewport
  landed on, and that cut, not the gap, is what reads as the top edge of a
  separate bar. `ui.NewFloatingDock` hangs the card over a full-height
  `messageScroll` so the cut lands behind it, a corner radius short of the card's
  bottom edge (the rounded corners would otherwise expose it in the two notches).
  Nothing shows beside the card because `MessageHorizontalPadding` is wider than
  `ComposerDockMargin` — that ordering is load-bearing. `ui.NewDockReserve` wraps
  the scroll's *content* (so `messageList.Objects` keeps its 1:1 indexing) and
  reports `ui.DockReserve` of extra height, which is what lets the newest message
  be scrolled clear of the card. It measures the card on demand rather than being
  told, so a reply preview, an attachment row or the mention picker growing the
  dock is accounted for without anything having to notice.
- **Sidebar widths.** The three side columns are pinned by
  `ui.NewFixedWidthContainer`, not by a minimum-size rectangle: a minimum is a
  *floor*, and `container.NewVScroll` reports its content's minimum width as its
  own, so one long name used to shove the message area sideways. Anything
  rendering a user-supplied name into a sidebar row goes in the stretching slot of
  a `Border` wrapped in `ui.NewEllipsisText` (or `Truncation = TextTruncateEllipsis`).
- **Composer geometry.** A growing entry's height is
  `lineHeight × lines + InnerPadding × 2` (`composerMinSize`) — the input border
  is *not* added on top, because `entryRenderer.Layout` pays for it out of the
  text provider's own padding. Counting it twice made both entries four pixels
  taller than their content. The dock hangs `ComposerDockMargin` in from three
  edges of the message area, which is why the area stacks through
  `ui.NewFillColumn`, not a `Border`: a Border would charge theme padding between
  its centre and the dock on top of that. The margin is only a gutter — widening
  it just grows the strip the card sits in.
- **Nested `Border`s hide padding.** `container.NewBorder` inserts theme padding
  between each edge slot and its centre, so a Border inside a Border inside a
  Border charged the message row three helpings. Reach for `NewFillRow` /
  `NewFillColumn` / `HBoxNoSpacing` / `NewInset` when the spacing has to be exact.
- **Mixed text sizes on one line** align by being siblings in an HBox — a
  `canvas.Text` centres its glyphs in whatever height it is given. Don't wrap one
  in a spacer to nudge it.
- **Message row rhythm.** The avatar is centred on the block a *single-line*
  message occupies, placed by an offset from the top rather than centred on the
  row, so a longer body grows away from it. That offset and the grouped gutter
  timestamp's are *derived* from `messageLineHeight()`, never hardcoded: a line is
  whatever Montserrat measures. Anything a row can additionally carry (a reply
  preview) must move the whole row, so it sits *inside* the row's margins.
  A message with no text hides its body slot entirely — an empty body still
  renders one text line tall, which draws as a gap above an embed or attachment.
- **A system message is not a message anybody wrote.** `NewMessageWidget` branches
  on `Message.System` before building anything: no avatar, no name, no body slot,
  no attachments, embeds or replies — one line of `canvas.Text` with the event's
  mark centred in the gutter an avatar would fill, and the time drawn beside it
  rather than revealed on hover (there is no name for it to follow, and "Someone
  left" with no *when* is half a sentence). Nothing in that line accepts a
  pointer: innermost wins, so a widget standing in it would be a hole in the row's
  own hover and context menu. It keeps `SystemMessagePadding` whatever surrounds
  it — `continuesGroup` refuses either side of one, so a run of joins is a block —
  which is why `verticalPad` is a *method*: `SetFollowedByGroup` reaches it too.
  Reply is offered nowhere on one (`canReply`), as edit already wasn't.
  `systemMark` answers with the glyph **and** the colour, because the two say
  different halves of one thing: the tone is the class of event — arrival,
  departure, removal, a change to the channel, a call — which is what a column of
  these is skimmed by, and the glyph is which event of that class it was. A kind
  the client doesn't know takes the generic mark in the neutral grey, so an event
  Revolt adds later still reads as an event.
- **Tinting one of the client's own marks is a substitution, not a theme name.**
  Fyne's `theme.NewColoredResource` rewrites an SVG's *fills* and leaves strokes
  alone, and every mark in `assets/` is an outline — so a stroked icon comes back
  white whatever colour name it is given. `ui.tintedIcon` replaces the source's
  one stroke colour instead, and puts the colour in the resource's **name**:
  Fyne caches the rasterised SVG under that name, so two resources sharing one
  would share a raster and the second colour asked for would come back as the
  first. That name is also the key of `ui.tintedIcons`, the memo that stops a
  channel of system events rewriting the same seven files a hundred times — a
  restyle changes the colour, hence the name, so it misses rather than handing
  back what the palette used to say. This is also why a message's own buttons and
  menu draw `action-*.svg` rather than Fyne's icons: a themed resource takes its
  colour from a theme *name*, and delete reading as delete is the point.
- **Repainting the message column.** `Container.Refresh` refreshes every child,
  and `RichText.Refresh` re-wraps its text — so `messageList.Refresh()` re-flowed
  every mounted body on every gateway message. Every mutation of the mounted
  window goes through `App.remountMessages` (`ui.Relayout`: re-run this one
  layout, don't walk the children). Use `Refresh` only when what a *mounted*
  widget says has changed. For the same reason nothing on the scroll path may call
  `MinSize` on the list — `BaseWidget.MinSize` is not memoised.
- **Fyne's scrollbar is a widget over the content, so the client draws its own.**
  The bar lives in a `scrollBarArea` laid across the right edge of the scroll's
  content, and that area accepts hover: innermost wins, so it took the hover the
  message row under it needed, and it swelled from `SizeNameScrollBarSmall * 2` to
  `SizeNameScrollBar` once the pointer reached it — over the text, not beside it.
  `AppTheme.Size` zeroes *both* (zeroing only the large one left an invisible
  six-pixel strip still eating hover). What replaces it is
  `ObservableScroll`'s own indicator: a `canvas.Rectangle` appended to Fyne's
  renderer objects (they are set once at construction and never replaced, so
  composing the slice once holds), which accepts nothing because it is not a
  widget. It is placed from `Content.Size()` — never `MinSize`, see above — and
  revealed from the *renderer's* `Refresh` by comparing the offset, since every
  offset change ends there whoever caused it while an unrelated repaint must not
  flash it. It lingers, then fades through a `fyne.Animation`, which the renderer's
  `Destroy` stops so a restyle's rebuild doesn't leave one ticking.
  `ScrollIndicatorWidth + ScrollIndicatorInset` must stay under
  `MessageHorizontalPadding` or the bar is drawn over the text; a width of zero
  turns it off. Only the message column has one — the sidebars, the settings pane
  and the profile dialog are bounded content whose right edges carry rows and
  controls a strip would obstruct.
- **Tooltips and notices are layers over the main row, not canvas overlays.**
  Pushing an overlay routes the whole hit test into it, so the hovered widget
  would never see `MouseOut`. Confirmations *are* canvas overlays, on the modal
  layer with the lightbox.
- **Fyne's form widgets do not survive `AppTheme`, and a scoped override only
  buys so much.** `AppTheme.Size` zeroes `SizeNameInputBorder`, which is what a
  `widget.Slider`'s track thickness is derived from (`trackWidth = inputBorder *
  2`), and its track is filled with `ColorNameInputBackground`, which `AppTheme`
  answers with the colour a settings group is a card of — so a slider drew as a
  bare thumb on an invisible track. A `container.NewThemeOverride` (the `WithCaret`
  shape) fixed both and still left the thumb swelling into a grey hover disc that
  covered the track it is read against, because that is Fyne's own drag affordance
  and no theme name reaches it. `ui.Slider` replaced it outright, and
  `settings_controls.go` is the general answer: a control here is canvas objects
  and a layout. Any Fyne widget mounted for the first time is worth rendering to a
  PNG before believing it.
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
  existing tag, pushes the tag and publishes. Pushing a `v*` tag by hand takes
  the tag verbatim — the escape hatch for off-calendar versions.

## Known gaps

- Reply-preview tap navigation is a TODO (`ui/message.go`, `buildReplyPreview`).
  `App.createServer` only reports that creation isn't built.
- Settings: the three typing toggles are recorded and honoured by nothing —
  typing itself is not implemented (revoltgo has `ChannelBeginTyping`/`EndTyping`
  and `EventChannelStartTyping`/`StopTyping` ready). The colour picker offers a
  grid of presets beside the hex field — there is no hue wheel, no alpha slider
  and no eyedropper, so a colour outside the presets is still typed. The image
  cache directory can be chosen now, but it and the message cache caps and the
  text-preview count are read once while the caches are built, so they take a
  restart, and each says so. The Account section can forget a saved login and log
  out; presence/status is not settable, and there is no "log out everywhere" —
  neither has a confirmed revoltgo action. The Advanced filter matches the field's
  own name only, since that is what its rows are labelled with; the curated groups
  in Styles are not searchable at all.
- Profiles show what the client already knows plus the bio and the banner.
  Mutual friends/servers, relationships, and acting on one are absent. Only the
  dialog's About section scrolls, past `ProfileBioMaxHeight`; the card previews a
  bio instead (`profileBioRunes`). Neither presentation refreshes while open. The
  banner is drawn flat: Revolt's own client fades it out under the card body, and
  a `canvas.Image` takes no gradient mask.
- A gradient role colour is spread across a *name* only (`ui.AccentText`).
  Everywhere else — a role chip's dot, a reply's accent bar, the mention picker's
  row — it fills as the mean of its stops. `parseColor` reads hex stops only, so a
  role written in `rgb()` or a CSS name still falls back to the default text
  colour, as it did before gradients were read at all.
- `markdown.CodeBlock.Language` is parsed but nothing highlights.
  `domain.FileKind` classifies video/audio/archive/PDF; only `FileImage` and
  `FileText` are branched on.
- `domain.Message` drops what nothing renders: reactions, flags, mentions, pins,
  masquerade contents (only *that* it carries one survives, for grouping).
- A system line names one person — `SystemMessage.Target`, resolved through
  `Store.UserName`, so it is the account's display name and never the server
  nickname — and says nothing about who did it: Revolt sends only the subject, so
  a kick reads "Marceline was kicked" with no one named as having kicked her. The
  name is not set apart from the sentence either; `Text` returns one string, and
  bolding the name would take a split the domain does not offer. A channel rename
  says only that it happened, not what to.
- Embeds render the site line, title, description, colour and one picture. A bare
  **video** embed is dropped at the boundary (Revolt puts a video's dimensions
  beside the type, so revoltgo carries only the URL — and there is no player).
  `MessageEmbedSpecial` (YouTube, Spotify, …) is not read at all. A bare **image**
  embed has the same dimensions problem, so it draws against the placeholder box
  and settles once the picture lands. Preview/large image *size* is not read.
  Sending an embed isn't offered — the composer can't compose a card.
- Slowmode is enforced from the client's own clock. The server's `InSlowmode`
  rejection carries the authoritative `retry_after`, but revoltgo surfaces a
  failed request as a formatted string, so it is not read back — a send refused
  because the cooldown was started elsewhere reports the generic failure notice.
  Nothing shows a cooldown outside the open channel: a channel row gives no hint
  that opening it lands on a wait.
- The composer has no attach or emoji button (files arrive by drag or paste) and
  no role/channel mentions. `EditEntry` has no mention picker. A composed mention
  stays a visible `<@id>` until sent: Fyne cannot draw a chip inside an entry, and
  mapping names back to IDs at send time breaks on duplicates.
  `markdown.PlainText` renders a mention as a bare `@` (it has no session), so a
  reply preview of a message opening with one starts with a lone `@`.
- The scroll indicator only reports where the view is. It cannot be dragged and
  has no track to click, so the wheel, the middle-button pan and the keyboard are
  what move the column; nothing outside the message area has one at all.
- Sidebar context menus stay small: an ID to copy, "Mark as read", and one
  destructive item below a separator (leave server — never for its owner, since
  the same endpoint would delete it; close conversation; remove member). Banning,
  role edits, nickname changes and channel deletion are one call away but not
  offered — moderation the client can't undo shouldn't be a menu item away.
  Only servers show a hover tooltip.
- Notices are transient and unlogged from the user's side — no history panel. The
  login screen has no notice layer (it isn't built until Ready), so `session.go`
  reports a dead token through `dialog.ShowError`. A notice carries a heading, but
  most callers still take their tone's rather than writing one: `App.notify` is the
  short form and `App.notifyNotice` the one that says something. Hovering does not
  pause the countdown, and nothing stacks two identical notices into a count.
- No `ChannelCreate` handler, so a DM opened while running only appears at the
  next DM-list refresh (its messages are still cached, its unread mark recorded).
- `assets/` still carries unreferenced `close.svg`, `edit.svg`, `file.svg`,
  `reply.svg`, `trash.svg` — four of them now duplicated in the house style by an
  `action-*.svg`, in a mix of fills and strokes that `ui.tintedIcon` could not
  colour anyway. Nothing embeds them.
- Markdown: strike/underline/spoiler share one `decoratedSegment` (Fyne renders
  neither strike nor underline natively), split per word since RichText only
  breaks rows at text spaces. The blockquote bar isn't drawn on wrapped
  continuation lines; a decoration can show a one-space nub at a wrap; inline
  `code` inside a decorated span isn't itself decorated.
- Text selection: only uniform-style bodies flatten to a `Selectable` Label. Fyne
  2.8 has no public RichText selection, so mixed-style bodies — including any body
  carrying a mention, whose colour is a second style — stay unselectable;
  right-click → Copy message covers them. Selecting *across* two messages isn't
  possible: each body is its own Label.
- `client.Client` has no test of its own — its actions want an HTTP fake, and
  revoltgo's REST layer takes no injectable transport.

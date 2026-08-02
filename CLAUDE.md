# rgoclient

A Fyne v2.8.0 desktop chat client (Discord-like) for Revolt, in Go 1.26.4. Uses
`github.com/sentinelb51/revoltgo` for the REST API and gateway websocket.

## Core principle: explicit dependencies, no globals

There is no global session or cache. The `*app.App` controller owns the session
and all three caches, and passes what widgets need through a `ui.Deps` value:

```go
type Deps struct {
    Session *revoltgo.Session // resolves users, members, and system messages
    Images  *cache.ImageCache // avatars, icons, attachments
    Texts   *cache.TextCache  // text-attachment previews
    Actions MessageActions    // user-interaction callbacks (implemented by *app.App)
}
```

`App.deps()` is the only producer, so **every field is always set** — widgets do
not nil-check `Actions`, and tests populate it with a stub rather than relying on
nil tolerance. Widget constructors take `Deps` (e.g. `ui.NewMessageWidget(deps,
msg, ...)`). `util` helpers take an explicit `*revoltgo.Session` argument. Do not
reintroduce package-level singletons.

The only package-level mutable state left is pure measurement memoisation
(`ui.lineHeights`, `ui.spaceWidths`) — caches of a pure function, UI-thread only.

## Code style

Follow revoltgo's conventions; they are the house style.

- **Naming.** Types are `DomainRoot + Modifier`, flat (`ServerWidget`,
  `MessageCache`). Constructors always use the verb *new* — `newX` unexported,
  `NewX` exported; never `create`/`make`/`build` for a constructor (`buildX` is
  reserved for assembling a UI subtree, which is not a constructor). Receivers are
  a single letter tied to the type initial (`a *App`, `w *MessageWidget`,
  `c *ImageCache`, `l *LRU`). Acronyms stay full-caps as a unit (`ID`, `URL`).
- **Structs.** Field order runs identity → descriptive data → collections →
  flags. Sections are separated by blank lines and `/* Label */` block comments.
  A mutex sits adjacent to what it guards, with a trailing comment naming it.
- **Functions.** Blank line after the signature before the first guard clause;
  guard clauses and early returns over nesting; a blank line fencing the final
  `return`. `defer Unlock()` by default.
- **Comments.** Doc comments restate the identifier name first and stay short.
  Struct fields get trailing comments only for semantic or unit clarification;
  statement-level explanation goes on its own line above. Prefer `/* Label */`
  dividers over splitting a file. **Comment only what the code cannot say** — the
  non-obvious Fyne/revoltgo constraint, the reason an invariant holds. Do not
  narrate mechanics.
- **Files.** Prefer fewer, larger files with visual sectioning over fragmentation.

## revoltgo: Session vs State

- `Session.X(...)` — authoritative, always a network request. Use when data must
  be fresh or on a cache miss.
- `Session.State.X(...)` — local cache from gateway events / prior calls. Fast,
  zero-network, may return nil (always nil-check).

Attachments/avatars/icons are all `*revoltgo.File` (the type was renamed from
`Attachment`). Its `Metadata` is a *pointer* and is nil for files the server
couldn't introspect — go through `util.IsImageAttachment` /
`util.AttachmentDimensions` rather than dereferencing it. Uploads take
`*revoltgo.FileParams`, not `File`.

**Known revoltgo bug.** `Session.ChannelMessages(..., IncludeUsers: true)` returns
the page's `Users` and `Members`, but only feeds them into State when the request
*failed* (`if err != nil` where `err == nil` was meant). So a freshly opened
channel gets messages whose authors State has never heard of, and the client has
to resolve them itself — that is what the batched `ensureAuthor` path exists for.
Once the library is fixed, State will already hold every author of a fetched page
and the batch will simply find nothing to do; nothing here needs changing.

## Project structure

```
cmd/rgoclient/main.go        Entry point: app metadata (unique ID + the fyneDo
                             migration flag), theme, app.New(...).Run().
                             `version`/`build` are link-time vars stamped by CI

assets/                      Embedded binaries (go:embed can't reach above its own
                             source file, so this sits at the repo root)
  fonts.go                   Montserrat static cuts
  icons.go                   MentionIcon, MembersIcon + AppIcon. Everything else
                             the UI draws comes from Fyne's theme icon set; nothing
                             is read from disk at runtime, so the binary runs from
                             any cwd

internal/
  app/                       Controller; owns session + caches + window + UI refs
    app.go                   App struct, New, Run, lifecycle, doOnUI (the single
                             UI-thread entry point), deps, styleNativeChrome,
                             state accessors, the ui.MessageActions impl, and the
                             one active in-place edit (startEditing/cancelActiveEdit)
    session.go               Opening a session (startWithToken/startWithLogin/
                             openSession/resetSessionState), the login screen, and
                             the saved-session JSON store (~/.rgoclient_sessions.json)
    events.go                Every gateway handler, registered in openSession:
                             Ready + error lifecycle, message create/update/delete,
                             the coalesced read-ack path (scheduleAck/sendAck — the
                             only one; selectChannel routes through it too), and
                             server/channel/member events — including the
                             ServerDelete and ChannelDelete departures, which are
                             what actually take a left server or closed
                             conversation out of the sidebar
    notify.go                The notification system's controller half: notify
                             (post a transient message) and confirm (ask before
                             something irreversible), plus the destructive actions
                             that use them — leave server, close conversation,
                             remove member — each paired with the check deciding
                             whether to offer it at all (canLeaveServer /
                             isConversation / canKickMember)
    navigation.go            buildUI (the 4-column fill row, under the notice and
                             tooltip layers) + the server and channel sidebars, the
                             settings window, selection, the sidebar context menus
                             (serverMenu / channelMenu / memberMenu +
                             markServerRead /
                             markChannelRead), and the home view (selectHome /
                             loadDirectMessages / sortConversations /
                             resolveRecipients). The server sidebar bookends its
                             scrolling icons with fixed home and settings buttons;
                             the join-server "+" sits at the end of the scrolling
                             icons. refreshChannelList renders either a server's
                             categorised channels or, in the home view, the flat
                             DM list
    messages.go              The message area end to end: build (the composer dock —
                             see "Composer geometry"), compose/submit, widget
                             construction (continuesGroup/dayLabel), setChannelGlyph
                             (the header's # / @ / group mark, sitting opposite the
                             member-sidebar toggle at the header's right edge),
                             load and render, and the mounted window — which slice
                             of the cache has live widgets and how it slides. Its
                             "mounted window" section states the invariants
    members.go               Lazy author resolution (ensureAuthor → flushAuthors →
                             resolveAuthor, queued then fetched in one bounded
                             batch; refreshAuthorMessages updates widgets in place),
                             the member sidebar (toggleMemberList hides and shows
                             the whole column), and the mention candidates the
                             composer's picker offers — the same State walk, so
                             they refresh together
    overlay.go               The modal layer: showOverlay (centred) / showPopover
                             (anchored) / closeOverlay / repositionOverlay, the
                             attachment lightbox, and the join-server dialog +
                             joinServer/createServer
    profile.go               User profiles: OnUserTapped (the compact card, beside
                             whatever was clicked) and showProfileDialog (the full
                             one, centred), profileOf — the one State walk both are
                             drawn from — loadBio, and openConversation /
                             showConversation, the "Message" button's path into the
                             home view
  cache/
    cache.go                 Package doc, LRU (the shared O(1) recency tracker
                             behind every bounded cache), and TextCache
                             (LRU-bounded text-attachment previews)
    message.go               MessageCache — per-channel, oldest→newest, capped per
                             channel, LRU channel eviction; Find/Remove/Replace =
                             binary search by ID (ULIDs sort chronologically;
                             CompareMessageID is shared with app); both entries
                             *and* published slices are immutable, so a UI-thread
                             reader holding an earlier slice is safe
    image.go                 ImageCache — LRU-bounded memory + disk + async load;
                             trimDiskCache evicts oldest-first at startup
  ui/
    ui.go                    Package doc, Deps + MessageActions, DoOnUI, the one
                             icon fill/scale policy (newScaledIcon), context menus,
                             and WithCaret (per-entry theme override restoring the
                             caret AppTheme's zero InputBorder collapses — wrap
                             every mounted entry in it)
    layouts.go               Spacers, fitWithin, Relayout (re-runs one container's
                             layout after a child was hidden, without walking every
                             descendant the way Refresh would), and the custom
                             layouts: noSpacingLayout (VBox/HBox/NewFillRow — one
                             layout, optional filling child, skipping hidden
                             children), columnLayout (the message
                             avatar gutter; `collapse` makes it report zero height
                             for the grouped timestamp), overlayLayout,
                             minSizeLayout (NewMinWidth/MinHeight/FixedWidth —
                             `pinWidth` turns the floor into a ceiling, see
                             "Sidebar widths"), insetLayout (NewInset: exact
                             per-edge padding, and with negative insets the way a
                             RichText's own inner padding is stripped, or a profile
                             avatar is raised over its banner), flowLayout (NewFlow:
                             chips wrapped into rows at a width it is *given*, see
                             its doc) and popoverLayout (placeBeside: a card put
                             next to an anchor and kept on screen)
    widgets.go               Shared interactive widgets on tapBase
                             (TappableContainer — which carries the same Menu hook
                             the sidebar rows do, for rows that are a container
                             rather than a widget of their own — HoverableStack,
                             IconButton — which
                             draws no plate, the icon itself dims and brightens —
                             SidebarButton, which carries a selected state so the
                             home button lights like a server icon, CloseButton,
                             roundedPanel, separator), Tooltip (the hover label,
                             mounted as its own layer — see "Tooltips"), the one
                             avatar loader (circularAvatar/Avatar/avatarCacheID),
                             ObservableScroll (wheel amplify + middle-button pan),
                             and NewEllipsisText/TruncateToWidth (text that shortens
                             to the width it is given, at zero minimum width)
    sidebar.go               ServerWidget, ChannelWidget (named through
                             util.ChannelName, so a DM row shows the other
                             participant) + collapsible category, the drawn glyph set
                             ChannelGlyph picks from (HashtagIcon / AtIcon /
                             GroupIcon), member row/section, and the saved-session
                             card. What leads a channel row is its type
                             (channelLeading): a server channel gets the glyph, a
                             conversation (isConversation — DM, group, saved notes)
                             gets a taller card led by util.ChannelAvatarURL's
                             picture, blank when there is none. All three rows carry
                             a Menu hook the controller fills with right-click items
                             (the member row's is on the TappableContainer it is
                             built from);
                             ServerWidget's OnHover drives its name tooltip
    message.go               MessageWidget: construction, permissions, quick
                             actions / context menu, in-place edit mode, hover,
                             content assembly — plus the day separator it owns
                             (not a list entry of its own), reply previews — each
                             led by its own square-cornered elbow (newReplyLine /
                             replyLineLayout), which is also what indents the quote
                             to the body's column — and EditEntry.
                             Its "Vertical alignment" section is where
                             the row's rhythm is decided: messageLineHeight and the
                             two offsets derived from it (avatarTopOffset,
                             gutterTimestampTopOffset) — see "Message row rhythm"
    markdown.go              AST → RichText rendering; strike/underline/spoiler
                             custom segments; uniform-style bodies flatten to a
                             Selectable Label (bodyText, mouse text selection); only
                             mixed-style bodies keep the unselectable RichText.
                             mdBuilder carries Deps because <@id> is only an ID in
                             the AST — mention() resolves it and draws "@Name" in
                             theme.ColorNameMention. bodyText + selectionCatcher are
                             what keep a right-click on selectable text reaching the
                             message — see "Right-clicking a message"
    attachment.go            Attachment rendering (image / text preview / generic
                             card), the name/size bar, and fetchText (shared,
                             byte-capped download). Images and text files are
                             tappable → Actions.OnAttachmentTapped; every attachment
                             takes the owning message's context menu
    input.go                 Message input + attachments + reply cards; OnEditLast
                             fires on Up in an empty composer, OnFocusChanged drives
                             the dock's focus ring, composerMinSize is shared with
                             EditEntry. Plus the @mention half: the trigger
                             (mentionQuery / syncMentions / acceptMention and the
                             cursorIndex ↔ cursorPosition pair converting Fyne's
                             row/column caret to a rune index and back) and the
                             MentionPicker it drives — pooled rows re-set per
                             keystroke, prefix-before-substring ranking, and a
                             "+N more" footer instead of a scrollbar
    modal.go                 Overlay (the full-canvas modal layer — centred and
                             dimmed, or anchored and clear through NewPopover;
                             Reposition re-places content that grew) + tapSink,
                             NewAttachmentViewer (the lightbox card), and
                             JoinServerDialog (validates through util.InviteCode;
                             its entry handles Esc itself, since a focused entry
                             never reaches the canvas handler)
    profile.go               The user profile, in two presentations off one Profile
                             value: NewProfileCard (the compact popover) and
                             NewProfileDialog (the full modal), sharing the banner /
                             overhanging avatar / section / chip helpers. Presence +
                             PresenceOf (the vocabulary the avatar's ring is drawn
                             from — presenceRing, absent entirely when offline),
                             identity (the display name with the account's real
                             handle beside it, the name shortening first) and
                             SetBio, the one field that arrives after the card is up
    notice.go                The notification system: Tone (the one vocabulary —
                             info / warning / danger — deciding colour, icon and
                             button weight), NoticeStack (transient cards on their
                             own layer, capped and self-dismissing) and
                             NewConfirmDialog (the modal question, shown on the
                             same layer as the lightbox). See "Warnings and
                             failures"
    theme/theme.go           Colors, Sizes, AppTheme (palette + scrollbar/widget
                             overrides + ColorNameMention, the app-specific colour
                             name a RichText segment can carry; ColorNameError /
                             ColorNameWarning are mapped because Fyne's Danger and
                             Warning button importances read a tone's fill off
                             them)
    titlebar_windows.go      DWM recolouring of the native title bar (no-op
    titlebar_notwindows.go   elsewhere, see App.styleNativeChrome's retry loop)
  markdown/                  Pure (no UI) Discord/Revolt markdown parser → AST
    markdown.go              Document/Block/Inline AST node types
    parser.go                Block parsing, inline parsing (bold/italic/strike/
                             spoiler/code/link/<@mention>), PlainText, and
                             DocumentText (a whole document as one line, for a
                             profile's bio preview).
                             Discord-style emphasis guards: _ opens/closes only at
                             word boundaries (snake_case stays literal) and single
                             */_ content can't be whitespace-edged
  util/
    state.go                 Everything resolved out of Session.State: MessageAuthor
                             (one-pass author resolution — name, avatar URL, role
                             colour, member-aware), MemberName/MemberAvatarURL/
                             MemberOnline/MemberColor/MemberRoles (Role, ordered
                             by seniority), UserName/UserHandle/UserBadges,
                             ChannelName + ChannelAvatarURL + DMRecipientID, and
                             FormatSystemMessage
    file.go                  Filetype + FormatFileSize, IsImageAttachment /
                             AttachmentDimensions (nil-Metadata safe), and
                             IDFromAttachmentURL
    text.go                  Truncate (rune-safe) + InviteCode (bare code /
                             invite link / scheme-less link → code, "" when it
                             isn't shaped like one)
    timestamp.go             ULID Timestamp + ShortTime + NiceTime + FullDate +
                             SameDay + DayLabel (Today / Yesterday / date)
```

## Data flow

1. Login (`session.go`) → `startWithToken` / `startWithLogin` → `openSession`
   registers handlers and opens the gateway. `startWithLogin` stashes the new
   token in `pendingToken` *before* opening (Ready can beat the login goroutine
   back to the UI thread). The login screen stays up until Ready; only failures
   return to it.
2. `onReady` → save pending token, record unreads, `showMainUI` (the only place
   the main layout is built), `refreshServerList`, `selectServer(first)` — or
   `selectHome` when the account is in no servers, so the client never lands blank.
3. `selectServer` → `refreshChannelList`, `refreshMemberList` (paints whatever
   members State currently holds) → `selectChannel(first)`. There is no bulk
   member fetch: Revolt's members endpoint has no pagination, so large servers
   would flood memory. Members are resolved lazily per author (`ensureAuthor`).
4. `selectChannel` → show cached messages, else `loadChannelMessages` (asks for
   `IncludeUsers: true`, see the revoltgo note above); ack unread.
   `displayMessages` is synchronous and mounts only the newest `initialMountCount`
   messages (~2-3 screenfuls; mounting more is churn the renderer cache holds for
   up to a minute, which is what made rapid channel switching ratchet memory).
   Callers render from the *cache* (`displayCached`), never from a page captured
   off-thread, so a message that arrives between the fetch and the render is
   included rather than lost.
   Scrolling to the top (`loadMoreHistory`) is two-tier: cached-but-unmounted
   messages prepend synchronously, then older pages come from the network — both
   anchored on the oldest *mounted* message. The mounted window is bounded at
   `mountedCap`: prepends past it trim widgets off the bottom (only while the new
   bottom is still cached — past the cache cap the window grows instead, since the
   cache ends at the live tail), and scrolling back down re-mounts them from cache
   (`mountNewerFromCache`, never network). Every trim `clear()`s the vacated slots
   so unmounted widgets are actually released rather than pinned by the list's
   backing array. Sending jumps to the newest message (`jumpToLatest`),
   re-rendering if scrollback trimmed the tail away.
   Message author name/avatar resolve from the server member (nickname, per-server
   avatar, role colour), falling back to the raw user. Consecutive messages from
   the same author within `messageGroupWindow` render grouped (`continuesGroup`):
   no repeated avatar/name, just a hover-revealed gutter timestamp. All widget
   construction goes through `App.newMessageWidget(prev, curr, next)`;
   `displayMessages`, `appendMessage`, and `prependMessages` each supply the right
   predecessor (prepend also re-evaluates the old top row against the newly
   prepended message above it). Being the single funnel, `newMessageWidget` is also
   where an unresolved author is queued for resolution (`ensureAuthor`), so every
   mount path — first page, gateway, scrollback — is covered by one call.
   The same predecessor decides the day separator (`dayLabel`): a message on a
   different local calendar day than the one above it — or with no predecessor
   mounted — heads its widget with a dated hairline, and a day change always
   breaks the author group. The separator belongs to the widget rather than
   being its own list entry, so the mounted window stays one object per message
   and every seam rebuild (prepend, delete, edit) re-derives it for free.
5. Author resolution: a message carries only its author's ID. `ensureAuthor`
   checks State for the user and (in a server) the member, and queues whatever is
   missing — it does not fetch. `authorTimer` fires `authorFetchDelay` later and
   `flushAuthors` resolves the whole batch through `authorFetchWorkers` goroutines,
   refreshing each author's mounted widgets as they land (`refreshAuthorMessages`,
   in place) and the member sidebar once at the end. Batching matters because
   mounting a page calls `ensureAuthor` per widget: unbatched, opening a busy
   channel would fan out dozens of requests and rebuild the sidebar after each one.
   `fetchedAuthors` guards each (server, user) against being queued twice, and is
   released on failure so a later message retries.
6. `onMessage` → cache append (which returns the predecessor under the cache lock,
   so grouping survives bursts) → append to open channel + coalesced read
   ack (`scheduleAck`), else mark unread. If scrollback has detached the view from
   the live tail, the append is skipped — the message mounts on the way back down.
   `onMessageUpdate` applies the edit to a *copy* that replaces the cache entry
   (entries are read by the UI without the cache lock, so they stay immutable) and
   rebuilds the mounted widget; `onMessageDelete` / `onBulkMessageDelete` remove
   from cache and unmount, re-evaluating neighbour grouping at the seam.
7. In-place editing: `OnEdit` (hover/context-menu, or Up in an empty composer via
   `editLastOwnMessage`, which scans the cache newest→oldest for the user's own
   message — the cache only gains own messages through the gateway echo, so the
   scan can't race the send path) calls `startEditing`: one edit at a time
   (`App.editing`), the widget swaps its body for an `EditEntry` with floating
   save/cancel buttons. Save applies the edit to the cache optimistically and
   sends `ChannelMessageEdit` (failure reverts; the gateway MessageUpdate echo
   reconciles); Esc/cancel restores the body. Any message-area rebuild
   (`displayMessages`/`clearMessages`/`showStatus`) cancels the active edit, and
   `refreshMessage` leaves a message being edited alone so a remote update can't
   discard the open editor.
8. Mentions. Typing `@` at the start of a message or after a space opens the
   composer's picker (`MessageInput.syncMentions`, driven from the typing methods
   rather than `Entry.OnChanged` because the picker also has to close when the
   caret merely *moves* out of a mention). While open it gets first refusal on
   Up/Down/Enter/Tab/Esc, which the composer otherwise binds to sending and to
   editing the last message. Accepting rewrites the `@query` span as Revolt's
   `<@id>` wire token — the composer shows the raw token, since a Fyne entry
   cannot draw a chip inside its text — and `ui/markdown.go` renders that token
   back as an accent-coloured `@Name` in message bodies.
   The candidate list is *pushed*, not pulled: `App.refreshMentionCandidates`
   resolves it from State (on `selectChannel`, on every `refreshMemberList`, and
   after each author batch lands) and the picker filters that snapshot per
   keystroke. So the expensive part — walking State and resolving names — happens
   once per membership change, and a keystroke is two string comparisons per
   candidate with nothing allocated. Reading State only, a server's candidates are
   whoever the client already knows: the gateway's members plus everyone lazy
   author resolution has pulled in, the same bounded set the member sidebar shows
   and the same reason there is no bulk member fetch. The picker is mounted
   *inside* the composer card rather than floating over the message area: a Fyne
   pop-up takes canvas focus, which would pull it off the entry and stop the
   typing that drives it.
9. `onServerMember{Join,Leave,Update}` → State auto-updates (revoltgo default
   handlers); the app handler refreshes the member sidebar when it's the open
   server. `Update` also calls `refreshAuthorMessages` so that author's mounted
   messages pick up the new nickname / role colour / avatar in place.
10. The home view: the fixed home button swaps the channel sidebar from a server's
    channels to the user's direct messages and groups. `App.homeSelected` marks it
    open — home has no server, so an empty `currentServerID` alone can't be told
    apart from nothing selected — and `selectServer` clears it. The list comes from
    `Session.DirectMessages()`, which feeds the channels into State on the way
    through, so the app keeps only the sidebar *order* (`App.dmChannels`, a slice
    of IDs) and looks every channel up through `App.stateChannel` like any other.
    It is still a plain fetch with no gateway event maintaining it, which is why
    the order is re-asked for rather than pushed: `selectHome` paints the cache
    immediately and refreshes in the background, so re-opening home never blanks
    the sidebar. Each refresh re-sorts by `LastMessageID` (a ULID, so string
    comparison sorts chronologically) and resolves any recipients missing from
    State, since a DM has no name of its own — the same resolution the row's
    avatar needs, conversations being drawn as taller cards led by the other
    participant's picture instead of a glyph. Ordering is a snapshot — an incoming
    message marks its row unread rather than re-sorting the list under the reader.
    Everything downstream of the sidebar is channel-keyed and needs no special
    case; the member sidebar simply stays empty, as a DM has no server members.
11. Joining a server: the "+" at the end of the server sidebar opens
    `showJoinServer`, an overlay on the same modal layer as the attachment viewer.
    The dialog resolves what was pasted through `util.InviteCode` (bare code,
    invite link, or link without a scheme) and hands `joinServer` a code, which
    calls `Session.InviteJoin` off-thread. The response is not what adds the
    server — the join payload carries the server as an object, which revoltgo
    decodes into an `Invite` whose `ServerID` is never populated — so the sidebar
    is updated by the `ServerCreate` gateway event instead (`onServerCreate`).
    `App.pendingJoin`, set for the duration of the request, is what tells that
    handler to *select* the server it adds; servers appearing for any other
    reason are added without moving the view. A failed join leaves the dialog up
    with a short message on its status line and the real error in the log.
12. Notices and confirmations (`ui/notice.go` + `app/notify.go`). Anything
    irreversible asks first and anything that fails says so, through one pair:
    `App.confirm(ui.Confirm{...})` puts a question on the modal layer, and
    `App.notify(tone, format, ...)` posts a transient card on the notice layer.
    Both take a `ui.Tone` — info, warning, danger — and that is the *only* thing
    deciding colour, icon and button weight, so a caller says what it means and
    never how it should look.
    The destructive actions all have the same shape: a `can…`/`is…` check decides
    whether the menu offers it at all, `confirm…` asks, and the action fires
    off-thread and lets the *gateway event* update the UI — `ServerDelete` for
    leaving a server, `ChannelDelete` for closing a conversation,
    `ServerMemberLeave` for removing someone. Nothing is removed optimistically,
    so a rejected request leaves the client exactly as it was and the failure
    arrives as a notice. Deleting a message goes through the same confirmation:
    the quick actions put it one click from the pointer, and the card quotes the
    message so a misaimed delete shows itself first.
13. Profiles (`ui/profile.go` + `app/profile.go`). Clicking a message avatar or a
    member row calls `Actions.OnUserTapped(userID, anchor)` — the anchor being the
    widget that was clicked — and the controller opens the compact card *beside*
    it on the modal layer (`showPopover`, an `ui.Overlay` with a clear backdrop
    and `placeBeside` doing the placement). Its "Full profile" button swaps it for
    the dialog, centred and dimmed like every other modal.
    Both presentations are drawn from one `ui.Profile` that `profileOf` resolves
    out of State in a single pass: the user gives the handle, avatar, presence,
    badges and creation date (from the ULID), and the *open server's* member record
    overrides the name, avatar and colour with the nickname, per-server avatar and
    role colour, and adds the roles and join date. A user State has never heard of
    still gets a card, with their resolution queued through `ensureAuthor`, because
    a click that does nothing is worse than a card that is thin.
    The bio is the one thing the client doesn't already hold: `Session.UserProfile`
    is fetched *after* the card is up and filled in through `ProfileCard.SetBio`,
    which grows the card — hence `repositionOverlay`, since neither placement
    re-runs on its own. A bio that fails to load is only logged; a profile reads
    perfectly well without one.
    "Message" goes through `openConversation` → `Session.DirectMessageCreate`,
    which unlike `DirectMessages` does *not* feed its channel into State, so the
    channel is asked for once before `showConversation` puts it at the top of the
    home view and selects it.

## Conventions

- Pass dependencies via `ui.Deps`; never reach for global state. `Deps` is always
  fully populated, so don't add nil checks for its fields.
- Background goroutines update the UI through `App.doOnUI(fn, wait)` (controller)
  or `ui.DoOnUI(fn)` (widgets). `internal/cache` dispatches to the driver directly
  because `internal/ui` imports it. `main.go` declares the `fyneDo` migration, so
  Fyne no longer relocates stray off-thread calls onto the UI thread for us — a
  widget touched from a goroutine is now a real data race, not a logged warning.
- `App.session` is written only on the UI thread (`openSession`, `onError`
  teardown). Worker goroutines capture `session := a.session` before launching
  and use the local — at worst they call into a closed session, which errors.
- Widget receiver is `w`; app receiver is `a`; cache receiver is `c`.
- Interface assertions live near the type: `var _ fyne.Tappable = (*T)(nil)`.
  Do *not* implement `desktop.Hoverable` with no-op methods: Fyne delivers hover
  to the innermost hoverable object, so an inner widget that accepts hover steals
  it from its parent row (this is why `ui.Avatar` deliberately isn't hoverable).
- **Right-clicking a message.** The same innermost-object rule decides every
  pointer event, so anything interactive inside a message row swallows the
  right-click unless it hands it back: the avatar, each attachment, and the reply
  preview are all passed `MessageWidget.TappedSecondary` at construction. The
  body is the awkward one. A selectable `widget.Label` mounts an unexported
  selection overlay above its text which answers right-clicks with its own
  one-item "Copy" menu, having first pulled keyboard focus off the composer —
  neither behaviour is configurable. `ui.bodyText` therefore wraps the Label and
  lays a `selectionCatcher` over that overlay: right-clicks stop at the catcher
  and go to the message, while press/drag/tap are forwarded down to the overlay
  through the exported interfaces it satisfies (the Label's own renderer is what
  hands it over). If a future Fyne stops exposing it, `newSelectionCatcher`
  returns nil and the body is a plain selectable Label again — selection keeps
  working, the right-click reverts. `TestSelectableBodyCatchesRightClick` guards
  both halves.
- **Tooltips.** `ui.Tooltip` is a layer stacked over the whole main row, not a
  Fyne pop-up: pushing an overlay routes the entire hit test into it, so the
  widget being hovered would never see `MouseOut` and the tooltip would never come
  down. Being its own layer is also what lets a server's name overhang the 60px
  column its icon sits in. Nothing in the layer is tappable or hoverable, so it
  never takes an event from the widgets underneath.
- **Warnings and failures.** Don't invent a way to tell the user something: an
  irreversible action asks through `App.confirm`, an outcome they didn't ask
  about arrives through `App.notify`, and a dialog with a status line of its own
  (the invite dialog) keeps using it rather than posting a notice over itself.
  `notify` logs as well as posts, so its text is what a *user* needs — the API
  error goes to the log at the call site. `ui.NoticeStack` is a layer over the
  main row for the same reason `ui.Tooltip` is: a canvas overlay would take the
  whole hit test, and a message nobody has to answer must not block the client.
  A confirmation is the opposite and *is* a canvas overlay, on the same modal
  layer as the attachment lightbox. New tones are a `ui.Tone` and its palette
  entry, never a colour at the call site.
- Any custom widget overriding `Dragged` must also have `DragEnd`, or the driver
  never recognises it as draggable.
- Colors and sizes come from `ui/theme`, never hardcoded. Don't express one size
  as an offset from an unrelated one — add a named entry.
- **Sidebar widths.** The three side columns are pinned by
  `ui.NewFixedWidthContainer`, not by a minimum-size background rectangle: only
  the message area (`NewFillRow`'s fill index) may change width, and only when the
  window does. A minimum size is a *floor*, and `container.NewVScroll` reports its
  content's minimum width as its own, so one long channel or member name used to
  widen its column and shove the message area sideways. Anything rendering a
  user-supplied name into a sidebar row therefore goes in the stretching slot of a
  `Border` wrapped in `ui.NewEllipsisText` (or, for a `widget.Label`, with
  `Truncation = fyne.TextTruncateEllipsis`) so it shortens instead of pushing out.
- **Composer geometry.** A growing entry's height is
  `lineHeight × lines + InnerPadding × 2` (`composerMinSize`, shared by the
  composer and `EditEntry`) — the input border is *not* added on top.
  `entryRenderer.Layout` pays for the border out of the text provider's own
  padding (it sets `textProvider().inset` to the border size), so the border is
  already inside `InnerPadding`. Counting it twice made both entries four pixels
  taller than their content, and since the entry top-aligns its text inside its
  scroller, all four landed as dead space under the caret.
  `TestComposerTextIsVerticallyCentred` guards it. Nothing around the dock may add
  padding it wasn't asked for either: the card uses `ui.NewInset`, because
  `container.NewPadded` applies theme padding and `container.NewBorder` inserts
  theme padding between its edges and its centre.
- **Mixed text sizes on one line.** A `canvas.Text` centres its glyphs inside
  whatever height it is given, and an HBox stretches every child to the row's
  height — so two texts of different sizes on the same line align by being
  siblings, with nothing to compute. Don't wrap one in a spacer to nudge it: that
  is what left the message timestamp sitting low against the author name
  (`TestHeaderTimestampCentredWithName`).
- **Message row rhythm.** The avatar is centred on the block a *single-line*
  message occupies — the author line plus one line of body — so its centre falls
  on the seam between the two, and it is placed by an offset from the top rather
  than centred on the row. A longer body, an attachment or a reply then grows away
  from it and the avatar stays where a one-line message puts it. Both that offset
  and the grouped continuation's gutter-timestamp offset are *derived* from
  `messageLineHeight()`, never written down as a constant: the avatar is a fixed
  40px but a line is whatever Montserrat measures, so a hardcoded nudge is right
  only for the font and text size it was eyeballed against. Anything a row can
  additionally carry must move the whole row and never the avatar within it —
  which is why a reply preview sits *inside* the row's margins (above the message,
  indented to the body's column) instead of being poked in above them.
  `TestAvatarCentredOnFirstLine`, `TestGutterTimestampCentredOnItsLine` and
  `TestReplyPreviewLeavesTheRowInPlace` guard the three.
- **Nested `Border`s hide padding.** `container.NewBorder` inserts theme padding
  between each edge slot and its centre, so a Border inside a Border inside a
  Border charged the message row three helpings of it: the gap after the avatar
  gutter read as `MessageContentPadding` (4) in the source and drew as 12. The row
  is one `NewFillRow` for that reason, and the theme value now says what it draws.
  Reach for `NewFillRow` / `HBoxNoSpacing` / `NewInset` when the spacing has to be
  exact, and only for a Border when the padding is wanted.
- Use the `log` package for diagnostics.
- Keep this file current when adding files/packages, changing data flow, adding
  widgets, modifying `App` fields, or changing event handling.

## Build / check

`go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l internal cmd assets`
(expect no output).

Note this repo is checked out with `core.autocrlf=true`; write source files with
LF endings so `gofmt -l` stays quiet.

## Versioning / CI

Versions are calendar-based: `YY.M.D`, UTC, no zero padding — `26.7.29`. A
second release on the same day appends a counter (`26.7.29.1`). CI builds of
`main`/PRs use the same date with a `-dev` suffix. There is no version literal
in the source: `main.version` and `main.build` are `var`s stamped at link time
with `-X`, defaulting to `0.0.0` / `0` for a local `go build`. `build` is the
workflow run number (per-workflow, so it's only unique alongside the version).

Two workflows, both `windows-latest`, both building `dist/RGOClient.exe` with
`CGO_ENABLED=1 -H windowsgui`. The exe is unsigned:

- `.github/workflows/build.yml` — push/PR to `main` + manual. Uploads the exe
  as a run artifact. No tag, no release.
- `.github/workflows/release.yml` — `workflow_dispatch` is the normal path: the
  run computes today's version, skips forward past any tag that already exists,
  creates and pushes the tag itself, then publishes the release. Pushing a `v*`
  tag by hand also works and takes the tag verbatim as the version — that's the
  escape hatch for re-releasing or naming an off-calendar version.

The build step is duplicated between the two files; if a third consumer
appears, lift it into a composite action.

## Known stubs / follow-ups

The settings window is a placeholder; reply-preview tap navigation is a TODO in
`ui/message.go` (`buildReplyPreview`); `App.createServer` (the join dialog's
"Create a server" button) only reports that creation isn't built yet.

A profile shows what the client already knows plus the bio. The account's profile
*background* is fetched with it and deliberately not drawn: a `canvas.Image`
takes no corner radius, so a full-bleed banner image would square off the card's
rounded top, and the alternative — cropping and alpha-masking the corners of a
possibly multi-megapixel image on every open — buys less than the flat role
colour already gives. Mutual friends and servers (`Session.UserMutual`), the
relationship (friend / blocked / pending) and any way to *act* on one are absent
for the same reason the sidebar menus stay small. Nothing on either presentation
scrolls, so a long bio is trimmed (`profileBioRunes`) rather than given a
scroller, and the compact card shows it as plain text — a card that only names
someone shouldn't chase their formatting. Neither presentation refreshes: a
`UserUpdate` arriving while one is open leaves it as it was until reopened.

`markdown.CodeBlock.Language` is parsed and tested but nothing renders syntax
highlighting yet. `util.FileType` classifies video/audio/archive/PDF, but only
`FileTypeImage` and `FileTypeText` are branched on.

The composer has no attach or emoji button — files still arrive only by drag or
paste — and no role/channel mentions (`<@id>` users only). `EditEntry` has no
mention picker: editing a message can only mention someone by typing the raw
token. A composed mention stays a visible `<@id>` in the entry until it is sent;
Fyne cannot draw a chip inside an entry's text, so the alternative would be
mapping display names back to IDs at send time, which breaks on duplicate names.
`markdown.PlainText` renders a mention as a bare `@` for the same reason it can't
resolve one — it has no session — so a reply preview of a message that opens with
a mention starts with a lone `@`.

The sidebar context menus stay small: an ID to copy, "Mark as read" where there
is something to mark, and — below a separator — the one destructive item that
row can offer, if any. Servers can be left (never by their owner, for whom the
same endpoint would delete the server outright — a different question needing a
sterner dialog), conversations closed, members removed. Banning, role edits,
nickname changes, and deleting a server channel are all one call away but none
is offered: each wants more than a yes/no card, and moderation the client cannot
undo should not be a menu item away. Only servers show a hover tooltip; the
fixed home, settings and "+" buttons are icon-only too and could take the same
`ui.Tooltip`.

Notices are transient and unlogged from the user's side: there is no history
panel, so a card that times out while the window is in the background is gone.
The login screen has no notice layer either — it isn't built until Ready — so
`session.go` still reports a dead token through `dialog.ShowError`.

The home view has no `ChannelCreate` handler, so a DM opened while the client is
running (by the other party, or from a profile once that exists) only appears the
next time the DM list is refreshed — its messages are still cached and its unread
mark still recorded, they just have no row until then.

The attachment viewer, the join-server dialog and confirmations are modal
overlays, not windows: they have no native chrome to recolour, cannot be left
behind, and can't drift off-centre. Windows the client *does* open (main, settings) get
their title bar recoloured through `App.styleNativeChrome`.

`assets/` still carries `close.svg`, `edit.svg`, `file.svg`, `reply.svg` and
`trash.svg`, which nothing references — the equivalents come from Fyne's theme
icon set. Only `mention.svg`, `members.svg` and `rgo.png` are embedded.

Markdown rendering (`ui/markdown.go`): strike / underline / spoiler share one
`decoratedSegment` (Fyne renders neither strike nor underline natively). They
carry nested bold/italic and inherit the block's size/color, and each word
bridges the following space so lines/covers read as continuous. Remaining
limitations: the blockquote bar is drawn per source line but not on wrapped
continuation lines; a decoration can show a one-space nub at the end of a wrapped
line; inline `code` inside a decorated span is not itself decorated.

Text selection: message bodies whose whole content shares one style (plain, but
also all-bold/italic, a lone code block, heading, subtext, plain lists, inline
code alone) flatten to a `Selectable` `widget.Label` carrying that style. Fyne
2.8 has no public RichText selection — its internal `selectable` helper is
unexported and assumes one uniform text style — so bodies mixing styles within
the text stay unselectable, as does any body carrying a mention (its colour is a
second style); right-click → Copy message covers them. Selecting *across* two
messages isn't possible either: each body is its own Label, and the selection
overlay belongs to one of them.

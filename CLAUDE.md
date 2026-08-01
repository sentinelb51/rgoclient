# rgoclient

A Fyne v2.8.0 desktop chat client (Discord-like) for Revolt, in Go 1.26.4. Uses
`github.com/sentinelb51/revoltgo` for the REST API and gateway websocket.

## Core principle: explicit dependencies, no globals

There is no global session or cache. The `*app.App` controller owns the session
and both caches and passes what widgets need through a `ui.Deps` value:

```go
type Deps struct {
    Session *revoltgo.Session   // resolve users, system messages
    Images  *cache.ImageCache   // load avatars / icons / attachments
    Actions MessageActions      // user-interaction callbacks (implemented by *app.App)
}
```

Widget constructors take `Deps` (e.g. `ui.NewMessageWidget(deps, msg)`).
`util` helpers take an explicit `*revoltgo.Session` argument. Do not reintroduce
package-level singletons.

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
                             (see "Versioning / CI")

assets/                      Embedded binaries (go:embed can't reach above its own
                             source file, so this sits at the repo root)
  fonts.go                   Montserrat static cuts
  icons.go                   MentionIcon + AppIcon. Everything else the UI draws
                             comes from Fyne's theme icon set; nothing is read
                             from disk at runtime, so the binary runs from any cwd

internal/
  app/                       Controller; owns session + caches + window + UI refs
    app.go                   App struct, New, Run, lifecycle, doOnUI (the single
                             UI-thread entry point for handlers/workers),
                             styleNativeChrome (every window's title bar),
                             MessageActions impl; OnEdit/startEditing/
                             cancelActiveEdit drive the one active in-place
                             message edit (App.editing, UI-thread only)
    session.go               startWithToken / startWithLogin + handler registration;
                             resetSessionState clears per-account caches/state on (re)login
    events.go                Gateway lifecycle handler (onError) + handler-split doc
    events_ready.go          onReady + savePendingToken
    events_message.go        onMessage / onMessageUpdate / onMessageDelete /
                             onBulkMessageDelete + scheduleAck/sendAck — the only
                             read-ack path (selectChannel routes through it too):
                             one MessageAck per ackDelay, and a pending ack for a
                             different channel flushes immediately on switch
    events_members.go        onServerMemberJoin / Leave / Update → refresh member sidebar
    events_server.go         onServerCreate → append to serverIDs + refresh the sidebar,
                             selecting the new server only when App.pendingJoin marks it
                             as one the user just asked to join
    navigation.go            Server+channel sidebar + 4-column buildUI: build, refresh, select.
                             Server sidebar bookends the scrolling icons with fixed home
                             (selectHome, directmessages.go) and settings (openSettings, WIP
                             window) buttons; the join-server "+" sits at the end of the
                             scrolling icons (re-added by every refreshServerList).
                             refreshChannelList renders either a server's categorised
                             channels or, in the home view, the flat DM list
    directmessages.go        The home view: selectHome + loadDirectMessages /
                             setDirectMessages (Session.DirectMessages, which feeds
                             State itself — only the order is kept here),
                             sortConversations (closed DMs dropped, newest activity
                             first, returns IDs) and resolveRecipients (bounded user
                             fetch so DM rows have names)
    messages.go              Message area: build, load, display, refresh/remove;
                             buildMessageArea assembles the composer dock (rounded
                             card, accent focus ring, exact insets — see "Composer
                             geometry");
                             setChannelGlyph (the header's # / @ / group mark);
                             continuesGroup/newMessageWidget (Discord-style author
                             grouping); dayLabel (day-separator text for a message,
                             "" when it shares its predecessor's day);
                             displayMessages/displayCached; jumpToLatest;
                             editLastOwnMessage (Up in empty composer → edit newest
                             own cached message)
    mounting.go              The mounted window — which slice of the cache has live
                             widgets, and how it slides: appendMessage,
                             loadMoreHistory, prependMessages, mountNewerFromCache,
                             appendMessages, trimMountedTop/Bottom. Its header
                             states the invariants
    compose.go               handleSubmit + attachment upload + reply conversion
    mentions.go              refreshMentionCandidates → mentionCandidates: who the
                             composer's @picker may offer in the open channel,
                             resolved from State alone (a server's members, or a
                             DM/group's recipients) and sorted by name so the
                             suggestions don't shuffle between rebuilds
    viewer.go                The attachment lightbox: showAttachmentViewer +
                             showOverlay/closeOverlay, the one modal layer
                             (App.overlay, UI-thread only)
    invite.go                showJoinServer + joinServer: the invite dialog on that
                             same modal layer (App.joinDialog) and the InviteJoin
                             call; createServer is the dialog's other button (stub)
    members.go               Member sidebar: build/refresh/group; ensureAuthor →
                             flushAuthors → resolveAuthor (lazy author resolution,
                             queued then fetched in one bounded batch);
                             refreshAuthorMessages (in-place per-author widget
                             update, no full re-render)
    login.go                 Login view + login flow
    store.go                 Saved-session JSON persistence (~/.rgoclient_sessions.json)
  cache/
    image.go                 ImageCache — instance built by App; LRU-bounded memory
                             (maxMemoryImages) + disk + async load; trimDiskCache
                             evicts oldest-first back under budget at startup
    message.go               MessageCache — per-channel, oldest→newest, capped per channel
                             (Set/Append/Prepend all trim), LRU channel eviction;
                             Find/Remove/Replace = binary search by ID (ULIDs sort
                             chronologically; CompareMessageID is shared with app);
                             both entries *and* published slices are immutable —
                             Remove/Replace rebuild rather than edit in place, so a
                             UI-thread reader holding an earlier slice is safe;
                             Clear on logout
    lru.go                   LRU — shared O(1) recency tracker (list + map) behind
                             every bounded cache (images, messages, text previews)
  ui/
    theme/theme.go           Colors, Sizes, AppTheme (palette + scrollbar/widget overrides)
    deps.go                  Deps struct + MessageActions interface
    thread.go                DoOnUI — the package's UI-thread dispatch
    layouts.go               Custom layouts + spacer helpers (incl. GutterLayout:
                             zero-height fixed-width column for the grouped timestamp;
                             NewMinWidth/MinHeightContainer, one pinned axis apiece;
                             NewFixedWidthContainer, which pins a sidebar's width
                             against its own content — see "Sidebar widths";
                             NewInset, exact per-edge padding, unlike NewPadded
                             and NewBorder which add theme padding of their own)
                             + FitWithin (aspect-preserving downscale, shared by the
                             attachment preview and the image viewer)
    ellipsis.go              NewEllipsisText + TruncateToWidth — a canvas.Text that
                             shortens to the width it is given (zero minimum width,
                             so a long name can't widen its column)
    interactive.go           Shared tap/hover widgets (TappableContainer, HoverableStack,
                             IconButton, CloseButton, Avatar, SidebarButton + the
                             NewSidebarSeparator bar) built on tapBase; roundedPanel.
                             SidebarButton carries a selected state (selection outranks
                             hover) so the home button lights like a server icon
    overlay.go               Overlay — the full-canvas modal layer (dim backdrop,
                             centred content, tap/Esc to dismiss) + tapSink, which
                             stops taps on the content reaching the backdrop
    viewer.go                NewAttachmentViewer — the lightbox card: header
                             (name/size/dimensions, open-in-browser, close) over a
                             fitted image, a full scrollable text pane, or a
                             no-preview placeholder
    joinserver.go            JoinServerDialog — the invite card, laid out like the
                             login screen (centred heading, separators, section
                             labels, full-width controls): code/link field, status
                             line (Fail/Notice), Join, and the Create a server stub.
                             Validates through util.InviteCode before calling back,
                             and its inviteEntry handles Esc itself (a focused entry
                             never reaches the canvas handler)
    icons.go                 newScaledIcon — the one fill/scale policy for every icon
    scroll.go                ObservableScroll (wheel amplify + middle-button pan)
    server.go                Server icon widget
    channel.go               Channel row (named via util.ChannelName, so a DM row shows
                             the other participant; the name takes the leftover width
                             through NewEllipsisText) + collapsible category + the glyph
                             set ChannelGlyph picks from: HashtagIcon / AtIcon / GroupIcon
    message.go               MessageWidget: construction, permissions, quick actions /
                             context menu, in-place edit mode, hover, content assembly.
                             A non-empty dayLabel draws a day separator above the row
                             (outside the hover-highlight stack)
    dayseparator.go          NewDaySeparator — the dated hairline (day name left, rule
                             out to the right edge) above the first message of a
                             calendar day + its two-child layout. Not a list entry of
                             its own: MessageWidget owns it
    attachment.go            Attachment rendering (image / text preview / generic card),
                             the LRU-bounded text-preview cache, fetchText (shared,
                             byte-capped download), and the name/size bar. Images and
                             text files are tappable → Actions.OnAttachmentTapped
    reply.go                 Reply preview line, reply resolution, circular avatar
    editor.go                EditEntry — in-place edit entry (Enter saves, Shift+Enter
                             newline, Esc cancels; grows like the composer)
    caret.go                 WithCaret — per-entry theme override restoring the caret
                             (AppTheme zeroes SizeNameInputBorder for flat inputs, and
                             Fyne draws the caret InputBorder wide). Wrap every mounted
                             entry in it
    contextmenu.go           ShowContextMenu / AnchorBelow / copyToClipboard
    member.go                Member row (name via NewEllipsisText, as in the channel
                             row) + section header + small presence-dimmed avatar
    markdown.go              AST → RichText rendering; strike/spoiler custom segments;
                             uniform-style bodies (plain, all-bold/italic, lone code
                             block / heading / subtext, plain lists) flatten to a
                             Selectable Label (mouse text selection); only mixed-style
                             bodies keep the unselectable RichText. mdBuilder carries
                             Deps because <@id> is only an ID in the AST — mention()
                             resolves it through the session and draws "@Name" in
                             theme.ColorNameMention
    composer_test.go         Composer/mention regression tests: text centred in the
                             dock, one line of growth per newline, mentionQuery's
                             open/don't-open rules, picker ranking, token insertion,
                             and that opening the picker really grows the card
    preview_test.go          Renders the composer to a PNG for eyeballing; skipped
                             unless RGO_PREVIEW names an output path
    input.go                 Message input + attachments + reply cards (slim,
                             role-colour-outlined; shared replyIconButton for the
                             mention toggle + close); OnEditLast fires on Up in an
                             empty composer; OnFocusChanged drives the dock's focus
                             ring; composerMinSize (shared with EditEntry — see
                             "Composer geometry"); the @mention trigger:
                             mentionQuery / syncMentions / acceptMention and the
                             cursorIndex ↔ cursorPosition pair that converts Fyne's
                             row/column caret to a rune index and back
    mention.go               MentionCandidate (+ its precomputed lowercase match
                             keys) and MentionPicker — the @autocomplete list, with
                             pooled rows re-set per keystroke, prefix-before-
                             substring ranking, a "+N more" footer instead of a
                             scrollbar, and Step/Accept/Reset for keyboard driving
    sessioncard.go           Saved-session card
    titlebar_windows.go      DWM recolouring of the native title bar (no-op elsewhere,
    titlebar_notwindows.go   see App.styleNativeChrome's retry loop)
  markdown/                  Pure (no UI) Discord/Revolt markdown parser → small AST
    markdown.go              Document/Block/Inline AST node types
    parser.go                Block parsing (paragraph, heading, quote, list, fence…)
    inline.go                Inline parsing (bold/italic/strike/spoiler/code/link/
                             <@mention>) + PlainText; Discord-style emphasis guards:
                             _ opens/closes only at word boundaries (snake_case stays
                             literal) and single */_ content can't be whitespace-edged
  util/
    message.go               MessageAuthor — one-pass author resolution (name, avatar URL,
                             role colour; member-aware: nickname + per-server avatar, fall
                             back to user) / FormatSystemMessage (session arg)
    member.go                MemberName / MemberAvatarURL / MemberOnline / MemberColor
                             (role colour, same rule MessageAuthor uses) / UserName
                             (user ID → display name from State, "" when unknown)
                             — all take an explicit session
    channel.go               ChannelName (DM → the other participant, SavedMessages →
                             "Saved Notes", else the channel's own name) / DMRecipientID
    attachment.go            IsImageAttachment / AttachmentDimensions (nil-Metadata safe)
    text.go                  Truncate (rune-safe, "..." suffix)
    files.go                 Filetype + FormatFileSize
    timestamp.go             ULID Timestamp + ShortTime + NiceTime + SameDay /
                             DayLabel (day-separator text: Today / Yesterday / date)
    url.go                   IDFromAttachmentURL
    invite.go                InviteCode — bare code / invite link / scheme-less link
                             → code ("" when it isn't shaped like one)
```

## Data flow

1. Login (`login.go`) → `startWithToken` / `startWithLogin` → `openSession` registers
   handlers and opens the gateway. `startWithLogin` stashes the new token in
   `pendingToken` *before* opening (Ready can beat the login goroutine back to the
   UI thread). The login screen stays up until Ready; only failures return to it.
2. `onReady` → save pending token, record unreads, `showMainUI` (the only place the
   main layout is built), `refreshServerList`, `selectServer(first)` — or
   `selectHome` when the account is in no servers, so the client never lands blank.
3. `selectServer` → `refreshChannelList`, `refreshMemberList` (paints whatever
   members State currently holds) → `selectChannel(first)`. There is no bulk member
   fetch: Revolt's members endpoint has no pagination, so large servers would flood
   memory. Members are resolved lazily per author (see `ensureAuthor`).
4. `selectChannel` → show cached messages, else `loadChannelMessages` (asks for
   `IncludeUsers: true`, see the revoltgo note below); ack unread.
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
   avatar, role colour), falling back to the raw user. Consecutive messages from the
   same author within `messageGroupWindow` render grouped (`continuesGroup`): no
   repeated avatar/name, just a hover-revealed gutter timestamp. All widget
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
   caret merely *moves* out of a mention). While it is open it gets first refusal
   on Up/Down/Enter/Tab/Esc, which the composer otherwise binds to sending and to
   editing the last message. Accepting rewrites the `@query` span as Revolt's
   `<@id>` wire token — the composer shows the raw token, since a Fyne entry
   cannot draw a chip inside its text — and `ui/markdown.go` renders that token
   back as an accent-coloured `@Name` in message bodies.
   The candidate list is *pushed*, not pulled: `App.refreshMentionCandidates`
   resolves it from State (on `selectChannel`, on every `refreshMemberList`, and
   after each author batch lands) and the picker filters that snapshot per
   keystroke. So the expensive part — walking State and resolving names — happens
   once per membership change, and a keystroke is two string comparisons per
   candidate with nothing allocated. Because it reads State only, a server's
   candidates are whoever the client already knows: the gateway's members plus
   everyone lazy author resolution has pulled in, which is the same bounded set
   the member sidebar shows and the same reason there is no bulk member fetch.
   The picker is mounted *inside* the composer card rather than floating over the
   message area: a Fyne pop-up takes canvas focus, which would pull it off the
   entry and stop the typing that drives it.
9. `onServerMember{Join,Leave,Update}` → State auto-updates (revoltgo default
   handlers); app handler refreshes the member sidebar when it's the open server.
   `Update` also calls `refreshAuthorMessages` so that author's mounted messages
   pick up the new nickname / role colour / avatar in place.
10. The home view: the fixed home button swaps the channel sidebar from a
   server's channels to the user's direct messages and groups. `App.homeSelected`
   is what marks it open — home has no server, so an empty `currentServerID`
   alone can't be told apart from nothing selected — and `selectServer` clears it.
   The list comes from `Session.DirectMessages()`, which feeds the channels into
   State on the way through, so the app keeps only the sidebar *order*
   (`App.dmChannels`, a slice of IDs) and looks every channel up through
   `App.stateChannel` like any other. It is still a plain fetch with no gateway
   event maintaining it, which is why the order is re-asked for rather than
   pushed. `selectHome` paints the cache immediately and refreshes
   in the background (stale-while-revalidate), so re-opening home never blanks
   the sidebar; each refresh re-sorts by `LastMessageID` (a ULID, so string
   comparison sorts chronologically) and resolves any recipients missing from
   State, since a DM has no name of its own. Ordering is a snapshot: an incoming
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

## Conventions

- Pass dependencies via `ui.Deps`; never reach for global state.
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
- Any custom widget overriding `Dragged` must also have `DragEnd`, or the driver
  never recognises it as draggable.
- Colors and sizes come from `ui/theme`, never hardcoded. Don't express one size
  as an offset from an unrelated one — add a named entry.
- **Sidebar widths.** The three side columns are pinned by
  `ui.NewFixedWidthContainer`, not by a minimum-size background rectangle: only
  the message area (`RowLayout.FillIndex`) may change width, and it changes only
  when the window does. A minimum size is a *floor*, and `container.NewVScroll`
  reports its content's minimum width as its own, so one long channel or member
  name used to widen its column and shove the message area sideways. Anything
  rendering a user-supplied name into a sidebar row therefore goes in the
  stretching slot of a `Border` wrapped in `ui.NewEllipsisText` (or, for a
  `widget.Label`, with `Truncation = fyne.TextTruncateEllipsis`) so it shortens
  to fit instead of pushing outwards.
- **Composer geometry.** A growing entry's height is
  `lineHeight × lines + InnerPadding × 2` (`composerMinSize`, shared by the
  composer and `EditEntry`) — and the input border is *not* added on top.
  `entryRenderer.Layout` pays for the border out of the text provider's own
  padding (it sets `textProvider().inset` to the border size), so the border is
  already inside `InnerPadding`. Counting it twice made both entries four pixels
  taller than their content, and since the entry top-aligns its text inside its
  scroller, all four landed as dead space under the caret — which is what made
  the composer look slack. `TestComposerTextIsVerticallyCentred` guards it.
  Nothing around the dock may add padding it wasn't asked for either: the card
  uses `ui.NewInset`, because `container.NewPadded` applies theme padding and
  `container.NewBorder` inserts theme padding between its edges and its centre.
- Use the `log` package for diagnostics.
- Keep this file current when adding files/packages, changing data flow, adding
  widgets, modifying `App` fields, or changing event handling.

## Build / check

`go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l internal cmd assets`
(expect no output).

## Versioning / CI

Versions are calendar-based: `YY.M.D`, UTC, no zero padding — `26.7.29`. A
second release on the same day appends a counter (`26.7.29.1`). CI builds of
`main`/PRs use the same date with a `-dev` suffix. There is no version literal
in the source: `main.version` and `main.build` are `var`s stamped at link time
with `-X`, defaulting to `0.0.0` / `0` for a local `go build`. `build` is the
workflow run number (per-workflow, so it's only unique alongside the version).

Two workflows, both `windows-latest`, both building `dist/RGOClient.exe` with
`CGO_ENABLED=1 -H windowsgui` and Authenticode-signing it with the persistent
self-signed cert in `CODE_SIGN_PFX_BASE64` / `CODE_SIGN_PFX_PASSWORD`:

- `.github/workflows/build.yml` — push/PR to `main` + manual. Uploads the exe
  as a run artifact. No tag, no release.
- `.github/workflows/release.yml` — `workflow_dispatch` is the normal path: the
  run computes today's version, skips forward past any tag that already exists,
  creates and pushes the tag itself, then publishes the release. Pushing a `v*`
  tag by hand also works and takes the tag verbatim as the version — that's the
  escape hatch for re-releasing or naming an off-calendar version.

The build + sign steps are duplicated between the two files; if a third
consumer appears, lift them into a composite action.

## Known stubs / follow-ups

`App.OnAvatarTapped` (profile) just prints; the settings window is a
placeholder; reply-preview tap navigation is a TODO in `ui/reply.go`
(`buildReplyPreview`); `App.createServer` (the join dialog's "Create a server"
button) only reports that creation isn't built yet.

The composer has no attach or emoji button — files still arrive only by drag or
paste — and no role/channel mentions (`<@id>` users only). `EditEntry` has no
mention picker: editing a message can only mention someone by typing the raw
token. A composed mention stays a visible `<@id>` in the entry until it is sent;
Fyne cannot draw a chip inside an entry's text, so the alternative would be
mapping display names back to IDs at send time, which breaks on duplicate names.
`markdown.PlainText` renders a mention as a bare `@` for the same reason it can't
resolve one — it has no session — so a reply preview of a message that opens with
a mention starts with a lone `@`.

The home view has no `ChannelCreate` handler, so a DM opened while the client is
running (by the other party, or from a profile once that exists) only appears
the next time the DM list is refreshed — its messages are still cached and its
unread mark still recorded, they just have no row until then.

The attachment viewer and the join-server dialog are modal overlays, not
windows: they have no native chrome to recolour, cannot be left behind, and
can't drift off-centre. Windows the client *does* open (main, settings) get
their title bar recoloured through `App.styleNativeChrome`.

`assets/` still carries `close.svg`, `edit.svg`, `file.svg`, `reply.svg` and
`trash.svg`, which nothing references — the equivalents come from Fyne's theme
icon set. Only `mention.svg` and `rgo.png` are embedded.

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
the text stay unselectable; right-click → Copy message covers them.

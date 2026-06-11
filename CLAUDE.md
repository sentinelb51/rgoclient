# rgoclient

A Fyne v2.7.4 desktop chat client (Discord-like) for Revolt, in Go 1.26.4. Uses
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

## Project structure

```
cmd/rgoclient/main.go        Entry point: Fyne app, theme, app.New(...).Run()

internal/
  app/                       Controller; owns session + caches + window + UI refs
    app.go                   App struct, New, Run, lifecycle, doOnUI, MessageActions impl;
                             OnEdit/startEditing/cancelActiveEdit drive the single active
                             in-place message edit (App.editing, UI-thread only)
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
    navigation.go            Server+channel sidebar + 4-column buildUI: build, refresh, select.
                             Server sidebar bookends the scrolling icons with fixed home
                             (selectHome, stub) and settings (openSettings, WIP window) buttons
    messages.go              Message area: build, load, display, send, history, viewer;
                             continuesGroup/newMessageWidget (Discord-style author grouping);
                             windowed mounting (mountedCap) with up/down cache re-mount
                             tiers; removeMessage/refreshMessage for live deletes/edits
                             (refreshMessage skips a message being in-place edited);
                             jumpToLatest on send; editLastOwnMessage (Up in empty
                             composer → edit newest own cached message)
    members.go               Member sidebar: build/refresh/group; ensureAuthor (lazy per-author
                             user+member fetch, fetchedAuthors guard); refreshAuthorMessages
                             (in-place per-author widget update, no full re-render)
    login.go                 Login view + login flow
    store.go                 Saved-session JSON persistence (~/.rgoclient_sessions.json)
  cache/
    image.go                 ImageCache — instance built by App; LRU-bounded memory
                             (maxMemoryImages) + disk + async load
    message.go               MessageCache — per-channel, oldest→newest, capped per channel
                             (Set/Append/Prepend all trim), LRU channel eviction;
                             Find/Remove/Replace = binary search by ID (ULIDs sort
                             chronologically; CompareMessageID is shared with app);
                             entries are immutable — edits Replace a copy; Clear on logout
    lru.go                   lruKeys — shared O(1) recency tracker (list + map) used by
                             both caches
  ui/
    theme/theme.go           Colors, Sizes, AppTheme (palette + scrollbar/widget overrides)
    deps.go                  Deps struct + MessageActions interface
    layouts.go               Custom layouts + spacer helpers (incl. GutterLayout:
                             zero-height fixed-width column for the grouped timestamp)
    interactive.go           Shared tap/hover widgets (TappableContainer, HoverableStack,
                             iconButton, CloseButton, Avatar, SidebarButton + the
                             NewSidebarSeparator bar) built on tapBase
    scroll.go                ObservableScroll (wheel amplify + middle-button pan)
    server.go                Server icon widget
    channel.go               Channel row + collapsible category + drawn glyphs
    message.go               Message widget + content/attachments/replies rendering;
                             grouped continuation mode (no avatar/name; hover-revealed
                             gutter timestamp); RefreshAuthor in-place update; in-place
                             edit mode (StartEdit/CancelEdit swap the body slot for an
                             EditEntry + floating save/cancel buttons)
    editor.go                EditEntry — in-place edit entry (Enter saves, Shift+Enter
                             newline, Esc cancels; grows like the composer)
    caret.go                 WithCaret — per-entry theme override restoring the caret
                             (AppTheme zeroes SizeNameInputBorder for flat inputs, and
                             Fyne draws the caret InputBorder wide). Wrap every mounted
                             entry in it
    member.go                Member row + section header + small presence-dimmed avatar
    markdown.go              AST → RichText rendering; strike/spoiler custom segments;
                             uniform-style bodies (plain, all-bold/italic, lone code
                             block / heading / subtext, plain lists) flatten to a
                             Selectable Label (mouse text selection); only mixed-style
                             bodies keep the unselectable RichText
    input.go                 Message input + attachments + reply cards (slim,
                             role-colour-outlined; shared replyIconButton for the
                             mention toggle + close); OnEditLast fires on Up in an
                             empty composer
    sessioncard.go           Saved-session card
  markdown/                  Pure (no UI) Discord/Revolt markdown parser → small AST
    markdown.go              Document/Block/Inline AST node types
    parser.go                Block parsing (paragraph, heading, quote, list, fence…)
    inline.go                Inline parsing (bold/italic/strike/spoiler/code/link) +
                             PlainText; Discord-style emphasis guards: _ opens/closes
                             only at word boundaries (snake_case stays literal) and
                             single */_ content can't be whitespace-edged
  util/
    message.go               MessageAuthor — one-pass author resolution (name, avatar URL,
                             role colour; member-aware: nickname + per-server avatar, fall
                             back to user) / FormatSystemMessage (session arg)
    member.go                MemberName / MemberAvatarURL / MemberOnline (session arg)
    text.go                  Truncate (rune-safe, "..." suffix)
    files.go                 Filetype + FormatFileSize
    timestamp.go             ULID Timestamp + NiceTime
    url.go                   IDFromAttachmentURL
```

## Data flow

1. Login (`login.go`) → `startWithToken` / `startWithLogin` → `openSession` registers
   handlers and opens the gateway. `startWithLogin` stashes the new token in
   `pendingToken` *before* opening (Ready can beat the login goroutine back to the
   UI thread). The login screen stays up until Ready; only failures return to it.
2. `onReady` → save pending token, record unreads, `showMainUI` (the only place the
   main layout is built), `refreshServerList`, `selectServer(first)`.
3. `selectServer` → `refreshChannelList`, `refreshMemberList` (paints whatever
   members State currently holds) → `selectChannel(first)`. There is no bulk member
   fetch: Revolt's members endpoint has no pagination, so large servers would flood
   memory. Members are resolved lazily per author (see `ensureAuthor`).
4. `selectChannel` → show cached messages, else `loadChannelMessages` (uses
   `IncludeUsers: true`, so its page populates State's users + members); ack unread.
   `displayMessages` mounts only the newest `initialMountCount` messages (~2-3
   screenfuls; mounting more is churn the renderer cache holds for up to a
   minute, which is what made rapid channel switching ratchet memory); each
   run bumps `App.renderGen` so a superseded render aborts (channel IDs alone
   can't tell fast A→B→A switches apart), and sets `App.rendering`, which holds
   off live appends and re-mounts until the final pass (which then catches up via
   `mountNewerFromCache`) so they can't interleave with the batches. Scrolling to
   the top (`loadMoreHistory`) is two-tier: cached-but-unmounted messages prepend
   synchronously, then older pages come from the network — both anchored on the
   oldest *mounted* message. The mounted window is bounded at `mountedCap`:
   prepends past it trim widgets off the bottom (only while the new bottom is
   still cached — past the cache cap the window grows instead, since the cache
   ends at the live tail), and scrolling back down re-mounts them from cache
   (`mountNewerFromCache`, never network). Sending jumps to the newest message
   (`jumpToLatest`), re-rendering if scrollback trimmed the tail away.
   Message author name/avatar resolve from the server member (nickname, per-server
   avatar, role colour), falling back to the raw user. Consecutive messages from the
   same author within `messageGroupWindow` render grouped (`continuesGroup`): no
   repeated avatar/name, just a hover-revealed gutter timestamp. All widget
   construction goes through `App.newMessageWidget(prev, curr)`; `displayMessages`,
   `appendMessage`, and `prependMessages` each supply the right predecessor (prepend
   also re-evaluates the old top row against the newly prepended message above it).
5. `onMessage` → cache append (which returns the predecessor under the cache lock,
   so grouping survives bursts) → `ensureAuthor` (a gateway message carries only the
   author ID; fetch the user and, in a server, the member when missing from State,
   then update just that author's mounted widgets via `refreshAuthorMessages` and,
   if a member was fetched, the sidebar) → append to open channel + coalesced read
   ack (`scheduleAck`), else mark unread. If scrollback has detached the view from
   the live tail, the append is skipped — the message mounts on the way back down.
   `onMessageUpdate` applies the edit to a *copy* that replaces the cache entry
   (entries are read by the UI without the cache lock, so they stay immutable) and
   rebuilds the mounted widget; `onMessageDelete` / `onBulkMessageDelete` remove
   from cache and unmount, re-evaluating neighbour grouping at the seam.
6. In-place editing: `OnEdit` (hover/context-menu, or Up in an empty composer via
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
7. `onServerMember{Join,Leave,Update}` → State auto-updates (revoltgo default
   handlers); app handler refreshes the member sidebar when it's the open server.
   `Update` also calls `refreshAuthorMessages` so that author's mounted messages
   pick up the new nickname / role colour / avatar in place.

## Conventions

- Pass dependencies via `ui.Deps`; never reach for global state.
- Background goroutines update the UI through `App.doOnUI(fn, wait)`.
- `App.session` is written only on the UI thread (`openSession`, `onError`
  teardown). Worker goroutines capture `session := a.session` before launching
  and use the local — at worst they call into a closed session, which errors.
- Widget receiver is `w`; app receiver is `a`; cache receiver is `c`.
- Interface assertions live near the type: `var _ fyne.Tappable = (*T)(nil)`.
- Colors and sizes come from `ui/theme`, never hardcoded.
- Use the `log` package for diagnostics.
- Keep this file current when adding files/packages, changing data flow, adding
  widgets, modifying `App` fields, or changing event handling.

## Build / check

`go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l internal cmd`
(expect no output).

## Known stubs / follow-ups

`App.OnAvatarTapped` (profile) just prints; reply-preview tap navigation is a
TODO in `ui/message.go` (`buildReplyPreview`).

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
2.7 has no public RichText selection — its internal `selectable` helper is
unexported and assumes one uniform text style — so bodies mixing styles within
the text stay unselectable; right-click → Copy message covers them.

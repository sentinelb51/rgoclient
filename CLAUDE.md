# rgoclient

A Fyne v2 desktop chat client (Discord-like) for Revolt, in Go 1.26. Uses
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
    app.go                   App struct, New, Run, lifecycle, doOnUI, MessageActions impl
    session.go               startWithToken / startWithLogin + handler registration;
                             resetSessionState clears per-account caches/state on (re)login
    events.go                Gateway lifecycle handler (onError) + handler-split doc
    events_ready.go          onReady + savePendingToken
    events_message.go        onMessage / onMessageUpdate / onMessageDelete /
                             onBulkMessageDelete + scheduleAck (coalesced read-acks
                             for the open channel, one MessageAck per ackDelay)
    events_members.go        onServerMemberJoin / Leave / Update → refresh member sidebar
    navigation.go            Server+channel sidebar + 4-column buildUI: build, refresh, select.
                             Server sidebar bookends the scrolling icons with fixed home
                             (selectHome, stub) and settings (openSettings, WIP window) buttons
    messages.go              Message area: build, load, display, send, history, viewer;
                             continuesGroup/newMessageWidget (Discord-style author grouping);
                             windowed mounting (mountedCap) with up/down cache re-mount
                             tiers; removeMessage/refreshMessage for live deletes/edits;
                             jumpToLatest on send
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
                             gutter timestamp); RefreshAuthor in-place update
    member.go                Member row + section header + small presence-dimmed avatar
    markdown.go              AST → RichText rendering; strike/spoiler custom segments
    input.go                 Message input + attachments + reply cards (slim,
                             role-colour-outlined; shared replyIconButton for the
                             mention toggle + close)
    sessioncard.go           Saved-session card
  markdown/                  Pure (no UI) Discord/Revolt markdown parser → small AST
    markdown.go              Document/Block/Inline AST node types
    parser.go                Block parsing (paragraph, heading, quote, list, fence…)
    inline.go                Inline parsing (bold/italic/strike/spoiler/code/link) + PlainText
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
6. `onServerMember{Join,Leave,Update}` → State auto-updates (revoltgo default
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

`App.OnDelete`, `OnEdit`, `OnAvatarTapped` (profile) just print; reply-preview tap
navigation is a TODO in `ui/message.go` (`buildReplyPreview`).

Markdown rendering (`ui/markdown.go`): strike / underline / spoiler share one
`decoratedSegment` (Fyne renders neither strike nor underline natively). They
carry nested bold/italic and inherit the block's size/color, and each word
bridges the following space so lines/covers read as continuous. Remaining
limitations: the blockquote bar is drawn per source line but not on wrapped
continuation lines; a decoration can show a one-space nub at the end of a wrapped
line; inline `code` inside a decorated span is not itself decorated.

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
  icons.go                   MentionIcon + AppIcon. Everything else the UI draws
                             comes from Fyne's theme icon set; nothing is read
                             from disk at runtime, so the binary runs from any cwd

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
                             server/member events
    navigation.go            buildUI (the 4-column fill row) + the server and
                             channel sidebars, the settings window, and selection.
                             The server sidebar bookends its scrolling icons with
                             fixed home and settings buttons; the join-server "+"
                             sits at the end of the scrolling icons
    messages.go              The message area end to end: build, compose/submit,
                             widget construction (continuesGroup/dayLabel), load
                             and render, and the mounted window — which slice of
                             the cache has live widgets and how it slides. Its
                             "mounted window" section states the invariants
    members.go               Lazy author resolution (ensureAuthor → flushAuthors →
                             resolveAuthor, queued then fetched in one bounded
                             batch; refreshAuthorMessages updates widgets in place)
                             plus the member sidebar
    overlay.go               The modal layer: showOverlay/closeOverlay, the
                             attachment lightbox, and the join-server dialog +
                             joinServer/createServer
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
    layouts.go               Spacers, fitWithin, and the custom layouts:
                             noSpacingLayout (VBox/HBox/NewFillRow — one layout,
                             optional filling child), columnLayout (the message
                             avatar gutter; `collapse` makes it report zero height
                             for the grouped timestamp), overlayLayout,
                             minSizeLayout (NewMinWidth/MinHeightContainer), and
                             stripPaddingLayout
    widgets.go               Shared interactive widgets on tapBase
                             (TappableContainer, HoverableStack, IconButton,
                             SidebarButton, CloseButton, roundedPanel, separator),
                             the one avatar loader (circularAvatar/Avatar), and
                             ObservableScroll (wheel amplify + middle-button pan)
    sidebar.go               ServerWidget, ChannelWidget + collapsible category,
                             drawn glyphs (HashtagIcon), member row/section, and
                             the saved-session card
    message.go               MessageWidget: construction, permissions, quick
                             actions / context menu, in-place edit mode, hover,
                             content assembly — plus the day separator it owns
                             (not a list entry of its own), reply previews, and
                             EditEntry
    markdown.go              AST → RichText rendering; strike/underline/spoiler
                             custom segments; uniform-style bodies flatten to a
                             Selectable Label (mouse text selection); only
                             mixed-style bodies keep the unselectable RichText
    attachment.go            Attachment rendering (image / text preview / generic
                             card), the name/size bar, and fetchText (shared,
                             byte-capped download). Images and text files are
                             tappable → Actions.OnAttachmentTapped
    input.go                 Message input + attachments + reply cards; OnEditLast
                             fires on Up in an empty composer
    modal.go                 Overlay (the full-canvas modal layer) + tapSink,
                             NewAttachmentViewer (the lightbox card), and
                             JoinServerDialog (validates through util.InviteCode;
                             its entry handles Esc itself, since a focused entry
                             never reaches the canvas handler)
    theme/theme.go           Colors, Sizes, AppTheme (palette + scrollbar/widget
                             overrides)
    titlebar_windows.go      DWM recolouring of the native title bar (no-op
    titlebar_notwindows.go   elsewhere, see App.styleNativeChrome's retry loop)
  markdown/                  Pure (no UI) Discord/Revolt markdown parser → AST
    markdown.go              Document/Block/Inline AST node types
    parser.go                Block parsing, inline parsing (bold/italic/strike/
                             spoiler/code/link), and PlainText. Discord-style
                             emphasis guards: _ opens/closes only at word
                             boundaries (snake_case stays literal) and single
                             */_ content can't be whitespace-edged
  util/
    message.go               MessageAuthor — one-pass author resolution (name,
                             avatar URL, role colour; member-aware) — plus
                             MemberName/MemberAvatarURL/MemberOnline and
                             FormatSystemMessage
    file.go                  Filetype + FormatFileSize, IsImageAttachment /
                             AttachmentDimensions (nil-Metadata safe), and
                             IDFromAttachmentURL
    text.go                  Truncate (rune-safe) + InviteCode (bare code /
                             invite link / scheme-less link → code, "" when it
                             isn't shaped like one)
    timestamp.go             ULID Timestamp + ShortTime + NiceTime + SameDay +
                             DayLabel (Today / Yesterday / date)
```

## Data flow

1. Login (`session.go`) → `startWithToken` / `startWithLogin` → `openSession`
   registers handlers and opens the gateway. `startWithLogin` stashes the new
   token in `pendingToken` *before* opening (Ready can beat the login goroutine
   back to the UI thread). The login screen stays up until Ready; only failures
   return to it.
2. `onReady` → save pending token, record unreads, `showMainUI` (the only place
   the main layout is built), `refreshServerList`, `selectServer(first)`.
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
8. `onServerMember{Join,Leave,Update}` → State auto-updates (revoltgo default
   handlers); the app handler refreshes the member sidebar when it's the open
   server. `Update` also calls `refreshAuthorMessages` so that author's mounted
   messages pick up the new nickname / role colour / avatar in place.
9. Joining a server: the "+" at the end of the server sidebar opens
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
- Any custom widget overriding `Dragged` must also have `DragEnd`, or the driver
  never recognises it as draggable.
- Colors and sizes come from `ui/theme`, never hardcoded. Don't express one size
  as an offset from an unrelated one — add a named entry.
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

`App.OnAvatarTapped` (profile) just prints; `selectHome` is a stub; the settings
window is a placeholder; reply-preview tap navigation is a TODO in `ui/message.go`
(`buildReplyPreview`); `App.createServer` (the join dialog's "Create a server"
button) only reports that creation isn't built yet.

`markdown.CodeBlock.Language` is parsed and tested but nothing renders syntax
highlighting yet. `util.FileType` classifies video/audio/archive/PDF, but only
`FileTypeImage` and `FileTypeText` are branched on.

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

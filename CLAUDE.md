# rgoclient

A Fyne v2.8.0 desktop chat client (Discord-like) for Revolt, in Go 1.26.4. Uses
`github.com/sentinelb51/revoltgo` for the REST API and gateway websocket.

## Sources of truth

Revolt's protocol and backend now ship as **Stoat** (stoat.chat) — same shape,
new name. `revoltgo` is a second-hand reflection of that backend and can diverge
from it, bugs included, so ground a claim about wire behaviour against these
rather than against `revoltgo` or memory:

- `sources/openapi-spec-0.15.1.json` — the Stoat OpenAPI spec, also live at
  https://developers.stoat.chat/api-reference. **374 KB: grep it, never read
  it** — one whole read is most of a context window.
- https://github.com/stoatchat/stoatchat — the Rust backend (crate `delta`),
  authoritative for what a route or event actually does.

## Architecture

`internal/client` is the **only** package that imports `revoltgo`. It converts
wire types into `internal/domain` values on the way in; everything above is
written against the domain. The dependency graph is a strict DAG:

```
domain, markdown, config       no internal dependencies
cpu                            no internal dependencies    (Win32 / sysfs, no cgo)
audio/rnnoise                  no internal dependencies    (vendored Xiph C, cgo)
audio      -> audio/rnnoise                  (+ malgo, go-mp3)
voice      -> domain                         (+ lksdk, gopus)
util       -> config
cache      -> domain
client     -> cache, config, domain          (+ revoltgo)
ui         -> cache, config, domain, markdown, util
app        -> audio, voice, cache, client, config, cpu, domain, ui, util
```

`config` is a leaf so everything above can read a setting. `cache` and `audio`
deliberately do *not* import it — budgets, directories, volumes and file paths
arrive as arguments, so either builds in a test with no settings file anywhere.
`ui` does not import `audio` either: the composer names the *kind* of keystroke
(`ui.Keystroke`) and `app` decides what it sounds like. A device list crosses the
same way, as `ui.AudioDevice`, and so does the microphone meter's scale — the
level and the gate's threshold both arrive as ratios, decibels being `audio`'s.

`cpu` crosses the same seam from the other side. It reports which logical
processors are which and pins the process to a set of them; what a *kind* of core
is for — that the default wants the efficiency cores on a hybrid part and CCD1
on a dual-chiplet one — is a policy, and it lives in
`app.resolveCores` beside the setting that names it. The counts reach the settings
page as `ui.CPUCores`, the way a device list reaches it as `ui.AudioDevice`.

`voice` declares `PCMSource` / `PCMSink` **structurally** and never imports
`audio`, so `app` is the only package importing both and therefore the only place
that can hand an `*audio.Capture` to a `voice.Call`. Nothing in its surface
mentions Revolt, LiveKit or miniaudio: it is shaped to lift into
`sentinelb51/revoltgo-voice` as package `rvoice` without a line changing above
it, which is the whole reason the transport lives behind `Jitter` rather than
being written into the call.

The seam is not tidiness: `revoltgo.State`'s caches are unexported and
`newState()` is package-private, so nothing holding a `*revoltgo.Session` can be
built in a test. `domain.Store` can — `ui/store_test.go` has a map-backed
`fakeStore`.

No globals. The `*app.App` controller owns the client, caches and widgets and
passes widgets what they need through `ui.Deps` (`Store`, `Images`, `Texts`,
`Actions`, `Tooltip`). `App.deps()` is the only producer, so **every field is
always set** and widgets never nil-check them. The only package-level mutable
state is measurement memoisation (`ui.lineHeights`, `ui.spaceWidths`), UI-thread
only.

### The client's contract

- **`Client.Store()`** — reads, safe from any goroutine, never the network. A
  miss reports `ok=false`. Values arrive resolved: a `domain.Member` already
  carries nickname, per-server avatar, role colour, presence, bot mark and the
  hoisted role it is filed under. Safe off-thread is not cheap — `Members`
  resolves all of that per member and sorts, so it belongs on a worker.
- **`Client.Events()`** — one buffered channel. `app.pumpEvents` is its single
  reader; `dispatch` hops onto the UI thread once per event. `client.Event`'s
  marker method is unexported, so the switch is exhaustive. **Not gateway
  order**: revoltgo dispatches frames on a goroutine each, so two events about
  the same object can arrive inverted if both land inside one state mutation.
  A handler must not read one event as following another — derive from the
  store, which the gateway has already updated, rather than from arrival.
- **Action methods** (`SendMessage`, `HistoryBefore`, …) **block**: the request
  and the cache update, no widget touched, no goroutine spawned. The caller owns
  the UI thread (`App.background`).

Logged out is a valid state: reads report nothing, actions return
`client.ErrNoSession`. `Client.session` is an `atomic.Pointer` because actions
read it off-thread. `Client.epoch` counts sessions and each gateway handler
captures its own, so events from a replaced session are dropped; `App.epoch` +
`App.stale` are the same guard on the controller's side.

## Working in this repo

This file is the **core**: the DAG, the client's contract, the layout, the
conventions and the build. The rest is filed beside the code it is about, so a
change in `markdown/` does not pay for the Fyne footguns:

- `internal/app/CLAUDE.md` — the data flow, items 1-38: what happens in what
  order and why each step is where it is.
- `internal/client/CLAUDE.md` — the revoltgo notes: every bug, missing field
  and route that has to be sent by hand.
- `internal/ui/CLAUDE.md` — the Fyne footguns.
- `docs/known-gaps.md` — what is not built, and what revoltgo or Fyne prevents
  rather than effort.
- `docs/voice-chat-todo.md` — the voice work queue: what is missing, what is
  compromised, and what was measured and left alone. Read it before touching
  `internal/voice`, `internal/audio` or the call half of `internal/app`.
- `docs/performance.md` — what a frame costs, which levers are reachable and
  which need a fork of Fyne. Read it before optimising anything.

A directory's `CLAUDE.md` arrives on its own when a file in that directory is
touched. **Read the others by hand when a change crosses a boundary** — a new
field the UI needs is `client/convert.go`, a `domain` type and a widget, which
is three of these.

### Context discipline

The tree is ~1.2 MB of Go and the largest files are 50 KB each, so what gets
read *is* the budget.

- **Use the `rgo-explore` agent to locate code** (`.claude/agents/`) rather than
  reading files to find it. It searches in its own context and reports
  `file:line` and the shape, so a twenty-file sweep costs a paragraph here.
  Worth the round trip for anything past about three files.
- **Then read the file before editing it.** A summary carries what a function
  does and drops the constraint it is shaped by. The agent narrows what has to
  be read; nothing is edited off a report alone.
- Grep with context lines beats a whole-file read for one identifier.

## Project structure

Names say most of it; only the non-obvious placements are annotated.

```
cmd/rgoclient/main.go    app ID, fyneDo flag, config.Load + theme.Apply before the
                         first widget. version/build are -X link-time vars
assets/                  at the root because go:embed can't reach above its own file
  fonts.go icons.go      Montserrat cuts; the marks. Stroked outlines but for the
                         two call handsets, which are solid; either way one colour,
                         which is what ui.tintedIcon rewrites

internal/
  config/config.go       Settings tree, Default, Load/Save/Path, Current/Update.
                         Styles holds *overrides* keyed by theme field name, so a
                         newly named size arrives with its default intact. Current
                         is an atomic.Pointer snapshot; writes debounced
  domain/                domain.go (value types; Embed is one shape for every kind,
                         so renderers branch on what is filled in) + store.go
  client/                client.go, auth.go, security.go, convert.go, store.go,
                         events.go, actions.go
  cache/                 cache.go (LRU + TextCache), message.go, image.go
  audio/                 both directions of the machine's sound, on miniaudio (malgo).
                         audio.go (the engine and the one playback device),
                         mix.go (what the device callback runs: notification sounds
                         and the call's per-participant lanes, summed without
                         allocating — and the wake it sends afterwards, which is
                         the clock the whole receive path is paced by. Also the
                         gain ceiling and softClip, the curve the *sum* meets it
                         through, both directions sharing the one definition),
                         ring.go (the wait-free SPSC queue every
                         hand-off across that callback is made of), sink.go (the
                         call's lanes; Want is how deep they are kept and is what
                         stops a decoder running ahead of the speakers),
                         capture.go + process.go (the microphone and
                         the chain that cleans and gates it: high-pass, RNNoise,
                         preamp, gate — the gain inside the chain and in front of
                         the gate, so it is what the gate and the meter both
                         measure, every stage always present and bypassed rather
                         than absent so a setting moves mid-call — and a Capture
                         outlives its device, which its own supervisor reopens,
                         falls back to the default, or swaps on SetDevice, none
                         of it visible to the reader inside Read),
                         rnnoise/ (Xiph's RNNoise v0.1.1 vendored as C, the last
                         release whose model ships in-tree; symbols rnn_-prefixed
                         so it coexists with gopus's libopus), device.go
                         (enumeration and the process's one miniaudio context),
                         decode.go (WAV + MP3 -> the device's format),
                         synth.go (the built-in sounds, rendered rather than shipped)
  cpu/                   which logical processors the client is allowed to run on.
                         cpu.go (Topology, Detect, Pin — Pin also moves GOMAXPROCS,
                         the runtime having counted its processors before any of
                         this), cpu_windows.go (GetSystemCpuSetInformation for the
                         efficiency classes, GetLogicalProcessorInformation for the
                         L3 groups, the registry for the vendor, one process-wide
                         mask), cpu_linux.go (the same answers out of sysfs and
                         /proc/cpuinfo, and a per-thread walk of /proc/self/task,
                         that syscall having no process-wide form), cpu_other.go
                         (macOS: no affinity API exists, so no split is reported
                         and the setting is not drawn)
  app/                   app.go, session.go, events.go, navigation.go, messages.go,
                         members.go, typing.go, overlay.go, profile.go, friends.go,
                         groups.go, pins.go, search.go, mentions.go, emoji.go,
                         notify.go, alerts.go, security.go, settings.go,
                         serversettings.go
  ui/                    ui.go, layouts.go, widgets.go, sidebar.go, members.go,
                         message.go, messagelist.go, reactions.go, emoji.go,
                         embed.go, invite.go, search.go,
                         markdown.go, code.go, attachment.go, input.go, modal.go,
                         profile.go, friends.go, group.go, panels.go, notice.go,
                         settings*.go, theme/, titlebar_*.go, filedialog*.go (the OS
                         picker — Fyne's is drawn in the canvas and is not used)
  voice/                 the media half of a call: voice.go (Call, its own event
                         set with an unexported marker, so app's switch is
                         exhaustive; `dial`, which publishes the microphone *in*
                         the join request over one peer connection and keeps the
                         two-connection split as the fallback for a node that
                         cannot; and quietSink, which stops lksdk logging pion's
                         every failed ICE check through this process's logger),
                         publish.go (microphone -> Opus -> one
                         LiveKit track; newMicrophone is split from newPublisher
                         because the track has to exist before the room does;
                         opusTuning is the assertion that lights
                         up FEC/DTX where the binding has them), subscribe.go
                         (RTP -> jitter -> decode -> sink: a reader goroutine per
                         participant, and one playLanes for all of them, woken by
                         the speakers rather than by a ticker so playout cannot
                         drift against the device), jitter.go (the Jitter interface
                         and the adaptive buffer behind it — what is left of
                         mouth-to-ear latency lives here, which is why it is
                         replaceable)
  markdown/              pure parser -> AST, no UI. parser.go is two passes:
                         classify each line into a block, then one byte scanner
                         over each block's whole text
  util/                  pure helpers: sizes, IDs, truncation, ULID timestamps

scripts/                 update-deps.sh — every module *except* Fyne and the
                         versions its go.mod pins, which is why it is not a
                         `go get -u`
```

Where things live that the filename doesn't say. The *why* of each is in that
package's own `CLAUDE.md`; this is the map:

- `app/events.go` — the pump, every handler **and** the refresh queue: queueing
  a rebuild is what most of those handlers do.
- `app/messages.go` — the message area end to end: composer dock, submit,
  slowmode, the window and what mounting a row asks for (`onMessageMounted`),
  load/render, jumps and paging.
- `app/navigation.go` — `buildUI` (the 4-column fill row), both sidebars,
  selection, sidebar context menus, the home/DM view. `#mention` candidates come
  off the channel sidebar's own walk as `@` ones come off the member sidebar's,
  and `OnChannelTapped` following one is why entering a server splits into
  `enterServer` (move both sidebars) and picking a channel: `selectServer` would
  load the first channel on the way past.
- `app/members.go` — lazy author resolution as well as the member sidebar and
  the mention candidates: one `Store.Members` walk feeds all three. It also holds
  what the member menu *does* to a member — nickname, roles, timeout — one route
  under three permissions, filed with the people it is about rather than beside
  the kick and ban confirmations in `notify.go`. And it holds the other list the
  sidebar can draw: `refreshRecipients`, a **group's** own participants, filed
  here rather than in `groups.go` because it is the same sidebar under the same
  rule — one walk feeding the rows and the mention pool together.
- `app/groups.go` — making a group and changing who is in it, which is all that a
  group is beyond a channel: its rows, header, messages and edit card are every
  channel's, and leaving one is closing a conversation. Both cards are picked from
  `Store.Relationships`, Revolt taking friends alone.
- `app/security.go` — the Security section's half: the challenge card every action
  there begins with, and the half-dozen things a ticket unlocks. Filed apart from
  `settings.go` because none of it is a setting — nothing here writes to config,
  and every action is a request Revolt gates behind a proof of identity.
  `client/security.go` is the other half, and holds which routes those are.
- `app/pins.go`, `app/search.go`, `app/mentions.go` — the three surfaces listing
  messages. The card all three are drawn from (`messageCard`, `messagePreview`,
  `messageWhen`) lives in pins: one summary, resolved and counted once, that each
  surface then takes back what its own subject already said — the pins panel
  drops the pinned badge, the inbox drops the mention edge. It carries a `Where`
  for the inbox, whose cards come from as many channels as the account is in. `search.go` additionally holds the answer the filter chips
  narrow — the route takes no filter, so a chip is applied here. `mentions.go` holds both halves of
  mentions — the set (channel → message IDs, what the sidebar counts and the
  rail marks off) and the inbox, those IDs fetched back into messages — because
  a channel gaining one moves both and neither is legible without the other.
- `app/alerts.go` — everything the client does about something the reader did
  not ask to be told: the sound, the taskbar flash, and the catalogue binding a
  sound to the setting that turns it on and the copy it is listed under. One
  table, because playing one, listing them all and pointing one at a file are
  three walks of the same set.
- `app/typing.go` — both halves of the typing indicator (the expiry map and its
  timer; the throttle that announces this account): one feature, one setting
  group, neither legible without the other.
- `ui/members.go` — the member list end to end, its own subsystem: the flat
  model (`NewMemberModel`), the geometry (`memberOffsets`, `visibleRange`,
  `memberListLayout`), the virtualised `MemberList`, the recycled `MemberRow` /
  `MemberSectionRow`, and `memberStatus`, the strip that speaks for the rows
  when there are none. The model is pure and theme-free so `App` can build it
  off the UI thread.
- `ui/messagelist.go` — the message column, the other virtualised list: the
  window's rows as data (`windowRow`, with `continuesGroup` / `dayLabel`
  deriving one from its neighbours), the estimate a row is placed by before it
  is measured, and `MessageList`, which mounts only the rows on screen.
  Variable-height, so unlike the member list it measures in its layout and moves
  the offset as heights settle.
- `ui/widgets.go` — the shared vocabulary: `newText` / `newBoldText` (how every
  `canvas.Text` in the package is built — they flatten the fill, so a gradient
  cannot reach one; a zero size is the theme's own) and `newInitial`, the letter
  a server icon falls back to in both the rail and an invite card; `glyphBox` +
  `glyphLine`, the 20-unit grid every drawn mark shares; tapBase widgets and
  `reportHover`; `Outline`, `hairline` + the two dividers, `Elevate`; `Button` —
  the only text button the client mounts, `ButtonWeight` deciding whether it
  wears the hairline or a tone fill; `Tooltip`, chips, `NewAuthorMark` and the
  three glyphs it answers with (`NewBotMark`, `NewWebhookMark`,
  `NewMasqueradeMark` — one tone, and nil for a person),
  `StatusLine`, the avatar loader, `ObservableScroll` + its indicator,
  `AccentText`, `NewEllipsisText`, `TypingMark` — that last one here because the
  composer's line, a channel row and the member sidebar's status all mount one.
  `imageCacheID` and `imageFrame` are here too: Revolt's file dimensions are
  optional, so the box a picture is drawn in is re-fitted from the decode where
  there were none, and the key a picture is cached under names its rendition as
  well as its file.
- `ui/input.go` — the composer, the mention picker, the slowmode chip, the
  typing line, `ComposerNotice` (what stands where the entry is hidden),
  `JumpBar` and `NewComposerButtonSlot`.
- `ui/code.go` — the fenced code block end to end: the well, the one-pass lexer
  that colours it, and `codeCopy`, the chip in its corner (a coloured block is
  many RichText segments and only a one-segment Label is selectable, so the chip
  is the only way to get the text out). A body carrying one is a column
  (`renderCodeColumn`) rather than a single widget, the card being block-level —
  the only reason `ui/markdown.go` renders *runs* of blocks.
- `ui/layouts.go` — every custom layout, `fitWithin`, `Relayout`.
- `ui/message.go` — also the system line, the day separator and reply previews.
- `ui/callisland.go` — the call card and the layer it floats on, at the top of the
  window over every view, drawn as the settings page's invite card. Two halves in
  one widget — the running call, and the voice channel on screen that is not it,
  each a channel over where it is and led by that place's picture — a server's
  icon, a group's, or the overlapping faces of the people in a group that has
  none (`islandIcon`, `facesLayout`) — because they are one card on screen and
  either can stand alone. The window's own
  layer is the only slot that works: a call outlives leaving the channel, the
  server *and* the view. `stateBar` is the strip along the *live* half's bottom
  edge — the connection as a colour, the word it stands for being what its tooltip
  answers with — and it ends at the rule, the other half being an offer with no
  state to report.
- `ui/friends.go` — the sidebar row *and* the page it opens, which stands where
  the messages do rather than on the modal layer: four sections of people, each
  row carrying what can be done about somebody, is a view a reader stays on. Its
  rows are the settings page's invite island (`newIslandCard`, shared) with what a
  person's row needs on top: the card *is* the primary action — writing to them,
  lifted out of the button row so nothing common sits a square from Block — so it
  answers the pointer, its picture is the way to their profile, and its buttons are
  marks picked from the action's *name* (`ProfileAction`) rather than at the call
  site. Every heading folds, and a folded section's cards are not built at all: a
  section here only ever grows.
- `ui/reactions.go` — the reaction row end to end, the chip and the emoji in it.
  What *adds* one is `ui/emoji.go`, the one picker, which the composer opens too.
- `ui/emoji.go` — that picker: what can be picked (`EmojiChoice`, and `Value` /
  `Token`, the two things one is worth), the island, its header (the hovered
  emoji redrawn large, which is what names a cell), the rail of servers that
  marks and jumps between groups, and the cell. `app/emoji.go` is
  the other half — which emoji are on offer and in what order, a walk of every
  server the account is in that no widget knows. That one walk also feeds the
  composer's `:` autocomplete, so the pop-up and the typed list cannot disagree.
- `ui/invite.go` — the invite card *and* `inviteCodesIn`, the scan that decides
  a message has one. `NewInviteCardFor` is the same card built from an invite
  already in hand — the join dialog's preview, which draws the banner and no
  button, neither being safe on a card a message is holding open.
- `ui/group.go` — the two cards a group is made and grown by, which are one card
  with and without a name field: the same question asked at two moments. A row is
  the friends page's own card at the same sizes, minus that page's buttons — the
  whole row is one answer, so the only thing at its end is the mark saying whether
  it has been given.
- `ui/panels.go` — the island all three message surfaces are drawn on and the
  card one message is drawn as, plus two of the surfaces: pins and the mention
  inbox. `messageIsland` is three surfaces deep (island, well, card) with a
  header, a count line and an empty state that holds a floor open, so a panel
  reporting one sentence is still an island rather than a strip. `islandParts` is
  what a surface adds — blocks under the header, something opposite the count.
  `MessageCard` is what a card is drawn from and `newMessageCard` draws it: a
  heading, a line, and a badge strip naming what the message carries, left off
  entirely where it carries nothing. A card's line is flattened by
  `ui.PreviewText`, which is where a body's markdown and its emoji shortcodes are
  resolved for a summary the controller assembles.
- `ui/search.go` — channel search: the same island, plus what only a question
  being refined needs. `SearchQuery` is the whole of what it asks —
  `SameRequest` is what tells a filter from the two things the route is sent —
  and `searchChip` is the pill both the filter run and the three orders are made
  of.
- `ui/modal.go` — the cards that are not lists: the attachment viewer, the join
  dialog (which previews what a pasted code opens once the typing settles),
  `PromptDialog` (a field per answer and one button), `BanDialog` (a
  confirmation that has to ask for more than a yes, the route taking a reason
  and a window of the member's recent messages), and the `dialogHeader` they all
  wear. `SliderCard` is the one that is not a modal: a range hung beside what it
  belongs to, for a value a `fyne.MenuItem` has no shape for.
- `ui/settings_shell.go` — the surface *both* settings pages are drawn on (the
  layer, the rail, the pane, the vocabulary of rows), embedded by value so a
  section reaches every row shape by promotion. `settings.go` is then the
  client's own sections and the controls that write to config;
  `settings_server.go` is one server's, almost all lists rather than switches
  (`entryRow`) — and the two sections that drill into a row of their own. Roles
  drills once, into an editor whose permission grid is the whole of what Revolt
  defines in the three states it stores each bit in; Channels drills *twice*, a
  channel's overrides being a channel and a role, and draws that same grid
  narrowed to the bits a channel decides. One `permissionScope` serves both, plus
  the two defaults. Two of those lists are also *ordered* by the rows
  in them (`moveButtons`): the roles by seniority, and the channels by the
  arrangement they are drawn in — which for a channel is its category as well as
  its place, a move past the end of one being how it joins the next. `settings_controls.go` holds the controls, none
  of them a Fyne form widget.
- `ui/settings_search.go` — the box at the head of the rail and the page of
  results it puts in the pane. Its index is taken by *building every section
  twice*, once as each mode lists them, the rows answering with their names
  instead of drawing anything (`indexRow`): a section that gains a row gains a
  result, and there is no second list of names to keep in step. What only the
  advanced pass saw is what that mode reveals, and an Advanced hit renders its
  real control rather than a link.
- `ui/theme/overrides.go` — `Apply`, reflection over the two tables against a
  defaults snapshot taken at init.
- `cache/message.go` — entries *and* published slices are immutable, so a
  UI-thread reader holding an older slice is safe. Find/Remove/Replace
  binary-search by ULID.
- `cache/image.go` — memory bounded in *bytes*, plus disk; `Get` stamps mtime so
  `trimDiskCache` evicts by recency. One `ImageCache` is one *folder* under the
  configured root (`ImagesFolder`, `EmojisFolder`) with its own budget and LRU,
  or an afternoon of scrolling attachments evicts the handful of emoji every
  message is drawn with. The settings name **one** budget, so `app.emojiShare`
  divides it rather than the second cache doubling it, and `cacheStats` sums
  both against that one number.

## Conventions

- **Keep revoltgo inside `internal/client`.** A new field the UI needs is a
  field on a `domain` type plus a line in `client/convert.go`; a new lookup is a
  `domain.Store` method.
- **Store methods return resolved values and never touch the network.** A miss
  is `ok=false`, not a fetch.
- **A surface that fetches its own list holds it, asks once, and expires it.**
  Three rules, each of them a request that would otherwise be sent twice.
  `ui.cachedList` in `settings_server.go` is the worked example; anything else
  fetching a list a reader can navigate away from and back to copies its shape
  rather than re-deriving it.
  - **Hold** the answer for as long as the surface is open, keyed to nothing
    else — tapping between two sections must not re-ask, the second answer being
    unable to differ. Drop it when the surface closes: that is the honest bound
    on how long an answer nothing announces can be believed.
  - **Single-flight** it: claim before the request goes out, release when it
    answers, so re-mounting a section whose first answer is still on its way
    *waits* rather than sending another. A plain bool, never `sync.Once` — the
    UI thread is single-threaded already and the point is "not again *yet*", not
    "exactly once": the entry has to be re-askable once it goes stale.
  - **Expire** it on a short TTL, so a list left on screen is not a lie.

  Two guards go with it, answering different questions. *Recording* a late
  answer is guarded on the surface still being the same **opening** (a counter
  bumped by open and close — a page reopened on another server is not the one
  that asked). *Drawing* it is guarded on the section still being **mounted**,
  and refills the body mounted **now** rather than one captured when the request
  went out: under single-flight no second request will come along to fill a card
  the first one missed. Whatever changes the list from inside clears its own
  entry before re-asking, or the next visit draws back what was just removed.
- Background goroutines update the UI through `App.doOnUI(fn, wait)` or
  `ui.DoOnUI(fn)`. `main.go` declares the `fyneDo` migration, so an off-thread
  widget touch is a real data race, not a logged warning.
- **A worker that outlives its session must not paint.** Capture
  `epoch := a.epoch` before leaving the UI thread, check `a.stale(epoch)` on the
  way back.
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
- Colours and sizes come from `ui/theme`, never hardcoded. Don't express one
  size as an offset from an unrelated one — add a named entry, which makes it
  configurable the same day: the settings page reaches the table by reflection.
- A tunable the user should be able to change is a field on `config.Settings`
  read at its use site, not a `const`. Everything else stays a `const` — the
  settings page is not a dumping ground for every number in the client.
- Use the `log` package for diagnostics.
- Keep these files current, each in the one it belongs to: packages and the DAG
  here, data flow and `App` fields in `internal/app/CLAUDE.md`, a revoltgo bug
  or missing route in `internal/client/CLAUDE.md`, a widget or a Fyne constraint
  in `internal/ui/CLAUDE.md`, a limit worked around rather than fixed in
  `docs/known-gaps.md`, a measured cost or a rejected optimisation in
  `docs/performance.md`. Keep them **terse** — the constraint and the reason,
  not the mechanics or the history. A note in the wrong file is paid for by
  every task that does not need it.

### Tests

**Do not add a test unless it was asked for.** Finish the change, then ask at
the end whether one is wanted, in a sentence naming what it would cover and how
it could fail. A change is done when the code is done; an unrequested test is
scope nobody asked for, and one written to have written something is worse than
none — it has to be read, kept current and believed.

When one *is* asked for: test rules and decisions, not rendering. A test earns
its place if it can fail for a reason a person wouldn't spot immediately —
parsing, ordering, caching, conversion, the mention query, a layout that has to
*react* (Relayout, placeBeside, a card that grows). Do **not** assert that a
palette constant is what the palette says, that a widget was built out of the
objects it was just built out of, or that a hand-tuned offset is still that
offset: those only make the next visual change more expensive.

To check appearance, render to a PNG with `fyne.io/fyne/v2/driver/software`,
look at it, and **delete the harness**. A screenshot test left behind asserts
nothing and fails on every deliberate change.

## Build / check

`go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l internal cmd assets`.

Fyne is **patched**, and the patched copy is a repository of its own —
[`rgoclient-fyne`](https://github.com/sentinelb51/rgoclient-fyne) — reached by a
`replace` in `go.mod` and fetched like any other module. Nothing is vendored and
there is no checkout step: a fresh clone builds. The fork keeps the module path
`fyne.io/fyne/v2`, which is why the `replace` needs nothing beside it. Its
`PATCHES.md` lists the ten, and `./update-fyne.sh vX.Y.Z` there carries them
onto a new Fyne by rebasing onto a pristine upstream branch. A bare `go get -u`
floats what that frozen Fyne compiles against, so everything else updates
through `scripts/update-deps.sh`.

The repository is LF throughout and `core.autocrlf=true` converts on checkout,
so on Windows `gofmt -l` names every file it has converted — the diff is the
line endings and nothing else. Read its output as *which* files rather than
*whether any*, and check a named one with `gofmt -d` before believing it. Write
source files with LF regardless: what is committed must stay LF.

## Versioning / CI

Calendar versions `YY.M.N`, UTC, no zero padding (`26.8.1`): year and month,
then a counter restarting each month, taken from the highest `26.8.*` tag so a
deleted tag is never reissued. Three components, so the tag parses as semver.
New tags are never `v`-prefixed — the ones predating this scheme are, and the
counter reads both spellings. CI builds of `main`/PRs use the next number with
`-dev`. There is no version literal in the source: `main.version` and
`main.build` are stamped at link time with `-X`.

Two workflows, each a matrix over `windows-latest`, `ubuntu-latest` and
`macos-latest` (arm64), running `go test ./...` and building `./cmd/rgoclient`
with `CGO_ENABLED=1`. The tests need cgo — `internal/ui` mounts real widgets —
and use Fyne's software driver, so no display is involved. Only Windows takes
`-H windowsgui`; passing it to any other linker is an error, not a no-op.
Ubuntu installs the cgo headers its image lacks (`libgl1-mesa-dev xorg-dev
libwayland-dev libxkbcommon-dev libasound2-dev` — GL/X11 for GLFW and the
clipboard, xkbcommon for the keymap, ALSA for miniaudio, and Wayland because glfw v3.4
compiles *both* display backends unless built with `-tags x11`, as does Fyne's
driver). Nothing is signed or notarised.

Resolving the version is its own job in both, so the three legs stamp one number
rather than each counting the tags. Nothing pushes a tag: `release.yml` resolves
it up front and the release at the end mints it, which keeps a failing tree from
leaving one behind — so tests are a step in the build leg rather than a job
ahead of it, and a platform is compiled once per run instead of twice.

- `build.yml` — push/PR to `main` + manual. One artifact per target. A second
  push cancels the first (`concurrency`, `cancel-in-progress`), which `release`
  deliberately does not do.
- `release.yml` — `workflow_dispatch` computes this month's next version and
  publishes; `softprops/action-gh-release` creates the tag, at
  `target_commitish: github.sha` so it names the tree that was tested rather
  than the default branch's head. The escape hatches take the tag verbatim: a
  tag pushed by hand (`v` optional), and a release drafted in the web UI, which
  fires `release` rather than reliably firing `push` — that path attaches the
  binaries and leaves the notes alone, the body being one someone wrote.

Every job carries a `timeout-minutes`: a deadlocked widget test would otherwise
hold a runner for six hours, billed at 10× on macOS.

Assets are named for their target. The unix ones ship as a `.tar.gz`: a release
asset is served as-is and an artifact is re-zipped, and neither keeps the
execute bit. macOS gets a bare binary rather than an `.app` — see
`docs/known-gaps.md`.

## Known gaps

See `docs/known-gaps.md` — what is simply not built, and what is limited by
revoltgo or Fyne rather than by effort. Check it before concluding something is
missing by accident, and add to it when a limit is found rather than worked
around.

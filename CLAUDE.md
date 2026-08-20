# rgoclient

A Fyne v2.8.0 desktop chat client (Discord-like) for Revolt, in Go 1.26.4. Uses
`github.com/sentinelb51/revoltgo` for the REST API and gateway websocket.

## Sources of truth

Revolt's protocol and backend now ship as **Stoat** (stoat.chat) — same shape,
new name. `revoltgo` is a second-hand reflection of that backend and can diverge
from it, bugs included, so ground a claim about wire behaviour against these
rather than against `revoltgo` or memory:

- `sources/openapi-spec-0.15.1.json` — the Stoat OpenAPI spec, also live at
  https://developers.stoat.chat/api-reference
- https://github.com/stoatchat/stoatchat — the Rust backend (crate `delta`),
  authoritative for what a route or event actually does

## Architecture

`internal/client` is the **only** package that imports `revoltgo`. It converts
wire types into `internal/domain` values on the way in; everything above is
written against the domain. The dependency graph is a strict DAG:

```
domain, markdown, config       no internal dependencies
audio                          no internal dependencies    (+ oto, go-mp3)
util       -> config
cache      -> domain
client     -> cache, config, domain          (+ revoltgo)
ui         -> cache, config, domain, markdown, util
app        -> audio, cache, client, config, domain, ui, util
```

`config` is a leaf so everything above can read a setting. `cache` and `audio`
deliberately do *not* import it — budgets, directories, volumes and file paths
arrive as arguments, so either can be built in a test with no settings file
anywhere. `ui` does not import `audio` either: the composer names the *kind* of
keystroke (`ui.Keystroke`) and `app` decides what it sounds like.

The seam is not tidiness: `revoltgo.State`'s caches are unexported and
`newState()` is package-private, so nothing holding a `*revoltgo.Session` can be
built in a test. `domain.Store` can — `ui/store_test.go` has a map-backed
`fakeStore`.

No globals. The `*app.App` controller owns the client, caches and widgets and
passes what widgets need through `ui.Deps` (`Store`, `Images`, `Texts`,
`Actions`, `Tooltip`). `App.deps()` is the only producer, so **every field is always set** —
widgets do not nil-check them. The only package-level mutable state is pure
measurement memoisation (`ui.lineHeights`, `ui.spaceWidths`), UI-thread only.

### The client's contract

- **`Client.Store()`** — reads, safe from any goroutine, never the network. A
  miss reports `ok=false`. Returns resolved values: a `domain.Member` already
  carries nickname, per-server avatar, role colour, presence, bot mark and the
  hoisted role it is filed under. Safe off-thread is not cheap — `Members`
  resolves all of that per member and sorts, so it belongs on a worker.
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

## Working in this repo

This file is the **core**: the DAG, the client's contract, the layout, the
conventions and the build. The rest is filed beside the code it is about, so a
change in `markdown/` does not pay for the Fyne footguns:

- `internal/app/CLAUDE.md` — the data flow, items 1-28: what happens in what
  order and why each step is where it is.
- `internal/client/CLAUDE.md` — the revoltgo notes: every bug, missing field
  and route that has to be sent by hand.
- `internal/ui/CLAUDE.md` — the Fyne footguns.
- `docs/known-gaps.md` — what is not built, and what revoltgo or Fyne prevents
  rather than effort.
- `docs/performance.md` — what a frame costs, which levers are reachable and
  which need a fork of Fyne. Read it before optimising anything.

A directory's `CLAUDE.md` arrives on its own when a file in that directory is
touched. **Read the others by hand when a change crosses a boundary** — a new
field the UI needs is `client/convert.go`, a `domain` type and a widget, which
is three of these.

### Context discipline

The tree is ~1.2 MB of Go and the largest files are 50 KB each, so what gets
read *is* the budget.

- **`sources/openapi-spec-0.15.1.json` is 374 KB — grep it, never read it.**
  One whole read is most of a context window.
- **Use the `rgo-explore` agent to locate code** (`.claude/agents/`), rather
  than reading files to find it. It searches in its own context and reports
  `file:line` and the shape, so a twenty-file sweep costs a paragraph here.
  Worth the round trip for anything spanning more than about three files.
- **Then read the file before editing it.** A summary carries what a function
  does and drops the constraint it is shaped by. The agent narrows what has to
  be read — it does not stand in for reading it, and nothing is edited off a
  report alone.
- Grep with context lines beats a whole-file read when the question is about
  one identifier.

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
  client/                client.go, auth.go, convert.go, store.go, events.go,
                         actions.go
  cache/                 cache.go (LRU + TextCache), message.go, image.go
  audio/                 audio.go (the engine, one device, a voice pool per sound),
                         decode.go (WAV + MP3 -> the device's format),
                         synth.go (the built-in sounds, rendered rather than shipped)
  app/                   app.go, session.go, events.go, navigation.go, messages.go,
                         members.go, typing.go, overlay.go, profile.go, friends.go,
                         pins.go, search.go, emoji.go, notify.go, alerts.go,
                         settings.go
  ui/                    ui.go, layouts.go, widgets.go, sidebar.go, members.go,
                         message.go, reactions.go, emoji.go, embed.go, invite.go,
                         markdown.go, code.go, attachment.go, input.go, modal.go,
                         profile.go, friends.go, panels.go, notice.go, settings*.go,
                         theme/, titlebar_*.go, filedialog*.go (the OS picker —
                         Fyne's is drawn in the canvas and is not used)
  markdown/              pure parser -> AST, no UI. parser.go is two passes:
                         classify each line into a block, then one byte scanner
                         over each block's whole text
  util/                  pure helpers: sizes, IDs, truncation, ULID timestamps

scripts/                 update-deps.sh — every module *except* Fyne and the
                         versions its go.mod pins, which is why it is not a
                         `go get -u`
```

Where things live that the filename doesn't tell you:

- `app/events.go` is the pump, every handler **and** the refresh queue: queueing
  a rebuild is what most of those handlers do, so hiding the queue elsewhere
  would put it half a file from the thing it is about.
- `app/messages.go` is the message area end to end — composer dock, submit,
  slowmode, widget construction, load/render and the mounted window.
- `app/navigation.go` holds `buildUI` (the 4-column fill row), both sidebars,
  selection, sidebar context menus and the home/DM view. The `#mention`
  candidates come off the channel sidebar's own walk, as the `@` ones come off
  the member sidebar's, and `OnChannelTapped` — following one — is why entering a
  server is split into `enterServer` (move both sidebars) and picking a channel:
  `selectServer` would load the first channel on the way past.
- `app/members.go` holds lazy author resolution as well as the member sidebar and
  the mention candidates, since one `Store.Members` walk feeds all three.
- `app/pins.go` and `app/search.go` are the two message panels, and the summary a
  row is drawn from (`messageEntry`, `messagePreview`, `messageWhen`) lives in the
  first: a pin list and a search result are the same row reached two ways.
- `app/alerts.go` is everything the client does about something the reader did
  not ask to be told — the sound and the taskbar flash — and the catalogue
  binding a sound to the setting that turns it on and the copy it is listed
  under. One table, because playing one, listing them all and pointing one at a
  file are three walks of the same set.
- `app/typing.go` holds both halves of the typing indicator — the expiry map and
  its timer, and the throttle that announces this account — one feature with one
  setting group, neither legible without the other.
- `ui/members.go` is the member list end to end, its own subsystem: the flat
  model (`NewMemberModel`), the geometry (`memberOffsets`, `visibleRange`,
  `memberListLayout`), the virtualised `MemberList`, and the recycled `MemberRow`
  / `MemberSectionRow`. The model is pure and theme-free so `App` can build it
  off the UI thread. `memberStatus` — the strip above the list — is here too,
  being what speaks for the rows when there are none.
- `ui/widgets.go` is the shared vocabulary: `newText` / `newBoldText` (how every
  `canvas.Text` in the package is built — they flatten the fill, so a gradient
  cannot reach one; a zero size is the theme's own) and `newInitial`, the letter a
  server icon falls back to in both the rail and an invite card; `glyphBox` +
  `glyphLine`, the 20-unit grid every drawn mark shares; tapBase widgets and
  `reportHover`; `Outline`, `hairline` + the two dividers, `Elevate`; `Button` —
  the only text button the client mounts, `ButtonWeight` deciding whether it wears
  the hairline or a tone fill; Tooltip,
  chips, `NewBotMark`, `StatusLine`, the avatar loader, `ObservableScroll` + its
  indicator, `AccentText`, `NewEllipsisText`, `TypingMark` — that last one here
  rather than beside a caller because the composer's line, a channel row and the
  member sidebar's status all mount one.
- `ui/input.go` holds the composer, the mention picker, the slowmode chip, the
  typing line, `ComposerNotice` — what stands where the entry is hidden — and
  `JumpBar`. The chip and the line are one row under one set of
  rules: a pill of their own (`newDockBadgeSurface`) sized by what it holds rather
  than by the row, accepting no pointer event so the messages underneath stay
  hoverable, an `OnResize` hook so the row can be re-laid out, and a change guard
  before any repaint. The bar is the opposite of that pill on every count — it
  spans the card's width, answers a tap, and says where the *column* is standing
  rather than something about the channel — but it hangs in the same stack and
  reports its own appearance the same way. `NewComposerButtonSlot` is beside them
  — it bottom-anchors the emoji button against the growing entry and lifts it by
  the entry's own `InnerPadding`, so it centres on the last *line* rather than on
  the entry's box.
- `ui/code.go` is the fenced code block end to end: the well it is drawn in, the
  one-pass lexer that colours it, and `codeCopy`, the chip in its corner — a
  coloured block is many RichText segments and only a one-segment Label is
  selectable, so the chip is the only way to get the text out. A body carrying one is a column
  (`renderCodeColumn`) rather than a single widget, the card being block-level —
  which is the only reason `ui/markdown.go` renders *runs* of blocks.
- `ui/layouts.go` holds every custom layout, `fitWithin` and `Relayout`.
- `ui/message.go` also owns the system line, the day separator, reply previews and
  `NewChannelNote` — the strip under the header saying what the client cannot do
  in the channel, which only a voice channel draws.
- `ui/reactions.go` is the reaction row end to end — the chip and the emoji inside
  it — neither being anything on its own and both answering the same question
  about what the server sent. What *adds* one is `ui/emoji.go`, the one picker,
  which the composer opens too.
- `ui/emoji.go` is that picker: what can be picked (`EmojiChoice`, and `Value` /
  `Token`, the two things one is worth), the pop-up, and the cell. `app/emoji.go`
  is the other half — which emoji are on offer and in what order, that being a
  walk of every server the account is in and no widget knows them.
- `ui/invite.go` holds the invite card *and* `inviteCodesIn`, the scan that
  decides a message has one — the card is mounted from what that scan finds.
- `ui/panels.go` holds both message panels — the pins list and channel search —
  because they are one card with one row (`MessageEntry`, `messageRow`) and differ
  only in what fills it. `ui/modal.go` holds the cards that are not lists: the
  attachment viewer, the join dialog, `PromptDialog` (a field per answer and one
  button — a name is all Revolt takes to create a server, where changing a
  username is that name *and* the account password) and the `dialogHeader` all of
  them wear.
- `ui/settings_controls.go` holds the controls, none of them a Fyne form widget.
- `ui/theme/overrides.go` holds `Apply` — reflection over the two tables, against
  a defaults snapshot taken at init.
- `cache/message.go`: entries *and* published slices are immutable, so a UI-thread
  reader holding an older slice is safe. Find/Remove/Replace binary-search by ULID.
- `cache/image.go`: memory bounded in *bytes*, plus disk. `Get` stamps mtime, so
  `trimDiskCache` evicts by recency. One `ImageCache` is one *folder* under the
  configured root (`ImagesFolder`, `EmojisFolder`), with its own budget and LRU —
  otherwise an afternoon of scrolling attachments evicts the handful of emoji
  every message is drawn with. The settings name **one** budget, so
  `app.emojiShare` divides it rather than the second cache doubling it, and
  `cacheStats` sums both against that one number.

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
- Keep these files current, each in the one it belongs to: packages and the DAG
  here, data flow and `App` fields in `internal/app/CLAUDE.md`, a revoltgo bug
  or missing route in `internal/client/CLAUDE.md`, a widget or a Fyne
  constraint in `internal/ui/CLAUDE.md`, a limit worked around rather than
  fixed in `docs/known-gaps.md`, a measured cost or a rejected optimisation in
  `docs/performance.md`. Keep them *terse* — the constraint and the
  reason, not the mechanics or the history. A note in the wrong file is paid for
  by every task that does not need it.

### Tests

**Do not add a test unless it was asked for.** Finish the change, then ask at the
end whether one is wanted, in a sentence naming what it would cover and how it
could fail. A change is done when the code is done; an unrequested test is
scope nobody asked for, and one written to have written something is worse than
none — it has to be read, kept current and believed.

When one *is* asked for: test rules and decisions, not rendering. A test earns
its place if it can fail for a reason a person wouldn't spot immediately:
parsing, ordering, caching, conversion, the mention query, a layout that has to
*react* (Relayout, placeBeside, a card that grows). Do **not** assert that a
palette constant is what the palette says, that a widget was built out of the
objects it was just built out of, or that a hand-tuned offset is still that
offset — those only make the next visual change more expensive.

To check appearance, render to a PNG with `fyne.io/fyne/v2/driver/software`,
look at it, and **delete the harness**. A screenshot test left behind asserts
nothing and fails on every deliberate change.

## Build / check

`go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l internal cmd assets`.

Fyne is **patched**, and the patched copy is a repository of its own —
[`rgoclient-fyne`](https://github.com/sentinelb51/rgoclient-fyne) — reached by a
`replace` in `go.mod` and fetched like any other module. Nothing is vendored
here and there is no checkout step: a fresh clone builds. The fork keeps the
module path `fyne.io/fyne/v2`, which is why the `replace` needs nothing beside
it. Its `PATCHES.md` is the list of four, and `./update-fyne.sh vX.Y.Z` there
carries them onto a new Fyne by rebasing them onto a pristine upstream branch.
A bare `go get -u` floats what that frozen Fyne compiles against, so everything
else updates through `scripts/update-deps.sh`.

The repository is LF throughout and `core.autocrlf=true` converts on checkout, so
on Windows `gofmt -l` names every file it has converted — the diff is the line
endings and nothing else. Read its output as *which* files rather than *whether
any*, and check a named one with `gofmt -d` before believing it. Write source
files with LF regardless: what is committed must stay LF.

## Versioning / CI

Calendar versions: `YY.M.N`, UTC, no zero padding (`26.8.1`) — year and month,
then a counter that restarts each month, taken from the highest `v26.8.*` tag so
a deleted tag is never reissued. Three components, so the tag parses as semver.
CI builds of `main`/PRs use the next number with `-dev`. There is no version
literal in the source — `main.version` and `main.build` are stamped at link time
with `-X`.

Two workflows, each a matrix over `windows-latest`, `ubuntu-latest` and
`macos-latest` (arm64), running `go test ./...` and building `./cmd/rgoclient`
with `CGO_ENABLED=1`. The tests need cgo — `internal/ui` mounts real widgets —
and they use Fyne's software driver, so no display is involved. Only Windows
takes `-H windowsgui`; passing it to any other linker is an error, not a no-op.
Ubuntu installs the cgo headers its image lacks (`libgl1-mesa-dev xorg-dev
libxkbcommon-dev libasound2-dev` — GL/X11 for GLFW and the clipboard, xkbcommon
for the keymap, ALSA for oto). Nothing is signed or notarised.

Resolving the version is its own job in both, so the three legs stamp one number
rather than each counting the tags — and in `release.yml`, so they don't race to
push the tag. Tests there are a job of their own *ahead* of it, so a failing tree
can't leave a tag behind.

- `build.yml` — push/PR to `main` + manual. One artifact per target.
- `release.yml` — `workflow_dispatch` computes this month's next version, pushes
  the tag and publishes. The escape hatches take the tag verbatim: a tag pushed
  by hand (`v` optional), and a release drafted in the web UI, which fires
  `release` rather than reliably firing `push`. That last path attaches the
  binaries and leaves the notes alone — the body is one someone wrote.

Assets are named for their target. The unix ones ship as a `.tar.gz`: a release
asset is served as-is and an artifact is re-zipped, and neither keeps the execute
bit. macOS gets a bare binary rather than an `.app` — see `docs/known-gaps.md`.

## Known gaps

See `docs/known-gaps.md` — what is simply not built, and what is limited by
revoltgo or Fyne rather than by effort. Check it before concluding something is
missing by accident, and add to it when a limit is found rather than worked
around.

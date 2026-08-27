# Performance and responsiveness

What the client's frame actually costs, which levers exist, and which of them
need a fork. Linked from the root `CLAUDE.md`. Companion to `known-gaps.md`:
that file says what is *missing*, this one says what is *slow* and why.

Line numbers are `fyne.io/fyne/v2@v2.8.0` and will drift. Re-check them against
the module cache copy (`go list -m -f '{{.Dir}}' fyne.io/fyne/v2`) before acting
on one — the shape of the claim survives a bump, the line does not.

Fyne is **patched** — [`rgoclient-fyne`](https://github.com/sentinelb51/rgoclient-fyne),
`PATCHES.md` there. Most of the levers below are ours now rather than
upstream's, and are marked where they appear.

## How Fyne draws

**One goroutine does everything.** `internal/driver/glfw/loop.go` `runGL` is the
whole client. Upstream it is a `select` over the shutdown channel, the `fyne.Do`
func queue and a ticker; **patched**, it drains the func queue, runs a frame if
the deadline has passed — `pollEvents` → mouse/resize fixups → `TickAnimations`
→ `drawSingleFrame` — and then blocks in `glfw.WaitEventsTimeout` until the next
one. There is no separate draw thread; `window.RunWithContext` (`window.go`) is
`MakeContextCurrent(); f(); DetachCurrentContext()` on the caller's goroutine.

The deadline comes from `fyne.FrameRate()` and is re-read each frame, which is
what `config.Performance.FrameRate` (default 120) sets through
`app.applyPacing`. Upstream it is the literal `time.Second / 60`. While another
window has the focus, `config.Performance.BackgroundFrameRate` (default 10,
floor 1) stands in for it — `app.applyFrameRate`, re-applied by the same
foreground hooks that gate notification sounds (`app.startAlerts`). Input is
still noticed at once (the loop waits on the OS queue, not the frame clock),
and the loop resets its deadline whenever the rate moves, so regaining focus
draws immediately. What the low rate actually paces is the little that draws
unwatched: the typing-indicator sweep — the client's one repeat-forever
animation — and repaints from gateway events.

**The rate is a ceiling, not a rate.** `drawSingleFrame` draws a window only if
`decideRepaint(visible, frame.ready(), canvas.CheckDirtyAndClear)`, so the drawn
frame count at rest is 0. Waiting on the OS queue rather than polling it also
takes the rate out of how quickly input is *noticed*: the wait returns on the
event, and only the drawing it asks for is paced. At rest the loop sleeps at the
patch's `idleWait` (100ms) instead of at the frame rate — at `FrameRate` 600,
188ms of CPU per 10s idle before that patch and 31ms after. Raising the rate
raises the ceiling on animation and scroll smoothness and costs nothing at
rest.

**Dirty says whether; damage says where.** Upstream, `Canvas.dirty` is one bool
and any `Refresh()` anywhere clears the framebuffer and redraws every visible
object. **Patched** (the eighth, `PATCHES.md` there): each frame diffs every
mounted object against the rect it last painted at
(`internal/driver/common/damage.go`), keeps the previous frame as a texture
(`internal/painter/gl/snapshot.go`), and repaints only the damaged rects — at
most four scissored passes, each under a root clip so the painter's rect test
culls the draw calls too. A caret blink is a restore quad plus a handful of
draws instead of the window — and is off by default anyway
(`config.Behaviour.CursorBlink`, the eleventh patch), so a focused composer
nobody is typing in asks for no frames at all. Damage past 80% coverage promotes to the old full
repaint, which is what a scroll frame is; `fyne.SetPartialRepaint(false)` is
the escape hatch and the measuring baseline, and
`config.Performance.PartialRepaint` is its setting. The diff covers moves, hides,
appearances and removals — no exported call site reports a rect, and none needs
to.

**A repaint walks every mounted object, and draws the ones in the damage.** The
walk (`internal/driver/util.go:137`) prunes only on `!Visible()`; there is no
rect prune, so a scrolled-away subtree is still descended — once for the damage
diff, once per damage rect painted. Culling happens one level down, in
`painter.Paint` (`internal/painter/gl/painter.go`), which rect-tests the object
against the current clip — now rooted at the damage rect — and returns before
`drawObject`. So a dirty frame is **O(mounted) in traversal and O(damage) in
draw calls**. Mounting fewer widgets buys traversal; it does not buy fill rate,
which was already bounded by the viewport.

**Text is shaped once, not per frame.** Two independent caches:
`internal/cache/text.go` memoises measurement globally on
`(text, size, style, font source)`, and `cache.GetTexture(obj)`
(`internal/cache/texture_common.go:29`) holds each text object's rasterised glyph
run as a GL texture, re-uploaded only when that object is refreshed. Scrolling
moves an offset and re-shapes nothing. Both expire at `cache.ValidDuration`,
**1 minute** of not being walked (`internal/cache/base.go:11`), so a message
scrolled away and back past that pays to re-raster.

**Parsing a font is cached too, now.** `painter.loadMeasureFont` is **patched** to
memoise per resource. Upstream it parses on every miss of a `{style, scope}` key,
and the client mints a scope per entry — see `docs/known-gaps.md` on
`ui.WithCaret`.

**Vsync blocks the loop, and is ours to set.** `SwapBuffers` runs inside
`repaintWindow` on the loop goroutine, so a vsync wait stalls `pollEvents` and
the `fyne.Do` queue too, not just drawing. Upstream calls `glfw.SwapInterval(0)`
**only under Wayland** and leaves the WGL default — interval 1 — everywhere else.
**Patched**, `repaintWindow` applies `fyne.VSync()` just before `SwapBuffers`,
that being the one moment the window's GL context is current on our goroutine.
`config.Performance.VSync` is the setting; Wayland is still left to the
compositor.

**The present gate is real on Wayland and Windows.** `presentGate` is
`wl_surface.frame` callbacks on Wayland and, **patched**, the adapter's vertical
blank on Windows (`present_windows.go` — `D3DKMTWaitForVerticalBlankEvent`;
DWM keeps only the newest buffer per blank, so presents past the display rate
were drawn and discarded). Everywhere else it is `noGate{}`, always ready.

## What the client already does

- **A virtualised message column.** `ui/messagelist.go`: the window
  `app/messages.go` keeps — bounded at `mountedCap()` / `renderedCap()` — is
  data, and only the rows the viewport touches plus `MessageOverscan` either side
  have widgets. The content's height is a field, so the scroller's per-offset
  `MinSize` and the driver's per-frame walk both stop scaling with the window.
  Rows are variable-height, so a row is placed by an estimate until its widget
  has been laid out at the column's width; measuring happens in the layout, and
  a height moving above the viewport shifts the offset with it. Measured with
  `internal/app/virtual_bench_test.go` (1400×900 window, 250-message cache,
  software driver, Ryzen 9 9950X3D), flat column → virtual:

  | | window of 50 | window of 250 |
  |---|---|---|
  | objects in the tree | 1,921 → 952 | 7,721 → 1,388 |
  | frame min-size walk | 345 µs → 88 µs | 1.84 ms → 136 µs |
  | wheel tick, up and back | 235 µs → 54 µs | 1.47 ms → 103 µs |
  | live heap of the mount | 342 KiB → 137 KiB | 1.67 MB → 355 KiB |
  | build + mount | 0.95 ms → 0.47 ms | 4.5 ms → 1.29 ms |

  A page of history prepended at the top went from 2.19 ms to 234 µs (it used to
  `Scroll.Refresh`, re-wrapping every mounted body) and a live message landing
  from 1.0 ms to 123 µs. What it costs: a row scrolled out past the overscan is
  rebuilt on its way back — the rebuild a trimmed row always paid, at a tighter
  margin — and the scroll indicator stands on estimates until the rows it covers
  have been seen.
- **Measurement memoisation of our own layouts**: `ui.lineHeights`
  (`ui/input.go:229`), `ui.spaceWidths` (`ui/markdown.go:1030`). UI-thread only,
  which is why they can be plain maps.
- **Virtualised, recycled member list** — `ui/members.go` (`NewMemberModel`,
  `visibleRange`, recycled `MemberRow`). Fixed-height rows, so nothing is
  measured; the message column shares the mounting and not the recycling, a
  message row being too many shapes to redraw in place.
- **Off-thread preparation.** `Store.Members` resolves and sorts on a worker;
  the model is pure and theme-free so it can be built off the UI thread.
- **Presence does not walk.** A presence event is the one a busy server raises
  continuously, and it moves a member between sections without moving anything
  they are ordered by — so `refreshPresence` (`app/events.go`) copies the
  membership the last walk resolved, re-resolves only who moved
  (`App.refreshMemberPresence`) and rebuilds the model from that. No walk, no
  fold, no sort, and the mention candidates are left alone. Over 20,000 members:
  **7.0 ms / 13.7 MB / ~70,000 allocations → 0.94 ms / 4.5 MB / 10**, at up to
  four bursts a second. The copy is what keeps it off the UI thread with no lock
  — `App.memberCache` is published and never written into — and `memberWorking`
  is the single flight that stops two of them starting from the same source.
- **The sort moves keys, not members.** `client.sortedByName` sorts a
  `{folded name, ID, index}` key and permutes once; sorting the entries
  themselves put a 200-byte resolved member into every swap. Over 20,000
  members, **9.4 ms → 6.2 ms**.
- **The model points at members rather than carrying them.** `ui.MemberEntry`
  holds a `*domain.Member` into the slice it was built from, which is published
  and immutable. Over 20,000 members, `NewMemberModel` went **831 µs / 4.33 MB →
  202 µs / 0.81 MB**.
- **The model is counted, not bucketed.** `memberModel` files each member under a
  bucket index and counts it, then writes straight into an exactly-sized slice. A
  `domain.Member` is ~128 bytes, so materialising a slice per section and copying
  it out again cost more than the walk did. Measured over 3000 members with three
  hoisted roles: **188 µs / 1.72 MB / 41 allocations → 71 µs / 0.53 MB / 11**.
  This runs on every presence change, which is what makes it worth the second
  pass.
- **The heap trims itself after a membership fetch.** Pulling a whole
  membership (a six-figure server decodes through a few times its resting size)
  leaves the peak mapped on the process for the session — the runtime's
  scavenger returns it too slowly to matter. `App.scheduleHeapTrim`
  (`app/members.go`) runs `debug.FreeOSMemory` on a timer 15 s after the last
  fetch settles, debounced so clicking through servers trims once. Measured on
  a ~114k-member server: private bytes **537 → 443 MB**, working set
  **406 → 313 MB**, with `HeapIdle` fully released. The live set (~200 MB with
  that server fetched) is the whole-membership design itself, which is
  deliberate — see `internal/app/CLAUDE.md` item 3.
- **One trip to the UI thread per burst, not per event.** `app.pumpEvents` takes
  whatever the gateway has already produced (`maxEventBatch`, 64 — the stream's
  own depth) and dispatches it in arrival order inside a single `doOnUI`. Every
  hop costs a queue entry and a wake of the driver's loop, and Revolt's traffic
  is bursts by construction: a rank reorder is an event per role, presence on a
  large server is continuous. The handlers behind them already coalesced their
  *work* through `queueRefresh`; this is the trip itself. The hop **waits**,
  which is what lets one buffer be reused and is also the backpressure — the pump
  gathering while the UI thread works is what makes the next batch bigger rather
  than the queue longer. `app.pumpCall` is the same shape for a call's events and
  deliberately does *not* wait: `voice.Call.emit` drops rather than blocks, so a
  pump held against a busy frame would lose speaking transitions.
- **A sidebar rebuild ranks the account's roles once, not once per channel.**
  `Store.Permissions(channelID)` re-reads the account, the server and the
  membership out of `State` and re-ranks the member's roles — an allocation and a
  sort — for an answer whose only per-channel part is two overwrite lookups. So
  `refreshChannelList` takes `Store.ServerChannelPermissions(serverID)`, one pass
  for the whole server, and `newChannelRow` reads the view bit out of it. Nothing
  is cached: it is one walk, made where a server's worth of the answer is wanted
  at once. Same shape in `App.refreshVoiceMarksAll`, which asked
  `Store.VoiceParticipants` — a resolve and a sort of the whole call — once per
  row of that call.
- **Nothing is resolved twice on a walk.** `Store.Relationships` carries the
  relationship it filtered on into the resolution rather than asking again. With
  the friends dialog down, `awaitingAnswer` does not call it at all:
  `Store.HasIncomingRequest` walks the same accounts resolving nobody, ordering
  nothing and taking the client's lock once for the walk rather than once per
  account (`Client.knownRelations`). Both run off `flushAuthors`, once per batch
  of resolved authors, on the UI thread — the shape worth looking for is a guard
  that resolves, then resolves again, and it was one level up from where it had
  already been fixed.

<!-- B2-14: the typing indicator's repaint cost belongs here as one more bullet,
     once it has been measured. Nothing else in this file is waiting on it. -->

## Reachable without touching Fyne

Ranked by return.

1. **Optimistic local echo.** Paint the sent message immediately, reconcile on
   the server ack. Pure application logic and the largest perceived win. The
   design constraint is ours: `cache/message.go` keeps entries ULID-sorted and
   `Find`/`Remove`/`Replace` binary-search on that, so a provisional ID has to
   sort where the real one will land — otherwise the ack is an insert plus a
   delete and the row visibly jumps. Revolt accepts a client-supplied `nonce`,
   which is the reconciliation key.
2. **`Refresh()` discipline.** Since the damage patch a call costs its own rect
   rather than the window, but the rect is the *refreshed object's*: refreshing
   a container to update one label inside it damages the container. Refresh the
   narrowest object that changed, and prefer *not dirtying at all* when the
   change is invisible (the change guards already used by the slowmode chip and
   the typing line are the pattern). The one already found: `MessageInput`
   refreshed after `widget.Entry`'s typing methods, which end in a refresh of
   their own — two re-wraps and two dirty windows per keystroke.
3. **`FYNE_CACHE`.** `internal/cache/base.go:22` reads it as a `time.Duration` in
   `init()` and it is the only Fyne env knob that touches performance. Raising it
   past a minute keeps glyph textures alive across a scroll-away-and-back at the
   cost of VRAM; lowering it trades redraws for memory. Settable from
   `main.go` with `os.Setenv` before the first widget, so it can be a real
   setting. Measure before shipping one — this is a guess until it is not.

## Taken by patching Fyne

[`rgoclient-fyne`](https://github.com/sentinelb51/rgoclient-fyne) is v2.8.0 with
twelve patches; its `PATCHES.md` is the list and `update-fyne.sh` carries them
onto a new version. The frame rate and vsync landed
together on purpose — raising the ceiling while the driver still blocks in
`SwapBuffers` changes nothing — and both are settings under Performance. The
third is the font-parse cache, which is a leak fixed rather than a lever. The
fourth gates presents on the vertical blank on Windows. The
fifth replaced the driver's poll loop with a wait on the OS event queue, which
is what makes the frame rate a ceiling on drawing rather than on noticing input.
The eighth is damage-region repaint — the "How Fyne draws" section above
describes the world with it in. The ninth is `RGO_FRAMETIME=1`, a per-frame
cost split logged every 120 painted frames (see Measuring). The tenth skips
what a frame repeats: GL state that has not moved (program, blend, buffer,
texture bindings, attribute pointers — each was a cgo call per object), the
per-draw coordinate slices (now painter-owned scratch), the cache-entry
allocation `EnsureMinSize` made per mounted object per dirty frame (~57k/s
during a scroll here), and the expired-texture sweep that ranged every cached
texture every frame for entries that expire at minute granularity (now once a
second). Measured with patch 9 on a message-column scroll: draw phase
**~1.74 → ~1.43 µs of CPU per drawn object**.

The **twelfth** is the fifth's own bill, come due: a loop parked in the OS event
queue is not woken by a channel send, so patch 5 made every `DoOnUI` post an
empty event — a cgo `PostMessage` that also ends the wait, so *n* funcs queued
before the loop reached any of them cost *n* syscalls and *n* whole loop passes
to run work one pass would have drained. It is one post per *drain* now, claimed
by a compare-and-swap. Measured on a 200,000-func flood against an idle loop,
**1.39 s wall / 1.79 s CPU → 99 ms / 190 ms** (8.9 µs → 0.94 µs of CPU per
queued func); on 3,000 bursts of 32 a millisecond apart, CPU **1.31 s → 0.20 s**.
Both are synthetic: the client at rest and through a login queues nowhere near
enough to show it — measured against this account, idle CPU over 90 s and startup
CPU over 25 s are both inside their own run-to-run spread. It raises a ceiling
rather than lowering a bill, which is why the *client's* own coalescing (the
event pump, below) is what actually pays here.

Where the claim is released is the whole patch and is worth reading `PATCHES.md`
for before touching it: released a few lines too early, 3% of enqueues stalled
for the full 100 ms `idleWait`, and nothing else says so.

The sixth and seventh are **work skipped**, both found by profiling the message
column and both in exported code rather than under `internal/`:

- **`widget.RichText.Resize` re-wrapped on a height.** Row bounds are wrapped
  against the width alone, but any change of size called `Refresh` — which
  re-runs `updateRowBounds` *and* dirties the canvas. This column resizes every
  mounted row twice per settle, at the estimate it is placed by and then at the
  height it measured, so every body on screen was wrapped twice for one settle.
  Channel open 635µs/324KB → **499µs/258KB**; a prepended page 334µs/133KB →
  **270µs/108KB**.
- **`canvas.Image.MinSize` re-parsed the file.** It refreshed while
  `i.Image == nil`, which for an SVG resource is until something rasterises it —
  so every `MinSize` re-ran the XML parse that finds the aspect. The driver asks
  every object on every dirty frame. Frame walk at 50 mounted rows
  179µs/61.3KB → **111µs/8.4KB**; at 250, 252µs/66.1KB → **187µs/13.2KB**.

What that costs: a Fyne bump is now a rebase in the fork rather than a bare
`go get`, and anything read out of `internal/` here has to be read out of the
fork instead.

## What a frame measured as (2026-08)

`RGO_FRAMETIME=1` on a 1216×639 window, 540 fps cap, vsync on, partial repaint
on, Ryzen 9 9950X3D, scrolling the message column flat out at ~60 painted
frames a second, after the tenth patch:

| phase | avg | what it is |
|---|---|---|
| prep | ~420 µs | `EnsureMinSize` (a `MinSize()` per mounted object) + the refresh-queue drain |
| damage | ~180 µs | the diff walk |
| draw | ~400 µs | GL calls for ~270 drawn objects, ~1.43 µs each |
| swap | ~500 µs | present, paced by vsync |

Scroll frames never promote to a full repaint at this window size — the message
column is under the 80% coverage threshold — and the worst draw (~20 ms spikes)
is glyph-run texture upload for rows newly scrolled into the overscan, not draw
calls. Idle with a quiet channel on screen is ~0.4% of one core, and that is
the gateway plus the 100 ms `idleWait` wakeups, not painting. What remains, in
order of size:

- **The prep walk is the frame's biggest CPU phase** and is structural:
  `obj.MinSize()` per mounted object goes through a renderer cache lookup each.
  Skipping the walk on frames whose refresh queue drained nothing was
  considered and rejected — a min-size change without a `Refresh` is legal API,
  and the walk is upstream's catch-all for it.
- **`logError` was a false lever.** `build.Mode` is a build-tag constant, so in
  a normal build `logGLError` compiles to an empty function and the calls cost
  nothing. The earlier claim here that gating it was "the cheap third" was
  wrong.
- **Batching/instancing** — one VBO, every rect in one instanced draw — is the
  "upgrade OpenGL" that would pay next (instancing needs 3.3+). A painter
  rewrite; at ~400 µs of draw per scroll frame it is not where the frame goes,
  so it stays unbuilt.
- **Parsing markdown is not a lever.** `markdown.Parse` measured (2026-08, same
  machine): ~390 ns for a typical one-line message, ~37 µs for a 6.6 KB
  many-block body, 0.44 ms worst case for 2000 bytes of pathological `[[[[…` —
  noise against the widget build each parse feeds. A parsed-AST cache per
  message was considered and rejected: it buys back microseconds per remount
  and costs held ASTs plus invalidation on every edit.

## Still needs more than a patch

- **A D3D or Vulkan painter.** `gl.Painter` is a clean, small interface
  (`internal/painter/gl/painter.go:16` — `Init`, `Clear`, `Paint`, `Free`,
  `Capture`, clipping, sizes) and a D3D11 implementation of it is a plausible
  amount of work. But `common.Canvas.SetPainter` takes `gl.Painter` — the
  concrete package's type — so it is a replacement for Fyne's painter rather
  than an addition beside it, and every window creation path has to be taught
  about it. The patched copy makes it possible; it does not make it small.
  Rejected for now: the workload is a few hundred textured quads and the API is
  not what is slow — with damage regions in, most frames are a restore quad
  plus a handful of draws, and no API change improves that.

## Other toolkits, honestly

Only relevant if the patches above stop being enough.

- **Gio** — Windows backend is Direct3D 11, so a flip-model swapchain and a
  DXGI waitable object are available to it and structurally are not to us. It is
  also immediate-mode with no widget library worth the name: this client's
  sidebars, member list, markdown renderer, settings page and theme system are
  all Fyne-shaped and none of it ports. A rewrite, not a migration.
- **Ebitengine** — has DirectX backends on Windows and shares oto's authors, so
  `internal/audio` would carry straight over. It is a game framework; there is no
  text input, no selection, no accessibility. Worse fit than Gio for a chat
  client.
- **Wails / webview** — inherits the browser's compositor, which solves
  presentation completely and replaces the entire UI layer with HTML. The strict
  DAG means `domain`, `client`, `cache`, `markdown`, `config` and `audio` survive
  a move; `ui` and `app` do not.

The seam that makes any of these thinkable is the one already enforced: only
`internal/client` knows revoltgo, only `internal/ui` knows Fyne. Keeping that
line clean is the cheapest insurance against needing this section.

## Which cores it runs on

`internal/cpu` reports the machine's logical processors split into kinds and
pins the process to one of them; the Performance section's **Processor cores**
row is what picks. Only two splits are read, because only two are legible rather
than guessed: Intel's hybrid parts publish an `EfficiencyClass` per logical
processor, and an AMD part whose cores sit behind exactly two L3 caches is a
pair of chiplets, offered as **CCD0** and **CCD1** in the machine's own
numbering. The numbering is an identity, not a judgement: which chiplet carries
the better bins is CPPC's preferred-core ranking, which Linux publishes
(`amd_pstate`'s `highest_perf`) and Windows offers no user-mode read for, so
neither side is claimed to be the faster one. There is no Automatic setting:
the first run on a split machine resolves the default against the machine and
writes it into the settings file (`app.resolveCores`) — the efficiency cores on
a hybrid part, CCD1 on a chiplet one, CCD0 being where preferred-core
scheduling and a game's cache steering usually land — so the file and the page
always name the set that actually runs.

Three things about it are worth knowing before it is trusted:

- **None of it is measured.** There is no benchmark for it and no number below,
  because the defaults rest on arguments rather than measurements: efficiency
  cores are a power argument for a client that is idle most of the time, and one
  chiplet is a latency argument about work not crossing the fabric plus staying
  out of the way of whatever is steered to CCD0. **All cores** is what to reach
  for the day either argument turns out wrong here.
- **It moves the whole process**, not the draw loop. `mixer.render`'s 10 ms
  deadline (below) is pinned with everything else, so a set of cores too slow for
  it is a dropout rather than a dropped frame. That is the one failure worth
  looking for after changing this.
- **`GOMAXPROCS` moves with it.** The runtime counts its processors once, at
  startup, and hears nothing about a mask set later; leaving it alone would
  schedule that many Ps onto however few cores remain. `cpu.Pin` sets both.

The baseline it restores to is the affinity the process *started* with, not every
core in the machine — `start /affinity` or `taskset` is a decision somebody made,
and widening past it would overrule them.

## Measuring

The client itself is not instrumented. `internal/app/virtual_bench_test.go`
drives the message column through the App's own entry points under the software
driver — open, wheel tick, a page of history, a live message, the per-frame
min-size walk and the live heap of a mount — and is where the numbers above come
from; run it before and after touching the column. Beyond that:

- **`RGO_FRAMETIME=1`** logs a frame-cost split every 120 painted frames —
  prep / damage / draw / swap, full-repaint count, objects drawn — the fork's
  ninth patch. Off it costs one bool test per frame. This is what the numbers
  in "What a frame measured as" are, and where any new frame claim starts.
- `pprof`'s CPU profile is close to useless on Windows for the loop goroutine:
  the profiler samples threads blocked in cgo, so `glfwWaitEventsTimeout`
  swallows the profile. Attribute idle cost with per-thread
  `TotalProcessorTime` deltas instead, and frame cost with `RGO_FRAMETIME`.
- `pprof` on the loop goroutine catches layout and measurement cost, which is
  where our own code lives; it will not catch GL or driver time. Reading one:
  the mark phase is most of the profile by samples and almost none of it by wall
  clock, running on the cores the loop is not. **`GOGC` is not a lever** —
  100, 400 and 800 are indistinguishable on every benchmark here.
- `-tags pprof` also serves `/debug/pprof/goroutineleak`. `App.epoch` drops a
  replaced session's workers rather than joining them, so a leak is possible;
  each carries a pprof label, so one reads as the action that started it.
- The thing worth watching during a scroll is **mounted object count**, not FPS.
  Traversal is what grows; fill rate is bounded by the viewport already.

## The audio callback

`mixer.render` (`internal/audio/mix.go`) runs on miniaudio's own thread, once
every 10 ms, and is the one place in the client with a hard real-time budget: a
period missed is an audible dropout, not a dropped frame. It **must not
allocate, lock, log, or call anything that might** — a Go allocation there can
trip a GC assist, and a mutex there can be held by a goroutine the scheduler has
parked.

Everything crossing into it does so through `ring` (wait-free SPSC) or an
atomic. The accumulator, the voice pool and the lane array are fixed-size fields
on the mixer, allocated once at construction, which is why a period is rendered
in `chunkFrames` passes rather than against a slice sized by whatever the
backend asks for.

The rule is cheap to assert and is asserted: `TestMixerAllocs`
(`internal/audio/mixer_bench_test.go`) runs `Sink.Write` plus `render` under
`testing.AllocsPerRun` and fails on anything but **zero**, because anything
nonzero is a dropout under load even when it benchmarks fine. The lane has to be
**opened** first — `Sink.Write` drops for a user with no lane, so the same
assertion over a sink nothing opened passes without rendering anything, which is
what `TestBenchMixer` did until it was fixed.

### The one rule it knowingly breaks

`render` also sends on `mixer.wake` once a period, which is what paces the whole
receive path — decode happens on `voice.Call.playLanes`, woken by that send, not
on this thread. That placement is the point: decoding here would put
`adaptiveJitter`'s mutex and a cgo call into libopus inside the callback, which
is every rule above broken at once.

The send is non-blocking and only made when a lane is open, so it costs nothing
outside a call. It is **not** lock-free, and this document used to imply it was:
a non-blocking send takes the channel's runtime lock whenever the buffer has
room, and it nearly always has, because `playLanes` drains it. So during a call
the callback takes a runtime mutex every 10 ms — bounded (the lock is held across
no syscall and no user code, ~100 ns) but a real violation of the paragraph
above, and the one the audio thread pays.

It stays because nothing else carries the device's clock. The alternatives were
worked through and every one is worse:

- **An atomic ticket the callback increments.** Free on this side — but a ticket
  cannot wake a parked goroutine, and the filler then has to *find out*.
- **Spinning on that ticket.** Burns a core for the length of every call.
- **A `time.Ticker` on the filler.** Playout paced by a timer beside the device
  rather than by the device: the exact drift the current arrangement was built to
  remove (`voice-chat-todo.md`, "Playout is now the device's clock").
- **`sync.Cond` / a mutex hand-off.** Both park, which turns a bounded ~100 ns
  runtime lock into an unbounded one held by whatever the scheduler chose.
- **Decimating the kick** — ticket every period, channel send every *N*th — is
  the only one that keeps the device as the clock, and it halves the rate without
  removing the lock. It also moves lane occupancy, so `maxFramesPerPass` has to
  be resized against *N* and the 50 ms figure below re-measured; sized wrong, the
  symptom is a lane running dry and nothing reports it. Not taken: it buys a
  factor of two on a cost that is already small, at the price of a silent failure
  mode. Take it only with the occupancy measurement in hand.

The way out is a wake primitive that is lock-free on the signalling side —
`runtime_Semrelease` and `notifyList` are exactly that and are not reachable
outside the standard library.

The arrangement bounds something that used to be unbounded. Playout ran on a
20 ms `time.Ticker` per participant, which drifts against the audio clock; a lane
the ticker outran was trimmed back **to** `laneBacklog`, so it parked at 120 ms of
buffered audio and stayed there — pure mouth-to-ear latency that nothing took
out again. Now the writer asks `Sink.Want` and supplies only that, so occupancy is
`laneTarget` (40 ms) plus at most one frame. Measured with `render` driven flat
out against a producer that would run ahead: peak **50 ms**, never near the
120 ms threshold.

Two costs are miniaudio's rather than ours and cannot be removed here: malgo
takes a package-level mutex in its own callback trampoline to find the Go
function for the device, and it leaks the C allocation behind a device
identifier — `deviceIDPointer` memoises those, so the leak is one per device
ever opened rather than one per open.

Measured on WASAPI shared mode: the microphone negotiates `IAudioClient3` at a
480-frame period with **passthrough** — no resample, no format conversion —
because the capture device is opened at exactly 48 kHz mono f32. Asking for
anything else puts a converter in the path for nothing.

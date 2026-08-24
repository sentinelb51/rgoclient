# Performance and responsiveness

What the client's frame actually costs, which levers exist, and which of them
need a fork. Linked from the root `CLAUDE.md`. Companion to `known-gaps.md`:
that file says what is *missing*, this one says what is *slow* and why.

Line numbers are `fyne.io/fyne/v2@v2.8.0` and will drift. Re-check them against
the module cache copy (`go list -m -f '{{.Dir}}' fyne.io/fyne/v2`) before acting
on one — the shape of the claim survives a bump, the line does not.

Fyne is **patched** — [`rgoclient-fyne`](https://github.com/sentinelb51/rgoclient-fyne),
`PATCHES.md` there. Five of the levers below are ours now rather than
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
`app.applyPacing`. Upstream it is the literal `time.Second / 60`.

**The rate is a ceiling, not a rate.** `drawSingleFrame` draws a window only if
`decideRepaint(visible, frame.ready(), canvas.CheckDirtyAndClear)`, so the drawn
frame count at rest is 0. Waiting on the OS queue rather than polling it also
takes the rate out of how quickly input is *noticed*: the wait returns on the
event, and only the drawing it asks for is paced. At rest the loop sleeps at the
patch's `idleWait` (100ms) instead of at the frame rate — at `FrameRate` 600,
188ms of CPU per 10s idle before that patch and 31ms after. Raising the rate
raises the ceiling on animation and scroll smoothness and costs nothing at
rest.

**Dirty is one bool for the whole window.** `Canvas.dirty`
(`internal/driver/common/canvas.go:50`), test-and-cleared by
`CheckDirtyAndClear`. `Canvas.Refresh(obj)` (`canvas.go:280`) queues the object
for texture freeing and then sets that same global flag. There are no damage
rectangles at any level, and `glCanvas.paint` opens with
`c.Painter().Clear()` — a full framebuffer clear. **Any `Refresh()` anywhere
repaints the entire window.**

**A repaint walks every mounted object, and draws the visible ones.** The walk
(`internal/driver/util.go:137`) prunes only on `!Visible()`; there is no rect
prune, so a scrolled-away subtree is still descended. Culling happens one level
down, in `painter.Paint` (`internal/painter/gl/painter.go:108`), which rect-tests
the object against the current clip and returns before `drawObject`. So a dirty
frame is **O(mounted) in traversal and O(on-screen) in draw calls**. Mounting
fewer widgets buys traversal; it does not buy fill rate, which was already
bounded by the viewport.

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

**The present gate is a stub off Wayland.** `presentGate` (`present.go`) is real
on Wayland via `wl_surface.frame` callbacks and `noGate{}` (always ready)
everywhere else. The seam for frame pacing exists; nothing fills it on Windows.

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
- **Nothing is resolved twice on a walk.** `Store.Relationships` carries the
  relationship it filtered on into the resolution rather than asking again. With
  the friends dialog down, `awaitingAnswer` does not call it at all:
  `Store.HasIncomingRequest` walks the same accounts resolving nobody, ordering
  nothing and taking the client's lock once for the walk rather than once per
  account (`Client.knownRelations`). Both run off `flushAuthors`, once per batch
  of resolved authors, on the UI thread — the shape worth looking for is a guard
  that resolves, then resolves again, and it was one level up from where it had
  already been fixed.

## Reachable without touching Fyne

Ranked by return.

1. **Optimistic local echo.** Paint the sent message immediately, reconcile on
   the server ack. Pure application logic and the largest perceived win. The
   design constraint is ours: `cache/message.go` keeps entries ULID-sorted and
   `Find`/`Remove`/`Replace` binary-search on that, so a provisional ID has to
   sort where the real one will land — otherwise the ack is an insert plus a
   delete and the row visibly jumps. Revolt accepts a client-supplied `nonce`,
   which is the reconciliation key.
2. **`Refresh()` discipline.** Every call is a full-window repaint plus a full
   framebuffer clear. Worth auditing what the gateway handlers and the typing
   timer refresh: refreshing a container to update one label inside it costs the
   same as a resize. Refresh the narrowest object that changed, and prefer *not
   dirtying at all* when the change is invisible (the change guards already used
   by the slowmode chip and the typing line are the pattern). The one already
   found: `MessageInput` refreshed after `widget.Entry`'s typing methods, which
   end in a refresh of their own — two re-wraps and two dirty windows per
   keystroke.
3. **`FYNE_CACHE`.** `internal/cache/base.go:22` reads it as a `time.Duration` in
   `init()` and it is the only Fyne env knob that touches performance. Raising it
   past a minute keeps glyph textures alive across a scroll-away-and-back at the
   cost of VRAM; lowering it trades redraws for memory. Settable from
   `main.go` with `os.Setenv` before the first widget, so it can be a real
   setting. Measure before shipping one — this is a guess until it is not.

## Taken by patching Fyne

[`rgoclient-fyne`](https://github.com/sentinelb51/rgoclient-fyne) is v2.8.0 with
seven patches; its `PATCHES.md` is the list and `update-fyne.sh` carries them
onto a new version. The frame rate and vsync landed
together on purpose — raising the ceiling while the driver still blocks in
`SwapBuffers` changes nothing — and both are settings under Performance. The
third is the font-parse cache, which is a leak fixed rather than a lever. The
fifth replaced the driver's poll loop with a wait on the OS event queue, which
is what makes the frame rate a ceiling on drawing rather than on noticing input.

The last two are **work skipped**, both found by profiling the message column
and both in exported code rather than under `internal/`:

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

## Still needs more than a patch

- **Damage-region redraw** would mean replacing `Canvas.dirty` with a rect list
  and teaching `paint` to scissor. Deep, and it fights the full `Clear()`.
- **A D3D or Vulkan painter.** `gl.Painter` is a clean, small interface
  (`internal/painter/gl/painter.go:16` — `Init`, `Clear`, `Paint`, `Free`,
  `Capture`, clipping, sizes) and a D3D11 implementation of it is a plausible
  amount of work. But `common.Canvas.SetPainter` takes `gl.Painter` — the
  concrete package's type — so it is a replacement for Fyne's painter rather
  than an addition beside it, and every window creation path has to be taught
  about it. The patched copy makes it possible; it does not make it small.

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
processor, and an AMD part whose two L3 domains carry different amounts of cache
is a stacked-cache pair of chiplets, offered as **CCD0** and **CCD1** in the
machine's own numbering — an identity, not a claim about which is faster. Two
chiplets with the *same* cache are **not** a split: what separates them is
binning, which nothing here can read, and a hypervisor's invented L3 domains
look exactly like them. The default is **Automatic**: the efficiency cores on a
hybrid part, and CCD1 on a chiplet one — usually the chiplet without the stacked
cache and therefore the higher-clocking, and one chiplet either way, so the work
does not cross between them.

Three things about it are worth knowing before it is trusted:

- **None of it is measured.** There is no benchmark for it and no number below,
  because the defaults rest on arguments rather than measurements: efficiency
  cores are a power argument for a client that is idle most of the time, and one
  chiplet is a latency argument about work not crossing the fabric. **All
  cores** is what to reach for the day either argument turns out wrong here.
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

- Frame cost is now observable from inside the loop — the fork is
  ours to instrument — but nothing there is instrumented, so measure from
  outside first: a GPU/frame profiler, or wall-clock around `App.doOnUI` work.
  A timer added to `repaintWindow` is a patch to carry forward; be sure it is
  worth carrying.
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

The rule is cheap to assert and worth asserting: `testing.AllocsPerRun` over
`Sink.Write` plus `render` must report **zero**. Anything that makes it nonzero
is a dropout under load even when it benchmarks fine.

`render` also sends on `mixer.wake` once a period, which is what paces the whole
receive path — decode happens on `voice.Call.playLanes`, woken by that send, not
on this thread. That placement is the point: decoding here would put
`adaptiveJitter`'s mutex and a cgo call into libopus inside the callback, which
is every rule above broken at once. The send is non-blocking and only made when a
lane is open, so it costs nothing outside a call.

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

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
is the glyph runs of rows newly scrolled into the overscan, not draw calls.
Idle with a quiet channel on screen is ~0.4% of one core, and that is the
gateway plus the 100 ms `idleWait` wakeups, not painting. What remains, in order
of size:

- **The prep walk is the frame's biggest CPU phase** and is structural:
  `obj.MinSize()` per mounted object goes through a renderer cache lookup each.
  Skipping the walk on frames whose refresh queue drained nothing was
  considered and rejected — a min-size change without a `Refresh` is legal API,
  and the walk is upstream's catch-all for it.
- **`logError` was a false lever.** `build.Mode` is a build-tag constant, so in
  a normal build `logGLError` compiles to an empty function and the calls cost
  nothing. The earlier claim here that gating it was "the cheap third" was
  wrong.
- **The overscan spike is rasterisation, not upload, and asynchronous upload
  is therefore a false lever.** This document used to say the spike was glyph-run
  texture *upload*; it is not. `newGlTextTexture` does two things, and measured
  in the fork (2026-08, i7-13700HX, a 79-character line at 14pt into a 700×20
  RGBA, `internal/painter`):

  | | cost | allocations |
  |---|---|---|
  | shaping alone (`MeasureString`) | 24 µs | 122 |
  | shaping + rasterisation (`DrawString`) | 282 µs | 482 |
  | the `image.NewRGBA` under it | 4.5 µs | 2 |

  So **rasterisation is ~258 µs of the 282**, and the `TexImage2D` it feeds is
  56 KB — single-digit microseconds. A PBO or a shared-context upload worker
  would attack about 5% of the phase. A ~20 ms spike is ~70 of these.

  Two costs are inside it, both in `go-text/render` rather than in Fyne.
  `DrawShapedRunAt` builds a `rasterx` scanner and filler **per call**, sized to
  the whole destination, which measures as ~6 ns per destination pixel: 700×20
  costs 294 µs, 1400×20 costs 377 and 700×60 costs 463, for the same 79 glyphs.
  The rest is ~2.7 µs per glyph outline, rasterised from segments every time —
  there is no glyph cache anywhere in the path. **A glyph mask atlas is the
  lever**, and it is a real project: per-glyph masks keyed by face, size and
  subpixel phase, composited with `draw.DrawMask`, falling back to the current
  path for bitmap and SVG glyphs. It lives in the fork's `internal/painter`, and
  the shaper it would sit beside (`painter.shaper`, one package-level
  `HarfbuzzShaper`) is not safe for concurrent use, so doing it on a worker
  instead needs a pool first. Unbuilt.
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

## What a screenshare costs (2026-08)

Measured on an i7-13700HX. Two things about the send half were worth a number
rather than an assumption:

- **Windows capture is `gfxcapture` first, and the win is the scale, not the
  grab.** All three Windows grabbers were measured against each other. The
  ladder is Graphics Capture → Desktop Duplication → GDI, and each step down
  is slower: gdigrab is a CPU `BitBlt` of a DC per frame, ddagrab hands back a
  full-size D3D11 surface the GPU already has, and gfxcapture hands back the
  same surface **already scaled to the encode box**, which is the only one of
  the three that can. That last part is where the money is: a 2560×1600
  monitor into a 1280×720 share reads back 3.3 MB per frame instead of 16 MB,
  and swscale is left with a format conversion instead of a resize. Measured
  on an RTX 4070 Laptop at 30 fps over fifteen seconds, production filter
  chain and encoder, 450 frames out either way: **0.23 s of process CPU with
  the CPU scale, 0.05 s with the GPU one** — 4.6×. Window capture measured a
  wash against gdigrab on CPU alone (0.52 s vs 0.59 s over ten seconds at
  720p30) and is taken for the other three reasons: no machine-wide pointer
  flicker, addressing by `HWND` rather than by title, and a covered window
  captured correctly.
  Two costs bought with it, both accepted. Graphics Capture starts slowly —
  ~1.3 s before the first frame against gdigrab's ~0, one-off per share — and
  on Windows 10 the system draws a yellow border around what is being
  captured that cannot be turned off (Windows 11 can, and does here).
  Each rung is **probed once per run** rather than guessed at, and the picker
  warns only when the floor is what a share will actually use.
- **The send encode is capped VBR by default, and that is the largest single
  saving in the send half.** A screen is idle most of the time, and CBR pays
  the target rate for a still picture — nvenc pads the difference with filler
  NALs (type 12). Measured on an RTX 4070 Laptop, 1080p30, 6.22 Mbps target,
  150 frames, the production argument list: a static screen costs **5.97 Mbps
  under `-rc cbr` against 0.05 under `-rc vbr`** — 119× — moderate motion 5.97
  against 1.18, and content that genuinely needs the budget an identical 6.35
  either way. So the ceiling is unchanged and the peak quality is unchanged;
  what goes is the padding. Every contract the pipeline depends on survives it:
  1.0 slices per frame, IDRs exactly at the `-g` interval, SPS/PPS ahead of
  each, AV1 sequence headers at the same points, and zero filler. `libx264`
  never had the problem — it pads only under `nal-hrd=cbr`, which nothing here
  sets. The mode is per encoder (`shareEncoder.rateControl`): `vbr` on NVENC,
  `vbr_latency`/`vbr_peak` on AMF, `rc_mode VBR` on VAAPI, and on QSV the
  *inequality* between `-b:v` and `-maxrate`, which is the only thing it reads.
  **CBR is a setting** (`Screenshare.RateControl`) because the padding is not
  purely waste on every path: the stream it replaces idles near zero and bursts
  to the ceiling the moment the screen moves, and a bandwidth estimator or a
  fixed uplink that has been measuring nothing is what that burst overshoots —
  which is the reason a live-streaming service ingests CBR. Nothing here has
  measured that effect, so the default stays the mode with the number behind it.
  Constant also shortens the buffer to one second from two, a ceiling that never
  moves being the point, and turns on `nal-hrd=cbr` for libx264 — which is
  emitted from `args` rather than `rateControl`, ffmpeg's `-x264-params`
  replacing rather than merging, so it has to travel with the latency tune.
- **Keyframe interval is nearly free under VBR, so Sparse is four seconds and
  not eight.** Same rig, 300 frames, moderate motion: 1 s = 1.29 Mbps, 2 s =
  1.20, 4 s = 1.17, 8 s = 1.17. Eight seconds bought **nothing** over four
  while doubling how long a joining viewer stares at nothing — the receive half
  drops frames until a keyframe and a CLI encoder cannot answer a PLI — so the
  interval nobody pays for is the one that halves the wait. Frequent (1 s)
  costs about 7% over Standard, which under CBR it could not have, the
  keyframe there being stolen from the P-frames rather than added.
- **(Historical: the send encode is H.264 now — hardware where a probe finds
  NVENC/AMF/QSV/VAAPI willing, libx264 otherwise — and libvpx is retired.
  The measurement stays as the record of why EncoderSpeed has exactly three
  levels.)** libvpx's `-cpu-used` is not a dial — it is four steps, and the
  first twelve values are one of them. Measured at 720p30 on captured desktop
  pixels panned to force full-frame motion, single thread, 300 frames, output
  compared by md5: 0 through 12 encode **byte-identical** output (2.95
  ms/frame, SSIM 0.9992); 13-14 are a second behaviour; 15 is 2.37 ms at
  0.9959; 16 is 1.72 ms at 0.9688. The client had been passing 8, which is
  exactly 0. The three levels `config.ShareSpeedQuality/Balanced/Fast` name are
  8, 15 and 16 — the values that actually differ. On an *idle* desktop every
  value produces identical output, so the setting only means anything under
  motion.
- **GPU-side scaling was tried and rejected: `scale_d3d11` does not work
  here.** The theory was good — the chain downloads the whole desktop as BGRA
  (16.4 MB/frame at 2560×1600) before swscale shrinks it, so scaling on the
  GPU first would cut the readback ~13× and delete a CPU stage. Two things
  killed it. The win is far smaller than it looks: the *entire* capture +
  download + scale + convert path measures 0.47-1.15 ms/frame in the real
  4-thread pipeline against an encoder at ~3.7-5.7 ms/frame, so the ceiling on
  the saving is under a fifth of the child's CPU, not the 2.7 ms/frame a
  single-threaded synthetic swscale suggests. And on an RTX 4070 Laptop
  (driver 537.13) with a current ffmpeg master build, `scale_d3d11` fails to
  allocate its output texture — `Could not create the texture (80070057)`,
  `E_INVALIDARG` — for every output format, **including its own documented
  d3d11va-decode path**, so the filter is unusable here rather than merely
  unhelpful. It also takes only `width`/`height`/`format`, with no
  `force_original_aspect_ratio` and no pad, so the fit would have to be
  computed on our side and padded on the CPU anyway. Revisit only with
  evidence the filter works on the target machine.
- **`ShareTee` costs a share with no preview open almost nothing**, and what
  it does cost is framing, not copying. Passing a synthetic 7.7 MB IVF stream
  (3000 frames, one 42 KB keyframe per 30, sizes taken off a real 1214×758
  capture) through a 32 KB buffer:

  | | per 7.7 MB | over a plain read | allocations |
  |---|---|---|---|
  | plain read, no tee | 171 µs | — | none |
  | tee, nothing attached | 213 µs | 42 µs | 240 B |
  | tee, nothing attached, copying bodies | 318 µs | 147 µs | 90 KB |

  The third row is what it did first, and the second was after **a body
  nobody is watching was counted past rather than copied**. That skip was
  then **deliberately given back** (2026-08) when publishing moved to
  arrival-paced `WriteSample` — the tee became the framer, and the assembled
  body *is* the publisher's sample buffer now, so the third row's cost is
  paid again but buys the sample that had to exist anyway rather than being
  overhead on a passthrough. ~150 µs and one frame-sized buffer per 7.7 MB
  of stream, the price of deleting a fixed ~5 s of viewer latency (below).

  (The numbers above are the IVF tee's; the H.264 walk hops FLV tags the
  same way, plus a length-prefix-to-start-code rewrite of each unit. It was
  a byte scan for Annex-B start codes until 2026-09, when that scan turned
  out to cost a frame of latency — a unit's end is only known at the next
  one's start — rather than only bytes. The parameter sets are still kept
  aside and replayed to a preview that attaches mid-share, so its decoder
  can enter at the next IDR.)

  **Bandwidth is zero either way and in both states**: the tee is local, and a
  preview neither publishes nor subscribes anything. With the preview open the
  tee adds one clone per frame (~1.7 KB at these sizes); the benchmark cannot
  put a useful number on that row, because a 45 GB/s producer against one
  writer goroutine measures the drop path rather than the work. The real cost
  of watching your own share is the second ffmpeg child decoding it and the
  paint, which is the receive path's cost, not the tee's.
- **A share's latency is a queueing property, not an encoding one** —
  reported as a ~5 s glass-to-glass delay every viewer saw while the
  self-preview was instant. The encoder's own contribution at the default
  lowest-latency flags is one frame; the five seconds were a standing queue
  between the encoder and the wire: lksdk's paced writeWorker sends exactly
  one frame per duration off its own schedule, so everything encoded during
  the publish negotiation — and every `fps`-filter duplicate burst a grabber
  stutter adds later — was replayed at 1× for the life of the share, and a
  matched-rate queue never drains. Invisible in the preview because the tee
  taps bytes upstream of where the queue stood. The fix was structural, not
  a tuning: write on arrival (`voice.pumpShareSend`), drop what cannot be
  sent yet, and there is no buffer left that *can* hold seconds. The same
  ratchet existed on the watch side — a waited paint hop under a dragged
  window — and got the same cure, a latest-wins mailbox that drops frames
  for a slow painter. The lesson generalises: in a live pipeline, every
  queue is latency and every pacer must be downstream of the drop.
- **Glass-to-glass, measured (2026-09).** The clock harness
  (`internal/app/screenshare_live_test.go`: a window whose picture is the
  wall clock, shared by one saved account and decoded back by the other
  through Stoat's `hel1` relay, 44 ms RTT from here) put a number on every
  frame, and the number was made of *held frames*, one each in six places
  — every one a whole frame at any rate, so 33 ms at 30 fps and 200 ms at
  5. Medians, 816×488 at 30 fps, AV1 on NVENC unless said:

  | stage | preview (tee → decoder) | watch (through the relay) |
  |---|---|---|
  | as found (decoder syncing CFR against IVF's 90 kHz clock) | — | 13–27 s, growing |
  | `-fps_mode passthrough` | 103 ms | 204 ms (H.264: 173 / 283) |
  | + output `-threads 1`, H.264 in IVF, preview in IVF | 79 ms | 152 ms (H.264: 106 / 181) |
  | + no `fps` filter (output CFR sync), own frame assembler | 42 ms | 88 ms |
  | + H.264 send in FLV | H.264: 41 ms | H.264: 86–93 ms |

  The same run at 5 fps: 50 / 90–99 ms, where each held frame had been
  200. At 60 fps and 1156×672 (3.3 Mbps): 51 / 93–100 ms. What is left is
  real: ~12 ms of clock granularity, a frame of capture and encode on the
  GPU, the one-way trip and the relay's turnaround (~50 ms of the 88), and
  the decoder (~2 ms, measured alone). The first picture still waits for a
  keyframe — up to the interval, a CLI encoder answering no PLI — and that
  is the largest number a viewer now sees. Encoder cost at this size:
  0.6% of a core mean (NVENC), 188 MB resident.

  Two ffmpeg defaults did the holding and are worth knowing cold: **the
  rawvideo encoder is frame-threaded** like any other, and frame threading
  keeps one frame back — a raw-in raw-out control showed it, so it was
  never the codec — and **the `fps` filter needs the frame after** to
  choose between them, where the output's own CFR sync (`-fps_mode cfr -r
  N`) decides on the frame in hand. Neither shows in a file transcode.

## Still needs more than a patch

- **Independent flip and MPO.** Not reachable from a WGL context at all: DWM's
  overlay planes need a DXGI flip-model swapchain, and WGL presents through the
  redirection surface. `docs/Fyne_fork_independent_flip.md` is the design for
  the one route that exists — `WGL_NV_DX_interop2` into a flip-model chain —
  and why it is not taken.
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

## What the OS is asked for

Three things beyond the cores, all of them the platform's rather than Fyne's.

### The frame clock is a timed wait, and Windows rounds one up

The driver parks in the OS event queue for whatever is left until the next frame
deadline (`glfw.WaitEventsTimeout` → `MsgWaitForMultipleObjectsEx`), and that
timeout is quantised to the process's timer resolution. Go stopped raising it in
1.16, when the runtime moved to high-resolution waitable timers for its own
scheduler — which does nothing for a Win32 wait. Measured here:

```
NtQueryTimerResolution: min=15.6250ms max=0.5000ms current=15.6250ms
  default: wait(1ms)=15.16ms  wait(8ms)=15.55ms  wait(16ms)=31.02ms
  raised:  wait(1ms)= 1.21ms  wait(8ms)= 8.49ms  wait(16ms)=16.51ms
```

So the fork's `waitResolution = time.Millisecond` — commented as "the
granularity of the OS wait — a millisecond on Win32" — was false as shipped.
What it cost: **with vsync off the client was capped near 64 fps**, the present
gate being defeated by the wait beneath it (the gate reports not-ready, the loop
asks for a millisecond and sleeps 15.6); and any pacing not woken by input or a
`DoOnUI` post quantised to the same tick. With vsync on and a 60 Hz panel it
mostly hid, `SwapBuffers` blocking longer than the tick.

`power.Precise` asks for it while the window is in front and gives it up behind,
off `applyPower`. Since Windows 10 2004 the request is the calling process's
alone, so this no longer holds the whole machine's clock fast — but it is still
real idle power, which is why it follows the focus rather than being set once.
It is asked for only where the deadline is finer than the coarse tick can
express, so a reader who has set a low `FrameRate` pays nothing.

### Efficiency mode, and the one thread it must not reach

`SetProcessInformation(ProcessPowerThrottling, EXECUTION_SPEED)` is what Task
Manager labels efficiency mode: efficient cores and a lower clock request. It is
the natural partner of `BackgroundFrameRate` and rides the same hooks
(`power.Throttle`, off `applyPower`).

It reaches **every** thread, and miniaudio registers no MMCSS task for the one
it renders on — `ma_wasapi_usage_default` maps to a NULL task name, and malgo
exposes no way to ask for "Pro Audio" — so the mixer's 10 ms period is a plain
thread's. A dropout is not a dropped frame, so **a live call withholds the
throttle**: `App.callLive`, set by `installCall` and cleared by `dropCall`, both
of which re-apply. Nothing exempts a single thread here, the thread not being
ours to name.

Windows only. Linux's nearest equivalent is nice and it is one-way — an
unprivileged process may raise its nice value and cannot lower it again
(RLIMIT_NICE defaults to 0), so a client that went quiet in the background would
stay quiet for the session. macOS's is PRIO_DARWIN_BG, which throttles disk I/O
hard enough to hold up the notification a backgrounded client exists to deliver.
`internal/power`'s non-Windows file is those two reasons and nothing else.

### The ffmpeg pipes are sized rather than defaulted

`exec.Cmd`'s pipes take the platform default — `CreatePipe(..., 0)` on Windows,
64 KiB on Linux — and a 1080p RGBA frame is 8.3 MB, so a frame crossed in
thousands of kernel copies, each a writer blocked and a reader woken. Measured
against a child writing 256 MiB, twice:

| pipe buffer | throughput |
|---|---|
| default | 3.5 / 4.0 GB/s |
| 64 KiB | 5.4 / 5.3 GB/s |
| 1 MiB | 6.0 / 6.0 GB/s |

`video.launch` makes both pipes itself now (`sizedPipe`, `pipeBytes` = 1 MiB;
`CreatePipe` with a size on Windows, `F_SETPIPE_SZ` on Linux, `os.Pipe` on
macOS, which has neither and grows a pipe to 64 KiB by itself). At 1080p30 that
is roughly **62 → 42 ms of CPU per second of playback**, split across both
processes. What it costs is the bookkeeping `exec` was doing: the child's ends
are closed after `Start` and the read end by `reap` rather than by `Wait`.

**`LiveFrames` deliberately keeps the small default.** Out, a buffer under one
frame is what turns a stalled reader into decoder backpressure instead of a
queue of stale frames; in, a megabyte of a 1.4 Mbps bitstream is six seconds of
latency nothing takes back out. The size is a parameter for exactly that.

### The live decoder's probe is floored, both formats

`avformat_find_stream_info` reads up to `probesize` before it answers with a
frame, and the default is 5 MB. `LiveFrames` floored it for IVF and left H.264
at the default, on the theory that H.264 has to be left to find its SPS. (Since
2026-09 every live stream is IVF — H.264 under the `H264` fourcc — so the
second column is history; the measurement stands.)
Measured against a 720p15 4 Mbps Annex-B stream fed at real time, time to first
picture:

| flags | to first frame |
|---|---|
| `-analyzeduration 0` | 3057 ms |
| `+ -probesize 32` | 280 ms |
| `+ -fpsprobesize 0` | 3063 ms |

`-fpsprobesize` moves nothing; `-probesize` is the whole of it, and 20 frames
decode byte-identical either way — the demuxer is named rather than sniffed, so
there is nothing to probe *for*. IVF measured 273 ms already. **Startup only**:
150 frames arrive at 9980 / 10006 ms against 10000 ms of stream, so the
pipeline takes it back. The cost is time to first picture, which for H.264 was
most of what a viewer waited.

### Looked at and not taken

- **`SO_RCVBUF` on the RTP socket.** pion sets a read buffer only on its
  server-side `udp_mux_multi`, so the client's gathered sockets take the OS
  default. Raising it means a `SettingEngine` and a UDP mux threaded through
  lksdk, which owns connection setup here and is handed none today — a fixed
  local port and a new seam for an unmeasured benefit. Revisit with a measured
  drop count under a 12 Mbps share.
- **Per-monitor present gate.** `present_windows.go` opens the *primary*
  adapter and says so, because the `HWND` does not exist when `newPresentGate`
  is called. Fixing it means moving the call site, which is more than the
  mispacing of a window dragged to a second panel is worth.
- **The display's refresh rate as the `FrameRate` default.** It would *lower*
  the ceiling on a 60 Hz panel, and a ceiling costs nothing at rest — that is a
  behaviour change dressed as an optimisation.

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
  remove ("Playout is now the device's clock", in the retired
  `docs/voice-chat-todo.md` — git history).
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

### What a silent participant costs

The publisher sends Opus's DTX packets — one or two bytes, fifty a second — for
as long as its gate is shut, and the decoder takes each as a lost frame. With
Deep PLC on that is the neural concealer running on nothing: **97 µs a frame
against 9** without it, and against 18 for a real frame (offline, 9950X3D). Ten
quiet participants were half a core. `voice.quietAfter` decodes the first five
of a run — the fade-out of the last real frame — and writes silence for the
rest, which is what the far end's gate produced anyway.

### Levelling and placement are free (2026-09)

Both receive-side treatments run inside `mixLanes`, which is the callback's
hottest loop, so the question was whether they could be afforded there at all.
Four active lanes, one 1024-frame chunk — 21.3 ms of audio — on a 13700HX:

| | per chunk | of a core |
| --- | ---: | ---: |
| neither | 18.7 µs | 0.088 % |
| levelling | 19.6 µs | 0.092 % |
| both | 19.6 µs | 0.092 % |

**0.004 % of a core for the pair**, at four lanes. The measurement includes the
ring push that feeds it, so the absolute figures overstate the mix itself and
only the delta is honest.

What keeps it there is that neither is per-sample work of its own. Placement
replaces one `int32` conversion with two multiplies. Levelling measures once a
*block* — one squared int64 a sample, `blockRMS` — and the per-sample cost is a
single one-pole add, which `(*leveller).next` inlines into the loop; the branch
picking which slope it is on is hoisted into `retarget`, once a block. The
sum is accumulated as int64 rather than float32, which a thousand squared
samples would overflow the mantissa of well before the end of a chunk.

The reason levelling smooths per sample rather than stepping the gain per block
is not cost: a block is up to 21 ms, and the gain step across one at the release
slope is ~0.6 dB, which on a sustained vowel is an audible edge.

### What buffer depth costs, and what picking it badly costs (2026-09)

The jitter buffer is 60-80 % of mouth-to-ear, so its depth *is* the latency
number. Cost is not the interesting axis — one histogram bucket increment per
arrival, fifty a second per talker, and a percentile walked over 80 buckets once
a second — against a receive path already spending 15 µs a frame decoding. It
does not appear in a profile.

What is expensive is choosing the depth wrongly, in either direction. Simulated
over five minutes of one talker per row, mean delay and dropouts a minute:

| | clean wifi | busy wifi | 40-110 ms tail on 2 % |
| --- | ---: | ---: | ---: |
| p98 | 41 ms / 0 | 67 ms / 11.6 | 88 ms / 23.0 |
| p99 | 41 ms / 0 | 77 ms / 9.2 | 112 ms / 4.0 |
| p99.5 | 44 ms / 0 | 91 ms / 4.8 | 124 ms / 0.6 |
| retired rule | 40 ms / 0 | 75 ms / 8.4 | 102 ms / 9.0 |

Two things in that table. The knee is between p98 and p99.5 and it is steep —
**24 ms of delay is worth 22 dropouts a minute** on a link with a tail, which is
why `JitterBalanced` sits past it. And on a link with no tail every row is the
same buffer, so the profile is free to be conservative: nobody on a good
connection pays for it.

The retired rule is the row worth reading twice. It grew on a starve and shrank
after 250 packets played without one, and its dropout rate is **8.4 and 9.0 a
minute on two very different links** — because the number is a property of the
shrink rule rather than of the network. Shrinking blind until it starves, one
starve per five clean seconds, is a dropout every five seconds forever on
anything whose jitter sits between two depth steps. No depth setting escapes it;
the rule walks back down to the edge whatever it is given.

The estimator's own floor is the part that had to be measured rather than
reasoned about. Lateness is a wall clock here minus a sample clock there, so it
carries the two clocks' drift, and taking the zero over the whole histogram
window turns drift into lateness that is not there. A live run caught it: a test
publisher paced by a coarse Windows sleep ran 2.8 % slow, the estimator read a
steadily-climbing 400 ms — the top of its range — and pinned the buffer at its
ceiling for the whole call on a connection that was fine. The floor is now a
one-second trailing minimum, which bounds drift exposure to a second's worth and
is more correct anyway: a delay that went up and *stayed* up is the connection's
new length, not jitter, and buffering against it spends delay to cover delay.

### Correcting the depth without anybody hearing it (2026-09)

A depth change taken in Opus comfort noise is free, and a conversation supplies
one every few seconds. What is left is the source that never goes quiet — a
shared video, music over a microphone, a client with DTX switched off — and
screenshare audio is exactly that: it arrives as `SCREEN_SHARE_AUDIO`, is
subscribed through `ShareLane`, and gets its own buffer with no silence in it
anywhere.

The rule that used to cover this dropped or invented one 20 ms frame every
200 ms. Counting them on a live two-ended run against a 440 Hz tone, which has
no DTX by construction, over 45 seconds: **21 and 25 audible corrections**, in
two independent runs, nearly all of them draining one burst — a link that stalls
and then delivers 30 frames at once is 600 ms of standing delay, and a rationed
5 frames a second takes four seconds to shed it.

`stretch.go` sheds it by shortening the frame instead. Removing exactly one
pitch period leaves pitch alone — the period's *length* is what pitch is — so a
vowel becomes one cycle shorter out of fifty, which no ear resolves. Resampling
would raise every frequency by the ratio, which is the chipmunk effect and is
why the naive version is not an option. The period is found by cross-correlating
the frame's last 5 ms against itself at every lag from 2 to 10 ms rather than by
detecting a pitch: detection has no answer at all for `s`, `f` or `sh`, where a
search settles for the least bad lag and noise splicing into noise is inaudible
regardless.

Two things fell out of the design that were not obvious up front. Nothing is
reported back to the buffer, because the speakers are filled against `Want`: a
frame handed over short leaves the lane hungry and the filler pops another, so
occupancy follows the length of what was written and the accounting is the one a
plain frame already gets. And `Drift` is read once per fill pass rather than per
frame — the pass pops up to three frames ahead of the speakers, so occupancy
dips inside it without anything being missing, and correcting against that dip
is correcting for the filler's own stride.

Cost is one correlation search per corrected frame — 385 lags over a 240-sample
window, ~90 k multiply-accumulates, tens of µs — and only on frames it corrects,
against a receive path already spending 15 µs a frame decoding. The ration is
the part that matters more than the cost: one period out of 20 ms is 11 % for a
440 Hz tone and over 40 % for a low voice, and sustaining 40 % for a second is
speech that is audibly hurried even though every splice in it is clean. A lag is
never trimmed to fit, part of a period not being spliceable, so what is limited
is how many frames in a row may carry one — 25 % of the stream, which for the
440 Hz case never binds and for a 120 Hz voice is every other frame.

Measured on the same harness afterwards: bursts of 36, 23 and 35 frames drained
to depth in about five seconds each, with **zero** dropped frames. Sink-side
peak occupancy rises about 10 ms while expanding, `expand` writing up to 25 ms
where the filler asked for 20 — bounded by the longest lag, and spent only when
the buffer is short and trying not to gap.

## What noise suppression costs

RNNoise's 2017 model was replaced with upstream's current one (main at
`70f1d256`) in 2026-09. It is 33× the arithmetic — 42 features to 65, three
GRUs of 24/48/96 to three of 384, 85 KB of weights to 3.4 MB — and **cheaper**,
because the old `rnn.c` walked `weights[j*stride+i]` scalar with a per-neuron
tanh lookup and the new one runs block-sparse int8 through libopus's `nnet`.
One 10 ms frame, i7-13700HX, one core:

| | µs/frame | of a core |
| --- | --- | --- |
| 2017 model, any `-march` | 264 | 2.64 % |
| current, plain x86-64 | 194 | 1.94 % |
| **current, x86-64-v3** | **95** | **0.95 %** |

The DSP front end is ~30 µs of each and is the same code in both; the network
alone goes 232 µs → 63 µs. Suppression depth on stationary broadband noise,
gain floor 0: **−8.9 dB → −36.5 dB**, and the strength dial is now honest
across its whole range where it used to run out of model at about 9 dB.

`-march=x86-64-v3` is the entire margin — `vec.h` reaches `vec_avx.h` off
`__SSE2__` either way, but the width it works in comes from `__AVX2__` and
`__FMA__`. `rnnoise/march_amd64.go` asks for it, mirroring the floor gopus
already puts under this binary for Deep PLC. arm64 needs no flag: clang defaults
Apple Silicon to `apple-m1`, so `__ARM_FEATURE_DOTPROD` is set and
`vec_neon.h`'s `vdotq_s32` is already what runs on every Mac this builds for.

That is the top of the ladder, not a rung short of it — the newer instruction is
the slower one:

| `-march`, same source, same box | µs/frame |
| --- | --- |
| none (SSE2) | 193 |
| **x86-64-v3** | **93** |
| x86-64-v3 + `-mavxvnni` | 107 |
| native (v3 + VNNI here) | 107 |
| native `-mno-avxvnni` | 92 |

`vec_avx.h` takes a VNNI dot product wherever one exists, and it loses to the
AVX2 sequence it replaces by 15 % on a 13700HX — gopus measured the same header
the same way round on Zen 5, so it is both vendors' current silicon. `-march=native`
is the wrong flag here as well as the unportable one. Not v4 either: AVX-512 is
gone from every Intel consumer part since Rocket Lake.

The cost is **10 ms of delay**. From v0.2 on the model gets a frame of
lookahead: `rnnoise_process_frame` answers with the previous frame's audio, so
the capture chain is delayed by one 10 ms subframe while suppression is on, and
not at all while it is off. Paid once, on the send side. The gate's VAD veto
gets the same 10 ms as *lead* rather than lag — the estimate belongs to the
frame that went in, the audio to the one before it — which is the direction
that stops a word's onset paying for the check.

Also +3.5 MB of binary (104.6 → 108.1 MB) and +14 KB of `DenoiseState`
(18,512 → 32,688 bytes), one stream.

### GTCRN (2026-09)

The second model, chosen on the settings row beside the switch. GTCRN (Rong
et al., ICASSP 2024) is 47.7 K weights and 33 MMAC/s at 16 kHz, and ships as
[`gtcrn-go`](https://github.com/sentinelb51/gtcrn-go): a pure Go port of
upstream's streaming model with BatchNorm folded at export, held to the
PyTorch reference block by block on a 3 s clip to float32 rounding. No cgo,
no ONNX Runtime — every other implementation anywhere is an ONNX Runtime
wrapper, and a 20 MB runtime in-process for a 48 K parameter network was the
wrong price.

One 16 ms hop, i7-13700HX, one core:

| kernels | µs/hop | of a core |
| --- | --- | --- |
| plain Go, first working port | 242 | 1.5 % |
| plain Go, restructured | 220 | 1.4 % |
| AVX2 + FMA, the convs | 114 | 0.7 % |
| AVX2 + FMA, the GRUs too (v0.1) | 63 | 0.4 % |
| **v0.2: activations fused, real FFT, four GRU chains interleaved** | **34** | **0.2 %** |

Scalar Go tops out near one multiply a cycle here and cannot vectorise, so
the port went the way RNNoise's `-march` did: six kernels in Go assembly,
dispatched on cpuid, the plain ones kept as the fallback and the test oracle.
The two that mattered were not the obvious ones. The 1×1 convs were 37 % of
a frame and vectorised as expected; the GRUs were 65 % of what remained, and
their cost was the gate transcendentals computed one at a time. Along
frequency the recurrence is real, so those 33 steps run inside one assembly
call with the state in a register; along time the 33 bands are independent
cells, so the hidden half of the gates is a second pointwise conv and one
pass advances them all. v0.2 took the rest that was cheap: PReLU fused into
every conv's store, the ERB matrices as the pointwise kernel with one output
row, the four frequency-direction GRUs of a block walked in one loop so the
CPU overlaps their gate maths, a real FFT on a half-length complex one, and
the last block of a row redone at the row's end rather than a scalar tail —
which is only valid where a lane is a pure function of its inputs, and the
GRU cell update is not. What is left is flat.

In the chain it is 1.25 hops a frame plus the resampling and the band
reconstruction — two interpolations and one decimation, all through one
AVX2 FIR kernel in `internal/audio/fir_amd64.s` — 44 µs per 20 ms, measured
with fresh input each frame: a benchmark that feeds its own output back in
decays into denormals and reads 70.
RNNoise is 190 for the same 20 ms. The delay is the cost, ~30 ms against
10, and `docs/known-gaps.md` says why it cannot be less.

### It is the whole capture chain, and muting is the only frame it may skip

Per 20 ms frame — two model calls and the cgo crossing — against the rest of the
chain together:

| stage | µs/frame | of a core |
| --- | --- | --- |
| high-pass + preamp + gate | 7 | 0.03 % |
| **suppressor** | **206** | **1.03 %** |

So the stage is 97 % of what an open microphone costs, and it cannot be skipped
on a quiet frame: the gate measures the *cleaned* RMS, which is what stops a fan
holding it open, and the VAD the veto reads is the model's own output. Gating
the model on a voice estimate the model produces is circular.

Mute is the one state where nothing reads the answer — the publisher still calls
`Read` to keep its cadence and discards the frame — so `Capture.SetIdle` holds
the stage off there. `App.applyCaptureIdle` is the one producer, because muted
is not the whole condition: the settings meter can borrow the call's own capture,
and a reader tuning the gate while muted needs the chain they are tuning.

The state is held rather than dropped, which is what makes this free. Skipping
`Process` leaves `DenoiseState` alone, so resuming is the same room the model
was already tracking: measured against a model fed continuously across a 30 s
gap, the resumed one is within ±3 dB from the first frame — the noise's own
frame-to-frame spread. A *cold* model is the thing to avoid, leaking up to 20 dB
more noise for ~140 ms before it settles.

Push-to-talk discards its frames the same way and deliberately does **not** idle.
The resume would land on a word's onset every time, and keeping the model warm
is the only reason to run it while the key is up — idling there would destroy
exactly the thing being paid for.

## What the neural decoder costs (2026-09)

libopus went 1.5.2 -> 1.6.1 with OSCE and the blind bandwidth extension turned
on. Everything a receiver gets from it hangs off one dial — the decoder's
complexity, which is 0 unless something sets it — and "Repair dropped audio"
sets it to LACE. Per decoder, per 20 ms frame, 48 kHz mono, 13700HX:

| | 12 kbps (SILK-only) | 32 kbps (hybrid) | of a core |
| --- | --- | --- | --- |
| off | 18.0 µs | 26.0 µs | 0.13 % |
| Deep PLC (5) | 17.8 | 26.1 | 0.13 % |
| **LACE (6)** | **31.9** | **39.9** | **0.20 %** |
| NoLACE (7) | 77.1 | 86.3 | 0.43 % |
| off, 10 % loss | 17.7 | 25.4 | 0.13 % |
| Deep PLC, 10 % loss | 53.4 | 61.1 | 0.31 % |
| LACE, 10 % loss | 65.7 | 73.6 | 0.37 % |

Two different shapes, which is the whole reason LACE is the level taken and
NoLACE is not. **Deep PLC is free until something is lost** — on 1.6.1 a good
frame decodes to the same bytes at complexity 5 as at 0, and times the same, in
both modes. (The 1.5.2 measurement that claimed a quarter more per good frame no
longer holds.) A postfilter is the opposite: LACE and NoLACE run on every frame
that *arrives*, so what they cost is charged per talker for as long as they talk.
Quiet lanes skip the decoder entirely (`voice.quietAfter`), so the bill scales
with who is talking, not who is in the room — four talkers is 0.8 % of a core at
LACE and 1.7 % at NoLACE.

**The bandwidth extension is inert at the bitrate this client sends.** It fires
only on a SILK-only packet or a concealed one, and 32 kbps keeps Opus in hybrid
for every frame — measured bit-identical with it on and off, at both 32 and
16 kbps, until packets start disappearing. Opus only picks SILK-only below about
16 kbps. It is on regardless, because the case where it *does* act is a concealed
frame, which is what the switch is for, and it costs nothing to have.

The weights stopped being C. Upstream emits 71 MB of float literals for these
models and downloads them at `autogen.sh` time; gopus writes them into one
3.5 MB `opus_data.bin`, embeds it, and hands it to each decoder through
`OPUS_SET_DNN_BLOB`. Its vendored tree went 8.1 MB to 3.9 MB while gaining three
models; the client binary went 108.1 MB to 110.6 MB.

### Compiling libopus's own SSE4.1 and AVX2 sources buys nothing

`march_amd64.go` gives the whole gopus package AVX2, so upstream's hand-written
SSE4.1 and AVX2 kernels for celt and silk *can* be compiled and presumed rather
than left to run-time dispatch that this build does not have. Tried, all ten
files: **130.7 µs to encode a 20 ms frame either way**, and decode inside the
noise at every complexity. Those intrinsics were written for a compiler that had
been told nothing about the target; once `-march=x86-64-v3` tells it, gcc reaches
the same width from the plain C. Ten vendored files and a second dispatch level
for zero, so they are not there — `opus-1.6.1/config.h` carries the note.

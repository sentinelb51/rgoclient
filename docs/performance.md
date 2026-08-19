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

- **Bounded mounted set.** `app/messages.go` trims to `mountedCap()` /
  `renderedCap()`, which is what keeps the traversal above from growing with
  history length.
- **Measurement memoisation of our own layouts**: `ui.lineHeights`
  (`ui/input.go:229`), `ui.spaceWidths` (`ui/markdown.go:1030`). UI-thread only,
  which is why they can be plain maps.
- **Virtualised, recycled member list** — `ui/members.go` (`NewMemberModel`,
  `visibleRange`, recycled `MemberRow`). The pattern the message list does not
  yet use.
- **Off-thread preparation.** `Store.Members` resolves and sorts on a worker;
  the model is pure and theme-free so it can be built off the UI thread.

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
   by the slowmode chip and the typing line are the pattern).
3. **Virtualise and recycle the message list**, the way `ui/members.go` already
   does. Cuts traversal per dirty frame and shrinks the texture cache's working
   set at once. Harder than the member list because rows are variable-height and
   grouped, so `memberOffsets` does not transfer directly.
4. **`FYNE_CACHE`.** `internal/cache/base.go:22` reads it as a `time.Duration` in
   `init()` and it is the only Fyne env knob that touches performance. Raising it
   past a minute keeps glyph textures alive across a scroll-away-and-back at the
   cost of VRAM; lowering it trades redraws for memory. Settable from
   `main.go` with `os.Setenv` before the first widget, so it can be a real
   setting. Measure before shipping one — this is a guess until it is not.

## Taken by patching Fyne

[`rgoclient-fyne`](https://github.com/sentinelb51/rgoclient-fyne) is v2.8.0 with
five patches; its `PATCHES.md` is the list and `update-fyne.sh` carries them
onto a new version. The frame rate and vsync landed
together on purpose — raising the ceiling while the driver still blocks in
`SwapBuffers` changes nothing — and both are settings under Performance. The
third is the font-parse cache, which is a leak fixed rather than a lever. The
fifth replaced the driver's poll loop with a wait on the OS event queue, which
is what makes the frame rate a ceiling on drawing rather than on noticing input.

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

## Measuring

There is no instrumentation in the client yet. Before any of the above is
called an improvement:

- Frame cost is now observable from inside the loop — the fork is
  ours to instrument — but nothing there is instrumented, so measure from
  outside first: a GPU/frame profiler, or wall-clock around `App.doOnUI` work.
  A timer added to `repaintWindow` is a patch to carry forward; be sure it is
  worth carrying.
- `pprof` on the loop goroutine catches layout and measurement cost, which is
  where our own code lives; it will not catch GL or driver time.
- `-tags pprof` also serves `/debug/pprof/goroutineleak`. `App.epoch` drops a
  replaced session's workers rather than joining them, so a leak is possible;
  each carries a pprof label, so one reads as the action that started it.
- The thing worth watching during a scroll is **mounted object count**, not FPS.
  Traversal is what grows; fill rate is bounded by the viewport already.

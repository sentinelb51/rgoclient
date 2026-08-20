# Gio, costed

Whether moving the UI off Fyne and onto [Gio](https://gioui.org) would make the
client faster, cheaper on a battery, or easier to keep. Written because
`performance.md`'s one paragraph on it asserts a conclusion without showing the
arithmetic. **The answer is no on all three, and the reasons are worth keeping.**

Gio facts here are v0.10.2 (10 Aug 2026) and will drift; the shape of each claim
should outlive the version.

## What would actually be paid

The DAG decides this, not enthusiasm. Prod lines, tests excluded:

| | lines | fate |
|---|---|---|
| `domain` `client` `cache` `config` `markdown` `util` `audio` | 8,963 | untouched |
| `ui/theme` colour and size tables | ~1,240 | data, ported verbatim |
| `ui/theme` `AppTheme` bridge | ~410 | rewritten |
| `ui` | 19,189 | rewritten |
| `app` | 7,925 | rewritten in part — see below |
| `cmd` | 102 | rewritten |
| tests in `ui` and `app` | 4,561 | discarded |

So **~9,000 lines survive, ~27,000 are rewritten**, and 4,600 lines of test that
mount real widgets are thrown away rather than ported. 61 files import `fyne.io`
— 44 in `ui`, 12 in `app`, one each in `cache`, `theme`, `assets`, `cmd`. Inside
that: 17 `BaseWidget` types, 52 `CreateRenderer`s, 21 `fyne.Layout`
implementations, 42 SVG marks that Gio has no rasteriser for (it draws IconVG),
and 455 lines of Win32 that port but re-source their HWND.

`app` is the only interesting one. Half of `App`'s fields are view state —
`currentChannelID`, the typing maps, the epoch, the debounce timers, the invite
memo — and all of that survives any toolkit. The other half are mounted widget
handles, and every method that touches one changes. Splitting `App` into a
view-agnostic controller and a view is the prerequisite for doing this
incrementally at all, and is the only part of the work worth doing on its own
merits: two UIs cannot share a window, so without that split the branch is
broken for as long as the rewrite takes.

## Performance

Three things cost frames here. Gio addresses one of them.

- **Traversal of mounted objects.** Fyne walks every mounted object per dirty
  frame with no rect prune. This is the cost that used to scale with the channel
  and it is *already paid off*: `virtual_bench_test.go` has the per-frame
  min-size walk at 136 µs for a 250-message window against 1.84 ms flat. Gio's
  `layout.List` gives the same property for free rather than in 1,900 lines of
  our own — a maintenance win, argued below, not a speed one.
- **Full-window repaint on any change.** `Canvas.dirty` is one bool and
  `paint` opens with a full `Clear()`. **Gio is not better here.** It also
  redraws the whole window when it redraws; it has no damage-rect model either.
  What differs is what a frame costs once you are drawing it, which is the next
  point.
- **Text shaping.** Both cache it, both on top of `go-text/typesetting`. A
  scroll re-shapes nothing in either.

What Gio genuinely buys is **GPU-side**: Direct3D 11 on Windows and Metal on
macOS instead of OpenGL through GLFW, and a batched vector renderer instead of
one textured quad per canvas object. A message row is dozens of objects and so
dozens of draw calls today; in Gio it is one op list. Against that: this
client's measured costs are CPU-side and in the hundreds of microseconds. A
wheel tick is 103 µs of *our* code. Halving the GPU submission cost of a frame
that is already inside its budget is not felt.

## Power

**Nothing meaningful.** Idle was the power story and the fork already took it:
`runGL` blocks in `glfw.WaitEventsTimeout` at a 100 ms idle wait instead of
polling, which is 188 ms of CPU per 10 s idle down to 31 ms. Gio's loop blocks
on the platform event queue and issues a frame only on interaction or resize —
the same behaviour, arrived at by design rather than by patch. Both draw zero
frames at rest.

The one real difference is presentation during a scroll: D3D11 gives a
flip-model swapchain and a DXGI waitable object, so the compositor copy goes
away and the wait is on the object rather than inside `SwapBuffers`. That is a
GPU-power and latency win while the reader is actively scrolling, and only then.
It is also the win a D3D11 implementation of Fyne's `gl.Painter` would capture
without touching a single widget — the patched copy already makes that reachable
(`performance.md`, "Still needs more than a patch").

## The regression nobody would forgive

This client is glyphs. Gio's text rendering is documented — [gio#70, open since
2019](https://todo.sr.ht/~eliasnaur/gio/70) — as consistently lighter than
FreeType for the same font, grey and soft rather than crisp, and worst on
low-DPI displays. Neither toolkit does subpixel AA, so this is a difference of
degree; but the degree lands on every line of every message on a 1080p desktop,
which is where this client is read. Nothing else in this document would be
noticed by a user in the first minute. This would.

## Maintainability

**Better, in four specific places:**

- The **fork disappears**. Five patches, a rebase per Fyne bump, and
  `internal/` reads that have to come out of `rgoclient-fyne` instead of the
  module cache. Two of the five (font-parse cache, the wait loop) exist only
  because of Fyne; Gio needs neither.
- `layout.List` **replaces the virtualisation**. `ui/messagelist.go` and
  `ui/members.go` are 1,933 lines whose subject is mounting only what is on
  screen, and Gio's list is that by construction — an immediate-mode frame only
  lays out what it draws.
- **Two known gaps close.** `widget.Selectable` and `x/richtext` give selectable
  rich text, which Fyne 2.8 has no public API for; `x/explorer` gives native
  file dialogs on every platform, retiring 292 lines of hand-indexed COM vtables
  that are wired on Windows alone. `x/notify` gives the desktop notification
  `fyne.App.SendNotification` spawns a PowerShell process for.
- **Windows drops cgo.** Gio needs no extra dependencies there; CI's
  `CGO_ENABLED=1` and the Ubuntu header install stay for the other two legs
  (Gio wants wayland, x11, xkbcommon, GLES, EGL, libXcursor — near enough the
  same list).

**Worse, in two that matter more:**

- **Gio is 0.10 after nine years, with a history of sweeping breaks.** The
  February 2024 event-routing rewrite changed how every widget receives input;
  October and November 2023 each broke event processing and window logic. v0.10
  landed May 2026 after a long quiet stretch. Trading a fork *you control and
  can pin* for an upstream that has twice rewritten its central abstraction is
  not obviously a reduction in risk — it moves the work from a scheduled rebase
  to an unscheduled one.
- **`gioui.org/x` is a second module with a second maintenance story.** Context
  menus, tooltips, modals and rich text — all load-bearing here — live in
  `x/component` and `x/richtext`, not in Gio proper. `x/component`'s own docs
  note the menu has no submenus yet.

And the cost that does not show in a line count: **`ui/CLAUDE.md` is 328 lines of
footguns**, every one of them learned by hitting it. Innermost object wins the
hover. A wrapping widget answers `MinSize` with the width it was last given.
A recycled row must own nothing it captured. A discarded widget hears nothing.
None of those specific traps exist in Gio — and an equivalent list does, unwritten,
against a toolkit with a fraction of the documentation and Stack Overflow
surface. That file is the real asset the rewrite discards.

## Verdict

Don't. The performance case rests on a GPU-side win in an app whose costs are
CPU-side and already inside budget; the power case was closed by a patch that
already shipped; the maintenance case is a genuine wash that a text-rendering
regression tips negative.

What is cheaper and points the same direction:

1. The two items still unspent in `performance.md` — optimistic local echo, and
   an audit of what every gateway handler `Refresh`es.
2. **A D3D11 `gl.Painter` in the fork.** It captures the flip-model presentation
   win, which is the only part of the Gio case that survives scrutiny, without
   touching a widget. Reversible; the fork is already ours.
3. **Split `App` into controller and view anyway.** It is the prerequisite for
   any toolkit move, it makes `app`'s logic testable without mounting anything,
   and it is worth doing if the answer here is never revisited.

# Independent flip, and the GL-to-D3D path to it

Unbuilt. This is the design for reaching DWM's overlay planes from a fork whose
painter is OpenGL, why the obvious route does not exist, and what the middle
route would cost. Companion to `docs/performance.md`, which is where the numbers
would land if it were ever built.

## The wall

Multi-plane overlay — MPO — is DWM handing a window's back buffer straight to
the display controller's own plane, skipping composition. What unlocks it is
**independent flip**, and what unlocks that is a **DXGI flip-model swapchain**
(`DXGI_SWAP_EFFECT_FLIP_DISCARD` or `FLIP_SEQUENTIAL`).

WGL cannot create one. A GLFW window is an `HWND` with a WGL context on its
`HDC`, and `SwapBuffers` presents through DWM's **redirection surface** — the
blt model, one composition copy per present, no plane, no independent flip. No
pixel format, no WGL extension and no swap-interval value changes that; the
model is decided by how the surface was created, and WGL only creates the old
one.

This is the same wall `docs/performance.md` names for Gio ("a flip-model
swapchain and a DXGI waitable object are available to it and structurally are
not to us"). MPO is that wall rather than a second one, and the fourth patch —
`D3DKMTWaitForVerticalBlankEvent` — is the client living behind it: pacing to
the blank because it cannot be handed a plane.

## The middle route

One path reaches a flip-model swapchain without replacing `gl.Painter`:
**`WGL_NV_DX_interop2`**. Despite the vendor prefix every current NVIDIA, AMD
and Intel Windows driver exposes it.

1. Create a D3D11 device and a flip-model swapchain for the window
   (`IDXGIFactory2::CreateSwapChainForHwnd`, `BufferCount` 2-3,
   `DXGI_SWAP_EFFECT_FLIP_DISCARD`, `ALLOW_TEARING` where the setting asks).
2. `wglDXOpenDeviceNV` on that device, then `wglDXRegisterObjectNV` each
   swapchain back buffer as a GL renderbuffer.
3. Per frame: `wglDXLockObjectsNV`, bind the renderbuffer as the painter's
   framebuffer, draw exactly as now, `wglDXUnlockObjectsNV`,
   `IDXGISwapChain::Present`.

The painter does not change — it draws into a framebuffer either way. What
changes is `glCanvas`'s surface and `repaintWindow`'s present, both in the fork.

`DXGI_SWAP_CHAIN_FLAG_FRAME_LATENCY_WAITABLE_OBJECT` comes with it: a real
present gate, waited on like any handle, replacing the D3DKMT blank wait with
the thing that call was standing in for.

## What it buys

- **A composition copy per present, gone.** DWM stops copying the window into
  the desktop surface when the plane takes it; on a battery that is GPU time
  and memory bandwidth not spent.
- **Latency.** Blt-model presents are queued behind DWM's composition pass;
  independent flip is not.
- **The waitable object.** `frame latency` is what DXGI was built to answer and
  the D3DKMT wait is a substitute for. It also paces to the window's *own*
  monitor, which the current gate does not — it opens the primary adapter,
  because the `HWND` does not exist when `newPresentGate` is called.
- **A YUV plane, in principle.** MPO's real prize is a video surface scanned
  out without a colour convert. Reaching it means the screenshare and video
  cards presenting on a plane of their own rather than as quads inside the UI,
  which is a second design and not this one.

## What it costs

- **It is a fork change to window creation and presentation**, not a patch to a
  function. Every path that makes a window, resizes one, or reads back pixels
  (`Capture`) has to be taught about the swapchain, and a resize is a swapchain
  resize with every registered buffer unregistered and re-registered around it.
- **Two graphics stacks in one process.** D3D11 device loss, adapter removal
  and mode changes all become states the client has to survive, and the GL path
  has to stay as the fallback for a driver without the extension — so it is a
  second presentation path, not a replacement for the first.
- **The prize is small here.** The measured frame is prep ~420 µs, damage
  ~180 µs, draw ~400 µs, swap ~500 µs, and the client draws **zero frames at
  rest**. Independent flip attacks the swap, on the frames a chat window is not
  drawing. What it would actually be worth is a screenshare watched full-screen
  for an hour on a laptop.
- **Windows only**, and it earns nothing on the two platforms where the
  compositor is already doing the right thing.

## Verdict

Not taken. The honest trigger for revisiting it is a *measurement*, not a
feature: a screenshare or video playback session where `RGO_FRAMETIME`'s swap
phase dominates and the machine is on battery. Absent that, the fourth patch
already buys the part that mattered — not presenting frames DWM would throw
away — and this buys the part that does not.

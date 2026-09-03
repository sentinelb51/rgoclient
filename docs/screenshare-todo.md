# Screenshare: the work queue

Sending and receiving a screenshare in voice calls. Both directions are now
built for X11 and Windows; this file is the design, what each half actually
does, and what is still owed — grounded in what was verified against the
pinned dependencies and the backend (file:line where it matters). The voice
queue that used to live at this path is basically done — what it still owed is
carried at the tail, and `git log docs/voice-chat-todo.md` keeps the full
record.

Written to be read cold. Companion to `known-gaps.md` and `performance.md`;
`video-player.md` holds the threat model this reuses.

## The shape

Stoat's voice is LiveKit, and a screenshare is just a **video track in the room
the call already holds** — no new signalling, no new route. Both directions are
the video player's architecture pointed at a live stream:

- **Sending** is one ffmpeg child that captures, scales and encodes in a
  single pass, writing to stdout what the tee frames and voice's own write
  loop publishes the moment each frame arrives: AV1 in IVF where the GPU
  encodes it — probed per family, hardware or nothing, no CPU holding a live
  AV1 encode — and H.264 in FLV otherwise (never bare Annex-B: the
  container carries every frame's length, and a tee without lengths can only
  close a frame at the next one's start code), hardware where the machine
  has any (NVENC, AMF, QSV or VAAPI, probed once per run with a test encode)
  and libx264 at the last. H.264 as the floor *because* of that: no
  VP8 encoder exists in silicon on any GPU. AV1 ships the same picture at
  ~0.7× the bitrate (`app.shareAV1BitrateScale`), the gain taken as
  bandwidth; a room that refuses the AV1 publish is retried as H.264, and
  `config.Screenshare.Codec` forces H.264 for viewers that cannot take AV1.
- **Receiving** is lksdk handing back encoded frames, remuxed to the same byte
  stream and fed to a **sandboxed** ffmpeg child on stdin, which answers RGBA
  at the size the view chose — the video player's exact-byte contract, live.

The DAG holds without a new edge: `voice` gains a video seam declared
structurally (an `io.Writer` of muxed bytes, the way `PCMSource` is a reader of
PCM — it never imports `video`), `video` gains live stream variants (stdin in,
no file), and `app/screenshare.go` is where they meet, exactly as
`app/video.go` and `app/voice.go` are. The call view that draws it is its own
work and not covered here; everything below ends at "RGBA frames arrive on the
UI thread" and "options the view offers".

## Facts worth not rediscovering

Verified against lksdk v2.18.1 and this machine's ffmpeg; line numbers are that
module's.

- **`NewLocalReaderTrack` is not used, and must not come back**: its
  writeWorker (`localtrack.go:635`) sends exactly one sample per frame
  duration off an absolute schedule of its own (`nextSampleTime`), catching
  up only when *behind the schedule* — never when frames are queued ahead of
  it. A backlog standing when the loop starts is therefore replayed at 1×
  for the life of the share, and one always stands: the encoder runs through
  the whole publish negotiation and nobody reads until `OnBind`. Every
  ddagrab stutter after that (a timestamp gap the `fps` filter fills with
  duplicate frames) ratchets it further. Measured as a ~5 s glass-to-glass
  delay every viewer kept — invisible in the self-preview, which taps the
  tee upstream of where the queue stood (for H.264 inside pion's h264reader
  buffer, for AV1 in the pipe). `voice.pumpShareSend` +
  `LocalTrack.WriteSample` replace it: frames written on arrival, `Duration`
  stepping the RTP timestamps (honest — the child's `fps` filter holds the
  stream CFR), pre-bind and pre-keyframe frames dropped rather than queued.
  A pre-bind `WriteSample` would be a silent no-op anyway (`packetizer ==
  nil`), but dropping to the next keyframe keeps the entry point decodable.
  The **one slice per frame** contract stays (x264's zerolatency tune breaks
  it until sliced-threads=0 unwinds it; hardware encoders default to one) —
  no longer for pacing, but because the tee recognises an access unit by its
  slice.
- **`PublishTrack` takes the source**: `TrackPublicationOptions{Source:
  livekit.TrackSource_SCREEN_SHARE, VideoWidth, VideoHeight}`, and
  `TrackSource_SCREEN_SHARE_AUDIO` exists for the sound half — a second,
  ordinary Opus track. Both are what tells every other client "this is a share,
  not a camera".
- **`RemoteParticipant.WritePLI(ssrc)`** (`remoteparticipant.go:165`) — the
  receive side can demand a keyframe, which is what makes watch-start and
  decoder restarts converge in a frame rather than a GOP.
- **`RemoteTrackPublication.SetSubscribed(bool)`** — subscription is per track
  and reversible, which is what click-to-watch is made of. `SetVideoQuality`
  only means anything on a simulcast publication.
- **`dial` inherits lksdk's `AutoSubscribe: true`** (`room.go:465`) and
  `subscribe.go`'s kind guard drops video *after* it is downloaded — so today a
  share in the room costs every rgoclient its full bitrate to discard. Phase 0
  below is the fix and is shippable alone.
- **x11grab takes `-window_id`** (checked `ffmpeg -h demuxer=x11grab`), so X11
  window capture is the same child as monitor capture. Stock Debian ffmpeg
  ships x11grab, libx264, libvpx, and the hw encoders (nvenc/qsv/vaapi) that
  light up where drivers exist.
- **malgo exposes miniaudio's `Loopback` device type** (`enumerations.go:32`) —
  WASAPI loopback, so sharing system audio on Windows needs no virtual device
  and no new dependency: it is a capture device the existing publisher path can
  read.
- **Stoat's token does grant video publish** — verified against the backend
  and against the live instance. `voice_client.rs:87` sets
  `can_publish_sources` from `get_allowed_sources`, which lists `camera`,
  `screen_share` and `screen_share_audio` when the channel grants
  `ChannelPermission::Video` and the tier has `video = true`
  (`core/database/src/voice/mod.rs:161-170`). `tokenAllowsScreen` reads it
  back: an absent or empty list allows every source, `canPublish: false`
  forbids all of them.
- **The instance publishes what it will enforce**, at `GET /` under
  `features.limits` — `video`, `video_resolution` (a *pair whose product* is
  the area cap, not a box), `video_aspect_ratio`, and `global.new_user_hours`
  choosing the tier. Live at stoat.chat today: new_user `[1080, 720]`,
  default `[1280, 720]`, both `[0.3, 2.5]`, both `video = true`. revoltgo's
  `InstanceConfig` **drops the whole limits block**, so it is asked by hand.
- **Over the limit is a disconnection, not a refusal**
  (`daemons/voice-ingress/src/api.rs:296-320`): the ingress calls
  `remove_user` and deletes the voice state. This is why the sender fits its
  own box rather than letting the server say no.

- **Every held frame is latency, and there were six of them** — found with
  the clock harness (`internal/app/screenshare_live_test.go`, run with
  `RGO_SHARE_LIVE=1 RGO_SHARE_CLOCK=scripts/share-clock.py`: both saved
  accounts in `jerome / vc`, one sharing a window whose picture *is* the
  wall clock, the other decoding it back). Each was a whole frame at any
  rate — 33 ms at 30 fps, 200 ms at 5 — and none showed as anything but
  "a bit behind": (1) the decoder's rawvideo output ran **frame-threaded**
  by default and frame threading holds one frame, even raw-in raw-out
  (`-threads 1` after the input; the decoder's own threads are before it);
  (2) the `-f h264` demuxer's parser closes an access unit only at the next
  start code (H.264 now rides IVF under the `H264` fourcc, which ffmpeg's
  demuxer maps like any other — its *muxer* refuses it, hence FLV on the
  send side); (3) pion's `samplebuilder` holds a complete frame until the
  packet after it arrives, and its flush drops what is still in flight
  (`voice.frameAssembler` replaces it: same reorder buffer, emits at the
  marker, waits `shareReorderWait` for a retransmission before giving a
  frame up); (4) the `fps` filter waits for the frame after to choose
  between them (the output's own `-fps_mode cfr -r N` fills and drops on
  the frame in hand; still needed, `gfxcapture` idling at ~3 fps on a still
  window); (5) the Annex-B tee, same reason as (2) (FLV, whose tags the tee
  hops by; the muxer converts NVENC's start codes to length prefixes and
  moves the parameter sets into a sequence-header tag, so the tee puts them
  back in front of every IDR); and (0), the one that was seconds rather
  than a frame: the decoder's default sync is *constant* rate for a muxer
  with no timestamps, and IVF's 90 kHz timebase had it duplicating every
  frame a thousand times over — a pipe standing 13–27 s deep, growing
  (`-fps_mode passthrough`). `docs/performance.md` has the ledger.
- **lksdk keeps a publication's `TrackRemote` after an unsubscribe** —
  nothing clears it short of an unpublish, and `IsSubscribed` reads the same
  field. A second watch of one share that trusted it started its reader on
  the dead track, read EOF at once and reported the share ended while the
  fresh subscription was still landing. Every `SetSubscribed(true)` yields a
  new track and a new `OnTrackSubscribed`, so a watch only ever waits for
  the callback.
- **`-fflags nobuffer` breaks a piped IVF outright**: half the frames lost
  and the rest 1.6 s late, measured again in 2026-09. Not the answer to
  anything here.

## Phase 0 — built

- **Unsubscribe video publications** — built. `registerPublication` in
  `voice/share.go` switches every video and share-audio publication off as it
  appears, and `sweepPublications` at the join covers what was already in the
  room, so a share costs nothing until watched. `WatchShare` is what turns
  exactly one back on.
- **The JWT grant check** — built (`tokenAllowsScreen`), and the answer is
  yes: `voice_client.rs:87` mints `canPublishSources` from
  `get_allowed_sources`, which appends `camera`, `screen_share` and
  `screen_share_audio` whenever the channel grants `ChannelPermission::Video`
  and the account's tier has `video = true`
  (`core/database/src/voice/mod.rs:167`). Both stoat.chat tiers have it on.
  The token is read rather than verified — it is what greys the button, and
  the server is still the one enforcing.

## Receiving — built

`voice/share.go` → `video.LiveFrames` → `app/screenshare.go`, the path as
designed; what stands, and what is still open on this side:

- **Watch**: the row's live mark (`ui.shareWatchTap`, drawn from the
  gateway's `Screensharing` flag) → `App.OnWatchShare` →
  `Call.WatchShare(userID, open)`. This account's own mark is the same tap
  and lands on `startSelfPreview` instead — the same window, child and pump,
  fed by the tee — so the one-watch rule covers both and watching somebody
  else replaces the preview. voice subscribes the share's video and
  audio publications, and when the track lands runs `open(codec, w, h)` on
  its own goroutine — the app launches the decoder and answers with its
  stdin. The seam stayed structural: `open` answers an `io.WriteCloser`, and
  `voice` imports nothing of `video`.
- **Depacketising is pion's; reassembly is ours** (`VP8Packet` /
  `VP9Packet` / `AV1Depacketizer` / `H264Packet`, the last emitting Annex-B
  with `IsAVC` unset, driven by `voice.frameAssembler` rather than
  `samplebuilder` — see "Facts"). The remux is `ivfMux` for every codec
  (timebase 1/90000, RTP timestamp unwrapped as the pts; keyframe-gated for
  VP8, AV1 and H.264, the last under the `H264` fourcc), holding the stream
  until a frame a decoder can enter on. `WritePLI` fires at watch start and,
  throttled to one per 500 ms, per frame the assembler gives up on.
- **The decoder is `LiveFrames`**: full sandbox minus the media bind, `-f
  ivf` always, the pad-scale filter keeping the byte-per-frame contract
  through a mid-stream source resize, `-threads 1` on the output side and
  `-fps_mode passthrough` — one frame out per frame in, the two ffmpeg
  defaults that each held frames (see "Facts"). The latency flags are
  `-flags low_delay -analyzeduration 0 -probesize 32` and nothing else:
  **`-fflags nobuffer` breaks a piped IVF**.
- **Pacing**: the pump reads on arrival — no wall clock, the sender paces
  the stream — and always drains; the painter sits behind a latest-wins
  mailbox (three rotating buffers, `paintShareFrame`) so a stalled UI thread
  (a window dragged on Windows blocks painting for seconds) costs dropped
  frames, never a stall backed up through the decoder into standing delay.
  The share window is its
  own `fyne.Window` with its own canvas, so a frame repaints nothing of the
  main window. `endShareWatch` / `closeShare` / `settleShareEnd` are the
  teardown meeting points; `dropCall` closes the watch with the call.
- **Share audio** is the lane machinery unchanged under
  `voice.ShareLane(userID)`, its own volume on the participant menu
  ("Screenshare volume"), persisted under the same map as the user gains.
- **A stats card** (resolution, FPS, codec, one joined line) sits over the
  picture's top-left corner — `shareStats`, the settings page's own
  invite-card surface (`SessionCardBg`, `SettingsGroupRadius`, the lighter
  `SettingsIslandOutline`, a lifted shadow) rather than the video card's
  translucent chip, this window being its own surface rather than a control
  on a page. Resolution and codec are known at mount; FPS is measured off
  the pump's own arrivals (`pumpShareFrames`, once a second), nothing on the
  wire carrying the sender's chosen rate, and the clause is absent from the
  line until the first second lands. The self preview's codec clause
  parenthesises the encoder (`"AV1 (NVENC)"`) rather than middot-joining it
  the way the three peer facts are — a middot there once read as "AV1 or
  NVENC, which one" rather than "AV1, encoded by NVENC".
- Still open on receive: a **watch resolution option** (today the sender's
  declared size capped at 1080p; relaunch-on-resize wants a voice-side
  re-open seam); `SetVideoQuality` once a simulcast sender exists (AV1 is
  received now — pion's `AV1Depacketizer` emits the low-overhead bitstream,
  which is exactly an IVF frame, so it rides the ivfMux as `AV01` gated on a
  sequence-header unit); VP9 keyframe *gating*
  (unparsed; the decoder skips to one at the cost of a logged complaint); and
  the backpressure edge: a *decoder* slower than the stream backs up into
  pion's receive buffer and drops until the next PLI — self-limiting, not yet
  measured on a weak machine (a slow *painter* no longer backs up at all;
  the mailbox drops for it).

## Sending — built (monitors and windows, X11 + Windows)

`ui.ShareDialog` → `app/screenshare.go` → `video.CaptureShare` →
`Call.StartShare` → `PublishTrack(Source: SCREEN_SHARE)`. The path as designed;
what stands, and what the build turned up:

- **The button is the call island's**, beside the microphone and the
  headphones: live in `VoiceShareLive` while this machine is sharing, offered
  in the card's own text colour otherwise, and greyed where the join token
  does not grant it (`Call.CanShare`). A tap stops a running share and opens
  the picker otherwise.
- **The picker** is the modal layer's own card: the sources under two
  headings, then quality (Source / 1080p / 720p / 480p, the *short* edge and a
  ceiling — nothing is upscaled) and frame rate (5 / 15 / 30 / 60) as chip
  runs. Answers are remembered in `config.State`, which is where the client
  keeps what it was told rather than what was chosen.
- **Enumeration is native**, ffmpeg listing nothing. X11 is `jezek/xgb` — a
  pure-Go connection of our own, the toolkit's belonging to glfw — with RandR
  for the monitors and the EWMH `_NET_CLIENT_LIST` for the windows; Windows is
  `EnumDisplayMonitors` / `EnumWindows` through `x/sys`, the `cpu` package's
  precedent, skipping cloaked, minimised, tool-window and untitled handles.
  The callbacks are built **once** at package level: `syscall.NewCallback`
  slots are never freed and the process's allowance is small.
- **One child does everything**: grab → scale+pad → encode → IVF or FLV on
  stdout, held to the asked-for rate by the output's own CFR sync rather
  than an `fps` filter (a frame of latency — see "Facts"), which the tee
  frames into the samples lksdk packetises. The pad half of the filter
  keeps the declared size true through a window resized mid-share.
- **Windows capture is a three-rung ladder and the top rung is Graphics
  Capture** (`gfxcapture`, ffmpeg's WGC filter source), for a window and a
  monitor alike. It takes the `HWND` or `HMONITOR` *itself*, so a window whose
  title changes between enumeration and start is still the one that was
  picked, and it is the only grabber here that **scales before handing the
  frame over** — so the readback is the encode box rather than the whole
  screen, and swscale is left with a format conversion instead of a resize.
  Measured on an RTX 4070 Laptop, 2560×1600 monitor into a 1280×720 share at
  30 fps: **0.23 s of process CPU over fifteen seconds becomes 0.05 s**, same
  450 frames out. Bicubic, the resampling being the GPU's either way. It fits
  rather than fills and pads to the *top-left*, so the chain's own centred pad
  stays — which is also what holds the declared size true through a window
  resized mid-share. Its own device is opened by the filter, so unlike ddagrab
  there is no `-init_hw_device` to become every other filter's default. One
  probe per run answers whether the machine has it at all (the API is Windows
  10 1803+, the filter newer again, and an ffmpeg found on PATH may predate
  it); WGC also has no exclusivity, so several clients may capture one output,
  and it captures a window that is covered.
- **`ddagrab` is the middle rung**, monitors only, and was the top one until
  Graphics Capture arrived. Desktop Duplication hands back a GPU surface too,
  but full-size — there is no way to ask it for less — so the readback is the
  whole screen. It is also **refusable at runtime**: `Desktop duplication
  access denied` was reproduced on the development laptop with nothing else
  capturing, where `gfxcapture` on the same output worked, which is on its own
  a reason not to have it first. Kept because it answers on Windows and
  ffmpeg versions predating the WGC filter.
- **`gdigrab` is the floor**, and the reason the ladder exists.
  gdigrab's `BitBlt` carries `CAPTUREBLT`,
  which redraws the mouse pointer once per captured frame — *on the machine*,
  for everybody at it, not only inside the stream. Measured here: no flicker
  with ddagrab at 5 fps, obvious flicker with gdigrab at the same rate, and at
  60 fps it beats against the panel's refresh into a slow blink instead of
  disappearing. ddagrab is a **filter source**, not an input device, so the
  grab seam answers with either args or a filter (`video.grab`) and the child
  gets `-filter_complex` instead of `-vf`.
  Two things it needs and gdigrab did not: a D3D11 device
  (`-init_hw_device d3d11va:<adapter>`) and `hwdownload,format=bgra` to bring
  the surfaces where `scale` can reach them. `output_idx` counts within *one*
  adapter, so both numbers are needed — which is what `dxgiOutputs` is for: a
  hand-rolled COM walk of `IDXGIFactory`→adapters→outputs, keyed by the
  `HMONITOR` each output reports, which is the one key `EnumDisplayMonitors`
  also hands out. Matching on that rather than trusting two enumerations to
  agree in order is the whole point. It is walked **where ddagrab is what will
  be used** rather than at enumeration: a source carries the bare `HMONITOR`,
  the same shape a window carries its `HWND`, so a machine with Graphics
  Capture never pays a COM walk per opening of the picker for an answer
  nothing reads. **`IID_IDXGIFactory1` is not
  `770aae78-…`** whatever the memory says — `CreateDXGIFactory1` answers
  `E_NOINTERFACE` for it; the plain `IID_IDXGIFactory` (`7b7166ec-…`) through
  `CreateDXGIFactory` works and carries `EnumAdapters` at the same slot.
  Availability is **probed**, once per *address* per run, by grabbing a single
  frame to `null`: ffmpeg predating the filter and a session with no output to
  duplicate (RDP) both answer no, and both fall back to gdigrab. The key is the
  address alone — a key carrying the frame rate re-probes the same output once
  per rate, which it did until it was caught. Probing costs a few hundred ms on
  the worker enumerating for the picker, where finding out at the first frame
  would cost a live track that publishes nothing. `Tools.CaptureFallback` asks
  the same probes about the whole enumerated set, which is what lets the picker
  **warn** that a share will run on the slower path — the one limit here that
  can be fixed from outside the client. Graphics Capture takes both kinds of
  source, so one answer now settles the set: with it, nothing warns.
  `docs/performance.md` carries why the path matters beyond the flicker.
- **Self-preview is a tee, not a subscription.** LiveKit never sends a
  publisher their own track back, so the bytes only exist twice if this side
  makes them: `video.ShareTee` frames the capture child's stdout — the
  publisher takes every whole frame through `ReadFrame`, and a copy of each
  is offered to whatever is watching locally. The preview's copy is **lossy
  on purpose** — the publisher must never wait on a preview, or a decoder
  stalled behind a blocked UI thread (dragging a window is enough on
  Windows) would stall the share everybody else is watching — so a frame the
  preview is behind on is dropped.
  Dropping is safe because every frame the preview reads carries its own
  length: a gap costs the decoder its prediction until the next keyframe,
  never the framing. The tee parses whether or not anybody is attached —
  both containers are hopped by their lengths, IVF's frame headers and
  FLV's tags — and a preview opened mid-share is handed a file header
  first (the encoder's own for AV1; one written here for H.264, whose
  access units the tee frames as IVF under the `H264` fourcc, the
  decoder's one format), the latest SPS/PPS next, then gated to the next
  keyframe, so the decoder is not handed a mid-GOP stream to complain
  about. The assembly is one copy of the stream, and it is the publisher's
  sample buffer, so a share nobody previews pays nothing extra. `Attach` after
  `Close` is
  refused rather than left waiting on frames that can no longer come.
- **The child is contained, not sandboxed.** The strict profile forbids what
  capture *is* — bwrap's `--unshare-all` severs the X11 socket, the Windows
  low-integrity token cannot BitBlt another program's window — and the input
  is this machine's own screen, so the player's threat model does not apply.
  What is kept is the resource half: priority, memory cap, no core files,
  kill-on-parent. **No CPU-seconds cap**, unlike a decode: an encoder honestly
  spends an hour of CPU on an afternoon of sharing, which is what `RLIMIT_CPU`
  would kill mid-call.
- **The publish limits are a disconnection, not a refusal.** `voice-ingress`
  measures the *declared* width×height on `track_published` and, over the
  tier's area or outside its aspect band, calls `remove_user` and drops the
  voice state (`daemons/voice-ingress/src/api.rs:296-320`). So the box is
  fitted here first: `Client.VideoLimits` fetches the tiers by hand — revoltgo
  parses `features` but drops `features.limits` — and `fitShareBox` applies
  them. **The order is load-bearing and is not the obvious one**: area, then
  even, then aspect. Both of the first two truncate, and truncating the two
  edges independently *moves the ratio* — an ultrawide fitted exactly to 2.5
  comes out of the area scale at 2.502 and is refused. Aspect goes last and
  only ever shrinks, so the area already fitted still holds, and it aims a
  percent inside the band: the ingress compares in `f32` where this computes
  in `float64`. Before the fetch lands, the *new-user* tier's numbers are
  assumed — guessing low is the point, the cost of guessing high being
  somebody's call.
- **The track is written on arrival and paced by nothing** — the design got
  this wrong twice, in opposite directions. First: a sample's duration must
  not come off the stream's own timestamps (`testsrc` gives timebase 1/30
  stepping by 1 while **x11grab gives 1/15 stepping by 15**, a second per
  frame through lksdk's arithmetic), so the asked-for rate stamps every
  frame — honest because the child's output sync holds the stream CFR.
  Second, the ~5 s one: lksdk's paced writeWorker preserves any standing
  backlog forever — see "Facts" above — so `voice.pumpShareSend` writes
  each frame as the tee completes it, drops what arrives before the track
  binds or before the first keyframe after it, and the pipe can never stand
  between the screen and the room as delay.
- Every stop path meets in `stopSharing` / `onShareStopped`: the button, the
  encoder dying (the captured window closed — `OnWriteComplete` → unpublish →
  `ShareStopped`), `dropCall`, and logging out.
- Still open on send: **share audio** (a second Opus track from a WASAPI
  loopback or a Pulse monitor), **Windows window capture
  without the pointer flicker** (WGC, which is WinRT and has no ffmpeg input),
  **macOS** (avfoundation screens plus the consent prompt), and **Wayland**,
  which has no grabber in stock ffmpeg — the portal is its own project, so a
  machine with no X says so in the picker and offers nothing.

## Options (the Discord-parity surface)

All read at share start and applied by **restarting the child** — a share
restart costs one GOP, which is what makes every option cheap to honour
mid-share. What the picker was last answered with is remembered in
`config.State` (not a settings section: no row writes to it, and it is what the
client was told rather than what was chosen), so the same monitor at the same
rate is one press next time.

Source, resolution, framerate and the bitrate override are built; the rest of the
table is still the plan.

| Option | Values | Where it lands |
| --- | --- | --- |
| Source | monitors, windows (X11/Windows) | grabber input args |
| Resolution | Source / 1080p / 720p / 480p | `scale` (+`pad` never needed on send: the source aspect is known) |
| Framerate | 5 / 15 / 30 / 60 | grabber `-framerate` / `max_framerate`, and the output's `-fps_mode cfr -r N` filling what a still screen does not deliver |
| Quality | Auto / bitrate override | **built** — `-b:v/-maxrate/-bufsize`; auto ≈ 0.1 bit per pixel per frame (720p30 ≈ 2.5 Mbps, 1080p30 ≈ 4.5, 1080p60 ≈ 8). The override is Bandwidth's fourth value (Custom) plus `Screenshare.Bitrate` in kbit/s, bounded by what the encoder clamps to |
| Audio | on / off | the loopback capture + second track; greyed on macOS |
| Codec | Auto (AV1 where the GPU encodes it) / H.264 | AV1 hardware-only, ~0.7× the bitrate for the same picture; H.264 the floor, libx264 at the last; VP8/VP9 remain receive-only, for senders that are browsers |
| Bandwidth | Auto / Half / Quarter | a scale on the automatic bitrate budget, the slow-uplink dial |
| Keyframes | Frequent (1 s) / Standard (2 s) / Sparse (8 s) | `-g`; the whole loss-recovery story — a CLI encoder cannot answer a PLI, so the interval is both the join wait and the smear after loss |
| Bitrate mode | Variable / Constant | **built** — the mode half of the rate control, spelled per encoder in `shareEncoder.rateControl` (x264's `nal-hrd=cbr` travelling with the tune in `args`). Variable is the default and the saving; constant pads to the ceiling for a fixed uplink or an estimator that dislikes a stream idling at nothing between bursts |

Receive-side options are the view's: which share is watched (one at a time
first — one decoder child, the one-playback rule), and the watch resolution,
which is the decoder's W×H and free to differ from the sender's.

## Order of work

0. ~~Unsubscribe unwatched video~~ **done**; the JWT grant check remains, and
   is the first task of the send half.
1. ~~`video.LiveFrames`~~ **done** — validated offline against a recorded IVF
   (count the bytes, check the letterbox corner).
2. ~~Receive~~ **done** — see "Receiving" above for what stayed open.
3. ~~Send, monitors, X11 + Windows~~ **done** — and windows with them, the
   enumeration being the same walk. See "Sending" above.
4. **Options still owed**: ~~the bitrate override~~ **done** (Bandwidth's
   Custom value plus `Screenshare.Bitrate`, read at share start like every
   other option); audio share (WASAPI loopback, pulse monitor) — and the
   receive-side watch resolution beside them.
5. **Latency**: ~~the six held frames~~ **done** (2026-09, see "Facts" and
   `docs/performance.md`). Left: the **join wait**, which is the keyframe
   interval — a CLI encoder cannot answer a PLI, so a viewer waits up to
   `Keyframes` seconds (1 / 2 / 4) for a picture; and **hardware decode**
   for the watch (NVDEC/D3D11VA inside the sandbox is unexplored; libdav1d
   and the native H.264 decoder are on the CPU today).
6. **Polish**: ~~self-preview tee~~ **done**; ~~ddagrab~~ **done**;
   ~~hw-encoder probe~~ **done** (and the codec moved to H.264 outright — the
   probe order, flags and the one-slice contract live in
   `video/capture.go`); ~~AV1~~ **done**, both directions (hardware-only
   send at a bitrate discount, IVF through the same tee and reader track;
   receive through pion's depacketizer into the ivfMux). Left: macOS screens,
   the Wayland portal investigation, and Windows window capture through WGC.

## Carried from the voice queue

Still open when the voice file was retired; `git log` on the old path has the
full history and measurements.

- **The `rvoice` lift** (`internal/voice` → `sentinelb51/revoltgo-voice`). Was
  waiting on two-way audio and the jitter question; now also wait for the video
  seam above to settle — it moves the surface again.
- **Echo cancellation** — the biggest quality gap for anyone on speakers;
  `audio.Processor` is the seam and `Engine` holds both directions for it.
- **Push-to-talk beyond Windows** (X11 `XQueryKeymap`, macOS Accessibility);
  **node selection UI** (matters once an instance publishes several nodes);
  **call recording**; **DRED / OSCE** (worth nothing until the far end is this
  library too); **libopus 1.6.1** (the bump must carry `dnn/` and re-check
  `DEEP_PLC_SOURCES`).
- **Verification owed**: interop with the official Stoat web client (the only
  honest mouth-to-ear number, and a browser *viewer* of a share has not
  been tried — it will take H.264 and AV1 from the room like any other
  subscriber, but nobody has looked); concealment listened to, not just measured;
  unplugging the microphone mid-call on real hardware; the settings-picker
  device swap while somebody can hear; pulling the network mid-call; logging
  out mid-call leaving no device open; the settings-search no-device check by
  hand; the macOS and Linux CI legs actually compiling gopus.
- **Housekeeping that still holds**: `voice-test` in Big up testers exists for
  live checks; `gopus` is required directly at a `master` commit (no
  `replace`, deliberately); `revoltgo` is pinned at the commit carrying the
  voice additions and `update-deps.sh` is what moves it.

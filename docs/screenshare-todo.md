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

- **Sending** is one ffmpeg child that captures, scales and encodes in a single
  pass, writing an IVF (VP8) or Annex-B (H.264) byte stream to stdout, which
  lksdk packetises and publishes (`NewLocalReaderTrack` takes exactly that).
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

- **`NewLocalReaderTrack(io.ReadCloser, mime)` eats an encoder child's stdout
  verbatim** (`readersampleprovider.go:226`): Annex-B for H.264/H.265, IVF for
  VP8/VP9/AV1 — `NewLocalFileTrack` sniffs the codec from the IVF header's
  FourCC. **IVF frames are paced by the IVF timebase** (`:482`), so a VP8 child
  carries its own clock; **Annex-B has no timestamps** and defaults to a fixed
  33 ms per frame, so H.264 needs `ReaderTrackWithFrameDuration(time.Second/fps)`.
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
  `Call.WatchShare(userID, open)`. voice subscribes the share's video and
  audio publications, and when the track lands runs `open(codec, w, h)` on
  its own goroutine — the app launches the decoder and answers with its
  stdin. The seam stayed structural: `open` answers an `io.WriteCloser`, and
  `voice` imports nothing of `video`.
- **Depacketising is pion's as planned** (`samplebuilder` + `VP8Packet` /
  `VP9Packet` / `H264Packet`; `H264Packet` does emit Annex-B with `IsAVC`
  unset). The remux is `ivfMux` (timebase 1/90000, RTP timestamp unwrapped as
  the pts, keyframe-gated for VP8) and `annexBMux` (IDR-gated); both hold the
  stream until a frame a decoder can enter on. `WritePLI` fires at watch
  start and, throttled to one per 500 ms, per hole the reassembler reports
  (`PrevDroppedPackets`). Verified offline: frames carved out of a recorded
  IVF, re-muxed with synthetic timestamps, decode whole through `-f ivf`.
- **The decoder is `LiveFrames`**: full sandbox minus the media bind, `-f
  ivf`/`-f h264` forced by the caller, the pad-scale filter keeping the
  byte-per-frame contract through a mid-stream source resize. One flag from
  the design did not survive contact: **`-fflags nobuffer` makes the IVF
  demuxer misframe a piped stream** (every packet refused as invalid), so the
  latency flags are `-flags low_delay -analyzeduration 0` (+`-probesize 32`
  for IVF) and nothing else.
- **Pacing**: the pump paints on arrival under a waited hop — no wall clock,
  the sender paces the stream — and always drains. The share window is its
  own `fyne.Window` with its own canvas, so a frame repaints nothing of the
  main window. `endShareWatch` / `closeShare` / `settleShareEnd` are the
  teardown meeting points; `dropCall` closes the watch with the call.
- **Share audio** is the lane machinery unchanged under
  `voice.ShareLane(userID)`, its own volume on the participant menu
  ("Screenshare volume"), persisted under the same map as the user gains.
- Still open on receive: a **watch resolution option** (today the sender's
  declared size capped at 1080p; relaunch-on-resize wants a voice-side
  re-open seam); `SetVideoQuality` once a simulcast sender exists; **AV1**
  (OBU remux unbuilt — refused with the codec named); VP9 keyframe *gating*
  (unparsed; the decoder skips to one at the cost of a logged complaint); and
  the backpressure edge: a consumer slower than the stream backs up into
  pion's receive buffer and drops until the next PLI — self-limiting, not yet
  measured on a weak machine.

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
- **One child does everything**: grab → `fps` → scale+pad → libvpx realtime →
  IVF on stdout, which is exactly what `NewLocalReaderTrack` eats. The pad
  half of the filter keeps the declared size true through a window resized
  mid-share.
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
- **The track is paced by the rate that was asked for**, not by the stream's
  own timestamps, and this is the one thing the design had wrong.
  `ReaderSampleProvider` derives a sample's duration from the IVF timebase and
  the timestamp delta — and ffmpeg does not write those two in agreement for
  every grabber. Verified: `testsrc` gives timebase 1/30 stepping by 1, while
  **x11grab gives timebase 1/15 stepping by 15**, which is a second per frame
  through lksdk's arithmetic and publishes the share at a fifteenth of its
  speed. `ReaderTrackWithFrameDuration` overrides it, which is honest because
  the child's own `fps` filter is what holds the rate constant.
- Every stop path meets in `stopSharing` / `onShareStopped`: the button, the
  encoder dying (the captured window closed — `OnWriteComplete` → unpublish →
  `ShareStopped`), `dropCall`, and logging out.
- Still open on send: **share audio** (a second Opus track from a WASAPI
  loopback or a Pulse monitor), **the bitrate override** (auto is 0.1 bit per
  pixel per frame and there is no row to change it), **hardware encoders**
  (probe `ffmpeg -encoders`, prefer nvenc/qsv/amf), **self-preview** (tee the
  encoder's stdout into the receive path's own decoder), **ddagrab** on
  Windows, **macOS** (avfoundation screens plus the consent prompt), and
  **Wayland**, which has no grabber in stock ffmpeg — the portal is its own
  project, so a machine with no X says so in the picker and offers nothing.

## Options (the Discord-parity surface)

All read at share start and applied by **restarting the child** — a share
restart costs one GOP, which is what makes every option cheap to honour
mid-share. What the picker was last answered with is remembered in
`config.State` (not a settings section: no row writes to it, and it is what the
client was told rather than what was chosen), so the same monitor at the same
rate is one press next time.

Source, resolution and framerate are built; the rest of the table is still the
plan.

| Option | Values | Where it lands |
| --- | --- | --- |
| Source | monitors, windows (X11/Windows) | grabber input args |
| Resolution | Source / 1080p / 720p / 480p | `scale` (+`pad` never needed on send: the source aspect is known) |
| Framerate | 5 / 15 / 30 / 60 | grabber `-framerate` (+`fps` filter where the grabber rounds) |
| Quality | Auto / bitrate override | `-b:v/-maxrate/-bufsize`; auto ≈ 0.1 bit per pixel per frame (720p30 ≈ 2.5 Mbps, 1080p30 ≈ 4.5, 1080p60 ≈ 8) |
| Audio | on / off | the loopback capture + second track; greyed on macOS |
| Codec | Auto / VP8 / H.264 | Auto = hw H.264 where probed, else VP8 (advanced row) |

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
4. **Options still owed**: the bitrate override, audio share (WASAPI
   loopback, pulse monitor) — and the receive-side watch resolution beside
   them.
5. **Polish**: self-preview tee, hw-encoder probe, ddagrab, macOS screens, the
   Wayland portal investigation.

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
  honest mouth-to-ear number); concealment listened to, not just measured;
  unplugging the microphone mid-call on real hardware; the settings-picker
  device swap while somebody can hear; pulling the network mid-call; logging
  out mid-call leaving no device open; the settings-search no-device check by
  hand; the macOS and Linux CI legs actually compiling gopus.
- **Housekeeping that still holds**: `voice-test` in Big up testers exists for
  live checks; `gopus` is required directly at a `master` commit (no
  `replace`, deliberately); `revoltgo` is pinned at the commit carrying the
  voice additions and `update-deps.sh` is what moves it.

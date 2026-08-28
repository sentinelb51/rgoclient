# Screenshare: the work queue

Sending and receiving a screenshare in voice calls. Nothing below is built; this
file is the design and the order to build it in, grounded in what was verified
against the pinned dependencies (file:line where it matters). The voice queue
that used to live at this path is basically done — what it still owed is carried
at the tail, and `git log docs/voice-chat-todo.md` keeps the full record.

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
- **Not verified: whether Stoat's token grants video publish.** The first task
  of any of this: `client.JoinCall`'s token is a JWT — decode the payload and
  read the `video` grant (`canPublish`, `canPublishSources`). An absent or
  empty `canPublishSources` allows every source; one that lists sources without
  `SCREEN_SHARE` means the server rejects the AddTrack and the send half is
  blocked on the backend, not on this client. Ground against
  https://github.com/stoatchat/stoatchat if the answer is surprising.

## Phase 0 — before anything is built

Two small changes, worth shipping on their own:

- **Unsubscribe video publications.** In `OnTrackPublished` / on subscribe,
  `pub.SetSubscribed(false)` for any video track. Turns "a share bills the
  room" into "a share costs nothing until watched", and is the hook watch will
  reuse.
- **The JWT grant check**, live against stoat.chat. Everything in "Sending"
  assumes the answer is yes.

## Receiving

**Path:** watch → `SetSubscribed(true)` → a reader goroutine per video track
(`ReadRTP` → `samplebuilder` → whole frames) → remux → decoder child → RGBA →
the view.

- **Depacketising is pion's, already in the tree**: lksdk depends on
  `pion/webrtc/v4`, whose `pkg/media/samplebuilder` plus the
  `pion/rtp/codecs` depacketizers (`VP8Packet`, `VP9Packet`, `H264Packet`)
  reassemble RTP into whole frames and drop what a lost packet truncated.
  `H264Packet` emits Annex-B by default (verify: `IsAVC` unset).
- **The seam**: `voice` writes a *muxed byte stream* — IVF for VP8/VP9 (32-byte
  header + 12-byte frame headers; timebase 1/90000 so the RTP timestamp is the
  pts verbatim), Annex-B for H.264 — into an `io.Writer` the app supplies, and
  emits `ScreenshareChanged{UserID, Started/Ended}` events beside
  `ParticipantChanged`. Structural like `PCMSource`: the contract is "a byte
  stream ffmpeg's `-f ivf` / `-f h264` demuxer reads", named in a doc comment,
  and `voice` still imports nothing of `video`.
- **The decoder is `internal/video`'s child, sandboxed in full.** A remote
  participant's bitstream is exactly as hostile as a message attachment. A new
  `LiveFrames(cfg)` beside `Frames`: input `pipe:0` instead of a file (the
  bwrap profile *loses* the media ro-bind — strictly smaller than the
  player's), `-f ivf`/`-f h264` forced by the caller (the codec came from SDP,
  not from sniffing), low-latency flags (`-fflags nobuffer -flags low_delay
  -probesize 32 -analyzeduration 0`), and the output contract is the player's:
  `-f rawvideo -pix_fmt rgba` at exactly W×H. **The scale filter gains a pad**:
  `scale=W:H:force_original_aspect_ratio=decrease,pad=W:H:(ow-iw)/2:(oh-ih)/2`
  — a shared window resizes mid-stream and the source dimensions move under
  the decoder, and this is what keeps the byte-per-frame contract standing
  through it (the cost is letterboxing until the view re-launches at the new
  aspect).
- **Live pacing is not the player's pacing.** No wall clock: a drain goroutine
  always reads the frame pipe and keeps only the newest frame (a depth-1
  mailbox), the UI hop paints what is newest when it runs. The pipe must always
  be drained — its buffer is smaller than one frame, so a stalled reader
  backpressures the decoder into latency rather than dropping, which is the
  wrong trade live.
- **Keyframes**: `WritePLI` on watch-start and on every decoder relaunch; the
  decoder discards until an IDR/keyframe arrives, so without it a watch starts
  up to a GOP late.
- **View resize / quality**: relaunch the decoder at the new size + PLI —
  pause/seek's restart discipline, cheap. `SetVideoQuality` waits for a
  simulcast sender, which phase 3's single-layer publish is not.
- **Share audio** is one more Opus lane: a `SCREEN_SHARE_AUDIO` track decodes
  through the existing lane machinery, keyed apart from the participant's voice
  (`userID+"\x00share"` beside the reserved-lane convention) so their volume
  and the share's are separate dials.
- **Costs**: 1080p30 RGBA over the pipe ≈ 250 MB/s — noise for a pipe. Software
  VP8/H.264 decode at 1080p30 is well under one core. The real cost is the
  player's: one texture upload and one full-window repaint per frame
  (`Canvas.dirty`), continuous while watching. `decodeThreads` is sized for a
  chat card; the live decoder should take threads by area (2 up to 720p, 4
  above).

## Sending

**Path:** picker → one ffmpeg child (grab → scale/fps → encode → mux) → stdout
→ `NewLocalReaderTrack` → `PublishTrack(Source: SCREEN_SHARE)`.

A share starts mid-call, so it is an ordinary renegotiation — one offer/answer
at share start, nothing touching the join fast path. Stop is symmetric: the
child exits (or the window it captured closed, which *is* the child exiting),
the reader track EOFs, unpublish, event. `leaveCall` tears the child down with
the rest; starting a new share stops the old one first — the one-playback rule
again.

- **Codec: VP8 in IVF by default.** Universal in WebRTC, and IVF's own
  timestamps mean the track is paced by the encoder's real output, not by a
  declared constant. H.264 (`-c:v libx264 -tune zerolatency -preset ultrafast`,
  Annex-B, plus `ReaderTrackWithFrameDuration`) where a hardware encoder is
  discovered — probe `ffmpeg -encoders` once beside `Discover` and prefer
  nvenc/qsv/amf/videotoolbox, which is most of what Discord's "go live" spends
  a GPU on. VP8 flags: `-deadline realtime -cpu-used 8` (up to 16 trades
  quality for CPU), `-b:v` + `-maxrate` + `-bufsize` for a bounded stream.
- **Keyframes are a fixed GOP** (`-g 2*fps`): a CLI encoder cannot answer a
  viewer's PLI, so a late joiner waits up to the GOP for a picture. Two seconds
  is the Discord-ish trade: ~0.5 % bitrate overhead against a two-second worst
  join. This is the one real compromise of the subprocess encoder, and the
  honest alternative (an in-process encoder answering PLI) is a cgo encoder
  dependency — not worth it until someone measures the wait mattering.
- **Capture, per OS** (the genuinely platform half):
  - *Windows*: `gdigrab` first — `-i desktop` with `-offset_x/y -video_size`
    for one monitor, `-i title=…` for a window; CPU BitBlt, works everywhere.
    `ddagrab` (Desktop Duplication) is the upgrade: GPU frames, pairs
    zero-copy with `h264_nvenc`, monitors only — take it when hw encode is
    also present, else the hwdownload erases the win.
  - *X11*: `x11grab`, `-window_id` for a window, `-video_size`+`grab_x/y` for
    a monitor (geometry from the toolkit's own screen info). Occluded windows
    capture whatever X has for the drawable — without a compositing WM that is
    garbage over the covered region; every X11 capturer shares this and it is
    a note in the picker, not a bug to fix.
  - *macOS*: `avfoundation` (`-i "N:"`) captures whole screens and triggers
    the OS Screen Recording consent prompt; window capture is ScreenCaptureKit
    (native, later). Screens only at first.
  - *Wayland*: no grabber in stock ffmpeg; the portal
    (`org.freedesktop.portal.ScreenCast` → a PipeWire node) is the only
    citizen's path and means consuming PipeWire ourselves. **A known gap at
    first**, stated in the picker; X11 and XWayland windows still work via
    x11grab.
- **Window enumeration is native, not ffmpeg's** (ffmpeg lists nothing):
  Windows `EnumWindows`/`EnumDisplayMonitors` through `x/sys` — no cgo, the
  `cpu` package's precedent, skipping cloaked/toolwindow handles; X11 the
  EWMH `_NET_CLIENT_LIST` walk, which wants an X connection — `github.com/jezek/xgb`
  is pure Go and the cleanest way to one we own (Fyne's is glfw's, not
  reachable). The list crosses to the picker as data (`ui.ShareSource{ID,
  Kind, Title}`), the `ui.AudioDevice` seam again.
- **Audio share**: a second capture through the *existing* chain-free publisher
  path (no RNNoise, no gate — it is not a microphone), encoded by the same
  Opus code, published as `SCREEN_SHARE_AUDIO`. Windows: malgo `Loopback` on
  the playback device. Linux: the PulseAudio monitor source (`….monitor`
  appears in ordinary capture enumeration). macOS: no OS loopback exists —
  the same gap Discord has there; the toggle greys out.
- **Self-preview costs no new machinery**: tee the encoder's stdout — one copy
  to the LiveKit track, one to the *receive* pipeline's own sandboxed decoder.
  The preview is then exactly what viewers see (artifacts included, which is a
  feature), and nothing platform-specific is added. Windows note: a second
  child pipe via `ExtraFiles` does not exist there, which the tee sidesteps —
  it is all in-process `io` plumbing.
- **Sandboxing the send child: containment, not isolation.** The strict
  profile forbids exactly what capture is (`--unshare-all` severs the X11
  abstract socket, macOS's `(deny network*)` and the Windows low-IL token are
  each a plausible capture breaker), and the input is this machine's own
  screen — nobody else's bytes reach the child, so the player's threat model
  simply does not apply. What it keeps is the resource half of `harden()`:
  niced, memory-capped, CPU-capped, kill-on-parent (job object / prlimit /
  setpriority). On Linux a *middle* profile is worth one self-test: bwrap
  keeping the network namespace and binding `/tmp/.X11-unix` and the cookie,
  everything else the full profile — grab one frame under it at `Discover`
  time, fall back to the plain hardened child the way every sandbox here
  already falls back.
- **Costs**: gdigrab/x11grab 1080p30 is a copy per frame — a few percent of a
  core; libx264 ultrafast 1080p30 is roughly a core, libvpx realtime somewhat
  more; hw encode is why the probe prefers it. **Measure here before picking
  defaults.** One interplay worth a line: `cpu.Pin`'s affinity is inherited by
  children, so the encoder competes inside the pinned set — an encoder child on
  the efficiency cores is the default's actual meaning, and possibly the right
  one; just know it when a share stutters on a pinned laptop.

## Options (the Discord-parity surface)

All read at share start and applied by **restarting the child** — a share
restart costs one GOP, which is what makes every option cheap to honour
mid-share. Stored under a new `config.Screenshare` section; the per-share picker
seeds from it and writes nothing back (the picker is a choice, the settings are
the default).

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

0. **Unsubscribe unwatched video + the JWT grant check.** Shippable alone.
1. **`video.LiveFrames`** — the stdin decoder with the pad-scale contract,
   testable offline by feeding a recorded IVF and counting bytes.
2. **Receive**: `voice` subscribe/depacketise/remux + events + the writer seam;
   `app/screenshare.go` wiring watch/unwatch, PLI, the mailbox pump into
   whatever surface exists to paint on.
3. **Send, monitors, X11 + Windows**: capture+encode child, IVF, publish,
   every stop path.
4. **Options**: window sources + enumeration, fps/resolution/bitrate, audio
   share (WASAPI loopback, pulse monitor).
5. **Polish**: self-preview tee, hw-encoder probe, macOS screens, the Wayland
   portal investigation.

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

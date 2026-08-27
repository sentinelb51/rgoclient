# The video player

What playing a video in this client should look like, and why. Nothing in this
file is built. Read it before writing any of it; move what gets built into the
package notes and keep the rest here.

## What exists today

- A **video attachment** falls into `buildGenericAttachment`
  (`internal/ui/attachment.go`): a file icon on a rectangle, no thumbnail, no
  duration, nothing tappable. `domain.FileVideo` is classified and never
  branched on.
- A **video embed** is dropped to nil in `client.toEmbed`
  (`internal/client/convert.go`): revoltgo carries only the URL and dimensions,
  and with no player there was nothing to draw. A YouTube link renders as its
  website card at best.
- A **gifbox GIF** somebody sent unfurls as an MP4 and a poster, so the sent
  half of the GIF feature is really this feature (`docs/known-gaps.md`).
- The spec offers no help: attachment `Video` metadata is width and height
  only — no duration, no server-side thumbnail or poster rendition anywhere in
  `sources/openapi-spec-0.15.1.json`.

## The threat model, which decides everything

A video in a message is a **sender-controlled bitstream**. Senders upload
arbitrary MP4/WebM/MKV and gifbox sends H.264 MP4, so "pick a safe codec" is
not an available policy — the client plays what arrives or refuses it, and a
codec allowlist is a refusal of most real messages. The lever is therefore not
*which* decoder but **where it runs**: demuxers and codec libraries written in
C parsing hostile input are the classic RCE surface (StageFright and every
libav CVE since), and the mitigation that actually holds is a process boundary,
not a better library.

### Rejected

- **libav/ffmpeg or libmpv linked in-process (cgo).** The whole attack surface
  lands inside the client's address space, next to the session token. Also a
  heavy build dependency on three platforms. Rejected outright.
- **Pure-Go decoders.** None exist at production quality for H.264/VP9/AV1
  (`gen2brain/mpeg` is MPEG-1). Not an option, not a matter of effort.
- **OS decoders (Media Foundation / AVFoundation / GStreamer).** Three
  platform-specific integrations, still C parsing hostile input in-process —
  OS-patched, but a compromise is still this process. Browsers use these *from
  sandboxed utility processes*, which is the architecture below with more work.
- **Handing the file to the system player only.** Zero new surface here, but it
  moves the same hostile file into an unsandboxed player with worse odds, and
  inline playback — the actual feature — never happens. Kept as the escape
  hatch on the card, not as the player.

## The architecture: ffmpeg as a sandboxed subprocess

One design: the client never parses video. An `ffmpeg` child process demuxes
and decodes; the client reads raw frames from a pipe and paints them exactly
the way `ui.gifAnimator` already paints GIF frames — one reusable RGBA buffer
into one `canvas.Image`, `Refresh` per frame. A codec exploit owns a throwaway
process with no token, no filesystem intent and a closed pipe.

- **Discovery, not bundling (first pass).** `exec.LookPath("ffmpeg")`; the card
  offers inline play only when found, and says what to install when not.
  Bundling (~15–30 MB per platform, LGPL build) is a release-pipeline decision
  to take later; nothing in the player changes either way.
- **Video pipe.** Fetch the file to the image cache directory first (bounded,
  resumable, one download for any number of plays), then
  `ffmpeg -i file -f rawvideo -pix_fmt rgba -vf scale=W:-2 -r cap pipe:1`.
  Scale at the *decoder* to the box the card draws in (`MessageImageMaxWidth`,
  like an attachment): an inline card is ≤ ~400 px, so the pipe carries a few
  MB/s, not 1080p. Frame size is W×H×4; read exactly that per frame, no
  framing protocol needed. Cap the frame rate (30) in the same filter.
- **Audio pipe.** A second invocation (or `-map` to fd 3):
  `-f s16le -ac 1 -ar 48000`, which is exactly what `audio.Sink.Write` takes.
  The video's sound is **a lane in the existing mixer** — open under a reserved
  ID the way `echoLane` is, paced by `Sink.Want`/`Wake()` like a call
  participant, per-lane gain giving the card its volume for free. `app` wires
  it; `ui` still never imports `audio`.
- **Pacing.** Audio is the clock (the sink already is — the speakers wake the
  writer); video frames are consumed at the `-r` cap and dropped when the
  reader falls behind rather than buffered. Muted playback paces off a plain
  ticker. Mouth-to-ear sync within a frame or two is enough for a chat card.
- **Poster and duration.** The spec provides neither, so the first frame is the
  poster: one `ffmpeg -frames:v 1` at mount time (lazily, like a text
  attachment's preview), cached as an ordinary image cache entry. `ffprobe`
  read the duration for the bar; both run in the same sandbox as playback.
- **Caps and lifetime.** Encoded file size capped before fetch (the attachment
  carries `Size`); wall-clock timeout on the child; the child is killed on
  card unmount, channel switch and `resetSessionState` — a decoder is a device
  the way a microphone is: stopped by whoever drops the widget, never by
  trusting `Destroy` (`internal/ui/CLAUDE.md`). One playing video at a time,
  like one animating GIF: starting one stops the other.

### Sandboxing the child, per OS

The process boundary is most of the win; these narrow what the child keeps.

- **Windows**: `CreateProcess` with a restricted token (low integrity via SAFER
  or `CreateRestrictedToken`), inside a Job object with a memory cap and
  `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` so an orphan dies with the client. No
  window station access needed — stdin/stdout only.
- **Linux**: prefer `bwrap` (bubblewrap) when on PATH — empty namespace, the
  one input file bind-mounted read-only — else plain subprocess with rlimits
  (`RLIMIT_AS`, `RLIMIT_CPU`). Seccomp without a helper binary is not worth
  hand-rolling.
- **macOS**: `sandbox-exec` with a minimal profile (deprecated but functional),
  else plain subprocess. macOS is already the platform with no packaging
  (`docs/known-gaps.md`).

The child gets: the input file (read-only), stdout, stderr. It does not get the
network — the client fetched the file — which is itself a hardening: an
exploited decoder with no socket has nowhere to send anything.

## The card, in shippable order

1. **A real video card** (no player): poster slot, name/size bar, duration once
   ffprobe exists, and **Open externally** — save to the cache directory with
   an extension derived from *sniffed* magic bytes, never the sender's
   filename (a ".mp4" that sniffs as something else is not handed to the
   shell), then open with the OS default. Ships without ffmpeg; better than
   the file icon on day one.
2. **Poster + duration** via sandboxed ffmpeg/ffprobe where found.
3. **Inline playback, muted, click-to-play**: the rawvideo pipe into the
   gifAnimator-style frame swap, a play/pause tap, the frame-drop pacing.
   Click-to-play, not hover — sound and multi-second decode make hover the
   wrong gesture here; hover is the GIF's.
4. **Audio lane + scrub bar**: the s16le pipe into a `Sink` lane; seeking is
   killing the child and restarting with `-ss` (cheap, and the only correct
   seek over a pipe).
5. **Video embeds**: stop dropping them in `toEmbed`, carry URL + dimensions on
   `domain.Embed`, draw the same card. Gifbox's MP4 unfurl starts moving at
   this step — consider hover-to-play *muted* for embeds Revolt marks as GIF
   provider pages, matching the GIF gesture.

## What this costs

- RGBA over a pipe at 400 px / 30 fps ≈ 20 MB/s — noise on any machine this
  client runs on; the texture upload per frame is the same cost the GIF
  animator already pays and is bounded by the card size.
- One ffmpeg process per playing video (one at a time), tens of MB RSS, dead
  the moment the card stops.
- The real risk to schedule around is **plumbing, not performance**: process
  lifecycle on three OSes, the sandbox variants, and kill-on-everything
  discipline. Steps 1–2 carry none of it.

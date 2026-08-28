# The video player

What playing a video in this client looks like, and why. The design below is
**built** — the shippable order's five steps all landed together — so this file
is now the threat model, the map of where each piece lives, and the remainder
that is not built. The per-package mechanics are in the package notes:
`internal/video` doc comments for the driver and the sandbox,
`internal/ui/CLAUDE.md` for the card, `internal/app/CLAUDE.md` item 45 for the
controller.

## The threat model, which decided everything

A video in a message is a **sender-controlled bitstream**. Senders upload
arbitrary MP4/WebM/MKV and gifbox sends H.264 MP4, so "pick a safe codec" is
not an available policy — the client plays what arrives or refuses it. The
lever is therefore not *which* decoder but **where it runs**: demuxers and
codec libraries written in C parsing hostile input are the classic RCE surface
(StageFright and every libav CVE since), and the mitigation that actually
holds is a process boundary, not a better library.

Rejected on that basis, still rejected:

- **libav/ffmpeg or libmpv linked in-process (cgo).** The whole attack surface
  inside the client's address space, next to the session token.
- **Pure-Go decoders.** None exist at production quality for H.264/VP9/AV1.
- **OS decoders (Media Foundation / AVFoundation / GStreamer).** Three
  platform integrations, still C parsing hostile input in this process.
- **Handing the file to the system player only.** Kept as the card's escape
  hatch ("Open in your player"), not as the player: it moves the same hostile
  file into an unsandboxed player, and inline playback never happens.

## The architecture, as built

The client never parses video. `internal/video` runs `ffmpeg` as a sandboxed
child: it demuxes and decodes, and the client reads raw RGBA frames and s16le
PCM from pipes at sizes **this side computed**. A codec exploit owns a
throwaway process with no token, no network and nothing writable.

The layers, and what each one refuses:

- **Sniffing before any child runs** (`video.Sniff`). The demuxer is forced
  from the file's magic bytes — MP4/MOV, Matroska/WebM, AVI, FLV, ASF — and
  the sender's filename and content type are never consulted. An HLS playlist
  or a script dressed as an `.mp4` is refused before ffmpeg exists to be
  confused by it; this is also what closes the playlist/concat local-file
  disclosure class, and the sniffed extension is the only name the file is
  ever stored or opened under.
- **Validation of everything a child reports** (`video.Probe`). Dimensions are
  bounded (32 Mpx, the image cache's own ceiling), durations are finite,
  positive and capped, aspect ratios and rotations clamped — the 2021-era
  Discord crashers were clients doing arithmetic on exactly these fields as
  sent. ffprobe's output is size-capped; stderr is size-capped; a probe or
  poster child lives `toolTimeout` and is killed.
- **A byte contract nothing in the file can move.** Frames cross the pipe as
  exactly width×height×4 bytes at the rate this side asked for
  (`scale=W:H` forces both dimensions), so a wrong probe costs a stretched
  picture, never a misread pipe. A poster child that writes more than one
  frame's worth is killed as a liar.
- **The sandbox** (`internal/video/sandbox_*.go`). Linux: bubblewrap when it
  works here — empty namespaces (no network), the toolchain and the one input
  file read-only, nothing writable, `--die-with-parent` — proven once by
  running `ffmpeg -version` under the exact profile, else a plain child under
  `prlimit` (address space, CPU, `RLIMIT_FSIZE=0`). Windows: a restricted
  low-integrity token (no privileges, no writes to the user's files or the
  registry) and a job object (memory cap, no child processes, kill-on-close).
  macOS: `sandbox-exec` denying the network and all file writes, self-tested
  the same way. Everywhere: the child gets the input file, stdout and stderr,
  and is niced below the UI.
- **Fetch bounds before the decoder** (`cache.MediaCache`). The attachment's
  `Size` is refused past `videoMaxFetchBytes` before a byte lands, the body is
  capped again against a lying `Content-Length`, and the store keeps originals
  under their own disk budget (`Cache.VideoDiskMiB`), named by sniffed magic,
  evicted by recency. A mere mount fetches nothing past
  `videoPosterFetchBytes` — scrolling past a hundred-megabyte video does not
  download it.

## What runs where

- `internal/video` — the driver: discovery (`exec.LookPath`, no bundling),
  sniff, probe, poster, and the frame/PCM streams with their kill discipline.
  No internal dependencies.
- `internal/cache/media.go` — fetched originals on disk: single-flight,
  progress-reporting, sniff-named, budgeted.
- `internal/ui/video.go` — `VideoCard`: poster box, play badge, duration/status
  chip, scrub strip, sound toggle, name/size bar with "Open in your player".
  Dumb on purpose; everything decided is an `OnVideo*` action.
- `internal/app/video.go` — the controller: mount fills poster+duration
  (cached as ordinary image-cache entries), tap toggles play/pause, seek and
  resume are `-ss` restarts, one playback at a time, killed on channel switch,
  sign-out and the card leaving the canvas. Sound is a reserved mixer lane
  (`Sink.StartVideo`), paced by the speakers like a call participant.
- Embeds: `client.toEmbed` carries `domain.Embed.Video` (a bare video link,
  or the MP4 an unfurl like gifbox serves) plus the provider's GIF mark;
  `ui.buildEmbedVideo` draws the same card, looped and silent-by-default for
  a GIF (`-stream_loop`, one child for any number of passes).

## What this costs

- RGBA over a pipe at ≤400 px / 30 fps ≈ 10–20 MB/s — noise on any machine
  this client runs on; the texture upload per frame is the cost the GIF
  animator already pays (`docs/performance.md`), bounded by the card size.
- One or two ffmpeg children per playing video (frames, and sound where the
  file has any), tens of MB RSS, dead the moment the card stops. Decoding is
  capped at two threads and niced.
- Pause is free: it kills the children and remembers the position, and resume
  is a seek — the only correct seek over a pipe, and cheap.

## Not built

- **Hover-to-play for GIF embeds.** A gifbox GIF is click-to-play like any
  video (then loops, silent). The GIF gesture — hover in, hover out — wants
  play/stop wired to the embed stack's hover with the same debouncing the
  `gifAnimator` gets for free from decode-per-hover; a decoder subprocess per
  pointer crossing needs more care than the tap took.
- **The attachment viewer still says "no preview" for video** — the inline
  card is the player; the viewer mounts nothing.
- **Decode resolution ignores HiDPI.** The pipe carries the card's logical
  size (≤400 px), so on a 2× display playback is soft. Deliberate for now:
  the poster is what rests on screen and it takes the same box.
- **No resumable downloads.** A fetch interrupted at 90% restarts; Range
  resumption is plumbing nothing demanded yet.
- **No hardware decode.** The child uses software codecs; `-hwaccel` differs
  per platform and fails in more ways than it saves at chat-card sizes.
- **Windows keeps the network.** The restricted token and job cap what a
  compromised child may touch, but no per-process firewall is applied;
  Linux (bwrap) and macOS (sandbox-exec) do cut the network off.
- **A YouTube/Spotify embed is still a website card** — `MessageEmbedSpecial`
  players are iframes on the web client and stay a known gap; only an unfurl
  that names a direct media URL gets the card.

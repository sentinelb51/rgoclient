# Voice chat: what is left

Voice calls work end to end. This is what is missing, what is compromised, and
what was measured and deliberately left alone. Companion to `known-gaps.md`
(what the client does not do) and `performance.md` (what is slow) — this file is
the *work queue*, and entries graduate out of it into those two.

Written to be read cold. Nothing below assumes you were there.

## Where it stands

A call is: `Client.JoinCall` gets a node and a token, `voice.Join` dials
LiveKit, one Opus track goes up from the microphone and every remote track is
decoded into the speakers. `internal/audio` owns both directions of the device;
`internal/voice` owns the media session; `internal/app/voice.go` is the
controller. The seam between the first two is structural — `voice.PCMSource` /
`PCMSink` — so `voice` never imports `audio` and `app` is the only package that
imports both.

Verified live against stoat.chat with **two accounts in one room**, which is the
check everything else was waiting on: the speaker's 440 Hz tone arrives as
440 Hz at rms 6382 against the 6364 sent, 1185 frames in 23.7 s — 49.9 a
second, playout rate exactly. Identity matches per account, FEC and DTX active
on libopus 1.5.2. Sink occupancy held at 50 ms throughout, the 40 ms target plus
one frame, which is what the offline measurement predicted.

That "publishes from a real microphone" was believed for a long time on the
strength of a `published track` log line, and was **not true** — see the codec
entry in section 3.

Swapping the input device is verified too, on real hardware and mid-call: reads
stay at exactly 50 a second across the swap, not one period missed.

**Still not verified:** a person hearing a person, and mouth-to-ear as a number.
Both ends above were this machine, so the number would be a loopback.

### Facts worth not rediscovering

- **`join_call` requires `node`.** An empty one is `400 UnknownNode`, not "you
  pick". The list is the instance config's `livekit` block, read through
  `revoltgo.Session.Instance`. stoat.chat publishes one node, `hel1`.
- **LiveKit identity is the Revolt user ID** and the room name is the channel ID.
  Everything per-person is keyed on the first. `voice.Call` logs if it ever
  diverges.
- **A voice channel is a `TextChannel` carrying a `voice` object.** There is no
  voice channel *type*. Creating one is `POST /servers/{id}/channels` with
  `{"name": ..., "type": "Voice"}`, which answers with `channel_type:
  "TextChannel"` and `voice: {}`.
- **There is no leave route.** Leaving is disconnecting; the gateway announces it.
- **One account cannot be in a call twice.** The second connection makes the
  server remove the participant — `PARTICIPANT_REMOVED`, a few seconds in, not
  at the join. Two clients signed into the same account is how it happens, and
  the eviction looks like a dropped call from inside.

---

## 1. Move the transport into `revoltgo-voice`

`sentinelb51/revoltgo-voice` exists and is empty. `internal/voice` was written to
be lifted into it as package `rvoice` — nothing in its surface names Revolt,
LiveKit or miniaudio, and the transport sits behind the `Jitter` interface
precisely so it can move without callers noticing.

**What moves:** `voice.go`, `publish.go`, `subscribe.go`, `jitter.go` verbatim.
`PCMSource` / `PCMSink` go with them. `domain.CallCredentials` becomes an
`rvoice` struct of two strings, and `internal/voice` shrinks to a thin adapter —
or disappears, with `app` importing `rvoice` directly.

**Benefit, honestly weighed:**

- *Real:* it is the only way anybody else gets a Go voice client for Revolt. The
  signalling half is in revoltgo already and the media half is the hard part
  nobody has written twice.
- *Real:* it keeps pion out of `revoltgo`. A bot library should not drag WebRTC,
  QUIC and a DTLS stack into every consumer's build. This was the original
  reason for the split and it still holds.
- *Real:* forces the interface to stay honest. Anything Revolt-shaped that leaks
  in becomes a compile error rather than a slow drift.
- *Overstated:* it does nothing for rgoclient itself. rgoclient never imported
  `rvoice/revolt` and would import `rvoice` exactly as it imports
  `internal/voice`.

**Cost:** a version bump per transport change, cross-repo debugging, and a
second CI. That is why it is still here — the surface is young and moved several
times while getting a call working. **Do it once two-way audio is confirmed and
the jitter buffer question below is settled**, not before; those are the two
things most likely to change the interface.

`rvoice/revolt` — the `JoinCall(*revoltgo.Session, channelID)` helper and node
selection — belongs in that repo too and rgoclient still would not import it.

## 2. Upstream what belongs in `revoltgo` — done

All three landed in `sentinelb51/revoltgo` and the workarounds here are gone.
Kept as a record of what the shapes are, since the spec disagrees with two:

- **`InstanceConfigFeatures.LiveKit`** replaced the dead `Voso` block:
  `{Enabled bool; Nodes []InstanceConfigVoiceNode{Name, Latitude, Longitude,
  PublicURL}}`, matching the spec's `VoiceFeature` / `VoiceNode`.
- **`ServerMemberEditParams`** gained `CanPublish *bool`, `CanReceive *bool` and
  `VoiceChannel string`, so voice moderation is an ordinary `editMember`.
  The trap is still the trap: clearing `CanPublish`/`CanReceive` resets them to
  **true**, so un-muting is `&true`, never a `Remove`.
- **`ChannelJoinCallParams.Node`** says it is required.
- **`Session.Instance`** is new alongside them. `Open` fetched the API root for
  the websocket URL and threw the rest away, so anything wanting the features
  block had to send the request again by hand.

## 3. Correctness and robustness

### Playout is now the device's clock — done

`playTrack` ran a `time.NewTicker(20ms)` per participant, which drifts against
the audio device's own clock. `mixLanes` discarded anything past `laneBacklog`,
and the part the old note here understated is what that *costs*: the discard
trims back **to** 120 ms, so a lane the ticker outran did not spike and recover,
it parked at 120 ms of buffered audio and stayed there. That is latency nothing
ever takes back out, on top of the jitter buffer's own 40-240 ms.

It is inverted now, but not the way the old note proposed. Decoding inside the
device callback would have put `adaptiveJitter`'s mutex and a cgo call into
libopus on the realtime thread, against everything `ring.go` and `lane.active`
exist for. Instead the callback *asks*:

- `mixer.render` sends on `Sink.Wake()` — one non-blocking send per period, the
  same thing `Capture.onData` already does — whenever a lane is open.
- `Call.playLanes` is a single goroutine for every participant, replacing one
  ticker each. It waits on that wake and tops each lane up to `Sink.Want`.
- `laneTarget` is 40 ms and nothing writes past it, so lane occupancy is a known
  constant instead of a free variable, and `laneBacklog` is now a backstop for a
  writer that ignores `Want` rather than the normal path.

Measured with `render` driven flat out against a producer that would happily run
ahead — far past any real clock skew — the lane peaks at **50 ms** and never
approaches the 120 ms discard threshold. Under the old arrangement that same
skew pinned it at 120 ms and discarded continuously.

Two things fell out of it. `reportLoss` now sends the **worst** lane's loss to
the encoder rather than whichever goroutine reported last, which is what FEC
should have been sized against all along. And a deafened lane is written
`silence` rather than skipped, so the jitter buffer's cursor advances at exactly
playout rate instead of being drained as fast as the loop can pop.

**Still not measured:** what any of this is worth in mouth-to-ear terms. It
bounds a 0-120 ms term; the jitter buffer's adaptive target is 40-240 ms and is
still the bigger number. Two people in a call is what would say whether
`minDepth` is the next thing to go after.

### The microphone was never actually sent — fixed

The one that matters. Every call logged `published track` and then, one line
later, `could not set remote description: unable to start track, codec is not
supported by remote`. The call connected, reported itself healthy, and carried
no audio at all.

`publish.go` declared the Opus track as `Channels: 1`, the audio being mono. But
**RFC 7587 fixes Opus's SDP declaration at `opus/48000/2` whatever the audio
is** — mono against stereo is the `stereo=` fmtp parameter, not the channel count
in the rtpmap. pion matches a local track against the negotiated codec on
MimeType, clock rate *and* channels (`internal/fmtp.ChannelsEqual`, a strict
compare after defaulting `audio/opus` to 2), so a track declaring 1 matches
nothing in an answer offering 2, `TrackLocalStaticRTP.Bind` returns
`ErrUnsupportedCodec`, and the sender never binds.

Nothing above that fails. `PublishTrack` returns a publication, the log says the
track is published, the participant appears in the room with a microphone. The
only symptom is silence at the far end, which is exactly the thing that had never
been tested.

Fixed by declaring `sdpChannels = 2` while continuing to encode mono, with
pion's own default fmtp line (`minptime=10;useinbandfec=1`) so the match is the
exact one rather than the fallback — and so the far end is told in-band FEC is
on, which this encoder has been enabling all along without saying so.

**This is almost certainly why two people hearing each other was never going to
work**, alongside the speakers never being opened. Both are now fixed and neither
has been confirmed with a second person.

### The jitter buffer only ever got deeper — fixed

`cleanRun` was reset by *every* missing packet, and shrinking needs 250 in a
row. But loss and starvation are different things: a hole with packets still
behind it is concealed and playout carries on unharmed, and it broke the run all
the same. Above one packet lost in 250 — 0.4 %, which is a good connection —
the depth could only ratchet up, and a long call ended at `maxDepth`'s 240 ms
and stayed there.

Only a dry buffer breaks the run now. On the same live path, at 0–2 % loss:
depth climbed 3 → 11 and shrank once in 25 s before, and reached 8 then came
back to 6 after.

### TURN is unreachable from this network

`failed to allocate on TURN client turn.hel1.voice.stoat.chat:443, all
retransmissions failed`, on every call. Measured from this machine: TCP/443 to
that host connects in 64 ms and completes TLS in 121 ms, while **UDP/443 and
UDP/3478 both time out** — to the TURN host and to the LiveKit host alike. UDP
itself is fine: the media path connects over UDP to a high port on the node.

So it is UDP/443 specifically, which is a filter this network applies (blocking
QUIC-shaped traffic is the usual reason) rather than anything the client does.

It costs nothing today because the direct path works — ICE connects on the Wi-Fi
candidate and TURN is only the fallback. It would cost everything on a network
behind symmetric NAT, where the fallback is the only path. `DisableTURN` would
silence the retransmissions and make that case strictly worse, so it stays on.
The fix is not here: the node would want to offer `turns:` over TCP/443 as well
as UDP, which is `hel1`'s configuration to change.

### The dial ran on the UI thread — fixed

`joinCall` put `voice.Join` in `backgroundThen`'s `then`, and `then` runs inside
`doOnUI`. `voice.Join` blocks for the whole connection handshake, so the window
froze for as long as the dial took: imperceptible on a good join, **five to six
seconds** when the voice node does not answer, which is exactly when a reader is
already wondering whether the client is broken.

Everything now runs on the worker and `installCall` is the hop back. The
staleness check therefore happens *after* the dial rather than before it — a call
that connected into a session that has since gone is closed rather than never
made, which is already how the microphone was handled. `a.background` replaces
`a.backgroundThen` because the latter runs neither branch when the worker
succeeds into a stale session, which would have leaked a live call.

### The voice node fails transiently, so a join retries

Three attempts against `hel1` in five minutes gave three different results: a
signal-connection read timeout at the 5 s deadline, a `500 InternalError` from
`join_call` itself (`voice_client.rs:120`), and a clean connected call. The node
is not slow — `curl` to it measures 74 ms TCP, 217 ms TLS, an answer inside a
second — it just fails sometimes.

So a first join that fails is retried, not reported, on the same machinery a
dropped call uses. `client.Transient` decides: anything that is not an HTTP
answer is worth retrying (a dial timeout says nothing about the request), a 5xx
is the server failing rather than refusing, and a 4xx or `ErrNoSession` is an
answer. Fewer attempts than a drop gets — `joinRetries` is 3 against
`callRetries` 5 — because somebody is watching a button they pressed, and the
dock says "Connecting" rather than "Reconnecting" for a call that was never up.
Only the last failure is said out loud.

### ICE gathers on adapters that cannot route

Seen on this machine: `Ethernet 3` sits on an APIPA address (`169.254.96.39`, no
DHCP lease) and neither adapter has a global IPv6, only `fe80::` link-locals.
pion gathers candidates on all of them and logs a "socket operation was attempted
to an unreachable network" per pair per check. It still connects — the Wi-Fi
candidate wins — so this is noise rather than a fault, but it is noise that
lengthens the ICE window on a machine with a dead NIC.

**Not fixable from here.** lksdk builds its `webrtc.SettingEngine` privately in
`transport.go` and exposes only `IPv6Only` and `DTLSEllipticCurves`; there is no
hook for `SetIPFilter` or `SetInterfaceFilter`. Filtering link-local and APIPA
candidates would mean a PR to `livekit/server-sdk-go` adding a settings hook to
`ConnectParams`. Worth doing if ICE setup time ever turns out to matter.

The server's own candidates are the other half and are also not ours: `hel1`
advertises `10.244.1.0` and `10.244.1.1` (a Kubernetes pod network), `100.64.0.2`
(CGNAT) and `fd7a:115c:a1e0::2` (Tailscale) alongside its real address. Every one
of those is unreachable from outside and is checked anyway.

### The speakers were never opened for a call

Found while inverting the above, and it is the more serious half. `Engine.open`
is reached from `play()` and nowhere else, so the playback device opened on the
first *notification sound*. A call joined before anything had rung wrote remote
audio into lanes that no callback was rendering — silence, with nothing saying
so, and no way to tell it from a call with nobody talking.

`Engine.StartOutput` opens the device explicitly and `joinCall` calls it before
the dial. This is very likely why "two people hearing each other" was never going
to work on a fresh client, and it is load-bearing now rather than incidental:
under the pull arrangement the callback is also what asks for the next frame, so
a closed device decodes nothing at all.

### Done since this file was written

- **Reconnect after a hard disconnect.** A `CallEnded` carrying an error now
  rejoins rather than reporting: `App.scheduleRejoin` doubles the wait per
  attempt to a 30 s ceiling, gives up after five, and keeps the dock on screen
  saying *Reconnecting* the whole time. A rejoin that itself fails counts against
  the same sequence; leaving cancels it, which is why every reader-facing exit is
  `leaveCall` rather than `hangUp`.
- **The input device changes mid-call.** `Capture` no longer *is* its device: a
  supervisor goroutine owns it, and `SetDevice` reopens underneath the same ring,
  so the publisher inside a blocking `Read` sees a period of quiet rather than a
  stream closing under it. `applyVoiceSettings` pushes the picker through.
- **Hot-plug is handled for input.** The same supervisor answers a device the
  backend took away by reopening it, then falling back to the default —
  `Engine.reopen`'s behaviour, generation guard and all. Only running out of
  microphones altogether ends a capture, which is the old behaviour as a last
  resort rather than the first response to an unplugged headset.
- **The settings meter shares the call's capture.** `startInputMonitor` borrows
  `a.capture` where there is one, so a device held in exclusive mode is not asked
  to grant a second open. `monitorOwned` is which, and `restartInputMonitor`
  moves the bar between the two as a call starts and ends.

**Unverified by hand:** all four. Unplugging a microphone mid-call and swapping
one in settings mid-call are two of the checks in section 6, and neither has been
done on real hardware.

## 4. Features not built

- **No echo cancellation.** Headphones are assumed; on speakers the far end
  hears itself. `audio.Processor` is the seam and `Engine` owns both directions
  precisely so the playback reference is reachable. This is the single biggest
  quality gap for anyone not wearing headphones.
- **No real noise suppression.** The only thing done to a frame's content is a
  one-pole high-pass in front of the gate, and the setting is named for that —
  "Rumble filter", `config.Voice.HighPass`. It was called "Noise reduction",
  which cost a live test to find out was untrue: a gate silences the frames
  between words and does nothing to static while somebody is talking. RNNoise
  was the original plan (vendored Xiph C, ~85 KB model) and still fits behind
  `Processor`.
- **Push-to-talk is Windows-only** (`ui.KeyHeld` → `GetAsyncKeyState`). X11 needs
  `XQueryKeymap` on a display connection the client does not own; macOS needs an
  Accessibility grant. `PushToTalkSupported` is false there and the mode is left
  out of settings rather than lying. The key also binds from a curated list
  because *capturing* an arbitrary key needs canvas focus, which the composer
  holds — see the modifier-key footgun in `ui/CLAUDE.md`.
- **No camera, no screen share**, and no way to watch either — a participant
  sharing one is drawn with the mark and nothing behind it.
- **No node selection UI.** The node is now *measured* — `nearestVoiceNode`
  dials every one the instance offers and takes the first handshake to complete,
  skipping the probe entirely when there is only one, which is stoat.chat today.
  What is still missing is a reader saying which node they want, which needs a
  surface and a setting and buys nothing until an instance publishes several.
- **No call recording.**
- **Deep PLC is in; DRED and OSCE are not.** `sentinelb51/gopus` now vendors
  upstream's `DEEP_PLC_SOURCES` and defines `ENABLE_DEEP_PLC`, and the client
  switches it per decoder — libopus gates the neural concealer on decoder
  complexity ≥ 5, so `Decoder.SetComplexity` *is* the switch and a decoder that is
  never told stays on the classic path. Settings → Voice → "Repair dropped audio",
  on by default, applied live by `Call.SetDeepPLC`.
  What is left is DRED, which recovers a burst loss rather than concealing it but
  needs the sender to enable it too, so it is worth nothing against a far end that
  is not also this library. OSCE's LACE/NoLACE is left out with it. Between them
  they are 10 MB of model data against Deep PLC's 5.
- **libopus 1.6.1 exists**; we vendor 1.5.2. A bump now also has to carry the
  `dnn/` sources and re-check that `dnn.c`'s file list still matches upstream's
  `DEEP_PLC_SOURCES`.

## 5. Performance — measured, and mostly a non-issue

Measured on this machine, 48 kHz mono, libopus 1.5.2 pure C:

| | cost | share of budget |
| --- | --- | --- |
| Opus encode | 165 µs/frame | 0.83 % of one core at 50 fps |
| Opus decode | 15.4 µs/frame | 0.08 % per participant |
| Opus PLC, classic | 8.0 µs/frame | only while concealing |
| Opus PLC, deep | 24.7 µs/frame | 0.12 % of realtime, only while concealing |
| Mixer, 1 lane | 1.6 µs/period | 0.016 % of the 10 ms budget |
| Mixer, 20 lanes | 18.1 µs/period | 0.181 % |
| Mixer, 50 lanes | 44.4 µs/period | 0.444 % |

Whole audio path with 20 remote participants: **about 2.4 % of one core.**

### Deep PLC costs nothing until something is lost

Measured in `sentinelb51/gopus`, mono at 48 kHz:

- **Clean decode is identical either way** — 16.7 µs/frame at complexity 0 and at
  complexity 5, 1.00×. The neural model is not run on a frame that arrived.
- **Concealment is 3.1×** — 8.0 µs → 24.7 µs per concealed frame, which is 0.12 %
  of realtime for one stream.
- **Decoder state is 85 KB** with it compiled in, so 1.7 MB across twenty
  participants. That is paid whether or not the switch is on, the state being part
  of `OpusDecoder`.

That is why the default is on: the only machine that pays is one already losing
packets, which is the machine that wants it. The switch exists because it is the
reader's machine, and because a build without the model — or a system libopus
older than 1.5 on the `opus_shared` path — reports the CTL as unimplemented,
which `lane.applyDeepPLC` logs once and carries on from.

### SIMD: not worth it for the codec, and already done for the model

The neural code was the part where this mattered, and it needed nothing:
`dnn/vec.h` selects `vec_avx.h` or `vec_neon.h` from the *compiler's* `__SSE2__`
and `__ARM_NEON`, not from libopus's `OPUS_X86_MAY_HAVE_*` config. amd64
guarantees SSE2 and arm64 guarantees NEON, so Deep PLC is vectorised on every
target this builds for with no flag at all. Going further means `-mavx2`, which
drops pre-2013 machines, or `OPUS_HAVE_RTCD` plus `dnn/x86/x86_dnn_map.c`, which
is the runtime dispatch the fork exists to avoid. The build prints libopus's own
`#warning` asking for AVX2; it is answered here, not ignored.

The rest of this section is about celt and silk, and stands.



libopus is vendored with intrinsics compiled out (`config.h` leaves every
`OPUS_X86_MAY_HAVE_*` and `OPUS_ARM_*` undefined), so it runs generic C. That was
a deliberate trade — no runtime dispatch tables, one object that works on every
amd64 and arm64 target.

Enabling SSE2/NEON would be a modest change: `OPUS_X86_PRESUME_SSE2` needs no
runtime detection because amd64 guarantees SSE2, and arm64 guarantees NEON. AVX2
would need `OPUS_HAVE_RTCD` and `celt/x86/x86cpu.c`.

It is not worth doing. A good SIMD build might take encode from 165 µs to
~110 µs — saving **0.3 % of one core**. The audio path is not where this client
spends anything. Revisit only if a call of 50 people on a weak machine turns out
to matter, and measure before and after.

The mixer is the other arithmetic-shaped loop and is 100× cheaper than the codec.
Hand-vectorising it would be optimising 0.4 % of a 10 ms budget.

### SIMD in the UI: aimed at the wrong layer

Fyne draws through OpenGL. Glyph runs are rasterised once and cached as GL
textures (`internal/cache/texture_common.go`), so the pixels are the GPU's work,
not the CPU's. `performance.md`'s own conclusion is that **traversal is what
grows and fill rate is bounded by the viewport already** — the CPU cost is
walking the widget tree, layout, and text measurement. That is pointer-chasing
and branching, which is precisely what SIMD does not help.

The levers that do exist are already written down in `performance.md`: fewer
mounted objects, fewer full-window repaints (`Canvas.dirty` is one bool, so *any*
`Refresh` repaints everything), and — the big structural one — a D3D or Vulkan
painter behind Fyne's small painter interface. None of those is a vectorisation
problem.

Where CPU vectorisation could theoretically apply is image decode and rescale for
avatars and attachments. Go does not auto-vectorise, so it would mean assembly or
cgo, and the work is already off the UI thread and cached to disk. Measure
`internal/app/virtual_bench_test.go` before believing there is anything there.

## 6. Verification still owed

- ~~**Two clients in one call**~~ — done, two accounts from this machine, and it
  is what found the jitter ratchet. What is left of it is **one of them the
  official Stoat web client**: the wire-format check against something that is
  not our own encoder, and the only honest mouth-to-ear number, both ends above
  being the same machine.
- **Hear a lost packet concealed both ways.** Turn "Repair dropped audio" off and
  on mid-call under real loss. The numbers say it works and the outputs differ;
  nobody has listened to it.
- **Unplug the microphone mid-call.** Should now fall back to the default —
  written, never run against real hardware. Worth doing with a headset and with
  a USB interface, the two that behave differently on WASAPI.
- ~~**Change the input device mid-call**~~ — done at the capture layer, twice
  each way, with no interruption to the 50 reads a second. What is left is doing
  it **from the settings picker while somebody can hear**, which is the half
  `app` owns.
- **Pull the network mid-call.** The reconnect should take over, the dock should
  say *Reconnecting*, and hanging up during the wait must not be undone by a
  timer that has already been armed.
- **Log out mid-call.** `resetSessionState` calls `hangUp` and
  `stopInputMonitor`; confirm no device stays open.
- **Open settings, type in the search box, close settings.** No capture device
  may open at any point — covered by `internal/ui/settings_index_test.go`, worth
  confirming by hand once.
- **CI on macOS and Linux.** Neither cgo leg has ever been built. The gopus
  build-tag change (vendored build on all architectures, not just amd64/386) is
  the part most worth confirming — before it, arm64 needed a system libopus.
- ~~Raise `timeout-minutes`~~ — the build legs are already at 30 minutes, which
  covers libopus's ~30–60 s of cold C compile per platform many times over. The
  version job's 5 and the release job's 10 do no compiling.

## 7. Test harnesses now in the repo

Added because they are diagnostics worth keeping, not because they were asked
for. Delete any that earn their keep less than they cost:

- `internal/voice/jitter_test.go` — ordering, sequence wrap, loss, the FEC
  hand-off, and the maintained `held` count. These fail for reasons a person
  would not spot by reading.
- `internal/voice/seam_test.go` — compile-time proof that `*audio.Capture`
  satisfies `voice.PCMSource` and `*audio.Sink` satisfies `voice.PCMSink`.
- `internal/voice/live_test.go` — **the live call.** Skipped unless `RGO_LIVE` is
  set; signs in with the saved session on this machine, finds a voice channel,
  joins and reports what identities the voice server hands back. `RGO_LIVE=tone`
  publishes a sine instead of opening the microphone. This is the fastest way to
  answer "is it actually working".
- `internal/ui/settings_index_test.go` — the regression most likely to ship
  silently: the settings search index builds every section twice, and the Voice
  section owns a microphone.
- `internal/audio/devices_test.go` — peak signal per input device. Written
  because the default microphone on this machine reads exactly `0.00000` while
  the other reads `1.00003`; that is a muted headset, not a bug, and it will look
  like a bug during two-person testing.
- `internal/audio/mixer_bench_test.go` — the numbers in section 5.

## 8. Housekeeping

- `voice-test` channel in **Big up testers** was created for testing and is
  still there.
- `go.mod` pins `layeh.com/gopus` to a `sentinelb51/gopus` commit on `master`.
  The FEC/DTX commit, the libopus 1.5.2 bump and the Deep PLC vendoring are all
  merged there. The `opus_shared` build path — link the system libopus instead —
  has never been compiled on this machine, pkg-config not being installed; the
  `Decoder.SetComplexity` shim was added to both files but only the vendored one
  is proven.
- `go.mod` pins `sentinelb51/revoltgo` to the commit carrying section 2's four
  additions. Nothing else in this client needs a newer one, so a `go get -u`
  through `scripts/update-deps.sh` is the next thing that will move it.
- `docs/known-gaps.md`, `internal/client/CLAUDE.md`, `internal/app/CLAUDE.md`
  item 36, `internal/ui/CLAUDE.md` and `docs/performance.md` all carry voice
  notes and are current as of this file.

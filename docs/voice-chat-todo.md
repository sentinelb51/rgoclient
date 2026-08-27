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
island says "Connecting" rather than "Reconnecting" for a call that was never up.
Only the last failure is said out loud.

### Joining took three handshakes; it now takes one — fixed

The join is timed at both seams now (`client.JoinCall`, `voice.Join`), because
none of it was and the four candidates for "the delay" turned out not to be the
same size at all. Measured live against `hel1` from this machine.

**What it was.** lksdk's default is a peer connection each way, so ICE and DTLS
ran twice against one node, and `PublishTrack` was then a further offer/answer
once both were up — three handshakes for one call. `dial` now asks for
`WithSinglePeerConnection()` and hands the track over with `WithTrack(...)`, so
the publisher's offer travels *in the join request*: one ICE agent, one DTLS
handshake, no renegotiation. `hel1` speaks it — verified, no fallback taken in
nine live joins.

It is not a free switch, hence the fallback: single-connection mode selects
`signallingJoinRequest`, a different signalling protocol (`&join_request=`
carrying a gzipped proto), so a node that does not speak it has to be met the old
way. That costs a second dial, and only on an instance that needs one. The track
cannot be reused across the two attempts — preparing a publication binds a
transceiver to it — so each builds its own, which is why `newMicrophone` is
separate from `newPublisher`.

**What it bought, honestly.** The renegotiation is gone outright: `start 0s` on
every join since, against 276 ms measured before. Everything else is swamped.
Four joins each way, `voice.Join` end to end:

| | samples (s) | median |
|---|---|---|
| split + publish after | 1.35, 1.92, 2.19, 3.35 | ~2.06 |
| single, published in the join | 0.62, 1.25, 1.85, 3.41 | ~1.85 |

The difference is about one renegotiation, which is what it should be. **The
spread is `hel1`'s and it dwarfs everything this client can do**: `join_call`
alone measured 104 ms, 340 ms, 554 ms, 566 ms, 732 ms, 1.05 s, 1.58 s, 2.32 s,
3.11 s and 3.48 s across ten joins of the same channel. Do not read a single
join as a regression or a win; nothing under two seconds of variance is visible
here.

**Two smaller ones, both taken.** `Instance()` was resolved lazily inside the
first join of a session (~55-70 ms, consistently) — `Client.WarmVoiceNode` is now
called off Ready, so nobody waits on it. And `JoinCall` and the two device opens
ran in order though they have nothing to say to each other; the worker now starts
both and waits for the pair, so a join costs the slower rather than the sum.

Still not measured: the device opens themselves. They are the app's path and the
live harness does not take it.

### lksdk logged through this process's own logger — fixed

`lksdk/logger.go` defaults to `stdr.New(log.Default())`, and nothing called
`lksdk.SetLogger` — so every LiveKit and pion `Infow` landed in the client's log:
a whole `ConnectParams` dump per join, and one "socket operation was attempted to
an unreachable network" per failed connectivity check, from the ICE agent's own
goroutine during the handshake.

Hygiene rather than latency — sixty lines a join is milliseconds — but it buried
the client's own diagnostics under pion's. `voice.quietSink` keeps `Error` and
drops everything below it. It has to be a sink: a verbosity cannot do it (stdr
enables `V(0)` whatever the level) and neither can a logr-level filter,
`LogRLogger.Warnw` mapping onto `Info` alongside `Infow`. The cost is that
lksdk's warnings go too — the TURN allocation failures below among them, which
are documented there instead.

### ICE gathers on adapters that cannot route

Still true at server-sdk-go v2.18.1: `transport.go:219` builds the
`webrtc.SettingEngine` privately and the only thing `ConnectParams` reaches on it
is `IPv6Only`, via `SetIPFilter`. Seen since on this machine as well:
`192.168.56.1`, a VirtualBox host-only adapter, gathered and then failing every
check it is picked for.

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

### A review pass, adversarially verified — fixed

A multi-agent review of the whole voice path, every finding below confirmed by
walking the failure path before it was touched. None verified by ear or on real
hardware; the code paths were.

- **`Call.emit` could panic the process.** A `select` send still panics on a
  *closed* channel — `default` arms only a full one — and lksdk delivers
  callbacks on detached goroutines that outlive `Disconnect`, so a late
  callback racing `close(c.events)` was a crash. `evMu` now orders `emit`
  against `endEvents`, the one shared close path of `Close` and `fail`.
- **Deafen permanently silenced everybody.** `SetDeafened(true)` used
  `sink.Reset()` — the hang-up primitive — which closed every lane; nothing
  reopened them, `Want` answers 0 for a missing lane, so undeafening heard only
  people who subscribed *after*. The lanes are reopened empty right after the
  Reset, which also restores the written-silence cursor-advance the `silence`
  var exists for.
- **Joining muted could send a syllable.** The capture opens before the dial,
  so the ring holds audio by the time the publish loop starts — and the mute
  was applied *after* `newPublisher` returned. It is now applied inside
  `newPublisher`, before the loop exists.
- **A video track would clobber the audio lane.** lksdk auto-subscribes
  whatever is published and `OnTrackSubscribed` had no kind filter, so a
  camera or screen share would have replaced a participant's working lane with
  garbage fed to an Opus decoder. Audio-only now.
- **A stale track reader could close the wrong lane.** `closeLane` was keyed
  on the user alone; after a reconnect resubscribes somebody, the old track's
  reader errors out of `ReadRTP` late and would destroy the replacement lane.
  It now closes only the lane still belonging to its own track.
- **`Sink.Write`/`SetGain` resurrected removed lanes.** Both opened a lane on
  a miss, so a write racing a leave — or the volume menu clicked after one —
  put the lane back, played the departed tail and leaked the slot until the
  next Reset. `Open` is now the only opener, which is what the `Want` docs
  always claimed.
- **A pause read as starvation, and shrinking never took latency out.** The
  jitter buffer treated running dry as proof of shallowness: every remote mute
  or DTX gap deepened it (toward 240 ms over an ordinary conversation), booked
  phantom loss for FEC to buy against, and dropped the resumed stream's first
  packet as late. The cursor now *freezes* on empty and the starve verdict is
  made at refill time — back inside `starveWindow` is jitter, later is a
  sender that stopped. And a shrink now `drain`s held audio down to the new
  depth, one packet per clean five seconds, where before it moved only the
  refill target and occupancy stayed pinned at the worst burst for the rest of
  the call.
- **FEC's 5 % seed survived one second.** `reportLoss` sent the worst lane's
  `Loss()`, which answered 0 before any window had been measured — overwriting
  the seed during exactly the window it exists for. Unmeasured is now negative
  and skipped.
- **The settings meter was dead without a call.** `Capture.Level` is stored by
  `Read` and nothing read an *owned* monitor capture, so the bar sat on the
  floor unless a call's publisher happened to be reading — the inverse of the
  intent. `startInputMonitor` now drives `Read` on captures it opened.
- **Hanging up during an in-flight join did not stop the retry.**
  `failedJoin` lacked the `callGen` guard `installCall` has, so a transient
  failure landing after the reader left silently re-armed a rejoin — and its
  non-transient branch could clear state under a *different* call joined
  since. Same guard both sides now.
- **Anybody's voice event could tear down a fresh join.** `followVoiceMove`
  read "we are in no call" off a store the gateway had not yet filed our own
  join into, and any server's event in that window triggered it.
  `client.VoiceChanged` now carries its subject and only events about this
  account are followed.
- **A swap could mask a real device stop.** `Capture.revive` queued the stop's
  generation in a 1-buffered channel; a stale token parked during a swap made
  a fresh stop's send drop, and the mismatch read as nothing-to-do — a dead
  microphone nothing recovers. The newest stopped generation now lives in an
  atomic and the channel is a bare signal.
- **The receive path allocated 1920 B per frame per participant.** gopus's
  `Decode` allocates its answer, fifty times a second per lane. The fork gains
  `Decoder.DecodeIn` (a caller's buffer; `Decode` delegates to it, so the two
  cannot drift) and each lane decodes into a buffer of its own — behind an
  `opusDecodeIn` assertion like `opusTuning`, so the client builds against
  either gopus and lights up when the pin carries it.
- **A rejoin made while the settings meter held the microphone would fail on
  an exclusive-mode backend** — the meter's open and the join's are two opens
  of one device. `joinCall` now releases an owned meter before the dial and
  `installCall`/`failedJoin` put the bar back on whichever capture wins;
  `startInputMonitor` sits out a join already in flight the same way.

### Done since this file was written

- **The last per-frame allocations on the media path are gone.** The publish
  loop encoded into a fresh 1275 B buffer per frame; the fork gained
  `Encoder.EncodeIn` (mirroring `DecodeIn` — `Encode` delegates, so the two
  cannot drift) and `publisher.encodeFrame` reuses one buffer behind an
  `opusEncodeIn` assertion. Safe because lksdk's `WriteSample` packetises and
  writes inside the call. And `Capture.Read` built a `time.After` timer per
  wait — 50-100 a second — where a reused `Timer.Reset` costs nothing (since
  Go 1.23 Reset flushes a stale expiry, so no drain dance). Byte-identical
  output verified offline against `Encode` before the harness was deleted.
- **Noise suppression grew two dials** — strength and a speech veto; see the
  entry in section 4 for the shape and the measurements.

- **Reconnect after a hard disconnect.** A `CallEnded` carrying an error now
  rejoins rather than reporting: `App.scheduleRejoin` doubles the wait per
  attempt to a 30 s ceiling, gives up after five, and keeps the island on screen
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
- **"Hear myself" is that capture written into a sink lane.** `Capture.SetEcho`
  is filled from inside `Read` — `Read` has exactly one caller, so no second
  reader is possible and none is needed: the test works on the call's stream and
  on the meter's own alike. Being a *lane* is what makes it honest, the mix, the
  call volume and the soft clipping all being the ones a call is heard through.
  `Sink.Reset` exempts it — a call ending must not silence a test — and
  `forgetInputMonitor` is what turns it off, it being a mode rather than a
  setting. No echo cancellation, hence the headphones line under the switch.
- **Everything was 6 dB from silence.** Both directions clamped at `maxGain = 2`,
  and the receive side could only ever *cut*: call volume topped out at 100 %,
  so the only boost anywhere was the per-person menu's 200 %. Now a gain is
  decibels, `config.VoiceGainOffDB`..`VoiceGainMaxDB` (−40 = off, +20 = ×10),
  with `maxGain = 10` to match. Percentages were the wrong unit as well as the
  wrong range: linear on amplitude, so half of one is −6 dB and every useful
  boost crowded into the top of the scale.
- **The input gain ran after the gate**, so cranking it did nothing for a quiet
  microphone the gate was closing on, and the meter measured ahead of it and
  never showed the boost. It is now a `preamp` stage *inside* the chain, after
  RNNoise (which keeps the level it was trained on) and in front of the gate —
  and the meter reads from it, so the bar and the threshold mark measure one
  signal. A settings file's `sensitivity` therefore means something different
  once a gain is set: it is compared against the boosted level now.
- **Soft clipping**, `config.Voice.SoftClip`, on by default. `maxGain` of 10 into
  a hard clamp is a sliced wave, which is buzz rather than loudness; `softClip`
  is a tanh knee at 70 % of full scale, continuous at the knee (slope 1 either
  side) and never reaching the ceiling. It is applied to the capture's output
  frame and to the mixer's *sum* — clipping each source separately is distortion
  the sum would not have had. Cost: a compare per sample below the knee, a
  `math.Tanh` above it. A full-scale peak comes out ~0.6 dB down, which is what
  the curve is.

**Unverified by hand:** all of them. Unplugging a microphone mid-call and
swapping one in settings mid-call are two of the checks in section 6, and neither
has been done on real hardware. The gain work is arithmetic-verified only — the
curve, the round trip and the preamp were checked numerically, and nothing has
been listened to.

## 4. Features not built

- **No echo cancellation.** Headphones are assumed; on speakers the far end
  hears itself. `audio.Processor` is the seam and `Engine` owns both directions
  precisely so the playback reference is reachable. This is the single biggest
  quality gap for anyone not wearing headphones.
- **Noise suppression — done.** RNNoise, the original plan, vendored as
  `internal/audio/rnnoise` (xiph v0.1.1, commit 6cbfd53 — the last release whose
  ~85 KB model ships *in* the tree; 0.2 fetches 30-78 MB of `rnnoise_data.c` at
  build time, which is why the older one). That release prefixed every non-API
  symbol `rnn_`, so its CELT-derived FFT/pitch code cannot collide with the
  libopus gopus links into the same binary — checked with `nm` before believing
  it. It sits between the high-pass and the gate (`noiseSuppressor` in
  `process.go`), so the gate's RMS measures the *cleaned* signal and a fan under
  the threshold cannot hold it open. All three stages are now always in the
  chain and bypassed rather than absent, which is what lets
  `config.Voice.NoiseSuppression` and `HighPass` both move mid-call the way
  sensitivity does: a flag lands in an atomic and `Read` applies it between
  frames.
  Measured offline (CI-class Xeon, not this machine): pink noise −35 dB, synth
  fan-and-hum −36 dB, mains hum −33 dB, all at VAD ≈ 0; a speech-shaped signal
  loses 0.1 dB at VAD 0.88. Full-band *white* noise is the one synthetic it
  barely touches (−1 dB) — out of distribution for the 2018 model, and not what
  a microphone produces. Cost ~63 µs per 10 ms frame on that Xeon, so well under
  1 % of a core while capturing.
  **Not verified by ear** — the numbers say it works; nobody has listened to it.
  It has **two dials** now, both live mid-call like every other chain setting:
  - *Strength* (`config.Voice.NoiseSuppressionDB`, 0–40, default 40 =
    uncapped): a floor under the model's band gains, patched into the vendored
    `denoise.c` (`rnnoise_set_gain_floor`, every change `rgoclient`-marked for
    the next re-vendor). Spectral flooring rather than a dry/wet mix because
    RNNoise carries 10 ms of algorithmic latency — an undelayed dry path would
    comb-filter, and a delayed one is a buffer the floor makes unnecessary.
    Measured: floor at 20 dB caps a −58 dB synthetic at −20.1 dB; floor 1
    passes through at −0.1 dB.
  - *Speech veto* (`config.Voice.VADThreshold`, 0–100, default 0 = off): the
    model's per-frame speech probability — computed all along, discarded until
    now — as a second condition on the gate *opening*. A veto and never a vote,
    so the RMS gate stays the one decider (the rule the old comment defended);
    it exists for the loud non-voice the RMS threshold cannot reject — keyboard,
    doors. Vetoed frames count down the hangover rather than freezing it, so
    sustained rejected noise closes the gate. Armed only while suppression runs,
    the model being what answers.
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

Re-measured 2026-08-25 on a 9950X3D, 48 kHz mono VoIP at 32 kbps, 20 ms frames,
libopus 1.5.2 built `-O3 -march=x86-64-v3`. The codec rows replace older ones
that are discussed below; the mixer rows are unchanged and were not re-taken.

| | cost | share of budget |
| --- | --- | --- |
| Opus encode | 38.0 µs/frame | 0.19 % of one core at 50 fps |
| Opus decode | 13.4 µs/frame | 0.07 % per participant |
| Opus decode, complexity 5 | 17.6 µs/frame | 0.09 % per participant |
| Opus PLC, classic | 16.7 µs/frame | only while concealing |
| **Opus PLC, deep** | **226.7 µs/frame** | **1.1 % of realtime, per concealed frame** |
| Mixer, 1 lane | 1.6 µs/period | 0.016 % of the 10 ms budget |
| Mixer, 20 lanes | 18.1 µs/period | 0.181 % |
| Mixer, 50 lanes | 44.4 µs/period | 0.444 % |

### Deep PLC is not free, and the old numbers here were wrong

Three claims that used to stand in this section do not survive re-measurement:

- **"Clean decode is identical either way."** It is not. A decoder at complexity
  5 costs about a quarter more on every frame that *arrives* — 17.6 µs against
  13.4 — because the model has to be fed whether or not anything is lost
  (`celt_decoder.c` calls `update_plc_state` on each good frame).
- **"Concealment is 3.1×, 8.0 → 24.7 µs."** It is **13.5×**, 16.7 → 226.7 µs —
  and 690 µs without the AVX2 floor.
- **"165 µs/frame" for encode** is not reproducible here; encode is 38 µs, or
  41 µs at `-O2`. Unexplained rather than explained away.

The likely cause of the first two is a trap worth writing down: **timing a run of
consecutive lost frames measures the wrong thing.** `celt_decoder.c:643` flips to
noise-based concealment once `loss_duration` reaches 80 ms, and that path never
reaches the model at all — so a benchmark that decodes nothing but holes reports
the cost of comfort noise and shows no difference between complexity 0 and 5.
The shape that measures it is one hole among good frames. (This bit twice: the
re-measurement made the same mistake first and briefly concluded Deep PLC was
never running.)

**Decoder state is 85 KB** with it compiled in, so 1.7 MB across twenty
participants, paid whether or not the switch is on — the state is part of
`OpusDecoder`. That still stands.

It is still right to default it on: 227 µs is 1 % of realtime for one stream, and
only a stream already losing packets pays it. But it is no longer true that it
costs nothing, and a fifty-person call on a weak machine with a lossy link is a
case worth remembering when this is next looked at. The switch also exists
because a build without the model — or a system libopus older than 1.5 on the
`opus_shared` path — reports the CTL as unimplemented, which
`lane.applyDeepPLC` logs once and carries on from.

### SIMD and compiler flags: the codec barely cares, Deep PLC does

Two separate things live in `sentinelb51/gopus`, and only the second matters.

**libopus's own intrinsics** are on: `config.h` presumes what each architecture
guarantees (SSE and SSE2 on amd64, Neon on arm64), `OPUS_HAVE_RTCD` stays
undefined, and the `PRESUME_*` branches resolve every dispatch macro to a direct
call — no table, no cpuid, no floor. Worth **0.5 %** of encode and nothing of
decode, measured with the presume block on and off. gcc 15 auto-vectorises the
float loops celt's SSE intrinsics were hand-written for in 2013. Kept because it
costs nothing, not because it bought anything.

**`-O3 -march=x86-64-v3`** is the one that pays, and it pays for `dnn/vec_avx.h`
rather than for celt. That header picks its width from the compiler's own
`__AVX2__` and `__FMA__`, so without the flag the neural model runs its SSE2
fallback — which is what "Deep PLC is vectorised everywhere with no flag at all"
used to mean here, and it was only half the story. `-tags opus_baseline` drops
the floor for anything older than Haswell (2013), where the failure would be
SIGILL rather than a slow decode.

Dead ends, so nobody spends the afternoon again:

- **`-march=native` is 13 % *slower*** than v3 on Zen 5. `vec_avx.h:623` takes
  the hardware VNNI dot product wherever one exists, and that instruction loses
  to the AVX2 sequence it replaces; `-mno-avxvnni` recovers every bit of it,
  which is what pins the cause. It is a property of this CPU, not a rule — on a
  part with fast VNNI, native would likely win.
- **`x86-64-v4`** is 3 % better than v3 and rules out every Intel consumer part
  since Rocket Lake. Not worth it.
- **`-Ofast` does not compile.** `celt/arch.h:201` refuses it: *"Cannot build
  libopus with -ffast-math unless FLOAT_APPROX is defined."*
- **`-fno-math-errno -fno-trapping-math`** are the halves of that which *are*
  safe here and are worth a further ~6 %, but cgo's flag allowlist rejects every
  `-f` flag outside its own list, so a library cannot carry them. They have to
  come from `CGO_CFLAGS` at build time.
- **LTO buys nothing and is broken.** `-flto` passes the allowlist and produces
  a binary Windows will not load. It would gain nothing regardless: the
  amalgamation is already one translation unit of 138 files, so cross-TU
  inlining has been done at the source level. cgo compiles three TUs on amd64.
- **`-funroll-loops`** is inside the noise.

arm64 is **unmeasured** — no machine here. Neon is already 128 bits wide and
`vec_neon.h` is unconditional, so there is no equivalent of the AVX2 cliff; the
further step would be `OPUS_ARM_PRESUME_DOTPROD`, which is an ARMv8.4 floor and
so a much harsher cut than v3 is on x86. Measure on Apple Silicon first.

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
- **Pull the network mid-call.** The reconnect should take over, the island should
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
  hand-off, the maintained `held` count, and the pause that must not read as
  starvation. These fail for reasons a person would not spot by reading.
- `internal/voice/seam_test.go` — compile-time proof that `*audio.Capture`
  satisfies `voice.PCMSource` and `*audio.Sink` satisfies `voice.PCMSink`.
- `internal/voice/live_test.go` — **the live call.** Skipped unless `RGO_LIVE` is
  set; signs in with the saved session on this machine, finds a voice channel,
  joins and reports what identities the voice server hands back. This is the
  fastest way to answer "is it actually working".
- `internal/ui/settings_index_test.go` — the regression most likely to ship
  silently: the settings search index builds every section twice, and the Voice
  section owns a microphone.
- `internal/audio/mixer_bench_test.go` — the numbers in section 5.

**No test opens an input device, and none should.** Two used to. The live call
took the real microphone by default (`RGO_LIVE=tone` was the opt-*out*) and
published it to a real channel for twenty seconds while asserting nothing about
it; it publishes a sine now, and the device is not reachable from there at all.
`internal/audio/devices_test.go` reported the peak signal per input and was
worse: it was gated on nothing, so every `go test ./...` — CI included — opened
every microphone on the machine for two seconds each with the gate wide open. It
had no assertions either, which made it a harness rather than a test. Deleted.

What that costs is real and worth naming: the **real capture chain end to end**
— device, high-pass, RNNoise, preamp, gate, encoder, track — is no longer covered
by anything automated, and the last time it went unverified the client published
no audio at all for weeks (section 3). It is the app's job to demonstrate now.
Run the client, join `voice-test`, and watch the settings meter move.

## 8. Housekeeping

- `voice-test` channel in **Big up testers** was created for testing and is
  still there.
- `go.mod` requires `github.com/sentinelb51/gopus` at a commit on `master`
  **directly** — the fork owns that module path, so there is no `replace`. It
  used to keep upstream's path and be reached through one, which left
  `layeh.com/gopus v0.0.0-20210501142526` in the require block: a coordinate
  whose vendored tree is Opus 1.1.2 and which scanners flag for CVE-2017-0381,
  fixed upstream in 1.1.4 and present in the 1.5.2 this fork carries. A
  `replace` would have been wrong here anyway — it applies to the main module
  alone, so it stops working the moment `internal/voice` lifts out as a module
  of its own.
  The FEC/DTX commit, the libopus 1.5.2 bump, the Deep PLC vendoring,
  `Decoder.DecodeIn` and `Encoder.EncodeIn` are all merged there; the pin
  carries both, so the receive path decodes and the publish loop encodes
  allocation-free through the `opusDecodeIn` / `opusEncodeIn` assertions. The `opus_shared` build path — link the system libopus instead —
  has never been compiled on this machine, pkg-config not being installed; the
  `Decoder.SetComplexity` shim was added to both files but only the vendored one
  is proven.
- `go.mod` pins `sentinelb51/revoltgo` to the commit carrying section 2's four
  additions. Nothing else in this client needs a newer one, so a `go get -u`
  through `scripts/update-deps.sh` is the next thing that will move it.
- `docs/known-gaps.md`, `internal/client/CLAUDE.md`, `internal/app/CLAUDE.md`
  item 36, `internal/ui/CLAUDE.md` and `docs/performance.md` all carry voice
  notes and are current as of this file.

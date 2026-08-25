# Known gaps

What is not built, and what is limited by revoltgo or Fyne rather than by
effort. Linked from the root `CLAUDE.md`; read it before concluding something
is missing by accident.

Simply not built, no constraint behind it: an attach button (files arrive by drag
or paste), role mentions, a notice history panel,
a hue wheel/alpha/eyedropper in the colour picker,
`MessageEmbedSpecial` (YouTube, Spotify, …), and moderation beyond what a
server's settings page and the destructive sidebar items offer — role edits,
nicknames, timeouts and channel deletion are each one call away but deliberately
not offered.

The gateway events still unregistered are the ones nothing here has to do about:
`EventEmojiCreate` / `Delete` (revoltgo's own default handlers file them into
`State`, and the picker reads `State` as it opens, so a handler here would have
nothing left to arrange), the webhook and report events, and
`EventUserSettingsUpdate` (revoltgo flags its msgp tuples as undecodable).
Everything a server, role, member or channel can do is handled.

Where something is limited by revoltgo or Fyne rather than by effort:

- **A call is audio only.** Joining works — `internal/voice` publishes one Opus
  track to Stoat's LiveKit node and mixes everybody else — but there is no
  camera, no screen share and no way to *watch* either, so a participant sharing
  one is drawn with the mark and nothing behind it. Nothing records a call.
  What a participant's own end is doing is still drawn only as far as it is a
  plain fact — `is_publishing` / `is_receiving` are a media session's bookkeeping
  rather than muted and deafened, so neither is shown. A *remote* speaking ring
  comes from the voice server's active-speaker report; this account's own comes
  off the capture gate instead, that report being about other people and landing
  about half a second late.

- **The voice node is measured, not chosen.** `join_call` requires a node by
  name; `nearestVoiceNode` dials every node the instance offers and takes the
  first handshake to complete, caching it for the session. The coordinates each
  node carries are not used — that would need the reader's own position, which
  this client neither knows nor should ask for. There is no surface offering the
  list, so a reader who wants a *particular* node cannot say so. stoat.chat
  publishes one today, which is taken without a probe.

- **ICE candidates cannot be filtered.** lksdk builds its `webrtc.SettingEngine`
  privately in `transport.go` and exposes only `IPv6Only` and
  `DTLSEllipticCurves` on `ConnectParams` — no `SetIPFilter`, no
  `SetInterfaceFilter`. So pion gathers on every local adapter, including ones
  that cannot route: an APIPA address left by a NIC with no DHCP lease, or an
  `fe80::` link-local on a machine with no global IPv6. It still connects, the
  working adapter's candidate winning, but the dead ones are checked first and
  logged per pair. Fixing it means a hook upstream in `livekit/server-sdk-go`.

- **No echo cancellation.** The capture chain is a high-pass, RNNoise, a preamp
  and a noise gate; `audio.Processor` is the seam AEC would go in and `Engine`
  owns both directions precisely so the playback reference is reachable, but
  nothing implements one. Headphones are assumed — on speakers the far end hears
  itself.

- **No automatic gain control.** The input gain is a number the reader sets, and
  the gate's threshold is another; neither follows the room. That is deliberate —
  an adaptive floor that guesses wrong is a microphone nobody can reason about —
  but it does mean a quiet microphone is fixed by hand, up to `maxGain`'s +20 dB,
  and that a gain far enough up to need soft clipping is amplifying the room's
  own noise along with the voice.

- **Push-to-talk is Windows-only, and binds from a list rather than a captured
  key.** `ui.KeyHeld` is `GetAsyncKeyState`, which needs no canvas focus — the
  same route `ShiftHeld` takes, and the reason the mode works at all while the
  composer holds focus. X11 would need `XQueryKeymap` on a display connection
  this client does not own and macOS an Accessibility grant, so
  `PushToTalkSupported` is false there and the settings page leaves the mode out
  entirely rather than offering one that silently behaves as voice activity.
  The key comes from a curated list (`pushToTalkKeys`) because *capturing* an
  arbitrary key still needs focus; the list is the modifiers and mouse buttons
  people actually bind.

- **DRED and OSCE are not vendored.** `sentinelb51/gopus` vendors libopus 1.5.2
  plus the `dnn/` sources Deep PLC needs, so neural loss concealment is available
  and switched by the "Repair dropped audio" setting. What is still out is DRED —
  redundant copies of past speech stapled to later packets, which recovers a burst
  loss instead of concealing it — and OSCE's LACE/NoLACE decoder enhancement.
  DRED is the one worth wanting, and the reason it is not here is that it needs
  the *sender* to enable it too: against the official web client, or anything else
  that is not this library, it buys nothing. The two are also 10 MB of model data
  against Deep PLC's 5.
  **amd64 builds require AVX2** — `-march=x86-64-v3`, so Haswell or Excavator
  (2013) and newer; below that the failure is SIGILL, and the answer is
  `-tags opus_baseline`, which drops the flag and roughly triples the cost of a
  concealed frame. The floor is there for Deep PLC, whose `dnn/vec_avx.h` reads
  the compiler's own `__AVX2__`: without it the model runs an SSE2 fallback at
  690 µs a concealed frame against 227. libopus's own SSE/SSE2/Neon intrinsics
  are on as well and are worth 0.5 %, gcc having long since learned to
  auto-vectorise what they were hand-written for. arm64 takes no floor and is
  unmeasured. Released amd64 binaries compile the *Go* half at the matching level
  too (`GOAMD64: v3` in both workflows), which costs no compatibility the C floor
  has not already spent. AVX-512 was measured and left alone: 3 % on the one
  workload that moves, against ruling out every Intel consumer part since Rocket
  Lake. See `docs/voice-chat-todo.md`.
  The encoder CTLs are reached through `voice.opusTuning`, an interface assertion
  rather than a direct call, so the client still builds against a binding without
  them — at the cost of a deeper jitter buffer, and it says so once at join.

- **There is no desktop notification, and Revolt's own push is unreachable.**
  `/push/subscribe` takes a `WebPushSubscription` — a browser service worker's
  endpoint and its VAPID keys — so there is nothing a Go desktop client can
  receive on, and nothing it would gain: the gateway is already open and the ping
  has already arrived. The *local* half is a toast, and that is left unbuilt on
  purpose. `fyne.App.SendNotification` spawns a PowerShell process per
  notification, and it raises the toast under `CreateToastNotifier(appID)` with
  the app's UniqueID — which Windows drops unless a Start Menu shortcut carries
  that AppUserModelID, and an unpackaged, unsigned build has none. Its template
  also has no way to mark the toast silent, so it would sound Windows' own chime
  over the client's. What is built instead is `ui.FlashTaskbar`, which answers for
  any window with a handle. **On anything but Windows nothing is signalled at
  all** — the settings group is dropped rather than drawn.
- **A sound is a WAV or an MP3, and the built-ins are synthesised.** Nothing ships
  as an asset: `audio/synth.go` renders each default, so there is no licence, no
  binary and no missing-file state, and a custom file replaces one rather than
  filling an empty slot. The decoder reads WAV (8/16/24/32-bit PCM, 32/64-bit
  float, extensible) and MP3, and nothing else — no OGG, no FLAC, no Opus — each
  being another decoder in the tree. A file is capped at 16 MiB on disk and 30
  seconds decoded, and resampling to the device's rate is linear, which is
  inaudible on a click and not what anybody should master music through. **Only
  the volume is configurable, not the pitch**: a take is a rendered buffer, so
  varying pitch per play would mean resampling at the moment of the keystroke —
  the built-in typing clicks are four takes rotated with a gain jitter instead,
  and a custom file gets the jitter alone, so a run of one repeats exactly.
  Overlap is bounded (six voices for a keystroke, two for anything else) and the
  engine drops a play its queue cannot take rather than blocking the UI thread, so
  a stalled device loses clicks instead of stuttering the client. `oto` allows one
  audio device per process and it is never given back — `Engine.Close` releases
  the players and leaves the context, there being nothing else in the process that
  wants it.
- **Two sounds have no honest trigger.** Revolt sends no "reconnecting", and
  revoltgo surfaces only a *fatal* drop, so `offline` is a session that ended and
  `online` is the next Ready after one — not a flaky connection recovering, which
  the client never learns about. A reaction is announced only for a message this
  client can still resolve: `alertReaction` reads the author out of the cache, so
  a reaction to something scrolled far enough back sounds nothing.
- **The member list is as complete as one request makes it.** Revolt's members
  endpoint has no pagination, no search and no Discord-style lazy subscription to
  the slice of the list actually on screen, so a server is one whole fetch or
  nothing — `exclude_offline` is the only lever it offers. Nothing keeps the
  membership current afterwards either: joins and leaves arrive on the gateway, but
  a client left running on a very large server drifts until it re-enters. The
  sections are Revolt's *hoisted* roles as the server defines them, with no way to
  reorder or collapse one, and a role's icon is dropped at the boundary. Presence
  reordering is the client's own debounce rather than anything the gateway batches.
  Its **timeout gives up watching rather than giving up**: revoltgo takes no
  context, so `memberFetchTimeout` only stops the strip claiming to be loading —
  the request is still out, a retry made before it lands is refused as `ErrBusy`
  and waits on the first, and an answer arriving after the strip has offered a
  retry is still installed. Nothing reports progress either: the endpoint is one
  response, so the mark sweeps rather than filling.
- **Slowmode** runs off the client's own clock: the `InSlowmode` rejection carries
  an authoritative `retry_after`, but revoltgo surfaces failures as a formatted
  string. A send refused because the cooldown started elsewhere reports the generic
  notice, and nothing hints at a cooldown outside the open channel.
- **A jump window is read-only in the sense that nothing updates it.** The page it
  mounts is not in the message cache, and every event handler writes there —
  `refreshMessage` finds nothing, so an edit, a pin or a reaction made while a jump
  is up does not reflect until the column comes back to the present. A delete does,
  `removeMessages` walking the mounted widgets rather than the cache. Live messages
  do not mount either, which is the behaviour any detached scrollback already has.
  The pinned-messages panel and channel search are the second and third things
  that open one. There is no way back to where the reader *was* — "Jump to
  present" is the tail, not the position the jump left.
- **The pinned-messages panel is a snapshot.** A pin is a flag on the message and
  Revolt publishes no collection of them, so the panel is one `ChannelSearch`
  made when it opens and nothing keeps it current: a pin made from another client
  — or by this account, from a message the panel is covering — appears only when
  it is reopened. It is
  capped at the hundred newest pins, Revolt's own ceiling on a search, with no way
  to page past it, and a row is a flattened one-line summary — a body with no text
  says what it carries instead of quoting nothing.
- **Channel search inherits that hundred-result ceiling and adds three limits of
  its own.** It searches the **open channel** only: Revolt's route is per channel,
  so there is no search across a server, let alone across the account. The
  matching is MongoDB's full-text search rather than a substring scan — words, not
  fragments, with what counts as a word decided by the server — so half a word
  finds nothing and there is no way to ask for a phrase. A query is 1–64
  characters (a longer one is cut before it is sent) and runs on Enter rather than
  as you type, each one being a request.
- **Search filters narrow the answer, not the request.** `DataMessageSearch` takes
  a query, an order, a limit and a `pinned` flag that cannot be sent beside a
  query — there is no author, attachment, mention or reaction filter on the wire —
  so the island's chips are applied to the hundred that came back. Narrowing by
  author finds that author's messages *among those hundred*, not their hundred,
  which is why the count line reports both numbers. The three orders **are** sent
  (`Relevance`, `Latest`, `Oldest`), so changing one is a fresh request while
  toggling a chip is free.
- **The mention inbox lists what Revolt has kept, which is not everything that
  ever named you.** The set is the `mentions` array on each unread marker, so it
  holds only what is still *unread*: acknowledging a channel prunes it, and there
  is no history of mentions already read. `@everyone` and `@online` are message
  flags rather than entries in that array, so a channel-wide ping washes the
  message warm and rings the sound without ever reaching the inbox or the sidebar
  count. A mention whose message was since deleted stays counted — Revolt prunes
  the array on an ack and on nothing else — and shows as one row fewer than the
  badge says. The panel resolves the newest `inboxLimit` of them, each being a
  request, and there is no paging past that; the sidebar still counts the rest.
  Nothing is muted, either: Revolt's per-channel notification settings are user
  settings this client does not read, so a muted channel counts like any other.
  **Dismissing one lasts as long as the client runs.** Revolt has no route
  dropping a single mention — the array is cleared by acknowledging a message,
  which would mark everything before it read as well — so `App.dismissMention`
  forgets it locally and `App.dismissedMentions` keeps every reconnect's `Ready`
  from handing it back. Nothing persists it, so a restart before the channel is
  ever opened brings it back.
- **A server is created with a name and nothing else.** Revolt takes no icon and
  no description at creation, so the card is one field; both are set afterwards
  from the server's own settings page.
  Nothing in the response can be believed either (see the revoltgo note), so the
  new server appears when the gateway says so rather than when the request
  returns.
- **A channel is edited for its name, topic, cooldown, user limit and age gate.**
  Stoat gates the whole edit on one permission (`ManageChannel`), so the card
  offers everything its kind has or does not open — there is no field to grey out.
  Two of the route's fields are left: the icon and a group's owner transfer want a
  file picker and a member picker. `archived` is not a gap — the spec lists it and
  `channel_edit.rs` reads it nowhere, so no client can set it.
- **A composed mention** stays a visible `<@id>` until sent — Fyne can't draw a chip
  inside an entry, and mapping names back to IDs at send time breaks on duplicates.
  `markdown.PlainText` has no session, so a reply preview of a message opening with
  one starts with a lone `@`.
- **Text selection** only works on uniform-style bodies, which flatten to a
  `Selectable` Label; Fyne 2.8 has no public RichText selection, so anything
  mixed-style — including any body carrying a mention — is covered by right-click →
  Copy message instead. Selecting *across* messages isn't possible.
- **Markdown:** strike/underline/spoiler share one `decoratedSegment` (Fyne renders
  neither natively), split per word since RichText only breaks rows at spaces. The
  blockquote bar isn't drawn on wrapped continuation lines; a decoration can show a
  one-space nub at a wrap; inline `code` inside a decorated span isn't decorated.
  A decorated word landing at a line end overhangs the column and is clipped, as a
  mention would without `mdBuilder.reserve`; the reserve is not extended to them.
  A quote's bar is drawn at body size whatever the row it opens, there being no way
  to ask a spliced segment what the row around it settled on. A nested list indents
  with spaces in the marker segment, RichText offering nothing else that moves the
  start of a row. A **fenced block** is a well of its own with a one-pass
  highlighter over it — comments, strings, numbers, a language's keywords and an
  identifier that is called, no more; an unlabelled fence is guessed from a marker
  and falls back to a generic C-shaped syntax. Being a card, it is never part of a
  uniform body, so a message carrying one cannot be selected — and highlighting is
  what forecloses it either way, a selectable Label being one segment of one style.
  The chip in the well's corner (`ui.codeCopy`) is what stands in for the drag:
  it copies the block, without the fences the message menu's copy keeps.
  A **relative timestamp is resolved once**, when the row mounts, and nothing
  re-reads it: "in 5 minutes" on a message left on screen stays that until a
  scroll past it remounts the body. Nor is one hoverable — the absolute instant
  behind an "R" is not reachable, a body carrying no tooltip.
- **Embeds** render site line, title, description, colour and one picture. A bare
  **video** embed is dropped at the boundary (revoltgo carries only the URL, and
  there is no player); a bare **image** embed has the same missing dimensions, so it
  draws against the placeholder until the picture lands.
- **An invite card does not refresh.** A server joined from another client keeps
  offering Join until the channel is reopened, the card being filled once when it
  resolves. Its **banner** is drawn only on a preview — the card the join dialog
  mounts from an invite already in hand: in a message it mounts saying nothing and
  is filled a moment later, so a part only some invites carry would shove the
  messages under it down as the answer landed. A **group** invite is named and
  offered as a group, but never with a picture: Revolt describes one with the
  channel fields alone and sends no icon, so it is always the initial.
- **A custom emoji** is a still: `image.Decode` takes the first frame of an
  animated one. It has no name beside it either — nothing tooltips a message body
  — so one whose picture fails to arrive leaves an empty square rather than the
  `:shortcode:` other clients fall back to. Two things hold that square there:
  `ImageCache` reports a load that failed to nobody (`LoadAsync` simply returns),
  and a segment reserves its width before the load starts, so text arriving in its
  place would re-flow the line under somebody already reading it. A *flattened*
  line does fall back — `markdown.DocumentTextNamed` takes `Store.EmojiName`, so a
  reply preview and a panel row quote `:shortcode:` — but only for the servers the
  account is in, a name being held nowhere else. A reaction chip inherits all of it, the
  square there being all the chip has to say. The **picker** names one at a time:
  its tooltip reads what the pointer is over, so a cell whose picture has not
  landed can be identified but not scanned for, nothing captions the cells
  themselves, and nothing says which one Enter would take — the caption line that
  did is gone. It is capped at `emojiPickerLimit` drawn at once — past that
  the field is the only way through — and it lists only what `State` holds, which
  is the servers the account is in: an emoji from anywhere else renders in a
  message and in a chip but is in nothing to be picked from.
- **A reaction says how many, and who only on hover.** The chip draws a count and
  its tooltip names the first ten people in `domain.Reaction.Users`; anybody the
  store cannot name is counted rather than named, which for a reaction reaching
  accounts nothing on the page has fetched is the ordinary case, and nothing
  fetches them for the sake of a label. Ordering is by emoji rather than by who
  reacted first — the payload is a map, and there is no first to read. Clearing
  them all lands wherever it was made: `EventMessageUpdate.Data` is a
  `PartialMessage` now, so the empty map Revolt announces a clear with is
  distinguishable from an edit that never mentioned reactions at all. One emoji
  taken off wholesale is a different event and lands too. `Message.Interactions` (the
  emoji a message *restricts* reactions to) is dropped at the boundary, so a pick
  it forbids is refused by the server rather than not offered.
- **A typing indicator** runs off the client's own clock: Revolt sends no
  heartbeat and no reliable stop, so an entry is carried by the events that keep
  arriving and lapses at `typingLifetime`. Somebody who closes their client is
  shown for up to that long. Nothing marks a channel outside the open server, the
  sidebar being the only surface besides the open channel's line, and a name too
  long for the row is truncated rather than the line wrapping — it shares its row
  with the slowmode chip, which is pinned to the far edge. `TypingShowSelf` draws
  what everyone else is shown, not what they have actually received: the local
  echo does not know whether the announcement reached the gateway, and with
  `SendTyping` off it is a preview of a line nobody is being sent.
- **A system line** names only the subject — Revolt sends no actor, so a kick reads
  "X was kicked". A rename says only that it happened.
- **Profiles** don't refresh while open, and the banner is flat: a `canvas.Image`
  takes no gradient mask. The About section is the dialog's alone, and scrolls, as
  are the mutual ones. A mutual server or friend now leads somewhere, but only one
  the store can *name* does — the rest are a "+n" that answers nothing — and
  `channels` (the groups and conversations in common) is dropped, Revolt sending it
  and nothing here having a place to say it.
- **An account can be secured, but not ended.** The Security section changes the
  password and the email, turns an authenticator on and off, shows and replaces
  the recovery codes, and lists every login with a way to revoke one. What it does
  not do is **disable or delete the account**: deletion is a two-step flow through
  a token Revolt emails, which this client cannot read, and neither is something
  to offer beside a rename. `EmailOTP` and `SecurityKeyMFA` are read from the
  status and drawn nowhere — there is no WebAuthn here and no way to read a mail,
  so an account secured either way is one `answerable` refuses to raise a
  challenge for, and it says so rather than offering a field that cannot work.
  Changing the **email** takes effect when Revolt's confirmation mail is followed,
  which this client cannot see: the section goes on reading the old address until
  then, and a notice says why. A **partial** failure of the section's one fetch
  reads as a total one — the three requests share an error, so a route being down
  greys the lot rather than a third of it.
- **A login can only mark itself as "this device" if it recorded its own session
  ID.** No route answers which of an account's sessions the caller is; the login
  route says it once, in `_id`, and nothing else ever does. So a session restored
  from a token saved before this client began recording one is listed without the
  mark, and `EventAuth` cannot tell that session being revoked from any other —
  both say so rather than guessing, and a fresh sign-in is what puts it right.
- **A group is made, named and grown, and has no picture.** Revolt takes a name,
  a description, an age gate and up to 49 friends at creation; this client asks
  for the name and the people, and the rest is the ordinary channel edit
  afterwards. The **icon** is not settable from here — `ChannelEditParams.Icon`
  takes an upload into a bucket of its own, as a server's did before it was built
  — so a group wears its members' faces wherever one is drawn. The picker lists
  every friend with no way to filter, which is the shape of the friends list
  itself (below): a long list is scrolled rather than searched. Nothing counts how
  many are in a group anywhere but its member sidebar, and **adding** has no
  client-side ceiling at all — the create route documents 49 and the add route
  documents none, the real limit being a runtime setting of the instance's, so
  one past it is refused by the server rather than not offered.
- **The friends list is as complete as the account cache is.** It is a walk of
  the cached users, so somebody Ready did not name and nothing has since fetched
  is not in it, and nothing announces an incoming request beyond the sidebar row's
  mark — which is only visible in the home view. It has no search and no ordering
  but by name. Somebody can now be asked **by handle** from the field at the head
  of the page — the one way to reach an account the client has never drawn — but
  the answer names an account nothing has cached, so the row appears when the
  gateway files it rather than when the request returns, and a refusal cannot say
  which of "no such handle" and "already asked" it was: Revolt answers both with
  a status code and no sentence. Somebody's presence is drawn but their status
  text is not: a row is a name and a handle, and the handle is what tells two
  identical names apart. A
  relationship change made from the profile card still closes it and reports
  through a notice, a profile not refreshing in place. A *server* relationship —
  Revolt's own `Relations` array on the account — is dropped at the boundary, so
  what the client holds is one value per person it has met rather than the whole
  graph.
- **Holding Shift skips a confirmation on Windows alone.** Fyne answers no
  question about a modifier that is not part of an event: `desktop.Canvas`'s key
  handlers fire only while *nothing* holds focus, and the composer holds it for
  most of the client's life, while a context-menu item is Fyne's own widget and
  reports no modifiers at all. So `ui.ShiftHeld` asks Win32 directly and the other
  half of the pair answers false, where every confirmation is asked and the card
  offers no hint that it could be skipped.
- **The OS file picker is wired up on Windows alone.** Fyne offers only a browser
  it draws in the canvas, which knows nothing the shell knows, so `ui.PickFile` /
  `ui.PickFolder` call the Common Item Dialog through COM. The `!windows` half
  reports false and the caller falls back to Fyne's — a picture, a sound or a
  cache folder is still choosable there, in a dialog that looks like nothing else
  on the machine. GTK/AppKit or the XDG desktop portal is what the other halves
  would take, each a binding of its own.
- **Pinning to a kind of core is Windows and Linux only, and reads two splits
  out of the possible many.** `internal/cpu` answers with no split on macOS,
  which has no affinity API to answer with: `thread_policy_set`'s affinity tag is
  a hint the scheduler may ignore and does ignore on Apple silicon, so the
  Processor cores group is left off the page there rather than drawn as a control
  that would do nothing. Three narrower gaps sit inside the two platforms that do
  work. A machine past 64 logical processors has more than one processor group,
  and `SetProcessAffinityMask` names processors within one group only, so such a
  machine is reported as having no split rather than pinned to a group the
  indices may not be in. The chiplet split is exactly two L3 domains on an AMD
  part — three or more, a Threadripper, are not offered — and which chiplet
  carries the better bins is never read: CPPC's preferred-core ranking has no
  user-mode answer on Windows (Linux publishes it as `amd_pstate`'s
  `highest_perf`, left unread so the two platforms answer alike), which is why
  the sides are named CCD0 and CCD1 by the machine's numbering and the default
  is CCD1 by convention rather than by measurement. And on Linux the syscall is
  per-thread with no process-wide form, so `pin` walks `/proc/self/task` twice
  and a thread created between the listing and the last call inherits from
  whichever thread started it — which is the right mask in every case but a
  vanishing race.

- **CI ships bare binaries, not installable applications.** All three targets
  build and are attached to a release, but nothing is packaged: macOS gets a
  Mach-O rather than a signed, notarised `.app`, so Gatekeeper refuses it until
  the quarantine bit is cleared by hand, and Linux gets an ELF rather than a
  `.desktop` entry, an AppImage or a package. `fyne package` is what would do it
  — it wants a `FyneApp.toml` and an icon the client does not currently set — and
  the signing halves want an Apple developer identity. The Windows exe is
  unsigned for the same reason. The matrix also builds one architecture each:
  amd64 on Windows and Linux, arm64 on macOS.

- **A button cannot be tabbed to.** Every text button is `ui.Button`, the client's
  own — Fyne's carries no edge and no theme name reaches its background — and it
  is not `fyne.Focusable`, so no button is reached by Tab or pressed with Space. A
  card that has to be answerable from the keyboard is answered from its *field*
  instead (`OnSubmitted` → `Button.Tap`); a card with none — a confirmation — is
  answered with the pointer or dismissed with Escape.
- **A gradient role colour** spreads across a *name* only; elsewhere it fills as the
  mean of its stops. `parseColor` reads hex stops only, so `rgb()` or a CSS name
  falls back to the default text colour.
- **The scroll indicator** only reports position — no drag, no track to click.
- **Every entry used to leak fonts, and Fyne is patched so it no longer does.** A
  caret is drawn `SizeNameInputBorder` wide in `Primary`, and the focus ring takes
  `Primary` too — so the only way to have one without the other is a scoped
  override, which is what `ui.WithCaret` is for. But `cache.OverrideTheme` mints a
  scope from a counter that never repeats, and `ThemeOverride` calls it from its
  constructor, `CreateRenderer` **and** `Refresh` — while `painter.CachedFontFace`
  keys on `{style, scope}`. Upstream `loadMeasureFont` has no cache, so every
  refresh reaching an entry re-parsed Montserrat, NotoSans, InterSymbols and
  Fyne's 4.2 MB `EmojiOneColor.otf` into a map nothing evicts: **~6 MB per
  open/close of a settings number box**, measured on one canvas. Nothing above the
  toolkit fixed it — reusing the entry and its wrapper across edits measured
  identically (6.048 vs 6.060 MB), since it is `Refresh` that mints, not
  construction. The patched copy caches the parse per resource instead
  (`rgoclient-fyne`'s `PATCHES.md`), so a fresh scope costs a map entry. The scopes
  themselves still accumulate; they are now a map entry each rather than four font
  parses each. Emoji could not be dropped to dodge it either: `-tags no_emoji`
  builds and runs, but emoji then render as **nothing** — go-text does not resolve
  Windows' Segoe UI Emoji through the system fallback, though it does resolve
  CJK.
- **Settings** that are read once while the caches are built (cache directory,
  message cache caps, text-preview count, concurrent downloads — the last being a
  channel sized at construction, which `SetLimits` cannot resize under the
  goroutines holding it) need a restart, and each row says so.
  The Advanced filter matches field names only; the curated Styles groups aren't
  searchable. The login screen has no notice layer (it isn't built until Ready), so
  everything it reports — a dead token, a refused password, a refused second
  factor, a snapshot that never came — goes on its one `ui.StatusLine`, one
  message at a time and gone at the next screen.
- **The status line is set, not seen.** Nothing in the client draws anybody's
  status text, this account's included, so the settings row is the only place it
  appears and it shows what the store last said rather than what was just typed:
  the change returns as a gateway event, and the page is not rebuilt for one.
- **A picture is sent as it is, and a profile is a snapshot.** Nothing is checked
  about a file before it is uploaded: Autumn owns the size limit and the accepted
  types, and a refusal arrives as a status code, so the notice can only offer
  "it may be too large". The bio and the banner are not on the user record — the
  v0 user model has no `profile` field at all, so no event announces either —
  which makes them a request asked once per session
  and again after each edit made here; one made from another client appears when
  the page is reopened. The banner is not previewed in the row that sets it, and
  a *username* refusal cannot be told apart: Revolt answers a taken name and a
  wrong password alike. **Pronouns** are simply not built: revoltgo models
  `User.Pronouns` and `UserEditParams.Pronouns` now, so this is a row on the
  settings page and a line on the profile card, not a constraint.
- **A second factor is a code, and only a code.** `AuthMFA*` beyond the login
  itself is uncovered: nothing enables or disables 2FA, generates recovery codes
  or lists them (`AuthMFAGenerateTOTPSecret`, `AuthMFARecoveryCodes`), so an
  account can be signed into but not configured. A security key is refused by
  `answerFor` rather than offered — Revolt names the method, and there is no
  WebAuthn here to answer it with.
- **`domain.Message` drops** what nothing renders: role mentions, masquerade
  contents (only *that* one exists survives — it refuses grouping and draws
  `domain.AuthorMasquerade`, so the row says the name is the account's rather
  than the one posted under) and `Interactions`,
  which is the list a message may restrict its reactions to — nothing here refuses
  a pick against it, so the server does. `Mentions` and the one flag bit behind
  `MentionsEveryone` are kept — they are what warms a row, as `Pinned` is what
  marks one and `Reactions` is what hangs beneath it. `FileKind` classifies
  video/audio/archive/PDF but only `FileImage`/`FileText` are branched on.
- `client.Client`'s **actions** have no test — they want an HTTP fake, and
  revoltgo's REST layer takes no injectable transport. What is testable without a
  session is: `events_test.go` covers the pin reconciliation and the reaction
  bookkeeping, both being cache work rather than a request, and `auth_test.go`
  covers the login bodies, which are hand-written against the spec rather than
  taken from revoltgo and so are the one request shape that can be wrong on its
  own.
- **A server's settings are three lists, a role editor and a statement of fact.**
  The cog on the channel sidebar's header opens them, and appears only where the
  account holds `ManageChannel`, `ManageServer`, `ManageRole` or `BanMembers` — a
  page offering none of those would be a reading of the sidebar behind it. Each
  section the account cannot reach is left off the rail rather than drawn empty.
  What limits it is that **nothing announces any of it**. No gateway event carries
  a lifted ban, a revoked invite or an invite created anywhere, so Bans and
  Invites go through `ui.cachedList` — held for the opening, one request at a
  time, expiring after `listTTL` (30s). So the window in which the page can be
  wrong about somebody *else's* revoked invite or lifted ban is that TTL, and it
  closes on the next tap into the section rather than on a timer: a list left on
  screen untouched keeps showing what it last fetched, however long that is.
  **An invite is a code, a channel and a creator** — Revolt stores no expiry, no
  use count and no date, and a code is not a ULID, so a duration is not something
  this client has left out. The creator is fetched where the account is unknown,
  which most are; only one whose account is gone is left unnamed.
  Overview sets the name, the description, the icon and the banner — that last
  one drawn nowhere in this client, an invite card being where it shows — and
  states the rest. There is no member count, since `Store.Members` answers with
  whoever has been resolved rather than with the membership and the number would
  climb as the reader scrolled.
- **A channel outside a category has no order, and there is no route that gives
  it one.** The arrangement is one field of the edit-server route (`categories`:
  id, title, and the channels under it), and `server.channels` — where everything
  no category claims lives, in the order the server happens to list it — is not
  editable by anything. So the Channels section moves a channel within a category
  and across the boundary of one, and the stretch above the first category can
  only be left, never reordered: down from there files a channel at the head of
  the first category, and back up out of one returns it to wherever the server
  already had it. Every move sends the **whole** arrangement, Revolt replacing it
  rather than patching, including the channels this account cannot see — those are
  carried through untouched, since leaving one out files it out of its category.
  Categories are also the one field of that route gated on `ManageChannel` rather
  than `ManageServer`, which is what the section asks for. There is no drag: Fyne
  gives a list no reordering affordance, so a place in the order is two buttons.
- **A role is edited at server scope, in hex, without a picture.** The editor sets
  a role's name, colour, hoist, seniority and all thirty-four permission bits, and
  three things about one are out of reach. Revolt's role **icon** is neither read
  nor set. The **colour** is a hex — Revolt takes any CSS colour, gradients
  included, and the client's own palette and swatch agree on hexes — so a role
  already wearing a gradient keeps it until somebody picks a colour and there is
  no way to type one back; `domain.Role.ColorText` is what the field opens on
  where it can, and what a read-only row states exactly. Nothing counts how many
  members hold a role, for the reason there is no member count. Which members hold
  it is the member menu's question, not this page's.
- **A channel's own overrides are set, and they are a subset of the grid.** The
  Channels section drills into a channel and then into a role, which is the second
  level the page has; what it draws there is the same three-state grid the Roles
  section does, filtered to the bits a channel decides (`ui.channelPermissions`).
  Revolt keeps **one** bitfield for both scopes, so an override of "ban members"
  would be stored and never read — a control that appears to work and does not —
  hence the mask rather than the whole grid. Two consequences worth knowing:
  what a channel changes about a role is not visible from the Roles section, and
  the row summarising a role in a channel says *how many* bits moved and never
  which. A **group's** default permissions are the one route not sent: Revolt takes
  a plain value there rather than an override
  (`revoltgo.GroupPermissionsSetDefault`), and no settings page for a group exists
  to put it on — a group is edited through the channel card, which the Channels
  section's own permissions button is not on.
- **Three permissions decide what of a role can be changed, and the page asks all
  three.** `ManageRole` covers the name, the colour, the hoist, the order and the
  delete; `ManagePermissions` covers the grid; and Revolt refuses to grant a bit
  the actor does not hold themselves, so the grid is drawn per bit — a control
  where all of that holds, and the state in words where it does not. The section
  is listed for either permission. A channel's grid asks the same two questions
  **of the channel** rather than of the server, an override being exactly what
  moves the two apart, and adds the rank check the server's route makes elsewhere:
  a role more senior than the reader's is listed and drawn read-only rather than
  left without a way in, since unlike the Roles section its row cannot say which
  bits a channel moved. What this cannot answer for is a *rank* change
  under an open editor: the page rebuilds from the store on every role event, so a
  role that has just risen above the reader closes its own editor and returns them
  to the list, which is correct and abrupt.
- **What a member menu can do to somebody depends on four permissions and a rank,
  and the client asks all five.** A nickname, a set of roles and a timeout are one
  route (`ServerMemberEdit`) under `ChangeNickname`/`ManageNicknames`,
  `AssignRoles` and `TimeoutMembers`; roles are additionally gated on rank, since
  Revolt refuses one at or above the actor's own. What it does **not** offer is a
  per-server avatar (`ChangeAvatar`/`RemoveAvatars`), which is an upload into a
  bucket with no surface here. A timeout has no server-side maximum, so the six
  spans the menu lists are the client's own; a member's remaining time is not
  drawn anywhere either — the menu offers to end one, which is the only place a
  standing timeout shows at all.

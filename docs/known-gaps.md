# Known gaps

What is not built, and what is limited by revoltgo or Fyne rather than by
effort. Linked from the root `CLAUDE.md`; read it before concluding something
is missing by accident.

Simply not built, no constraint behind it: an attach button (files arrive by drag
or paste), role mentions, a notice history panel,
code-block highlighting, a hue wheel/alpha/eyedropper in the colour picker,
`MessageEmbedSpecial` (YouTube, Spotify, …), creating or renaming a channel
(`ServerChannelCreate` / `ChannelEdit`), listing or revoking the invites this
client can now create (`ServerInvites` / `InviteDelete`), and moderation beyond
the three destructive sidebar items (banning, role edits, nicknames, channel
deletion are one call away but deliberately not offered).

The gateway events still unregistered are the ones nothing here has to do about:
`EventEmojiCreate` / `Delete` (revoltgo's own default handlers file them into
`State`, and the picker reads `State` as it opens, so a handler here would have
nothing left to arrange), the webhook, voice and report events, and
`EventUserSettingsUpdate` (revoltgo flags its msgp tuples as undecodable).
Everything a server, role, member or channel can do is handled.

Where something is limited by revoltgo or Fyne rather than by effort:

- **A voice channel is a text channel here.** It is recognised — its own speaker
  glyph in the sidebar and the header, and a standing note under that header
  saying so — and everything a text channel does works in it, Revolt keeping
  messages in one all the same. What is missing is the call: joining one is a
  WebRTC session against Revolt's media server, so the signalling revoltgo
  already models (`EventVoiceChannelJoin`/`Leave`/`Move`, the channel's voice
  token) is the small half, and the audio devices and the media stack behind it
  are not something Fyne offers any part of. Nobody's voice state is drawn
  either, for the same reason: a list of who is in a call this client cannot
  join is an invitation to a dead end.

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
  it is reopened. The search route also cannot be asked for the users
  (`include_users` changes the response shape past what revoltgo decodes), so the
  authors are a second round of requests before the list can be drawn. It is
  capped at the hundred newest pins, Revolt's own ceiling on a search, with no way
  to page past it, and a row is a flattened one-line summary — a body with no text
  says what it carries instead of quoting nothing.
- **Channel search is that panel with a query and inherits every one of those
  limits**, plus two of its own. It searches the **open channel** only: Revolt's
  route is per channel, so there is no search across a server, let alone across
  the account. And the matching is MongoDB's full-text search rather than a
  substring scan — words, not fragments, with what counts as a word decided by
  the server — so half a word finds nothing and there is no way to ask for a
  phrase. A query is 1–64 characters (a longer one is cut before it is sent), it
  runs on Enter rather than as you type since each one is a request, and results
  come back newest first: `Relevance` is the route's default and is not asked for,
  a list that leads into a channel's history reading better in the order that
  history has.
- **A server is created with a name and nothing else.** Revolt takes no icon and
  no description at creation, so the card is one field; the icon has to be set
  from another client, `ServerEdit` being moderation and deliberately not offered.
  Nothing in the response can be believed either (see the revoltgo note), so the
  new server appears when the gateway says so rather than when the request
  returns.
- **A channel is edited for its name, topic, cooldown, user limit and age gate.**
  Stoat gates the whole edit on one permission (`ManageChannel`), so the card
  offers everything its kind has or does not open — there is no field to grey out.
  Two of the route's fields are left: the icon and a group's owner transfer want a
  file picker and a member picker. `archived` is not a gap — the spec lists it and
  `channel_edit.rs` reads it nowhere, so no client can set it.
  The **cooldown is fetched before the card opens** and left out of the edit when
  that request fails: revoltgo drops the field, so the store's zero would otherwise
  clear a slowmode on save.
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
  uniform body, so a message carrying one cannot be selected.
  A **relative timestamp is resolved once**, when the row mounts, and nothing
  re-reads it: "in 5 minutes" on a message left on screen stays that until a
  scroll past it remounts the body. Nor is one hoverable — the absolute instant
  behind an "R" is not reachable, a body carrying no tooltip.
- **Embeds** render site line, title, description, colour and one picture. A bare
  **video** embed is dropped at the boundary (revoltgo carries only the URL, and
  there is no player); a bare **image** embed has the same missing dimensions, so it
  draws against the placeholder until the picture lands.
- **An invite card** says "server" whatever the code opens: revoltgo carries
  `Invite.Type`, and a *group* invite resolves with no `ServerID`, so it is
  offered as a join — which works — under the wrong noun. It draws no banner
  (`Invite.ServerBanner` is dropped at the boundary, as a profile's is flat for
  the same reason) and does not refresh, so a server joined from another client
  keeps offering Join until the channel is reopened. `NewInviteCardFor` exists
  for a caller holding a resolved invite, but nothing calls it yet — the join
  dialog still validates a pasted code without previewing what it opens.
- **A custom emoji** is a still: `image.Decode` takes the first frame of an
  animated one. It has no name beside it either — nothing tooltips a message body
  — so one whose picture fails to arrive leaves an empty square rather than the
  `:shortcode:` other clients fall back to, and a preview of a body that is only
  emoji (`markdown.PlainText`) is blank. A reaction chip inherits all of it, the
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
  reacted first — the payload is a map, and there is no first to read. **Clearing
  them all is offered but only reflects
  when this client does it:** Revolt announces a clear as a message update
  carrying an empty reaction map, and revoltgo's `EventMessageUpdate.Data` is a
  whole `Message`, so an empty map is also what an ordinary content edit brings —
  the same unreadable field that keeps pin state off that event. One emoji taken
  off wholesale is a different event and does land. `Message.Interactions` (the
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
- **The friends list is as complete as the account cache is.** It is a walk of
  the cached users, so somebody Ready did not name and nothing has since fetched
  is not in it, and nothing announces an incoming request beyond the sidebar row's
  mark — which is only visible in the home view. It does not follow presence, has
  no search and no way to add somebody by handle: adding is offered from a
  profile, which is a route only their message or their member row opens. A
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
- **Every entry leaks fonts, because `ui.WithCaret` has to exist.** A caret is
  drawn `SizeNameInputBorder` wide in `Primary`, and the focus ring takes
  `Primary` too — so the only way to have one without the other is a scoped
  override, which is what `WithCaret` is for. But `cache.OverrideTheme` mints a
  scope from a counter that never repeats, and `ThemeOverride` calls it from its
  constructor, `CreateRenderer` **and** `Refresh` — while `painter.CachedFontFace`
  keys on `{style, scope}` and `loadMeasureFont` has no cache at all. So every
  refresh that reaches an entry re-parses Montserrat, NotoSans, InterSymbols and
  Fyne's 4.2 MB `EmojiOneColor.otf`, and files the result in a map nothing evicts:
  **~6 MB per open/close of a settings number box**, measured on one canvas.
  `painter.ClearFontCache` cannot release it — it never touches the `fontscan`
  map — and none of it is reachable from here, `painter` being internal. Nothing
  above the toolkit fixes this: reusing the entry and its wrapper across edits
  measured identically (6.048 vs 6.060 MB), since it is `Refresh` that mints, not
  construction. It needs a patched Fyne — a cache on `loadMeasureFont` keyed by
  resource, which makes a fresh scope cost a map entry instead of four font
  parses. Emoji cannot be dropped to dodge it either: `-tags no_emoji` builds and
  runs, but emoji then render as **nothing** — go-text does not resolve Windows'
  Segoe UI Emoji through the system fallback, though it does resolve CJK.
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
  "it may be too large". The bio and the banner are not on the user record —
  `PartialUser` carries a `Profile` that revoltgo's own `User.update` ignores, so
  no event announces either — which makes them a request asked once per session
  and again after each edit made here; one made from another client appears when
  the page is reopened. The banner is not previewed in the row that sets it, and
  a *username* refusal cannot be told apart: Revolt answers a taken name and a
  wrong password alike. Revolt's `pronouns` is modelled by neither revoltgo's
  `User` nor its edit params, so it is neither read nor set.
- **A second factor is a code, and only a code.** `AuthMFA*` beyond the login
  itself is uncovered: nothing enables or disables 2FA, generates recovery codes
  or lists them (`AuthMFAGenerateTOTPSecret`, `AuthMFARecoveryCodes`), so an
  account can be signed into but not configured. A security key is refused by
  `answerFor` rather than offered — Revolt names the method, and there is no
  WebAuthn here to answer it with.
- **`domain.Message` drops** what nothing renders: role mentions, masquerade
  contents (only *that* one exists survives, for grouping) and `Interactions`,
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
- `ui.NewInviteCardFor` — the entry point for a caller already holding a resolved
  `domain.Invite` — is built and unreferenced. The join dialog still validates a
  pasted code without previewing what it opens, which is what it is for.
- **`ui.TestAttachmentViewerFits` is intermittently flaky**, roughly one run in
  fifteen. `ui.viewerText` starts a goroutine that fetches the file and calls
  `DoOnUI`, and Fyne's *test* driver runs that work concurrently with the test
  rather than serialising it onto a UI thread — so it races the harness inside
  `go-text`'s unsynchronised font-shaper LRU (`concurrent map writes`) or Fyne's
  own `RichText` row bounds. Nothing is wrong with the widget: the real driver has
  one UI thread and `DoFromGoroutine` queues onto it. Re-run the package.

# internal/client

The **only** package that imports `revoltgo`. It converts wire types into
`internal/domain` values on the way in. See the root `CLAUDE.md` for the
dependency DAG and the client's contract; this file is the wire-level notes.

## revoltgo notes

- `Session.X(...)` = network. `Session.State.X(...)` = local cache, may be nil.
- Attachments/avatars/icons are all `*revoltgo.File`, whose `Metadata` is a
  *pointer*, nil for files the server couldn't introspect — `domain.File` carries
  plain `Width`/`Height`/`Kind` so `client/convert.go` absorbs that nil check
  once. Uploads take `*revoltgo.FileParams`.
- **`State.updateUser` silently drops** an update for an account it has never
  cached, so presence for somebody nobody fetched never arrives. Hence
  `FetchMembers` asking for the whole membership rather than only the
  memberships: the same response carries the users, and putting them in State is
  what makes `EventUserUpdate` mean anything for them. `Session.ServerMembers`
  writes both, gated on revoltgo's `TrackBulkAPICalls` (on by default) — turn
  that off and the call succeeds while recording nothing.
- `ServerRole` carries `Hoist` and `Rank`; `PartialUser` makes every field nilable
  and keeps `Online` separate from `Status`, which is what lets `userUpdateKinds`
  tell a presence change from a rename without diffing against State.
- **Known bug:** `Session.ChannelMessages(..., IncludeUsers: true)` only feeds
  Users/Members into State when the request *failed* (`if err != nil` where
  `err == nil` was meant). Hence the batched `ensureAuthor` path; when fixed, the
  batch simply finds nothing to do.
- **Missing field:** Revolt carries `slowmode` (seconds) on a text channel and in
  `ChannelUpdate`; revoltgo models neither, so the number never arrives with the
  channel and nothing announces a change. `Client.FetchSlowmode` is the one action
  that goes round the typed API — a raw `session.HTTP.Request` for
  `EndpointChannel` — and records the result for `store.Channel` to hand back.
- **`ChannelTypeVoice` is a dead constant.** Stoat dropped the `VoiceChannel`
  variant: a voice channel is a **`TextChannel` carrying a `voice` object**, which
  revoltgo does model (`Channel.Voice`). So `toChannelKind` takes the whole
  channel — reading `channel_type` alone files every voice channel under text,
  and nothing looks wrong, the glyph is simply the other one. The constant is
  kept in the switch for a server old enough to still send it. Note the reverse
  is unreachable: `Voice` is a removable field (`FieldsChannel`) and
  `Channel.clear` handles only Icon and Description, so a channel that *stops*
  being voice keeps its mark until the client restarts.
- **Known bug:** `State.ChannelPermissions`/`ServerPermissions` are unused and
  `client/store.go` does the whole calculation itself, because all three of their
  mistakes land on exactly what it is for. They ignore `Channel.RolePermissions`
  (a channel denied to everyone and handed back to one role — how a private
  channel is actually built — reads as invisible to the role that holds it), they
  apply a member's roles in carry order rather than by rank, and they clamp a
  timed-out member *before* the channel's overwrites, so an overwrite can hand
  back what the timeout took. They also error for a server the account has no
  cached membership of, a state the client is routinely in, and they decide a
  **DM** from `Channel.Permissions` — a field Revolt only sends on a *group*, so
  every DM came back view-only and would have disabled the composer in all of
  them. Revolt decides a DM from the relationship instead, which is
  `User.Relationship` (`client.blocked`). `BypassSlowmode` (`1 << 39`) is missing
  from the permission constants too, hence `domain.Permission` naming every bit
  itself.
- **Known bug:** `MessageFlags` is a bitfield and revoltgo numbers it 1, 2, 3 —
  positions, not bits — so its `MentionsOnline` collides with
  `SuppressNotifications|MentionsEveryone` and can never be read for what it is.
  `client/convert.go` names the two bits it wants itself.
- **`EventMessageUpdate.Data` is a whole `Message`, not a partial one.** Every
  field arrives at its zero value when the update did not mention it, so a
  `bool` there cannot be read at all: `Pinned` false means *either* "now
  unpinned" or "this was a content edit". Nothing therefore derives pin state
  from it — see `applyPinEvent`, which takes the pin/unpin **system message**
  instead, that being the one announcement carrying both the message and which
  of the two happened. The partial-update handler touches only `Content`,
  `Edited` and `Embeds`, each of which has a nil or empty state that reads the
  same as absent. **Reactions are the second casualty:** Revolt announces a
  cleared message as an update carrying an empty map, which is also what every
  edit carries — hence `Client.ClearReactions` writing the cache itself, and a
  clear made elsewhere never landing. `EventMessageRemoveReaction` is a *single*
  emoji taken off wholesale and is unaffected.
- **`Session.ChannelSearch` must not be asked for users.** `include_users` changes
  the response from an array of messages to an object carrying messages, users
  and members — `BulkMessageResponse` is an `anyOf` — and the method decodes into
  `[]*Message` only, so setting it fails on shape. `ChannelMessages` handles both
  and this does not. `Client.PinnedMessages` therefore comes back with author IDs
  and nothing behind them, and the caller resolves what it cannot name.
- **A system message's `id` is not always a user.** `MessageSystem` models one
  `ID` for every kind, and for `message_pinned`/`message_unpinned` it is the
  *message* that moved — so resolving it as an author is a fetch the server can
  only refuse, and a failed author fetch drops its guard, which brought the
  request back on every remount. `domain.SystemMessage.TargetsUser` is the
  guard; `PinnedMessageID` is the other half of the same question. Revolt also
  sends a `by` naming who pinned it, which revoltgo drops.
- **Custom emoji.** `EndpointCustomEmoji` is the *metadata* route
  (`/custom/emoji/{id}`) and nothing drawn needs it: the picture is
  `EndpointAutumnFile("emojis", id, …)`, derivable from the ID alone.
  `store.EmojiURL` therefore asks `State` nothing — `State` only holds the emoji
  of servers the account is in, while a message routinely names one from a server
  it is not, and Autumn serves those all the same. *Picking* one is the opposite
  question and `State` is the whole answer: Ready fills `emojis` and revoltgo
  registers its **own** default handlers for `EmojiCreate`/`Delete` (gated on
  `TrackEmojis`, on by default), so the set stays current with nothing registered
  here. `Session.ServerEmojis` is the one route to leave alone — it decodes into
  a slice it hands straight back and writes nothing to `State`, so calling it
  buys an overlay to maintain, not an answer.
- **Known bug:** `Session.UserMutual` cannot succeed. `/users/{id}/mutual` answers
  with one object and the method decodes into a **slice** of them, so the request
  fails on shape whatever the account; the struct also drops `channels`, the groups
  and conversations both are in. `Client.Mutual` therefore sends its own — the
  fourth thing to go round the typed API, and the only one that is a plain
  mis-declaration rather than a missing field or route.
- **`Session.WS` is nilable and unguarded.** `ChannelBeginTyping`/`ChannelEndTyping`
  are websocket writes rather than requests — no rate limiter, nothing to wait
  for — but they reach `s.WS.WriteMessage` without a check, and `WS` is nil until
  `Open` builds it and stale after `Close`. `Client.BeginTyping`/`EndTyping`
  therefore test it alongside the session. Also note `EventChannelStopTyping`
  *embeds* `EventChannelStartTyping` rather than aliasing it: the fields are
  promoted, but handlers are keyed on the concrete type, so both must be
  registered. `ID` on either is the **channel**.
- **`EventUserRelationship` has no default handler and no way to write one.** It
  is not among the events revoltgo files into `State` on the way past, `State`'s
  caches are unexported, and `PartialUser`-shaped `updateUser` is the only writer
  there is — so a friend added or a block made anywhere reaches `User.Relationship`
  never. `Client.relations` is the overlay that answers instead. Note the `ID` on
  the event is **this** account and the `User` it carries is the other half.
  revoltgo's `FriendAdd` is also mis-named for what the client wants: it is
  `PUT /users/{id}/friend`, which *accepts*, while sending a request is
  `POST /users/friend` and has no method at all — see `Client.AddFriend`.
- **No `context.Context`.** revoltgo's REST layer takes none, so a superseded
  request can't be cancelled — only its result discarded. `Client.fetching`
  (per-channel in-flight dedup → `ErrBusy`) and the epoch counters do that
  instead. Don't thread a `ctx` through to look correct; it would cancel nothing.
- **An MFA login cannot be expressed at all**, which is why `client/auth.go` sends
  Revolt's own shapes rather than revoltgo's: `LoginResponse` carries neither the
  ticket nor `allowed_methods`, so the challenge is invisible; `LoginParams`
  carries no `mfa_ticket`, so it could not be answered; and `MFAResponse` carries
  only a password, so the answer could not be a code. Three gaps on one route.
  Both stages are the *same* endpoint (`EndpointAuthSession("login")`) with
  different bodies, and Revolt reads which factor is being answered off **which
  field** carries the code — so `answerFor` mapping a method to the wrong field
  is a refusal with nothing to say why, hence `auth_test.go` asserting the JSON.
  The request goes through a throwaway `revoltgo.New("")`: the route is
  unauthenticated, and the session that serves the account is built from the
  token afterwards by `Open`.

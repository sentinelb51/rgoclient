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
- `IncludeUsers: true` feeds Users/Members into State (gated on
  `TrackBulkAPICalls`) on both `Session.ChannelMessages` and `Session.ChannelSearch`,
  so a history page, a pin list and a search result all resolve their own authors.
  The batched `ensureAuthor` path covers what no response carries — a webhook,
  somebody departed — and finds nothing to do otherwise.
- `slowmode` (seconds) arrives on `Channel` and in `PartialChannel`, and
  `Channel.clear` handles it, so `store.Channel` reads the field and
  `EventChannelUpdate` announces a change. `ChannelEditParams` carries it and
  `voice` (`max_users`) too, so `Client.EditChannel` is the typed call.
- **A cleared field is a name in `remove`** (`FieldsChannel`: Description, Icon,
  DefaultPermissions, Voice, Slowmode), never a blank: every string in
  `ChannelEditParams` is `omitzero` and a zero is dropped the same way, so `""` is
  read as "leave it alone". Two rules from `channel_edit.rs` that the spec does not
  say: only `TextChannel` and `Group` are editable at all (a DM is
  `InvalidOperation`), and `voice` is what *makes* a channel a voice channel, so it
  must never be sent to one that is not. `archived` the route ignores entirely.
- **A voice channel does not say so in its type.** Stoat dropped the
  `VoiceChannel` variant and revoltgo no longer defines the constant: a voice
  channel is a **`TextChannel` carrying a `voice` object** (`Channel.Voice`). So
  `toChannelKind` takes the whole channel — reading `channel_type` alone files
  every voice channel under text, and nothing looks wrong, the glyph is simply the
  other one — and every text-channel test elsewhere (`store.Permissions`) covers
  voice by saying nothing about it.
- **`State.ChannelPermissions`/`ServerPermissions`/`UserPermissions` agree with
  the backend now** — rewritten against `calculate_*_permissions` in
  `core/permissions/src/impl.rs` — and `client/store.go` still does the whole
  calculation itself for two reasons that are not bugs. Their DM branch resolves
  the relationship through `UserPermissions`, which walks every cached membership
  *and* every group to decide a mutual connection, where `store.Permissions` is
  asked once per message row and once per sidebar channel; and a server channel
  answers 0 for a membership State has not caught up with, which would empty a
  server the account had just joined (`rankRoles` reads a nil member as one
  holding only the default role instead). The arithmetic here tracks that
  reference otherwise — the channel's default overwrite *before* the roles, the
  view-only floor under a group's own permissions, the timeout clamp last, and no
  `ViewChannel` meaning nothing at all — except for the server mute/deafen revoke
  (`ServerMember.CanPublish`/`CanReceive`), which only touches voice bits
  `domain.Permission` does not name. Revolt decides a **DM** from the relationship
  (`User.Relationship`, `client.blocked`), never from `Channel.Permissions`: that
  field is sent on a *group* only.
- **Known bug:** `MessageFlags` is a bitfield and revoltgo numbers it 1, 2, 3 —
  positions, not bits — so its `MentionsOnline` collides with
  `SuppressNotifications|MentionsEveryone` and can never be read for what it is.
  `client/convert.go` names the two bits it wants itself.
- **`EventMessageReact` is the only update worth announcing, and it has to say so
  itself.** By the time the app sees `MessageUpdated` the cache holds the new
  state, so a reaction and a content edit are the same event — hence
  `MessageUpdated.ReactedBy`, filled from the react handler alone. Unreacting and
  `EventMessageRemoveReaction` leave it empty: nothing is worth a sound for a
  reaction going away.
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
- **`Session.ChannelSearch` returns `ChannelMessages`**, normalising both shapes
  the route answers in — `BulkMessageResponse` is an `anyOf`, an array without
  `include_users` and an object with it — so `Client.search` asks for the users and
  reads `page.Messages`. The route takes `pinned` and `query` as **alternatives** —
  Revolt refuses them together — so the two callers share `Client.search` and
  differ in which one they fill; a query is 1–64 characters, past which the request
  is refused rather than cut, hence `maxSearchQuery`.
- `Session.ServerCreate` and `Session.InviteJoin` decode their own responses now
  (`ServerCreateResponse`, `InviteJoin` — server plus default channels either way),
  but `Client.CreateServer` and `Client.JoinInvite` still ignore them and wait for
  `EventServerCreate`, which revoltgo files into `State`: one path into the sidebar
  rather than two, and `App.pendingJoin` already selects what turns up.
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
- **An upload's bucket is not a detail.** `Session.AttachmentUpload` posts to Autumn's
  *attachments* bucket and takes no say in it, while Revolt looks a file up by ID **and**
  bucket at the moment it is used — so an attachment's ID handed to `UserEdit` as an
  avatar is refused as a file that does not exist. `client.uploadFile` is the same request
  with the bucket as an argument (`attachments`, `avatars`, `backgrounds`), and is now the
  only upload path; `AttachmentUpload` is not called.
- **Missing field:** `UserEditParams.Profile` is a `*UserProfile` whose `Background` is a
  `*File` — the shape a profile is *read* in — where `DataUserProfile` takes an attachment
  **ID**. A banner cannot be expressed through the typed API at all, hence
  `Client.editProfile` sending its own body for both halves of the profile. Note too that
  `PartialUser` carries a `Profile` which revoltgo's own `User.update` **ignores**, and
  `EventUserUpdate` for a profile edit therefore writes nothing to `State`: a bio or a
  banner is a request (`Session.UserProfile`) and stays one, which is why the settings page
  re-reads it after every edit rather than recording what it sent. Revolt's `pronouns` is
  modelled by neither `User` nor `UserEditParams`, so it is out of reach entirely.
- **Known bug:** `Session.UserMutual` cannot succeed. `/users/{id}/mutual` answers
  with one object and the method decodes into a **slice** of them, so the request
  fails on shape whatever the account; the struct also drops `channels`, the groups
  and conversations both are in. `Client.Mutual` therefore sends its own — the
  fourth thing to go round the typed API, and the only one that is a plain
  mis-declaration rather than a missing field or route.
- **Missing body:** `Session.ServerMemberBan` sends the ban with **no body**, where
  `ban_create`'s `DataBanCreate` is required — so `reason` (1024 characters) and
  `delete_message_seconds` (up to 7 days) are out of reach through it entirely.
  `Client.BanMember` therefore sends its own. Kicking is unaffected:
  `ServerMemberDelete` takes nothing beyond the two IDs.
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

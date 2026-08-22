# internal/client

The **only** package that imports `revoltgo`. It converts wire types into
`internal/domain` values on the way in. See the root `CLAUDE.md` for the
dependency DAG and the client's contract; this file is the wire-level notes.

## revoltgo notes

- `Session.X(...)` = network. `Session.State.X(...)` = local cache, may be nil.
- Attachments, avatars and icons are all `*revoltgo.File`, whose `Metadata` is a
  *pointer*, nil for files the server couldn't introspect — `domain.File`
  carries plain `Width`/`Height`/`Kind` so `convert.go` absorbs that nil check
  once. Uploads take `*revoltgo.FileParams`.
- **`State.updateUser` silently drops** an update for an account it has never
  cached, so presence for somebody nobody fetched never arrives. Hence
  `FetchMembers` asking for the whole membership rather than only the
  memberships: the same response carries the users, and putting them in State is
  what makes `EventUserUpdate` mean anything for them. `Session.ServerMembers`
  writes both, gated on revoltgo's `TrackBulkAPICalls` (on by default) — turn
  that off and the call succeeds while recording nothing.
- `ServerRole` carries `Hoist` and `Rank`; `PartialUser` makes every field
  nilable and keeps `Online` separate from `Status`, which lets
  `userUpdateKinds` tell a presence change from a rename without diffing against
  State.
- `IncludeUsers: true` feeds Users/Members into State (gated on
  `TrackBulkAPICalls`) on both `Session.ChannelMessages` and
  `Session.ChannelSearch`, so a history page, a pin list and a search result all
  resolve their own authors. The batched `ensureAuthor` path covers what no
  response carries — a webhook, somebody departed — and finds nothing to do
  otherwise.
- `slowmode` (seconds) arrives on `Channel` and in `PartialChannel`, and
  `Channel.clear` handles it, so `store.Channel` reads the field and
  `EventChannelUpdate` announces a change. `ChannelEditParams` carries it and
  `voice` (`max_users`) too, so `Client.EditChannel` is the typed call.
- **A cleared field is a name in `remove`** (`FieldsChannel`: Description, Icon,
  DefaultPermissions, Voice, Slowmode), never a blank: every string in
  `ChannelEditParams` is `omitzero` and a zero is dropped the same way, so `""`
  reads as "leave it alone". Two rules from `channel_edit.rs` the spec does not
  state: only `TextChannel` and `Group` are editable at all (a DM is
  `InvalidOperation`), and `voice` is what *makes* a channel a voice channel, so
  it must never be sent to one that is not. `archived` the route ignores.
- **Every `remove`/`clear` array is a named string type**, one per model
  (`ChannelClearType`, `ServerRoleClearType`, `BotRemoveField`,
  `MessageClearType`, plus `UserRemoveField`, `ServerMemberClearType`,
  `ServerEditParamsRemove`, `WebhookRemoveField`), on the edit params and on the
  gateway event alike — so `State`'s `clear()` switches on the constant rather
  than converting a string, and a `[]string` no longer assigns. A bare literal
  still does, being untyped: name the constant.
- **`ServerMemberClearType` is not a list of nullable fields.** Stoat's
  `FieldsMember` is one vocabulary shared by the database, the edit body and the
  event, so two of its nine names have no member field behind them: `JoinedAt`
  (required on a live member — the variant exists to `$unset` the tombstone a
  timed-out member leaves) and `VoiceChannel`, an edit-only *command* that
  disconnects the member from voice under `MoveMembers`. `Member::update`
  echoes the request's `remove` list into the event's `clear` unfiltered, so
  both arrive on the wire meaning nothing to a cache; revoltgo no-ops both.
  `CanReceive`/`CanPublish` are the other trap: non-nullable server-side, so
  clearing one resets it to **true** — un-deafened, un-muted — not to unset.
- **A voice channel does not say so in its type.** Stoat dropped the
  `VoiceChannel` variant and revoltgo no longer defines the constant: a voice
  channel is a **`TextChannel` carrying a `voice` object** (`Channel.Voice`). So
  `toChannelKind` takes the whole channel — reading `channel_type` alone files
  every voice channel under text, and nothing looks wrong, the glyph is simply
  the other one — and every text-channel test elsewhere (`store.Permissions`)
  covers voice by saying nothing about it.
- **The voice cache is revoltgo's, not this package's.** `State` tracks who is
  in which call by default: `TrackVoice` puts `voice_states` in the open query,
  Ready seeds it, and the default handlers apply join, leave, move and
  `UserVoiceStateUpdate` before ours run. So `registerVoice` only *names* the
  channel that moved and `store.VoiceParticipants` reads `State.VoiceStates`
  back — nothing here mirrors the participants. A move arrives as one event
  naming both ends, never a leave/join pair, and `EventUserMoveVoiceChannel` is
  the same move sent only to the account that was moved.
- **`State.ChannelPermissions`/`ServerPermissions`/`UserPermissions` agree with
  the backend now** — rewritten against `calculate_*_permissions` in
  `core/permissions/src/impl.rs` — and `client/store.go` still does the whole
  calculation itself for two reasons that are not bugs. Their DM branch resolves
  the relationship through `UserPermissions`, which walks every cached
  membership *and* every group to decide a mutual connection, where
  `store.Permissions` is asked once per message row and once per sidebar
  channel; and a server channel answers 0 for a membership State has not caught
  up with, which would empty a server just joined (`rankRoles` reads a nil
  member as one holding only the default role instead). The arithmetic tracks
  that reference otherwise — the channel's default overwrite *before* the roles,
  the view-only floor under a group's own permissions, the timeout clamp last,
  and no `ViewChannel` meaning nothing at all — except for the server
  mute/deafen revoke (`ServerMember.CanPublish`/`CanReceive`), which only
  touches voice bits `domain.Permission` does not name. Revolt decides a **DM**
  from the relationship (`User.Relationship`, `client.blocked`), never from
  `Channel.Permissions`: that field is sent on a *group* only.
- **Known bug:** `MessageFlags` is a bitfield and revoltgo numbers it 1, 2, 3 —
  positions, not bits — so its `MentionsOnline` collides with
  `SuppressNotifications|MentionsEveryone` and can never be read for what it is.
  `convert.go` names the two bits it wants itself.
- **`EventMessageReact` is the only update worth announcing, and it has to say
  so itself.** By the time the app sees `MessageUpdated` the cache holds the new
  state, so a reaction and a content edit are the same event — hence
  `MessageUpdated.ReactedBy`, filled from the react handler alone. Unreacting
  and `EventMessageRemoveReaction` leave it empty: nothing is worth a sound for
  a reaction going away.
- **`EventMessageUpdate.Data` is a `PartialMessage`**, every field nilable, plus
  the `Clear` array that names what the update *removed*. Four routes write
  through it and no two of them at once, so the handler's arms are independent:
  an edit sends `content`, `edited` and `embeds` together; a pin sends `pinned`
  alone; an **unpin sends an empty partial and names `Pinned` in `clear`**, there
  being no field that carries false; a bulk reaction clear sends an empty map and
  nothing else. Reading `Data` alone therefore misses every unpin — hence
  `pinnedAfter`, which reads the clear array first. An empty reaction map is now
  distinguishable from an edit that never mentioned reactions, so a clear made
  elsewhere lands. `EventMessageRemoveReaction` is a *single* emoji taken off
  wholesale and is a different event.
- **Pin state comes off the update, not the system line.** Revolt announces a pin
  twice — the partial above, and a `message_pinned` system message in the
  channel. The line is a message like any other and the client renders it as one;
  deriving cache state from it as well would be deriving state from a rendering,
  and the two paths would race for the same write.
- **The arms of that handler split on whether a field can echo a local write.**
  `Content`/`Edited`/`Embeds` only arrive on somebody's edit and are taken as
  sent. `Pinned` and `Reactions` echo `PinMessage` and `ClearReactions`, which
  write the cache the moment the server agrees — so they report a change only
  when there is one, or one tap repaints twice.
- **`Session.ChannelSearch` returns `ChannelMessages`**, normalising both shapes
  the route answers in — `BulkMessageResponse` is an `anyOf`, an array without
  `include_users` and an object with it — so `Client.search` asks for the users
  and reads `page.Messages`. The route takes `pinned` and `query` as
  **alternatives** (Revolt refuses them together), so the two callers share
  `Client.search` and differ in which one they fill; a query is 1–64 characters,
  past which the request is refused rather than cut, hence `maxSearchQuery`.
- `Session.ServerCreate` and `Session.InviteJoin` decode their own responses now
  (`ServerCreateResponse`, `InviteJoin` — server plus default channels either
  way), but `Client.CreateServer` and `Client.JoinInvite` still ignore them and
  wait for `EventServerCreate`, which revoltgo files into `State`: one path into
  the sidebar rather than two, and `App.pendingJoin` already selects what turns
  up.
- **Two invite shapes, and they share no field name but `type`.**
  `Session.ServerInvites` and `ChannelInviteCreate` answer `InviteRecord` — the
  stored document, `_id` being the code, plus `server`, `creator`, `channel` —
  while `Session.Invite` answers `Invite`, the preview a non-member sees
  (`server_id`, `channel_name`, `user_name`, a banner, a member count). Reaching
  for the wrong one decodes to empty structs and reports no error. There is no
  expiry, no use count and no creation time on a stored invite, so
  `domain.ServerInvite` is the whole of one.
- **A system message's `id` is not always a user.** `MessageSystem` models one
  `ID` for every kind, and for `message_pinned`/`message_unpinned` it is the
  *message* that moved — so resolving it as an author is a fetch the server can
  only refuse, and a failed author fetch drops its guard, which brought the
  request back on every remount. `domain.SystemMessage.TargetsUser` is the guard.
  Revolt also sends a `by` naming who pinned it, which revoltgo drops.
- **`EventReady.ChannelUnreads` is read state, not unread channels.** Each row
  is a marker the account owns for a channel it has *acknowledged*, carrying the
  last message read (`last_id`, null for a row with only mentions) and the
  `mentions` still standing past it. Reading the rows as the unread set inverts
  the feature — acking a channel is what creates its row — so `readState`
  compares each channel's `last_message_id` against its marker, absent meaning
  never read, and a standing mention is unread on its own account.
  **A marker outlives its channel**: leaving a server deletes neither it nor the
  mentions on it, and only an ack ever prunes one, so `readState` builds *both*
  lists by walking `event.Channels` and looking the marker up — a mention read
  straight off the rows lights the inbox for a channel that can never be opened,
  resolved or acknowledged again.
- **An ack prunes mentions, it does not clear them.** `PUT /channels/{id}/ack/{m}`
  is `$pull: {mentions: {$lte: m}}` + `$set: {last_id: m}` in `delta`, so a
  mention past the message acknowledged survives — hence `ChannelRead.MessageID`,
  off `EventChannelAck`, and `App.pruneMentions` rather than a delete. The
  **server** ack (`PUT /servers/{id}/ack`) replaces the documents outright and so
  clears them. Only users named in `mentions` are stored: `@everyone` is a
  message *flag* and puts nobody in the array, which is why `App.recordMention`
  reads `Message.Mentions` rather than `MentionsUser` — see the known gap.
- **Custom emoji.** `EndpointCustomEmoji` is the *metadata* route
  (`/custom/emoji/{id}`) and nothing drawn needs it: the picture is
  `EndpointAutumnFile("emojis", id, …)`, derivable from the ID alone.
  `store.EmojiURL` therefore asks `State` nothing (`store.EmojiName` is the
  opposite: a *name* is held nowhere but `State`, so it answers only for the
  servers the account is in) — `State` only holds the emoji
  of servers the account is in, while a message routinely names one from a
  server it is not, and Autumn serves those all the same. *Picking* one is the
  opposite question and `State` is the whole answer: Ready fills `emojis` and
  revoltgo registers its **own** default handlers for `EmojiCreate`/`Delete`
  (gated on `TrackEmojis`, on by default), so the set stays current with nothing
  registered here. `Session.ServerEmojis` is the one route to leave alone: it
  decodes into a slice it hands straight back and writes nothing to `State`, so
  calling it buys an overlay to maintain, not an answer.
- **An upload's bucket is not a detail.** Revolt looks a file up by ID **and**
  tag at the moment it is used, so an attachment's ID handed to `UserEdit` as an
  avatar is refused as a file that does not exist. revoltgo now names the whole
  set — `revoltgo.FileTag*`, six of them, derived in `revoltgo/file.go` from
  Autumn's own config (`crates/core/config/Revolt.toml`) and from
  `core/database/src/models/files/model.rs`, which is what pairs a tag with a
  field: `use_server_icon` wants `icons`, `use_server_banner` `banners`,
  `use_background` `backgrounds` (a *profile's* banner, not a server's).
  `Session.Upload(tag, file)` is the request; `UploadAttachment` is now a
  wrapper on it. **Nothing here hand-rolls an upload any more** —
  `client.uploadFile` opens the path, calls `FileUpload` and closes it, and every
  picture the client sends goes through that one function with the tag named at
  the call site. A tag revoltgo has no constant for is not refused anywhere: it
  is logged once per distinct value (`FileTag.check`), which is how a bucket
  added to the backend would surface. Autumn is the CDN; `delta` is the REST API
  and `january` the link proxy, neither of which serves an upload.
  `ServerEditParams` takes `Icon` and `Banner` as IDs and
  `ServerEditParamsRemove` names them for a removal, the same partial-edit shape
  the description already uses.
- **A profile is a request, and stays one.** `UserEditParams.Profile` is a
  `*UserProfileParams` now — `Content` a `*string`, `Background` the attachment
  ID an upload answered with — so `Client.editProfile` is the typed call. What
  has not changed is that **no event announces a profile**: the v0 user model
  carries no `profile` field at all, so there is nothing for `EventUserUpdate` to
  bring and nothing in `State` to hold it. A bio or a banner is
  `Session.UserProfile` and nothing else, which is why the settings page re-reads
  it after every edit rather than recording what it sent.
- **Typed calls that cannot work.** Two plain mis-declarations and one missing
  body; each `Client` method sends its own request instead. (The rest of what
  goes round the typed API is a missing route, `AddFriend`.)
  - `Session.UserMutual` decodes `/users/{id}/mutual`'s single object into a
    **slice**, so it fails on shape whatever the account; the struct also drops
    `channels`, the groups and conversations both are in. → `Client.Mutual`.
  - `Session.ServerBans` makes the same mistake: a **slice** where
    `/servers/{id}/bans` answers one `{users, bans}` object. → `Client.ServerBans`.
    Both halves inside are right — a `BannedUser` is `_id`, `username`,
    `discriminator` and `avatar`, four fields of revoltgo's `User`, so it decodes
    into one and keeps the default-avatar fallback every other row gets, and a
    `ServerBan` is the reason plus a composite `{user, server}` ID. The two are
    joined on that ID here, a ban naming somebody `users` left out still being a
    ban.
  - `Session.ServerMemberBan` sends the ban with **no body**, where
    `ban_create`'s `DataBanCreate` is required — so `reason` (1024 characters)
    and `delete_message_seconds` (up to 7 days) are out of reach through it
    entirely. → `Client.BanMember`. Kicking is unaffected:
    `ServerMemberDelete` takes nothing beyond the two IDs.
- **Four role and permission routes were mis-declared and were fixed in revoltgo**, none of
  them having been called before the role editor. Worth knowing because the shapes are not
  guessable and the failures differ:
  - `PermissionsSet`, `ChannelPermissionsSet` and `ChannelPermissionsSetDefault` sent
    `PermissionOverwrite` — the shape an overwrite is *read* in, `{a, d}` — where all three
    routes take `{"permissions": {"allow", "deny"}}`. `revoltgo.Override` is that write
    shape now and the three wrap it; a group's channel default takes a plain value instead
    and is `GroupPermissionsSetDefault`.
  - `ServersRoleRanksEdit` sent a bare array where the route takes `{"ranks": [...]}`.
  - `ServersRoleCreate` decoded `{id, role}` into a `ServerRole`, so every field came back
    zero and the ID a creation answers with was lost — it now fills `ServerRole.ID`, which
    is otherwise never populated, the map key being the ID.
  Beside those, `ServerRoleEditParams.Rank` and `ServerRoleCreateParams.Rank` are dead:
  `roles_edit.rs` drops rank from the partial and `DataCreateRole.rank` is documented as
  having no effect. Ordering is `ServersRoleRanksEdit` and nothing else, and the array is
  the server's whole order — rank is the index in it (`Server::set_role_ordering`).
- **A server's channel arrangement is one field, replaced whole.**
  `ServerEditParams.Categories` (`[]*ServerCategory`: `id`, `title`, `channels`)
  is the *only* way to order channels — `server.channels` takes no route, so the
  channels no category claims have no order at all and are read back in whatever
  order the server lists them. `server_edit.rs` gates this one field on
  **`ManageChannel`** where the rest of the route is `ManageServer`, refuses the
  edit outright if a channel appears in two categories, and silently drops any ID
  it does not find in the server — so what is sent has to be the server's whole
  arrangement, hidden channels included, rather than the part a reader can see.
  An ID is the caller's to mint (unique, ≤32 characters; `SetServerCategories`
  makes a ULID for a category with none), `channels` is required so an empty
  category sends `[]` rather than a nil that `omitzero` would drop, and clearing
  every category is `ServerEditDataRemoveCategories`, not an empty array. The
  edit returns as a `ServerUpdate` carrying the whole structure, which
  `State.updateServer` files — `PartialServer.Categories` is handled.
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
  there is — so a friend added or a block made anywhere reaches
  `User.Relationship` never. `Client.relations` is the overlay that answers
  instead. The `ID` on the event is **this** account and the `User` it carries is
  the other half. revoltgo's `FriendAdd` is also mis-named for what the client
  wants: it is `PUT /users/{id}/friend`, which *accepts*, while sending a request
  is `POST /users/friend` and has no method at all — see `Client.AddFriend`.
- **An error carries no status.** Every non-2xx comes back as
  `fmt.Errorf("bad status code %d: %s")` — no type, no code — so "it is not
  there" and "the request failed" are one answer. `answeredGone` reads the number
  back out of the text for the one caller that must tell them apart
  (`ResolveMessages` reports `gone` so the mention inbox can forget a deleted
  message without a dropped connection erasing a live one). A wording revoltgo
  stops using reads as no status, which forgets nothing — the safe way round.
- **No `context.Context`.** revoltgo's REST layer takes none, so a superseded
  request can't be cancelled — only its result discarded. `Client.fetching`
  (per-channel in-flight dedup → `ErrBusy`) and the epoch counters do that
  instead. Don't thread a `ctx` through to look correct; it would cancel nothing.
- **An MFA login cannot be expressed at all**, which is why `auth.go` sends
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

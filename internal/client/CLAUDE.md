# internal/client

The **only** package that imports `revoltgo`. It converts wire types into
`internal/domain` values on the way in. See the root `CLAUDE.md` for the
dependency DAG and the client's contract; this file is the wire-level notes.

## revoltgo notes

- `Session.X(...)` = network. `Session.State.X(...)` = local cache, may be nil.
- **A cached object is immutable once published.** revoltgo's `State` is
  copy-on-write: a mutator clones the object, updates the clone and swaps it in
  under the write lock. So a pointer read out of `State` can go *stale* but can
  never change under its reader — which is why `store.go` and `convert.go` hold
  and dereference those pointers without copying first. Deliberate, not
  accidental; anything resolving *twice* should still re-read rather than hold.
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
- **`ServerMemberEditParams` carries the three voice fields**, `CanPublish`,
  `CanReceive` and `VoiceChannel`, so the four voice moderation actions are
  ordinary `editMember` calls. Un-muting is `CanPublish: &true` and **not** a
  remove, for the reason above — the one pair in that file whose negative case is
  not a `Remove`.
- **`join_call` is the whole of joining, and there is no leave.** `POST
  /channels/{id}/join_call` answers with a voice node URL and a token minted
  against this session (`Client.JoinCall`). Its `force_disconnect` is not
  optional in practice: Stoat refuses a second connection for one account, so a
  client that crashed mid-call cannot rejoin without it. Nothing hangs up —
  neither revoltgo nor Stoat has a route — leaving *is* disconnecting from the
  voice server, after which the gateway announces it as a voice-state leave.
- **`node` is required, and an empty one is not "you pick".** `voice_join.rs`
  answers a blank node with **400 `UnknownNode`**, so a client that does not name
  one cannot join at all — verified against the live instance, not read off the
  spec, where `node` looks optional. The list is the instance config's `livekit`
  block — `revoltgo.InstanceConfigFeatures.LiveKit`, read through
  `Session.Instance`. `Client.pickVoiceNode` caches the answer for the session,
  the node list being instance configuration. With more than one node the choice
  is *measured*: `nearestVoiceNode` dials them all and takes the first handshake
  to complete, the coordinates each node carries being useless without the
  reader's own. stoat.chat publishes exactly one, `hel1`, which is taken without
  a probe.
- **The LiveKit token's `sub` is the Revolt user ID**, and its `video.room` is
  the channel ID — one room per voice channel. Everything per-person in the
  client is keyed on the first of those, so `voice.Call` logs a line if the room
  ever disagrees; it is a check rather than a fix, but the symptom otherwise is
  audio filed under the wrong name, which is invisible from this end.
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
- **A voice state says nothing about mute or deafen.** `UserVoiceState` carries
  `is_publishing` / `is_receiving`, and neither means what the names suggest:
  `voice/mod.rs` sets `is_publishing` from LiveKit's track-published webhook —
  a self-mute leaves the track published — and writes `is_receiving` exactly
  once, `true`, when the state is created. So a **hold** is read off the
  *membership* instead (`ServerMember.CanPublish` / `CanReceive`, nil meaning
  allowed), which `toMember` converts and `VoiceParticipants` copies onto each
  participant. It follows that a hold is known for anybody whose membership is
  cached, in a call or not, and that a call in a **group** conversation can carry
  none — there is no membership there to file one on.
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
- **A bulk delete is not a loop of deletes, and its rules are the route's.**
  `DELETE /channels/{id}/messages/bulk` (`ChannelMessageDeleteBulk`, whose
  `ChannelMessageBulkDeleteParams` is `{ids}`) differs from the single delete on
  three counts, all read off `message_bulk_delete.rs` rather than the spec:
  - **`ManageMessages` always**, even over the account's own words, where a single
    delete takes authorship instead. So it is a moderator's action wherever it is
    offered.
  - **Every ID must be under a week old**, checked by parsing the ULID *before*
    the permission is looked at, and one that is not — or one that will not parse
    — refuses the **whole batch** with `InvalidOperation`. Nothing partial: a
    ninety-ninth message a week and a minute past costs the other ninety-eight.
    Hence `domain.MaxBulkDeleteAge`, applied by the caller (this package has no
    ULID reader, see the DAG) and re-applied at submit, a selection being
    something a reader can sit on.
  - **1–100 ids**, validated server-side, so `Client.DeleteMessages` chunks by
    `domain.MaxBulkDelete`.

  The answer is a bare **204 whatever matched**. `Message::bulk_delete` filters
  the IDs to the ones actually in that channel and drops the rest in silence, then
  publishes `BulkMessageDelete` carrying only what went — so the *event* is the
  truth and nothing is written to the cache here, as with a single delete. The
  route also takes an `X-Audit-Log-Reason` header, which revoltgo cannot send (no
  per-request headers, see the MFA note) and which only writes an audit entry for
  a channel in a server.
- **A group's default permissions are a different request from a channel's.**
  `PUT /channels/{id}/permissions/default` branches on the channel: a `Group` takes
  `{"permissions": <u64>}` and a `TextChannel` takes `{"permissions": {allow, deny}}`,
  and the wrong shape is `InvalidOperation` with nothing else said. Hence
  `GroupPermissionsSetDefault` (`PermissionsSetDefaultParams`) beside
  `ChannelPermissionsSetDefault` (`Override`), and `Client.SetGroupPermissions`
  beside `SetChannelDefaultPermissions`. Three more things off
  `permissions_set_default.rs`:
  - It is gated on **`ManagePermissions` in the channel**, which for a group's
    owner is everything (`do_we_own_the_channel` short-circuits to `GrantAllSafe`).
  - The group arm makes **no** `throw_permission_override` check, unlike the
    server-channel arm and unlike `permissions_set.rs` — so somebody who may manage
    a group's permissions can grant bits they do not hold themselves. The client's
    grid is gated to match rather than being stricter than the route.
  - The value is written to `Channel.permissions` and resolves as
    `DEFAULT_PERMISSION_VIEW_ONLY | permissions`, so ViewChannel and
    ReadMessageHistory cannot be denied and a nil field means the DM preset rather
    than zero (`client/store.go`'s `conversationPermissions`, and the same
    resolution again in `domain.Channel.Permissions`).
- **A group's icon is `ChannelEdit` with the `icons` bucket.** `ChannelEditParams.Icon`
  takes an Autumn ID and `ChannelClearIcon` is how one comes off, every string on
  those params being `omitzero` so a blank reads as "leave it alone". Same bucket a
  server's icon uses — a bucket is half of what identifies a file, and an icon is an
  icon. `DataEditChannel` also carries an `owner` field (transferring a group), which
  nothing here sends.
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
  **`MessageUpdated.Pinning` is the one thing on that event read off the wire
  rather than off the cache.** `reviseMessage` finds nothing for a message the
  cache never had, and a *pinned* message routinely is one — a pin reaches as far
  back as anybody cared to keep something — so an unpin made elsewhere announced
  nothing at all. It says a pin moved and never which way: an unpin carries no
  field, only `Pinned` in `clear`, so the direction is knowable only for a message
  the cache holds. The pins panel re-asks on it rather than reading it.
- **`Session.ChannelSearch` returns `ChannelMessages`**, normalising both shapes
  the route answers in — `BulkMessageResponse` is an `anyOf`, an array without
  `include_users` and an object with it — so `Client.search` asks for the users
  and reads `page.Messages`. The route takes `pinned` and `query` as
  **alternatives** (Revolt refuses them together), so the two callers share
  `Client.search` and differ in which one they fill; a query is 1–64 characters,
  past which the request is refused rather than cut, hence `maxSearchQuery`.
  `ChannelSearchParams` embeds `ChannelMessagesParams`, so the route also takes
  **`before`/`after`** — the only narrowing on it beyond the query, and the only
  way past its hundred-result cap. Both are message IDs rather than times, hence
  `boundaryID`: a ULID's leading 48 bits are its millisecond, so one built with no
  entropy sorts below everything minted in that same millisecond and is an
  inclusive floor for `after` and an exclusive ceiling for `before`. Whether
  `delta` honours them on `/search` as it does on `/messages` is **not verified
  against the backend** — `app.withinSpan` re-checks the window locally for that
  reason.
  **Those two fields carry the paging as well**, which is why `pageFrom` and not
  the caller decides them: a cursor and a date span both want the same field, so
  the tighter of the two is sent and the reader's window is never widened by a
  page. Which end moves is the *order's* — a `Latest` answer walks back through
  `before`, an `Oldest` one forward through `after` — and `Relevance` moves
  neither: the route re-ranks whatever window it is given, so narrowing one is not
  the page after it. On the unverified honouring above, `app.appendUnseen` drops a
  page that repeats what is held and a wholly repeated page stops the paging,
  which is what a build ignoring the fields would look like from here.
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
- **Three typed calls that could not work, since fixed upstream.**
  `Session.UserMutual` and `Session.ServerBans` decoded a single `{…}` response
  into a **slice**, and `Session.ServerMemberBan` sent the ban with no body, so
  `reason` and `delete_message_seconds` never reached the server. All three are
  corrected in the pinned revoltgo and `Client.Mutual` / `Client.ServerBans` /
  `Client.BanMember` go through them now — nothing here is sent by hand any
  more. Worth keeping because the response shapes are not guessable:
  `/users/{id}/mutual` is one `{users, servers, channels}` object, and
  `/servers/{id}/bans` one `{users, bans}` object whose halves are joined on the
  ban's composite `{user, server}` ID here, a ban naming somebody `users` left
  out still being a ban.
- **Four role and permission routes were mis-declared and were fixed in revoltgo**, none of
  them having been called before the role editor. Worth knowing because the shapes are not
  guessable and the failures differ:
  - `PermissionsSet`, `ChannelPermissionsSet` and `ChannelPermissionsSetDefault` sent
    `PermissionOverwrite` — the shape an overwrite is *read* in, `{a, d}` — where all three
    routes take `{"permissions": {"allow", "deny"}}`. `revoltgo.Override` is that write
    shape now and the three wrap it; a group's channel default takes a plain value instead
    and is `GroupPermissionsSetDefault`. Both channel routes document "Channel must be a
    `TextChannel`", which reads as excluding voice and does not: Stoat dropped the
    `VoiceChannel` variant, so a voice channel *is* a text channel carrying a `voice`
    object (see `toChannelKind`) and both take one. What the arm excludes is a DM and
    saved notes.
  - `ServersRoleRanksEdit` sent a bare array where the route takes `{"ranks": [...]}`.
  - `ServersRoleCreate` decoded `{id, role}` into a `ServerRole`, so every field came back
    zero and the ID a creation answers with was lost — it now fills `ServerRole.ID`, which
    is otherwise never populated, the map key being the ID.
  Beside those, `ServerRoleEditParams.Rank` and `ServerRoleCreateParams.Rank` are dead:
  `roles_edit.rs` drops rank from the partial and `DataCreateRole.rank` is documented as
  having no effect. Ordering is `ServersRoleRanksEdit` and nothing else, and the array is
  the server's whole order — rank is the index in it (`Server::set_role_ordering`).
- **`GroupCreate` answers with a shape of its own and writes nothing.**
  `POST /channels/create` returns a whole `Channel` of the Group variant, and
  revoltgo decodes it into `revoltgo.Group` — `{_id, owner, name, description,
  users}`, so the icon, the permissions and the age gate are dropped — and files
  none of it in `State`. Every channel-keyed path here looks a channel up there,
  so `Client.CreateGroup` asks for it once afterwards, exactly as
  `OpenConversation` does with what `DirectMessageCreate` hands back. The
  `ChannelCreate` the gateway sends does file it, but a caller that wants to
  select the new group cannot wait for it.
  Two more numbers the route carries and revoltgo does not: `users` is capped at
  **49** (fifty counting the account making it), and every ID in it must be a
  **friend** — a stranger among them is refused, and the whole request with them.
  Adding to a group that exists has no such documented cap; the ceiling there is
  a runtime limit of the instance's own, so it is asked for and refused rather
  than guessed at. `GroupMemberAdd`/`Delete` name **one** recipient each
  (`PUT`/`DELETE /channels/{group}/recipients/{member}`), so adding several is a
  request apiece; removing is the **owner's** alone and is not a permission bit,
  so no grant carries it and `domain.Channel.OwnerID` is what answers instead.
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
  is `POST /users/friend` and has no method at all — see `Client.AddFriend`. That
  route names somebody by **handle**, not by ID, and matches on the name *and*
  the discriminator with no guess at either: `{"username": "name#0000"}`. So the
  same request serves two callers — `AddFriend` reads the handle out of `State`
  for a profile button, `AddFriendByHandle` takes one somebody typed — and the
  answer to the second names an account nothing has cached, which is why the
  friends list waits for the gateway to file it rather than drawing it at once.
- **The whole relationship graph is on the account's own record, and only there.**
  `User.Relationship` is filled per account, so it answers only for the people a
  `Ready` happened to send; `User.Relations` — the `relations` array, which Revolt
  puts on the authenticated account's record and on no other — is the complete
  statement. `Client.recordRelations` files it into the same `Client.relations`
  overlay at `Ready` and answers with the IDs, so a relationship survives `State`
  never caching the account at all. `selfIn` finds that record by ID where `State`
  is already built and by the presence of the array otherwise, revoltgo filing the
  snapshot from a handler of its own whose ordering against this one is not
  something to depend on.
- **`MessageInteractions` is two independent things.** `Reactions` is a list of
  emoji and `RestrictReactions` says whether it is a *restriction* or a set of
  suggested quick picks. `toInteractions` keeps only the restricting case — this
  client draws no quick-pick row — and keeps it even when the list is empty, an
  empty restriction being what forbids every reaction.
- **A colour is any CSS value, and role presets use most of them.** `parseColor`
  reads a hex run of 3/4/6/8 digits, `rgb()`/`rgba()`, `hsl()`/`hsla()` in the
  comma form and the space-and-slash one, and the 148 CSS keywords; a gradient is
  every stop it finds, in order. The scanner steps *into* a function it does not
  know rather than over it, which is how `linear-gradient(...)`'s stops are
  reached. `transparent` and `currentcolor` are left out of the keyword table on
  purpose: falling back to the default text colour beats an invisible name.
- **An MFA ticket is a header, and revoltgo has no per-request headers.** Every
  route that changes how an account is *reached* is guarded by one:
  `x-mfa-ticket`, minted by `PUT /auth/mfa/ticket` from a password, a TOTP code or
  a recovery code, and good for a few minutes. Which routes take one is **not**
  guessable and is not symmetric — read it off the spec's own `security` lists,
  which are per-route and precise (`PUT /auth/mfa/ticket` is guarded by an
  *Unvalidated* ticket, everything else by a *Valid* one):
  - **Gated:** revoke a session, revoke all, change password, change email,
    disable TOTP, generate a TOTP secret, both recovery-code routes, disable and
    delete the account.
  - **Not:** read the account, list sessions, *rename* a session, MFA status,
    MFA methods, logout — and **enabling** TOTP, whose answer rides in the body
    because the only proof worth taking there is a code from the authenticator.

  `HTTPClient` has `SetHeader`/`RemoveHeader` and nothing per call, so
  `Client.withTicket` sets it for the length of one request and takes it off
  again, serialised on `ticketMu`. The header map has its own lock, so that is
  safe; what it cannot promise is that no unrelated request overlaps and carries
  the header too — harmless, since a route that does not declare the guard never
  reads it, and it is this account's own ticket either way.
  `LogoutEverywhere` was **sending no ticket at all** before this and is one of
  the gated ones, so it was almost certainly being refused; it now goes through
  `withTicketOn`, against the session it has already dropped.
- **A session's own ID is answered by the login route and nothing else.** There is
  no "which of these am I": `GET /auth/session/all` is `{_id, name}` per row with
  no current marker, and the friendly name is whatever a client called itself, so
  two logins from one machine share it. `ResponseLogin` carries `_id`, which is
  the only time it is ever said — hence `Login.SessionID`, `Client.OpenAs`, and
  `SavedSession.SessionID` beside the token. Without it this client cannot mark
  its own row in the session list, and `EventAuth` cannot tell this session being
  revoked from anybody else's; both say so rather than guessing.
- **An error carries its answer.** Every non-2xx comes back as
  `*revoltgo.APIError` — status, method, URL and the body — reached through
  `apiError` (`errors.As`), which is what `statusOf` and `answeredGone` are built
  on; nothing here parses the error's sentence any more. The body is what makes a
  *typed* refusal readable: `SlowmodeRetry` decodes
  `{"type":"InSlowmode","retry_after":n}` for the send path. An error that is not
  an answer at all — a dial, a timeout, a reset — reports no status, which is what
  tells a network failure from a refusal (`Transient`).
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

- **The GIF service is not the API, and revoltgo had to learn the host.**
  `gifs.go` calls `Session.GIFSearch` / `GIFTrending` / `GIFCategories`, which are
  the fork's own (added for this client): `HTTPClient.ResolveURL` allows the API
  base and the CDN and rejects everything else, so an absolute `api.gifbox.me`
  URL was refused until `parsedGifboxBase` joined that list. It is an allowlist of
  hosts *the session token is sent to* — the service authenticates with it rather
  than with a key of its own, which is the whole reason nothing here ships one.
  Rate-limit buckets key on the path alone (scheme and host are dropped), so
  `/search`, `/trending` and `/categories` would share a bucket with API routes of
  those names; none exists today.

## `features.limits` is dropped at parse

`revoltgo.InstanceConfig` models `features` — the captcha, autumn, january and
the LiveKit nodes — and **not `features.limits`**, so nothing typed reaches the
publish limits a screenshare has to fit under. `Client.VideoLimits` asks
`GET /` again by hand (`session.HTTP.Request` against `revoltgo.BaseURL()`,
the `requestFriend` shape) and reads the slice it needs.

Two things about the answer are easy to get wrong. `video_resolution` is a
**pair whose product is an area cap**, not a box — the ingress compares
`width * height` against `res[0] * res[1]`, so a 1920×405 track passes a
`[1280, 720]` limit. And a tier's guards are *conditional*: the area is
enforced only where both halves are non-zero, the aspect only where the two
bounds differ, which is what `toVideoLimits` folds into zeroes meaning
unenforced.

## `avatar` on a member edit is two permissions, not one

`ServerMemberEditParams.Avatar` and `ServerMemberClearAvatar` are one route, but
`member_edit.rs` reads them as two different requests. Setting one is
`ChangeAvatar` **and self only** — an `avatar` sent for anybody else's membership
is refused as `InvalidOperation`, whatever the caller holds. Removing one is
`RemoveAvatars`, and that is the only thing the permission covers. So the menu
offers a moderator a removal and never a change (`app.memberAvatarItems`), which
is exactly the pair the route accepts.

Which of the two pictures a membership is drawn with is not answerable from
`domain.Member.AvatarURL`: `toMember` falls back to the account's where the
membership has none, so the two are indistinguishable by then.
`domain.Member.ServerAvatar` is set alongside and is what says there is one to
take off.

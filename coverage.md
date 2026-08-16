# revoltgo `Session` coverage map

## Context

`internal/client/actions.go` is the client's whole action surface — every network call the
user can cause. revoltgo's `Session` (module cache:
`revoltgo@v0.0.0-20260810192541-889490ef5cb5/session.go`) exposes ~110 REST methods plus the
socket pair. The client calls **39** of them, and registers **31** of revoltgo's 49 gateway
event types. Six routes are reached without revoltgo's types at all, because revoltgo
cannot express them — see *Round the typed API* below.

`ChannelMessages` counts once but is now asked four ways: the newest page, `Before` into
the cache, and `Nearby` / `After` / an uncached `Before` for a jump window, which is why
`ChannelMessagesParams` no longer has an unused field. `ChannelSearch` counts once and is
asked two ways, `pinned` and `query`, which Revolt takes as alternatives rather than
together.

This is a map, kept so targets can be picked from it. Anything moved out of *Not covered*
is built and working, not merely planned.

---

## Covered today (39 calls)

| Area | revoltgo call | Client action |
|---|---|---|
| Lifecycle | `Open`, `Close` | `Client.start` / `Close` |
| | `Logout` | `Client.Logout` |
| | `SessionsDeleteAll(true)` | `Client.LogoutEverywhere` |
| This account | `UserEdit` (`Status`) | `Client.SetPresence`, `Client.SetStatusText` |
| | `UserEdit` (`DisplayName`) | `Client.SetDisplayName` |
| | `UserEdit` (`Avatar`) | `Client.SetAvatar`, `Client.RemoveAvatar` |
| | `SetUsername` | `Client.SetUsername` (the one edit that re-authenticates) |
| Messages | `ChannelMessageSend` | `SendMessage` |
| | `ChannelMessageEdit` | `EditMessage` |
| | `ChannelMessageDelete` | `DeleteMessage` |
| | `ChannelMessagePin` / `ChannelMessageUnpin` | `PinMessage` |
| | `ChannelMessageReactionCreate` / `…Delete` | `React` |
| | `ChannelMessageReactionClear` | `ClearReactions` (a moderator's, confirmed) |
| | `ChannelMessages` (latest + `Before`) | `LatestMessages`, `HistoryBefore` |
| | `ChannelMessages` (`Nearby`, `After`, uncached `Before`) | `MessagesAround`, `MessagesAfter`, `MessagesBefore` (jump windows) |
| | `ChannelMessage` | `ResolveMessages` (reply targets) |
| | `ChannelSearch` (`Pinned`) | `PinnedMessages` (the pinned-messages panel) |
| | `ChannelSearch` (`Query`) | `SearchMessages` (channel search) |
| Read state | `MessageAck`, `ServerAck` | `AckMessage`, `AckServer` |
| Typing | `ChannelBeginTyping`, `ChannelEndTyping` | `BeginTyping`, `EndTyping` |
| Channels | `Channel` | `OpenConversation` (State backfill) |
| | `ChannelDelete` | `CloseChannel` |
| Conversations | `DirectMessages`, `DirectMessageCreate` | `Conversations`, `OpenConversation` |
| Users | `User`, `ServerMember` | `ResolveAuthors`, `resolveRecipients` |
| | `UserProfile` | `UserProfile` |
| Relationships | `FriendAdd` | `AcceptFriend` (accepts an arrived request) |
| | `FriendDelete` | `RemoveFriend` (unfriend, decline, withdraw) |
| | `UserBlock` / `UserUnblock` | `BlockUser` / `UnblockUser` |
| Servers | `ServerCreate` | `CreateServer` (the join dialog's other half) |
| | `ServerMembers` | `FetchMembers` |
| | `ServerDelete` | `LeaveServer` |
| | `ServerMemberDelete` | `KickMember` |
| Invites | `Invite`, `InviteJoin` | `FetchInvite`, `JoinInvite` |
| | `ChannelInviteCreate` | `CreateInvite` |
| Raw | `HTTP.Request` → `EndpointAutumn(bucket)` | `uploadFile` — attachments, avatars and banners alike |
| | `HTTP.Request` → `EndpointUser` (`profile`) | `SetBio`, `SetBanner`, `RemoveBanner` |
| | `HTTP.Request` → `EndpointChannel` | `FetchSlowmode` (revoltgo models no `slowmode`) |
| | `HTTP.Request` → `EndpointAuthSession("login")` | `Client.Login`, `Client.AnswerMFA` |
| | `HTTP.Request` → `EndpointUserFriend("")` | `Client.AddFriend` (revoltgo has no *send* route) |
| | `HTTP.Request` → `EndpointUserMutual` | `Client.Mutual` (revoltgo's decodes into the wrong shape) |

`Session.Server` is never called, correctly — `client/store.go` reads `State`, and the
account's own servers arrive with Ready. **`Session.ServerEmojis` is the same case and now
the more interesting one:** the emoji picker is built and asks for nothing, because Ready
carries the emoji of every server the account is in, `ServerCreate` carries a joined one's,
and revoltgo registers its *own* default handlers for `EmojiCreate`/`Delete` — so `State` is
the whole set and stays current. The route decodes into a slice it hands straight back and
writes nothing to `State`, so calling it would buy an overlay to maintain rather than an
answer. `NewWithLogin` and `Session.Login` are no longer called either — see below.

### Round the typed API

Six things the client needs are not reachable through revoltgo's types, and all go
through `Session.HTTP.Request`, which takes any struct and any result.

**An upload names its bucket.** `Session.AttachmentUpload` posts to Autumn's *attachments*
bucket and nothing else, while Revolt looks a file up by ID **and** bucket at the moment it
is used — so an attachment's ID offered as an avatar is refused as a file that does not
exist. `uploadFile` is the same request with the bucket as an argument, which is the whole
difference between an attachment, an avatar and a banner; `AttachmentUpload` is therefore
no longer called at all.

**A profile edit cannot carry a banner.** `UserEditParams.Profile` is a `*UserProfile`
whose `Background` is a `*File` — the shape a profile is *read* in — where the route takes
an attachment ID. `Client.editProfile` sends its own body, and the bio rides along in it
rather than being sent the one way the typed API can express: one field pair sent two
different ways is worth less than the one shape.

**`slowmode`** is a field revoltgo models neither on `Channel` nor on `PartialChannel`, so
the number never arrives with the channel and nothing announces a change. `FetchSlowmode`
asks for the raw channel, and re-asks on every visit for want of the event carrying it.

**An MFA login** cannot be expressed at all. `LoginResponse` carries neither the ticket nor
`allowed_methods`, so the challenge is invisible; `LoginParams` carries no `mfa_ticket`, so
the challenge could not be answered; and `MFAResponse` carries only a password, so the
answer could not be a code. `client/auth.go` therefore sends Revolt's own `DataLogin` and
decodes its own `ResponseLogin`. Both stages are the *same* endpoint with different bodies,
and Revolt reads which factor is being answered off **which field** carries the code — so
`answerFor` putting it in the wrong one is a refusal with nothing to say why, which is what
`auth_test.go` exists for. The request goes through a throwaway `revoltgo.New("")`: the
route is unauthenticated, and the session that serves the account is built from the token
afterwards by `Open`, so both stages land on the path a saved login already takes.

**Sending a friend request** is a route revoltgo does not have. `FriendAdd` is
`PUT /users/{id}/friend`, which *accepts* one that has already arrived; sending is
`POST /users/friend`, which names the person by handle and has no method at all. The two are
not interchangeable, and the wrong one aimed at a stranger is a refusal with nothing to say
why — so `Client.AddFriend` composes the body itself, reading the handle out of `State` since
the caller holds only an ID.

**Mutual friends and servers** cannot be asked for at all. `/users/{id}/mutual` answers
with one object and `Session.UserMutual` decodes into a **slice** of them, so the request
fails on shape whatever the account; its struct also drops `channels`, the groups and
conversations both are in. It is the only one of these four that is a plain
mis-declaration rather than a missing field or a missing route.

### Notes on the ones that are not a plain call

**Pinning** is the only action whose result the gateway cannot be trusted to report.
`EventMessageUpdate.Data` is a whole `Message` rather than a partial one, so `Pinned` arrives
as a plain `bool` with no way to tell *now false* from *not mentioned in this update* — read
there, every ordinary content edit would land as an unpin. The pin/unpin **system message**
names the message and says which of the two happened, so `client.applyPinEvent` is what
believes anything about pin state; `Client.PinMessage` writes the cache itself for the pin
this client made, and `markPinned` reports "nothing moved" so the echo does not repaint twice.
Pinning needs `ManageMessages` even over your own message — a pin is a change to the channel,
not to the message — which is why `canPin` does not fall back to authorship the way `canDelete`
does.

**Creating a server** is the one action whose response says nothing. `/servers/create`
answers with the server *and* its default channels, while `Session.ServerCreate` decodes
into a bare `Server` — every field arrives zero, the ID included — so `Client.CreateServer`
reports only the error and the server itself arrives as `ServerCreate` on the gateway. That
is the path a *join* already takes for the same class of reason, so both mark `App.pendingJoin`
and let `onServerJoined` select what turns up. A name is all Revolt takes at creation: no
icon, no description, which is why the card is one field.

**The two searches** are one route asked either way it can be. `ChannelSearch` takes
`pinned` *or* `query` — Revolt refuses them together — so `Client.PinnedMessages` and
`Client.SearchMessages` share `Client.search` and differ in which field they fill.
Listing the pins has to be a search at all because Revolt publishes no collection of them,
the pin being a flag on the message. `Sort` is named in the shared half because the route's
default is `Relevance`, which with nothing to be relevant to is an order nobody chose, and
which for a list read as a channel's history is not the one wanted either. Neither may ask
for the users: `include_users` turns the response from an array into an object and revoltgo's
method decodes only the array, so both hand back author IDs and `app/pins.go` /
`app/search.go` resolve what the store cannot name, in the worker that fetched them.
Nothing is cached — either panel is a snapshot for as long as it is up — and taking a pin off
from one goes through `App.setPinned`, `OnPin` with a hook, the message being one the column
need not be holding at all. A query is 1–64 characters, past which the route refuses rather
than truncates, so `SearchMessages` cuts it before sending.

**Logout** drops the session before it revokes, deliberately: being logged out is a local fact
that must not wait on the network or depend on it succeeding. `Session.Logout` is a REST call,
so the captured session stays usable for it after `Close` has taken the websocket down.
`Client.revoke` is that ordering shared with `LogoutEverywhere`, which passes `revokeSelf: true`
— signing every *other* device out and staying signed in here is not what the words mean — and
whose app half also drops this computer's saved login, the token on it being one of the ones
just revoked.

**Reactions** write the cache once the server has agreed, the way a pin does, and for a
related reason rather than the same one: the gateway *does* echo a reaction back, but a chip
the user has just clicked has to answer now. `client.applyReaction` reports "nothing moved"
when the echo lands on what the action already wrote, so the round trip costs one repaint
instead of two, and the same answer covers a reaction made from another client of this
account. Everything reachable from the cached message is replaced on the way — the message,
its reaction slice and the user list inside it — since all three are read on the UI thread
without the cache lock. Ordering is the client's own (`toReactions`, by emoji): reactions
arrive as a JSON object, so there is no order in the payload to keep.

**Clearing them all** is the same write for a harder reason. Revolt announces it as an
ordinary message *update* carrying an empty reaction map — not `MessageRemoveReaction`,
which is one emoji taken off wholesale — and `EventMessageUpdate.Data` is a whole `Message`,
so an empty map is equally what a content edit brings. Nothing can read it, exactly as
nothing can read `Pinned` there, which is why `Client.ClearReactions` writes the cache
itself and a clear made from another client does not reflect. It is the one reaction
action that is confirmed, undoing other people's clicks rather than this account's, and
it is offered in the context menu alone under `ManageMessages`.

**Presence and the status line** are one object to Revolt, which takes the whole of it — so
whichever half is not being changed is read back out of `State` and sent again unchanged,
either setter omitting the other's half being enough to destroy it. `editStatus` is that
read-and-resend, shared by both. Clearing the line is the one change that cannot be a value:
an empty `Text` is omitted from the request, so it goes as `Remove: ["StatusText"]`.

**The picture, the profile and the username** are the rest of what an account is, and each
is a different shape of the same edit. The **avatar** is `UserEdit` with an ID, so the file
is uploaded first and into the *avatars* bucket — see above — and removing it is
`Remove: ["Avatar"]`, the shape a display name already uses. The **bio and banner** are not
on the user record at all: a profile is a request of its own, `PartialUser` carries a
`Profile` that revoltgo's own `User.update` ignores, and no gateway event announces either
— so `App.loadSelfProfile` asks once per session and again after every edit made here, and
one made from another client is not seen until the page is reopened. That re-read is also
what tells the Banner row it now has something to remove. The **username** is the one edit
that re-authenticates: Revolt takes the account password with the new name, which is why it
is a card on the modal layer rather than a row on a page that stays open, and why
`validUsername` refuses a malformed name here — the server answers a name it will not take
and one somebody else already has alike, with a status code and no sentence.

**The display name** is the same route asked a third way and needs none of that: it is a
single field and Revolt applies the edit as a partial, so `SetDisplayName` sends it alone.
Clearing takes the same `Remove` shape (`"DisplayName"`), `UserEditParams` modelling it as a
plain string with `omitempty`. Revolt's bounds are 2–32 characters and its pattern forbids
newlines and the zero-width space, so `cleanDisplayName` drops those and cuts a long name —
nobody chose the newline that arrived with a paste — and reports back only the case with no
honest repair, a name of one character. That refusal is the one thing the client says out
loud about the row: the request was never made, so "could not" would be untrue.
`domain.User.DisplayName` is the raw name kept apart from `Name`, which has already fallen
back to the username — a field pre-filled with *that* would send the username back as a
chosen name at the first blur.

**A jump window** is the second thing kept out of the message cache, and for the same
reason a reply target is: it is a page from wherever somebody was quoted, and the cache is
a channel's contiguous tail. So `MessagesAround` / `MessagesBefore` / `MessagesAfter` share
one helper that fetches, sorts and hands back — writing nothing — and `App.uncached` holds
what they return, which also lets a quote *inside* such a window resolve without a request
of its own. Whether the mounted column is in the cache is then read off the cache rather
than off a flag, which is what makes deep scrollback — the same situation reached by
scrolling rather than by jumping — stop prepending non-contiguous pages into it.
`Nearby` decides its own bounds (half the limit either side, the message included) and
answers in no documented order, hence the sort; `After` must be asked with `Sort: Oldest`,
Revolt's default with an anchor being the newest messages that happen to be after it.

**A reply target** is fetched one at a time and kept **out** of the message cache. That
cache is the contiguous tail of a channel; a reply reaches as far back as somebody cared to
answer, so a quoted message filed among its messages would be mounted by `loadMoreHistory`
as though it were history. `App.replies` holds them instead, bounded by `maxCachedReplies`
because nothing else evicts them, and `ResolveMessage` reads the cache first and that map
second. Revolt offers no route taking a list of IDs, so `ResolveMessages` is a batch only in
that the caller gets one answer and one repaint for it; the authors behind what comes back
are queued in the same pass, somebody who only ever spoke that far back being nobody the
page has resolved.

**The emoji picker** is one pop-up for two callers — the composer's button and a message's
add-reaction — since both are choosing from the same set and differ only in what they do with
the answer, which is what `EmojiChoice.Value` (a reaction takes the ID or the character) and
`.Token` (a body takes `:ID:`) are for. Which emoji are on offer is the controller's
(`app.emojiGroups`): one walk of `Store.Emojis` bucketed by server, the open server first,
the unicode dozen last — asking per server would be a walk of the whole set per server, and
the picker is opened from a click. The drawn grid is capped at `emojiPickerLimit` and the
search field is what reaches past it; cells are memoised per emoji, so narrowing a query
reorders objects that already exist rather than rebuilding a hundred widgets and re-asking
the cache for a hundred pictures on every keystroke. Names are folded once as the picker
opens (`foldGroups`), a keystroke otherwise lowering the whole set per character typed, and
the line under the grid is what names a cell at all — what the pointer is over, or what
Enter would take when it is over nothing.

**Creating an invite** is offered on a *channel*, not on the server icon — Revolt has no
server-wide invite, only one per channel that lands the joiner in it — and gated on
`InviteOthers`, a bit `domain.Permission` had to name. `util.InviteLink` composes the link and
is deliberately not the inverse of `InviteLinkCode`: the reader must keep accepting every host
Revolt has ever used, because that is what other people's messages contain, while what this
client writes should say what the platform is called now. `text_test.go` asserts the round trip.

**A relationship, once changed, has nowhere to be written.** Ready fills
`revoltgo.User.Relationship` for everybody it names and nothing keeps it current afterwards:
revoltgo registers no default handler for `EventUserRelationship`, `State`'s caches are
unexported, and `PartialUser`-shaped `updateUser` is the only writer there is. So
`Client.relations` is an overlay — the same shape `slowmode` is, read first and falling back
to `State` — written by the gateway handler and by each of the four actions once the server
has agreed, and cleared with the session. There is no collection to fetch either: Revolt
files each relationship on the account it is with, so `Store.Relationships` is a **walk** of
the cached users, asking the relationship before resolving the account because most cached
users are a member of some server and nothing more.

---

## Not covered

### Tier 1 — completes a feature the UI already half-has

| Call(s) | What it unlocks | Also needs |
|---|---|---|
| `Emoji` (metadata by ID) | Naming an emoji the picker cannot: one from a server the account is not in renders in a message and in a chip, and `State` has no record of it. Marginal — the ID draws the picture regardless. | Somewhere to say a name. The picker's own cells have a tooltip now, but they are the emoji this *would not* cover; naming one in a message body means making it hoverable, and it is a bare `canvas.Image` precisely so that hover and the row's menu pass through it. |

### Tier 2 — new but ordinary user-client features

| Call(s) | Feature |
|---|---|
| `AuthMFA*` (8 calls) | **Managing** a second factor — enabling or disabling TOTP, generating and listing recovery codes. Signing *in* with one is built (see above); configuring one is not, so an account can be reached but not secured from here. |
| `ServerChannelCreate`, `ChannelEdit` | Channel create and rename. `EventChannelCreate`/`Update` are handled, so the results would already reflect. |
| `ServerInvites`, `InviteDelete` | Listing and revoking a server's invites. Creating one is built; nothing can see or withdraw them afterwards, and Revolt offers no expiry or use limit to set at creation either. |
| `GroupCreate`, `GroupMemberAdd` / `Delete`, `GroupMembers` | Group DMs beyond viewing one. |
| `SyncUnreads` | Refreshing unread state after a resume; only Ready carries it now. |
| `Sessions`, `SessionEdit`, `SessionsDelete` | A session manager in settings. |
| `Account`, `AccountChangePassword`, `AccountChangeEmail`, `AccountDisable`, `AccountDelete*` | Account settings. |

### Tier 3 — moderation / administration (per `CLAUDE.md`, deliberately not offered)

`ServerMemberBan`, `ServerMemberUnban`, `ServerBans`, `ServerMemberEdit` (nickname, roles,
timeout), `ServerEdit`, `ChannelMessageDeleteBulk`, `ServersRole*` (5 calls),
`PermissionsSet`, `PermissionsSetDefault`, `ChannelPermissionsSet`,
`ChannelPermissionsSetDefault`.

### Tier 4 — out of scope for this client

Webhooks (11 calls), bots (7), `AccountCreate` / `Onboarding*` / `PasswordReset*` /
`VerifyEmail`, `PushSubscribe` / `Unsubscribe` (webpush), `SyncSettingsFetch` / `Set`
(msgp tuples revoltgo itself flags as undecodable), `PolicyAck`, `UserFlags`,
`UserDefaultAvatar`, `WriteSocketJSON` / `MSGP`, and voice (`ChannelsJoinCall`,
`ChannelsEndRing` plus six `EventVoice*` / `EventUserVoiceState*` events — that wants a
LiveKit client, not an action).

---

## The other half: gateway events

An action without its event counterpart does not reflect. `client/events.go` registers 31 of
revoltgo's 49 event types — it was 15 before pinning went in.

### Handled

`Ready`, `Error`, `Message`, `MessageUpdate`, `MessageAppend`, `MessageDelete`,
`BulkMessageDelete`, `MessageReact`, `MessageUnreact`, `MessageRemoveReaction`,
`ServerCreate`, `ServerUpdate`, `ServerDelete`, `ServerRoleUpdate`,
`ServerRoleDelete`, `ServerRoleRanksUpdate`, `ChannelCreate`, `ChannelUpdate`, `ChannelDelete`,
`ChannelAck`, `ChannelGroupJoin`, `ChannelGroupLeave`, `ServerMemberJoin`,
`ServerMemberLeave`, `ServerMemberUpdate`, `UserUpdate`, `UserRelationship`,
`UserPlatformWipe`, `ChannelStartTyping`, `ChannelStopTyping`, `Logout`.

Everything a server, role, member or channel can do is now covered. Creating a role has no
event of its own — it arrives as `ServerRoleUpdate` for a role `State` has never heard of,
which revoltgo files on the way past.

The three role events collapse onto one `RolesChanged`: what a reader does about a role is
re-read the members it colours and orders, and that is the same walk whichever of the three
arrived. Every rebuild a handler asks for goes through `App.queueRefresh`, a dirty set over
three surfaces flushed on one settling window, because Revolt's bursts do not respect the
boundary: a rank reorder is an event per role, and a channel added to a server is a create
*and* a server update. `ChannelAck` is the one event that exists *because* of another client — without it a
conversation read on a phone stayed bold here for the life of the session — and our own acks
echo back through it onto a mark already cleared. `Logout` is a session revoked from
elsewhere and is the same fatal drop a rejected authentication is.

`revoltgo`'s own default handlers keep `State` current for every one of these first, so each
handler here only names what moved and lets the store answer what things now are —
`UserRelationship` being the one exception, and the reason `Client.relations` exists. It is
also the one event that can name somebody `State` has never cached, since nothing files the
account it carries; the friends list queues them through `ensureAuthor` and refills when they
land. Note the `ID` on it is **this** account and the `User` it carries is the other half.

Two leaves are announced to everyone **except** as a deletion to the one who left, so both
are recognised by their own user ID and handed to the path that already exists:
`ServerMemberLeave` for our own member is a server left (revoltgo evicts it from `State` on
the strength of it, and no `ServerDelete` follows), and `ChannelGroupLeave` for our own is a
conversation closed. `ChannelGroupLeave` *embeds* `ChannelGroupJoin` rather than aliasing it,
the third such pair in the file, so both must be registered.

### Still unhandled, and what each would cost

- `EventEmojiCreate` / `Delete` — deliberately, now that the picker exists. revoltgo's own
  default handlers file both into `State`, which is what a handler here would have had to
  arrange, and the picker reads `State` as it opens rather than holding a copy — so there is
  nothing left for one to do.
- `EventUserSettingsUpdate` — revoltgo flags its msgp tuples as undecodable (Tier 4).
- `EventReportCreate`, `EventWebhook*` and the six voice events belong to Tier 4.

Note that `EventChannelUpdate` still does **not** carry slowmode: revoltgo models the field
neither on `Channel` nor on `PartialChannel`, which is why `FetchSlowmode` re-asks on every
channel visit rather than trusting the event now that the event is listened to.

---

## Known limits of what is built

- **A pinned message is marked in the column and collected in a panel.** The mark rides the
  name line, so `continuesGroup` refuses to group a pinned message — grouped, it would have
  nowhere to draw. The panel is opened from the channel header and is a **snapshot**: it is
  one search, nothing keeps it current, so a pin made anywhere while it is open — this
  account's own from another client included — does not appear until it is reopened. A row
  is a flattened one-line summary and leads to the message; a body with no text says what it
  carries instead. It is capped at the hundred newest, Revolt's own ceiling on a search, with
  no way to page past it.
- **Channel search is that panel with a query**, and inherits all of it: a snapshot, the
  hundred newest matches with no paging, one-line summaries, and the open channel alone —
  there is no search across a server, Revolt's route being per channel. It asks on Enter
  rather than as you type, since each query is a request. Revolt's matching is MongoDB's
  full-text search, so it is words rather than substrings: half a word finds nothing, and
  what counts as a word is the server's business.
- **A reaction now says who**, on hover, and only as far as the store can name them: a
  chip's tooltip is `domain.Reaction.Users` folded through `Store.UserName`, so anybody the
  client has never fetched is counted rather than named. It names the first ten and counts
  the rest.
- **An unpin made from another client is believed only through its system message.** If
  Revolt ever stops emitting one, the flag would go stale until the channel is re-fetched;
  the partial update alongside it cannot be read for the reason given above.
- **An invite made here is permanent.** Revolt takes no expiry and no use count at creation,
  and nothing in the client lists or revokes one afterwards (`ServerInvites` / `InviteDelete`).
- **The status line is set, not seen.** Nothing in the client draws anybody's status text,
  this account's included, so the settings row is the only place it appears — and it shows
  what the store last said rather than what was just typed, the change returning as a
  gateway event that does not rebuild the page. Over `MaxStatusText` is truncated by rune
  rather than refused, the limit being Revolt's.
- **An account is edited whole, except for the parts Revolt does not send back.** The
  picture, the banner, the description, the display name, the presence, the status line and
  the username can all be changed from the Account section. What that section cannot do is
  *follow* two of them: nothing announces a bio or a banner, so both are a snapshot asked
  for once per session and re-read after each edit made here. Nothing checks a picture
  before it is uploaded either — Autumn owns the size and the type, and a refusal is a
  status code — so the notice can only offer "it may be too large". `pronouns` is a field
  Revolt takes and revoltgo models nowhere, so it is neither read nor set.
  The section is **rebuilt** when a user update moves the picture or the handle, and
  deliberately not when it moves the display name: that field is on the same section and
  Enter leaves the cursor in it, so an echo would rebuild the page under whoever was typing.
- **A username change cannot say why it failed.** Revolt answers a name somebody else
  already has and a wrong password alike, with a status code and a body revoltgo formats
  into a string, so the card names both possibilities rather than guessing between them.
  What it *can* answer for is the pattern, which `validUsername` holds a copy of.
- **A second factor is a code, and only a code.** Signing in with TOTP, a recovery code or a
  password challenge works; nothing enables, disables or lists one (`AuthMFA*`, Tier 2). A
  security key is refused by `answerFor` rather than offered — Revolt names the method, and
  there is no WebAuthn here to answer it with. The ticket is short-lived and there is no
  resend: an expired one is a failure notice and a trip back to the login screen.
- **A quote leads somewhere, and a jump window is a dead copy while it is up.** Tapping a
  reply preview mounts the page around what it names, which is a request when the message
  is older than the cache. That page is deliberately *not* cached, so nothing keeps it
  current: every event handler writes to the cache, and `refreshMessage` finds nothing —
  an edit, a pin or a reaction made while a jump is showing does not reflect until "Jump
  to present" brings the tail back. A delete does, `removeMessages` walking the mounted
  widgets instead. There is no way back to the *position* the jump left, only to the tail.
  The pinned-messages panel and channel search are the second and third ways in — a row in
  either leads to its message the way a quote does.
  A quote that resolved to nothing is not tappable, a failed fetch keeping its guard rather
  than releasing it the way an author's does — the usual reason is that the message was
  deleted, which stays true, and a quote remounts on every scroll past it. So a quote
  missed through a dropped connection keeps its placeholder, and stays a dead end, until
  the channel is reopened.
- **A reaction draws a count and names who on hover.** The chips are ordered by emoji rather
  than by who reacted first, there being no first in a JSON object, so a busy message reads
  in an order nobody chose. **A clear made from another client does not reflect**, for the
  reason above — the update announcing it cannot be told from an edit.
- **A confirmation is skipped by holding Shift, on Windows only.** Fyne answers no question
  about a modifier outside an event it delivers: a canvas key handler fires only while
  nothing holds focus, and a context-menu item reports no modifiers at all — so `ui.ShiftHeld`
  asks Win32 and the other half of the pair answers false. `App.confirm` is the single place
  that reads it, so it covers every destructive action rather than the ones wired to it, and
  the card names the key only where it works (`shiftSkippable`).
- **The friends list is as complete as the account cache is.** It is a walk of the cached
  users, so somebody Ready did not name and nothing has since fetched is not in it. It does
  not follow presence, has no search, and offers no way to add somebody by handle — adding is
  a profile button, and a profile is a surface only their message or their member row opens.
  Nothing announces an incoming request beyond the sidebar row's mark, which is drawn in the
  home view alone. A *server* relationship — Revolt's own `Relations` array on the account —
  is dropped at the boundary, so what is held is one value per person met rather than the
  whole graph.
- **The emoji picker lists what `State` holds**, which is the servers the account is in. An
  emoji from anywhere else renders in a message and in a chip and is in nothing to be picked
  from. The cells themselves carry no caption — the line under the grid names one at a time,
  so a picture that has not landed can be identified under the pointer but not scanned for.
  `Message.Interactions` (the set a message restricts reactions to) is still dropped at the
  boundary, so a pick it forbids is refused by the server rather than not offered.
- **A mutual chip leads somewhere only if it can be named.** Both lists are capped at
  `profileMutualLimit` with the rest counted into a "+n" — which also absorbs anybody the
  store cannot name, the totals being carried apart from the entries for exactly that, and
  which answers nothing, there being nobody named in it to lead to. Only the dialog carries
  them, they do not refresh while it is open, and `channels` (the groups and conversations in
  common) is dropped: Revolt sends it and there is nowhere here to say it.

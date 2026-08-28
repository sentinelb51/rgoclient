# internal/app

The controller: owns the client, caches and widgets, and passes what widgets
need through `ui.Deps`. This file is the data flow end to end — what happens in
what order, and why each step is where it is. See the root `CLAUDE.md` for the
DAG and conventions.

## Data flow

1. **Login.** `App.Run` starts `pumpEvents` before the login screen.
   `Client.Open`/`Login` drops the previous session, registers handlers against a
   fresh epoch and opens the gateway; `startWithLogin` stashes the token in
   `pendingToken` *before* Ready can land. The screen stays up until Ready.
   A **second factor** makes that two requests: `Client.Login` returns `Pending`
   with the ticket the server holds the login on and the methods it takes, and
   `AnswerMFA` finishes it. Nothing is logged in in between — a password is not a
   session — so `showMFAChallenge` *replaces* the login screen rather than
   stacking a dialog on it, there being nothing behind it. Both stages land on
   `Client.Open`, the path a saved token takes.
   Both screens report through **`ui.ModalNotice`**, not `dialog.ShowError`:
   `NoticeStack` belongs to the main UI and does not exist until Ready, and a Fyne
   dialog is the one surface `AppTheme` does not reach. `mountLogin` stacks that
   layer over either screen and sizes the window to what the card *measures*
   rather than to a number — the saved-session list is as long as it is. The layer
   takes no room, so neither screen reserves a line for a message that is usually
   not there, which is what a `ui.StatusLine` did. The
   way out for somebody with no account is a `ui.LinkText` to Stoat's signup page
   (`registerURL`); registration cannot happen here, see `docs/known-gaps.md`.
   **A session that opens and never reports is the failure that looks like a
   hang**, so `awaitReady` watches for the snapshot: `Client.Open` returns once
   the websocket is up, Ready alone names the account, and revoltgo drops an event
   it cannot decode before any handler runs. At `readyTimeout` the session closes
   and the login screen returns saying so. `onReady` disarms it, as does
   `resetSessionState` — the gateway that owed the snapshot is being replaced.
2. `onReady` → save token, record unreads, `showMainUI`, `refreshServerList`,
   `selectServer(first)` — or `selectHome` when the account is in no servers.
   The unread channels and the mentions are both taken **wholesale**, not merged:
   Ready is the account's whole read state, so a reconnect carrying neither must
   not leave a channel read on a phone still bold here. What is carried over is
   `dismissedMentions` alone (item 33), Revolt keeping no record of that.
3. `selectServer` → `refreshChannelList`, `refreshMemberList`, `loadMembers` →
   `selectChannel(first)`. `loadMembers` is **one request for the whole
   membership**, once per server per session (`App.fetchedMembers`,
   `Client.FetchMembers`): Revolt has no pagination and no member search, so a
   server is all of it or none, and `exclude_offline` is declined because the
   Offline section is the point. Paint-then-fill, so re-entering a server never
   blanks its list. It is a setting (`FetchAllMembers`) because it is the one call
   whose cost is somebody else's server. It also fills the *user* cache, which is
   what makes presence work — `State.updateUser` drops an update for an account it
   has never seen, so an unfetched member could never be seen to come online.
   Lazy per-author resolution stays for what it does not reach: webhooks, people
   who have left, a failed fetch, conversations. `finishMembers` also arms
   `scheduleHeapTrim` (`App.heapTrim`, debounced 15 s): decoding a whole
   membership peaks at a few times its resting size and the runtime keeps the
   peak mapped, so the trim runs `debug.FreeOSMemory` once the fetches settle.
4. **Mounting a channel.** `selectChannel` → cached messages, else
   `Client.LatestMessages` (deduped per channel); ack unread. Callers render from
   the *cache* (`displayCached`), never from a page captured off-thread.
   `displayMessages` holds only the newest `initialMountCount`.
   `loadMoreHistory` is three-tier: unheld cache synchronously, then
   `HistoryBefore`, then `MessagesBefore` for a window not in the cache at all
   (which writes nothing to it). The window is bounded at `mountedCap`.
   The window is `ui.MessageList`, virtualised: the App addresses it by message
   (`Message(i)`, `Index`, `Mounted`) and only the rows on screen have widgets, so
   the cap bounds what is indexed rather than what a frame costs. The list derives
   grouping (`continuesGroup`, within `messageGroupWindow`) and the day separator
   from a row's neighbours and builds the widget; every build reports through
   `OnMount` → `App.onMessageMounted`, which is where `ensureAuthor` and
   `ensureReplies` run — again for a row scrolled out past the overscan and back,
   its widget having been dropped in between. The separator belongs to the widget,
   not a row of its own, so the window stays one row per message.
   A column with nothing to draw says so on one line (`App.showStatus` →
   `ui.NewMessageStatus`). `showStatusMark` is that line led by a mark, and only
   an empty channel carries one: every other status is a wait or a refusal, where
   a mark would decorate an apology.
   `syncChannelKind` is what the header owes each switch: the prefix glyph, and
   the note under it that only a **voice** channel draws. Revolt keeps messages in
   a voice channel like any other, so nothing about *mounting* one differs — the
   glyph and that strip are the whole of what says so. The strip is built once and
   shown per channel, and hiding a child reclaims nothing on its own, hence the
   `ui.Relayout` either way. It is also the primary way into the call (item 36):
   `syncVoiceNote` gives it a caption and a button per state, joining never being
   a side effect of selecting.
5. **Author resolution.** A message carries only an author ID. `ensureAuthor`
   checks `HasUser`/`HasMember` (which exist so this allocates nothing — it runs
   per mounted message) and queues gaps; `authorTimer` fires `authorFetchDelay`
   later and `flushAuthors` makes a **single** trip back to the UI thread.
   `fetchedAuthors` guards each (server, user) pair, released on failure so a
   later message retries. A **system** message has no author but names a target
   and reads "Someone joined" until that user is known, so it queues
   `System.Target` and `MessageWidget.Author` answers with it — which lets
   `refreshAuthorMessages` cover both in one pass. `RefreshAuthor` relayouts the
   line, the name sitting *inside* the sentence with the time beside it.
   In a server the batch then goes through `refreshMemberList` whatever it
   resolved: `AuthorResolution` does not distinguish a member fetch from a user
   one, because `toMember` fills a membership's name and username from the account
   behind it and `memberCandidates` drops a member it cannot name — so a resolved
   *user* is what makes an already-cached membership mentionable.
   **A reply target resolves the same way and is stored apart.** `ensureReplies`
   queues what `ResolveMessage` cannot answer for; `flushReplies` fetches the batch
   (`Client.ResolveMessages`, guarded by `App.fetchedReplies`) and then queues the
   *authors* behind what came back — somebody who only ever spoke that far back is
   nobody the page has resolved. What arrives goes in `App.uncached`, not the
   message cache: that cache is a channel's contiguous tail while a reply reaches
   as far back as somebody cared to answer, so one filed among its messages would
   be mounted by `loadMoreHistory` as history. Nothing else evicts them, hence
   `holdUncached` dropping the store and its guard together at
   `maxUncachedMessages`. The guard is **kept** on failure, unlike `ensureAuthor`'s:
   a target that cannot be fetched was usually deleted, which stays true, and a
   quote remounts on every scroll past it — releasing it is a request per pass for
   an answer that cannot change.
   `ui.replyPreview` is a struct, not a built subtree, for exactly this: it mounts
   saying it found nothing and fills itself through `RefreshReplies`, which takes
   the resolved *set* because re-laying a line out is not free and every mounted
   row is offered the batch. A grouped continuation draws no quotes, so nothing is
   queued for one.
6. **Incoming messages.** The client caches one (the cache returns the predecessor
   under its own lock, so grouping survives bursts) and emits `MessageCreated`
   with both. If scrollback has detached the view from the tail the append is
   skipped — it mounts on the way back down. An edit replaces the cache entry with
   a *copy*; entries are read without the cache lock, so they stay immutable.
   Deletes arrive as one `MessageDeleted` carrying a slice → one `removeMessages`
   pass, `rebuildSeams` re-grouping at each seam.
7. **In-place editing.** `startEditing` — one at a time (`App.editing`). Save
   applies optimistically and calls `Client.EditMessage`; failure reverts.
   Message-area rebuilds cancel the active edit; `refreshMessage` leaves a message
   being edited alone.
   **An edit is announced twice over**: the row trails a pencil and how long ago
   (`MessageWidget.buildEditMark`), and `refreshMessage` flashes it as it lands.
   An update arrives as "this message changed", so `newlyEdited` compares the held
   copy's `Edited` stamp with the new one — a reaction or an unfurled embed must
   not flash — and only if `MessageList.InView` says the row overlaps what the dock
   does not cover, only a row on screen having a widget to wash. The optimistic
   copy carries no stamp, so the author's own edit flashes once, off the gateway
   echo, rather than twice.
   The span goes stale on a row nothing else redraws, so `refreshTimeSpans` walks
   the mounted widgets every `timeSpanTick` (a minute) and re-arms only while one
   still has something to re-read. It covers **both** things a row says about the
   clock: the "edited N ago" mark, and a `<t:…:R>` in the body, which
   `MessageWidget.RefreshRelativeTime` re-renders and `markdown.HasRelativeTimestamp`
   is what marks a row as carrying. `onMessageMounted` arms it unconditionally —
   the walk is what decides, and one wasted tick per channel visit is cheaper than
   a second parse of every body to find out.
8. **Mentions.** `@`, `#` or `:` at the start or after a space opens the picker,
   which gets first refusal on Up/Down/Enter/Tab/Esc. The marker decides which of
   the three pools is filtered and what the span is rewritten as — Revolt's
   `<@id>` or `<#id>`, which `ui/markdown.go` renders back as `@Name` and
   `#channel`, or an emoji's `:id:` / character, exactly what the pop-up picker's
   button inserts. `MentionKind.marker`/`markerKind` are the only place the three
   characters are named. A heading's `# ` opens the channel list for the one
   keystroke before the space closes it; refusing the picker at the start of a line
   would cost every mention typed there. `:` is the one marker that does not open
   on itself — `emojiQueryMin` characters have to follow it, colons being ordinary
   punctuation and `:)` not a search.
   Candidates are **pushed** — `refreshMemberList` and `refreshChannelList` build
   rows and candidates from one walk — so a keystroke is two string comparisons per
   candidate, nothing allocated. Emoji come from `refreshEmojiCandidates`, which
   flattens the same groups the pop-up picker draws, called from
   `refreshServerList` (the set) and `enterServer` (the open server's first). A
   **server's** people therefore arrive only from `refreshMemberList`, which makes
   that walk off the UI thread; `refreshRecipients` covers the conversation
   case alone (`recipientCandidates`, bounded by the channel's recipient list) and
   returns at once for a server channel. Asking it for a server's would walk a
   whole membership on the UI thread, per channel switch, for what the picker
   already holds — every path into a server channel goes through `enterServer`.
   The picker mounts *inside* the composer card, not floating: a Fyne pop-up takes
   canvas focus, which would stop the typing that drives it. Being inside the card,
   **it must not close on blur** — Fyne unfocuses on the mouse *press* and
   re-hit-tests on the release to decide where the tap lands, so hiding here
   resized the composer out from under the click and the first click on anything
   was spent dismissing the picker. Visibility follows the caret instead
   (`syncMentions`, from the typing methods and from `MessageInput.MouseDown`,
   where `widget.Entry` moves the caret). An open picker outlives the entry's focus
   and can outlive its channel, so `SetCandidates` re-runs the query.
   A **rendered** mention is tappable: `mentionSegment` / `mentionText` in
   `ui/markdown.go`, reaching `Actions.OnUserTapped` (anchored on the word, so the
   card opens beside the name) or `Actions.OnChannelTapped`. It is a widget because
   a `TextSegment` carries a colour but not a tap, and that costs what every custom
   segment costs: RichText measures one only to subtract it, so it can neither
   break nor be broken before. Hence per-word splitting *and* `mdBuilder.reserve` —
   the widest mention word in the body, kept clear on the right, the only thing
   stopping one at a line end being cut off by the message column. Anything else in
   a body that answers a click (`decoratedText`) carries `onMenu` for the same
   reason: the driver gives the press to the innermost object accepting one and
   does not walk back up, so a word without the message's menu is a hole in it.
   A **link** in a body is the third thing there that answers a click, and the
   only one whose destination somebody else chose: `Actions.OnLinkTapped(raw,
   label)`. The controller owns it because both questions it raises are its to
   ask — a scheme outside http/https/mailto is refused with a notice saying so
   (`fyne.App.OpenURL` reaches ShellExecute and xdg-open, which run whatever a
   scheme is registered to), and a link whose visible text names a host it does
   not open raises `App.confirm` naming the real one before anything is opened.
   A refused destination never becomes a segment at all — `mdBuilder.link` draws
   it as plain text — so the check is made twice on purpose.

   A message naming the account is washed warm rather than transparent
   (`MessageWidget.fill`, decided once at construction from
   `Message.MentionsUser(Store.SelfID())`). It reads Revolt's own `mentions` plus
   its channel-wide flag, not the content, so a reply with its mention toggle on
   counts and an `@everyone` counts without naming anybody. The colour is a *rest*
   state, so hover lifts it rather than replacing it.
9. **The home view.** `App.homeSelected` marks it open (home has no server ID).
   The list comes from `Client.Conversations`; the app keeps only the order
   (`App.dmChannels`). No gateway event maintains it, so `selectHome` paints the
   cache immediately and refreshes in the background. Ordering is a snapshot — an
   incoming message marks its row unread rather than re-sorting under the reader.
   **Friends and Saved Notes are pinned above that list** (`App.channelTop`, via
   `setChannelGroup`), neither being a conversation with anybody: one is ordered by
   nothing, the other would be moved about by its own last message. The block sits
   *outside* the list's side padding and above its scroll, so it is the full width
   of the column and does not scroll away; its own hairline marks it off, and an
   empty group draws none. Saved Notes is drawn twice over as not-a-person:
   `ui.avatarLed` leaves it a glyph row rather than the taller avatar-led card, the
   picture otherwise being this account's own standing in for a notepad. Its rows
   answer to selection, unread and typing like any other, hence `App.channelRows`
   walking both halves — a walk that knew only the list would leave one row that
   never repaints. The Friends row is *not* in that walk, being no channel, so
   `syncChannelList` paints it separately (`syncFriendsRow`): it answers to
   selection too, the page it opens standing in the same slot a channel's messages
   would (item 27).
10. **Joining a server.** The join response does *not* add the server: revoltgo
    decodes it into an `Invite` whose `ServerID` is never populated. The
    `ServerJoined` event does, and `App.pendingJoin` tells that handler to select
    what it adds. Both entry points — the dialog and an invite card — go through
    `App.joinInvite`, differing only in where a failure is said. The **dialog
    previews** what the field's code opens: `invitePreviewDelay` after the typing
    stops, since every prefix of a code parses as one, then `ResolveInvite` and a
    `NewInviteCardFor` beside its own button. The preview grows the card, so
    `JoinServerDialog.OnResize` is `App.repositionOverlay`, and `closeOverlay`
    calls `Close` to stop a delay outliving the dialog.
    An **invite link in a message** unfurls into `ui.InviteCard`, built from a
    *code* rather than an invite because a code is all the message carries.
    Resolving one is `Client.FetchInvite` (that route *does* populate `ServerID`),
    so a card mounts loading and fills itself through `SetInvite` — also how a
    caller already holding a `domain.Invite` skips the request
    (`NewInviteCardFor`). Its width is fixed, not measured like an embed's: it
    mounts saying nothing, and resizing on arrival would shuffle the column under
    someone reading it. Its action follows membership — `Store.Server` for a
    server, `Store.Channel` for a group (a group *is* its channel, there being no
    server to be in) — and offers `OnServerTapped` / `OnChannelTapped` rather than
    `OnJoinInvite` where the account is in it already. `domain.Invite.Kind` is
    what picks between the two, and what the card is named by: Revolt serves both
    through one route and describes a group with the channel fields alone.
    `App.invites` caches both outcomes, failures included (an expired invite stays
    expired, and a card remounts on every scroll past it), and `App.pendingInvites`
    collapses two cards for one code onto one request.
    Finding the links is `markdown.Links` over the parsed body, not a scan of the
    source: a URL in a code span is not a link, and a spoiler's contents are
    deliberately not reported. `util.InviteLinkCode` is the **strict** matcher
    deciding which of those URLs is an invite, not interchangeable with
    `util.InviteCode` — the lenient one serves a field somebody typed into and
    reads a code out of any last path segment, which pointed at a channel's worth
    of links would card half of them. `util.MayContainInvite` keeps the parse off
    the mounting path for almost every message.
11. **Slowmode.** The number rides on the channel and `ChannelUpdate` announces a
    change, so `selectChannel` and `editChannel` both read `store.Channel` and
    nothing asks the network. `App.slowmodeOf` is the cooldown as it applies *to
    this account*, so `BypassSlowmode` collapses it to zero and the badge never
    appears for a moderator. `handleSubmit` refuses while `slowmodeRemaining` is
    non-zero and keeps what was typed, saying nothing: the badge counting down is
    the answer, and a notice per keypress would bury it. The cooldown starts
    optimistically at submit — so a second Enter can't outrun the request — and is
    given back when the send fails. `onMessageCreated` starts it too, covering a
    message the same account sent from another client (`startSlowmode` won't
    restart a running one, so our own echo is a no-op). `refreshSlowmode` re-arms
    one timer a second at a time rather than running a ticker for the life of the
    app.
    The badge sits *outside* the card, above its right edge: inside, it was
    furniture the entry had to make room for. Its pill hugs the text rather than
    spanning the row — a surface that wide just above the card reads as a bar
    growing out of it. `App.composerDock` is that row and the jump bar stacked over
    the card, and the whole stack floats, so `ui.DockReserve` covers both.
    Relabelling moves only where the chip starts (`SlowmodeBadge.OnResize` →
    `ui.Relayout`); appearing or disappearing changes the stack's height, so
    `refreshSlowmode` calls `App.resizeDock` — Fyne reclaims nothing for a
    shrinking minimum.
12. **Notices and confirmations.** `App.confirm(ui.Confirm{...})` for anything
    irreversible, `App.notify(tone, …)` for an outcome the user didn't ask about,
    and `App.notifyModal(tone, …)` for one that must not be missed — the same
    message in the middle of the window instead of the corner, for
    `config.Notifications.ModalSeconds`. Which of the two a call site takes is its
    own judgement: the corner is the default and the middle is the exception, so a
    receipt for something the reader just did stays in the corner. It is also all
    the login screens have (item 1). The card floats rather than dimming — it is
    click-through except for itself, a tap on it dismisses it early — so a message
    nobody has to answer never stops the client answering.
    A `ui.Tone` is the *only* thing deciding colour, icon and button weight.
    **A heading says what happened.** `Tone.title()` is the fallback and is one
    word — Done, Not done, Failed — because that is the most a tone alone can
    honestly say; a caller those would misname takes `notifyTitled` and supplies
    its own. Never a pleasantry: a heading carrying no information spends the card's
    first line on nothing.
    Destructive actions share one shape: a `can…` check decides whether to offer
    it, `confirm…` asks, the action fires through `App.background`, and the
    **gateway event** updates the UI. Nothing is removed optimistically.
    **Holding Shift answers a confirmation in advance**, `App.confirm` being the
    one place that decides it — so the key covers every destructive action in the
    client rather than the ones somebody wired it to, deleting a message included.
    A `Confirm` with no `OnConfirm` is never skipped: it is a statement, and
    skipping it would say nothing. `ui.ShiftHeld` asks the platform at the moment
    of the click rather than tracking the key (see the footgun) and is
    Windows-only, which `shiftSkippable` carries to the card so the hint under the
    buttons appears only where it is true.
    A confirmation's two answers are **half the card each**, not a pair in the
    corner: the same two targets in the same places every time, so it is answered
    by position rather than by reading a small label. Tone still colours only the
    confirming one, so which is destructive is read off that rather than off which
    is easier to hit.
13. **Settings.** The page is `ui.SettingsPage`, a layer in the window's content
    stack beside `notices.Layer` and `tooltip.Layer` — **not** a canvas overlay,
    because `mountOverlay` closes whatever was there and a confirmation raised from
    settings has to draw *over* it. `App.bindKeys` decides who owns Escape
    (overlay, then settings, then nobody) and is called from all four of
    mount/close overlay and open/close settings.
    A change goes `SettingsPage.change` → `App.updateSettings` → `config.Update`,
    which is all a Behaviour flag needs: they are read where they are used
    (`store.Members`, `continuesGroup`, `messages.go`'s mount caps). Performance
    reaches past the client on both counts: `applyPacing` hands the frame rate,
    vsync, partial repaint and whether the caret blinks to the patched Fyne
    (`rgoclient-fyne`), which picks each up where it uses it — the caret at the
    next refresh of an entry, the rest on the next tick. The frame rate is the
    focus-dependent one: `applyFrameRate` hands over `BackgroundFrameRate`
    instead while the window is unfocused, re-applied by the foreground hooks in
    `startAlerts` — off the UI thread, which is safe because everything it
    touches is an atomic. `applyAffinity`
    hands the process itself to a set of cores through `internal/cpu`. `Run`
    calls both once at startup. The core setting is the one on the page that
    moves things nothing here draws — the gateway, the image loaders,
    miniaudio's callback — and `resolveCores` is the whole of the policy
    (`cpu` says which cores are which and never which to take): an unset or
    foreign value collapses to the machine's default and is written back, so the
    file always names an actual set. A style goes through `restyle` →
    `App.applyStyles`, which rebuilds the theme tables and then **defers** the tree
    rebuild while the page is open — the page covers the client, and `SetContent`
    under a slider mid-drag would take the slider with it. `App.stylesDirty`
    carries that to `closeSettings`; what answers a drag meanwhile is the section's
    own preview, built from real widgets. Styles are *overrides* keyed by
    `theme.Sizes`/`Colors` field names and applied by reflection, so the curated
    groups and the generated Advanced list add up to the whole table —
    `settings_test.go` asserts it.
    A section returns `[]settingsGroup` — a card *beside its caption* — because the
    rail lists the open section's groups under it and scrolling to one is an offset
    into the pane. That offset is a prefix sum over `MinSize`, taken once per
    section (`measureGroups`): `Position()` is right only while the pane's top
    inset is zero and is unset before the first layout, and the scroll path must
    not walk the pane per event. A tap sets the marked entry itself, since the
    scroll clamps at the end of the content and the last group never reaches the
    top; `ObservableScroll.OnScroll` corrects it afterwards and fires only for real
    movement, never a programmatic one. The rail is **not** rebuilt to move the
    marker (`settingsRailButton.setSelected`) — following a scroll would destroy
    the button under the pointer, which then never hears `MouseOut`. A
    caption-less group is a preview: a card, but nowhere to go, hence `navGroups`
    beside `subButtons`.
    **Advanced mode** (`config.Interface.AdvancedMode`, the switch at the foot of
    the rail) keeps the page short. `p.adv(row)` returns nil in basic mode,
    `separateRows` drops nils — also closing the hole where a `sizeRow` for an
    unknown field reached a container — and `group` drops a card with nothing left.
    A `styleGroup` is gated whole rather than per row: each ends with its own reset
    button, so gating the sizes would leave a card holding only a way to undo them.
    It is read in `Rebuild`/`reload`, not per section, since a rail tap cannot
    change it and the two things that can both come through `reload`.
    `showSection` holds the fallback off `SectionAdvanced`, because About's reset
    turns the mode off from another section entirely.
    The controls are the client's own (`settings_controls.go` — see the footgun on
    Fyne's form widgets). A row with a *slider* stacks it under the description at
    full width (`stackedRow`, `newWideNumberControl`), 190 px not being enough to
    aim one; `sizeRow` stays inline, a line of table with no prose, of which Styles
    and Advanced mount a hundred. Everything else sits in a row's `fixedControl`,
    so a row is the same height whichever it holds — which is what lets `numberBox`
    swap its number for a `widget.Entry` without a layout jump. That swap is where
    a stale focus bites: focusing a second box makes the first report `FocusLost`
    *after* the second installed its field, so `numberBox.commit` ignores the
    reporting entry unless it is still the open one. The colour picker floats on
    `SettingsPage.popover`, inside the page's own layer, for the same reason the
    page isn't on the modal one.
    `newSettingsMarker` is the one bar saying "this is the open section" and "this
    setting is on". It is inset vertically by `SettingsGroupRadius` on every row
    rather than drawn full height: the group card is stacked *under* its rows, so a
    bar reaching a corner squares it off, and insetting only the end rows would
    need a row to know its own index and give three different bar lengths.
    Row copy is UI text, not commentary: the label names the setting and stands
    alone, the description says what changes in one plain sentence, and a row whose
    label is complete carries none. What the client does internally is a Go comment.
14. **Profiles.** `Actions.OnUserTapped(userID, anchor)` opens the compact card
    beside the anchor; "Full profile" swaps it for the centred dialog. Both draw
    from one `domain.Profile` that `profileOf` resolves in a single pass. The bio
    and banner are a request of their own, made *after* the card is up and filled
    in through `SetProfile`. Only the dialog carries an About section — the card
    names someone, and the bio is what expanding it is for — so a bio grows the
    dialog, hence `repositionOverlay`. The banner *replaces* the accent strip
    rather than covering it: a `canvas.Image` takes one radius for all four
    corners, so the card's own corners are right at the top and the bottom band is
    laid over itself squared off to meet the body.
    **Mutual servers and friends** are a third late arrival, the dialog's alone,
    same pattern (`loadMutual` → `SetMutual`). `Client.Mutual` goes round revoltgo
    (see the note) and `App.mutualProfile` resolves the IDs, handing the *totals*
    over beside the names: somebody the store cannot name is still one of the
    people in common, so the card's "+n" counts them rather than the total quietly
    shrinking to whatever is cached. Nothing is asked about this account,
    everything being in common with yourself.
    A named chip **leads somewhere** — `ui.MutualEntry.Open`, supplied by the
    controller as a `ProfileButton.Do` is, since where a name goes is a question
    about what is behind the dialog. Both destinations *replace* the modal layer
    rather than stacking: a server is behind the dialog, so the dialog goes, and
    another profile is the same surface with somebody else in it. A nil `Open`
    draws the plain chip, which is what the "+n" is and must stay — it names
    nobody. `ui.NewTappableChip` is its own widget rather than a
    `TappableContainer` around a chip, that one hovering a square behind whatever
    it wraps: a square lighting up behind a rounded label is a second shape
    appearing rather than the chip answering.
    **What the card offers to do is a `[]ui.ProfileButton` the controller hands
    over** (`App.profileButtons`), not a func field per action: which of them apply
    is entirely a question about `domain.Relationship`, and the widget has no
    business knowing Revolt's states. "Message" is therefore *not* always offered —
    Revolt will not open a conversation with a stranger, so a stranger is offered
    "Add friend" instead, and a bot is the exception that is only ever written to.
    A `nil Do` draws the button **disabled** rather than leaving it out ("Request
    sent" is the state). `Danger` tracks the confirmation exactly: removing a
    friend and blocking are confirmed and drawn destructive; declining and
    withdrawing are neither, being undone by asking again. The compact card draws
    only the first button; the dialog draws them two to a row
    (`profileButtonRows`), an odd last one full width, so one action is not a
    different size depending on how somebody else stands with you. Every one of
    them closes the card first — a profile does not refresh while it is open, so
    one left up would go on offering what has just been done, and the notice is the
    only receipt.
    `Overflow` is where one is drawn rather than whether: blocking, removing and
    copying the ID go behind the card's **hamburger** (`profileMenuItems`, a
    `ui.GlyphButton` on the banner beside the close button), none of them being
    what a profile is opened for, and a row leading with a way to block the person
    it names says the wrong thing. `Icon` marks it there, already in the colour it
    is drawn in — which mark names an action is decided beside the action, and only
    the menu reads either field, so `ui.FriendsDialog` ignores both and still draws
    every button it is given. Copying the ID is added in `profileButtons`, not
    `relationshipButtons`: it is the one thing offered about *anybody*, this
    account included, where the relationship policy answers with nothing. A
    clipboard write is invisible, so both it and a click on the handle report
    through `ProfileActions.OnCopied` → `App.copied`, which names the thing rather
    than quoting it.
    The two dates carry **a mark each and no caption** — "Member since" named only
    one of them, and under a shared heading the two differ only in the word each
    opens with. `profileDetail` is a `NewFillRow`, not an `HBox`: an ellipsis box
    reports no width of its own, so a layout handing every child its minimum draws
    the mark alone.
15. **Custom emoji.** `:26-char-ULID:` in a body is `markdown.Emoji`; the length
    is exact because a colon is ordinary punctuation, and a looser match would turn
    "10:30:00" and every `:shortcode:` nobody serves a picture for into a blank
    square. It renders as a bare `canvas.Image` in a fixed square — no widget, so
    hover and the row's menu pass through as they do an embed's card — loaded from
    the emoji cache. The square is exactly `emojiSide`, one line of the text around
    it: RichText baseline-aligns a row as soon as its objects differ in height and
    reads the baseline of a segment it cannot measure as text as *zero*, so an
    emoji a pixel taller is moved down a whole baseline and draws through the line
    below. It is measured, not memoised through `lineHeight`, because it has to
    agree with that row exactly. Like a mention it can neither break nor be broken
    before, so it feeds `mdBuilder.reserve` too.
    **Picking one is `ui/emoji.go`**, one pop-up serving the composer's button and
    a message's add-reaction alike — both choose from the same set and differ only
    in what they do with the answer, hence `EmojiChoice.Value` (a reaction) and
    `.Token` (a body's `:ID:`). Nothing is fetched and neither emoji event is
    registered here (see the custom-emoji note): `Store.Emojis` is already the
    whole set and already current, so `app.emojiGroups` buckets **one** walk rather
    than asking per server.
    The drawn grid is capped (`emojiPickerLimit`) and the search field reaches past
    it; cells are memoised per emoji, so narrowing a query reorders objects that
    exist rather than rebuilding a hundred widgets and re-asking the cache for a
    hundred pictures per keystroke. Every name is folded **once**, when the picker
    opens (`foldGroups`): a keystroke asks the whole set whether it matches, and
    folding at the comparison would lower thousands of strings per character typed.
    `EmojiChoice.Keywords` is what a character answers to besides its name,
    searched and never drawn — "no" has to reach 👎 without the label over it
    reading "thumbs down no".
    What names a cell is the **header**, the grid being pictures: the hovered
    emoji redrawn at `EmojiPickerPreviewSize` beside its name and the group it
    came from, which is the rendition a 34-unit square cannot show. With nothing
    hovered it stands at what Enter would take, so it is also where a query that
    matched nothing says so — the grid below simply collapses, and `fill` resizes
    the pop-up to what is left (a pop-up takes its size once, as it opens).
    Which group is on screen is the **rail**, one server icon per drawn section
    down the leading edge, marked and jumped like the settings rail
    (`markSectionAt`, `jumpTo`); it is what `app.emojiGroup` fills `IconID` /
    `IconURL` for. A section's offset is read off the laid-out block rather than
    summed from minimums — a wrapping grid answers `MinSize` with one cell. The
    rail is drawn only where there are two groups to move between, decided as the
    picker opens: one appearing as a query narrowed would re-wrap the grid under
    the pointer. It carries the picker's *own* `ui.Tooltip` for the server names —
    the app's is a layer in the window's content, and a pop-up is a canvas overlay
    drawn over all of that. The grid scrolls in an `ObservableVScroll` held off the
    right edge by the indicator's own width, so the bar lands in a gutter rather
    than on the last cell of every row.
16. **Role colours.** A Revolt role colour is a CSS value, and the server's own
    presets are as often a gradient as a triple — hence `client.parseColor` reading
    *every* stop and `domain.Gradient` carrying them. A gradient is a `color.Color`
    answering as the mean of its stops, so a chip's dot, a reply's accent bar and a
    picker row keep filling one shape without knowing. Only `ui.AccentText` spreads
    one: a text object takes a single colour, so a gradient name is one object per
    rune, each measured off the whole name up to it (summing single glyphs drifts a
    fraction of a pixel each).
    **A gradient must never reach a `canvas.Text`.** Fyne keys its glyph-run
    texture cache on the text object's fields, colour included, so a fill that
    can't be a map key panics the painter on the frame it is first drawn — off the
    UI thread, where no recover of ours is on. That is **structural** rather than
    remembered: `ui.newText` / `newBoldText` are how a text object is built here and
    both flatten through `ui.solidColor`, so a call site cannot forget. A shape
    needs nothing, its texture being keyed by the object. `widgets_test.go` still
    asserts it over the built tree, the software painter a render test uses taking
    a different path and not noticing.
17. **Permissions.** `Store.Permissions(channelID)` / `ServerPermissions(serverID)`
    hand back a whole `domain.Permission` bitfield rather than a `CanX` per
    question: a call site asking three things should walk the roles once, and the
    interface would otherwise grow a method per bit Revolt defines. Zero — logged
    out, an unknown ID, a channel with no server — means "allow nothing".
    The arithmetic is `client.channelPermissions` / `serverPermissions`, taking
    plain `*revoltgo.Server`/`Member`/`Channel` values rather than reading `State`:
    that is what makes it testable at all, `State`'s caches being unexported. Order
    is load-bearing — server default, then the member's roles least senior *first*
    so the most senior has the last word, then the channel's default overwrite,
    then the channel's overwrites for those same roles, then the timeout clamp last
    so no overwrite can hand back what a timeout took. A **nil member** resolves as
    one holding no roles, not as no access: that is what Revolt computes for the
    default role and what revoltgo fabricates on `ServerCreate`, and refusing would
    empty the sidebar of a server just joined.
    `ViewChannel` is the one permission answered by **hiding** — `newChannelRow`
    returns nil, so the channel is not a row and (same walk) not a `#mention`
    candidate either, and `selectServer` opens on `firstVisibleChannel`. Only a
    server decides it: `App.canViewChannel` exempts conversations, which are in the
    user's own list because they are in them. `selectChannel` is where the checks
    pay for themselves — a channel it cannot see returns before
    `loadChannelMessages`, and `ReadMessageHistory` gates the page on its own, so
    the request is never sent to be refused.
    `SendMessage` **disables** the composer (`MessageInput.SetPermissions`) and
    `syncComposer` hides the entry row for `ui.ComposerNotice`, which carries the
    reason behind a mark — a disabled `widget.Entry` draws its border in
    `ColorNameDisabled`, which no scoped theme can flatten without taking the
    placeholder with it, so the box read as a stray outline inside the card's own.
    Typed text is kept, and so are the queued replies — `syncComposer` pushes the
    channel itself as well (`MessageInput.SetChannel`), which is what greys the
    cards belonging to another one and keeps them out of the send
    (`RepliesHere`); see `internal/ui/CLAUDE.md`. `UploadFiles` is checked in `AddAttachment`, where a drop
    and a paste both land, and reported through `OnRefused` — nothing else would
    happen, and nothing happening reads as a bug. A drop checks once for the whole
    batch, not per file.
    Nothing caches the answer. The lookups are `State`'s own RWMutex-guarded map
    reads and the questions are asked per channel switch, per hover and once a
    second at worst — while holding a `*revoltgo.ServerMember` would be both a data
    race (the gateway writes `Roles` in place) and a cache to invalidate.
    **The one place that asks in bulk asks once**: `refreshChannelList` wants the
    same question about every channel of a server, and the only per-channel part
    of the answer is two overwrite lookups — the account, the server, the
    membership and the ranking of its roles are shared, and the ranking allocates
    and sorts. So it takes `Store.ServerChannelPermissions(serverID)` and hands it
    to `newChannelRow`, which reads `ViewChannel` out of it (`App.viewable`, nil
    meaning ask the store, which is every other caller). Still not a cache: one
    walk, held no longer than the rebuild.
    `onMemberUpdated` is the one event that can change the answer under a standing
    selection: for **our own** member it rebuilds the channel list and re-syncs the
    composer, a role gained or lost being what makes a channel appear.
18. **Parsing a body.** `markdown.Parse` classifies each line *once* into a
    `lineKind` — paragraph collection stops at anything that is not `lineText`, so
    a predicate per block type would be re-run per line — then hands each block's
    text to `parseInline` **whole**, newlines included. That is not tidiness: a
    Discord span crosses a hard line break, and a scanner given one line at a time
    can never match one. `LineBreak` is what the scanner emits at a `\n`.
    The scanner is a byte loop over an `inlineSpecial` table: an ordinary run costs
    no call and no copy, being emitted as a slice of the source, and
    `inlineScanner.buf` only exists once an escape has to be dropped out of a run.
    Everything else is delimiter matching, in `matchInline`. The **autolink** lives
    in the scanner rather than in it because a bare URL's scheme sits *behind* the
    `://` that announces it: the one construct matched by looking back, bounded by
    the pending run's start so it can't reach into a node already emitted.
    A `<t:1700000000:R>` **timestamp** is `markdown.Timestamp`, matched off `<`
    beside the mentions. Its style is validated against the letters Revolt defines
    rather than taken verbatim: an unknown one has no rendering, and falling back
    to the default would show the wrong face of the right instant where staying
    literal shows what was typed. A miss falls *through* to `matchAngleURL`, `t`
    being a scheme byte too. The style is carried rather than resolved — which face
    to draw is a question about the reader's clock, and this package has no config
    — so `util.MessageTimestamp` decides in `ui/markdown.go` and `PlainText` takes
    the plainest reading of the same instant. It renders through `mdBuilder.mention`
    with a **nil tap**, being a fact the client resolved rather than something the
    author typed; `mentionText.Cursor` keeps the hand off one that leads nowhere.
    A `Blockquote` holds **blocks**, not inlines, so `> # Note` is a heading and a
    quote marker among them nests; `mdBuilder.blockquote` builds them first and
    splices the bar in afterwards, a block's own non-inline break segment being the
    only thing that knows where a row ended. A `List` is one block whatever its
    depth — `ListItem.Indent` moves the marker column, nothing else — and
    `ListItem.Number` counts per depth, the renderer being unable to recover that
    from a flat index.
19. **The member list.** A server holds thousands of members whose presence
    changes continuously, so nothing about it is per-row work on the UI thread.
    `refreshMemberList` runs the `Store.Members` walk **off-thread** — a nickname,
    avatar, presence and role colour per member, then a sort — together with
    `ui.NewMemberModel`, which is pure and reads no theme size for exactly that
    reason. Only installing the result hops back. Two rebuilds can race, so
    `App.memberSeq` drops the older.
    The model is flat and its two entry kinds are one fixed height each, which
    makes a position a prefix sum (`memberOffsets`) and the window two binary
    searches (`visibleRange`). `MemberList` mounts only that window and **recycles**
    its rows: `MemberRow.SetMember` no-ops on unchanged state, so an overlapping
    scroll and a whole-model repaint both cost nothing per row that did not move.
    Keying the mounted map by *entry index* puts the same object back on the same
    entry. Nothing per-row may capture a member — `RowMenu` is one hook on the list
    taking a user ID, and both row callbacks read `w.userID` at the moment of the
    click.
    Ordering is one bucket index per member and no second sort: `Store.Members` has
    already ordered them (tie-broken on user ID so it is total) and bucketing is
    stable. An **offline member never appears in their hoisted role's section** — a
    hoisted section is a list of who is here — and an empty bucket emits no header.
    Presence is the only event that resections, and it is queued **as its own
    target** (`refreshPresence`, item 22) rather than as a rebuild: it moves a
    member between sections and moves nothing they are ordered by, so the
    membership the last walk resolved still stands. `App.memberCache` is that
    membership, published for the server it was walked for and never written
    into; `refreshMemberPresence` copies it, re-resolves only the people named in
    `App.presenceDirty` and hands the copy to the model. The copy is what lets it
    run off the UI thread against no lock at all, and `App.memberWorking` is the
    single flight that keeps two of them from both starting at the previous
    answer — a rebuild landing re-queues whatever arrived while it held the claim
    (`memberRebuilt`). A walk clears `presenceDirty` on the way out, having
    resolved everybody's presence itself. `UserUpdated` still repaints one row in
    place.
    Following presence at all is a setting; so are hoisting, hiding the offline
    half, hiding members with no role, the settling window and the overscan. The
    two hiding settings meet in `MemberListOptions.hides`, asked by both branches
    of the model before anything decides where a member would have gone. Roleless
    is **not** `HoistRoleID == ""` — a member holding only an unhoisted role has
    none — hence `domain.Member.HasRoles`, which counts a role the server has not
    published. Those two settings are also the one thing that can empty the list on
    a server they were never chosen for, and an empty sidebar is indistinguishable
    from a fetch that failed: `FallbackToAll` draws everybody instead, which is why
    `NewMemberModel` wraps `memberModel` rather than being the walk itself. The
    retry is guarded on the first pass having produced *nothing* and on a filter
    having been on, so a server that really is empty stays empty and one nothing
    was hiding is never walked twice.
    **A hidden sidebar skips the model build entirely** (`App.memberStale`, caught
    up by `toggleMemberList`) but never the walk — the mention picker is fed off
    it, including people the list hides.
    **The strip above the list is what speaks when the rows cannot.**
    `ui.MemberListStatus` takes its own height off the top of the column, not in
    place of it and not centred: the list is paint-then-fill, so saying
    "refreshing" must not take away the members already there, and a message in the
    middle would sit among the rows the moment there were any. It is **not** an
    overlay — laid over the rows through `NewLayer` it cut the first avatar and
    name in half, the mounted window being drawn from the column's own origin — so
    `MemberList` holds its `NewFillColumn`, and `SetStatus` re-lays out and
    re-mounts, the strip appearing being a shorter viewport. `App` decides it in
    `memberStatusFor`, a pure function kept apart from the widget so the precedence
    can be tested: a fetch in flight outranks a failure a retry has just cleared,
    and a failure outranks an empty list, "nobody to show here" for a membership
    that never arrived being a claim nothing on screen contradicts.
    `updateMemberStatus` is the only writer, called from every side that can move
    either half — four call sites each setting a message is four chances to leave
    the sidebar loading something that has landed. `memberFetchTimeout` is a
    `const`, not a setting (what the user would be choosing is how long to watch a
    sweeping line before being told nothing came) and it cancels **nothing**:
    revoltgo's REST layer takes no context, so the request is still out and a late
    answer still installs. The mark is `ui.TypingMark` (item 20), so "something is
    happening" is one shape in this client; it is built once with the strip rather
    than per status, and `MemberList.SetSweeping` stops it when the column is
    hidden or the tree holding it is replaced — see the footgun.
20. **Typing indicators.** `client.TypingChanged` is the one event that carries
    its value rather than naming what moved: `revoltgo.State` does not model
    typing, so no store answers who is typing where and the reader keeps it —
    `App.typing` (channel → user → expiry), the same shape as `slowmodeUntil`.
    Every channel is tracked, not only the open one, because the sidebar marks the
    others; nothing outlives `typingLifetime`, so it cannot grow. One `typingTimer`
    is re-armed to the **next expiry across all channels** rather than ticking, the
    line changing only when somebody lapses, and `pruneTyping` reports which
    channels emptied so only those repaint. Revolt sends no stop before a message,
    so `onMessageCreated` forgets its author.
    `typingPhrase` names the people and **nothing else** — the mark beside them
    says they are typing, so the line is `Alice, Bob +2` rather than a sentence
    repeating the mark in the longest form available. A name not resolved yet is
    *counted* rather than named (`hidden` covers both that and everyone past the
    limit, hence `Someone` / `3 people` with nothing to name), and the line redraws
    when `flushAuthors` or `UserUpdated` fills the gap — `onUserUpdated` asking
    `App.typing` first, since account updates arrive continuously and a redraw
    resolves every typist in the channel.
    The **open channel's row is never marked** (`isTypingIn`): its line above the
    composer already names them, and a row that could be marked while it is the
    open one is a row nothing puts back — the mark would sweep there until some
    unrelated sidebar-wide sync cleared it — so `showTyping` sends that channel to
    the line instead. Selecting a channel and leaving one both run that sync.
    Sending re-announces at most once per `typingSendInterval` and takes itself
    back on an empty composer, a submit, a channel switch, or `typingIdleTimeout`
    of quiet. `MessageInput.OnTyping` reports *whether text survived* the
    keystroke, one callback rather than a pair, and rides the typing methods beside
    `syncMentions` for the same reason they do.
    `TypingShowSelf` files this account among the typists from `noteSelfTyping`, a
    **local** echo rather than a reflected event: nothing guarantees Revolt sends
    our own typing back, and the preview is wanted whether or not we are
    announcing — so `typingChannelID` marks where we count as composing either way,
    and a zero `sentTypingAt` says nothing is owed a retraction. Only the first
    keystroke repaints; later ones move the expiry alone, a timer left armed at the
    older one costing a wake that prunes nothing and re-arms itself. We are named
    "You", first and out of the sorted order, and take a slot against the limit
    like anybody else.
    `TypingNames` is the limit **and** the off switch — at zero `onTypingChanged`
    returns on its first statement. It cannot be turned off any earlier: revoltgo
    drops an event before decoding when nothing is registered for its type, but it
    has no `RemoveHandler`, so a live setting has to be read in the handler.
    The mark is `ui.TypingMark`: a capsule sweeping its box once a second with a
    lagged trail. The lag is in **time**, not in space — every segment walks the
    same path, so the trail gathers at each turn and draws out across the middle
    with nothing to clamp at either end. The cosine eases the turns, which is why
    the animation's own curve is linear; an eased one would also stutter at every
    repeat. Only positions move, and `canvas.Rectangle.Move` repaints for itself,
    so nothing here refreshes anything. `typingTrailTint` is **not** `theme.Fade`:
    that scales the alpha of a `color.RGBA` and leaves channels Go defines as
    already multiplied by it, so a faded colour composites *brighter* than its
    source — a tail lighter than the line casting it. The mark is centred against
    the *name*, both being children of one `HBoxNoSpacing` row sized by the label,
    so an avatar or a larger mark moves the pair together.
    **A mark nobody can see must not run.** `ChannelWidget.Hide`/`Show` carry the
    sweep, a collapsed category hiding the row while Fyne's `Visible()` is per
    object rather than per tree; `App.releaseChannelRows` stops the rows a rebuild
    is about to drop, and `restyle` does the same for the line. Every wake of an
    animation asks the canvas to repaint, so one left running against a discarded
    widget is a repaint per frame for nothing — see the footgun.
21. **Pinning.** `Client.PinMessage` writes the cache itself on success rather
    than optimistically, so a refused pin leaves the row as it was and a tap
    answers without waiting for the gateway. `App.OnPin` repaints; the echo that
    follows announces nothing, the update handler reporting no change when the
    flag already holds what it is being told, which is what stops one pin
    redrawing twice.
    `canPin` asks for `ManageMessages` and does **not** fall back to authorship the
    way `canDelete` does: a pin is a change to the channel, not to the message, and
    Revolt refuses your own on that basis. It is offered in the context menu only —
    the hover quick-actions are what is done often enough to be worth a click
    without opening anything, which pinning is not.
    The mark rides the name line, which is why `continuesGroup` refuses a pinned
    message: grouped, it draws no name line and pinning would show nothing.
    `refreshMessage` therefore re-tightens the row *above* as well, that margin
    belonging to the predecessor; the row below needs nothing, its own grouping
    being read off itself.
    **Seeing them together is `pins.go`**, and it is a *request*: a pin is a flag
    on the message and Revolt publishes no collection of them, so
    `Client.PinnedMessages` searches the channel (`ChannelSearch(Pinned: true)`,
    the only route that enumerates them). What comes back stays out of the message
    cache for the reason item 5 keeps a quote out of it, and out of `a.uncached`
    too — a card is a flattened summary, not a mounted message, so nothing here
    resolves a quote. The search carries its own users, so the leftovers are
    resolved in the same worker rather than through `ensureAuthor`'s queue: a
    webhook or somebody departed would otherwise be a raw ID filling in a moment
    later.
    The panel is a **snapshot** — `App.pinned` for as long as it is up — so a pin
    made anywhere while it is open does not reflect. `App.pinsChannelID` is what
    drops an answer that lands after the reader has moved on, and `App.pinsSeq`
    what drops one belonging to a list since replaced: `loadPinned` bumps it, so a
    page still in flight when `reloadPins` re-asks from the top lands on nothing
    rather than appending to the new list. Revolt's hundred is per request —
    `loadMorePins` asks for the hundred past the oldest held — and a pin moving
    throws the paged-in pages away, which pin is the hundredth having moved too. A card leads to its
    message through `OnJumpToMessage` and closes the panel on the way, a jump
    moving the column underneath it; unpinning goes through `App.setPinned` — the
    shared half of `OnPin`, with a hook, since the message a pin is taken off here
    need not be mounted at all — and the card is dropped only once the server has
    agreed. It is the one destructive-looking action not confirmed: repeating it
    puts the pin back.
22. **Events that only name what moved, and the refresh queue.** `ServerUpdated`,
    `RolesChanged`, `ChannelCreated`, `ChannelUpdated` and `ChannelRead` carry an
    ID and nothing else, revoltgo's own default handlers having already put `State`
    right by the time ours run. So a handler's whole job is to decide which surface
    is now wrong and let the rebuild re-read the store.
    **Two coalescings sit on top of each other here and answer different
    questions.** `pumpEvents` batches the *trip*: whatever the gateway has
    already produced goes onto the UI thread in one hop, in arrival order, since
    every hop wakes the driver's loop. The refresh queue below batches the
    *work*, which is what the events in that batch ask for. Neither subsumes the
    other — a burst spread over three settling windows still costs three
    rebuilds, and a burst inside one window still arrives as however many trips
    the pump made.

    Those rebuilds are **queued**, not made: `App.queueRefresh` sets bits in
    `App.dirty` (`refreshServers` | `refreshChannels` | `refreshMembers` |
    `refreshEmojis` | `refreshPresence` | `refreshFriends`) and arms one timer; `flushRefresh` runs
    each at most once, outermost column first. `refreshPresence` is skipped when
    `refreshMembers` ran in the same flush — the walk answered it on the way
    past — the same way `refreshEmojis` is skipped behind `refreshServers`. The
    window is armed by the *first* event of a burst and deliberately **not**
    restarted by the ones behind it — presence on a large server arrives faster
    than any window worth having, so a renewing one would never elapse. It is one
    knob (`RefreshDelayMS`), not one per surface: what the user is choosing is how
    long a burst may gather, and Revolt's bursts do not respect the boundary anyway
    — a rank reorder is an event per role, a channel added to a server is a create
    *and* a server update, and both would otherwise rebuild a sidebar twice for one
    change. Anything that changes only what is **open** — a header's text, the
    channel glyph, whether the composer takes a message — stays immediate: it is a
    setter and a permission lookup, and deferring it would make the client feel
    slow to save nothing. Same for a selection pointing at a channel that has just
    stopped existing, which must not survive a settling window.
    The three role events collapse onto one `RolesChanged` because a colour, a rank
    and a deletion all cost the same walk of the membership; creating a role
    arrives as an update for one `State` has never heard of, which revoltgo files
    on the way past, so there is no create to handle. `ChannelUpdated` rebuilds the
    whole channel sidebar rather than the one row: it announces permission
    overwrites too, and whether a channel is a row at all is `ViewChannel`'s to
    decide, which repainting in place cannot express. `ChannelRead` is the only
    event that exists because of *another client*; our own acks echo through it
    onto a mark already cleared.
    `VoiceChanged` is the same shape for a call, and the same rebuild: who is in a
    voice channel is drawn as rows under it, so `App.callRows` builds them with
    that channel's row and hands them to the category **with** it, a collapse
    otherwise leaving participants hanging under nothing. The handler is guarded on
    `App.drawsCall` — the open server, and not the home view — because voice events
    arrive for every server the account is in and a call elsewhere draws nothing.
    Joining and leaving are what that rebuild is right for; **speaking is not**,
    and does not come through here at all — see item 36.
    Two leaves are announced to everyone **except** as a deletion to the one who
    left, so both are recognised by their own user ID: `MembersChanged` for our own
    member *is* `ServerLeft` (revoltgo evicts the server from `State` on the
    strength of it), and `RecipientsChanged` for our own is `ChannelClosed`. Both
    are handed to the existing path, a no-op for something already gone.
    `RecipientsChanged` is a group conversation's own membership — the one channel
    whose participants are a list the client reads, being the `@mention` pool there
    — and `EventChannelGroupLeave` *embeds* the join event, the third pair in
    `client/events.go` that must therefore be registered twice.
    `UserRemoved` is an account taken off the platform; revoltgo has already
    dropped the user, their conversations and every membership, so the handler
    prunes the app's *own* order (`dmChannels`) of IDs the store no longer answers
    for and clears the selection if it was one of them.
23. **This account.** Presence and the status line beside it are **one object** to
    Revolt and it takes the whole of it, so whichever half is not being changed has
    to be read back out of `State` and sent again unchanged — either setter
    omitting the other's half would silently destroy it. `Client.editStatus` is
    that read-and-resend, shared by `SetPresence` and `SetStatusText`. Clearing the
    line is the one change that cannot be expressed as a value: an empty `Text` is
    *omitted* from the request, so it goes as `Remove: ["StatusText"]`. Length is
    clamped by rune to `MaxStatusText` rather than refused — the limit is Revolt's,
    and a failed send is worse than as much of it as fits. Nothing is recorded
    locally; the change returns as an ordinary `EventUserUpdate`, which is also
    what makes a presence set from another client arrive — so neither row writes
    back to its control, and `ui.commitEntry` reports on Enter and on blur rather
    than per keystroke, every report being a request. The picker offers
    **Invisible** where the domain says `PresenceOffline`: `toPresence` resolves
    Revolt's invisible *to* offline on the way in, so the two names are the same
    state seen from either side, and `client.fromPresence` is the bridge.
    **The display name** is the third row and the same `UserEdit` route, but it is
    a single field applied as a partial, so `SetDisplayName` sends it alone with no
    read-and-resend; blank clears it through `Remove: ["DisplayName"]`. The field
    holds `domain.User.DisplayName`, the raw name, *not* `Name` — Name has already
    fallen back to the username, and a field pre-filled with that would send the
    username back as a chosen name at the first blur. Revolt's bounds are 2–32, and
    `client.cleanDisplayName` drops the characters its pattern forbids and cuts a
    long name the way a status line is cut; a one-character name is the only thing
    it refuses, and `App.setDisplayName` names that limit in a warning rather than
    letting `notifyFailure` say "could not" about a request never made.
    **The picture, the banner, the description and the username** are the rest of
    it, and only the first is an ordinary edit: `SetAvatar` uploads into the
    *avatars* bucket (see the revoltgo note) and the new picture arrives as a user
    update like anybody else's. The **profile** half arrives as nothing at all — it
    is not on the user record and no event announces it — so `App.selfProfile` is a
    snapshot `loadSelfProfile` fetches once per session, dropped after each edit
    and re-read through `App.editProfile`, which is also what tells the Banner row
    it now has something to remove. `ui.SettingsPage.SetProfile` fills those two
    rows the way a profile card is filled, and `commitEntry.Fill` refuses to
    overwrite a field somebody has already typed in.
    The **username** is a `ui.PromptDialog` on the modal layer, not a row: Revolt
    takes the account password with the new name, which is two answers at once and
    one of them not something to leave sitting on a page that stays open. The card
    is the same one that creates a server, now a field per answer.
    `refreshSettingsAccount` is what rebuilds the section afterwards, and it
    compares before it does: a picture or a handle that moved is a rebuild, a
    display name is not, that field being on the section and Enter leaving the
    cursor in it.
    `Client.revoke` is the shape both logouts share — drop the session, then spend
    the captured one on the request that invalidates it. `logOutEverywhere`
    additionally removes this computer's saved login, which plain logout keeps: the
    token in it is one of the ones just revoked, so its card could only offer a
    sign-in that fails.
24. **Creating an invite.** Offered on a channel row, not a server icon — Revolt
    has no server-wide invite, only one per channel that lands the joiner in it —
    and gated on `InviteOthers`. The link goes on the clipboard and the notice is
    the receipt: a clipboard write is invisible, and this is the one action whose
    entire result is a string the user must now paste somewhere. `util.InviteLink`
    composes it and is deliberately **not** the inverse of `InviteLinkCode`, which
    must keep reading every host Revolt has ever served invites from because that
    is what other people's messages contain. Hence `inviteLinkHost` named apart
    from `inviteShortHosts`: adding a host to the reader's list must not silently
    change what the writer emits.
25. **Reactions.** Revolt sends them as a JSON *object*, so revoltgo hands over a
    `map[string][]string` with no order in the payload at all; `client.toReactions`
    sorts by the emoji, the one order that survives a count changing — anything
    derived from the count would move the chip beside the one somebody just joined
    out from under the pointer. The people are carried rather than a count
    (`domain.Reaction.Users`) because a chip is drawn differently for the account
    that is in it, and `By` answers that where a conversion folding in a self ID
    could not.
    `Client.React` writes the cache once the server agrees, exactly as `PinMessage`
    does and for a related reason: the gateway does echo a reaction back, but a
    chip the user just clicked has to answer now, so `applyReaction` reports
    "nothing moved" for the echo and the round trip costs one repaint. Everything
    reachable from a cached message is **replaced** on the way — the message, its
    reaction slice and the user list inside it — all three being read on the UI
    thread without the cache lock. `EventMessageUnreact` *embeds* `EventMessageReact`
    rather than aliasing it, so both are registered, as the typing pair is;
    `EventMessageRemoveReaction` is one emoji taken off wholesale.
    The row is drawn only for a message that carries one, which keeps a mounted
    page free of permission checks: chips have to know whether they answer a click,
    and asking that per message would be a lookup per row for something few of them
    have. Adding the *first* reaction is offered from the hover actions and the
    context menu instead, both of which read permissions lazily already. A chip
    declares hover for itself and therefore takes it from the row — innermost wins
    — so it reports back through `MessageWidget.overChild`, the hook the
    quick-action group uses; without it the buttons would vanish as the pointer
    crossed a chip on the way to them.
    **Clearing every reaction** (`OnClearReactions`) writes the cache and repaints
    from here for a harder version of the same reason: Revolt announces a clear as
    a message *update* carrying an empty map, indistinguishable from an edit, so
    nothing arrives to be believed. It is the one reaction action that is confirmed
    — it undoes other people's clicks — and the menu offers it only on a message
    that has some, an item that can never do anything being worse than no item.
    What it opens is the shared picker, through `Actions.OnPickEmoji` rather than
    directly: what is on offer is a walk of every server the account is in, which no
    widget knows. `ui.UnicodeEmoji` is the dozen characters offered under the
    servers' own — the ones that work in a conversation, where a custom emoji is
    still pickable but no server heading names it. A **custom** emoji renders in a
    chip either way: the ID is all a reaction carries, and `util.IsEmojiID` — exact
    ULID length, nothing looser — decides picture from character, a length range
    being enough to read a two-letter flag as an ID.
26. **Relationships.** `domain.User.Relationship` is how this account stands with
    somebody, and `Client.relations` is what keeps it true. Ready fills
    `revoltgo.User.Relationship` for everybody it names and **nothing keeps it
    current after that** — see the `EventUserRelationship` note. `relations` is the
    overlay that answers instead: read first, falling back to `State`, written by
    the gateway handler and by each action once the server has agreed, cleared with
    the session.
    `AddFriend` goes round revoltgo's typed API, it being a missing *route* rather
    than a missing field: Revolt takes a **sent** request at `POST /users/friend`
    naming the person by handle, while `PUT /users/{id}/friend` — revoltgo's
    `FriendAdd`, here `AcceptFriend` — accepts one that has already arrived. The
    two are not interchangeable and the wrong one of a stranger is a refusal with
    nothing to say why; the handle comes out of `State`, the caller having only an
    ID.
    `RemoveFriend` covers unfriending, declining and withdrawing alike, Revolt
    spending one route on all three — what it means is decided by where the
    relationship stood, which the button that raised it has already read to label
    itself. `blocked` is gone from `client/store.go`: `conversationPermissions`
    takes a `domain.Relationship` rather than a `*revoltgo.User`, which both routes
    it through the overlay and lets it be tested without one.
    `RelationshipChanged` reaches two surfaces. A block is what takes the composer
    away in an open DM (`Relationship.Blocked`, either direction); the other is the
    friends list. Nothing else draws a relationship — a member row says nothing
    about one, and a profile does not refresh while it is up.
27. **The friends list.** `Store.Relationships` is the only place relationships
    are seen as a set. It is a *walk*, not a lookup: Revolt files each relationship
    on the account it is with and sends no collection, so the set exists only as a
    property of the people in it — hence walking `State.Users()` and asking the
    relationship **before** resolving the account, most cached users being a member
    of some server and nothing more. Ordering is `Members`' — folded name,
    tie-broken on ID, so a row cannot swap out from under the pointer about to
    answer it.
    It is a **view, not an overlay** (`ui.FriendsPage`), opened from
    `ui.FriendsRow` above the conversations in the home sidebar, a relationship
    being a fact about somebody rather than about a server. It is stacked in the
    message area's own slot with `App.messageColumn` hidden under it
    (`buildMessageArea`), so the two are one view apiece and `App.friendsOpen` says
    which: `showFriendsPage` clears the channel selection first — the page stands
    where its messages would — and `clearChannelSelection` calls
    `leaveFriendsPage`, which covers every path ending in no channel at all;
    `selectChannel` calls it itself. Its own no-op guard cannot swallow the page:
    the selection was cleared on the way in, so no channel is still the current one.
    `restyle` hands back a fresh tree with the page hidden, so `restoreFriendsPage`
    puts it back from the flag that outlived the rebuild.
    The row is rebuilt with the sidebar — those objects are replaced wholesale —
    and draws selection and a pending mark against each other exactly as a channel
    row draws selection against unread (`SetState`), an incoming request being the
    one part of the list that arrives unasked.
    With the page **down**, that mark is all `refreshFriends` is for, and it
    takes `awaitingAnswer` rather than the sections: `flushAuthors` calls it once
    per batch of resolved authors, and building four sections of rows and their
    buttons to ask whether one of them is empty is the whole list's cost for one
    boolean.
    The page **refills in place** (`SetSections`) rather than closing: accepting
    a request is an action whose entire result is the list changing, and every
    other answer is still up. That is what `App.relationshipButtons` is for — the
    profile card's own policy with the way out left open, so the two surfaces
    cannot come to offer different things about one person. A button drawn disabled
    is dropped here, "Request sent" being what the heading above it already says.
    Each button also carries a `ui.ProfileAction`, which is what lets the page draw
    a **mark** where the card draws a word: the controller names the kind of action
    and `ui.friendMark` picks the glyph and the tint, the way the composer names a
    kind of keystroke and `app` decides what it sounds like.
    Every heading **folds**, and which sections are shut is the page's own state —
    what the controller decides is only which one *starts* shut (`Folded`, on
    Blocked). See `internal/ui/CLAUDE.md`.
    That name is also how `friendEntry` lifts **Message** out of the buttons
    entirely and hands it over as `FriendEntry.Open`, the card's own tap: writing to
    somebody is the one thing done from a friends list often, and a target for it a
    square from Block is a hand aiming at the wrong one. Most rows have no such
    action — Revolt opens a conversation only between friends — and their card falls
    back to the profile, which the picture leading every card opens in any case.
    Presence is drawn on every row, so `onPresenceChanged` queues `refreshFriends`
    **before** the member sidebar's own gate — this page is the one surface open
    while no server is, which is the first thing that gate drops, and its people are
    the reader's own rather than a thousand strangers.
    Somebody the gateway names that `State` has never cached has no name to draw —
    `EventUserRelationship` carries the account and nothing files it — so
    `friendsChanged` queues them through `ensureAuthor`, and `flushAuthors` refills.
    The same hole exists at `Ready` and is filled the same way: `Ready.RelatedIDs`
    is the whole graph off the account's own `relations` array, and
    `resolveRelated` queues whoever `Store.HasUser` cannot answer for — otherwise
    somebody befriended long ago and not spoken to since is a relationship with no
    account behind it, and this list is a walk of the accounts.
    The page is **filtered** by name or handle from the field in its header, which
    is built once in the constructor: the list is replaced wholesale on every
    refill and presence alone refills it, so a field inside it would lose what was
    being typed. A filter also unfolds what it matched — a hit inside a shut
    section is a hit nobody can see.
28. **Jumping to a message.** Tapping a quoted line is `Actions.OnJumpToMessage` →
    `App.OnJumpToMessage`, three answers cheapest first: already in the window
    (`revealMessage`), in the channel's cached tail (`jumpWithinCache`), or a
    request for the page around it (`loadJumpWindow` → `Client.MessagesAround`,
    Revolt's `nearby`). Only the **open** channel is asked about — a reply names a
    message in the channel it was written in, so the two are the same by
    construction. A line that resolved to nothing leads nowhere: everything a
    mounted reply names has been asked for by the time it is drawn, so one still
    unresolved was deleted.
    An island card goes through `App.jumpToMessageIn` instead, which opens the
    channel first (`openChannel`, the walk `OnChannelTapped` is now one line of)
    and then asks the same three. The inbox is why: its cards come from as many
    servers as the account is in. Entering a channel that way puts *two* requests
    out — that channel's first page and the window around the message — so
    `loadChannelMessages` drops its answer when `App.jumped` is already set, the
    window being what the reader asked for. Every caller shows a status line
    first, which clears the flag, so a set one can only be a jump made since.
    None of the three page routes writes to the message cache, for the reason item
    5 keeps a quote out of it, and what comes back is held in the same
    `App.uncached` — which is what lets a quote *inside* a jump window resolve
    without a request of its own. That is also why an update to a message on such
    a page cannot be read anywhere: every handler writes to the cache, so
    `refreshMessage` finds nothing and hands off to `refetchMounted`, one
    `ResolveMessages` for that message, single-flighted through `App.refetching`
    so an edit and the reaction after it are one request rather than two. So the window is not in the cache, and
    `loadMoreHistory` and `mountNewerFromCache` read that off the cache rather than
    off a flag: the top or bottom mounted message not being in it means
    `MessagesBefore`/`MessagesAfter`, no flag needed, and deep scrollback past the
    cache's own per-channel cap is the *same* situation reached another way (it
    used to prepend a non-contiguous page into the cache). `MessagesAfter` sorts
    oldest-first deliberately; Revolt's default with an anchor hands back the live
    tail instead. Nothing re-attaches explicitly: a newer page reaching into the
    cached tail leaves the bottom row inside it and the cache tier serves every
    scroll after that, which is also where `setJumped(false)` is reached.
    `App.jumped` exists only to offer the way back (`backToPresent`), and `atOldest`
    is what the cache's own depleted flag is for a window that is not in it. That
    way back is `ui.JumpBar`, a bar in the composer dock between the badge row and
    the card, and it is *not* driven by `jumped` alone: `App.viewingOlder` is that
    flag **or** a scroll offset further than `atBottomTolerance` from the end — the
    same tolerance an append reads, so the two cannot disagree about where the
    reader is. Everything that can move that answer calls `syncJumpBar` (the
    scroll, `setJumped`, `appendMessage`, `scrollToBottom`, `showStatusMark`);
    `JumpBar.Set` guards on a change and `App.resizeDock` re-hangs the dock, the
    bar's height being part of `ui.DockReserve`. Tapping it is `backToPresent`:
    `returnToPresent` out of a jump window, the cheaper `jumpToLatest` out of plain
    scrollback.
    `MessageList.Reveal` centres the row — twice, the rows around it being measured
    by the first placing — and `MessageWidget.Flash` marks it; see the ui note on
    why a wash is an animation and not state. Hovering stops it, the pointer
    arriving being the reader having found the row.
29. **Channel search** shares the pins panel's route (`Client.SearchMessages`, the
    two being mutually exclusive on the wire — Revolt refuses `query` and `pinned`
    together), its refusal to cache what comes back and its author resolution in
    the fetching worker. All three draw the same `ui.MessageCard` on the same island
    (`App.messageCard`), each dropping what its own subject already said — pins
    the pinned badge, the inbox the mention edge. What is search's alone is the
    field, the run of filter chips and the three orders.
    It asks for **nothing** until Enter: a search is a request per query, so the
    field reports on submit rather than on every keystroke, and `App.searchQuery`
    is what drops the answer to a superseded one — a second Enter while the first
    is still out is the ordinary case, and the two can land in either order.
    The island reports **every** change through one hook (`App.onSearchChanged`)
    and decides nothing: which of them costs a request is answered here, because
    the answer is held here. `App.searchFound` is the messages the last request
    returned, kept while the island is up, and `ui.SearchQuery.SameRequest` is what
    tells the narrowing done *here* from the four things the route is actually
    asked with — the query, the order and the two ends of the span — so toggling a
    filter or picking a person re-runs `drawSearchResults` over what is already in
    hand and changing the order or a date asks again. Something *has to differ*
    for that fast path (`narrowedApart`), which is what keeps a second Enter on an
    unchanged query a real request rather than a redraw. `App.searchAnswered`
    distinguishes "nothing came back" from "nothing has been asked", which an
    empty slice cannot, and is what makes a chip toggled mid-flight do nothing:
    the pending answer is drawn through the narrowing standing when it lands.
    Every filter but the span is a property of the message the route cannot be
    asked about (`matchesSearch`), so the count line carries both numbers — see
    the known gap. The **author** is one of those, which is the whole reason the
    picker is honest only about the hundred: `loadSearchAuthors` fills it from the
    membership `refreshMemberList` last walked, or walks one off the UI thread
    where the sidebar has not — the same slice, published and never written into.
    The **span** is the exception and is sent, so `withinSpan` re-checks it
    locally rather than trusting a field nothing in this repo has verified the
    backend reads on this route.
    The hundred is per *request*, not per island: `loadMoreSearch` asks again
    beginning past the last result held and `appendUnseen` grows `searchFound`, so
    both numbers on the count line climb. `drawSearchResults` is the **single
    writer** of the button — every path that can move whether there is a next page
    ends there, so it cannot be left saying something the state has stopped
    agreeing with. `App.searchSeq` is what kills a page still in flight when the
    query is re-asked: `SameRequest` cannot tell the same query asked twice apart,
    and a page of the answer just thrown away would append to the new one.
    Relevance offers no next page at all — see the client note on `pageFrom`.
    `refillSearch` wraps every change to the island in a `repositionOverlay`,
    unlike the pins panel: replacing cards with "Searching..." is a change of
    height, and a centred card sized from its own minimum re-places for neither.
30. **Creating a server** is `ui.PromptDialog` — one field, because a name is all
    Revolt takes at creation — and it *replaces* the join dialog rather than
    stacking on it, the modal layer holding one card. The response is ignored (see
    the revoltgo note), so the created server arrives exactly as a joined one does:
    `pendingJoin` marks the request and `onServerJoined` selects what the gateway
    brings.
31. **What a reaction chip says on hover** is who is in it, `domain.Reaction.Users`
    having carried the names all along with nothing drawing them. It is folded at
    the moment of the hover (`ui.reactorNames`), not at construction: a chip draws
    a count, a mounted page carries hundreds of them, and nobody is over more than
    one. The line is the typing indicator's — names, then `+n` for the rest — since
    somebody the store cannot name is still one of the people in it, and a row of
    raw IDs answers nothing. The label is the app's own `ui.Tooltip`, reached
    through `Deps.Tooltip` rather than through an action: where a label goes is a
    question about the widget being hovered. `clearMessages` takes it down, as
    `refreshServerList` does — a chip about to be dropped never reports the pointer
    leaving.
32. **Alerts.** Revolt's own push is unreachable — `/push/subscribe` takes a Web
    Push subscription, which needs a browser's service worker and a push service to
    deliver through — and it would answer nothing here anyway: this client holds an
    open gateway, so a ping has already arrived. What `alerts.go` adds is the
    *noticing*: a sound, and the taskbar button flashing (`ui.FlashTaskbar`,
    Windows-only — see the known gap on why there is no toast).
    `soundCatalogue` is the one table binding a sound to the flag that turns it on
    and the copy the settings page lists it under. `playSound` is where **every**
    decision about audibility lives — the master switch, the sound's own flag, the
    focus gate, the volume — so a handler says what happened and nothing more.
    Only what somebody *else* caused is focus-gated (`soundEntry.gated`): a send, a
    refusal and a lost connection are answers to the user's own action, and they
    happen while the window is in front by definition, so gating them would be
    gating them off.
    What a message is worth is `messageAlert`, ordered by how much it is *about*
    the reader — named, then the conversation it arrived in, then whether it is the
    channel on screen. Our own messages are not announced: `audio.Send` plays in
    `handleSubmit`, at the keystroke, rather than on the echo, where it would land
    on whatever the user had moved to.
    `noticeMessage` is the third thing a message can be worth: the message *itself*
    on the notice layer, the sender's face and the line they wrote, tapping through
    to it (`jumpToMessageIn`). Only the two kinds addressed to the reader, each on
    its own switch (`ShowMention` / `ShowDirect`) — anything wider is a card in the
    corner for every message in every server — and never for the channel on screen,
    which is showing it. The notice is `Unfiltered`: the tone switches name which
    *outcomes* are worth reporting, and this is not an outcome of anything the
    reader did. `noticeHeading` names the sender, plus where they said it anywhere
    the name alone would not say which conversation it belongs to.
    Three events needed something they did not carry. A **reaction** arrives as
    `MessageUpdated`, indistinguishable from an edit once the cache is written —
    hence `MessageUpdated.ReactedBy`, the one field on it the reader could not work
    out afterwards. **Reconnection** has no event at all: revoltgo emits only a
    fatal drop, so `App.reconnected` is what makes the *next* Ready a reconnect
    rather than a launch, the first Ready of a run being the client starting up
    while the user watches. And a **keystroke** is `ui.Keystroke`, named by the
    composer rather than sounded there — `ui` does not import `audio`.
    `App.focused` is the one field on `App` that is not UI-thread confined: Fyne's
    foreground hooks fire from the driver's own goroutine, hence `atomic.Bool`.
    Those hooks also re-apply the frame rate (`applyFrameRate`), and Fyne keeps
    one callback per hook — anything else following focus joins those closures.
    The engine is built in `New`, not `startAlerts` — a nil one is a panic on the
    first notice — and holds no audio device until something is actually played, so
    a client with sounds off never takes one. Loading is a worker (`loadSounds`): a
    custom file is decoded there, and the built-ins are *synthesised*, which is not
    free either. A file that cannot be read falls back to its built-in and says so,
    a client that quietly stops pinging being one the user believes is broken.
    The typing **board** is the one sound setting not read where it is played: a
    keystroke is synthesised once and installed, so `updateSettings` compares it
    across the change and `applyTypingProfile` builds all four again on a worker.
    Only the four, and only those still on a built-in — a file somebody pointed a
    key at outranks a board they picked. `SetTypingProfile` is named *before*
    `loadSounds` in `startAlerts`, or the first four clicks are the default board's
    and are replaced a moment later.
33. **Mentions as a set.** `App.mentions` is channel → message IDs, oldest first,
    and `mentions.go` is both what maintains it and the inbox that reads it back.
    It is filled wholesale from Ready (`client.Ready.MentionIDs`, off the account's
    unread markers — see the client note) and added to by `onMessageCreated`, which
    reads `Message.Mentions` rather than `MentionsUser`: the broader test counts
    `@everyone`, which Revolt stores against nobody, so a client counting it would
    disagree with itself at the next reconnect.
    Clearing is the **ack**, not a separate act — opening a channel, marking it
    read and marking a server read each already acknowledge, which is what prunes
    Revolt's own copy. `onChannelRead` prunes rather than deletes, the ack pulling
    mentions only as far as the message named. Item 22's rule holds: the mark is
    immediate, nothing is queued.
    **The other way one goes is that it stopped existing**, and every path that
    can leave one behind forgets it, or the inbox is lit for a message it can
    never list: a deleted message (`removeMessages`, for *any* channel — the one
    nobody is looking at is the one that would go on counting), a server left
    (`forgetLeftServer`), a conversation taken off the platform
    (`onUserRemoved`), and the inbox itself, which drops what the server answered
    404/403 for (`forgetGone`, off `ResolveMessages`' `gone` — a request that
    merely failed leaves the set alone and the panel says so instead). Losing one
    locally is safe in a way keeping a dead one is not: `App.mentions` is a copy
    of the account's own record and the next Ready brings it back, nothing here
    acknowledging anything.
    Three surfaces read it. A **channel row** takes the count
    (`ChannelWidget.SetState`), which recolours the marker slot the unread bar
    already owns — a mention is unread by definition, so the two can only ever be
    one bar saying which — and hangs the count at the trailing end past the typing
    mark. A **server icon** and the **home button** take a boolean into the same
    marker their selection uses, selection outranking it: the view is open, so the
    channel sidebar is already carrying the row the rail would be pointing at.
    `syncMentionMarks` is the one writer of all three, the two fixed buttons
    sitting outside the icon list's own walk. Both are mounted **bare** in the
    rail, not in a `container.NewCenter`: the marker is pinned to whatever width
    the widget is given, and centred that width is the icon's, which puts the bar
    under the circle instead of on the rail's edge.
    The **inbox** is the third, and the only one that costs anything: the client
    holds IDs, so every row is a `Client.ResolveMessages` fetch — bounded by
    `inboxLimit`, newest first, with `loadMoreMentions` resolving the next
    `inboxLimit` on demand. It is the one panel paging a set held *here* rather
    than a route's own ceiling, and it pages by **ID** rather than by an offset
    into `mentionTargets`' sorted list: that list moves under the panel — a card
    dismissed drops one, an arriving mention adds one at the other end — and an
    index into a shifted list is a page with a hole or a repeat in it. Only the new
    refs are resolved; what is already drawn costs nothing to keep.
    `App.inboxSeq` drops a fill for a panel already
    closed and reopened; `App.mentioned` is what came back, kept while the panel
    is up exactly as `App.pinned` is, so dismissing one card redraws the rest
    without asking again.
    Its cards arrive **gathered by server** (`ui.MentionGroup`, one line apiece
    counting what follows), each group placed the first time one of its channels
    appears — so the groups are ordered by their newest mention as the cards
    inside them are. A card then addresses only its channel: the group's line said
    the server, and a DM says nothing at all, its author being the person it is
    with. `Dismiss` replaces the jump mark on those cards rather than sitting
    beside it — the card is already the way to the message — and drops the mention
    from `App.mentions` and the panel both.
    Nothing is **sent**: Revolt clears the array on an ack and on nothing else, and
    acking would mark everything before the message read as well. So the ID goes
    into `App.dismissedMentions`, which `keepDismissed` filters every later `Ready`
    through — otherwise re-reading the account's whole read state is exactly
    what would hand a dismissed mention back — and into
    `config.State.DismissedMentions`, keyed to the account, which
    `restoreDismissedMentions` merges back at the *next* `Ready`. Merged rather
    than assigned: a reconnect must not narrow the set to whatever the debounced
    write had reached. Bounded at `config.MaxDismissedMentions`, oldest first,
    since opening the channel is what drops one for real. See the known gap.
34. **A server's settings.** `ui.ServerSettingsPage` is the client's own settings
    shell (item 13) filled with one server: Overview, Channels, Invites, Bans. It
    is the **second** layer in the content stack beside `settings.Layer`, and only
    ever one of the two is up — `openServerSettings` closes the other, the cog
    being on a sidebar the client's own page covers. One page for the process, as
    `settings` is: its hooks read `App.serverSettingsID` rather than closing over a
    server, so opening it on another is a field and a rebuild.
    `syncServerCog` is what decides the cog exists at all — `manageable`, any of
    `ManageChannel | ManageServer | BanMembers` — and it is called from both
    selection paths *and* from `flushRefresh`, a role change being the one thing
    that moves the answer without the selection moving. It is also where a
    permission lost under an open page closes it.
    Which *sections* the rail lists is the same question one level down
    (`permissionFor`): Overview and Channels ask for nothing, being a reading of
    the sidebar, so the Channels section is listed for anybody and only its "New
    channel" row is gated. A section the account cannot reach is left off rather
    than drawn empty.
    **Nothing on the page has a gateway event except a created channel and the two
    Overview fields.** Revolt announces no unban, no revoked invite and no invite
    created anywhere, so Bans and Invites go through **`ui.cachedList`** — held,
    single-flight, short TTL; the root `CLAUDE.md` states the convention and this
    is where it is used. `ServerSettingsPage.visit` is its opening counter, bumped
    by `Open` and `Close`, and it guards *recording*: the page can be closed and
    reopened on a different server before an answer lands, which `IsOpen` alone
    cannot tell apart. `redraw` guards the *drawing* separately and refills
    `p.listBody` — whatever is mounted now — because a section re-mounted
    mid-flight has a different card and single-flight means no second request is
    coming to fill it. The refill is in place (`settingsShell.refill`), not a
    remount, which would put the reader back at the top. `RefreshFromStore` can
    therefore rebuild *any* section: a fetched one redraws from what is held and
    costs no request, so a burst of channel events repaints the rail's name instead
    of re-asking per event. `resetSessionState` closes the page, which stops a
    claim sticking: a fetch whose callback `App.stale` swallows would otherwise
    leave its section claiming to be fetching for as long as the page stayed open.
    Overview's name, description, icon and banner are all `ServerEdit`, gated on
    `ManageServer` — the two fields read-only without it, the two pictures left
    out entirely. A picture is an upload first (`uploadFile` into `icons` /
    `banners`, the bucket being half of what identifies a file) and the ID second.
    `domain.Server.BannerURL` exists for the row that offers to remove one;
    nothing in this client draws a server banner. Neither writes back what it sent — the edit returns as a
    `ServerUpdate` and the store answers for it, exactly as this account's own
    display name does, so a rename the server refused is never left on screen.
    **The two things that add to a server are reachable from the sidebar too**:
    `channelSpaceMenu` is what the empty part of the channel column offers on a
    right-click, and it raises the same `promptCreateChannel` /
    `promptCreateCategory`. Both therefore take a server rather than reading
    `serverSettingsID` — no page is open behind that menu — and check
    `ManageChannel` on it themselves. The home view answers with no items at all,
    a conversation not being made this way, which `ui.ShowContextMenu` reads as no
    menu.
    Creating a channel is `ui.NewChannelCreateDialog`, the edit card asked from
    empty with one row it cannot have — the kind, which Revolt takes only at
    creation. On success the page comes *down* and the new channel is selected: the
    sidebar is the point of having made one and it is behind the page. The channel
    itself arrives as `ChannelCreated`, already handled; the response is spent on
    the ID alone.
    **An invite is four facts and Invites draws all of them**: the code, the channel
    it opens and who made it, the fourth being the server the page is already about.
    There is no expiry, no use count and no date, and a code is not a ULID, so
    nothing can say when one was made or how long it has left. Most creators are
    accounts this client has never drawn — an invite outlives the reason anybody
    looked at one — so `loadServerInvites` batches the unknown ones through
    `ResolveAuthors` on its own worker before building the rows, and prefers the
    membership for the name, picture **and role colour**. The row names one with
    `ui.NewUserChip` — a role chip's shape with an avatar where the dot goes, in
    that colour rather than the mention colour, a person and a role being the two
    things this client draws as chips. It is its own anchor for `OnUserTapped`: the
    profile card mounts on the modal layer, which is above this page.
    Invites are the one list here drawn as **a card per entry, two to a row**
    (`newIsland` + `pairedRows`, each cell built at `halfCardWidth`) rather than as
    rows sharing one card: an invite is a thing handed out and revoked on its own,
    and a code and two buttons do not need half the page. The section itself is a
    `bareGroupOf` — one card behind them all would be a surface saying they belong
    together — so it has no rectangle to wash and a jump to it does not flash. What
    stands between two rows is then a gap rather than a hairline (`spaceRows`, on
    `settingsShell.islands`, since a late answer refills a body it did not build).
    Inside a cell the code sits in `entryColumn` with the channel beside it and the
    chip on the line below, so code, channel and chip each line up down both
    columns; `SettingsPairGutter` and `SettingsIslandGap` are equal, and both beat
    the padding inside a card, or the grid reads as rows again.
    **Roles** is the one section drawn from the store rather than fetched — Ready
    carries every role and the three role events keep them current — so it has no
    `cachedList` and nothing to expire. It *drills*:
    `ServerSettingsPage.roleID` is the role being edited, "" the list, and the rail
    entry going back to the list is why a tap on it clears the field. The editor is
    rebuilt from the store on every role event, this account's own edits included,
    which is what draws what took — but the permission grid must not be: a second
    change made inside the settling window would compute from the role the row was
    built with and send the first change's absence, hence `roleAllow` / `roleDeny`
    held on the page and re-seeded per build. A created role can only be opened once
    the event carrying it lands (`App.pendingRoleID`, `OpenRole`), the store being
    what the section draws from — the same shape `pendingJoin` has. Everything the
    section offers is gated on rank as well as on a permission, since Revolt refuses
    a role at or above the actor's own: `ServerRoleEntry.Editable` carries that, and
    `moveRole` additionally checks the neighbour being swapped past. The permissions
    are **two** and the section is listed for either — `ManageRole` for the name,
    colour, hoist, order and delete, `ManagePermissions` for the grid — and the grid
    is gated a third time, per bit, because `permissions_set.rs` refuses to grant
    what the actor does not hold. What cannot be changed is drawn as the state in
    words rather than left out, so the editor keeps its shape for everybody.
    **The default role is drawn there too and is not a role**: it is
    `Server.DefaultPermissions`, a plain value set through a route of its own and
    announced as a *server* update, so `serverSettingsRoles` appends it last under a
    reserved ID, nothing outranks it, and its editor is switches rather than the
    three-state control. It is what a role's "Inherit" defers to, which is why it
    had to be reachable at all.
    **A channel's own overrides are the same grid one scope down**, and the page's
    only *second* level: Channels drills into a channel (`ServerSettingsPage.channelID`)
    and then into a role, so `paneBack` steps back one at a time while a rail tap
    leaves both. `App.channelOverrides` is what it draws — `Store.ChannelOverrides`
    merged with `Store.ServerRoles`, so every role is listed whether this channel
    changes anything about it or not, and `Store.Permissions(channelID)` rather than
    the server-wide answer, an override being exactly what moves those apart. It is
    read from the store like the roles and for a better reason: `PartialChannel`
    carries both halves, revoltgo applies them, and the `ChannelUpdate` that follows
    an edit already queues `refreshChannels`, which is what rebuilds this page. The
    grid draws a **subset** of the bits (`ui.channelPermissions`) — Revolt keeps one
    bitfield for both scopes, so an override of a server-only bit would be stored and
    never read. Unlike the Roles section, a role this account does not outrank is
    still opened, read-only: its row can only say how many bits moved, never which.
35. **Editing a member** is `ServerMemberEdit` under three different permissions,
    and the member menu is where all of it is offered (`members.go`, built into
    `memberMenu`). A **nickname** is a `PromptDialog` that closes on submit — a
    refusal is nothing to correct in the field it came from, unlike a username — and
    "Remove nickname" appears only where there is one, hence `domain.Member.Nickname`
    kept apart from `Name`'s fallback. **Roles** are a submenu of checkable items,
    each sending the whole set with its own change made, offering only what this
    account outranks (`App.serverRanking`, Revolt's own rule: the lowest rank held,
    everything for the owner, nothing for a member with no roles). A **timeout** is a
    submenu of spans and then a confirmation naming the span, with "End timeout"
    replacing it while one stands — `domain.Member.Timeout` being a moment rather
    than a flag, since nothing announces the expiry. `canTimeoutMember` asks
    `Store.MemberServerPermissions` about the *target*, that being the one refusal
    the route makes that permissions of ours cannot predict.
36. **The call.** Two halves that never meet in one package: `internal/audio` is
    the devices, `internal/voice` is the media session, and `app` is the only
    thing importing both — which is what lets `voice` name its microphone
    structurally (`PCMSource`) and never import `audio`. `app/voice.go` is the
    whole controller half.
    **Joining is never a side effect of selecting.** Revolt keeps messages in a
    voice channel, and a tap that opens a microphone is not a tap anybody expects,
    so `selectChannel` is untouched. There are two ways in: the button on the
    strip under the message header (`syncVoiceNote`, the one surface already drawn
    only for a voice channel), and "Join call" on the channel's own menu
    (`leadWithCall`, above Copy channel ID the way marking read leads). **Not** the
    participant row's tap — that opens a profile and must keep doing so.
    `joinCall` runs **entirely on the worker** — the REST call, `audio.OpenInput`
    and `voice.Join` alike — with `installCall` as the single hop back. `voice.Join`
    blocks for the whole connection handshake, so putting it in `backgroundThen`'s
    `then`, which runs inside `doOnUI`, froze the window for five or six seconds
    whenever a voice node did not answer. The epoch check therefore happens *after*
    the dial, in `installCall`: a call that connected into a session that has since
    gone is closed rather than never made, the same way the microphone already was.
    `a.background` rather than `a.backgroundThen`, because the latter runs neither
    branch when the worker succeeds into a stale session — which would leak a live
    call.
    Inside that worker the credentials and the **devices** are started together
    and waited for as a pair (`openedInput`): the microphone has nothing to say to
    the token, so run in order a join cost a REST round trip *plus* two device
    opens. The pair is what makes the wait unconditional — a capture arriving
    after a failed route is a device nothing holds and nothing closes. The node
    itself is off the path entirely: `Client.WarmVoiceNode` resolves it off Ready,
    it being instance configuration that cannot change under a running client, and
    resolving it lazily made the first join of every session pay ~60 ms for it.
    `callJoining` is the
    single flight, a plain bool because the point is "not again *yet*": a second
    tap must not open a second microphone. `force_disconnect` is passed always —
    Stoat refuses a second connection for one account, so a client that crashed
    mid-call could not otherwise rejoin. The join is also cancellable: `dropCall`
    bumps `callGen`, a connection landing after the reader gave up is closed
    rather than installed — otherwise it is a live call with an open microphone
    and no island to leave it from — and a *failure* landing after they gave up is
    dropped by the same generation in `failedJoin`, or it would re-arm a rejoin
    of a call they left.
    **Three ways out, and they are not the same.** `dropCall` releases the media
    and *keeps* `callChannelID`, which is what a call being reconnected looks
    like: the island stays on screen saying so. `hangUp` is that plus giving the
    channel up. `leaveCall` is `hangUp` plus `cancelRejoin`, and is what every
    surface a reader can reach is bound to — the island's own button, the channel
    menu, a moderator disconnect, and signing out. A call somebody left must not
    reconnect itself, which is the whole distinction.
    **A drop is rejoined, not reported.** lksdk recovers a transient blip on its
    own (`OnReconnecting`/`OnReconnected`), so a `CallEnded` carrying an error
    means the room is gone and a fresh token is needed — `JoinCall` again, with
    `force_disconnect`. `scheduleRejoin` doubles the wait per attempt to a
    30 s ceiling and gives up after five, `callRetryFor` naming the channel the
    sequence belongs to so a join of anywhere else abandons it. A rejoin that
    *itself* fails keeps the sequence going (`failedJoin`) — a voice server that
    is down is exactly what the wait is for — and a `Connected` retires it. This
    is also why `joinCall`'s "already in this one" guard asks for a call as well
    as a channel: between a drop and its rejoin the channel is still named, and
    that gap is when the retry has to be let through.
    A *first* join that fails is retried on the same machinery, `hel1` being
    measurably flaky — a 5 s signal timeout and a `500` from `join_call` in the
    same five minutes as a clean call. `client.Transient` is what decides:
    anything that is not an HTTP answer is worth asking again, a 5xx is the server
    failing rather than refusing, a 4xx or `ErrNoSession` is an answer.
    `callRetryAfterDrop` separates the two sequences — five attempts and
    "Reconnecting" for a call that was up, three and "Connecting" for one that
    never landed — and only the last failure is said out loud, so a join that
    succeeds on the second attempt is silent.
    **Being moved is followed.** A moderator dragging this account into another
    voice channel arrives as an ordinary `VoiceChanged`, and the media session
    knows nothing about it — so `followVoiceMove` compares where the store now
    says we are against `callChannelID` and rejoins, or hangs up where we are in
    no call at all. It runs *before* `drawsCall`'s guard, a move out of the open
    server being exactly the case that guard drops. It acts only on an event
    about this account (`VoiceChanged.UserID`, "" meaning self): right after a
    join the gateway may not have filed our own voice state yet, and anybody
    else's event in that window would read the store's empty answer as a
    disconnect and tear down a call that just connected.
    **Settings apply to a call already running.** `applyVoiceSettings` pushes
    sensitivity, input gain, soft clipping, the rumble filter, noise suppression
    (its strength and speech-veto dials included), call volume and the output
    device onto whatever is open, and re-arms the push-to-talk poll; without it
    each is read once at join and a slider dragged mid-call does nothing, which
    reads as a broken setting rather than a deferred one. `inputConfig` and
    `applyCaptureSettings` are the one builder and one pusher both microphone
    openers share — a dial only the join carried would behave differently in a
    call than under the meter tuning it.
    **Both gains are decibels** (`config.VoiceGainOffDB`..`VoiceGainMaxDB`,
    −40 = off, +20 = ×10), converted at the seam by `audio.GainFromDB` — `audio`
    speaks linear gain, being where the arithmetic is, and `config` owns where
    the range ends, being the leaf everything reads. **So is the gate's
    threshold**: `config.Voice.SensitivityDB`, `VoiceGateQuietestDB`..
    `VoiceGateLoudestDB`, handed straight to `Capture.SetGateThreshold` with no
    mapping left anywhere. It was an arbitrary 0-100 onto that same range, and a
    file written before the change carries the old key — `config.sanitise`
    converts it once and clears it, which is the whole migration (there is no
    version field, and reading a saved `35` as decibels would clamp the gate to
    −20 and silence the microphone with nothing saying so). The per-person volume is the
    same range in the same unit, on a `ui.SliderCard` hung beside the participant
    row (`showUserVolume` → `showPopover`) and seeded by `audio.DecibelsFromGain`
    off `Sink.Gain`: a slider is not a `fyne.MenuItem`, so the menu item opens a
    card rather than a submenu of the levels somebody thought of. Fyne dismisses
    the menu before running an item's action, so nothing is left over it. It is
    **written down twice**, to the sink and to `config.SetUserGain`: the sink
    holds it for as long as the client runs and hands it to the lane opened for
    that person next — theirs across a leave, a rejoin and the next call — and
    config is what survives a restart, seeded back into the sink in `joinCall`
    before any lane can open. Unity is stored in neither, so the map is as long
    as the list of people actually moved. A participant with no lane is still
    left alone (`Sink.SetGain` conjures none), which is
    what a volume set across somebody's leave comes to. The input gain is a **preamp inside the capture chain**, in front
    of the gate: raising it is also what lets the gate hear a quiet microphone,
    and the meter reads the same measurement so the bar and the threshold mark
    cannot disagree. The input *device* included: `Capture.SetDevice`
    opens the new microphone on the capture's own goroutine and keeps feeding the
    same ring, so the publisher inside a blocking `Read` sees a period of quiet
    rather than a stream closing under it.
    **The level meter borrows the call's microphone.** Two captures on one device
    is a second open, which WASAPI shared mode grants and a device somebody holds
    exclusively refuses — and a reader adjusting the gate mid-call is when the
    meter matters most. `startInputMonitor` takes `a.capture` where there is one
    and opens its own otherwise (`monitorOwned` is which); `monitorReport` is kept
    so `restartInputMonitor` can move the bar between the two as a call starts and
    ends. `stopInputMonitor` closes only a stream it opened; `forgetInputMonitor`
    is the page's own exit, which drops the bar as well.
    **Push-to-talk is a poll, not a key handler.** `ui.KeyHeld` asks the platform
    directly (`GetAsyncKeyState`), because `desktop.Canvas`'s key hooks fire only
    while nothing holds canvas focus and the composer holds it for most of the
    client's life. `armPushToTalk` runs a 16 ms ticker for as long as a call is up
    in that mode and writes an atomic the capture reads from its own `Read` — no
    UI hop, 60 of those a second being a window repaint's worth of scheduling for
    a bool.
    **The speakers are opened before the dial.** `Engine.open` is reached from
    `play()` and nowhere else, so the playback device used to open on the first
    *notification sound* — a call joined before anything had rung wrote remote
    audio into lanes no callback was rendering, and was silent with nothing saying
    so. `joinCall` calls `Engine.StartOutput` first. It is load-bearing rather than
    tidy: the callback is also what asks `voice` for the next frame, so a closed
    device does not even decode.
    **Deep PLC is a switch, not a build.** libopus gates its neural concealer on
    the decoder's complexity being ≥ 5 and defaults it to 0, so the model is
    compiled in and asked for per decoder. `config.Voice.DeepPLC` → `voice.Options`
    at join, `Call.SetDeepPLC` from `applyVoiceSettings` after. The call holds an
    atomic and the filler pushes it onto a decoder when it changes, because libopus
    decoder state is per stream and must not be reconfigured from under a decode.
    **A second pump, not `dispatch`.** `client.Event`'s marker is unexported so a
    voice event cannot be one, and that channel *blocks rather than drops*, so
    speaking updates would stall the gateway reader behind them. `pumpCall` is
    modelled on `pumpEvents`, one `doOnUI` hop per **burst**, ending when
    `Call.Close()` closes the channel. `onCallEvent` drops anything from a call
    that is no longer `a.call` — the same check `armTypingTimer` makes.
    `leaveCall` is in `resetSessionState` beside `resetTyping`: the token was
    minted against a session that no longer exists, and a microphone must not stay
    open across a sign-out. There is no leave route — leaving *is* disconnecting.
    **Speaking must not rebuild the column.** `Canvas.dirty` is one bool, so any
    Refresh repaints the whole window; a call of eight people talking over each
    other would be eight full repaints a second through
    `queueRefresh(refreshChannels)`. Two mitigations, both needed: `voice` emits on
    a *transition* against its own last-reported set, and `onSpeakingChanged` diffs
    `App.speaking` again and touches only the rows for that user
    (`voiceRows`, beside `channelRows`; `VoiceParticipantRow.SetSpeaking` no-ops on
    an unchanged value and moves one circle's fill). `callRows` re-applies
    `a.speaking` as it builds, a rebuilt row being a new object that knows nothing
    of who was talking. The third mitigation is the reader's:
    `config.Voice.SpeakingRing` off keeps the *record* — `App.speaking` is still
    written, so turning it back on draws the truth rather than whoever talks next
    — and skips only the painting, which is the whole of what it costs.
    **What is held against a voice comes from three places, and only one of them
    is an event.** `App.voiceMarks` gathers them into a `ui.VoiceMarks` per row:
    the two server-wide holds off `domain.VoiceParticipant` (the *membership*'s
    `can_publish` / `can_receive`, so they are known whether or not this client is
    in the call, and a rebuild of the column is what draws them); a participant's
    own mute off `voice.MuteChanged`, which is the room reporting their microphone
    track muted and is therefore known only while we are in the same call; and
    their volume being at `VoiceGainOffDB` **here**, which is neither their doing
    nor the server's. This account's own two are read from `a.muted` / `a.deafened`
    instead — `SetMuted` is what was just decided, not news to wait for — and
    nobody else's deafen is knowable at all (see `docs/known-gaps.md`).
    `refreshVoiceMarks` re-reads the participant out of the store rather than
    trusting the row: a hold is the membership's. Which is also why
    `onMemberUpdated` queues `refreshChannels` for a member *in a call*, not for
    this account alone — a mute given to somebody else moves their row and nothing
    else announces it.
    **The moderation items toggle.** `memberVoiceItems` reads
    `Member.ServerMuted` / `ServerDeafened` and offers the opposite, because this
    menu is the only place either hold is lifted from; an item that could only
    ever mute somebody already muted is a hold with no way out. For the same
    reason the pair is offered to a member who is **not in a call but is already
    held** — a hold is the membership's and outlives the call it was given in —
    where Move and Disconnect stay gated on the call. Every item there
    is refused for this account (`userID == SelfID`), and `voiceParticipantMenu`
    now makes the same refusal for the per-person **volume** — this account is not
    one of the voices this client mixes, so its slider moved nothing.
    **The island** (`ui.CallIsland`) floats on its own layer at the top of the
    window and nowhere else: a call outlives leaving the channel, the server *and*
    the view, and the window's layer is the one slot present in all of them. It
    carries both halves of voice — the running call, and the join offer for a voice
    channel on screen that is not it — so `syncCallIsland` is the single writer and
    `syncCall` is a call to it. `settleCallIsland` is what every path finishes with:
    the card's size changes with what is in it, and the layer under it has to be
    re-measured. `voiceWhere` fills a half's two lines and its picture, falling
    back rather than failing — the call outlives leaving the server, so the store
    may hold neither, and a channel it cannot place gets a name alone, an ID with
    no name being worse than nothing. A **conversation** is in no server and takes
    its own picture: a group's icon or a direct message's other account, and under
    a group's name how many are in it. A group with no icon is drawn as its
    members instead (`voiceGroupFaces`), skipping this account — the reader knows
    they are in it — and anybody the store cannot resolve, a blank circle not being
    a person. **A restyle rebuilds
    the tree** (`applyStyles` → `buildUI`), so the island is re-synced there or a
    running call loses the only way to leave it; a connected call needs no
    `SetState` to come back, a fresh card's bar already being green.
    **The settings meter owns a device**, which is the one thing on that page that
    does. It is its own `audio.Capture`, not the call's, so adjusting the gate
    mid-call is not two things fighting over the microphone; it is sampled at
    `meterInterval` rather than per callback, because a level arrives 100 times a
    second and each repaint is the whole window. An owned capture is also *read*
    by a loop of its own — `Capture.Level` is stored by `Read` and nothing else
    reads a monitor the call is not borrowing, so without one the bar never moves. It is stopped from
    `SettingsPage.showSection` **and** `Close` **and** `resetSessionState` — the
    page has no unmount hook, signing out does not close it, and either hole holds
    the microphone open for the rest of the run. The index pass
    (`buildSettingsIndex`) stubs all five voice hooks to nil for the same reason
    `LoadProfile` and `CacheStats` are stubbed: it builds every section twice on
    the first keystroke in the search box, and `StartInputMonitor` opens a device.
    **"Hear myself" is that same capture played back**, and it is written from
    inside `Capture.Read` (`SetEcho`) rather than by a reader of its own — `Read`
    has exactly one caller, so a test tapping the microphone any other way could
    not run mid-call at all. It lands in a `Sink` lane, so what is heard is what
    a call would send, `Sink.Reset` exempting it because a call ending must not
    silence a test. `App.monitorEcho` is the reader's intention and
    `applyInputEcho` re-applies it wherever the meter moves between the call's
    stream and its own; `forgetInputMonitor` clears it, the switch being a mode
    somebody turned on to listen to rather than a setting.
    **One stream feeds two bars.** The sampler reports a `ui.InputMeter` — the
    loudness as a ratio *and* as the dBFS figure the bar says beside itself
    (`audio.LevelDecibels`, floored at the bar's own bottom so the two readings
    cannot disagree), plus the noise suppressor's speech estimate, all off the
    same sample so what a reader compares is the same frames — because a
    second monitor would be a second open of the one device, which is what the
    borrowing above exists to avoid. The estimate is `Capture.VAD`, and it is
    **negative** where the model is not running: it runs only while suppression
    does, and a bar left at its last answer would report a microphone nothing is
    listening to.
37. **A group conversation.** Everything a group *is* was here before one could
    be made: it is a channel, so its sidebar row, header, messages and edit card
    are every channel's, and leaving one is `confirmCloseChannel`. What
    `groups.go` adds is the two things Revolt files on the account rather than on
    the channel — who is in it, and who may change that.
    Both cards are picked from `Store.Relationships` filtered to friends
    (`friendCandidates`), because friendship is the whole of what the routes take:
    a stranger in a create is refused and the whole request with them. Making one
    is the **plus in the channel sidebar's header**, which shares the cog's slot
    (`syncGroupAdd` against `syncServerCog`) — a server is configured, the home
    view is added to, and neither view has the other's button. Adding to one is
    "Add people" on the channel's own menu under `InviteOthers`; removing somebody
    is the member row's menu and the **owner's** alone, that not being a
    permission bit — hence `domain.Channel.OwnerID`.
    The response to a create is spent on selecting the new group, which is why
    `Client.CreateGroup` fetches the channel into `State` first: the
    `ChannelCreate` that files it cannot be waited for. Everything after is the
    gateway's — `ChannelCreated` puts the row in the sidebar, `RecipientsChanged`
    follows every arrival and departure.
    **A group is also the one conversation with a member sidebar.**
    `refreshRecipients` (in `members.go`, beside the walk it shares with the
    mention picker) draws it: a DM is two people the header has already named and
    saved notes is one, so only a group fills the column. That walk is cheap
    enough for the UI thread where a membership is not — the channel carries its
    participants and every lookup is the store answering from what it holds — so
    it is rebuilt whole rather than patched, which is also what presence in a
    group queues (`refreshMembers`, not `refreshPresence`). The options are
    `memberListOptions` with hoisting and hide-roleless cleared: a group has no
    roles, so the second would hide every row in it. Resolving the accounts behind
    the participants is `ensureRecipients`, called where the conversation changes
    and deliberately **not** from `refreshRecipients` — `flushAuthors` ends there,
    and it releases the guard on a failure, so re-queueing from inside it would be
    one request per batch forever for an account that cannot be fetched.
    **Asking somebody new** is the field at the head of the friends page
    (`askFriend`), and it is the only way to reach an account this client has
    never drawn — every other route to a person is a surface they appear on. The
    handle goes to the same route `AddFriend` uses; `done(sent)` is what clears
    the field on success and keeps it for a typo. The account behind the answer is
    not cached, so the row appears when the gateway files it, not when the request
    returns.
38. **Securing the account.** The Security section is how an account is *reached*
    rather than what is in it — its email, its password, its second factor and
    every device holding a login — and `app/security.go` is the whole controller
    half. It is the one section where nothing writes to config and nothing is a
    setting.
    **Everything begins with a challenge.** Revolt gates every route that ends a
    session or changes a credential behind an MFA ticket (see the client note for
    which and why), so the shape is: `App.challenge` raises `ui.ChallengeDialog`,
    mints a ticket from what is typed, and hands it to one request. The ticket is
    never held between two actions — it is short-lived by design, and an action
    reusing one is an action nobody confirmed. The methods on that card are
    **asked for** (`MFAMethods`) rather than assumed, and `answerable` drops what
    this client cannot answer: Revolt names a security key for an account that
    has one and there is no WebAuthn here.
    Two actions deliberately do not take that shape. **Password and email** each
    need the current password in the request *as well as* a ticket, so a challenge
    in front of them would ask for one answer twice — they are one card that mints
    its own ticket (`App.spend`), and `errPasswordRefused` is what tells a wrong
    password from the route refusing the new value. **Turning TOTP on** takes no
    ticket at all: its proof is a code from the authenticator, which is the only
    proof that means anything there. Its secret *is* gated, so setting one up is a
    challenge, then `ui.SecretDialog` holding the key and the code together — the
    key is shown once and asking for the code afterwards would mean showing it
    again.
    **The section is one fetch.** `ui.SecurityState` is the email, the factors and
    the logins together: three requests the section cannot draw without any of, so
    `loadSecurity` runs them in parallel and reports one error for the lot. It is
    held in `cachedOne` for the life of one *opening* — the same rule a server's
    fetched lists follow, and `SettingsPage.visit` is the same guard on an answer
    landing after the page closed. Every action here calls `refreshSecurity` once
    the server has agreed, which drops the held answer; unlike a server's list a
    late answer rebuilds the whole section, every row in it being drawn from that
    one answer.
    **It must not fetch while indexing.** `buildSettingsIndex` builds every
    section twice on the first keystroke in the search box, so `loadSecurity`
    checks `p.indexing` *and* the hook is stubbed there — the same pair
    `LoadProfile` and the microphone get. `settings_index_test.go` covers the
    guard through `indexPass`, which is the only way to see it: the stubbing in
    `buildSettingsIndex` hides exactly what it is about.
    **`EventAuth` is finally actionable**, and only because the session ID is
    recorded at login and saved beside the token. Naming *this* session is a fatal
    drop, the same one `Logout` is; naming any other is `SessionsChanged`, which
    only the page cares about. Where the ID is unknown — a login saved before the
    client recorded one — it says nothing rather than guessing, and the section
    says so under the list instead of leaving the reader to work out which row
    they are sitting at.
39. **Selecting messages, and deleting them together.** A bulk delete is a *mode*
    the message column is in, and `messages.go` owns both halves of it: the mode
    lives on `ui.MessageList` (the set, the anchor, which rows may join it) and
    what stands over the composer while it is on lives here.
    **There is one way in and it names a message**: "Select messages" on a row's
    own context menu, gated by `MessageWidget.canBulkSelect` — the route asks for
    `ManageMessages` even over the account's own words, and refuses the whole
    batch over one message past `domain.MaxBulkDeleteAge` (see the client note).
    A mode entered by a stray click is a mode nobody meant to be in, so it is
    not the hover actions and not a modifier-click.
    **The bar stands where the entry does**, inside the composer card beside
    `ui.ComposerNotice` and for the same reason: nothing is typed in that mode,
    and the card is where a reader already looks for what the next keystroke will
    do. Putting it there is also what makes `resizeDock` cover it — the card's
    height changing is a thing that machinery already knows about. `syncComposer`
    is the single arbiter: while selecting it hides the entry *and* the notice, so
    a permission arriving mid-selection cannot put the entry back under the bar,
    and a permission **lost** mid-selection ends the mode instead of leaving a set
    nothing can be done with.
    **Three things end it**, all through `endSelection`: Cancel, Escape
    (`escapeToPresent` takes it before the jump bar — the composer is not mounted,
    and a mode with no key out of it is one to be stuck in), and a channel switch,
    since the window a set was picked out of is about to be replaced.
    `MessageList.dropRows` additionally empties the *set* on a jump within the
    channel — the mode is the controller's and stays — a selection the reader
    cannot see being one they do not know they hold.
    **Nothing is removed optimistically**, as with a single delete: the rows leave
    through `BulkMessageDelete` → `removeMessages`, which was already the handler,
    so a refused request leaves them where they are and says so. The set is
    re-filtered against the week-long window on the way out — the reader can sit
    on a selection until a message ages past it — and how many were left behind is
    a warning rather than a silent drop. `Client.DeleteMessages` chunks by a
    hundred, so a large selection is several requests and several events — and
    the rows those events name are held like any other deletion (item 43), so a
    selection of ninety leaves the column in one move.
40. **A group's own settings.** `ui.GroupSettingsPage` is the settings shell
    (item 13) filled with one conversation, and the **third** such layer beside
    `settings` and `serverSettings` — only ever one of the three is up, each
    opening by closing the other two. Two sections, no lists and nothing fetched:
    everything on it is a field on the channel or a bit in one value, and both
    arrive as `ChannelUpdate`, so `refreshGroupPage` rebuilds it from the store
    the way `refreshServerPage` does.
    It exists for the two things a group has that no other conversation does — a
    **picture**, which is an upload into a bucket rather than a field (which is
    why the create card cannot ask for one), and a **say in what the people in it
    may do**. So it *replaces* the channel edit card for a group rather than
    joining it: `channelMenu` offers one or the other, and the age gate moved
    onto the page so nothing was lost. `groupEdit` is what makes a single field
    settable — `ChannelEdit` refuses a blank name and the route applies every
    field it is given, so whichever half is not changing is read back out of the
    store, the same read-and-resend this account's status line needs (item 23).
    The permission grid is the **shell's** now (`settingsShell.permissionGroups`,
    working from `gridAllow`/`gridDeny`), three pages drawing one. A group's scope
    is the odd one twice over: it is a plain **set** rather than an overwrite, so
    the rows are switches, and its `can` asks for `ManagePermissions` and nothing
    else — the group arm of `permissions_set_default.rs` makes no
    "can't grant what you lack" check, so gating on the bit would refuse what the
    server accepts. `groupPermissions` drops three bits from the channel mask:
    ViewChannel and ReadMessageHistory are in the floor Revolt ORs the value with
    and so cannot be turned off, and MentionRoles names something a group has not
    got. `groupDetails` rewords the one entry whose explanation says "role".
    `canManageGroup` is either permission, and `refreshGroupSettings` closes the
    page when neither is left — the owner can take both off everybody in one edit.
41. **What keeps the three message panels current, and what does not.** Pins,
    search and the inbox are *fetched* snapshots — each lists messages further
    back than the message cache reaches, which is why none of them is drawn from
    it — so the handlers that keep the column right say nothing to them. Three
    things can move under one, and they are not equally affordable:
    - A **deletion** is free: `removeMessages` calls `dropPanelMessages`, for any
      channel, beside the mention set it already prunes there. This is the one
      worth fixing first — a card for a message that is gone leads nowhere.
    - A **pin** is the pins panel asking again (`reloadPins`), which is the whole
      of what that panel is. It goes through `queueRefresh(refreshPins)` because
      Revolt sends an event per message; `client.MessageUpdated.Pinning` is what
      says a pin was what moved, read off the wire because a pinned message is
      routinely one the cache never held. An unpin made *here* re-asks too — the
      echo is indistinguishable from somebody else's — which costs one redundant
      search per settling window and needs no marker that has to be right.
    - An **edit** is bound to the cache (`refreshPanelMessage`): the gateway names
      the message and only the cache can say what it now reads. A card inside the
      channel's cached tail follows an edit; an older one keeps the line it was
      fetched with. Re-asking per edit would be a request per keystroke somebody
      else makes. See `docs/known-gaps.md`.

42. **Telling the client it has been superseded.** `updates.go` is one request to
    GitHub's `/releases/latest` per run, hung off `onReady` rather than off
    launch: the login screens are drawn before the notice layer exists, and Ready
    is the first moment there is somewhere to report into. `App.updateAsked` is
    the once-per-run guard — Ready repeats on every reconnect, the release does
    not — and `App.updates.Checking` is the claim single-flighting the request, so
    the Updates section's own **Check** button and the startup check are one call.
    Three things about it:
    - **The answer is not session state.** What GitHub publishes has nothing to do
      with which account is signed in, so `settleUpdate` is reached through a bare
      `doOnUI` rather than through `backgroundThen`'s epoch guard. Epoch-guarding
      it would drop the answer *and* leave `Checking` set, killing the button for
      the rest of the run.
    - **A failure is drawn, never announced.** Nobody asked for the startup check,
      so `UpdateState.Err` is held for the section to say and no notice is raised.
      A repository with nothing published (`update.ErrNoRelease`) is a state
      rather than a failure and carries no error at all.
    - **A release is announced once, ever.** `config.Updates.Announced` is
      persisted before the modal goes up, so a client relaunched twice a day
      interrupts once per release rather than twice a day. An unstamped build
      (`update.Comparable`) is never announced to — it is behind every release by
      definition. The modal is an `ui.Confirm` whose action is
      `openSettingsAt(ui.SectionUpdates)`: what the reader asked to see is the
      changelog, and that is where it is drawn.

    Nothing is downloaded and no binary is replaced. `ui.UpdateRelease` carries
    the one asset `update.Release.AssetFor` picked for this GOOS/GOARCH, and both
    buttons hand a URL to `openLink`. See `docs/known-gaps.md` for why.

43. **A deleted row is washed red and left standing.** `removeMessages` no longer
    takes the rows out where it stands: `holdRemoval` washes them
    (`MessageList.SetDeleted`) and files them in `App.removing`, a `removeHold`
    with one wake, and the whole batch leaves the column together when it lapses.
    **Nothing about the deletion waits on this** — it has already happened here
    and everywhere else. The request went when it was confirmed
    (`App.deleteMessage`, unchanged), the cache dropped the message before this
    ran, and the mention set, the three panels and any open editor are answered
    *before* the hold, an editor over a message that no longer exists being one
    nothing can save.
    Two things come of it. A burst of deletions moves the column **once** — a
    sweep arrives as an event per message and each of them re-derives seams and
    re-lays out — and a message somebody is halfway through reading goes red
    rather than vanishing out from under them.
    - **The hold widens rather than restarting**, and is measured from the *first*
      row of the batch: `config.Behaviour.DeletedHold(standing)` is
      `DeletedHoldMS + DeletedHoldStepMS × (standing-1)`, capped at
      `DeletedHoldCapMS` — 5 s, +0.5 s, 8 s. The cap is therefore a bound on how
      long any one row stands, where a hold restarted per deletion could be pushed
      out indefinitely by a steady trickle. At zero `holdRemoval` answers false
      and the caller removes as it always did.
    - **A hold belongs to a channel**, and only the open one has rows: a deletion
      elsewhere returns before it (`removeMessages`' own guard), and a hold whose
      channel has since been left is dropped rather than acted on — the window
      went with the switch, so there is nothing to take out. `flushRemoval` checks
      that at the wake as well as `holdRemoval` at the arrival.
    - **A wake that has fired cannot be recalled**, so `armRemoval` checks it is
      still the timer the hold carries — the trap `armTypingTimer` names — and it
      is epoch-guarded like any other. `resetSessionState` calls `dropRemoval`:
      the column is about to be replaced whatever the wake would have done.
    - `MessageList.Remove` is what clears the marks, so the one path that takes a
      row out is the one that forgets it. `dropRemoval` gives them back by hand,
      being the case where the rows are *not* removed.

    The marks live on the list rather than on the widgets (`SetDeleted`, keyed by
    message ID) so a row scrolled past and back is built already washed — the
    arrangement the selection already has.

44. **Picking a GIF.** `gifs.go` is the controller half of the composer's second
    pop-up. Nothing about it is held here: the lists come from a service *beside*
    Revolt (`api.gifbox.me`, which holds the provider's key and authenticates
    with this session's token), no event announces any of it, and its bucket is
    ten requests in ten seconds — so the picker holds each answer for as long as
    it is open, single-flights a query already in flight and lets the field settle
    before asking. `App` only fetches: `fetchGIFs` runs one list on a worker and
    answers on the UI thread through the picker's own callback, epoch-guarded like
    any other. **A failure is drawn, not announced** — nobody asked out loud, and
    the line above the grid is where the reader is already looking.
    **Which rendition a tile draws is policy and lives here**, the way a
    keystroke's sound does: `gifPreviewOrder` prefers the small stills, and a
    rendition this client cannot decode is stepped over (`gifVideoMarkers`, matched
    on the name and the URL both) — the service prefers WebM and MP4 and there is
    no player. A format set this client has never heard of falls back to the
    smallest that is not a video, walked in sorted order so the answer is the same
    twice: an unknown set drawing nothing would be an empty grid.
    **What is picked is a link**, not an upload: `MessageInput.pickGIF` inserts
    the page URL at the caret exactly as the emoji picker inserts a token, so
    the send rules — the permission, slowmode, the queued replies — are the
    composer's own and unchanged, and a draft already typed is not lost. Revolt
    unfurls that page into the embed (item 6).
45. **Playing a video.** `video.go` is the controller half of
    `docs/video-player.md`: the card (`ui.VideoCard`) is dumb on purpose, and
    every `OnVideo*` action lands here because each one is a policy about a
    sender-controlled bitstream — what a mount may fetch, what a tap decodes
    with, which file the OS is handed.
    **A mount fills the card in, under a ceiling.** `OnVideoMounted` answers
    from `a.videoInfo` and the poster's image-cache entry where it can, and
    otherwise runs one worker per file (`a.videoBusy`, the poster job's own
    single-flight): fetch — only under `videoPosterFetchBytes`, scrolling past
    a large video must not download it — then sniff, probe, poster, all
    through `internal/video`'s sandboxed children. The probe's answer is
    memoised in `a.videoInfo` and the poster as an ordinary image-cache entry
    (`id+"-poster"`), so a remount costs two lookups. A file that will never
    play is memoised too (`a.videoFailed`, via `permanentVideoError`, holding
    the refusal's reason — a file declaring no length is one of them, the
    probe refusing it) — the driver's own refusals stay true on retry where a
    lost connection does not — so its card says "Not playable" instead of
    retrying per scroll, and a tap answers with the reason.
    **A tap toggles, and one video plays at a time.** `OnVideoTapped` on the
    playing card is `pauseVideo`; on anything else it is `startVideo`, which
    stops what was playing first. Pause **is** a stop that remembers: the
    children are killed and `a.videoAt[id]` keeps the position, because `-ss`
    on a restarted child is the only correct seek over a pipe — so pause,
    resume, and `OnVideoSeek` while playing are all the same restart
    (`launchVideo`). A resume within `videoResumeSlack` of the end starts
    over; a finished video drops its position; a stop keeps it, so leaving a
    channel mid-film resumes on return.
    **Launching is the installCall arrangement.** `startVideo` prepares on a
    worker (`backgroundThen`, fetch progress reaching the card's chip through
    `videoProgress`), then `launchVideo` reads the card's box on the UI thread
    — the frame pipe carries exactly that many pixels — and starts the
    children off-thread; one hop installs them, and a playback landing into a
    replaced session or after another play took over is closed rather than
    installed. `a.video` is the one playback; `videoPlayback.halt` is
    idempotent and tolerates streams not yet installed.
    **Two pumps, two clocks.** `pumpVideoFrames` paces by wall clock against
    the rate this side asked for, paints through one *waited* hop per frame
    (the scratch buffer is reused the moment it returns), drops and re-anchors
    when more than two frames late, and is what notices a dropped card —
    `card.Mounted()` per paint, the tick-stops-itself rule the GIF animator
    keeps. `pumpVideoSound` tops the reserved mixer lane up to `VideoWant` on
    the speakers' wake, exactly as a call participant's writer does, with a
    20 ms ticker as insurance for the wake a concurrent call's playout
    consumed. Mute is lane gain (`SetVideoGain`), re-asserted at every start
    from the card's own toggle.
    **Every teardown path is explicit**: `selectChannel` (the window goes, the
    position stays), `resetSessionState` (beside `leaveCall`, with all four
    maps), EOF (`settleVideoEnd`, which the kill path skips by reading
    `p.stop`), and the paint that finds the card off-canvas. A **GIF-marked
    embed** (`card.Loop`) decodes through `-stream_loop` — one child for any
    number of passes, no EOF to settle — starts silent, and wraps its clock
    through `videoPlayback.position`.
    **Open in your player** (`OnVideoOpen`) fetches like play does and hands
    the OS the stored file — named by sniffed magic, never the sender's
    filename — through `openLocalFile`'s file URL; `ui.openURL` is not the
    path, that gate being for destinations somebody else chose. A file
    nothing recognises is refused with the reason, not handed to a shell that
    would believe its name.
46. **Watching a screenshare.** `screenshare.go` is the receive half of
    `docs/screenshare-todo.md`, the video player's seam pointed at a live
    stream: `voice` owns the track (the registry, the nothing-video-until-
    watched subscription policy and the depacketise/remux — `voice/share.go`),
    `video.LiveFrames` owns the sandboxed decode from stdin, and `app` is
    where the writer the one hands the other is made. The way in is the row's
    live mark (`ui.shareWatchTap`, drawn from the gateway's `Screensharing`
    flag — which is why it stands even out of the call): `OnWatchShare`
    refuses out-of-call and no-ffmpeg taps with a notice, keeps **one watch**
    (a second tap focuses the window, a different sender replaces it), opens
    the window *before* the subscription — a watch that never delivers must
    leave something to close — and hands `voice.WatchShare` a `ShareOpen`
    that launches the decoder at `shareDecodeSize` (the sender's declared
    size, believed only under the 1080p cap) and starts the pump. The pump is
    `pumpVideoFrames` minus the clock: live is paced by the sender, so it
    paints on arrival under a waited hop and always drains; the share window
    is its own `fyne.Window`, so a frame dirties nothing of the main canvas.
    Every teardown meets in `closeShare`: the window's own close,
    `voice.ShareEnded` (a sender stopping is silent, a failure is a notice),
    the pipe dying (`settleShareEnd`, guarded on `stopped` so an owner's kill
    is not reported twice), and `dropCall` — the window must not outlive the
    media session feeding it. Share audio is the lane machinery unchanged
    under `voice.ShareLane(userID)`; its dial is "Screenshare volume" on the
    participant menu, offered while the store says they are sharing, written
    to the sink and to `config.SetUserGain` under the lane's own key.

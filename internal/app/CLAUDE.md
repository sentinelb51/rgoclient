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
   Both screens report on a **`ui.StatusLine` of their own**, not through
   `dialog.ShowError`: `NoticeStack` belongs to the main UI and does not exist
   until Ready, and a Fyne dialog is the one surface `AppTheme` does not reach. It
   is a `widget.Label`, not a `canvas.Text` — a transport error is a sentence that
   must wrap, and `Importance` colours it without holding a colour a restyle would
   leave stale.
   **A session that opens and never reports is the failure that looks like a
   hang**, so `awaitReady` watches for the snapshot: `Client.Open` returns once
   the websocket is up, Ready alone names the account, and revoltgo drops an event
   it cannot decode before any handler runs. At `readyTimeout` the session closes
   and the login screen returns saying so. `onReady` disarms it, as does
   `resetSessionState` — the gateway that owed the snapshot is being replaced.
2. `onReady` → save token, record unreads, `showMainUI`, `refreshServerList`,
   `selectServer(first)` — or `selectHome` when the account is in no servers.
3. `selectServer` → `refreshChannelList`, `refreshMemberList`, `loadMembers` →
   `selectChannel(first)`. `loadMembers` is **one request for the whole
   membership**, once per server per session (`App.fetchedMembers`,
   `Client.FetchMembers`): Revolt has no pagination and no member search, so a
   server is all of it or none, and `exclude_offline` is declined because the
   Offline section is the point. Paint-then-fill, so re-entering a server never
   blanks its list. It is a setting (`FetchAllMembers`) because it is the one call
   whose cost is somebody else's server. It also fills the *user* cache, which is
   what makes presence work: `State.updateUser` drops an update for an account it
   has never seen, so an unfetched member could never be seen to come online.
   Lazy per-author resolution stays for what it does not reach — webhooks, people
   who have left, a failed fetch, conversations.
4. **Mounting a channel.** `selectChannel` → cached messages, else
   `Client.LatestMessages` (deduped per channel); ack unread. Callers render from
   the *cache* (`displayCached`), never from a page captured off-thread.
   `displayMessages` mounts only the newest `initialMountCount`.
   `loadMoreHistory` is three-tier: unmounted cache synchronously, then
   `HistoryBefore`, then `MessagesBefore` for a window not in the cache at all
   (which writes nothing to it). The window is bounded at `mountedCap` and trims
   `clear()` vacated slots so widgets are released.
   All construction goes through `App.newMessageWidget(prev, curr, next)` — hence
   also where `ensureAuthor` runs and where grouping (`continuesGroup`, within
   `messageGroupWindow`) and the day separator are decided. The separator belongs
   to the widget, not a list entry of its own, so the window stays one object per
   message.
   A column with nothing to draw says so on one line (`App.showStatus` →
   `ui.NewMessageStatus`). `showStatusMark` is that line led by a mark, and only
   an empty channel carries one: every other status is a wait or a refusal, where
   a mark would decorate an apology.
   `syncChannelKind` is what the header owes each switch: the prefix glyph, and
   the note under it that only a **voice** channel draws. Revolt keeps messages
   in a voice channel like any other and this client cannot join the call, so
   nothing about mounting one differs — the glyph and that strip are the whole of
   what says so. The strip is built once and shown per channel, and hiding a
   child reclaims nothing on its own, hence the `ui.Relayout` either way.
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
   queues what `ResolveMessage` cannot answer for; `flushReplies` fetches the
   batch (`Client.ResolveMessages`, guarded by `App.fetchedReplies`) and then
   queues the *authors* behind what came back — somebody who only ever spoke that
   far back is nobody the page has resolved. What arrives goes in `App.uncached`,
   not the message cache: that cache is a channel's contiguous tail while a reply
   reaches as far back as somebody cared to answer, so one filed among its
   messages would be mounted by `loadMoreHistory` as history. Nothing else evicts
   them, hence `holdUncached` dropping the store and its guard together at
   `maxUncachedMessages`. The guard is **kept** on failure, unlike
   `ensureAuthor`'s: a target that cannot be fetched was usually deleted, which
   stays true, and a quote remounts on every scroll past it — releasing it is a
   request per pass for an answer that cannot change.
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
8. **Mentions.** `@` or `#` at the start or after a space opens the picker, which
   gets first refusal on Up/Down/Enter/Tab/Esc. The marker decides which of the
   two pools is filtered and what the span is rewritten as — Revolt's `<@id>` or
   `<#id>`, which `ui/markdown.go` renders back as `@Name` and `#channel`.
   `MentionKind.marker`/`markerKind` are the only place the two characters are
   named. A heading's `# ` opens the channel list for the one keystroke before the
   space closes it; refusing the picker at the start of a line would cost every
   mention typed there.
   Candidates are **pushed** — `refreshMemberList` and `refreshChannelList` build
   rows and candidates from one walk — so a keystroke is two string comparisons
   per candidate, nothing allocated. A **server's** people therefore arrive only
   from `refreshMemberList`, which makes that walk off the UI thread;
   `refreshMentionCandidates` covers the conversation case alone
   (`recipientCandidates`, bounded by the channel's recipient list) and returns at
   once for a server channel. Asking it for a server's would walk a whole
   membership on the UI thread, per channel switch, for what the picker already
   holds — every path into a server channel goes through `enterServer`.
   The picker mounts *inside* the composer card, not floating: a Fyne pop-up takes
   canvas focus, which would stop the typing that drives it. Being inside the
   card, **it must not close on blur** — Fyne unfocuses on the mouse *press* and
   re-hit-tests on the release to decide where the tap lands, so hiding here
   resized the composer out from under the click and the first click on anything
   was spent dismissing the picker. Visibility follows the caret instead
   (`syncMentions`, from the typing methods and from `MessageInput.MouseDown`,
   where `widget.Entry` moves the caret). An open picker outlives the entry's
   focus and can outlive its channel, so `SetCandidates` re-runs the query.
   A **rendered** mention is tappable: `mentionSegment` / `mentionText` in
   `ui/markdown.go`, reaching `Actions.OnUserTapped` (anchored on the word, so the
   card opens beside the name) or `Actions.OnChannelTapped`. It is a widget
   because a `TextSegment` carries a colour but not a tap, and that costs what
   every custom segment costs: RichText measures one only to subtract it, so it
   can neither break nor be broken before. Hence per-word splitting *and*
   `mdBuilder.reserve` — the widest mention word in the body, kept clear on the
   right, the only thing stopping one at a line end being cut off by the message
   column. Anything else in a body that answers a click (`decoratedText`) carries
   `onMenu` for the same reason: the driver gives the press to the innermost
   object accepting one and does not walk back up, so a word without the message's
   menu is a hole in it.
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
   `setChannelGroup`), neither being a conversation with anybody: one is ordered
   by nothing, the other would be moved about by its own last message. The block
   sits *outside* the list's side padding and above its scroll, so it is the full
   width of the column and does not scroll away; its own hairline marks it off,
   and an empty group draws none. Saved Notes is drawn twice over as not-a-person:
   `ui.avatarLed` leaves it a glyph row rather than the taller avatar-led card,
   the picture otherwise being this account's own standing in for a notepad. Its
   rows answer to selection, unread and typing like any other, hence
   `App.channelRows` walking both halves — a walk that knew only the list would
   leave one row that never repaints.
10. **Joining a server.** The join response does *not* add the server: revoltgo
    decodes it into an `Invite` whose `ServerID` is never populated. The
    `ServerJoined` event does, and `App.pendingJoin` tells that handler to select
    what it adds. Both entry points — the dialog and an invite card — go through
    `App.joinInvite`, differing only in where a failure is said.
    An **invite link in a message** unfurls into `ui.InviteCard`, built from a
    *code* rather than an invite because a code is all the message carries.
    Resolving one is `Client.FetchInvite` (that route *does* populate `ServerID`),
    so a card mounts loading and fills itself through `SetInvite` — also how a
    caller already holding a `domain.Invite` skips the request
    (`NewInviteCardFor`). Its width is fixed, not measured like an embed's: it
    mounts saying nothing, and resizing on arrival would shuffle the column under
    someone reading it. Its action follows membership — if `Store.Server` reports
    the account is in the server it offers `OnServerTapped`, not `OnJoinInvite`.
    `App.invites` caches both outcomes, failures included (an expired invite stays
    expired, and a card remounts on every scroll past it), and
    `App.pendingInvites` collapses two cards for one code onto one request.
    Finding the links is `markdown.Links` over the parsed body, not a scan of the
    source: a URL in a code span is not a link, and a spoiler's contents are
    deliberately not reported. `util.InviteLinkCode` is the **strict** matcher
    deciding which of those URLs is an invite, not interchangeable with
    `util.InviteCode` — the lenient one serves a field somebody typed into and
    reads a code out of any last path segment, which pointed at a channel's worth
    of links would card half of them. `util.MayContainInvite` keeps the parse off
    the mounting path for almost every message.
11. **Slowmode.** `selectChannel` paints what is known and fires `loadSlowmode`,
    which re-asks on *every* visit — see the revoltgo note: entering the channel
    is the only moment the client can learn the number, or that it moved.
    `App.slowmodeOf` is the cooldown as it applies *to this account*, so
    `BypassSlowmode` collapses it to zero and the badge never appears for a
    moderator. `handleSubmit` refuses while `slowmodeRemaining` is non-zero and
    keeps what was typed, saying nothing: the badge counting down is the answer,
    and a notice per keypress would bury it. The cooldown starts optimistically at
    submit — so a second Enter can't outrun the request — and is given back when
    the send fails. `onMessageCreated` starts it too, covering a message the same
    account sent from another client (`startSlowmode` won't restart a running one,
    so our own echo is a no-op). `refreshSlowmode` re-arms one timer a second at a
    time rather than running a ticker for the life of the app.
    The badge sits *outside* the card, above its right edge: inside, it was
    furniture the entry had to make room for. Its pill hugs the text rather than
    spanning the row — a surface that wide just above the card reads as a bar
    growing out of it. `App.composerDock` is that row stacked over the card, and
    the whole stack floats, so `ui.DockReserve` covers the chip too. Relabelling
    moves only where the chip starts (`SlowmodeBadge.OnResize` → `ui.Relayout`);
    appearing or disappearing changes the stack's height, so `refreshSlowmode`
    calls `App.resizeDock` — Fyne reclaims nothing for a shrinking minimum.
12. **Notices and confirmations.** `App.confirm(ui.Confirm{...})` for anything
    irreversible, `App.notify(tone, …)` for an outcome the user didn't ask about.
    A `ui.Tone` is the *only* thing deciding colour, icon and button weight.
    Destructive actions share one shape: a `can…` check decides whether to offer
    it, `confirm…` asks, the action fires through `App.background`, and the
    **gateway event** updates the UI. Nothing is removed optimistically.
    **Holding Shift answers a confirmation in advance**, `App.confirm` being the
    one place that decides it — so the key covers every destructive action in the
    client rather than the ones somebody wired it to, deleting a message
    included. A `Confirm` with no `OnConfirm` is never skipped: it is a statement,
    and skipping it would say nothing. `ui.ShiftHeld` asks the platform at the
    moment of the click rather than tracking the key — see the footgun — and is
    Windows-only, which `shiftSkippable` carries to the card so the hint under the
    buttons appears only where it is true.
    A confirmation's two answers are **half the card each**, not a pair in the
    corner: the same two targets in the same places every time, so it is answered
    by position rather than by reading a small label. Tone still colours only the
    confirming one, so which is destructive is read off that rather than off which
    is easier to hit.
13. **Settings.** The page is `ui.SettingsPage`, a layer in the window's content
    stack beside `notices.Layer` and `tooltip.Layer` — **not** a canvas overlay,
    because `mountOverlay` closes whatever was there and a confirmation raised
    from settings has to draw *over* it. `App.bindKeys` decides who owns Escape
    (overlay, then settings, then nobody) and is called from all four of
    mount/close overlay and open/close settings.
    A change goes `SettingsPage.change` → `App.updateSettings` → `config.Update`,
    which is all a Behaviour flag needs: they are read where they are used
    (`store.Members`, `continuesGroup`, `messages.go`'s mount caps). A style goes
    through `restyle` → `App.applyStyles`, which rebuilds the theme tables and
    then **defers** the tree rebuild while the page is open — the page covers the
    client, and `SetContent` under a slider mid-drag would take the slider with
    it. `App.stylesDirty` carries that to `closeSettings`; what answers a drag
    meanwhile is the section's own preview, built from real widgets. Styles are
    *overrides* keyed by `theme.Sizes`/`Colors` field names and applied by
    reflection, so the curated groups and the generated Advanced list add up to
    the whole table — `settings_test.go` asserts it.
    A section returns `[]settingsGroup` — a card *beside its caption* — because
    the rail lists the open section's groups under it and scrolling to one is an
    offset into the pane. That offset is a prefix sum over `MinSize`, taken once
    per section (`measureGroups`): `Position()` is right only while the pane's top
    inset is zero and is unset before the first layout, and the scroll path must
    not walk the pane per event. A tap sets the marked entry itself, since the
    scroll clamps at the end of the content and the last group never reaches the
    top; `ObservableScroll.OnScroll` corrects it afterwards and fires only for
    real movement, never a programmatic one. The rail is **not** rebuilt to move
    the marker (`settingsRailButton.setSelected`) — following a scroll would
    destroy the button under the pointer, which then never hears `MouseOut`. A
    caption-less group is a preview: a card, but nowhere to go, hence `navGroups`
    beside `subButtons`.
    **Advanced mode** (`config.Interface.AdvancedMode`, the switch at the foot of
    the rail) keeps the page short. `p.adv(row)` returns nil in basic mode,
    `separateRows` drops nils — also closing the hole where a `sizeRow` for an
    unknown field reached a container — and `group` drops a card with nothing
    left. A `styleGroup` is gated whole rather than per row: each ends with its
    own reset button, so gating the sizes would leave a card holding only a way to
    undo them. It is read in `Rebuild`/`reload`, not per section, since a rail tap
    cannot change it and the two things that can both come through `reload`.
    `showSection` holds the fallback off `SectionAdvanced`, because About's reset
    turns the mode off from another section entirely.
    The controls are the client's own (`settings_controls.go` — see the footgun on
    Fyne's form widgets). A row with a *slider* stacks it under the description at
    full width (`stackedRow`, `newWideNumberControl`), 190 px not being enough to
    aim one; `sizeRow` stays inline, a line of table with no prose, of which
    Styles and Advanced mount a hundred. Everything else sits in a row's
    `fixedControl`, so a row is the same height whichever it holds — which is what
    lets `numberBox` swap its number for a `widget.Entry` without a layout jump.
    That swap is where a stale focus bites: focusing a second box makes the first
    report `FocusLost` *after* the second installed its field, so
    `numberBox.commit` ignores the reporting entry unless it is still the open
    one. The colour picker floats on `SettingsPage.popover`, inside the page's own
    layer, for the same reason the page isn't on the modal one.
    `newSettingsMarker` is the one bar saying "this is the open section" and "this
    setting is on". It is inset vertically by `SettingsGroupRadius` on every row
    rather than drawn full height: the group card is stacked *under* its rows, so
    a bar reaching a corner squares it off, and insetting only the end rows would
    need a row to know its own index and give three different bar lengths.
    Row copy is UI text, not commentary: the label names the setting and stands
    alone, the description says what changes in one plain sentence, and a row
    whose label is complete carries none. What the client does internally is a Go
    comment.
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
    — see the note — and `App.mutualProfile` resolves the IDs, handing the
    *totals* over beside the names: somebody the store cannot name is still one of
    the people in common, so the card's "+n" counts them rather than the total
    quietly shrinking to whatever is cached. Nothing is asked about this account,
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
    over** (`App.profileButtons`), not a func field per action: which of them
    apply is entirely a question about `domain.Relationship`, and the widget has
    no business knowing Revolt's states. "Message" is therefore *not* always
    offered — Revolt will not open a conversation with a stranger, so a stranger
    is offered "Add friend" instead, and a bot is the exception that is only ever
    written to. A `nil Do` draws the button **disabled** rather than leaving it
    out ("Request sent" is the state). `Danger` tracks the confirmation exactly:
    removing a friend and blocking are confirmed and drawn destructive; declining
    and withdrawing are neither, being undone by asking again. The compact card
    draws only the first button; the dialog draws them two to a row
    (`profileButtonRows`), an odd last one full width, so one action is not a
    different size depending on how somebody else stands with you. Every one of
    them closes the card first — a profile does not refresh while it is open, so
    one left up would go on offering what has just been done, and the notice is
    the only receipt.
    `Overflow` is where one is drawn rather than whether: blocking, removing and
    copying the ID go behind the card's **hamburger** (`profileMenuItems`, a
    `ui.GlyphButton` on the banner beside the close button), none of them being
    what a profile is opened for, and a row leading with a way to block the person
    it names says the wrong thing. `Icon` marks it there, already in the colour it
    is drawn in — which mark names an action is decided beside the action, and
    only the menu reads either field, so `ui.FriendsDialog` ignores both and still
    draws every button it is given. Copying the ID is added in `profileButtons`,
    not `relationshipButtons`: it is the one thing offered about *anybody*, this
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
    is exact because a colon is ordinary punctuation, and a looser match would
    turn "10:30:00" and every `:shortcode:` nobody serves a picture for into a
    blank square. It renders as a bare `canvas.Image` in a fixed square — no
    widget, so hover and the row's menu pass through as they do an embed's card —
    loaded from the emoji cache. The square is exactly `emojiSide`, one line of
    the text around it: RichText baseline-aligns a row as soon as its objects
    differ in height and reads the baseline of a segment it cannot measure as text
    as *zero*, so an emoji a pixel taller is moved down a whole baseline and draws
    through the line below. It is measured, not memoised through `lineHeight`,
    because it has to agree with that row exactly. Like a mention it can neither
    break nor be broken before, so it feeds `mdBuilder.reserve` too.
    **Picking one is `ui/emoji.go`**, one pop-up serving the composer's button and
    a message's add-reaction alike — both choose from the same set and differ only
    in what they do with the answer, hence `EmojiChoice.Value` (a reaction) and
    `.Token` (a body's `:ID:`). Nothing is fetched and neither emoji event is
    registered here — see the custom-emoji note: `Store.Emojis` is already the
    whole set and already current. `app.emojiGroups` therefore buckets **one**
    walk rather than asking per server.
    The drawn grid is capped (`emojiPickerLimit`) and the search field reaches
    past it; cells are memoised per emoji, so narrowing a query reorders objects
    that exist rather than rebuilding a hundred widgets and re-asking the cache
    for a hundred pictures per keystroke. Every name is folded **once**, when the
    picker opens (`foldGroups`): a keystroke asks the whole set whether it
    matches, and folding at the comparison would lower thousands of strings per
    character typed. `EmojiChoice.Keywords` is what a character answers to besides
    its name, searched and never drawn — "no" has to reach 👎 without the label
    over it reading "thumbs down no".
    That label is the only thing naming a cell, the grid being pictures, and it is
    a **tooltip** over the hovered square rather than a caption under the grid: a
    name read off the far end of the pop-up had nothing beside it saying which
    square it belonged to. It is the picker's *own* `ui.Tooltip`
    (`Tooltip.ShowAbove`, which centres it on the cell and keeps it inside the
    pop-up's width) — the app's is a layer in the window's content, and a pop-up
    is a canvas overlay drawn over all of that, so one mounted there would be
    covered by the grid it names. The grid scrolls in an `ObservableVScroll` held
    off the right edge by the indicator's own width, so the bar lands in a gutter
    rather than on the last cell of every row.
16. **Role colours.** A Revolt role colour is a CSS value, and the server's own
    presets are as often a gradient as a triple — hence `client.parseColor`
    reading *every* stop and `domain.Gradient` carrying them. A gradient is a
    `color.Color` answering as the mean of its stops, so a chip's dot, a reply's
    accent bar and a picker row keep filling one shape without knowing. Only
    `ui.AccentText` spreads one: a text object takes a single colour, so a
    gradient name is one object per rune, each measured off the whole name up to
    it (summing single glyphs drifts a fraction of a pixel each).
    **A gradient must never reach a `canvas.Text`.** Fyne keys its glyph-run
    texture cache on the text object's fields, colour included, so a fill that
    can't be a map key panics the painter on the frame it is first drawn — off the
    UI thread, where no recover of ours is on. That is **structural** rather than
    remembered: `ui.newText` / `newBoldText` are how a text object is built here
    and both flatten through `ui.solidColor`, so a call site cannot forget. A
    shape needs nothing, its texture being keyed by the object. `widgets_test.go`
    still asserts it over the built tree, the software painter a render test uses
    taking a different path and not noticing.
17. **Permissions.** `Store.Permissions(channelID)` / `ServerPermissions(serverID)`
    hand back a whole `domain.Permission` bitfield rather than a `CanX` per
    question: a call site asking three things should walk the roles once, and the
    interface would otherwise grow a method per bit Revolt defines. Zero — logged
    out, an unknown ID, a channel with no server — means "allow nothing".
    The arithmetic is `client.channelPermissions` / `serverPermissions`, taking
    plain `*revoltgo.Server`/`Member`/`Channel` values rather than reading
    `State`: that is what makes it testable at all, `State`'s caches being
    unexported. Order is load-bearing — server default, then the member's roles
    least senior *first* so the most senior has the last word, then the channel's
    default overwrite, then the channel's overwrites for those same roles, then
    the timeout clamp last so no overwrite can hand back what a timeout took. A
    **nil member** resolves as one holding no roles, not as no access: that is
    what Revolt computes for the default role and what revoltgo fabricates on
    `ServerCreate`, and refusing would empty the sidebar of a server just joined.
    `ViewChannel` is the one permission answered by **hiding** — `newChannelRow`
    returns nil, so the channel is not a row and (same walk) not a `#mention`
    candidate either, and `selectServer` opens on `firstVisibleChannel`. Only a
    server decides it: `App.canViewChannel` exempts conversations, which are in
    the user's own list because they are in them. `selectChannel` is where the
    checks pay for themselves — a channel it cannot see returns before
    `loadSlowmode` and `loadChannelMessages`, and `ReadMessageHistory` gates the
    page on its own, so neither request is sent to be refused.
    `SendMessage` **disables** the composer (`MessageInput.SetPermissions`), which
    is why the placeholder carries the reason: it is then the only thing left in
    the card. Typed text is kept. `UploadFiles` is checked in `AddAttachment`,
    where a drop and a paste both land, and reported through `OnRefused` — nothing
    else would happen, and nothing happening reads as a bug. A drop checks once
    for the whole batch, not per file.
    Nothing caches the answer. The lookups are `State`'s own RWMutex-guarded map
    reads and the questions are asked per channel switch, per hover and once a
    second at worst — while holding a `*revoltgo.ServerMember` would be both a
    data race (the gateway writes `Roles` in place) and a cache to invalidate.
    `onMemberUpdated` is the one event that can change the answer under a standing
    selection: for **our own** member it rebuilds the channel list and re-syncs
    the composer, a role gained or lost being what makes a channel appear.
18. **Parsing a body.** `markdown.Parse` classifies each line *once* into a
    `lineKind` — paragraph collection stops at anything that is not `lineText`, so
    a predicate per block type would be re-run per line — then hands each block's
    text to `parseInline` **whole**, newlines included. That is not tidiness: a
    Discord span crosses a hard line break, and a scanner given one line at a time
    can never match one. `LineBreak` is what the scanner emits at a `\n`.
    The scanner is a byte loop over an `inlineSpecial` table: an ordinary run
    costs no call and no copy, being emitted as a slice of the source, and
    `inlineScanner.buf` only exists once an escape has to be dropped out of a run.
    Everything else is delimiter matching, in `matchInline`. The **autolink**
    lives in the scanner rather than in it because a bare URL's scheme sits
    *behind* the `://` that announces it: the one construct matched by looking
    back, bounded by the pending run's start so it can't reach into a node already
    emitted.
    A `<t:1700000000:R>` **timestamp** is `markdown.Timestamp`, matched off `<`
    beside the mentions. Its style is validated against the letters Revolt defines
    rather than taken verbatim: an unknown one has no rendering, and falling back
    to the default would show the wrong face of the right instant where staying
    literal shows what was typed. A miss falls *through* to `matchAngleURL`, `t`
    being a scheme byte too. The style is carried rather than resolved — which
    face to draw is a question about the reader's clock, and this package has no
    config — so `util.MessageTimestamp` decides in `ui/markdown.go` and
    `PlainText` takes the plainest reading of the same instant. It renders through
    `mdBuilder.mention` with a **nil tap**, being a fact the client resolved
    rather than something the author typed; `mentionText.Cursor` keeps the hand
    off one that leads nowhere.
    A `Blockquote` holds **blocks**, not inlines, so `> # Note` is a heading and a
    quote marker among them nests; `mdBuilder.blockquote` builds them first and
    splices the bar in afterwards, a block's own non-inline break segment being
    the only thing that knows where a row ended. A `List` is one block whatever
    its depth — `ListItem.Indent` moves the marker column, nothing else — and
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
    searches (`visibleRange`). `MemberList` mounts only that window and
    **recycles** its rows: `MemberRow.SetMember` no-ops on unchanged state, so an
    overlapping scroll and a whole-model repaint both cost nothing per row that
    did not move. Keying the mounted map by *entry index* puts the same object
    back on the same entry. Nothing per-row may capture a member — `RowMenu` is
    one hook on the list taking a user ID, and both row callbacks read `w.userID`
    at the moment of the click.
    Ordering is one bucket index per member and no second sort: `Store.Members`
    has already ordered them (tie-broken on user ID so it is total) and bucketing
    is stable. An **offline member never appears in their hoisted role's section**
    — a hoisted section is a list of who is here — and an empty bucket emits no
    header. Presence is the only event that reorders, so `PresenceChanged` goes
    through the refresh queue (item 22) while `UserUpdated` repaints one row in
    place.
    Following presence at all is a setting; so are hoisting, hiding the offline
    half, hiding members with no role, the settling window and the overscan. The
    two hiding settings meet in `MemberListOptions.hides`, asked by both branches
    of the model before anything decides where a member would have gone. Roleless
    is **not** `HoistRoleID == ""` — a member holding only an unhoisted role has
    none — hence `domain.Member.HasRoles`, which counts a role the server has not
    published. Those two settings are also the one thing that can empty the list
    on a server they were never chosen for, and an empty sidebar is
    indistinguishable from a fetch that failed: `FallbackToAll` draws everybody
    instead, which is why `NewMemberModel` wraps `memberModel` rather than being
    the walk itself. The retry is guarded on the first pass having produced
    *nothing* and on a filter having been on, so a server that really is empty
    stays empty and one nothing was hiding is never walked twice.
    **A hidden sidebar skips the model build entirely** (`App.memberStale`, caught
    up by `toggleMemberList`) but never the walk — the mention picker is fed off
    it, including people the list hides.
    **The strip above the list is what speaks when the rows cannot.**
    `ui.MemberListStatus` takes its own height off the top of the column, not in
    place of it and not centred: the list is paint-then-fill, so saying
    "refreshing" must not take away the members already there, and a message in
    the middle would sit among the rows the moment there were any. It is **not**
    an overlay — laid over the rows through `NewLayer` it cut the first avatar and
    name in half, the mounted window being drawn from the column's own origin — so
    `MemberList` holds its `NewFillColumn`, and `SetStatus` re-lays out and
    re-mounts, the strip appearing being a shorter viewport. `App` decides it in
    `memberStatusFor`, a pure function kept apart from the widget so the
    precedence can be tested: a fetch in flight outranks a failure a retry has
    just cleared, and a failure outranks an empty list, "nobody to show here" for
    a membership that never arrived being a claim nothing on screen contradicts.
    `updateMemberStatus` is the only writer, called from every side that can move
    either half — four call sites each setting a message is four chances to leave
    the sidebar loading something that has landed. `memberFetchTimeout` is a
    `const`, not a setting (what the user would be choosing is how long to watch a
    sweeping line before being told nothing came) and it cancels **nothing**:
    revoltgo's REST layer takes no context, so the request is still out and a late
    answer still installs. The mark is `ui.TypingMark`, so "something is
    happening" is one shape in this client; it is built once with the strip rather
    than per status, and `MemberList.SetSweeping` stops it when the column is
    hidden or the tree holding it is replaced — see the footgun.
20. **Typing indicators.** `client.TypingChanged` is the one event that carries
    its value rather than naming what moved: `revoltgo.State` does not model
    typing, so no store answers who is typing where and the reader keeps it —
    `App.typing` (channel → user → expiry), the same shape as `slowmodeUntil`.
    Every channel is tracked, not only the open one, because the sidebar marks the
    others; nothing outlives `typingLifetime`, so it cannot grow. One
    `typingTimer` is re-armed to the **next expiry across all channels** rather
    than ticking, the line changing only when somebody lapses, and `pruneTyping`
    reports which channels emptied so only those repaint. Revolt sends no stop
    before a message, so `onMessageCreated` forgets its author.
    `typingPhrase` names the people and **nothing else** — the mark beside them
    says they are typing, so the line is `Alice, Bob +2` rather than a sentence
    repeating the mark in the longest form available. A name not resolved yet is
    *counted* rather than named (`hidden` covers both that and everyone past the
    limit, hence `Someone` / `3 people` with nothing to name), and the line
    redraws when `flushAuthors` or `UserUpdated` fills the gap — `onUserUpdated`
    asking `App.typing` first, since account updates arrive continuously and a
    redraw resolves every typist in the channel.
    The **open channel's row is never marked** (`isTypingIn`): its line above the
    composer already names them, and a row that could be marked while it is the
    open one is a row nothing puts back — the mark would sweep there until some
    unrelated sidebar-wide sync cleared it — so `showTyping` sends that channel to
    the line instead. Selecting a channel and leaving one both run that sync.
    Sending re-announces at most once per `typingSendInterval` and takes itself
    back on an empty composer, a submit, a channel switch, or `typingIdleTimeout`
    of quiet. `MessageInput.OnTyping` reports *whether text survived* the
    keystroke, one callback rather than a pair, and rides the typing methods
    beside `syncMentions` for the same reason they do.
    `TypingShowSelf` files this account among the typists from `noteSelfTyping`, a
    **local** echo rather than a reflected event: nothing guarantees Revolt sends
    our own typing back, and the preview is wanted whether or not we are
    announcing — so `typingChannelID` marks where we count as composing either
    way, and a zero `sentTypingAt` says nothing is owed a retraction. Only the
    first keystroke repaints; later ones move the expiry alone, a timer left armed
    at the older one costing a wake that prunes nothing and re-arms itself. We are
    named "You", first and out of the sorted order, and take a slot against the
    limit like anybody else.
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
21. **Pinning.** `Client.PinMessage` is the one action that writes the cache
    itself on success rather than optimistically or not at all, the gateway not
    being trustable to report the result — see the `EventMessageUpdate` note.
    Nothing is applied before the server agrees, so a refused pin leaves the row
    as it was; `App.OnPin` repaints, since `applyPinEvent` announces nothing when
    the echo tells it what it already holds (`markPinned` reporting false), which
    is what stops one pin redrawing twice.
    `canPin` asks for `ManageMessages` and does **not** fall back to authorship
    the way `canDelete` does: a pin is a change to the channel, not to the
    message, and Revolt refuses your own on that basis. It is offered in the
    context menu only — the hover quick-actions are what is done often enough to
    be worth a click without opening anything, which pinning is not.
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
    too — a row is a flattened summary, not a mounted message, so nothing here
    resolves a quote. Authors are resolved in the same worker rather than through
    `ensureAuthor`'s queue: the search route cannot be asked for the users (see the
    revoltgo note), and the alternative is a column of raw IDs filling in a moment
    later on every open.
    The panel is a **snapshot** — `App.pinned` for as long as it is up — so a pin
    made anywhere while it is open does not reflect. `App.pinsChannelID` is what
    drops an answer that lands after the reader has moved on. A row leads to its
    message through `OnJumpToMessage` and closes the panel on the way, a jump
    moving the column underneath it; unpinning goes through `App.setPinned` — the
    shared half of `OnPin`, with a hook, since the message a pin is taken off here
    need not be mounted at all — and the row is dropped only once the server has
    agreed. It is the one destructive-looking action not confirmed: repeating it
    puts the pin back.
22. **Events that only name what moved, and the refresh queue.** `ServerUpdated`,
    `RolesChanged`, `ChannelCreated`, `ChannelUpdated` and `ChannelRead` carry an
    ID and nothing else, revoltgo's own default handlers having already put
    `State` right by the time ours run. So a handler's whole job is to decide
    which surface is now wrong and let the rebuild re-read the store.
    Those rebuilds are **queued**, not made: `App.queueRefresh` sets bits in
    `App.dirty` (`refreshServers` | `refreshChannels` | `refreshMembers`) and arms
    one timer; `flushRefresh` runs each at most once, outermost column first. The
    window is armed by the *first* event of a burst and deliberately **not**
    restarted by the ones behind it — presence on a large server arrives faster
    than any window worth having, so a renewing one would never elapse. It is one
    knob (`RefreshDelayMS`), not one per surface: what the user is choosing is how
    long a burst may gather, and Revolt's bursts do not respect the boundary
    anyway — a rank reorder is an event per role, a channel added to a server is a
    create *and* a server update, and both would otherwise rebuild a sidebar twice
    for one change. Anything that changes only what is **open** — a header's text,
    the channel glyph, whether the composer takes a message — stays immediate: it
    is a setter and a permission lookup, and deferring it would make the client
    feel slow to save nothing. Same for a selection pointing at a channel that has
    just stopped existing, which must not survive a settling window.
    The three role events collapse onto one `RolesChanged` because a colour, a
    rank and a deletion all cost the same walk of the membership; creating a role
    arrives as an update for one `State` has never heard of, which revoltgo files
    on the way past, so there is no create to handle. `ChannelUpdated` rebuilds
    the whole channel sidebar rather than the one row: it announces permission
    overwrites too, and whether a channel is a row at all is `ViewChannel`'s to
    decide, which repainting in place cannot express. `ChannelRead` is the only
    event that exists because of *another client*; our own acks echo through it
    onto a mark already cleared.
    Two leaves are announced to everyone **except** as a deletion to the one who
    left, so both are recognised by their own user ID: `MembersChanged` for our
    own member *is* `ServerLeft` (revoltgo evicts the server from `State` on the
    strength of it), and `RecipientsChanged` for our own is `ChannelClosed`. Both
    are handed to the existing path, a no-op for something already gone.
    `RecipientsChanged` is a group conversation's own membership — the one channel
    whose participants are a list the client reads, being the `@mention` pool
    there — and `EventChannelGroupLeave` *embeds* the join event, the third pair
    in `client/events.go` that must therefore be registered twice.
    `UserRemoved` is an account taken off the platform; revoltgo has already
    dropped the user, their conversations and every membership, so the handler
    prunes the app's *own* order (`dmChannels`) of IDs the store no longer answers
    for and clears the selection if it was one of them.
23. **This account.** Presence and the status line beside it are **one object** to
    Revolt and it takes the whole of it, so whichever half is not being changed
    has to be read back out of `State` and sent again unchanged — either setter
    omitting the other's half would silently destroy it. `Client.editStatus` is
    that read-and-resend, shared by `SetPresence` and `SetStatusText`. Clearing
    the line is the one change that cannot be expressed as a value: an empty
    `Text` is *omitted* from the request, so it goes as `Remove: ["StatusText"]`.
    Length is clamped by rune to `MaxStatusText` rather than refused — the limit
    is Revolt's, and a failed send is worse than as much of it as fits. Nothing is
    recorded locally; the change returns as an ordinary `EventUserUpdate`, which
    is also what makes a presence set from another client arrive — so neither row
    writes back to its control, and `ui.commitEntry` reports on Enter and on blur
    rather than per keystroke, every report being a request. The picker offers
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
    update like anybody else's. The **profile** half arrives as nothing at all —
    it is not on the user record and no event announces it — so `App.selfProfile`
    is a snapshot `loadSelfProfile` fetches once per session, dropped after each
    edit and re-read through `App.editProfile`, which is also what tells the
    Banner row it now has something to remove. `ui.SettingsPage.SetProfile` fills
    those two rows the way a profile card is filled, and `commitEntry.Fill`
    refuses to overwrite a field somebody has already typed in.
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
    additionally removes this computer's saved login, which plain logout keeps:
    the token in it is one of the ones just revoked, so its card could only offer
    a sign-in that fails.
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
    `Client.React` writes the cache once the server agrees, exactly as
    `PinMessage` does and for a related reason: the gateway does echo a reaction
    back, but a chip the user just clicked has to answer now, so `applyReaction`
    reports "nothing moved" for the echo and the round trip costs one repaint.
    Everything reachable from a cached message is **replaced** on the way — the
    message, its reaction slice and the user list inside it — all three being read
    on the UI thread without the cache lock. `EventMessageUnreact` *embeds*
    `EventMessageReact` rather than aliasing it, so both are registered, as the
    typing pair is; `EventMessageRemoveReaction` is one emoji taken off wholesale.
    The row is drawn only for a message that carries one, which keeps a mounted
    page free of permission checks: chips have to know whether they answer a
    click, and asking that per message would be a lookup per row for something few
    of them have. Adding the *first* reaction is offered from the hover actions
    and the context menu instead, both of which read permissions lazily already. A
    chip declares hover for itself and therefore takes it from the row — innermost
    wins — so it reports back through `MessageWidget.overChild`, the hook the
    quick-action group uses; without it the buttons would vanish as the pointer
    crossed a chip on the way to them.
    **Clearing every reaction** (`OnClearReactions`) writes the cache and repaints
    from here for a harder version of the same reason: Revolt announces a clear as
    a message *update* carrying an empty map, which is indistinguishable from an
    edit, so nothing arrives to be believed. It is the one reaction action that is
    confirmed — it undoes other people's clicks — and the menu offers it only on a
    message that has some, an item that can never do anything being worse than no
    item.
    What it opens is the shared picker, through `Actions.OnPickEmoji` rather than
    directly: what is on offer is a walk of every server the account is in, which
    no widget knows. `ui.UnicodeEmoji` is the dozen characters offered under the
    servers' own — the ones that work in a conversation, where a custom emoji is
    still pickable but no server heading names it. A **custom** emoji renders in a
    chip either way: the ID is all a reaction carries, and `util.IsEmojiID` —
    exact ULID length, nothing looser — decides picture from character, a length
    range being enough to read a two-letter flag as an ID.
26. **Relationships.** `domain.User.Relationship` is how this account stands with
    somebody, and `Client.relations` is what keeps it true. Ready fills
    `revoltgo.User.Relationship` for everybody it names and **nothing keeps it
    current after that** — see the `EventUserRelationship` note. `relations` is
    the overlay that answers instead, the same shape `slowmode` is: read first,
    falling back to `State`, written by the gateway handler and by each action
    once the server has agreed, cleared with the session.
    `AddFriend` is the second action to go round revoltgo's typed API, and unlike
    `FetchSlowmode` it is a missing *route* rather than a missing field: Revolt
    takes a **sent** request at `POST /users/friend` naming the person by handle,
    while `PUT /users/{id}/friend` — revoltgo's `FriendAdd`, here `AcceptFriend` —
    accepts one that has already arrived. The two are not interchangeable and the
    wrong one of a stranger is a refusal with nothing to say why; the handle comes
    out of `State`, the caller having only an ID.
    `RemoveFriend` covers unfriending, declining and withdrawing alike, Revolt
    spending one route on all three — what it means is decided by where the
    relationship stood, which the button that raised it has already read to label
    itself. `blocked` is gone from `client/store.go`: `conversationPermissions`
    takes a `domain.Relationship` rather than a `*revoltgo.User`, which both
    routes it through the overlay and lets it be tested without one.
    `RelationshipChanged` reaches two surfaces. A block is what takes the composer
    away in an open DM (`Relationship.Blocked`, either direction); the other is
    the friends list. Nothing else draws a relationship — a member row says
    nothing about one, and a profile does not refresh while it is up.
27. **The friends list.** `Store.Relationships` is the only place relationships
    are seen as a set. It is a *walk*, not a lookup: Revolt files each
    relationship on the account it is with and sends no collection, so the set
    exists only as a property of the people in it — hence walking `State.Users()`
    and asking the relationship **before** resolving the account, most cached
    users being a member of some server and nothing more. Ordering is `Members`' —
    folded name, tie-broken on ID, so a row cannot swap out from under the pointer
    about to answer it.
    It is a dialog opened from `ui.FriendsRow`, above the conversations in the
    home sidebar, a relationship being a fact about somebody rather than about a
    server. The row is rebuilt with the sidebar — those objects are replaced
    wholesale — and marks itself the way an unread channel does when requests are
    waiting, that being the one part of the list that arrives unasked.
    The dialog **refills in place** (`SetSections`) rather than closing: accepting
    a request is an action whose entire result is the list changing, and every
    other answer is still up. That is what `App.relationshipButtons` is for — the
    profile card's own policy with the way out left open, so the two surfaces
    cannot come to offer different things about one person. A button drawn
    disabled is dropped here, "Request sent" being what the heading above it
    already says, and only a section `Awaiting` an answer draws its first button
    emphasised — a coloured slab per row in a list that is mostly read would be
    the loudest thing in it.
    Somebody the gateway names that `State` has never cached has no name to draw —
    `EventUserRelationship` carries the account and nothing files it — so
    `friendsChanged` queues them through `ensureAuthor`, and `flushAuthors` refills.
28. **Jumping to a message.** Tapping a quoted line is `Actions.OnJumpToMessage` →
    `App.OnJumpToMessage`, three answers cheapest first: already mounted
    (`scrollToMounted`), in the channel's cached tail (`jumpWithinCache`), or a
    request for the page around it (`loadJumpWindow` → `Client.MessagesAround`,
    Revolt's `nearby`). Only the **open** channel is asked about — a reply names a
    message in the channel it was written in, so the two are the same by
    construction. A line that resolved to nothing leads nowhere: everything a
    mounted reply names has been asked for by the time it is drawn, so one still
    unresolved was deleted.
    None of the three page routes writes to the message cache, for the reason item
    5 keeps a quote out of it, and what comes back is held in the same
    `App.uncached` — which is what lets a quote *inside* a jump window resolve
    without a request of its own. So the window is not in the cache, and
    `loadMoreHistory` and `mountNewerFromCache` read that off the cache rather
    than off a flag: the top or bottom mounted message not being in it means
    `MessagesBefore`/`MessagesAfter`, no flag needed, and deep scrollback past the
    cache's own per-channel cap is the *same* situation reached another way (it
    used to prepend a non-contiguous page into the cache). `MessagesAfter` sorts
    oldest-first deliberately; Revolt's default with an anchor hands back the live
    tail instead. Nothing re-attaches explicitly: a newer page reaching into the
    cached tail leaves the bottom row inside it and the cache tier serves every
    scroll after that, which is also where `setJumped(false)` is reached.
    `App.jumped` exists only to offer the way back (`returnToPresent`), and
    `atOldest` is what the cache's own depleted flag is for a window that is not
    in it. The chip is in the **header**, not the badge row over the column:
    nothing in that row accepts a pointer and this is a button — and it is about
    which part of the channel is on screen, which is what the name beside it says.
    `revealMounted` centres the row and `MessageWidget.Flash` marks it. The wash
    is *not* a state the widget holds — `fill()` is untouched, so the row goes on
    answering the pointer and the last tick hands the background back — and it
    fades between two **opaque** colours: the palette writes straight alpha into
    `color.RGBA`, which Go composites as premultiplied (see `theme.Fade`), so a
    wash faded down its alpha darkens the row on the way out. Hovering stops it,
    the pointer arriving being the reader having found the row.
    `ObservableScroll.SyncContent` is what makes the scroll land: an offset is
    clamped against the size the content was last *laid out* at, and only a
    `Scroll.Refresh` updates that — which would re-wrap every mounted body.
29. **Channel search** is the pins panel with a query, and `search.go` is what is
    left once that is said: the same `ChannelSearch` route (`Client.SearchMessages`,
    the two being mutually exclusive on the wire — Revolt refuses `query` and
    `pinned` together), the same rows through `messageEntry`, the same refusal to
    cache what comes back, and the same author resolution in the fetching worker.
    It asks for **nothing** until Enter: a search is a request per query, so the
    field reports on submit rather than on every keystroke, and `App.searchQuery`
    is what drops the answer to a superseded one — a second Enter while the first
    is still out is the ordinary case, and the two can land in either order.
    `refillSearch` wraps every change to the panel in a `repositionOverlay`,
    unlike the pins one: replacing a list of results with "Searching..." is a
    change of height, and a centred card sized from its own minimum re-places for
    neither.
30. **Creating a server** is `ui.PromptDialog` — one field, because a name is all
    Revolt takes at creation — and it *replaces* the join dialog rather than
    stacking on it, the modal layer holding one card. Nothing in the response can
    be believed (see the revoltgo note), so the created server arrives exactly as
    a joined one does: `pendingJoin` marks the request and `onServerJoined`
    selects what the gateway brings.
31. **What a reaction chip says on hover** is who is in it, `domain.Reaction.Users`
    having carried the names all along with nothing drawing them. It is folded at
    the moment of the hover (`ui.reactorNames`), not at construction: a chip draws
    a count, a mounted page carries hundreds of them, and nobody is over more than
    one. The line is the typing indicator's — names, then `+n` for the rest —
    since somebody the store cannot name is still one of the people in it, and a
    row of raw IDs answers nothing. The label is the app's own `ui.Tooltip`,
    reached through `Deps.Tooltip` rather than through an action: where a label
    goes is a question about the widget being hovered. `clearMessages` takes it
    down, as `refreshServerList` does — a chip about to be dropped never reports
    the pointer leaving.

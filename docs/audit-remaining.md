# Audit pass — what is left

State at the end of the first session. Both repositories are on `main`, both
green (`build`, `vet`, `gofmt`; rgoclient also `test`), nothing pushed.

| Repo | Commits |
|---|---|
| rgoclient | `67738f2` WIP · `d860a69` Part B/C wave 1 · `6272154` patch count |
| revoltgo | `f010343` vet green · `2b52e4e` CI · `e6b6898` Part A wave 1 · `d521fe7` rate-limit claim |

Item ids are the audit plan's. Verification digests (every claim re-checked
against the real code, with current line numbers) are in the session scratchpad:
`revoltgo-verify-digest.md`, `rgoclient-verify-digest.md`.

## Settled: dispatch stays parallel

A1-3 proposed serialising gateway dispatch. It was applied, then **reverted on
Sentinel's call** — the trade was re-examined against the real gws source
(v1.10.1, `reader.go:283` -> `readQueue.Go` -> one goroutine per frame, bounded
by a semaphore at `NumCPU`) rather than the audit's description.

Reordering is mechanically real, but the window is one state mutation — a msgp
decode and a single guarded map write, microseconds — and two *related* frames
must land inside it. `handle` runs the state handler before the user handlers,
so the slow part (rgoclient's `emit` blocking on a full 64-slot channel) happens
after the cache is already updated. Serialising bought almost no throughput (the
state mutex serialises the expensive half regardless) and cost liveness: a
blocking handler would have stalled the read loop, and revoltgo's own Ready
handler does a blocking HTTP round trip.

Ruled out along the way: copy-on-write does **not** make parallel dispatch lossy.
The mutators hold the write lock across the whole read-clone-update-swap, so
concurrent mutators serialise properly — no lost updates.

The consequence is written down where it binds: the `ClientOption` block and
`OnMessage` in `websocket.go`, and rgoclient's root `CLAUDE.md`, which used to
promise `Client.Events()` in gateway order and no longer does. A handler must
derive from the store rather than reading one event as following another.

If it is ever revisited, the option neither side took is a single ordered worker
between the read loop and the handlers: ordered *and* non-blocking, at the cost
of a queue whose depth becomes an explicit backpressure decision.

## revoltgo — not done

Wave 2 lost three of its four agents to the session limit. `state.go` was
reverted mid-refactor; `session.go`, `auth.go`, `group.go`, `endpoints.go`,
`revoltgo.go` and `file.go` were never touched.

| Item | What |
|---|---|
| ~~A1-5~~ | **Done** (2026-09-09). `Session.selfbot` is an `atomic.Bool` |
| A1-9b | `SessionEdit` decodes the response into the **request** type. json/v2 ignores unknown members, so this fails *silently* — a zero-valued struct, no error |
| A1-9c | `UserFlags` decodes `{"flags":n}` into a bare `int`. Fails *loudly*, every call |
| A1-9d | `GroupCreate` decodes a group Channel into `*Group`; return `*Channel` and delete `Group` (exported signature change) |
| A2-15 | MFA login (ticket / allowed_methods / mfa_ticket) + `Session.AddFriend`. `LoginParams` already carries the ticket field from wave 1 — read it before adding another. rgoclient's `internal/client/auth.go` + `auth_test.go` are shapes a real server accepted |
| ~~A2-17b~~ | **Done** (2026-09-09). `handlerName` panics |
| A2-14b | State side of the `UserRelationship` default handler landed in wave 1; its registration line in `session.go` did not. The verbatim block is in the scratchpad's `revoltgo-wave1-report.md` |
| A3-20d | `mutualConnection` scans every server and channel per DM permission check. Needs a `userID → set[serverID]` index beside the member cache. **Attempted and reverted** — the agent's partial `stateMembers` reshape broke `permissions.go` |
| A3-21 | ~50 unused `URL*` constants in `endpoints.go` → `// Deprecated:` (exported, so marked not deleted). Count each by grep; do not trust the number |
| A3-19 | `//msgp:ignore` the REST-only params/response types and regenerate. Regeneration is verified reproducible, so this is mechanical — currently ~25k generated lines for a codec only the gateway uses |
| A3-22 | Tests: golden frames for `eventTypeFromMSGP` at 15/16/17 map entries, a `-race` State test, ratelimiter bucket-key and eviction, `APIError` shape. CI already runs `go test -race ./...` and passes vacuously |
| ~~—~~ | **Done** (2026-09-09). Every diagnostic goes through `logf`; the update check is silent until `SetLogger` |
| ~~—~~ | **Done** (2026-09-09). `Session.WS` is gone: the socket is an `atomic.Pointer` behind `Session.Socket()`, and rgoclient's two typing guards read that |
| — | `SyncSettings` set/fetch asymmetry: `/set` sends `map[string]string` with the timestamp as a `?timestamp=` query parameter; `/fetch` answers the `(int64, string)` tuple. `http.go` now models fetch correctly; `session.go` still sends the fetch shape both ways |

## Second pass (2026-09-09)

A sweep of every package for allocation and repeated work, after the first
pass. The tree was already tight — no `sort.Slice`, no `regexp`, no `[]rune`
on a hot path, the audio callback allocation-free and asserted so — and what
was left is below, applied:

- `ui/message.go` `SetFollowedByGroup` ended in `w.Refresh()`, re-uploading the
  row's avatar and every attachment on each live message that landed under a
  mounted row; it is `Relayout(w.content)`, as `RefreshRelativeTime` already was.
- `ui/markdown.go` `bodyRenderer.Objects()` built its slice per call — and the
  Fyne Label renderer under it builds *its* list per call too — on every paint
  and hit test of every mounted body. Composed once in `CreateRenderer`.
- `voice/share.go` `readShare` still went through `track.ReadRTP()`, an MTU
  buffer and a packet struct per RTP packet of a 12 Mbps share. It reads into
  `sharePacket`s the assembler pools, released one packet late because pion's
  AV1 depacketizer aliases an unfinished fragment into the payload it came in.
- `audio/ring.go` `PushAll`/`PopAll` copy in one or two `copy` calls rather
  than a masked index per element; `audio/mix.go` voices read their sound as
  `[]int16` through one `unsafe.Slice` view instead of decoding bytes per sample.
- `app/members.go` `recipientMembers` folds a name once rather than per
  comparison; `markdown/parser.go` presizes the two slices it grew by appending.
- revoltgo: the four items struck out above, and `routeKey` builds its bucket
  key in one sized `strings.Builder` rather than `Split`/`Join`.

Looked at and not taken: `navigation.go` rebuilding category headers per
channel event (small count, rare event); `subscribe.go` `applySpeakers`'s two
small allocations per speaker report (a shared scratch would need lksdk's
callback to be single-threaded, which is not promised); `store.go`
`memberColour` re-parsing a role colour per row (a cache keyed on role would
need a `RolesChanged` invalidation for a hex triple's worth of saving).

## rgoclient — not done

All seven are cross-package: the agent that found them did not own the other file.

| Item | What |
|---|---|
| ~~B2-8~~ | **Done differently.** `syncChannelKind` now ends in `syncCallIsland` (`messages.go:704`), so the one call site covers both and the comment above it says why |
| ~~C1-2~~ | **Done.** `MessageCache.Update` holds the lock across search+copy+apply+store; `Client.reviseMessage` is the one-line delegate its six call sites still go through |
| ~~C3-12~~ | **Closed as not worth the machinery** (2026-08-30). `markdown.Parse` measures ~390 ns for a typical line and ~37 µs for a 6.6 KB body (`docs/performance.md`), and a preview parse is per *mount*, not per frame. A `DocCache` through `ui.Deps` plus a config bound buys back microseconds at the cost of held ASTs and invalidation on edit — the same trade performance.md already rejected for the per-row cache. Reopen only with a profile showing preview parses in a frame |
| ~~C3-13~~ | **Already landed.** `refreshChannelList` takes `Store.ServerChannelPermissions(serverID)` — one pass per rebuild (`navigation.go:345`, `viewable`). The remaining per-call `Store.Permissions` sites are one-shot action checks, not loops |
| ~~B2-13~~ | **Done** (2026-08-30), without the generation: `fetchTextPreview`'s UI-thread callback now checks `CanvasForObject(preview) != nil` — the gifAnimator's own liveness guard — so a parse is never spent on a row rebuilt while the fetch was out |
| B2-14 | Doc note only. The typing sweep repaints the whole window at frame rate while anyone types; no cheap fix under Fyne's one-bool dirty model. Prose is in the scratchpad's `rgoclient-wave1-report.md` under `docsNote` |
| ~~C1-1~~ | **Confirmed and documented** (2026-08-30). revoltgo's `state.go` clone helpers are the COW mutators; `internal/client/CLAUDE.md` now states the contract — a pointer can go stale but never change under its reader — beside the `Session.State` note |

## C5 — workaround retirements, none started

Needs `go.mod` pointed at the local revoltgo first (nothing is pushed):
`replace github.com/sentinelb51/revoltgo => ../revoltgo`.

- `flagMentionsEveryone` / `flagMentionsOnline` locals in `convert.go` — A1-8 landed, `MessageFlags` are real bits now
- ~~`answeredGone` / `statusOf` / `Transient` string parsing in `actions.go`~~ **done** — `apiError` is `errors.As` on `*revoltgo.APIError` and `statusOf` reads `StatusCode`; nothing parses `Error()` any more. The slowmode `retry_after` notice landed with it (`client.SlowmodeRetry` → `App.holdSlowmode`)
- ~~`Client.Mutual`, `Client.ServerBans`, `Client.BanMember`~~ **done** — all three go through the fixed typed routes; the CLAUDE.md note and `Client.Mutual`'s stale "sent by hand" comment were rewritten (2026-08-30)
- `Client.relations` overlay, hand-rolled MFA bodies, `Client.AddFriend` raw POST — blocked on A2-14b/A2-15 above
- ~~`MessageSystem` actor~~ **done** — `By` and `Name` reach the line through `convert.go`, `domain.SystemMessage` and `ui/message.go`. `Subject()` is which account the line's one name stands for; `MessageWidget.NamesAny` replaced the single-ID compare in `refreshAuthorMessages`, a line being able to name two people

## Deliberately not done

- **C2-4** — the audio callback still takes the runtime channel lock every 10 ms period. The fix rests on a lane-occupancy measurement that cannot be taken without running the client, and the failure mode is a lane running dry with nothing reporting it. `docs/performance.md` now has "The one rule it knowingly breaks", listing every alternative and why each is worse, and naming `runtime_Semrelease`/`notifyList` as the primitive that would fix it and is unreachable. Take it with an occupancy number in hand.
- **C3-13's reaction-row half** — the instruction was wrong on the code side. `canReact` decides whether the add-reaction chip is drawn *at all*, so deferring the read defers the chip: either an add chip appears for someone who cannot react, or it is missing for someone who can. The misleading comment was corrected instead.
- **B4 optimistic local echo** — out of scope by your call.

## Two claims the audit got wrong

- `A1-4c` is half right. `session.go` had a genuine blank-line defect (fixed); `http.go` and `revoltgo.go` were CRLF artefacts. revoltgo has no `.gitattributes` and `core.autocrlf=true`, so read `gofmt -l` there as *which* files, and check a named one on an LF-normalised copy.
- `A1-6`'s race is not reachable — `WriteClose` cancels the context before it takes the lock, so `connect` cannot install a socket on a closed session. The dial was still moved out of the lock, but not as a race fix.
- `B1-5`, `B3-20` and `C3-17-cache-d` are not bugs. Do not re-open them.

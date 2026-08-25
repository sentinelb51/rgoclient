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

## Do this first

**revoltgo A1-3 follow-through — a regression I introduced and did not close.**
Gateway dispatch is now serial (`ParallelEnabled: false`), which is correct:
ordering is the gateway's contract and a join could previously apply after its
own leave. But a handler now runs *on the read loop*, and the `EventReady`
default handler at `session.go:196-215` calls `s.User("@me")` inline — a full
HTTP round trip plus rate limiter. The read deadline is `HeartbeatInterval*2`
(60s) and the REST timeout is 10s, so one round trip cannot reach it today; the
hazard is that nothing says so. Either move the self-fetch off the read loop
(mind the ordering constraint: `Self()` must be populated before user handlers
see Ready, so a bare goroutine is wrong) or record in a comment why one round
trip at connect time is safe. Same class downstream: rgoclient's `Client.emit`
blocks on a 64-slot channel, so a stalled UI thread now stalls the socket.

## revoltgo — not done

Wave 2 lost three of its four agents to the session limit. `state.go` was
reverted mid-refactor; `session.go`, `auth.go`, `group.go`, `endpoints.go`,
`revoltgo.go` and `file.go` were never touched.

| Item | What |
|---|---|
| A1-5 | `Session.selfbot` written from the gateway, read on every REST call → `atomic.Bool`. The `Selfbot()` accessor already exists and `http.go` already calls it; only the field and its three writes remain |
| A1-9b | `SessionEdit` decodes the response into the **request** type. json/v2 ignores unknown members, so this fails *silently* — a zero-valued struct, no error |
| A1-9c | `UserFlags` decodes `{"flags":n}` into a bare `int`. Fails *loudly*, every call |
| A1-9d | `GroupCreate` decodes a group Channel into `*Group`; return `*Channel` and delete `Group` (exported signature change) |
| A2-15 | MFA login (ticket / allowed_methods / mfa_ticket) + `Session.AddFriend`. `LoginParams` already carries the ticket field from wave 1 — read it before adding another. rgoclient's `internal/client/auth.go` + `auth_test.go` are shapes a real server accepted |
| A2-17b | Six `log.Fatalf` in `AddHandler` registration → `panic`. A library must never `os.Exit` |
| A2-14b | State side of the `UserRelationship` default handler landed in wave 1; its registration line in `session.go` did not. The verbatim block is in the scratchpad's `revoltgo-wave1-report.md` |
| A3-20d | `mutualConnection` scans every server and channel per DM permission check. Needs a `userID → set[serverID]` index beside the member cache. **Attempted and reverted** — the agent's partial `stateMembers` reshape broke `permissions.go` |
| A3-21 | ~50 unused `URL*` constants in `endpoints.go` → `// Deprecated:` (exported, so marked not deleted). Count each by grep; do not trust the number |
| A3-19 | `//msgp:ignore` the REST-only params/response types and regenerate. Regeneration is verified reproducible, so this is mechanical — currently ~25k generated lines for a codec only the gateway uses |
| A3-22 | Tests: golden frames for `eventTypeFromMSGP` at 15/16/17 map entries, a `-race` State test, ratelimiter bucket-key and eviction, `APIError` shape. CI already runs `go test -race ./...` and passes vacuously |
| — | ~20 `log.Printf`/`log.Println` left in `session.go`, `auth.go`, `endpoints.go`, `revoltgo.go`, `file.go` → `logf`. `revoltgo.go`'s are an update check that prints to the program log unasked |
| — | `session.go:528` writes `s.WS` unsynchronised while other goroutines read it (`session.go:424`, `:541`, rgoclient `actions.go:386,395`). A real data race, found during the pass, not in the original audit |
| — | `SyncSettings` set/fetch asymmetry: `/set` sends `map[string]string` with the timestamp as a `?timestamp=` query parameter; `/fetch` answers the `(int64, string)` tuple. `http.go` now models fetch correctly; `session.go` still sends the fetch shape both ways |

## rgoclient — not done

All seven are cross-package: the agent that found them did not own the other file.

| Item | What |
|---|---|
| B2-8 | One line: `internal/app/events.go:497` should call `a.syncCallIsland()`, not `a.syncChannelKind()`. A channel's kind cannot change, so the glyph rebuild and `Relayout` are wasted on every channel update |
| C1-2 | `reviseMessage` is a non-atomic find-then-replace, so a gateway reaction and a background worker race and one silently wins. Needs `MessageCache.Update(channelID, messageID, change func(*domain.Message) bool) bool` doing search+copy+apply+store under one held lock — only `internal/cache` can hold the mutex across both halves |
| C3-12 | The AST memo. Down from four parses per body to one (two with an invite) without it, but `PreviewText` and `resolveReply` still parse per call — they are about *other* messages than the row being built, so no per-row cache reaches them. Needs a `DocCache` beside `TextCache`, through `ui.Deps`, bounded from `config.Settings` |
| C3-13 | `Store.Permissions` re-ranks the same member for every channel in a sidebar rebuild. The store must stay pure, so the memo belongs in `app/navigation.go`'s rebuild scope |
| B2-13 | `fetchTextPreview`'s liveness guard on the miss path: needs a generation on `MessageWidget` bumped by a release called from `MessageList.mount` and the drop sites |
| B2-14 | Doc note only. The typing sweep repaints the whole window at frame rate while anyone types; no cheap fix under Fyne's one-bool dirty model. Prose is in the scratchpad's `rgoclient-wave1-report.md` under `docsNote` |
| C1-1 | **Probably already fixed.** revoltgo's caches became copy-on-write, so a handed-out pointer can go stale but never change under its reader — which is exactly what the bare reads in `store.go`/`convert.go` needed. Confirm against the COW mutators, then say so in `internal/client/CLAUDE.md` rather than leaving the reads looking accidental |

## C5 — workaround retirements, none started

Needs `go.mod` pointed at the local revoltgo first (nothing is pushed):
`replace github.com/sentinelb51/revoltgo => ../revoltgo`.

- `flagMentionsEveryone` / `flagMentionsOnline` locals in `convert.go` — A1-8 landed, `MessageFlags` are real bits now
- `answeredGone` / `statusOf` / `Transient` string parsing in `actions.go` — `APIError` landed. **Its `Error()` string is deliberately byte-identical**, so the parser still works and the switch to `errors.As` is safe to make at leisure. This also unlocks the slowmode `retry_after` notice in `known-gaps.md`
- `Client.Mutual`, `Client.ServerBans`, `Client.BanMember` — verified **already fixed upstream**. `internal/client/CLAUDE.md:238-256` still calls them broken and is stale
- `Client.relations` overlay, hand-rolled MFA bodies, `Client.AddFriend` raw POST — blocked on A2-14b/A2-15 above
- `MessageSystem` actor: A2-16 landed upstream, so `By` can now reach the system line through `convert.go` + `domain.SystemMessage` + `ui/message.go`

## Deliberately not done

- **C2-4** — the audio callback still takes the runtime channel lock every 10 ms period. The fix rests on a lane-occupancy measurement that cannot be taken without running the client, and the failure mode is a lane running dry with nothing reporting it. `docs/performance.md` now has "The one rule it knowingly breaks", listing every alternative and why each is worse, and naming `runtime_Semrelease`/`notifyList` as the primitive that would fix it and is unreachable. Take it with an occupancy number in hand.
- **C3-13's reaction-row half** — the instruction was wrong on the code side. `canReact` decides whether the add-reaction chip is drawn *at all*, so deferring the read defers the chip: either an add chip appears for someone who cannot react, or it is missing for someone who can. The misleading comment was corrected instead.
- **B4 optimistic local echo** — out of scope by your call.

## Two claims the audit got wrong

- `A1-4c` is half right. `session.go` had a genuine blank-line defect (fixed); `http.go` and `revoltgo.go` were CRLF artefacts. revoltgo has no `.gitattributes` and `core.autocrlf=true`, so read `gofmt -l` there as *which* files, and check a named one on an LF-normalised copy.
- `A1-6`'s race is not reachable — `WriteClose` cancels the context before it takes the lock, so `connect` cannot install a socket on a closed session. The dial was still moved out of the lock, but not as a race fix.
- `B1-5`, `B3-20` and `C3-17-cache-d` are not bugs. Do not re-open them.

# RevoltGo Client Architecture

## Overview

A Fyne v2 desktop chat client (Discord-like) for Revolt, written in Go 1.26.
Uses `github.com/sentinelb51/revoltgo` for the REST API and gateway websocket.

## Core principle: explicit dependencies, no globals

There is no global session or cache. The `*app.App` controller owns the session
and both caches and passes what widgets need through a `ui.Deps` value:

```go
type Deps struct {
    Session *revoltgo.Session   // resolve users, system messages
    Images  *cache.ImageCache   // load avatars / icons / attachments
    Actions MessageActions      // user-interaction callbacks (implemented by *app.App)
}
```

Widget constructors take `Deps` (e.g. `ui.NewMessageWidget(deps, msg)`).
`util` helpers take an explicit `*revoltgo.Session` argument.

## revoltgo: Session vs State

- `Session.X(...)` — authoritative, always a network request. Use when data must
  be fresh or on a cache miss.
- `Session.State.X(...)` — local cache from gateway events / prior calls. Fast,
  zero-network, may return nil.

## Project structure

```
cmd/rgoclient/main.go        Entry point: Fyne app, theme, app.New(...).Run()

internal/
  app/                       Controller; owns session + caches + window + UI refs
    app.go                   App struct, New, Run, lifecycle, doOnUI, MessageActions impl
    session.go               startWithToken / startWithLogin + handler registration
    events.go                onReady / onMessage / onError
    navigation.go            Server+channel sidebar: build, refresh, select
    messages.go              Message area: build, load, display, send, history, viewer
    login.go                 Login view + login flow
    store.go                 Saved-session JSON persistence (~/.rgoclient_sessions.json)
  cache/
    image.go                 ImageCache — instance built by App; memory + disk + async load
    message.go               MessageCache — per-channel, oldest→newest, LRU channel eviction
  ui/
    theme/theme.go           Colors, Sizes, NoScrollTheme
    deps.go                  Deps struct + MessageActions interface
    layouts.go               Custom layouts + spacer helpers
    interactive.go           Shared tap/hover widgets (TappableContainer, HoverableStack,
                             iconButton, CloseButton, Avatar) built on tapBase
    scroll.go                ObservableScroll (wheel amplify + middle-button pan)
    server.go                Server icon widget
    channel.go               Channel row + collapsible category + drawn glyphs
    message.go               Message widget + content/attachments/replies rendering
    input.go                 Message input + attachments + replies + mention toggle
    sessioncard.go           Saved-session card
  util/
    message.go               DisplayName / DisplayAvatarURL / FormatSystemMessage (session arg)
    files.go                 Filetype + FormatFileSize
    timestamp.go             ULID Timestamp + NiceTime
    url.go                   IDFromAttachmentURL
```

## Data flow

1. Login (`login.go`) → `startWithToken` / `startWithLogin` → `openSession` registers
   handlers and opens the gateway.
2. `onReady` → save pending token, record unreads, `showMainUI`, `refreshServerList`,
   `selectServer(first)`.
3. `selectServer` → `refreshChannelList` → `selectChannel(first)`.
4. `selectChannel` → show cached messages, else `loadChannelMessages`; ack unread.
5. `onMessage` → cache append → append to open channel, else mark unread.

## Conventions

- Pass dependencies via `ui.Deps`; never reach for global state.
- Background goroutines update the UI through `App.doOnUI(fn, wait)`.
- Widget receiver is `w`; app receiver is `a`; cache receiver is `c`.
- Interface assertions live near the type: `var _ fyne.Tappable = (*T)(nil)`.
- Colors and sizes come from `ui/theme`, never hardcoded.
- Use the `log` package for diagnostics.

## Update requirements

Update this file when adding files/packages, changing data flow, adding widgets,
modifying `App` fields, or changing event handling.

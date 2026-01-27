---

# RevoltGo Client Architecture

## Overview

Fyne-based Go 1.25.5 chat client (Discord-like). Uses `github.com/sentinelb51/revoltgo` for API/websocket.

## Quirks

Use `app.GoDo()` for background UI updates if ChatApp is in scope
Use fmt.Sprintf() over "+" for string concatenation
Use generics for slices/maps when appropriate (slices.Reverse)

## Global Session Access

Use `context.Session()` to access the current session from anywhere
- Thread-safe with RWMutex
- Returns nil if no session active
- No need to pass session through function parameters

## Session (ex: Session.User("id"))
Authoritative API access.
Always performs network requests.
Use when data must be fresh or cache misses.

## State (ex: Session.State.User("id"))
Local cache populated by gateway events and prior API calls.
Fast, zero-network.
May return nil if the object is unknown or not cached.


## Project Structure

```
cmd/rgoclient/main.go     - Entry point, initializes Fyne app
internal/
  context/
    session.go            - Global session context (thread-safe accessor)
  interfaces/
    actions.go            - MessageActions interface definition
  app/
    app.go                - ChatApp struct, state logic (SelectServer/Channel)
    auth.go               - Session persistence (JSON file storage)
    events.go             - WebSocket event handlers (Ready, Message, Error)
    login.go              - Login UI and saved session management
    messages.go           - Message loading, display, submission logic
    ui.go                 - UI layout building (server/channel lists)
  cache/
    images.go             - Image cache (memory + disk persistence)
    messages.go           - In-memory message cache per channel
  ui/
    theme/
      theme.go            - Colors, Sizes, NoScrollTheme
    widgets/
      category.go         - Collapsible category header
      channel.go          - Channel list item
      clickable.go        - ClickableImage, ClickableAvatar
      helpers.go          - GetAvatarInfo, GetServerIconInfo
      hoverable.go        - HoverableStack widget
      layout.go           - Layout helpers (VerticalCenterFixedWidth, NoSpacing)
      message.go          - MessageWidget container
      message_content.go  - Content building, attachments, text preview
      observable_scroll.go- Custom scroll container with callbacks
      server.go           - Server icon widget
      sessioncard.go      - SessionCard widget
      spacers.go          - Spacer helpers (NewHSpacer, NewVSpacer)
      swift_action.go     - Swift action button widget
      tappable.go         - TappableContainer wrapper
      xbutton.go          - X button for removing items
      input/
        attachments.go    - Attachment handling for input
        input.go          - Multi-line input with shift-enter
        mention.go        - Mention toggle button
        replies.go        - Reply preview cards
  util/
    files.go              - File utilities
    message.go            - Message helpers (DisplayName, FormatSystemMessage)
    timestamp.go          - Timestamp(); extract time from ULID
    url.go                - URL utilities
    
```

## Key Components

### ChatApp (internal/app/app.go)

- Main application state holder
- Manages Session, CurrentServer/Channel, UnreadChannels
- Tracks loading state (`isLoadingHistory`)
- Contains UI containers (serverListContainer, channelListContainer, messageListContainer)

### Theme (internal/ui/theme/theme.go)

- `Colors` struct: all UI colors (customizable)
- `Sizes` struct: all UI dimensions (customizable)
- `NoScrollTheme`: hides scrollbars

### Widgets

All widgets implement fyne.Widget + fyne.Tappable + desktop.Hoverable where applicable.

### MessageActions Interface (internal/interfaces/actions.go)

- Unified interface for message interactions
- Implemented by ChatApp
- Used by widgets to handle user actions (reply, delete, edit, etc.)
- Provides message resolution from cache

### Global Session Context (internal/context/session.go)

- Thread-safe session accessor using RWMutex
- `context.Session()` returns current session (or nil)
- `context.SetSession(s)` updates global session
- `context.ClearSession()` clears session on logout
- Optimized for high-frequency reads (message rendering)

## Data Flow

1. Login → StartRevoltSessionWithToken/Login → context.SetSession() → registerEventHandlers
2. onReady → serverIDs/unreads → RefreshServerList → SelectServer
3. SelectServer → RefreshChannelList → SelectChannel
4. SelectChannel → check cache → loadChannelMessages → clear unread
5. onMessage → cache message → AddMessage (current) OR mark unread
6. Widgets → context.Session() for user/message data (no parameter passing)

## Conventions

- Use `context.Session()` to access session (never pass as parameter)
- Use `util.DisplayName(message)` and `util.DisplayAvatarURL(message)` (no session param)
- Use `interfaces.MessageActions` for message interaction callbacks
- Use `w` for widget receiver (e.g., `func (w *CategoryWidget)`)
- Use `app` for ChatApp receiver
- Interface assertions at top of file: `var _ fyne.Widget = (*WidgetName)(nil)`
- Colors/Sizes in theme.go, not hardcoded
- Background goroutines use `app.GoDo()` for UI updates

## Update Requirements

**Agents must update this file when:**

- Adding new files/packages
- Changing data flow
- Adding new widgets
- Modifying ChatApp struct fields
- Changing event handling

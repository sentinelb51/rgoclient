Rewrite all explanations as terse implementation steps.
Prefix first line with "!"

Output format:

- Bullet points only
- Each bullet ≤ 8 words
- Verb-first
- No articles, no pronouns, no conjunctions

If unable to shorten, split into multiple bullets.
Before responding, compress each sentence to its minimal form.
Delete any word not required to identify the action.

---

# RGOClient Architecture

## Overview

Fyne-based Go 1.25.5 chat client (Discord-like). Uses `github.com/sentinelb51/revoltgo` for API/websocket.

## Quirks

Use `app.GoDo()` for background UI updates if ChatApp is in scope
Use fmt.Sprintf() over "+" for string concatenation
Use generics for slices/maps when appropriate (slices.Reverse)


## Project Structure

```
cmd/rgoclient/main.go     - Entry point, initializes Fyne app
internal/
  api/
    auth.go               - Session persistence (JSON file storage)
  app/
    app.go                - ChatApp struct, state logic (SelectServer/Channel)
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
      avatar.go           - SessionCard widget
      category.go         - Collapsible category header
      channel.go          - Channel list item
      clickable.go        - ClickableImage, ClickableAvatar
      helpers.go          - GetAvatarInfo, GetServerIconInfo
      message.go          - MessageWidget with attachments
      message_input.go    - Multi-line input with shift-enter
      server.go           - Server icon widget
      tappable.go         - TappableContainer wrapper
      xbutton.go          - X button for removing items
    animations/
      pulsing_text.go     - PulsingText and BigText helpers
  utils/
    timestamp.go          - Timestamp(); extract time from ULID
    
```

## Key Components

### ChatApp (internal/app/app.go)

- Main application state holder
- Manages Session, CurrentServer/Channel, UnreadChannels
- Contains UI containers (serverListContainer, channelListContainer, messageListContainer)

### Theme (internal/ui/theme/theme.go)

- `Colors` struct: all UI colors (customizable)
- `Sizes` struct: all UI dimensions (customizable)
- `NoScrollTheme`: hides scrollbars

### Widgets

All widgets implement fyne.Widget + fyne.Tappable + desktop.Hoverable where applicable.

## Data Flow

1. Login → StartRevoltSessionWithToken/Login → registerEventHandlers
2. onReady → serverIDs/unreads → RefreshServerList → SelectServer
3. SelectServer → RefreshChannelList → SelectChannel
4. SelectChannel → check cache → loadChannelMessages → clear unread
5. onMessage → cache message → AddMessage (current) OR mark unread

## Conventions

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

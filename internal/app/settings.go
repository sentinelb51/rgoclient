package app

// The settings page's controller half: opening and closing the layer, and the
// one thing a changed setting can need that the page cannot do for itself —
// rebuilding the client from the theme tables.
//
// The tables are read at widget construction, at some hundreds of call sites, so
// changing one repaints nothing. Applying a style means building the whole tree
// again, which is what showMainUI already does. That rebuild is deferred while
// the page is open: it covers the client, so nothing of it would be seen, and
// replacing the window's content under a slider that is mid-drag would take the
// slider with it. The page answers a drag with its own preview instead.

import (
	"log"
	"net/url"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	fynetheme "fyne.io/fyne/v2/theme"

	"RGOClient/internal/cache"
	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
)

/* Opening and closing */

// openSettings shows the settings layer over the client. Call on the UI thread.
func (a *App) openSettings() {
	if a.settings == nil {
		return
	}

	a.closeOverlay() // a lightbox left up would draw over the page it was opened from
	a.settings.Open()
	a.bindKeys()
}

// closeSettings takes the layer down and pays off whatever the session on it
// asked for. Call on the UI thread.
func (a *App) closeSettings() {
	if a.settings == nil || !a.settings.IsOpen() {
		return
	}

	a.settings.Close()
	a.bindKeys()

	if a.stylesDirty {
		a.restyle()
	}
	a.focusInput()
}

// bindKeys points Escape at the topmost layer that answers it: the modal layer
// first, since it draws over the settings page, then the page itself.
//
// A focused entry swallows keys before the canvas ever sees them, so Escape does
// nothing while the cursor is in a text field — which is why both surfaces keep a
// close button in view.
func (a *App) bindKeys() {
	var onEscape func()

	switch {
	case a.overlay != nil:
		onEscape = a.closeOverlay
	case a.settings != nil && a.settings.IsOpen():
		onEscape = a.closeSettings
	}

	if onEscape == nil {
		a.window.Canvas().SetOnTypedKey(nil)
		return
	}

	a.window.Canvas().SetOnTypedKey(func(event *fyne.KeyEvent) {
		if event.Name == fyne.KeyEscape {
			onEscape()
		}
	})
}

/* Applying */

// updateSettings records a change and carries out whatever takes effect at once.
// Two things do: the picture budgets, which live on the cache rather than being
// read from it, and the member list, whose shape is decided when it is built
// rather than per row — so the Members group would otherwise do nothing visible
// until somebody joined the open server.
func (a *App) updateSettings(mutate func(*config.Settings)) {
	config.Update(mutate)

	settings := config.Current().Cache
	a.images.SetLimits(imageLimits(settings))
	a.emojis.SetLimits(emojiLimits(settings))

	a.refreshMemberList()

	// The typing settings are read where they are drawn, so turning the limit to
	// zero or the animation off has to reach both surfaces now rather than at the
	// next event. Withdrawing consent to be seen typing is the one that cannot
	// wait to be noticed: it has to be said out loud, once, on the way out.
	behaviour := config.Current().Behaviour
	if !behaviour.SendTyping {
		a.stopTyping(a.typingChannelID)
	}
	if !behaviour.TypingShowSelf {
		a.forgetSelfTyping()
	}
	a.refreshTyping()
	a.syncChannelList()
}

// applyStyles rebuilds the theme tables from the settings. The client itself is
// repainted when the page closes; see the file comment.
func (a *App) applyStyles() {
	settings := config.Current()

	theme.Apply(settings.Styles.Colors, settings.Styles.Sizes)
	theme.SetFontSize(settings.Interface.FontSize)

	if a.settings != nil && a.settings.IsOpen() {
		a.stylesDirty = true
		return
	}

	a.restyle()
}

// restyle rebuilds the client from the tables as they now stand. Fyne's own
// widgets repaint from the theme, the client's own are constructed again, and
// the message column — which buildUI hands back empty — is re-mounted from cache.
func (a *App) restyle() {
	a.stylesDirty = false
	if a.mainRow == nil {
		return // still on the login screen; the tables are read when it is built
	}

	// The tree about to be replaced holds the typing marks, and a discarded widget
	// hears nothing for up to a minute — see releaseChannelRows. Both surfaces are
	// put back below from state that outlived the rebuild.
	a.releaseChannelRows()
	a.typingIndicator.Set("", nil, false)
	a.memberList.SetSweeping(false)

	a.fyne.Settings().SetTheme(theme.NewAppTheme(fynetheme.DefaultTheme()))
	a.window.SetContent(a.buildUI())

	if a.currentChannelID != "" {
		a.displayCached()
	}

	// The typing line is a fresh widget knowing nothing, and the channel rows are
	// the same objects with their renderers replaced — which stopped the pulse on
	// the way out. Both are put back from state that outlived the rebuild.
	a.refreshTyping()
	a.syncChannelList()

	a.styleNativeChrome(a.window)
}

// emojiShare is the slice of the picture budget the emoji cache is given. The
// settings name one number for what cached pictures may occupy, so the two caches
// divide it rather than the second quietly doubling it. An eighth is generous —
// an emoji is kilobytes where an attachment is megabytes — and what makes the
// split worth having is not its size anyway: it is that an afternoon of scrolling
// through pictures can no longer evict the handful of emoji every message is
// drawn with.
const emojiShare = 8

// imageLimits is the picture cache's budgets as the settings express them, less
// the emoji cache's share.
func imageLimits(settings config.Cache) cache.ImageLimits {
	emoji := emojiLimits(settings)

	return cache.ImageLimits{
		DiskBytes:   settings.ImageDiskBytes() - emoji.DiskBytes,
		MemoryBytes: settings.ImageMemoryBytes() - emoji.MemoryBytes,
		MaxEdge:     int64(settings.MaxImageEdge),
		Loaders:     settings.ImageLoaders,
	}
}

// emojiLimits is that share, decoded at the emoji cap rather than the settings'
// — see cache.EmojiMaxEdge.
func emojiLimits(settings config.Cache) cache.ImageLimits {
	return cache.ImageLimits{
		DiskBytes:   settings.ImageDiskBytes() / emojiShare,
		MemoryBytes: settings.ImageMemoryBytes() / emojiShare,
		MaxEdge:     cache.EmojiMaxEdge,
		Loaders:     settings.ImageLoaders,
	}
}

/* What the page is given */

// settingsHooks is everything the settings page needs from the controller. It is
// built once, in New, and the page holds it for the life of the process.
func (a *App) settingsHooks() ui.SettingsHooks {
	return ui.SettingsHooks{
		Deps:    a.deps(),
		Update:  a.updateSettings,
		Restyle: a.applyStyles,
		Close:   a.closeSettings,
		Confirm: a.confirm,

		Version: a.info.Version,
		Build:   a.info.Build,

		Sessions:         settingsSessions,
		ForgetSession:    a.forgetSession,
		LogOut:           a.logOut,
		LogOutEverywhere: a.logOutEverywhere,
		SetPresence:      a.setPresence,
		SetStatusText:    a.setStatusText,

		CacheDir:       func() string { return a.assetDir },
		ChooseCacheDir: a.chooseCacheDir,
		CacheStats:     a.cacheStats,
		ClearCache:     a.clearImageCache,

		ConfigPath: settingsPath,
		OpenPath:   a.openPath,
	}
}

// settingsSessions lists the saved logins for the Account section. A store that
// cannot be read is reported as empty: the section is informational, and the
// login screen already surfaces the failure.
func settingsSessions() []ui.SettingsSession {
	saved, err := LoadSessions()
	if err != nil {
		log.Printf("load sessions: %v", err)
		return nil
	}

	sessions := make([]ui.SettingsSession, len(saved))
	for i, session := range saved {
		sessions[i] = ui.SettingsSession{
			UserID:    session.UserID,
			Username:  session.Username,
			AvatarURL: session.AvatarURL,
		}
	}

	return sessions
}

// forgetSession drops one saved login. Call on the UI thread.
func (a *App) forgetSession(userID string) {
	if err := RemoveSession(userID); err != nil {
		log.Printf("remove session: %v", err)
		a.notify(ui.ToneDanger, "Couldn't forget that login.")
		return
	}

	a.notifyNotice(ui.Notice{
		Tone:  ui.ToneInfo,
		Title: "Login forgotten",
		Body:  "Signing in as that account will ask for the password again.",
	})
}

// logOut ends the session and returns to the login screen, revoking the token on
// the way out rather than merely forgetting it here. Call on the UI thread.
//
// The revocation is a request and so cannot be made on this thread, but nothing
// waits on it: Client.Logout drops the session before it asks, and the screen is
// torn down here regardless. The two orderings that leaves are both safe —
// resetSessionState clears every selection the gateway handlers key on, so an
// event arriving in between finds nothing to paint into.
//
// A failure is logged rather than announced. The notice layer belongs to the main
// UI, which this is in the middle of replacing with the login screen, so there is
// nowhere left to say it — and nothing the user could do about it here anyway.
func (a *App) logOut() {
	a.closeSettings()

	a.background(
		a.client.Logout,
		func(err error) { log.Printf("revoke session: %v", err) },
	)
	a.resetSessionState()
	a.showLogin()
}

// logOutEverywhere revokes every session the account has, this one included, and
// returns to the login screen. Call on the UI thread.
//
// The saved login goes with it, which plain logout deliberately keeps: the token
// on this computer is one of the ones just revoked, so leaving its card on the
// login screen would offer a sign-in that can only fail. It is removed before the
// request rather than after — the identity is read off a session this is about to
// drop, and a failure to reach the server does not make the token any less dead.
func (a *App) logOutEverywhere() {
	a.closeSettings()

	if userID := a.store.SelfID(); userID != "" {
		if err := RemoveSession(userID); err != nil {
			log.Printf("remove session: %v", err)
		}
	}

	a.background(
		a.client.LogoutEverywhere,
		func(err error) { log.Printf("revoke all sessions: %v", err) },
	)
	a.resetSessionState()
	a.showLogin()
}

// setPresence publishes how this account appears. Nothing is painted here: the
// change comes back as an ordinary user update, which is also what makes it
// arrive when it was made from another client. Call on the UI thread.
func (a *App) setPresence(presence domain.Presence) {
	a.background(
		func() error { return a.client.SetPresence(presence) },
		a.notifyFailure("set presence", "Could not change your presence."),
	)
}

// setStatusText publishes the line beside the account's name, on the same terms
// as the presence beside it: nothing is painted, the gateway echo is what the
// store answers from. Call on the UI thread.
func (a *App) setStatusText(text string) {
	a.background(
		func() error { return a.client.SetStatusText(text) },
		a.notifyFailure("set status text", "Could not change your status."),
	)
}

// chooseCacheDir asks for a directory to keep cached images in. Fyne's folder
// picker is a canvas overlay, so it draws over the settings layer rather than
// under it; cancelling reports nothing at all. Call on the UI thread.
func (a *App) chooseCacheDir(onPicked func(path string)) {
	dialog.ShowFolderOpen(func(dir fyne.ListableURI, err error) {
		if err != nil {
			log.Printf("choose cache directory: %v", err)
			a.notify(ui.ToneWarning, "Couldn't open the folder picker.")
			return
		}
		if dir == nil {
			return
		}

		onPicked(dir.Path())
		a.notifyNotice(ui.Notice{
			Tone:  ui.ToneInfo,
			Title: "Cache location changed",
			Body:  "Images will be kept in " + dir.Path() + " from the next start.",
		})
	}, a.window)
}

// cacheStats measures the picture caches off the UI thread and reports back on
// it. The two are summed: they divide one budget, and it is that one number the
// settings page meters them against.
func (a *App) cacheStats(onDone func(cache.ImageStats)) {
	epoch := a.epoch

	go func() {
		stats := a.images.Stats().Add(a.emojis.Stats())
		a.doOnUI(func() {
			if !a.stale(epoch) {
				onDone(stats)
			}
		}, false)
	}()
}

// clearImageCache empties the cache in the background. Every avatar the client is
// still showing was decoded from it, so they are fetched again as they are next
// drawn — which is why nothing is repainted here.
func (a *App) clearImageCache() {
	a.background(func() error {
		a.images.Clear()
		a.emojis.Clear()
		return nil
	}, nil)

	a.notifyNotice(ui.Notice{
		Tone:  ui.ToneInfo,
		Title: "Image cache cleared",
		Body:  "Avatars, attachments and emoji are downloaded again as they are next drawn.",
	})
}

// settingsPath is where the settings file lives, or an explanation of why the
// client cannot say.
func settingsPath() string {
	path, err := config.Path()
	if err != nil {
		return "unavailable"
	}

	return path
}

// openPath hands a file to the system. Fyne only opens URLs, so a path has to be
// spelled as one — with forward slashes, which a Windows path does not have.
func (a *App) openPath(path string) {
	link := &url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(path)}
	if err := a.fyne.OpenURL(link); err != nil {
		log.Printf("open %s: %v", path, err)
		a.notify(ui.ToneWarning, "Couldn't open that file.")
	}
}

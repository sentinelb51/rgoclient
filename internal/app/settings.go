package app

// The settings page's controller half: opening and closing the layer, and the one
// thing a changed setting can need that the page cannot do — rebuilding the
// client from the theme tables.
//
// The tables are read at widget construction, at hundreds of call sites, so
// changing one repaints nothing. Applying a style means building the whole tree
// again. That is deferred while the page is open: it covers the client, so
// nothing would be seen, and replacing the window's content under a slider
// mid-drag would take the slider with it. The page previews instead.

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	fynetheme "fyne.io/fyne/v2/theme"

	"RGOClient/internal/cache"
	"RGOClient/internal/client"
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

// bindKeys points Escape at the topmost layer that answers it: the modal layer,
// which draws over the settings page, then the page. A focused entry swallows
// keys before the canvas sees them, so Escape does nothing while the cursor is in
// a field — which is why both surfaces keep a close button in view.
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

// updateSettings records a change and carries out what takes effect at once: the
// picture budgets, which live *on* the cache rather than being read from it, and
// the member list, whose shape is decided when it is built rather than per row —
// so the Members group would do nothing visible until somebody joined.
func (a *App) updateSettings(mutate func(*config.Settings)) {
	config.Update(mutate)

	settings := config.Current().Cache
	a.images.SetLimits(imageLimits(settings))
	a.emojis.SetLimits(emojiLimits(settings))

	a.refreshMemberList()

	// The typing settings are read where they are drawn, so turning the limit to zero
	// or the animation off has to reach both surfaces now rather than at the next
	// event. Withdrawing consent to be seen typing cannot wait: it has to be said out
	// loud, once, on the way out.
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
	// hears nothing for up to a minute — see releaseChannelRows.
	a.releaseChannelRows()
	a.typingIndicator.Set("", nil, false)
	a.memberList.SetSweeping(false)

	a.fyne.Settings().SetTheme(theme.NewAppTheme(fynetheme.DefaultTheme()))
	a.window.SetContent(a.buildUI())

	if a.currentChannelID != "" {
		a.displayCached()
	}

	// The typing line is a fresh widget knowing nothing, and the channel rows are the
	// same objects with their renderers replaced. Both are put back from state that
	// outlived the rebuild.
	a.refreshTyping()
	a.syncChannelList()

	a.styleNativeChrome(a.window)
}

// emojiShare is the slice of the picture budget the emoji cache is given. The
// settings name one number, so the two caches divide it rather than the second
// quietly doubling it. An eighth is generous — an emoji is kilobytes where an
// attachment is megabytes — and the size is not the point: the split is what stops
// an afternoon of scrolling evicting the handful of emoji every message is drawn
// with.
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
		SetDisplayName:   a.setDisplayName,
		ChangeUsername:   a.changeUsername,

		ChangeAvatar: a.changeAvatar,
		RemoveAvatar: a.removeAvatar,
		ChangeBanner: a.changeBanner,
		RemoveBanner: a.removeBanner,
		SetBio:       a.setBio,
		LoadProfile:  a.loadSelfProfile,

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

// logOut ends the session and returns to the login screen, revoking the token
// rather than merely forgetting it. Nothing waits on the revocation: Client.Logout
// drops the session before it asks, and the screen is torn down regardless — both
// orderings are safe, resetSessionState having cleared every selection the
// gateway handlers key on. A failure is logged rather than announced: the notice
// layer belongs to the UI this is replacing. Call on the UI thread.
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
// The saved login goes with it, which plain logout keeps: the token here is one
// of the ones just revoked, so its card would offer a sign-in that can only fail.
// Removed *before* the request — the identity is read off a session about to be
// dropped, and a failure to reach the server does not make the token less dead.
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

// setDisplayName publishes the name shown in place of the username, on the same
// terms as the two rows above. A name too short to send is the one failure worth
// naming: the client refused it before the request, so "could not" would be
// untrue and the reader has no other way to learn the limit. Call on the UI thread.
func (a *App) setDisplayName(name string) {
	a.background(
		func() error { return a.client.SetDisplayName(name) },
		func(err error) {
			if errors.Is(err, client.ErrDisplayNameShort) {
				a.notify(ui.ToneWarning, "A display name needs at least %d characters.", client.MinDisplayName)
				return
			}

			a.notifyFailure("set display name", "Could not change your display name.")(err)
		},
	)
}

/* This account's picture, profile and username */

// changeAvatar and changeBanner each ask for a picture and hang it on the
// account. The avatar comes back as a user update and repaints everything drawing
// it; the banner is announced by nothing, so it takes the profile path below.
func (a *App) changeAvatar() {
	a.choosePicture("Choose a profile picture", func(path, name string) {
		a.background(
			func() error { return a.client.SetAvatar(path, name) },
			a.notifyFailure("set avatar", "Could not change your picture. It may be too large."),
		)
	})
}

func (a *App) removeAvatar() {
	a.background(a.client.RemoveAvatar, a.notifyFailure("remove avatar", "Could not remove your picture."))
}

func (a *App) changeBanner() {
	a.choosePicture("Choose a profile banner", func(path, name string) {
		a.editProfile("set banner", "Could not change your banner. It may be too large.",
			func() error { return a.client.SetBanner(path, name) })
	})
}

func (a *App) removeBanner() {
	a.editProfile("remove banner", "Could not remove your banner.", a.client.RemoveBanner)
}

// setBio publishes the description on the account's profile. Blank removes it.
func (a *App) setBio(text string) {
	a.editProfile("set bio", "Could not change your description.",
		func() error { return a.client.SetBio(text) })
}

// editProfile runs one profile edit and re-reads the profile behind it. A read
// rather than a record of what was sent: a banner is an ID this client never
// sees, and the row offering to remove one has to know there is something to
// remove. Nothing announces either half, so asking again is the only answer.
func (a *App) editProfile(what, failure string, edit func() error) {
	epoch := a.epoch

	go func() {
		err := edit()

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err != nil {
				a.notifyFailure(what, "%s", failure)(err)
				return
			}

			a.selfProfileOK = false
			a.loadSelfProfile(a.fillSettingsProfile)
		}, false)
	}()
}

// loadSelfProfile hands over this account's bio and banner, fetching them the
// first time. Neither is on the user record and no event announces a change, so
// what is held is a snapshot for the session — dropped after an edit of our own,
// stale after one from another client. A failure answers nothing: empty rows are
// what an account with neither looks like, so the miss goes to the log.
func (a *App) loadSelfProfile(onLoaded func(domain.UserProfile)) {
	if a.selfProfileOK {
		onLoaded(a.selfProfile)
		return
	}

	userID := a.store.SelfID()
	if userID == "" {
		return
	}
	epoch := a.epoch

	go func() {
		profile, err := a.client.UserProfile(userID)
		if err != nil {
			log.Printf("fetch own profile: %v", err)
			return
		}

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}

			a.selfProfile, a.selfProfileOK = profile, true
			onLoaded(profile)
		}, false)
	}()
}

// fillSettingsProfile is loadSelfProfile's answer where nobody asked for it: the
// page may have been closed, or never opened, since the edit was made.
func (a *App) fillSettingsProfile(profile domain.UserProfile) {
	if a.settings != nil {
		a.settings.SetProfile(profile)
	}
}

// refreshSettingsAccount rebuilds the Account section when a user update moved
// something it draws and cannot put right in place — the picture, the handle. Not
// the display name: its field is on that section and Enter keeps the cursor in
// it, so an echo would rebuild the page under whoever was typing. UI thread.
func (a *App) refreshSettingsAccount() {
	self, ok := a.store.Self()
	if !ok || (self.AvatarURL == a.selfAvatarURL && self.Handle == a.selfHandle) {
		return
	}
	a.selfAvatarURL, a.selfHandle = self.AvatarURL, self.Handle

	if a.settings != nil {
		a.settings.RefreshAccount()
	}
}

// changeUsername asks for the new handle and the password Revolt takes with it.
// The card sits on the modal layer over the settings page, which is where a
// confirmation raised from that page goes too.
func (a *App) changeUsername() {
	self, ok := a.store.Self()
	if !ok {
		return
	}

	dialog := ui.NewPromptDialog(ui.Prompt{
		Title:  "Change username",
		Action: "Change",
		Busy:   "Changing...",
		Fields: []ui.PromptField{
			{Label: "New username", Placeholder: self.Username},
			{Label: "Current password", Placeholder: "Your account password", Password: true},
		},
		OnSubmit: a.submitUsername,
	}, a.closeOverlay)

	a.showOverlay(dialog.Content)
	a.prompt = dialog // after showOverlay, which clears the field
	a.window.Canvas().Focus(dialog.Entry)
}

// submitUsername changes the handle, leaving the card up until it has. The new
// name reaches the client as an ordinary user update, so nothing is drawn from
// the response.
func (a *App) submitUsername(values []string) {
	name, password := values[0], values[1]
	epoch := a.epoch

	go func() {
		err := a.client.SetUsername(name, password)

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err == nil {
				a.closeOverlay()
				a.notify(ui.ToneInfo, "Username changed.")
				return
			}

			log.Printf("change username: %v", err)
			if a.prompt != nil {
				a.prompt.Fail(usernameFailure(err))
			}
		}, false)
	}()
}

// usernameFailure is what the card says about a refusal. Only the client's own is
// worth spelling out: Revolt answers an unacceptable name and a taken one alike
// with a status code and no sentence, so the rest names both rather than guessing.
func usernameFailure(err error) string {
	if errors.Is(err, client.ErrUsernameInvalid) {
		return fmt.Sprintf("Use %d–%d letters, digits, dots, dashes or underscores.",
			client.MinUsername, client.MaxUsername)
	}

	return "Could not change it. Check your password, and try another name."
}

// choosePicture asks for an image file and reports what was picked, or nothing.
// Fyne's picker is a canvas overlay, so it draws *over* the settings layer — as
// the folder picker below does.
func (a *App) choosePicture(title string, onPicked func(path, name string)) {
	picker := dialog.NewFileOpen(func(file fyne.URIReadCloser, err error) {
		if err != nil {
			log.Printf("choose picture: %v", err)
			a.notify(ui.ToneWarning, "Couldn't open the file picker.")
			return
		}
		if file == nil {
			return
		}

		// Re-opened by the upload, off this thread: the client takes a path, as it does
		// for an attachment, and a reader held across a request nobody has made yet is
		// a handle with nothing to close it.
		uri := file.URI()
		_ = file.Close()

		onPicked(uri.Path(), uri.Name())
	}, a.window)

	picker.SetFilter(storage.NewExtensionFileFilter(pictureExtensions))
	picker.SetTitleText(title)
	picker.Show()
}

// pictureExtensions is what Revolt serves back as a picture. The filter is a
// courtesy — the server decides what it will take — so it stays generous.
var pictureExtensions = []string{".png", ".jpg", ".jpeg", ".gif", ".webp"}

// chooseCacheDir asks for a directory to keep cached images in; cancelling
// reports nothing. Call on the UI thread.
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

// clearImageCache empties the cache in the background. Every avatar still on
// screen was decoded from it and is fetched again as it is next drawn, which is
// why nothing is repainted here.
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

// openPath hands a file to the system. Fyne only opens URLs, so a path is spelled
// as one — with forward slashes, which a Windows path does not have.
func (a *App) openPath(path string) {
	link := &url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(path)}
	if err := a.fyne.OpenURL(link); err != nil {
		log.Printf("open %s: %v", path, err)
		a.notify(ui.ToneWarning, "Couldn't open that file.")
	}
}

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

	"RGOClient/internal/audio"
	"RGOClient/internal/cache"
	"RGOClient/internal/client"
	"RGOClient/internal/config"
	"RGOClient/internal/cpu"
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
// which draws over the settings page, then the page, then the message column,
// where it is the jump bar. A focused entry swallows keys before the canvas sees
// them, so this handler only ever speaks for what has no focused field of its own
// — which is why both overlays keep a close button in view, and why the composer
// carries its own Escape.
func (a *App) bindKeys() {
	var onEscape func()

	switch {
	case a.overlay != nil:
		onEscape = a.closeOverlay
	case a.settings != nil && a.settings.IsOpen():
		onEscape = a.closeSettings
	case a.serverSettingsOpen():
		onEscape = a.closeServerSettings
	case a.messages != nil:
		onEscape = a.escapeToPresent
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

	// The voice settings are read where they are used, so a slider dragged during a
	// call has to reach the devices already open rather than waiting for the next
	// join — and the output picker has to reach the engine whether or not there is
	// a call at all.
	a.applyVoiceSettings()
	a.syncChannelList()

	applyPacing()
	applyAffinity()
}

// applyPacing hands the frame settings to the toolkit. Both are patches to the
// patched Fyne — stock Fyne has no setting for either — and both are read by the
// driver on its next tick, so neither needs a restart.
func applyPacing() {
	performance := config.Current().Performance

	fyne.SetFrameRate(performance.FrameRate)
	fyne.SetVSync(performance.VSync)
}

// applyAffinity restricts the process to the cores the setting names.
//
// Unlike the frame settings this is nothing to do with the toolkit: it moves the
// whole process, so the gateway, the image loaders and miniaudio's callback go
// wherever the drawing does. That is why the page says as much, and why a machine
// with one kind of core is never asked.
func applyAffinity() {
	topology := cpu.Detect()

	cores := topology.All
	if topology.Split() {
		cores = coresFor(resolveCores(topology), topology)
	}
	if len(cores) == 0 {
		return // a platform that cannot pin, or would not say what it has
	}

	if err := cpu.Pin(cores); err != nil {
		log.Printf("pin to %d cores: %v", len(cores), err)
	}
}

// resolveCores is the setting with the machine held against it, and the whole
// of the policy — `cpu` reports the sides and never which to take. A value
// naming a side this machine has passes through; anything else — empty on a
// fresh install, or a value saved on a different kind of machine — becomes this
// machine's default and is written back, so the file and the page always name
// the set that actually runs. The default is the out-of-the-way side both
// times: the efficiency cores on a hybrid part, and CCD1 on a chiplet one,
// CCD0 being the chiplet that preferred-core scheduling and a game's cache
// steering usually favour. Only meaningful under Split().
func resolveCores(topology cpu.Topology) string {
	stored := config.Current().Performance.Cores

	resolved := config.CoresEfficiency
	if len(topology.CCD1) > 0 {
		resolved = config.CoresCCD1
	}

	switch stored {
	case config.CoresAll:
		resolved = stored
	case config.CoresEfficiency, config.CoresPerformance:
		if len(topology.Efficiency) > 0 {
			resolved = stored
		}
	case config.CoresCCD0, config.CoresCCD1:
		if len(topology.CCD0) > 0 {
			resolved = stored
		}
	}

	if resolved != stored {
		config.Update(func(s *config.Settings) { s.Performance.Cores = resolved })
	}

	return resolved
}

// coresFor maps the resolved setting to the machine's processors. resolveCores
// has already collapsed anything foreign, so an unknown value here is All.
func coresFor(mode string, topology cpu.Topology) []int {

	switch mode {
	case config.CoresEfficiency:
		return topology.Efficiency
	case config.CoresPerformance:
		return topology.Performance
	case config.CoresCCD0:
		return topology.CCD0
	case config.CoresCCD1:
		return topology.CCD1
	}

	return topology.All
}

// settingsCPUCores describes the machine's cores to the settings page. The counts
// are all it draws: which processors they are is this side's business.
func settingsCPUCores() ui.CPUCores {
	topology := cpu.Detect()

	return ui.CPUCores{
		Performance: len(topology.Performance),
		Efficiency:  len(topology.Efficiency),
		CCD0:        len(topology.CCD0),
		CCD1:        len(topology.CCD1),
	}
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
	a.restoreFriendsPage()

	// The typing line is a fresh widget knowing nothing, and the channel rows are the
	// same objects with their renderers replaced. Both are put back from state that
	// outlived the rebuild.
	a.refreshTyping()
	a.syncChannelList()

	// So is the call island, which starts hidden: a restyle under a running call
	// would otherwise leave the reader in one with nothing on screen to leave it
	// from. A connected call needs no SetState to come back — a fresh pill is a
	// green dot and no word, which is what Connected looks like.
	a.syncCallIsland()

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

		InputDevices:      a.inputDevices,
		OutputDevices:     a.outputDevices,
		StartInputMonitor: a.startInputMonitor,
		StopInputMonitor:  a.forgetInputMonitor,
		GateRatio:         audio.GateRatio,

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

		LoadSecurity:   a.loadSecurity,
		ChangePassword: a.changePassword,
		ChangeEmail:    a.changeEmail,
		EnableTOTP:     a.enableTOTP,
		DisableTOTP:    a.disableTOTP,
		RecoveryCodes:  a.recoveryCodes,
		RenewRecovery:  a.renewRecovery,
		RenameLogin:    a.renameLogin,
		RevokeLogin:    a.revokeLogin,
		RevokeOthers:   a.revokeOthers,

		Sounds:      settingsSounds,
		ChooseSound: a.chooseSound,
		ResetSound:  a.resetSound,
		PlaySound:   a.previewSound,

		CacheDir:       func() string { return a.assetDir },
		ChooseCacheDir: a.chooseCacheDir,
		CacheStats:     a.cacheStats,
		ClearCache:     a.clearImageCache,

		CPUCores: settingsCPUCores,

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
// The challenge in front of it is Revolt's: every route that ends a session is
// ticket-gated, so the question is asked before anything is torn down — a card
// raised over a client that had already returned to the login screen would be
// asking about an account it no longer knows.
func (a *App) logOutEverywhere() {
	a.challenge("Signing every device out of this account needs you to confirm it's you.",
		func(ticket string) {
			a.closeSettings()

			if userID := a.store.SelfID(); userID != "" {
				if err := RemoveSession(userID); err != nil {
					log.Printf("remove session: %v", err)
				}
			}

			a.background(
				func() error { return a.client.LogoutEverywhere(ticket) },
				func(err error) { log.Printf("revoke all sessions: %v", err) },
			)
			a.resetSessionState()
			a.showLogin()
		})
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

	a.showPrompt(ui.Prompt{
		Title:  "Change username",
		Action: "Change",
		Busy:   "Changing...",
		Fields: []ui.PromptField{
			{Label: "New username", Placeholder: self.Username},
			{Label: "Current password", Placeholder: "Your account password", Password: true},
		},
		OnSubmit: a.submitUsername,
	})
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
func (a *App) choosePicture(title string, onPicked func(path, name string)) {
	a.chooseFile(title, pictureFilter, onPicked)
}

// chooseFile asks for a file of the given kinds and reports what was picked, or
// nothing. The OS picker is the one worth showing — it is the dialog the reader
// already knows, and it is a window of its own rather than something drawn over
// the settings layer. Fyne's is the fallback where there is no native one.
func (a *App) chooseFile(title string, filter ui.FileFilter, onPicked func(path, name string)) {
	failed := func(err error) {
		log.Printf("choose file: %v", err)
		a.notify(ui.ToneWarning, "Couldn't open the file picker.")
	}

	if ui.PickFile(a.window, title, filter, func(path string, err error) {
		switch {
		case err != nil:
			failed(err)
		case path != "":
			onPicked(path, filepath.Base(path))
		}
	}) {
		return
	}

	// Fyne's picker is a canvas overlay, so it draws *over* the settings layer —
	// as the folder one below does.
	picker := dialog.NewFileOpen(func(file fyne.URIReadCloser, err error) {
		if err != nil {
			failed(err)
			return
		}
		if file == nil {
			return
		}

		// Re-opened by whatever reads it, off this thread: every caller takes a path,
		// and a reader held across work nobody has started yet is a handle with
		// nothing to close it.
		uri := file.URI()
		_ = file.Close()

		onPicked(uri.Path(), uri.Name())
	}, a.window)

	// An empty filter means every kind — Fyne's extension filter reads it as *no*
	// kind, so it is left unset rather than handed nothing.
	if len(filter.Extensions) > 0 {
		picker.SetFilter(storage.NewExtensionFileFilter(filter.Extensions))
	}
	picker.SetTitleText(title)
	picker.Show()
}

// pictureFilter is what Revolt serves back as a picture. The filter is a
// courtesy — the server decides what it will take — so it stays generous.
var pictureFilter = ui.FileFilter{
	Label:      "Pictures",
	Extensions: []string{".png", ".jpg", ".jpeg", ".gif", ".webp"},
}

// chooseCacheDir asks for a directory to keep cached images in; cancelling
// reports nothing. The OS picker on the same terms as chooseFile above.
// Call on the UI thread.
func (a *App) chooseCacheDir(onPicked func(path string)) {
	failed := func(err error) {
		log.Printf("choose cache directory: %v", err)
		a.notify(ui.ToneWarning, "Couldn't open the folder picker.")
	}

	chosen := func(path string) {
		onPicked(path)
		a.notifyNotice(ui.Notice{
			Tone:  ui.ToneInfo,
			Title: "Cache location changed",
			Body:  "Images will be kept in " + path + " from the next start.",
		})
	}

	if ui.PickFolder(a.window, "Choose a cache folder", func(path string, err error) {
		switch {
		case err != nil:
			failed(err)
		case path != "":
			chosen(path)
		}
	}) {
		return
	}

	dialog.ShowFolderOpen(func(dir fyne.ListableURI, err error) {
		if err != nil {
			failed(err)
			return
		}
		if dir == nil {
			return
		}

		chosen(dir.Path())
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

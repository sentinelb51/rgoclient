package app

import (
	"fmt"
	"iter"
	"log"
	"time"

	"fyne.io/fyne/v2"
	fynetheme "fyne.io/fyne/v2/theme"

	"RGOClient/internal/audio"
	"RGOClient/internal/client"
	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/voice"
)

/* Joining */

// joinCall opens the microphone and dials the channel's voice node. Never a side
// effect of selecting a channel: Revolt keeps messages in a voice channel, and a
// tap that opens a microphone is not a tap anybody expects. It is reached from
// the note under the header and from the channel's own menu.
//
// The REST call and the microphone happen off the UI thread; the dial happens in
// `then`, after the epoch check. a.backgroundThen guards `then` and not `fn`, so
// a join whose round trip outlives a logout would otherwise still connect.
func (a *App) joinCall(channelID string) {
	if a.callJoining || channelID == "" {
		return
	}
	// Already in this one — but only if there is something to be in. Between a
	// drop and its rejoin the channel is still named, and that gap is exactly when
	// the retry has to be let through.
	if a.callChannelID == channelID && (a.call != nil || a.callJoining) {
		return
	}

	// A join of anywhere else is the reader deciding where they want to be, which
	// retires whatever the reconnect was still trying to get back to.
	if channelID != a.callRetryFor {
		a.cancelRejoin()
	}

	if !a.canJoinCall(channelID) {
		a.notify(ui.ToneWarning, "You cannot join calls in this channel.")
		return
	}

	// A call already running elsewhere is left first: one microphone, one call.
	a.hangUp()

	a.callJoining = true
	a.callChannelID = channelID
	a.syncCall()

	settings := config.Current().Voice
	epoch := a.epoch
	gen := a.callGen

	// Everything that can block is on the worker, the dial most of all:
	// voice.Join waits out the whole connection handshake, which is seconds when a
	// voice node is not answering. Running it on the UI thread — which is what
	// backgroundThen's `then` would mean — freezes the window for exactly that
	// long, and a failing join is the case where it is longest.
	//
	// The staleness check therefore happens *after* the dial rather than before
	// it, on the hop back: a call that connected into a session that has since
	// gone is closed rather than never made. That is already how the microphone is
	// handled, and a call briefly connected and hung up is cheaper than a frozen
	// client.
	a.background(func() error {
		// force_disconnect: Stoat refuses a second connection for one account, so a
		// client that crashed mid-call cannot rejoin without it.
		creds, err := a.client.JoinCall(channelID, true)
		if err != nil {
			return err
		}

		capture, err := audio.OpenInput(settings.InputDevice, audio.InputConfig{
			Sensitivity:      settings.Sensitivity,
			Gain:             float32(settings.InputVolume) / 100,
			HighPass:         settings.HighPass,
			NoiseSuppression: settings.NoiseSuppression,
		})
		if err != nil {
			return err
		}

		// The speakers are opened before the dial. The engine otherwise opens them
		// on the first notification sound, and the device callback is both what mixes
		// the call's lanes and what asks for the next frame — so a call joined before
		// anything had rung would decode nothing and be silent. Both are safe from a
		// worker: one is a send to the engine goroutine, the other an atomic.
		a.sounds.StartOutput()
		a.sounds.SetCallVolume(float64(settings.OutputVolume) / 100)

		call, err := voice.Join(creds, capture, a.sounds.Sink(), voice.Options{
			Muted:    settings.JoinMuted,
			Deafened: settings.JoinDeafened,
			DeepPLC:  settings.DeepPLC,
			SelfID:   a.store.SelfID(),
		})
		if err != nil {
			capture.Close()
			return err
		}

		a.doOnUI(func() { a.installCall(channelID, epoch, gen, call, capture) }, false)

		return nil
	}, func(err error) {
		a.callJoining = false
		a.failedJoin(channelID, epoch, gen, err)
	})
}

// installCall is the join's last step, back on the UI thread. It is the only
// place a.call and a.capture are set.
//
// Both are closed rather than installed where the reader has given up in the
// meantime — a sign-out, or a hang-up while the dial was still out. The join is
// cancellable precisely because this check is here and not before the dial.
func (a *App) installCall(channelID string, epoch, gen uint64, call *voice.Call, capture *audio.Capture) {
	a.callJoining = false

	if a.stale(epoch) || a.callGen != gen {
		call.Close()
		capture.Close()

		return
	}

	a.call = call
	a.capture = capture
	a.muted, a.deafened = call.Muted(), call.Deafened()
	a.syncCall()
	a.armPushToTalk()

	// A meter running on a stream of its own is moved onto this one: two captures
	// on one device is a second open the backend may refuse.
	if a.monitor != nil && a.monitorOwned {
		a.restartInputMonitor()
	}

	go a.pumpCall(call)
}

// dropCall releases the media without giving up the channel: the call, the
// microphone and the key poll go, and callChannelID stays. That is what a call
// being reconnected looks like — the dock has not been left, so it stays on
// screen saying so. Call on the UI thread.
func (a *App) dropCall() {
	// Bumped first and unconditionally: the case this exists for is a hang-up
	// while the join is still in flight, when there is no call to close yet.
	a.callGen++
	a.callJoining = false
	a.disarmPushToTalk()

	if a.call != nil {
		a.call.Close() // there is no leave route: leaving *is* disconnecting
		a.call = nil
	}
	if a.capture != nil {
		a.capture.Close()
		a.capture = nil

		// The meter was reading that microphone. It needs one of its own now, or
		// the bar sits at whatever level the call ended on.
		if a.monitor != nil && !a.monitorOwned {
			a.restartInputMonitor()
		}
	}

	a.speaking = nil
	a.muted, a.deafened = false, false

	a.refreshSpeakingAll()
}

// hangUp leaves whatever call is running and closes the microphone. Safe to call
// with no call, which is what makes it usable from resetSessionState. Call on the
// UI thread.
//
// It does not cancel a reconnect — onCallEnded hangs up and then schedules one.
// Everything a reader reaches is leaveCall.
func (a *App) hangUp() {
	a.dropCall()

	a.callChannelID = ""
	a.syncCall()
}

// leaveCall is hanging up on purpose: the dock's button, the channel menu, the
// note under the header, a moderator disconnecting this account, and logging
// out. It gives up on getting back in, which is the whole difference from
// hangUp — a call somebody left must not reconnect itself.
func (a *App) leaveCall() {
	a.cancelRejoin()
	a.hangUp()
}

// canJoinCall reports whether this account may connect to a channel's call. Speak
// is deliberately not asked: a listener who may not talk is still allowed in, and
// the voice server refuses the publish rather than this hiding the button.
func (a *App) canJoinCall(channelID string) bool {
	channel, ok := a.store.Channel(channelID)
	if !ok || channel.Kind != domain.ChannelVoice {
		return false
	}

	return a.store.Permissions(channelID).Has(domain.PermissionConnect)
}

/* The pump */

// pumpCall drains one call's events for as long as it runs. A second pump rather
// than an arm on dispatch, for two reasons: client.Event's marker is unexported,
// so a voice event cannot be one; and that channel blocks rather than drops, so
// speaking updates would stall the gateway reader behind them.
func (a *App) pumpCall(call *voice.Call) {
	for event := range call.Events() {
		a.doOnUI(func() { a.onCallEvent(call, event) }, false)
	}
}

// onCallEvent handles one. The switch is exhaustive by construction:
// voice.Event's marker method is unexported.
func (a *App) onCallEvent(call *voice.Call, event voice.Event) {
	// An event from a call that has been replaced or hung up paints nothing — the
	// same check armTypingTimer makes against its own timer.
	if a.call != call {
		return
	}

	switch e := event.(type) {
	case voice.SpeakingChanged:
		a.onSpeakingChanged(e)
	case voice.ParticipantChanged:
		// The gateway announces the voice state and the sidebar is rebuilt from
		// that; this half only stops a departed participant being left ringing.
		if !e.Joined {
			a.onSpeakingChanged(voice.SpeakingChanged{UserID: e.UserID})
		}
	case voice.ConnectionChanged:
		a.onCallConnection(e)
	case voice.CallEnded:
		a.onCallEnded(e)
	}
}

// onSpeakingChanged marks one participant. It touches the rows for that user and
// nothing else: Canvas.dirty is one bool, so any Refresh repaints the whole
// window, and a call of eight people talking over each other must not be eight
// full repaints.
func (a *App) onSpeakingChanged(e voice.SpeakingChanged) {
	if a.speaking == nil {
		a.speaking = make(map[string]bool)
	}

	if a.speaking[e.UserID] == e.Speaking {
		return
	}

	if e.Speaking {
		a.speaking[e.UserID] = true
	} else {
		delete(a.speaking, e.UserID)
	}

	for row := range a.voiceRows() {
		if row.UserID() == e.UserID {
			row.SetSpeaking(e.Speaking)
		}
	}
}

// onCallConnection moves the dock's state line.
func (a *App) onCallConnection(e voice.ConnectionChanged) {
	// Back in the room, so whatever the reconnect was counting is spent.
	if e.State == voice.Connected {
		a.cancelRejoin()
	}

	if a.callDock == nil {
		return
	}

	a.callDock.SetState(e.State.String(), e.State == voice.Connected)
}

// onCallEnded tears the call down from the far end's side. The microphone is
// still ours to close, and a call that ended because something broke is worth
// getting back into rather than only complaining about.
//
// A nil Err is this client's own Close and is already accounted for.
func (a *App) onCallEnded(e voice.CallEnded) {
	if e.Err == nil {
		a.hangUp()
		return
	}

	channelID := a.callChannelID
	if channelID == "" || a.callRetry >= callRetries {
		a.hangUp()
		a.cancelRejoin()
		a.notifyFailure("call ended", "The call ended unexpectedly.")(e.Err)

		return
	}

	// The channel is kept, so the dock stays and says what is happening.
	a.dropCall()
	a.callRetryAfterDrop = true
	a.scheduleRejoin(channelID, e.Err)
}

/* Reconnecting */

// A voice server that drops the connection is not the same as a call being left,
// so it is rejoined. lksdk recovers a transient blip itself — that is what
// OnReconnecting/OnReconnected report — so reaching here means the room is gone
// and a fresh token is needed, which is JoinCall again with force_disconnect.
//
// The wait doubles per attempt so a server that is down is not hammered, and the
// count is small: past half a minute of silence a reader has worked out that the
// call ended and would rather be told.
const (
	callRetries    = 5
	joinRetries    = 3
	callRetryDelay = 2 * time.Second
	callRetryMax   = 30 * time.Second
)

// retryLimit is how many attempts the sequence now running gets. A call that
// dropped is worth more patience than a join that has not landed: the first was
// working a moment ago, and the second has somebody watching a button they
// pressed.
func (a *App) retryLimit() int {
	if a.callRetryAfterDrop {
		return callRetries
	}

	return joinRetries
}

// failedJoin is what every failed join does, first attempt or reconnect alike.
//
// A failure worth trying again starts or continues a sequence rather than
// reporting; only the last one is said out loud, so a join that succeeds on the
// second attempt is silent. client.Transient is what decides: a refusal —
// forbidden, no session — is an answer, and a timed-out dial or a 500 is not.
// Call on the UI thread.
func (a *App) failedJoin(channelID string, epoch, gen uint64, err error) {
	// The reader gave up while the dial was out — dropCall bumped the
	// generation, the mirror of installCall's check. Retrying would silently
	// rejoin a call they left, and a hang-up already cleared the state a
	// failure would otherwise clear, possibly for a *different* call joined
	// since. Nothing to report: they left.
	if a.callGen != gen {
		return
	}

	if !a.stale(epoch) && a.callRetry < a.retryLimit() && client.Transient(err) {
		// Either a reconnect still working, or a first join that failed for a reason
		// asking again can fix. The voice node answers a dial with a timeout often
		// enough, and the route with a 500, that one refusal is not an answer.
		log.Printf("join call %s failed (%v); trying again", channelID, err)
		a.scheduleRejoin(channelID, err)

		return
	}

	if !a.stale(epoch) {
		a.cancelRejoin()
		a.callChannelID = ""
		a.syncCall()
	}

	a.notifyFailure("join call "+channelID, "Could not join the call.")(err)
}

// scheduleRejoin arms the next attempt. Call on the UI thread.
func (a *App) scheduleRejoin(channelID string, err error) {
	if a.callRetryFor != channelID {
		a.callRetry = 0
	}

	a.callRetry++
	a.callRetryFor = channelID

	delay := min(callRetryDelay<<(a.callRetry-1), callRetryMax)
	log.Printf("rejoining %s in %v (attempt %d of %d, after %v)",
		channelID, delay, a.callRetry, a.retryLimit(), err)

	// Redrawn from a state with no call, which sets the line to "Connecting" —
	// right for a join that has not landed, and overwritten for one that had.
	a.syncCallDock()
	if a.callDock != nil && a.callRetryAfterDrop {
		a.callDock.SetState("Reconnecting", false)
	}

	epoch := a.epoch

	if a.callRetryTimer != nil {
		a.callRetryTimer.Stop()
	}

	a.callRetryTimer = time.AfterFunc(delay, func() {
		a.doOnUI(func() {
			// The reader may have left, joined elsewhere, or logged out while the
			// timer was pending; any of those retires this sequence.
			if a.callRetryFor != channelID || a.stale(epoch) {
				return
			}

			a.joinCall(channelID)
		}, false)
	})
}

// cancelRejoin retires the sequence. Safe with none pending, which is what lets
// leaveCall call it unconditionally. Call on the UI thread.
func (a *App) cancelRejoin() {
	if a.callRetryTimer != nil {
		a.callRetryTimer.Stop()
		a.callRetryTimer = nil
	}

	a.callRetry = 0
	a.callRetryFor = ""
	a.callRetryAfterDrop = false
}

/* The dock */

// syncCallDock matches the strip at the foot of the channel column to whatever
// the call is doing, and hides it when there is none. Hiding a child reclaims
// nothing on its own, so the column is relaid out either way. Call on the UI
// thread.
func (a *App) syncCallDock() {
	if a.callDock == nil {
		return
	}

	if a.callChannelID == "" {
		a.callDock.Hide()
		ui.Relayout(a.channelColumn)

		return
	}

	name := "Voice"
	if channel, ok := a.store.Channel(a.callChannelID); ok && channel.Name != "" {
		name = channel.Name
	}

	a.callDock.SetChannel(name)
	a.callDock.SetMuted(a.muted)
	a.callDock.SetDeafened(a.deafened)

	if a.call == nil {
		a.callDock.SetState("Connecting", false)
	}

	a.callDock.Show()
	ui.Relayout(a.channelColumn)
}

// syncCall is the whole of what a call starting or ending changes on screen: the
// dock at the foot of the channel column, and the strip under the message header
// if the open channel happens to be the one the call is in.
func (a *App) syncCall() {
	a.syncCallDock()

	if a.channelKind() == domain.ChannelVoice {
		a.syncVoiceNote()
		ui.Relayout(a.messageColumn)
	}
}

// toggleMute and toggleDeafen are the dock's two switches. Deafening implies
// muting, so both redraw the pair.
func (a *App) toggleMute() {
	if a.call == nil {
		return
	}

	a.call.SetMuted(!a.call.Muted())
	a.muted = a.call.Muted()
	a.syncCallDock()
}

func (a *App) toggleDeafen() {
	if a.call == nil {
		return
	}

	a.call.SetDeafened(!a.call.Deafened())
	a.muted, a.deafened = a.call.Muted(), a.call.Deafened()
	a.syncCallDock()
}

/* The note under the header */

// syncVoiceNote matches the strip under the message header to what this account
// can do about the open channel's call. It is the primary way in: the one surface
// already drawn only for a voice channel, already built once and already relaid
// out on every switch.
//
// Joining is never a side effect of selecting, so the button is what does it.
// Call on the UI thread.
func (a *App) syncVoiceNote() {
	if a.channelNote == nil {
		return
	}

	channelID, _ := a.currentChannel()

	switch {
	case a.callChannelID != "" && a.callChannelID == channelID.ID:
		a.channelNote.Set(voiceNoteJoined)
		// Warning rather than danger: leaving a call is undone by joining it again.
		a.channelNote.SetAction(ui.NewWeightedButton("Disconnect", ui.ButtonWarning, a.leaveCall))

	case a.canJoinCall(channelID.ID):
		a.channelNote.Set(voiceNote)
		// Primary: the strip is drawn for this channel and joining is what it is for.
		a.channelNote.SetAction(ui.NewWeightedButton("Join call", ui.ButtonPrimary,
			func() { a.joinCall(channelID.ID) }))

	default:
		a.channelNote.Set(voiceNoteClosed)
		a.channelNote.SetAction(nil)
	}
}

/* Rows */

// voiceRows walks the participant rows the channel sidebar has mounted, the way
// channelRows walks the channel ones. A row holds its own user ID, so nothing is
// captured per rebuild.
func (a *App) voiceRows() iter.Seq[*ui.VoiceParticipantRow] {
	return func(yield func(*ui.VoiceParticipantRow) bool) {
		for _, host := range [...]*fyne.Container{a.channelTop, a.channelList} {
			if host == nil {
				continue
			}

			for _, obj := range host.Objects {
				if w, ok := obj.(*ui.VoiceParticipantRow); ok && !yield(w) {
					return
				}
			}
		}
	}
}

// refreshSpeakingAll re-marks every mounted row from a.speaking. Called after the
// sidebar is rebuilt, the rows being new objects that know nothing of who was
// talking when the old ones were dropped.
func (a *App) refreshSpeakingAll() {
	for row := range a.voiceRows() {
		row.SetSpeaking(a.speaking[row.UserID()])
	}
}

/* The participant menu */

// voiceParticipantMenu is where all four per-person voice actions meet: the local
// volume, and the three moderation ones a permission may allow.
//
// Not on the profile card — every action there closes the card first, so a slider
// would be dragged out from under the pointer — and not on the member sidebar's
// own menu, which is the whole membership, most of whom are not in the call.
func (a *App) voiceParticipantMenu(channelID, userID string) []*fyne.MenuItem {
	items := []*fyne.MenuItem{a.userVolumeItem(userID)}

	channel, ok := a.store.Channel(channelID)
	if !ok || channel.ServerID == "" {
		return items
	}

	if moderation := a.memberVoiceItems(channel.ServerID, userID); len(moderation) > 0 {
		items = append(items, fyne.NewMenuItemSeparator())
		items = append(items, moderation...)
	}

	return items
}

// callVolumeSteps is what a per-person volume may be set to. A submenu of steps
// rather than a slider: a slider is not a fyne.MenuItem, and this needs no
// surface of its own.
var callVolumeSteps = []int{0, 50, 100, 150, 200}

// userVolumeItem is the local volume for one participant. It lives in the call's
// mixer and is deliberately not persisted: a voice too quiet today is a room
// rather than a preference.
func (a *App) userVolumeItem(userID string) *fyne.MenuItem {
	current := int(a.sounds.Sink().Gain(userID)*100 + 0.5)

	steps := make([]*fyne.MenuItem, 0, len(callVolumeSteps))
	for _, step := range callVolumeSteps {
		item := fyne.NewMenuItem(fmt.Sprintf("%d%%", step), func() {
			a.sounds.Sink().SetGain(userID, float64(step)/100)
		})
		item.Checked = step == current
		steps = append(steps, item)
	}

	item := fyne.NewMenuItemWithIcon("Volume", fynetheme.VolumeUpIcon(), nil)
	item.ChildMenu = fyne.NewMenu("", steps...)

	return item
}

/* Devices, for the settings page */

// inputDevices and outputDevices answer the settings page's two pickers. Both
// enumerate the backend, which is not UI-thread work — the page asks them from a
// worker and mounts the answer.
func (a *App) inputDevices() []ui.AudioDevice {
	devices, err := audio.Inputs()
	if err != nil {
		log.Printf("list microphones: %v", err)
		return nil
	}

	return toAudioDevices(devices)
}

func (a *App) outputDevices() []ui.AudioDevice {
	devices, err := audio.Outputs()
	if err != nil {
		log.Printf("list speakers: %v", err)
		return nil
	}

	return toAudioDevices(devices)
}

// toAudioDevices converts what the audio package knows into what a widget may
// see. `ui` must not import `audio`, so the shape crosses as a value the way
// ui.Keystroke does.
func toAudioDevices(devices []audio.Device) []ui.AudioDevice {
	out := make([]ui.AudioDevice, 0, len(devices))
	for _, device := range devices {
		out = append(out, ui.AudioDevice{
			ID:      device.ID,
			Name:    device.Name,
			Default: device.Default,
		})
	}

	return out
}

/* The settings meter */

// meterInterval is how often the level bar is redrawn. Levels arrive at the
// device's rate — 100 a second — and each repaint is the whole window, so this is
// the rate a meter can be *drawn* at rather than the rate it is measured at.
const meterInterval = 60 * time.Millisecond

// startInputMonitor points the settings page's level bar at a microphone. It
// borrows the call's capture where there is one and opens a stream of its own
// otherwise: WASAPI shared mode would grant a second open, but a device somebody
// else holds exclusively refuses it, and a reader adjusting the gate mid-call is
// exactly when the meter matters most.
//
// A monitor already running is stopped first, so a second section change cannot
// leave one behind.
func (a *App) startInputMonitor(report func(level float32)) {
	a.stopInputMonitor()

	capture, owned := a.capture, false

	if capture == nil {
		settings := config.Current().Voice

		opened, err := audio.OpenInput(settings.InputDevice, audio.InputConfig{
			Sensitivity:      settings.Sensitivity,
			Gain:             float32(settings.InputVolume) / 100,
			HighPass:         settings.HighPass,
			NoiseSuppression: settings.NoiseSuppression,
		})
		if err != nil {
			log.Printf("open microphone for the level meter: %v", err)
			return
		}

		capture, owned = opened, true
	}

	done := make(chan struct{})
	a.monitor, a.monitorOwned, a.monitorDone, a.monitorReport = capture, owned, done, report

	// An owned capture has no publisher behind it, and Level is stored by Read
	// and nowhere else — without a reader the bar never moves. The loop is paced
	// by the device (Read blocks a frame at a time) and ends when stopInputMonitor
	// closes the capture; the call's capture is the publisher's to read.
	if owned {
		go func() {
			frame := make([]int16, audio.FrameSamples)
			for {
				if _, err := capture.Read(frame); err != nil {
					return
				}
			}
		}()
	}

	epoch := a.epoch

	go func() {
		ticker := time.NewTicker(meterInterval)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
			}

			level := capture.Level()

			a.doOnUI(func() {
				// The page may have closed, or the session been replaced, between the
				// sample and the hop back.
				if a.monitorDone == done && !a.stale(epoch) {
					report(audio.MeterRatio(level))
				}
			}, false)
		}
	}()
}

// restartInputMonitor moves the meter onto whichever capture is right now — the
// call's when one has just started, its own when one has just ended. A no-op
// with no meter open, which is the usual case on both paths. Call on the UI
// thread.
func (a *App) restartInputMonitor() {
	if a.monitorReport == nil {
		return
	}

	a.startInputMonitor(a.monitorReport)
}

// stopInputMonitor closes that microphone. Called from the page's own two exits —
// showSection and Close — because the page has no unmount hook, and a settings
// page left with this running holds the input device open for the rest of the run.
func (a *App) stopInputMonitor() {
	if a.monitorDone != nil {
		close(a.monitorDone)
		a.monitorDone = nil
	}

	// Only a stream this opened is closed. The other case is the call's own
	// microphone, which the call is still using.
	if a.monitor != nil && a.monitorOwned {
		a.monitor.Close()
	}

	a.monitor, a.monitorOwned = nil, false
}

// forgetInputMonitor is stopInputMonitor plus the bar itself, for the page
// closing rather than the meter moving. restartInputMonitor is what keeps the
// report between the two, so the page's own exits have to drop it.
func (a *App) forgetInputMonitor() {
	a.stopInputMonitor()
	a.monitorReport = nil
}

/* Push-to-talk */

// pushPollInterval is how often the key is asked about. 16 ms is a frame, which
// is well inside the 20 ms audio frame it gates — the ear notices a syllable
// clipped off the front of a sentence, and nothing cheaper than this avoids it.
//
// The poll runs only while a call is up in push mode, so it costs nothing the
// rest of the time.
const pushPollInterval = 16 * time.Millisecond

// armPushToTalk starts watching the key, if that is the mode and the platform
// can answer. Voice activity needs nothing — the gate decides in the capture
// chain, off the audio thread entirely.
func (a *App) armPushToTalk() {
	a.disarmPushToTalk()

	settings := config.Current().Voice
	if !ui.PushToTalkSupported || settings.Mode != config.VoiceModePush {
		return
	}
	if a.capture == nil {
		return
	}

	capture, key := a.capture, settings.PushToTalkKey
	done := make(chan struct{})
	a.pushDone = done

	// The capture is told which way it is being driven whatever happens next, so a
	// mode changed mid-call takes effect on the next frame.
	capture.SetPushToTalk(true)
	capture.SetTransmitting(false)

	go func() {
		ticker := time.NewTicker(pushPollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
			}

			// No UI hop: the capture reads this atomically from its own Read, and
			// bouncing 60 samples a second off the UI thread to set a bool would be a
			// window repaint's worth of scheduling for nothing.
			capture.SetTransmitting(ui.KeyHeld(key))
		}
	}()
}

// disarmPushToTalk stops the poll and hands the microphone back to the gate. Safe
// with nothing running, which is what lets hangUp call it unconditionally.
func (a *App) disarmPushToTalk() {
	if a.pushDone != nil {
		close(a.pushDone)
		a.pushDone = nil
	}

	if a.capture != nil {
		a.capture.SetPushToTalk(false)
	}
}

/* Settings, applied to a call already running */

// applyVoiceSettings pushes what changed onto whatever is open. Without it every
// one of these reads once — at join, or at startup — and a slider dragged during
// a call does nothing until the next one, which reads as a broken setting rather
// than as a deferred one.
//
// The input device included: Capture opens the new one on its own goroutine and
// keeps feeding the same ring, so the publisher inside a blocking Read sees a
// period of quiet rather than a stream closing under it.
func (a *App) applyVoiceSettings() {
	settings := config.Current().Voice

	// The speakers are the engine's, call or no call, so this applies even when
	// nothing is joined — it is also what makes the output picker do anything at
	// all for the notification sounds.
	a.sounds.UseOutput(settings.OutputDevice)
	a.sounds.SetCallVolume(float64(settings.OutputVolume) / 100)

	if a.capture != nil {
		a.capture.SetSensitivity(settings.Sensitivity)
		a.capture.SetGain(float32(settings.InputVolume) / 100)
		a.capture.SetHighPass(settings.HighPass)
		a.capture.SetNoiseSuppression(settings.NoiseSuppression)
		a.capture.SetDevice(settings.InputDevice)
	}

	// A meter on a stream of its own follows the picker too, so the bar shows the
	// device that was just chosen rather than the one before it.
	if a.monitor != nil && a.monitorOwned {
		a.monitor.SetSensitivity(settings.Sensitivity)
		a.monitor.SetGain(float32(settings.InputVolume) / 100)
		a.monitor.SetHighPass(settings.HighPass)
		a.monitor.SetNoiseSuppression(settings.NoiseSuppression)
		a.monitor.SetDevice(settings.InputDevice)
	}

	// The mode and the key are the poll's, and re-arming is how both are picked up:
	// it is idempotent and stops whatever was running first.
	if a.call != nil {
		a.call.SetDeepPLC(settings.DeepPLC)
		a.armPushToTalk()
	}
}

/* Following a move */

// followVoiceMove answers a moderator dragging this account into another voice
// channel, or out of one. Revolt announces it as an ordinary voice state change
// and the media session knows nothing about it, so without this the client goes
// on talking into the room it was moved out of.
//
// Called from onVoiceChanged, which already fires for exactly these events.
// subject is who the event was about, "" meaning this account (the one voice
// event with no user names a move only its subject is sent).
func (a *App) followVoiceMove(subject string) {
	if a.call == nil || a.callJoining {
		return // nothing to follow, or a join is already deciding where we are
	}

	selfID := a.store.SelfID()
	if selfID == "" {
		return
	}

	// Only an event about this account can have moved where it stands. The
	// filter is also what closes a race: right after a join the gateway may not
	// have filed our own voice state yet, and anybody else's event landing in
	// that window would read the empty answer below as a disconnect and tear
	// down a call that just connected.
	if subject != "" && subject != selfID {
		return
	}

	where := a.voiceChannelOfSelf(selfID)

	switch {
	case where == a.callChannelID:
		// Where we already are, which is the ordinary case for every event about
		// somebody else in the same call.

	case where == "":
		// Dropped out of voice entirely: disconnected by a moderator, or from
		// another device.
		a.leaveCall()
		a.notify(ui.ToneWarning, "You were disconnected from the call.")

	default:
		// Moved. Rejoining is the whole of following: the token is per channel.
		a.joinCall(where)
	}
}

// voiceChannelOfSelf is where this account currently appears in voice, across
// every server it is in — a move can cross servers, so this cannot be limited to
// the open one.
func (a *App) voiceChannelOfSelf(selfID string) string {
	for _, serverID := range a.serverIDs {
		server, found := a.store.Server(serverID)
		if !found {
			continue
		}

		for _, id := range server.Channels {
			channel, ok := a.store.Channel(id)
			if !ok || channel.Kind != domain.ChannelVoice {
				continue
			}

			for _, participant := range a.store.VoiceParticipants(id) {
				if participant.UserID == selfID {
					return id
				}
			}
		}
	}

	return ""
}

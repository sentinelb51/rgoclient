package app

import (
	"fmt"
	"iter"
	"log"
	"math"
	"time"

	"fyne.io/fyne/v2"
	fynetheme "fyne.io/fyne/v2/theme"

	"RGOClient/assets"
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
// the island's join half and from the channel's own menu.
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

	// An owned meter holds the input device the worker is about to open, and a
	// second open is a refusal on a backend in exclusive mode — which would fail
	// every attempt of a rejoin made while settings is up. The report is kept,
	// so installCall or failedJoin puts the bar back on whichever capture wins.
	if a.monitorOwned {
		a.stopInputMonitor()
	}

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
		// The devices and the credentials have nothing to say to each other, so both
		// are started at once: run in order, a join costs a REST round trip *plus*
		// two device opens where it need only cost the slower of the two.
		//
		// The speakers are opened before the dial. The engine otherwise opens them
		// on the first notification sound, and the device callback is both what mixes
		// the call's lanes and what asks for the next frame — so a call joined before
		// anything had rung would decode nothing and be silent. Both are safe from a
		// worker: one is a send to the engine goroutine, the other an atomic.
		devices := make(chan openedInput, 1)

		go func() {
			a.sounds.StartOutput()
			a.sounds.SetCallVolume(float64(audio.GainFromDB(settings.OutputGainDB, config.VoiceGainOffDB)))
			a.sounds.SetSoftClip(settings.SoftClip)

			// The per-person volumes are the sink's own and outlive a call, but on the
			// first join after a restart it has never heard of anybody: they are seeded
			// before a lane can open, since a lane takes its gain when it is opened.
			for id, db := range settings.UserGainsDB {
				a.sounds.Sink().SetGain(id, float64(audio.GainFromDB(db, config.VoiceGainOffDB)))
			}

			capture, err := audio.OpenInput(settings.InputDevice, audio.InputConfig{
				Sensitivity:      settings.Sensitivity,
				Gain:             audio.GainFromDB(settings.InputGainDB, config.VoiceGainOffDB),
				SoftClip:         settings.SoftClip,
				HighPass:         settings.HighPass,
				NoiseSuppression: settings.NoiseSuppression,
			})

			devices <- openedInput{capture: capture, err: err}
		}()

		// force_disconnect: Stoat refuses a second connection for one account, so a
		// client that crashed mid-call cannot rejoin without it.
		creds, err := a.client.JoinCall(channelID, true)

		// Waited for whatever the route answered: the microphone is being opened
		// either way, and one left to arrive after this returns is a device nothing
		// is holding and nothing will ever close.
		opened := <-devices

		switch {
		case err != nil:
			opened.close()

			return err

		case opened.err != nil:
			return opened.err
		}

		capture := opened.capture

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

// openedInput carries the microphone back from the goroutine that opened it
// beside the credentials request. Both halves have to be waited for, so a
// failure travels with the device rather than being reported from in there.
type openedInput struct {
	capture *audio.Capture
	err     error
}

// close releases a microphone that opened into a join which has since failed for
// some other reason. Nothing else holds it, so this is its only way out.
func (o openedInput) close() {
	if o.capture != nil {
		o.capture.Close()
	}
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

	// A meter that wants to exist is moved onto this capture: an owned one would
	// be a second open the backend may refuse, and one joinCall released for the
	// dial has no stream at all until it is restarted here.
	a.restartInputMonitor()

	go a.pumpCall(call)
}

// dropCall releases the media without giving up the channel: the call, the
// microphone and the key poll go, and callChannelID stays. That is what a call
// being reconnected looks like — the call has not been left, so the island stays
// on screen saying so. Call on the UI thread.
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

// leaveCall is hanging up on purpose: the island's own button, the channel menu,
// a moderator disconnecting this account, and logging out. It gives up on getting
// back in, which is the whole difference from hangUp — a call somebody left must
// not reconnect itself.
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

// onCallConnection moves the island's dot, and the word beside it where the
// colour alone would not say enough.
func (a *App) onCallConnection(e voice.ConnectionChanged) {
	// Back in the room, so whatever the reconnect was counting is spent.
	if e.State == voice.Connected {
		a.cancelRejoin()
	}

	a.setCallState(e.State.String(), e.State == voice.Connected)
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

	// joinCall released an owned meter so the dial could open the device. The
	// attempt is over either way, so the bar comes back until the next one.
	if !a.stale(epoch) && a.monitorReport != nil && a.monitor == nil {
		a.restartInputMonitor()
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
	a.syncCallIsland()
	if a.callRetryAfterDrop {
		a.setCallState("Reconnecting", false)
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

/* The island */

// syncCallIsland matches the card at the top of the window to both things it can
// report: the call this account is in, and the voice channel it is looking at.
// They are one widget because they are one card on screen, and because a call and
// the channel being read move independently — a reader in one call browsing
// another voice channel sees both halves at once.
//
// Joining is never a side effect of selecting, so the join half's button is what
// does it. Call on the UI thread.
func (a *App) syncCallIsland() {
	if a.callIsland == nil {
		return
	}

	if a.callChannelID == "" {
		a.callIsland.ClearCall()
	} else {
		a.callIsland.SetCall(a.voiceWhere(a.callChannelID))
		a.callIsland.SetMuted(a.muted)
		a.callIsland.SetDeafened(a.deafened)

		// The bar is otherwise whatever the last ConnectionChanged painted it, which
		// for a call that has not landed yet is nothing at all.
		if a.call == nil {
			a.callIsland.SetState("Connecting", false)
		}
	}

	channelID := a.currentChannelID

	switch {
	case a.channelKind() != domain.ChannelVoice, channelID == a.callChannelID:
		// Nothing to offer: not a voice channel, or the one the live half already names.
		a.callIsland.ClearJoin()

	default:
		a.callIsland.SetJoin(a.voiceWhere(channelID), a.canJoinCall(channelID))
	}

	a.settleCallIsland()
}

// settleCallIsland shows or hides the card and re-measures the layer under it.
// The card is as wide as what is in it and a widget does not re-measure the
// layer it floats on; Refresh rather than Relayout because the room is retaken
// by walking descendants, which here is one card. Call on the UI thread.
func (a *App) settleCallIsland() {
	a.callIsland.Sync()
	a.callIslandLayer.Refresh()
}

// setCallState paints the island's state bar. It settles after for the case the
// bar was not drawn at all a moment ago — a call that has only just been asked
// for is a card without one.
func (a *App) setCallState(text string, good bool) {
	if a.callIsland == nil {
		return
	}

	a.callIsland.SetState(text, good)
	a.settleCallIsland()
}

// showCallState is the island's one hover: the state bar carries the connection
// as a colour, and the word it stands for is read by pointing at it.
func (a *App) showCallState(text string, over fyne.CanvasObject, hovering bool) {
	if !hovering {
		a.tooltip.Hide()
		return
	}

	// Below rather than beside: the bar runs the card's whole width, so a label off
	// its right edge would be nowhere near the pointer — and the bar is the card's
	// bottom edge, so under it is the one side with nothing of the card on it.
	a.tooltip.ShowBelow(text, over)
}

// syncCall is the whole of what a call starting or ending changes on screen.
func (a *App) syncCall() {
	a.syncCallIsland()
}

// voiceWhere fills the island's two lines and its picture for a channel a call is
// in or on offer. It falls back rather than failing: the call outlives leaving the
// server it is in, so the store may hold neither, and a channel it cannot place
// gets a name alone — an ID with no name being worse than nothing.
func (a *App) voiceWhere(channelID string) ui.CallIslandWhere {
	where := ui.CallIslandWhere{Channel: "Voice"}

	channel, ok := a.store.Channel(channelID)
	if !ok {
		return where
	}
	if channel.Name != "" {
		where.Channel = channel.Name
	}

	// A conversation is in no server: its picture is its own — a group's icon, or
	// the other account's avatar for a direct message — and only a group has
	// anything to put on the second line.
	if channel.ServerID == "" {
		where.Initial = where.Channel
		where.IconURL = channel.AvatarURL

		if channel.Kind == domain.ChannelGroup {
			where.Detail = voiceGroupDetail(len(channel.Recipients))
			where.Faces = a.voiceGroupFaces(channel.Recipients)
		}

		return where
	}

	server, ok := a.store.Server(channel.ServerID)
	if !ok {
		return where
	}

	where.Detail = server.Name
	where.Initial = server.Name
	where.IconURL = server.IconURL

	return where
}

// callIslandFaces is how many of a group's members the island draws where the
// group has none of its own. Three: past that a cluster is a smudge, and the
// people after the third say nothing the member count has not.
const callIslandFaces = 3

// voiceGroupFaces is who a group with no picture of its own is drawn as. This
// account is skipped — the reader knows they are in it, and a face they see in
// every mirror names nothing — and anybody the store cannot resolve with them: a
// blank circle is not a person.
func (a *App) voiceGroupFaces(recipients []string) []ui.CallIslandFace {
	selfID := a.store.SelfID()

	faces := make([]ui.CallIslandFace, 0, callIslandFaces)
	for _, userID := range recipients {
		if userID == selfID {
			continue
		}

		user, ok := a.store.User(userID)
		if !ok {
			continue
		}

		faces = append(faces, ui.CallIslandFace{Name: user.Name, AvatarURL: user.AvatarURL})
		if len(faces) == callIslandFaces {
			break
		}
	}

	return faces
}

// voiceGroupDetail is what stands under a group's name: a group is in no server,
// and how many are in it is the one thing about it worth a line. The count is
// every recipient, this account included — it is the size of the group, not of
// everybody else.
func voiceGroupDetail(members int) string {
	if members == 1 {
		return "1 member"
	}

	return fmt.Sprintf("%d members", members)
}

// joinCallHere joins the call in the channel on screen, which is what the
// island's join half offers. It reads the open channel at the tap rather than
// closing over one: the pill outlives every selection.
func (a *App) joinCallHere() {
	if a.currentChannelID == "" {
		return
	}

	a.joinCall(a.currentChannelID)
}

// toggleMute and toggleDeafen are the island's two switches. Deafening implies
// muting, so both redraw the pair.
func (a *App) toggleMute() {
	if a.call == nil {
		return
	}

	a.call.SetMuted(!a.call.Muted())
	a.muted = a.call.Muted()
	a.syncCallIsland()
}

func (a *App) toggleDeafen() {
	if a.call == nil {
		return
	}

	a.call.SetDeafened(!a.call.Deafened())
	a.muted, a.deafened = a.call.Muted(), a.call.Deafened()
	a.syncCallIsland()
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
// volume, and the three moderation ones a permission may allow. anchor is the row
// it was opened from, which the volume card hangs beside.
//
// Not on the profile card — every action there closes the card first, so a slider
// would be dragged out from under the pointer — and not on the member sidebar's
// own menu, which is the whole membership, most of whom are not in the call.
func (a *App) voiceParticipantMenu(anchor fyne.CanvasObject, channelID string,
	participant domain.VoiceParticipant) []*fyne.MenuItem {

	userID := participant.UserID
	items := []*fyne.MenuItem{a.userVolumeItem(anchor, participant)}

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

// userVolumeItem is how loud one participant is heard, on this machine and
// nobody else's.
//
// The item opens a card rather than a submenu of steps: a slider is not a
// fyne.MenuItem, so a menu on its own can offer only the levels somebody thought
// of. Fyne dismisses the menu before running the action, so nothing is left over
// the card.
func (a *App) userVolumeItem(anchor fyne.CanvasObject,
	participant domain.VoiceParticipant) *fyne.MenuItem {

	return fyne.NewMenuItemWithIcon("Volume", fynetheme.VolumeUpIcon(),
		func() { a.showUserVolume(anchor, participant) })
}

// showUserVolume hangs the volume card beside the row it was opened from. Whole
// decibels, the unit the settings are in, so the two cannot be read against each
// other wrongly — and the card is titled with the person and marked with the
// headphones, there being three other volumes in the client and nothing else on
// a card this size to say which one this is.
//
// The level is written back as it moves: the sink holds it for as long as the
// client runs, and config for longer. A voice too quiet is usually the room and
// not the person, but the room is the same one tomorrow.
func (a *App) showUserVolume(anchor fyne.CanvasObject, participant domain.VoiceParticipant) {
	userID := participant.UserID
	current := audio.DecibelsFromGain(a.sounds.Sink().Gain(userID), config.VoiceGainOffDB)

	// Unity in the middle: the range is -40 to +20, so the level everybody else is
	// at would otherwise sit two thirds along.
	unity := 0.0

	card := ui.NewSliderCard(ui.SliderCard{
		Title:   participant.Name,
		Icon:    assets.HeadphonesIcon,
		Low:     config.VoiceGainOffDB,
		High:    config.VoiceGainMaxDB,
		Step:    1,
		Value:   float64(current),
		Pivot:   &unity,
		Reading: func(db float64) string { return callVolumeLabel(int(math.Round(db))) },
		OnChanged: func(db float64) {
			level := int(math.Round(db))

			a.sounds.Sink().SetGain(userID, float64(audio.GainFromDB(level, config.VoiceGainOffDB)))
			config.SetUserGain(userID, level)
		},
	})

	a.showPopover(card, anchor)
}

// callVolumeLabel names one level. The bottom of the range is silence rather than
// a quantity, and unity is what everybody is at until somebody moves them, so
// neither reads as a decibel figure.
func callVolumeLabel(db int) string {
	switch db {
	case config.VoiceGainOffDB:
		return "Off"
	case 0:
		return "Normal"
	}

	return fmt.Sprintf("%+d dB", db)
}

/* Devices, for the settings page */

// loadVoiceNodes records the media servers this instance offers, so the settings
// page can put the choice in front of the reader without a request of its own.
// Asked once per session off Ready, beside the node warm-up that shares its
// round trip — the list is instance configuration and cannot change under a
// running client. Call off the UI thread.
func (a *App) loadVoiceNodes() {
	epoch := a.epoch

	nodes, err := a.client.VoiceNodes()
	if err != nil {
		log.Printf("list voice servers: %v", err)
		return
	}

	a.doOnUI(func() {
		if a.stale(epoch) {
			return
		}
		a.voiceNodes = toVoiceNodes(nodes)
	}, false)
}

// voiceNodeList is what the settings page reads. A plain read of what landed
// above, never a request, so the index pass may call it like any other hook.
// Call on the UI thread.
func (a *App) voiceNodeList() []ui.VoiceNode { return a.voiceNodes }

// toVoiceNodes converts what the client knows into what a widget may see, the
// way toAudioDevices does.
func toVoiceNodes(nodes []domain.VoiceNode) []ui.VoiceNode {
	out := make([]ui.VoiceNode, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, ui.VoiceNode{Name: node.Name, URL: node.URL})
	}

	return out
}

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

	if capture == nil && a.callJoining {
		// The join's worker is opening this device right now; a second open is a
		// refusal on an exclusive-mode backend. The report is kept, and
		// installCall or failedJoin restarts the meter onto whichever capture
		// wins the race.
		a.monitorReport = report
		return
	}

	if capture == nil {
		settings := config.Current().Voice

		opened, err := audio.OpenInput(settings.InputDevice, audio.InputConfig{
			Sensitivity:      settings.Sensitivity,
			Gain:             audio.GainFromDB(settings.InputGainDB, config.VoiceGainOffDB),
			SoftClip:         settings.SoftClip,
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
	a.sounds.SetCallVolume(float64(audio.GainFromDB(settings.OutputGainDB, config.VoiceGainOffDB)))
	a.sounds.SetSoftClip(settings.SoftClip)

	if a.capture != nil {
		a.capture.SetSensitivity(settings.Sensitivity)
		a.capture.SetGain(audio.GainFromDB(settings.InputGainDB, config.VoiceGainOffDB))
		a.capture.SetSoftClip(settings.SoftClip)
		a.capture.SetHighPass(settings.HighPass)
		a.capture.SetNoiseSuppression(settings.NoiseSuppression)
		a.capture.SetDevice(settings.InputDevice)
	}

	// A meter on a stream of its own follows the picker too, so the bar shows the
	// device that was just chosen rather than the one before it.
	if a.monitor != nil && a.monitorOwned {
		a.monitor.SetSensitivity(settings.Sensitivity)
		a.monitor.SetGain(audio.GainFromDB(settings.InputGainDB, config.VoiceGainOffDB))
		a.monitor.SetSoftClip(settings.SoftClip)
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
		a.notifyTitled(ui.ToneWarning, "Call ended", "You were disconnected from the call.")

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

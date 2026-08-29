package app

import (
	"fmt"
	"iter"
	"log"
	"math"
	"slices"
	"strings"
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

			capture, err := audio.OpenInput(settings.InputDevice, inputConfig(settings))

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

// inputConfig is the capture chain as the settings describe it, converted at
// the seam: config speaks decibels and percentages, audio linear gains. One
// builder because two sites open a microphone — the join and the settings
// meter — and a dial only one of them carried would behave differently in a
// call than under the bar tuning it.
func inputConfig(settings config.Voice) audio.InputConfig {
	return audio.InputConfig{
		GateThresholdDB:  settings.SensitivityDB,
		Gain:             audio.GainFromDB(settings.InputGainDB, config.VoiceGainOffDB),
		SoftClip:         settings.SoftClip,
		HighPass:         settings.HighPass,
		NoiseSuppression: settings.NoiseSuppression,
		SuppressionFloor: audio.SuppressionFloor(settings.NoiseSuppressionDB, config.VoiceSuppressionMaxDB),
		VADThreshold:     settings.VADThreshold,
	}
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
	a.refreshSelfVoiceMarks()
	a.armPushToTalk()

	// A meter that wants to exist is moved onto this capture: an owned one would
	// be a second open the backend may refuse, and one joinCall released for the
	// dial has no stream at all until it is restarted here.
	a.restartInputMonitor()

	// Asked once the call is up rather than on the way into it: the answer is the
	// same either way, and the enumeration it can reach has no business on the
	// path to being heard.
	a.checkInputEffects(config.Current().Voice.InputDevice)

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

	// Both halves of a screenshare were the call's: the window must not
	// outlive the media session feeding it, and the capture child must not
	// outlive the track it was feeding.
	a.closeShare()
	a.stopSharing()

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
	a.callMuted = nil
	a.muted, a.deafened = false, false

	// Every mark the media session was the source of goes with it: what is left is
	// the moderation, which the store still answers for.
	a.refreshSpeakingAll()
	a.refreshVoiceMarksAll()
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
//
// Batched onto one hop per burst, as pumpEvents is and for the same reason —
// every hop wakes the driver's loop — which matters most here: an active speaker
// diff arrives several times a second and a room of eight talking over each other
// is the highest event rate the client sees.
//
// Unlike pumpEvents this does **not** wait. Call.emit drops rather than blocks,
// so a pump held against the UI thread would turn a busy frame into lost
// speaking transitions; and without the wait the batch cannot be reused, hence a
// slice per burst — still one allocation where there was a closure per event.
func (a *App) pumpCall(call *voice.Call) {
	events := call.Events()

	for first := range events {
		batch := gather(make([]voice.Event, 0, maxEventBatch), first, events, maxEventBatch)

		a.doOnUI(func() {
			for _, event := range batch {
				a.onCallEvent(call, event)
			}
		}, false)
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
	case voice.MuteChanged:
		a.onMuteChanged(e)
	case voice.ParticipantChanged:
		// The gateway announces the voice state and the sidebar is rebuilt from
		// that; this half only stops a departed participant being left ringing or
		// wearing a mark from the last time they were here.
		if !e.Joined {
			a.onSpeakingChanged(voice.SpeakingChanged{UserID: e.UserID})
			a.onMuteChanged(voice.MuteChanged{UserID: e.UserID})
		}
	case voice.ShareEnded:
		a.onShareEnded(e)
	case voice.ShareStopped:
		a.onShareStopped()
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

	// Recorded either way, so turning the ring back on draws the truth rather
	// than whoever happens to start talking next. Only the drawing is the
	// setting's, and it is read here rather than at the row: a repaint that does
	// not happen is the whole of what the setting buys.
	if !config.Current().Voice.SpeakingRing {
		return
	}

	for row := range a.voiceRows() {
		if row.UserID() == e.UserID {
			row.SetSpeaking(e.Speaking)
		}
	}
}

// onMuteChanged marks one participant as holding their own microphone, or lets
// go. Touches that user's rows only, for the reason onSpeakingChanged does —
// though this one moves far less often, a mute being a decision rather than a
// syllable.
func (a *App) onMuteChanged(e voice.MuteChanged) {
	if a.callMuted == nil {
		a.callMuted = make(map[string]bool)
	}

	if a.callMuted[e.UserID] == e.Muted {
		return
	}

	if e.Muted {
		a.callMuted[e.UserID] = true
	} else {
		delete(a.callMuted, e.UserID)
	}

	a.refreshVoiceMarks(e.UserID)
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

		// Sharing is offered only where the call's own token grants it, which
		// is Revolt's Video permission read back off the JWT rather than
		// guessed at from the channel.
		a.callIsland.SetSharing(a.sending != nil, a.call != nil && a.call.CanShare())

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
	a.refreshSelfVoiceMarks()
}

func (a *App) toggleDeafen() {
	if a.call == nil {
		return
	}

	a.call.SetDeafened(!a.call.Deafened())
	a.muted, a.deafened = a.call.Muted(), a.call.Deafened()
	a.syncCallIsland()
	a.refreshSelfVoiceMarks()
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
		row.SetSpeaking(a.showSpeaking(row.UserID()))
	}
}

// showSpeaking is whether a row may wear the ring. The record is kept whatever
// the setting says, so this is only ever about the painting — which is why
// onSpeakingChanged asks it by not walking the rows at all.
func (a *App) showSpeaking(userID string) bool {
	return config.Current().Voice.SpeakingRing && a.speaking[userID]
}

// voiceMarks is everything a participant's row says about their voice, gathered
// from the three places that know: the membership carries a moderator's hold,
// the media session carries their own microphone, and config carries whether
// this machine has turned them down to nothing.
//
// participant is what the sidebar was built from, and is where the server-wide
// hold comes from — the store resolves it with the rest of the membership.
func (a *App) voiceMarks(participant domain.VoiceParticipant) ui.VoiceMarks {
	marks := ui.VoiceMarks{
		ServerMuted:    participant.ServerMuted,
		ServerDeafened: participant.ServerDeafened,
		SelfMuted:      a.callMuted[participant.UserID],
		Silenced:       config.Current().Voice.UserGainsDB[participant.UserID] == config.VoiceGainOffDB,
	}

	// This account's own two are the call's, not the room's: SetMuted is what was
	// just decided here, and a deafen is not reported to anybody at all — see
	// docs/known-gaps.md. Silenced is never drawn on our own row, a volume for
	// somebody nobody hears meaning nothing.
	if participant.UserID == a.store.SelfID() {
		marks.SelfMuted = a.muted
		marks.SelfDeafened = a.deafened
		marks.Silenced = false
	}

	return marks
}

// refreshVoiceMarks re-marks the rows drawing one user, for a change that does
// not rebuild the sidebar: their own mute inside the call, this account's
// switches, or their volume being moved here. The participant is re-read from
// the store, a hold being the membership's rather than the row's.
func (a *App) refreshVoiceMarks(userID string) {
	if userID == "" {
		return
	}

	for row := range a.voiceRows() {
		if row.UserID() != userID {
			continue
		}

		row.SetMarks(a.voiceMarks(a.voiceParticipantOf(row.ChannelID(), userID)))
	}
}

// voiceParticipantOf finds one participant in a channel's call. A miss answers
// with the ID alone, which draws no hold: somebody who has just left is somebody
// with nothing held against them.
func (a *App) voiceParticipantOf(channelID, userID string) domain.VoiceParticipant {
	for _, participant := range a.store.VoiceParticipants(channelID) {
		if participant.UserID == userID {
			return participant
		}
	}

	return domain.VoiceParticipant{UserID: userID}
}

// refreshVoiceMarksAll re-marks every mounted row, for a change that moved all
// of them at once: a call ending, which retires everything the media session was
// the only source of.
//
// One snapshot per channel rather than one per row: voiceParticipantOf resolves
// and sorts a whole call to answer about one person, and the rows here are
// exactly the people in it — so asking per row is that walk once for every
// participant. A channel absent from the snapshot answers as voiceParticipantOf
// does, with the ID alone.
func (a *App) refreshVoiceMarksAll() {
	calls := make(map[string]map[string]domain.VoiceParticipant)

	for row := range a.voiceRows() {
		channelID, userID := row.ChannelID(), row.UserID()

		people, ok := calls[channelID]
		if !ok {
			participants := a.store.VoiceParticipants(channelID)

			people = make(map[string]domain.VoiceParticipant, len(participants))
			for _, participant := range participants {
				people[participant.UserID] = participant
			}
			calls[channelID] = people
		}

		participant, ok := people[userID]
		if !ok {
			participant = domain.VoiceParticipant{UserID: userID}
		}

		row.SetMarks(a.voiceMarks(participant))
	}
}

// refreshSelfVoiceMarks re-marks this account's own row wherever it is drawn,
// which is what a mute or a deafen changes besides the island. A no-op with
// nothing on screen, which is the usual case: the row exists only while the
// server holding that call is the open one.
func (a *App) refreshSelfVoiceMarks() {
	a.refreshVoiceMarks(a.store.SelfID())
}

/* The participant menu */

// voiceParticipantMenu is where all four per-person voice actions meet: the local
// volume, and the three moderation ones a permission may allow. anchor is the row
// it was opened from, which the volume card hangs beside.
//
// Not on the profile card — every action there closes the card first, so a slider
// would be dragged out from under the pointer — and not on the member sidebar's
// own menu, which is the whole membership, most of whom are not in the call.
func (a *App) voiceParticipantMenu(anchor fyne.CanvasObject, channelID,
	userID string) []*fyne.MenuItem {

	var items []*fyne.MenuItem

	// Not on our own row: this account is not one of the voices this client
	// mixes, so a volume for it would be a slider that moves nothing. Every other
	// item here is already refused for the same person by memberVoiceItems.
	if userID != a.store.SelfID() {
		items = append(items, a.userVolumeItem(anchor, channelID, userID))

		// The stream's sound is its own lane and its own dial, offered only
		// while there is a stream to be loud.
		if a.voiceParticipantOf(channelID, userID).Screensharing {
			items = append(items, a.shareVolumeItem(anchor, channelID, userID))
		}
	}

	channel, ok := a.store.Channel(channelID)
	if !ok || channel.ServerID == "" {
		return items
	}

	if moderation := a.memberVoiceItems(channel.ServerID, userID); len(moderation) > 0 {
		if len(items) > 0 {
			items = append(items, fyne.NewMenuItemSeparator())
		}
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
func (a *App) userVolumeItem(anchor fyne.CanvasObject, channelID, userID string) *fyne.MenuItem {
	return fyne.NewMenuItemWithIcon("Volume", fynetheme.VolumeUpIcon(),
		func() { a.showUserVolume(anchor, channelID, userID) })
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
func (a *App) showUserVolume(anchor fyne.CanvasObject, channelID, userID string) {
	current := audio.DecibelsFromGain(a.sounds.Sink().Gain(userID), config.VoiceGainOffDB)

	// Unity in the middle: the range is -40 to +20, so the level everybody else is
	// at would otherwise sit two thirds along.
	unity := 0.0

	card := ui.NewSliderCard(ui.SliderCard{
		Title:   a.voiceParticipantOf(channelID, userID).Name,
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

			// The bottom of the range is silence, which the row says so with a mark:
			// a person nobody can hear and nothing saying why is a bug report.
			a.refreshVoiceMarks(userID)
		},
	})

	a.showPopover(card, anchor)
}

// shareVolumeItem is how loud somebody's stream is heard, beside their voice's
// own dial: the two are separate lanes on purpose, a friend worth hearing
// being able to share a game worth turning down.
func (a *App) shareVolumeItem(anchor fyne.CanvasObject, channelID, userID string) *fyne.MenuItem {
	return fyne.NewMenuItemWithIcon("Screenshare volume", fynetheme.VolumeUpIcon(),
		func() { a.showShareVolume(anchor, channelID, userID) })
}

// showShareVolume is showUserVolume pointed at the share's lane: same range,
// same card — marked with the screenshare glyph where the voice's wears the
// headphones — written down the same twice, the sink for this run and config
// for the next. The lane's key is the voice package's to mint, being the key
// its decode goroutine writes under.
func (a *App) showShareVolume(anchor fyne.CanvasObject, channelID, userID string) {
	lane := voice.ShareLane(userID)
	current := audio.DecibelsFromGain(a.sounds.Sink().Gain(lane), config.VoiceGainOffDB)

	unity := 0.0

	card := ui.NewSliderCard(ui.SliderCard{
		Title:   a.voiceParticipantOf(channelID, userID).Name,
		Icon:    assets.ScreenshareIcon,
		Low:     config.VoiceGainOffDB,
		High:    config.VoiceGainMaxDB,
		Step:    1,
		Value:   float64(current),
		Pivot:   &unity,
		Reading: func(db float64) string { return callVolumeLabel(int(math.Round(db))) },
		OnChanged: func(db float64) {
			level := int(math.Round(db))

			a.sounds.Sink().SetGain(lane, float64(audio.GainFromDB(level, config.VoiceGainOffDB)))
			config.SetUserGain(lane, level)
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

// loadVideoLimits records what the instance enforces about a published video
// track, so a share is fitted under it before publishing rather than being
// disconnected for declaring a size the ingress refuses. Asked once per
// session beside the node warm-up — it is instance configuration and cannot
// change under a running client. Call off the UI thread.
func (a *App) loadVideoLimits() {
	epoch := a.epoch

	limits, err := a.client.VideoLimits()
	if err != nil {
		// Not worth reporting: with no answer the share is fitted under
		// nothing here and the server has the last word either way.
		log.Printf("instance video limits: %v", err)
		return
	}

	a.doOnUI(func() {
		if a.stale(epoch) {
			return
		}
		a.videoLimits = limits
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
func (a *App) startInputMonitor(report func(m ui.InputMeter)) {
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

		opened, err := audio.OpenInput(settings.InputDevice, inputConfig(settings))
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

	// The echo rides whichever capture the bar is now reading, so a call starting
	// or ending carries the microphone test over with the meter.
	a.applyInputEcho()

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

			// Both bars come off the one sample, so the loudness and the model's
			// estimate a reader compares are about the same run of frames — and the
			// bar and its figure off the one level, for the same reason.
			level := capture.Level()

			meter := ui.InputMeter{
				Level:   audio.MeterRatio(level),
				LevelDB: int(math.Round(audio.LevelDecibels(level))),
				Speech:  capture.VAD(),
			}

			a.doOnUI(func() {
				// The page may have closed, or the session been replaced, between the
				// sample and the hop back.
				if a.monitorDone == done && !a.stale(epoch) {
					report(meter)
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

	// The echo comes off first, before the capture it is set on goes away — and
	// the lane with it, so a test carried across a call's start does not play out
	// the tail of the stream it was reading.
	if a.monitor != nil {
		a.monitor.SetEcho(nil)
	}
	a.sounds.Sink().StopEcho()

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
//
// The microphone test goes with it: it is a mode somebody turned on to listen to,
// not a setting, and one left running behind a closed page is this account
// talking to itself for the rest of the session.
func (a *App) forgetInputMonitor() {
	a.monitorEcho = false
	a.stopInputMonitor()
	a.monitorReport = nil
}

/* The microphone test */

// setInputEcho turns the microphone test on or off — this account's own voice
// played back through the speakers with the whole capture chain applied, which is
// the one thing the level bar cannot answer: a bar says a microphone is heard,
// not what it sounds like once the filters have had it. Call on the UI thread.
func (a *App) setInputEcho(on bool) {
	a.monitorEcho = on
	a.applyInputEcho()
}

// applyInputEcho points whichever microphone the meter holds at the speakers, or
// takes the echo off it. Every path that moves the meter between the call's
// capture and its own ends here, the echo being a property of the reader's
// intention rather than of the stream.
func (a *App) applyInputEcho() {
	sink := a.sounds.Sink()

	if !a.monitorEcho || a.monitor == nil {
		sink.StopEcho()

		if a.monitor != nil {
			a.monitor.SetEcho(nil)
		}

		return
	}

	// The lane exists before anything writes to it: a write for a user with no
	// lane is dropped, which is what stops a departed participant being
	// resurrected and would here simply be silence.
	sink.StartEcho()
	a.monitor.SetEcho(sink)

	// The speakers open on the first sound *played*, so a client that has rung
	// none holds no device and the lane would be filled and never rendered — the
	// same silence a call joined before anything rang used to be. It waits for the
	// engine goroutine, hence the worker.
	a.background(func() error {
		a.sounds.StartOutput()
		return nil
	}, nil)
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
		applyCaptureSettings(a.capture, settings)
	}

	// A meter on a stream of its own follows the picker too, so the bar shows the
	// device that was just chosen rather than the one before it.
	if a.monitor != nil && a.monitorOwned {
		applyCaptureSettings(a.monitor, settings)
	}

	// The ring is drawn by the sidebar rather than by the call, so nothing else
	// would notice the switch: turned off mid-call it would leave whoever was
	// talking at that moment ringing for as long as the column stood.
	a.refreshSpeakingAll()

	// The mode and the key are the poll's, and re-arming is how both are picked up:
	// it is idempotent and stops whatever was running first.
	if a.call != nil {
		a.call.SetDeepPLC(settings.DeepPLC)
		a.armPushToTalk()
	}
}

// applyCaptureSettings pushes every chain setting onto one open capture, the
// call's and the meter's alike — inputConfig's mid-call twin, and one function
// for the same reason: a dial applied to one stream and not the other would
// read differently in a call than under the bar tuning it.
func applyCaptureSettings(c *audio.Capture, settings config.Voice) {
	c.SetGateThreshold(settings.SensitivityDB)
	c.SetGain(audio.GainFromDB(settings.InputGainDB, config.VoiceGainOffDB))
	c.SetSoftClip(settings.SoftClip)
	c.SetHighPass(settings.HighPass)
	c.SetNoiseSuppression(settings.NoiseSuppression)
	c.SetSuppressionFloor(audio.SuppressionFloor(settings.NoiseSuppressionDB, config.VoiceSuppressionMaxDB))
	c.SetVADThreshold(settings.VADThreshold)
	c.SetDevice(settings.InputDevice)
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

/* What the OS is already doing to the microphone */

// stackedEffects are the effects an OS applies to a microphone that do the same
// work as this client's own capture chain, and the words a reader knows them by.
//
// The rest of what Windows reports is deliberately absent: beamforming and tone
// removal do not double up with anything here, and echo cancellation is work
// this client does not do at all. A warning naming those would be noise.
var stackedEffects = map[audio.EffectKind]string{
	audio.EffectNoiseSuppression:     "noise suppression",
	audio.EffectDeepNoiseSuppression: "Voice Focus",
	audio.EffectGainControl:          "automatic gain control",
}

// checkInputEffects says once that the OS is already cleaning the microphone
// this client is also cleaning, the two together being what hollows a voice out.
//
// It only ever reports: turning an effect off reaches outside this process for
// something the reader configured, and the notice leads to the Voice section
// instead. Call on the UI thread.
func (a *App) checkInputEffects(device string) {
	epoch := a.epoch

	a.background(func() error {
		effects, err := audio.InputEffects(device)
		if err != nil {
			return err
		}

		names := stackedNames(effects)
		if len(names) == 0 {
			return nil
		}

		// Reached only once there is something to warn about, an enumeration
		// being too much to spend on every join for a sentence nobody will read.
		name, named := a.inputName(device)

		key := name
		if key == "" {
			key = device
		}
		if config.InputEffectsWarned(key, strings.Join(names, ",")) {
			return nil
		}

		subject := "your input device"
		if named {
			subject = name
		}

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}

			config.RememberInputEffectsWarning(key, strings.Join(names, ","))

			a.notifyNotice(ui.Notice{
				Tone:  ui.ToneWarning,
				Title: "Windows is already filtering this microphone",
				Body: fmt.Sprintf(
					"Windows has %s on for %s. This client's noise suppression runs on top of it, "+
						"which can make your voice sound thin — open Voice settings to turn one of them off.",
					listPhrase(names), subject,
				),
				OnTap: func() { a.openSettingsAt(ui.SectionVoice) },
			})
		}, false)

		return nil
	}, func(err error) {
		// Not worth a notice: every platform but Windows 11 answers this way, and
		// there is nothing a reader could do about it if it were said.
		log.Printf("input effects: %v", err)
	})
}

// stackedNames is what to call the effects that collide with this client's own
// chain. Sorted and deduplicated because the joined string is what the warning
// is remembered by, and an order the OS chose would make the same situation look
// like a new one.
func stackedNames(effects []audio.Effect) []string {
	var names []string

	for _, effect := range effects {
		if name, ok := stackedEffects[effect.Kind]; ok {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	return slices.Compact(names)
}

// listPhrase writes a list the way a sentence does rather than the way a key
// does, the stored form being joined without spaces beside it.
func listPhrase(names []string) string {
	switch len(names) {
	case 0:
		return ""

	case 1:
		return names[0]
	}

	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// inputName is what to call a microphone in a sentence, and the name the warning
// about it is filed under. An unnamed device reports false: the identifier is
// still a usable key, but it is not something to put in front of a reader.
func (a *App) inputName(id string) (string, bool) {
	devices, err := audio.Inputs()
	if err != nil {
		log.Printf("name microphone: %v", err)

		return "", false
	}

	for _, device := range devices {
		if (id == "" && device.Default) || (id != "" && device.ID == id) {
			return device.Name, device.Name != ""
		}
	}

	return "", false
}

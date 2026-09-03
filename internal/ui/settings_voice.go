package ui

import (
	"slices"

	"fyne.io/fyne/v2"

	"RGOClient/internal/config"
)

/* Voice */

// voiceSection is the call: which devices it uses, what it does to the
// microphone on the way out, and what state it joins in.
func (p *SettingsPage) voiceSection() []settingsGroup {
	settings := config.Current().Voice

	return []settingsGroup{
		p.group("Devices", "",
			p.deviceRow("Microphone", settings.InputDevice, p.hooks.InputDevices,
				func(s *config.Settings, id string) { s.Voice.InputDevice = id }),
			// The note is the output's alone — nothing plays a notification through
			// a microphone — so it is on the row rather than over the pair, where it
			// read as true of both.
			p.deviceRowDetailed("Output", "Also plays notification sounds.",
				settings.OutputDevice, p.hooks.OutputDevices,
				func(s *config.Settings, id string) { s.Voice.OutputDevice = id }),
		),

		// Ahead of the two groups under it for a reason beyond reading order: the
		// level meter is what opens the one monitor stream, and the speech bar in
		// Voice activity detection rides it rather than opening a second.
		p.group("Microphone", "", p.microphoneRows(settings)...),

		p.group("Noise suppression",
			"Removes steady background noise — fans, hiss, hum — while you are "+
				"talking as well as in the gaps between words.",
			p.suppressionRows(settings)...),

		p.group("Voice activity detection",
			"Noise suppression's own model decides whether a sound is a voice, so "+
				"the microphone does not open for a keyboard or a door. Measured only "+
				"while noise suppression is on.",
			p.detectionRows(settings)...),

		p.group("Playback", "",
			p.numberRow("Call volume",
				"Everybody else in the call. 0 dB is unchanged, -40 dB is off.",
				settings.OutputGainDB, config.VoiceGainOffDB, config.VoiceGainMaxDB, "dB",
				func(s *config.Settings, v int) { s.Voice.OutputGainDB = v }),
			p.toggleRow("Even out voice levels",
				"Brings quiet people up and loud people down, so everybody arrives "+
					"at about the same volume. Per-person volumes still apply on top.",
				settings.Levelling,
				func(s *config.Settings, on bool) { s.Voice.Levelling = on }),
			p.toggleRow("Spread people out",
				"Places each person at a slightly different point between your ears, "+
					"which makes two people talking at once easier to follow. Needs "+
					"headphones or stereo speakers.",
				settings.Placement,
				func(s *config.Settings, on bool) { s.Voice.Placement = on }),
			p.optionRow("Buffering",
				"How long to wait for audio that arrives late. Less waiting is a "+
					"faster conversation and more breaking up; more waiting is the "+
					"other way round.",
				settings.Buffering, bufferingProfiles,
				func(s *config.Settings, v string) { s.Voice.Buffering = v }),
			p.toggleRow("Repair dropped audio",
				"Rebuilds lost packets as speech instead of fading them out, and "+
					"smooths the roughness a low bitrate leaves on a voice.",
				settings.DeepPLC,
				func(s *config.Settings, on bool) { s.Voice.DeepPLC = on }),
		),

		// Its own group because it is the one row on this page that is not about
		// one direction: `softClip` is the curve a peak meets the ceiling through
		// and both ends share the definition, so under Playback the row had to
		// apologise for its own group in its description.
		p.group("Loudness",
			"How a sound that overshoots meets the ceiling. The one setting here "+
				"that applies both ways: your microphone, everybody else's voice, and "+
				"this client's own sounds.",
			p.toggleRow("Smooth loud peaks",
				"Rounds off what overshoots instead of cutting it flat.",
				settings.SoftClip,
				func(s *config.Settings, on bool) { s.Voice.SoftClip = on }),
		),

		p.group("During a call", "",
			p.toggleRow("Ring who is speaking",
				"Draws a ring round anybody talking, on their row under the voice channel. "+
					"Costs a repaint of the window each time somebody starts or stops.",
				settings.SpeakingRing,
				func(s *config.Settings, on bool) { s.Voice.SpeakingRing = on }),
		),

		p.group("Joining a call", "", p.joiningRows(settings)...),
	}
}

// joiningRows is what a call is joined *as*, plus where it is joined through.
// The node row is only drawn where the instance offers more than one: a list of
// one is a control that cannot be answered differently, and most instances —
// stoat.chat included — publish exactly that.
func (p *SettingsPage) joiningRows(settings config.Voice) []fyne.CanvasObject {
	rows := []fyne.CanvasObject{
		p.toggleRow("Join muted", "", settings.JoinMuted,
			func(s *config.Settings, on bool) { s.Voice.JoinMuted = on }),
		p.toggleRow("Join deafened", "Deafened also mutes you.", settings.JoinDeafened,
			func(s *config.Settings, on bool) { s.Voice.JoinDeafened = on }),
	}

	var nodes []VoiceNode
	if p.hooks.VoiceNodes != nil {
		nodes = p.hooks.VoiceNodes()
	}
	if len(nodes) < 2 {
		return rows
	}

	options := []settingsOption{{Label: "Fastest to answer", Value: ""}}
	for _, node := range nodes {
		options = append(options, settingsOption{Label: node.Name, Value: node.Name})
	}

	// A node the instance has since dropped would otherwise leave the control
	// showing the first option and the setting saying something else — the same
	// hazard an unplugged device is, and the same answer.
	value := settings.Node
	if !hasOption(options, value) {
		value = ""
	}

	return append(rows, p.optionRow("Voice server",
		"Which of this instance's media servers calls go through. Measured by default.",
		value, options, func(s *config.Settings, name string) { s.Voice.Node = name }))
}

// microphoneRows is the capture group. Push-to-talk contributes two rows and
// only where the platform can answer whether a key is held — offering a mode that
// silently behaves as voice activity is worse than not offering it, which is the
// same rule the taskbar-flash group follows.
func (p *SettingsPage) microphoneRows(settings config.Voice) []fyne.CanvasObject {
	rows := []fyne.CanvasObject{
		p.numberRow("Sensitivity",
			"How loud a sound has to be to count as speech, in dBFS — the same scale "+
				"the meter under it is drawn on. Ordinary speech sits around -30.",
			settings.SensitivityDB, config.VoiceGateQuietestDB, config.VoiceGateLoudestDB, "dB",
			func(s *config.Settings, v int) {
				s.Voice.SensitivityDB = v
				p.moveGateMark(v)
			}),
	}
	if meter := p.levelMeterRow(settings.SensitivityDB); meter != nil {
		rows = append(rows, meter)
	}
	if echo := p.echoRow(); echo != nil {
		rows = append(rows, echo)
	}
	rows = append(rows,
		p.numberRow("Input volume",
			"Amplifies the microphone first, so it also raises what the meter and sensitivity measure.",
			settings.InputGainDB, config.VoiceGainOffDB, config.VoiceGainMaxDB, "dB",
			func(s *config.Settings, v int) { s.Voice.InputGainDB = v }),
		// Filed here rather than under Noise suppression: it is a plain filter on
		// the way in and runs whether or not the model does, where every row in
		// that group is the model's.
		p.toggleRow("Rumble filter",
			"Removes hum and rumble from below the voice range.",
			settings.HighPass,
			func(s *config.Settings, on bool) { s.Voice.HighPass = on }),
	)

	if !PushToTalkSupported {
		return rows
	}

	mode := p.optionRow("Mode", "Voice activity opens the microphone when you speak.",
		settings.Mode, voiceModes,
		func(s *config.Settings, v string) { s.Voice.Mode = v })

	// The key is a list rather than something captured. Reading an arbitrary key
	// needs canvas focus, which the composer holds for most of the client's life —
	// see the modifier-key footgun — so these are the keys people bind, offered
	// the way every other choice on this page is.
	key := p.optionRow("Push-to-talk key", "Held to speak.",
		defaultedKey(settings.PushToTalkKey), pushToTalkOptions(),
		func(s *config.Settings, v string) { s.Voice.PushToTalkKey = v })

	return append([]fyne.CanvasObject{mode, key}, rows...)
}

// suppressionRows is the model itself: whether it runs, and how deep it may cut.
// Both labels stand alone under the group's caption, which is what a reader
// searching for "noise suppression" matches — recordGroup files a caption as a
// hit of its own, so splitting a feature out of Microphone costs no search term.
func (p *SettingsPage) suppressionRows(settings config.Voice) []fyne.CanvasObject {
	return []fyne.CanvasObject{
		p.toggleRow("Remove background noise", "", settings.NoiseSuppression,
			func(s *config.Settings, on bool) { s.Voice.NoiseSuppression = on }),
		p.optionRow("Model",
			"RNNoise delays the microphone by 10 ms. GTCRN removes more and "+
				"delays it by about 30 ms.",
			settings.NoiseModel, noiseModels,
			func(s *config.Settings, v string) { s.Voice.NoiseModel = v }),
		p.numberRow("Strength",
			"The most it may take out. Full strength can hollow a voice out; lower keeps some of the room.",
			settings.NoiseSuppressionDB, 0, config.VoiceSuppressionMaxDB, "dB",
			func(s *config.Settings, v int) { s.Voice.NoiseSuppressionDB = v }),
	}
}

// detectionRows is the veto and the bar that makes it aimable — a group rather
// than two rows among the microphone's because they are one control read
// together, and because what they both depend on (the model running) is then
// said once, in the caption, instead of on each row.
//
// The bar is built only where the level meter above it was, that being what
// opens the stream both read.
func (p *SettingsPage) detectionRows(settings config.Voice) []fyne.CanvasObject {
	rows := []fyne.CanvasObject{
		p.numberRow("Threshold",
			"How sure the model has to be before loudness may open the microphone. "+
				"0% leaves it to sensitivity alone. GTCRN has no detector of its own, so "+
				"for it this is how much of the sound it kept.",
			settings.VADThreshold, 0, 100, "%",
			func(s *config.Settings, v int) {
				s.Voice.VADThreshold = v
				p.moveVADMark(v)
			}),
	}
	if speech := p.speechMeterRow(settings.VADThreshold); speech != nil {
		rows = append(rows, speech)
	}

	return rows
}

// bufferingProfiles is how long the client waits for late audio. Named for the
// conversation rather than for the buffer: what a reader is choosing between is
// a call that feels quick and a call that holds together.
var bufferingProfiles = []settingsOption{
	{Label: "Responsive", Value: config.BufferingResponsive},
	{Label: "Balanced", Value: config.BufferingBalanced},
	{Label: "Smooth", Value: config.BufferingSmooth},
}

// noiseModels is which network cleans the microphone.
var noiseModels = []settingsOption{
	{Label: "RNNoise", Value: config.NoiseModelRNNoise},
	{Label: "GTCRN", Value: config.NoiseModelGTCRN},
}

// voiceModes is how the microphone decides it is being spoken into.
var voiceModes = []settingsOption{
	{Label: "Voice activity", Value: config.VoiceModeActivity},
	{Label: "Push to talk", Value: config.VoiceModePush},
}

// pushToTalkOptions is the bindable keys, named by the platform that can answer
// for them.
func pushToTalkOptions() []settingsOption {
	names := PushToTalkKeys()

	options := make([]settingsOption, 0, len(names))
	for _, name := range names {
		options = append(options, settingsOption{Label: name, Value: name})
	}

	return options
}

// defaultedKey settles what the control shows when nothing is bound yet, or when
// the settings name a key this build no longer offers: the first of the list,
// which is what the poll would answer for anyway.
func defaultedKey(key string) string {
	names := PushToTalkKeys()
	if len(names) == 0 {
		return ""
	}

	for _, name := range names {
		if name == key {
			return key
		}
	}

	return names[0]
}

// deviceRow is a device picker. The list is enumerated by the controller, so the
// row is built from whatever it answers with — and from nothing at all during the
// index pass, where the hook is stubbed out.
//
// "System default" is always first and is what an empty identifier means, which
// is also what a device that has since been unplugged falls back to.
func (p *SettingsPage) deviceRow(label, value string, list func() []AudioDevice, set func(*config.Settings, string)) fyne.CanvasObject {
	return p.deviceRowDetailed(label, "", value, list, set)
}

// deviceRowDetailed is the same row carrying a sentence, for the one device that
// is used for something besides a call.
func (p *SettingsPage) deviceRowDetailed(label, detail, value string, list func() []AudioDevice, set func(*config.Settings, string)) fyne.CanvasObject {
	options := []settingsOption{{Label: "System default", Value: ""}}

	if list != nil {
		for _, device := range list() {
			options = append(options, settingsOption{Label: device.Name, Value: device.ID})
		}
	}

	// A device the settings name that is no longer present would otherwise leave
	// the control showing the first option and the setting saying something else.
	if !hasOption(options, value) {
		value = ""
	}

	return p.optionRow(label, detail, value, options, set)
}

func hasOption(options []settingsOption, value string) bool {
	return slices.ContainsFunc(options, func(option settingsOption) bool { return option.Value == value })
}

/* The live input meter */

// voiceLevelMeter is the bar beneath the sensitivity slider: what the microphone
// is hearing, and where the gate opens, drawn on one scale.
//
// It sits inside the microphone group rather than in one of its own because it
// is not a reading, it is the other half of a control. A threshold in decibels
// set by a number from 0 to 100 is unusable on its own — the reader has no way
// to know what their own voice measures — and the two apart on the page is the
// same problem with an extra scroll in it.
type voiceLevelMeter struct {
	block fyne.CanvasObject

	setLevel     func(ratio float32, figure string)
	setThreshold func(ratio float32)

	// The speech bar's pair, filled by speechMeterRow rather than here: it rides
	// the stream this meter opened — one microphone, one report — so it is a row
	// in a group further down the page and nil until that row is built.
	setSpeech     func(ratio float32, figure string)
	setSpeechMark func(ratio float32)
}

// levelMeterRow builds it, or names it and builds nothing during the index pass.
//
// Nothing here may be mounted by the index walk: StartInputMonitor **opens a
// capture device**, and doing that on the first keystroke in the search box, for
// a page nobody is looking at, is the bug. The row still has to be findable,
// hence the indexRow.
func (p *SettingsPage) levelMeterRow(thresholdDB int) fyne.CanvasObject {
	if p.indexing {
		return newIndexRow("Input level")
	}
	if p.hooks.StartInputMonitor == nil {
		return nil
	}

	bar, setLevel, setThreshold := newMeterBar()

	// stackedRow, like the slider above it: the control on a line of its own under
	// the explanation, and inside the group's card rather than beside it. The
	// figure beside the bar is the same dBFS the row above is set in, which is
	// what makes the threshold aimable by reading rather than by eye alone.
	row := p.stackedRow("Input level",
		"Green is what the call hears, in dBFS. The mark is where the microphone opens.", bar)

	meter := &voiceLevelMeter{block: row, setLevel: setLevel, setThreshold: setThreshold}
	p.meter = meter
	meter.setThreshold(p.gateRatio(thresholdDB))

	// Levels arrive off the audio thread and each repaint is the whole window, so
	// the controller samples rather than reporting per callback. One report feeds
	// both bars: a second monitor would be a second open of the same device.
	p.hooks.StartInputMonitor(func(m InputMeter) {
		if p.meter != meter {
			return
		}

		meter.setLevel(m.Level, meterDecibels(m.LevelDB))

		if meter.setSpeech != nil {
			meter.setSpeech(max(m.Speech, 0), meterPercent(m.Speech))
		}
	})

	return meter.block
}

/* The speech estimate */

// speechMeterRow is the second diagnostic bar: what the model makes of the
// microphone right now, against the threshold the row above it sets.
//
// It is that setting's other half exactly as the level bar is Sensitivity's — a
// threshold cannot be aimed without seeing what a voice, a keyboard and a fan
// each measure — which is why the two are a group of their own rather than two
// more rows under Microphone.
//
// It rides the stream levelMeterRow opened, so it is built after that row and
// not at all where there is none: the Microphone group is therefore ahead of
// this one in voiceSection, and the order is load-bearing. Named without being
// built during the index pass, that walk mounting every section twice.
func (p *SettingsPage) speechMeterRow(threshold int) fyne.CanvasObject {
	if p.indexing {
		return newIndexRow("Speech likelihood")
	}
	if p.meter == nil {
		return nil
	}

	block, setSpeech, setMark := newMeterBar()

	row := p.stackedRow("Speech likelihood",
		"How sure it is right now. The mark is the threshold above.", block)

	p.meter.setSpeech, p.meter.setSpeechMark = setSpeech, setMark
	setMark(vadRatio(threshold))

	return row
}

// moveVADMark walks the speech bar's threshold as its slider is dragged, for the
// reason moveGateMark does: rebuilding the group would close and reopen the
// capture device on every step of the drag — the meter being the Microphone
// group's, which a rebuild here would take with it.
func (p *SettingsPage) moveVADMark(threshold int) {
	if p.meter == nil || p.meter.setSpeechMark == nil {
		return
	}

	p.meter.setSpeechMark(vadRatio(threshold))
}

// vadRatio places the veto's threshold on the speech bar. No hook, unlike
// gateRatio: the setting is already the model's own probability as a
// percentage, so there is no scale here for a second copy to drift from.
func vadRatio(threshold int) float32 { return clamp(float32(threshold), 0, 100) / 100 }

/* The microphone test */

// echoRow plays the microphone back through the speakers. It sits directly under
// the bar because the two answer different halves of one question: the bar says
// the microphone is *heard*, and only this says what it sounds like once the
// filters, the gate and the gain have had it.
//
// A plain switch rather than a setting: it is a mode somebody turns on to listen
// to, and one that came back on at the next launch would howl at whoever left it
// there. It is taken down by the same two exits the meter is, so a rebuilt row
// starting at off is never a lie.
//
// It rides the stream the meter opened, so it is built on the same two
// conditions — and named without being built during the index pass, that walk
// mounting every section twice.
func (p *SettingsPage) echoRow() fyne.CanvasObject {
	if p.indexing {
		return newIndexRow("Hear myself")
	}
	if p.hooks.SetInputEcho == nil || p.hooks.StartInputMonitor == nil {
		return nil
	}

	return p.boolRow("Hear myself",
		"Plays your microphone back to you exactly as the call would send it. "+
			"Wear headphones, or the speakers feed straight back into it.",
		false, p.hooks.SetInputEcho)
}

// moveGateMark walks the meter's threshold as the sensitivity slider is dragged.
// Called from the row's own setter rather than from a rebuild: rebuilding the
// group would close and reopen the capture device on every step of the drag.
func (p *SettingsPage) moveGateMark(thresholdDB int) {
	if p.meter == nil {
		return
	}

	p.meter.setThreshold(p.gateRatio(thresholdDB))
}

// gateRatio is where a threshold in dBFS falls on the meter, 0-1. The bar's own
// floor and ceiling are the audio package's, reached through a hook because ui
// does not import it; without one the mark would sit at the bottom and say
// nothing.
func (p *SettingsPage) gateRatio(thresholdDB int) float32 {
	if p.hooks.GateRatio == nil {
		return 0
	}

	return p.hooks.GateRatio(thresholdDB)
}

// stopMeter closes the microphone the meter opened. Called from **both**
// showSection and Close: the page has no unmount hook, and this is "a discarded
// widget hears nothing" with a microphone attached — a settings page left with
// the monitor running holds the input device open for the rest of the run.
func (p *SettingsPage) stopMeter() {
	if p.meter == nil {
		return
	}
	p.meter = nil

	if p.hooks.StopInputMonitor != nil {
		p.hooks.StopInputMonitor()
	}
}

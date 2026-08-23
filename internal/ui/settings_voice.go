package ui

import (
	"fyne.io/fyne/v2"

	"RGOClient/internal/config"
)

/* Voice */

// voiceSection is the call: which devices it uses, what it does to the
// microphone on the way out, and what state it joins in.
func (p *SettingsPage) voiceSection() []settingsGroup {
	settings := config.Current().Voice

	return []settingsGroup{
		p.group("Devices", "Also used for the client's own sounds, there being one pair of speakers.",
			p.deviceRow("Microphone", settings.InputDevice, p.hooks.InputDevices,
				func(s *config.Settings, id string) { s.Voice.InputDevice = id }),
			p.deviceRow("Output", settings.OutputDevice, p.hooks.OutputDevices,
				func(s *config.Settings, id string) { s.Voice.OutputDevice = id }),
		),

		p.group("Microphone", "", p.microphoneRows(settings)...),

		p.group("Playback", "",
			p.numberRow("Call volume", "Everybody else in the call. The client's own sounds have their own.",
				settings.OutputVolume, 0, 100, "%",
				func(s *config.Settings, v int) { s.Voice.OutputVolume = v }),
			p.toggleRow("Repair dropped audio",
				"Rebuilds lost packets as speech instead of fading them out. Only runs while audio is being lost.",
				settings.DeepPLC,
				func(s *config.Settings, on bool) { s.Voice.DeepPLC = on }),
		),

		p.group("Joining a call", "",
			p.toggleRow("Join muted", "", settings.JoinMuted,
				func(s *config.Settings, on bool) { s.Voice.JoinMuted = on }),
			p.toggleRow("Join deafened", "Deafened also mutes you.", settings.JoinDeafened,
				func(s *config.Settings, on bool) { s.Voice.JoinDeafened = on }),
		),
	}
}

// microphoneRows is the capture group. Push-to-talk contributes two rows and
// only where the platform can answer whether a key is held — offering a mode that
// silently behaves as voice activity is worse than not offering it, which is the
// same rule the taskbar-flash group follows.
func (p *SettingsPage) microphoneRows(settings config.Voice) []fyne.CanvasObject {
	rows := []fyne.CanvasObject{
		p.numberRow("Sensitivity", "How loud a sound has to be to count as speech.",
			settings.Sensitivity, 0, 100, "",
			func(s *config.Settings, v int) {
				s.Voice.Sensitivity = v
				p.moveGateMark(v)
			}),
	}
	if meter := p.levelMeterRow(settings.Sensitivity); meter != nil {
		rows = append(rows, meter)
	}
	rows = append(rows,
		p.numberRow("Input volume", "", settings.InputVolume, 0, 200, "%",
			func(s *config.Settings, v int) { s.Voice.InputVolume = v }),
		p.toggleRow("Rumble filter",
			"Removes hum and rumble from below the voice range. Steady hiss and background sound are unaffected.",
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

	return p.optionRow(label, "", value, options, set)
}

func hasOption(options []settingsOption, value string) bool {
	for _, option := range options {
		if option.Value == value {
			return true
		}
	}

	return false
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

	setLevel     func(ratio float32)
	setThreshold func(ratio float32)
}

// levelMeterRow builds it, or names it and builds nothing during the index pass.
//
// Nothing here may be mounted by the index walk: StartInputMonitor **opens a
// capture device**, and doing that on the first keystroke in the search box, for
// a page nobody is looking at, is the bug. The row still has to be findable,
// hence the indexRow.
func (p *SettingsPage) levelMeterRow(sensitivity int) fyne.CanvasObject {
	if p.indexing {
		return newIndexRow("Input level")
	}
	if p.hooks.StartInputMonitor == nil {
		return nil
	}

	bar, setLevel, setThreshold := newLevelBar()

	// stackedRow, like the slider above it: the control on a line of its own under
	// the explanation, and inside the group's card rather than beside it.
	row := p.stackedRow("Input level",
		"Green is what the call hears. The mark is where the microphone opens.", bar)

	meter := &voiceLevelMeter{block: row, setLevel: setLevel, setThreshold: setThreshold}
	p.meter = meter
	meter.setThreshold(p.gateRatio(sensitivity))

	// Levels arrive off the audio thread and each repaint is the whole window, so
	// the controller samples rather than reporting per callback.
	p.hooks.StartInputMonitor(func(level float32) {
		if p.meter == meter {
			meter.setLevel(level)
		}
	})

	return meter.block
}

// moveGateMark walks the meter's threshold as the sensitivity slider is dragged.
// Called from the row's own setter rather than from a rebuild: rebuilding the
// group would close and reopen the capture device on every step of the drag.
func (p *SettingsPage) moveGateMark(sensitivity int) {
	if p.meter == nil {
		return
	}

	p.meter.setThreshold(p.gateRatio(sensitivity))
}

// gateRatio is where the gate's threshold falls on the meter, 0-1. The mapping
// is the audio package's, reached through a hook because ui does not import it;
// without one the mark would sit at the bottom and say nothing.
func (p *SettingsPage) gateRatio(sensitivity int) float32 {
	if p.hooks.GateRatio == nil {
		return 0
	}

	return p.hooks.GateRatio(sensitivity)
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

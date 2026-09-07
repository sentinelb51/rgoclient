package ui

import (
	"fmt"
	"strconv"

	"fyne.io/fyne/v2"

	"RGOClient/internal/config"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

/* Screenshare */

// FFmpegState is what the ffmpeg row draws: which copy the client is running,
// whether one can be downloaded for this platform, and how far a download that
// is under way has got. The controller resolves all of it — where a program
// lives and what a platform can be offered are its questions, the way a device
// list is.
type FFmpegState struct {
	Path      string // the ffmpeg in use, empty where none was found
	Directory string // where a downloaded copy is kept
	Version   string // the release the client would install
	Advice    string // what to run instead, on a platform with no download

	Size        int64 // the download, in bytes
	Downloaded  int64
	DownloadAll int64 // zero where the server declined to say

	Found      bool
	Managed    bool // the copy this client downloaded rather than one on PATH
	Offered    bool // this platform has a build to download
	Installing bool
}

// screenshareSection is what a share is encoded at. The source, its size and
// its frame rate are not here: those are the picker's, asked once per share
// because they are about what is being shared rather than about this machine.
func (p *SettingsPage) screenshareSection() []settingsGroup {
	settings := config.Current().Screenshare

	groups := []settingsGroup{}
	if group, ok := p.ffmpegGroup(); ok {
		groups = append(groups, group)
	}

	return append(groups,
		p.group("Encoder",
			"Screen sharing encodes on the graphics card when it offers a video "+
				"encoder — NVIDIA, AMD and Intel are found automatically — and on "+
				"the processor otherwise.",
			p.optionRow("Effort",
				"Quality keeps the most detail in whatever is moving; Fast spends "+
					"the least and softens motion. The difference in what a share "+
					"costs this machine is only worth choosing over on a processor "+
					"encode. None of them changes how much of your connection a "+
					"share uses.",
				settings.EncoderSpeed,
				[]settingsOption{
					{Label: "Quality", Value: config.ShareSpeedQuality},
					{Label: "Balanced", Value: config.ShareSpeedBalanced},
					{Label: "Fast", Value: config.ShareSpeedFast},
				},
				func(s *config.Settings, picked string) { s.Screenshare.EncoderSpeed = picked }),
			p.optionRow("Latency",
				"Lowest sends every frame the moment it is encoded. Buffered lets "+
					"the encoder read a little ahead before deciding, which sharpens "+
					"the picture at the same bitrate and delays what viewers see by "+
					"up to a second.",
				settings.Latency,
				[]settingsOption{
					{Label: "Lowest", Value: config.ShareLatencyLowest},
					{Label: "Buffered", Value: config.ShareLatencyBuffered},
				},
				func(s *config.Settings, picked string) { s.Screenshare.Latency = picked }),
			p.optionRow("Codec",
				"Best available sends AV1 or H.265 when the graphics card can encode "+
					"it and the server takes it, at a third or more less bandwidth than "+
					"H.264, and falls back to H.264 otherwise. H.264 forces that "+
					"fallback for viewers on older clients and servers; H.264 baseline "+
					"is the same for the oldest decoders of all, at about half again "+
					"the bandwidth.",
				settings.Codec,
				[]settingsOption{
					{Label: "Best available", Value: config.ShareCodecAuto},
					{Label: "H.264", Value: config.ShareCodecH264},
					{Label: "H.264 baseline", Value: config.ShareCodecH264Baseline},
				},
				func(s *config.Settings, picked string) { s.Screenshare.Codec = picked }),
			p.bandwidthRow(settings.Bandwidth),
			p.locked(p.numberRow("Upload limit",
				"The ceiling a custom share is encoded to. 2500 suits 720p at 30 "+
					"frames a second and 4500 suits 1080p; below about 1000 a share "+
					"stops resolving small text while the picture is moving.",
				settings.Bitrate, config.ShareBitrateMin, config.ShareBitrateMax, "kbps",
				func(s *config.Settings, kbps int) { s.Screenshare.Bitrate = kbps }),
				customBandwidthReason(settings.Bandwidth)),
			p.optionRow("Bitrate mode",
				"Variable spends the limit only on what is moving, so a still "+
					"screen costs almost nothing. Constant sends the limit at all "+
					"times, padding the picture with filler when it does not need "+
					"the bandwidth — it wastes upload, but some connections and "+
					"servers handle a steady stream better than a stream that "+
					"jumps from nothing to the limit when the screen starts moving.",
				settings.RateControl,
				[]settingsOption{
					{Label: "Variable", Value: config.ShareRateVariable},
					{Label: "Constant", Value: config.ShareRateConstant},
				},
				func(s *config.Settings, picked string) { s.Screenshare.RateControl = picked }),
			p.optionRow("Keyframes",
				"How often the whole picture is sent fresh instead of only what "+
					"changed. Someone who joins, or whose connection drops, sees "+
					"nothing until the next one. Frequent shortens that wait for "+
					"a few percent more bandwidth; Sparse lengthens it and saves "+
					"very little.",
				settings.Keyframes,
				[]settingsOption{
					{Label: "Frequent", Value: config.ShareKeyframesFrequent},
					{Label: "Standard", Value: config.ShareKeyframesStandard},
					{Label: "Sparse", Value: config.ShareKeyframesSparse},
				},
				func(s *config.Settings, picked string) { s.Screenshare.Keyframes = picked }),
		),
		p.soundGroup(settings),
	)
}

// soundGroup is what a share sends beside its picture. Empty on a machine that
// cannot capture what it is playing, which the controller answers for.
func (p *SettingsPage) soundGroup(settings config.Screenshare) settingsGroup {
	if !p.hooks.ShareAudio {
		return settingsGroup{}
	}

	return p.group("Sound",
		"Sharing a window sends only that window's sound. Sharing a whole screen "+
			"sends everything the machine is playing, including your notifications.",
		p.toggleRow("Share sound",
			"Sends what you are sharing along with the picture. People watching hear "+
				"it in stereo, and can set your share's volume separately from your "+
				"microphone.",
			settings.Audio,
			func(s *config.Settings, on bool) { s.Screenshare.Audio = on }),
		p.optionRow("Sample rate",
			"How much of the sound is captured. 48 kHz keeps all of it; the lower "+
				"rates drop the highest frequencies and use less of your upload.",
			strconv.Itoa(settings.AudioRate),
			[]settingsOption{
				{Label: "48 kHz", Value: "48000"},
				{Label: "24 kHz", Value: "24000"},
				{Label: "16 kHz", Value: "16000"},
			},
			func(s *config.Settings, picked string) {
				s.Screenshare.AudioRate = settingNumber(picked, 48000)
			}),
		p.optionRow("Bit depth",
			"How each sample is captured before it is encoded. This does not change "+
				"what people hear \u2014 it only changes how the sound reaches the encoder "+
				"on this machine.",
			strconv.Itoa(settings.AudioBitDepth),
			[]settingsOption{
				{Label: "16-bit", Value: "16"},
				{Label: "32-bit float", Value: "32"},
			},
			func(s *config.Settings, picked string) {
				s.Screenshare.AudioBitDepth = settingNumber(picked, 16)
			}),
		p.numberRow("Sound bitrate",
			"How much of your upload the sound may use. Well under what the picture "+
				"costs at any setting, and the default is past the point where more "+
				"makes a difference.",
			settings.AudioBitrate, config.ShareAudioBitrateMin, config.ShareAudioBitrateMax, "kbit/s",
			func(s *config.Settings, v int) { s.Screenshare.AudioBitrate = v }),
	)
}

// settingNumber is what an option row costs a numeric setting: the row speaks
// in strings, being the one control every choice in a section is drawn with.
func settingNumber(picked string, fallback int) int {
	v, err := strconv.Atoi(picked)
	if err != nil {
		return fallback
	}

	return v
}

// bandwidthRow is the Bandwidth option, written out rather than taken from
// optionRow because picking Custom is what unlocks the row beneath it — and a
// row that appears or greys is a section rebuild, not a repaint.
func (p *SettingsPage) bandwidthRow(value string) fyne.CanvasObject {
	var control *optionControl
	control = newOptionControl(value, []settingsOption{
		{Label: "Auto", Value: config.ShareBandwidthAuto},
		{Label: "Half", Value: config.ShareBandwidthHalf},
		{Label: "Quarter", Value: config.ShareBandwidthQuarter},
		{Label: "Custom", Value: config.ShareBandwidthCustom},
	}, func(picked string) {
		p.change(func(s *config.Settings) { s.Screenshare.Bandwidth = picked })
		control.set(picked)
		p.reload()
	})

	return p.row("Bandwidth",
		"The most a share is allowed to use. Unless Bitrate mode is Constant, a "+
			"still screen costs almost nothing whatever this is set to — the limit "+
			"only applies while the picture is moving. Auto fits the size and frame "+
			"rate you pick; Half and Quarter are for a slow upload, and soften "+
			"motion rather than shrinking the picture. Custom sets the limit "+
			"yourself.",
		control)
}

// customBandwidthReason is why the upload limit is greyed, and "" where it is
// not: the number is only read where Bandwidth hands the budget over to it.
func customBandwidthReason(bandwidth string) string {
	if bandwidth == config.ShareBandwidthCustom {
		return ""
	}

	return "Set Bandwidth to Custom to choose the limit yourself."
}

/* ffmpeg */

// ffmpegGroup stands first in the section because everything under it depends
// on ffmpeg being there at all: nothing is captured, encoded or watched without
// it, and a reader who has just been turned away from the share button arrives
// here to find out why.
func (p *SettingsPage) ffmpegGroup() (settingsGroup, bool) {
	if p.hooks.FFmpeg == nil {
		return settingsGroup{}, false
	}

	state := p.hooks.FFmpeg()

	return p.group("ffmpeg",
		"Screen sharing and inline video both run on ffmpeg. The client uses the "+
			"one on your PATH when there is one, and its own copy otherwise.",
		p.ffmpegRow(state)), true
}

// ffmpegRow is the one row that group holds, in whichever of the four states
// the client is in.
func (p *SettingsPage) ffmpegRow(state FFmpegState) fyne.CanvasObject {
	switch {
	case state.Installing:
		return p.row("Downloading", ffmpegProgress(state),
			newText(ffmpegPercent(state), theme.Colors.TimestampText, 0))

	case state.Found:
		return p.row("Installed", ffmpegWhere(state),
			newText("In use", theme.Colors.TimestampText, 0))

	case !state.Offered:
		return p.row("Not installed", state.Advice,
			newText("Missing", theme.Colors.NoticeWarning, 0))
	}

	return p.actionRow("Not installed",
		fmt.Sprintf("Downloads ffmpeg %s (%s) from GitHub, checks it against the "+
			"checksum this client was built with, and keeps it in %s.",
			state.Version, util.FormatFileSize(int(state.Size)), state.Directory),
		"Download", ToneInfo, p.installFFmpeg)
}

// ffmpegWhere names the copy in use. The path is what a reader chasing a
// missing encoder needs — which of several ffmpeg builds on the machine this
// client actually found.
func ffmpegWhere(state FFmpegState) string {
	if state.Managed {
		return fmt.Sprintf("This client's own copy, ffmpeg %s, at %s.", state.Version, state.Path)
	}

	return "Found on your PATH at " + state.Path + "."
}

// ffmpegProgress is the line under a download in flight. A server that declined
// to say how long the body is leaves only the figure that has landed, which is
// still movement.
func ffmpegProgress(state FFmpegState) string {
	if state.DownloadAll <= 0 {
		return util.FormatFileSize(int(state.Downloaded)) + " so far."
	}

	return fmt.Sprintf("%s of %s.",
		util.FormatFileSize(int(state.Downloaded)), util.FormatFileSize(int(state.DownloadAll)))
}

func ffmpegPercent(state FFmpegState) string {
	if state.DownloadAll <= 0 {
		return "…"
	}

	return fmt.Sprintf("%d%%", state.Downloaded*100/state.DownloadAll)
}

// installFFmpeg asks the controller to fetch it. The controller single-flights
// the download and redraws this section itself — as it claims, at each step and
// when it answers — so pressing again while one is out does nothing.
func (p *SettingsPage) installFFmpeg() {
	if p.hooks.InstallFFmpeg == nil {
		return
	}

	p.hooks.InstallFFmpeg(p.RefreshScreenshare)
}

// RefreshScreenshare redraws the section for a download that moved while it was
// on screen. UI thread; a no-op unless that section is the one showing.
func (p *SettingsPage) RefreshScreenshare() {
	if !p.IsOpen() || p.searching || p.section != SectionScreenshare {
		return
	}

	p.reload()
}

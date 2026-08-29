package ui

import "RGOClient/internal/config"

/* Screenshare */

// screenshareSection is what a share is encoded at. The source, its size and
// its frame rate are not here: those are the picker's, asked once per share
// because they are about what is being shared rather than about this machine.
func (p *SettingsPage) screenshareSection() []settingsGroup {
	settings := config.Current().Screenshare

	return []settingsGroup{
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
				"Auto uses AV1 when the graphics card can encode it, which sends "+
					"the same picture with about a third less bandwidth. H.264 works "+
					"for viewers on older clients and servers that do not take AV1.",
				settings.Codec,
				[]settingsOption{
					{Label: "Auto", Value: config.ShareCodecAuto},
					{Label: "H.264", Value: config.ShareCodecH264},
				},
				func(s *config.Settings, picked string) { s.Screenshare.Codec = picked }),
			p.optionRow("Bandwidth",
				"The most a share is allowed to use. A still screen costs "+
					"almost nothing whatever this is set to — the limit only "+
					"applies while the picture is moving. Auto fits the size "+
					"and frame rate you pick; Half and Quarter are for a slow "+
					"upload, and soften motion rather than shrinking the picture.",
				settings.Bandwidth,
				[]settingsOption{
					{Label: "Auto", Value: config.ShareBandwidthAuto},
					{Label: "Half", Value: config.ShareBandwidthHalf},
					{Label: "Quarter", Value: config.ShareBandwidthQuarter},
				},
				func(s *config.Settings, picked string) { s.Screenshare.Bandwidth = picked }),
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
	}
}

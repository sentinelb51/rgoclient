package audio

// EffectKind is one piece of processing the operating system applies to a
// capture device before this client sees a sample of it.
//
// Only the kinds this client's own chain can collide with are named. Anything
// else the OS reports is EffectOther, carried rather than dropped so a caller
// counting what is active is not counting a filtered list.
type EffectKind int

const (
	EffectOther EffectKind = iota
	EffectEchoCancellation
	EffectNoiseSuppression
	EffectDeepNoiseSuppression
	EffectGainControl
	EffectBeamforming
	EffectToneRemoval
)

// Effect is one effect the OS has switched on for an input.
//
// Removable is the OS's own answer to whether a process may turn it off for its
// own stream. It is reported rather than acted on: what to do about an effect is
// policy and belongs above this package.
type Effect struct {
	Kind EffectKind

	Removable bool
}

// InputEffects reports what the OS is already doing to one microphone, id being
// what a setting stored and "" the system default. Only effects that are *on*
// come back, an inactive one being nothing the signal has passed through.
//
// It opens a device of its own to ask, so it belongs on a worker rather than
// the UI thread, and a platform with no such API answers nil — which is not the
// same as an answer of none, and is why the error is worth reading.
func InputEffects(id string) ([]Effect, error) {

	return inputEffects(id)
}

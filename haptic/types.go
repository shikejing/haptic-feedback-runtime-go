package haptic

import "time"

// DeviceID 唯一标识一只手柄。
type DeviceID string

// SourceID 标识反馈指令来源（如某个游戏系统）。
type SourceID string

// EffectID 唯一标识一条反馈效果。
type EffectID string

// EffectKind 反馈效果类型。
type EffectKind int

const (
	KindRumble EffectKind = iota
	KindPulse
	KindWaveform
)

func (k EffectKind) String() string {
	switch k {
	case KindRumble:
		return "rumble"
	case KindPulse:
		return "pulse"
	case KindWaveform:
		return "waveform"
	default:
		return "unknown"
	}
}

// EnvelopePoint 是波形包络上的折点，折点之间线性插值。
type EnvelopePoint struct {
	Offset time.Duration
	Gain   float64
}

// Effect 是一条反馈指令的完整描述。不同 Kind 使用不同字段子集：
//   - Rumble:   Amplitude, Frequency, Duration
//   - Pulse:    Amplitude, Duration
//   - Waveform: Samples, SampleRate, Envelope（Duration 由采样数/采样率推出）
type Effect struct {
	ID         EffectID
	Source     SourceID
	Priority   int
	Kind       EffectKind
	Amplitude  float64
	Frequency  float64
	Duration   time.Duration
	Samples    []float64
	SampleRate int
	Envelope   []EnvelopePoint
}

// Validate 校验指令字段是否合法，返回可区分原因的错误。
func (e Effect) Validate() error {
	if e.ID == "" {
		return newError(ErrKindInvalidInput, "effect id is empty")
	}
	if e.Source == "" {
		return newError(ErrKindInvalidInput, "effect %s: source is empty", e.ID)
	}
	switch e.Kind {
	case KindRumble:
		if e.Amplitude <= 0 || e.Amplitude > 1 {
			return newError(ErrKindInvalidInput, "effect %s: rumble amplitude %v out of (0,1]", e.ID, e.Amplitude)
		}
		if e.Frequency <= 0 {
			return newError(ErrKindInvalidInput, "effect %s: rumble frequency %v must be positive", e.ID, e.Frequency)
		}
		if e.Duration <= 0 {
			return newError(ErrKindInvalidInput, "effect %s: rumble duration %v must be positive", e.ID, e.Duration)
		}
	case KindPulse:
		if e.Amplitude <= 0 || e.Amplitude > 1 {
			return newError(ErrKindInvalidInput, "effect %s: pulse amplitude %v out of (0,1]", e.ID, e.Amplitude)
		}
		if e.Duration <= 0 {
			return newError(ErrKindInvalidInput, "effect %s: pulse duration %v must be positive", e.ID, e.Duration)
		}
	case KindWaveform:
		if len(e.Samples) == 0 {
			return newError(ErrKindInvalidInput, "effect %s: waveform has no samples", e.ID)
		}
		if e.SampleRate <= 0 {
			return newError(ErrKindInvalidInput, "effect %s: waveform sample rate %d must be positive", e.ID, e.SampleRate)
		}
		for i, s := range e.Samples {
			if s < -1 || s > 1 {
				return newError(ErrKindInvalidInput, "effect %s: waveform sample %d value %v out of [-1,1]", e.ID, i, s)
			}
		}
		prev := time.Duration(-1)
		for i, p := range e.Envelope {
			if p.Offset < 0 {
				return newError(ErrKindInvalidInput, "effect %s: envelope point %d has negative offset", e.ID, i)
			}
			if p.Offset <= prev {
				return newError(ErrKindInvalidInput, "effect %s: envelope point %d offsets not strictly increasing", e.ID, i)
			}
			prev = p.Offset
		}
	default:
		return newError(ErrKindInvalidInput, "effect %s: unknown kind %d", e.ID, int(e.Kind))
	}
	return nil
}

// effectDuration 返回效果的总时长（波形按采样数/采样率计算）。
func (e Effect) effectDuration() time.Duration {
	if e.Kind == KindWaveform {
		return time.Duration(len(e.Samples)) * time.Second / time.Duration(e.SampleRate)
	}
	return e.Duration
}

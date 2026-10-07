package haptic

import "time"

// Frame 是一帧渲染输出：某设备在某时刻各通道的幅值。
type Frame struct {
	Device   DeviceID
	Time     time.Duration
	Channels []float64
}

// sampleEffect 计算效果在其本地时刻 t（相对效果起点）的原始幅值。
// 纯函数：相同输入必然得到相同输出。t 超出效果时长时返回 0。
func sampleEffect(e Effect, t time.Duration) float64 {
	if t < 0 || t >= e.effectDuration() {
		return 0
	}
	switch e.Kind {
	case KindRumble:
		// 控制帧率下持续震动输出恒定幅值，频率作为设备侧参数携带。
		return e.Amplitude
	case KindPulse:
		return e.Amplitude
	case KindWaveform:
		// 定位采样点并线性插值，再乘以包络增益。
		pos := float64(t) * float64(e.SampleRate) / float64(time.Second)
		i := int(pos)
		if i >= len(e.Samples) {
			return 0
		}
		v := e.Samples[i]
		if i+1 < len(e.Samples) {
			frac := pos - float64(i)
			v = v*(1-frac) + e.Samples[i+1]*frac
		}
		return v * envelopeGain(e.Envelope, t)
	default:
		return 0
	}
}

// envelopeGain 计算包络在时刻 t 的增益。
// 无包络时增益为 1；折点间线性插值；首点之前、末点之后取端点值。
func envelopeGain(env []EnvelopePoint, t time.Duration) float64 {
	if len(env) == 0 {
		return 1
	}
	if t <= env[0].Offset {
		return env[0].Gain
	}
	last := env[len(env)-1]
	if t >= last.Offset {
		return last.Gain
	}
	for i := 0; i+1 < len(env); i++ {
		a, b := env[i], env[i+1]
		if t >= a.Offset && t <= b.Offset {
			span := float64(b.Offset - a.Offset)
			if span <= 0 {
				return b.Gain
			}
			frac := float64(t-a.Offset) / span
			return a.Gain*(1-frac) + b.Gain*frac
		}
	}
	return last.Gain
}

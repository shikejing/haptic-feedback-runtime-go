package haptic

import "fmt"

// Capabilities 描述一个手柄的触觉输出能力。
type Capabilities struct {
	// Channels 输出通道数（例如左右两个线性马达为 2）。
	Channels int
	// MinFrequency / MaxFrequency 可表达的频率范围 Hz。
	MinFrequency float64
	MaxFrequency float64
	// SampleRate 设备采样率 Hz，波形重采样到该速率。
	SampleRate int
	// MaxAmplitude 最大振幅 [0,1]，超出部分被截断。
	MaxAmplitude float64
}

// Validate 校验能力描述本身是否自洽。
func (c Capabilities) Validate() error {
	if c.Channels <= 0 {
		return fmt.Errorf("%w: device has no output channels", ErrInvalidCommand)
	}
	if c.MaxFrequency <= c.MinFrequency {
		return fmt.Errorf("%w: invalid frequency range [%.1f, %.1f]", ErrInvalidCommand, c.MinFrequency, c.MaxFrequency)
	}
	if c.SampleRate <= 0 {
		return fmt.Errorf("%w: invalid sample rate %d", ErrInvalidCommand, c.SampleRate)
	}
	if c.MaxAmplitude <= 0 || c.MaxAmplitude > 1 {
		return fmt.Errorf("%w: invalid max amplitude %.3f", ErrInvalidCommand, c.MaxAmplitude)
	}
	return nil
}

// Degradation 记录一次能力降级中发生了什么，保证降级可解释而非静默。
type Degradation struct {
	AmplitudeClipped   bool // 振幅超出设备上限被截断
	FrequencyClamped   bool // 频率超出设备范围被钳制
	WaveformFlattened  bool // 设备采样率过低，包络被近似为分段常量
	RequestedAmplitude float64
	AppliedAmplitude   float64
	RequestedFrequency float64
	AppliedFrequency   float64
}

// mappedCommand 是经过能力映射后、可以安全下发的指令副本。
type mappedCommand struct {
	amplitude float64
	frequency float64
	envelope  []EnvelopePoint // 已按设备采样率重采样（如需）
	deg       Degradation
}

// mapToDevice 把一条指令按设备能力做映射/降级。
// 表达不了的部分采用确定的近似策略：
//   - 振幅：截断到 MaxAmplitude；
//   - 频率：钳制到 [MinFrequency, MaxFrequency]；
//   - 包络：当设备采样率不足以表达控制点密度时，重采样为设备可表达的分段曲线。
//
// 绝不静默丢弃，也绝不越界输出：所有调整都记录在 Degradation 中。
func mapToDevice(c Command, caps Capabilities) mappedCommand {
	m := mappedCommand{
		deg: Degradation{
			RequestedAmplitude: c.Amplitude,
			RequestedFrequency: c.Frequency,
		},
	}

	// 振幅映射：截断到设备上限。
	amp := c.Amplitude
	if amp > caps.MaxAmplitude {
		amp = caps.MaxAmplitude
		m.deg.AmplitudeClipped = true
	}
	m.amplitude = amp
	m.deg.AppliedAmplitude = amp

	// 频率映射：钳制到设备范围。
	freq := c.Frequency
	if freq < caps.MinFrequency {
		freq = caps.MinFrequency
		m.deg.FrequencyClamped = true
	}
	if freq > caps.MaxFrequency {
		freq = caps.MaxFrequency
		m.deg.FrequencyClamped = true
	}
	m.frequency = freq
	m.deg.AppliedFrequency = freq

	// 包络映射：设备采样率限制了可表达的最快变化，
	// 控制点间隔小于设备采样周期时，按采样周期重采样（取区间最大值，保守不丢峰值）。
	if c.Kind == KindEnvelope {
		m.envelope = resampleEnvelope(c.Envelope, caps.SampleRate)
		if len(m.envelope) != len(c.Envelope) {
			m.deg.WaveformFlattened = true
		}
	}
	return m
}

// resampleEnvelope 按设备采样率重采样包络。
// 采样周期内的多个控制点合并为一个（取最大振幅），保证峰值不被静默丢弃。
func resampleEnvelope(env []EnvelopePoint, sampleRate int) []EnvelopePoint {
	if len(env) == 0 || sampleRate <= 0 {
		return env
	}
	period := int64(1_000_000_000) / int64(sampleRate) // 纳秒
	if period <= 0 {
		return env
	}
	out := make([]EnvelopePoint, 0, len(env))
	bucket := int64(-1)
	for _, p := range env {
		b := int64(p.Time) / period
		if b != bucket {
			out = append(out, p)
			bucket = b
		} else if p.Amplitude > out[len(out)-1].Amplitude {
			out[len(out)-1].Amplitude = p.Amplitude
		}
	}
	return out
}

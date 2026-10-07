package haptic

import "fmt"

// DeviceCapability 描述一只手柄的触觉输出能力。
type DeviceCapability struct {
	Channels     int     // 独立输出通道数
	MinFrequency float64 // 可表达的最低频率 Hz
	MaxFrequency float64 // 可表达的最高频率 Hz
	SampleRate   int     // 波形采样率上限 Hz
	MaxAmplitude float64 // 最大振幅 (0,1]
}

// Validate 校验能力描述本身是否自洽。
func (c DeviceCapability) Validate() error {
	if c.Channels <= 0 {
		return newError(ErrKindInvalidInput, "device capability: channels %d must be positive", c.Channels)
	}
	if c.MinFrequency <= 0 {
		return newError(ErrKindInvalidInput, "device capability: min frequency %v must be positive", c.MinFrequency)
	}
	if c.MaxFrequency < c.MinFrequency {
		return newError(ErrKindInvalidInput, "device capability: max frequency %v below min %v", c.MaxFrequency, c.MinFrequency)
	}
	if c.SampleRate <= 0 {
		return newError(ErrKindInvalidInput, "device capability: sample rate %d must be positive", c.SampleRate)
	}
	if c.MaxAmplitude <= 0 || c.MaxAmplitude > 1 {
		return newError(ErrKindInvalidInput, "device capability: max amplitude %v out of (0,1]", c.MaxAmplitude)
	}
	return nil
}

// Degradation 记录一次能力降级/近似的说明，保证可解释、不静默。
type Degradation struct {
	Field  string // 被调整的字段
	From   string
	To     string
	Reason string
}

// mapEffectToDevice 把一条效果映射到目标设备能力上。
// 返回映射后的效果与降级说明列表；完全无法表达时返回 ErrKindCapabilityMismatch。
// 映射是确定性的纯函数：同一 (effect, caps) 必然得到同一结果。
func mapEffectToDevice(e Effect, caps DeviceCapability) (Effect, []Degradation, error) {
	if err := caps.Validate(); err != nil {
		return e, nil, err
	}
	out := e
	var degs []Degradation

	clampAmp := func(a float64) float64 {
		if a > caps.MaxAmplitude {
			degs = append(degs, Degradation{
				Field:  "amplitude",
				From:   fmt.Sprintf("%v", a),
				To:     fmt.Sprintf("%v", caps.MaxAmplitude),
				Reason: "exceeds device max amplitude, clamped",
			})
			return caps.MaxAmplitude
		}
		return a
	}

	switch e.Kind {
	case KindRumble:
		out.Amplitude = clampAmp(e.Amplitude)
		f := e.Frequency
		if f < caps.MinFrequency {
			degs = append(degs, Degradation{
				Field:  "frequency",
				From:   fmt.Sprintf("%v", f),
				To:     fmt.Sprintf("%v", caps.MinFrequency),
				Reason: "below device min frequency, raised to nearest representable",
			})
			f = caps.MinFrequency
		} else if f > caps.MaxFrequency {
			degs = append(degs, Degradation{
				Field:  "frequency",
				From:   fmt.Sprintf("%v", f),
				To:     fmt.Sprintf("%v", caps.MaxFrequency),
				Reason: "above device max frequency, lowered to nearest representable",
			})
			f = caps.MaxFrequency
		}
		out.Frequency = f
	case KindPulse:
		out.Amplitude = clampAmp(e.Amplitude)
	case KindWaveform:
		if e.SampleRate > caps.SampleRate {
			out.Samples = resampleLinear(e.Samples, e.SampleRate, caps.SampleRate)
			degs = append(degs, Degradation{
				Field:  "sample-rate",
				From:   fmt.Sprintf("%d", e.SampleRate),
				To:     fmt.Sprintf("%d", caps.SampleRate),
				Reason: "exceeds device sample rate, resampled with linear interpolation",
			})
			out.SampleRate = caps.SampleRate
		}
		// 波形采样点幅值钳制到设备量程。
		clamped := false
		samples := make([]float64, len(out.Samples))
		for i, s := range out.Samples {
			v := s
			if v > caps.MaxAmplitude {
				v = caps.MaxAmplitude
				clamped = true
			} else if v < -caps.MaxAmplitude {
				v = -caps.MaxAmplitude
				clamped = true
			}
			samples[i] = v
		}
		if clamped {
			out.Samples = samples
			degs = append(degs, Degradation{
				Field:  "amplitude",
				From:   "waveform samples",
				To:     fmt.Sprintf("[-%v,%v]", caps.MaxAmplitude, caps.MaxAmplitude),
				Reason: "samples exceed device max amplitude, clamped",
			})
		}
	}
	return out, degs, nil
}

// resampleLinear 用线性插值把采样序列从 fromHz 重采样到 toHz（仅支持降采样方向的使用场景，
// 但函数本身对任意比率都有确定结果）。输出长度按时长守恒计算。
func resampleLinear(samples []float64, fromHz, toHz int) []float64 {
	if fromHz <= 0 || toHz <= 0 || len(samples) == 0 {
		return nil
	}
	if fromHz == toHz {
		out := make([]float64, len(samples))
		copy(out, samples)
		return out
	}
	// 输出长度：保持总时长不变，floor(n * to / from)，至少 1 个采样。
	n := len(samples) * toHz / fromHz
	if n < 1 {
		n = 1
	}
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		// 输出第 i 点对应输入位置（以输入采样索引为单位）。
		pos := float64(i) * float64(fromHz) / float64(toHz)
		j := int(pos)
		if j >= len(samples)-1 {
			out[i] = samples[len(samples)-1]
			continue
		}
		frac := pos - float64(j)
		out[i] = samples[j]*(1-frac) + samples[j+1]*frac
	}
	return out
}

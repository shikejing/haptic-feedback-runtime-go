package haptic

import (
	"errors"
	"testing"
	"time"
)

// 振幅超出设备上限被截断，且降级被记录而非静默。
func TestAmplitudeClipped(t *testing.T) {
	rt := NewRuntime(DefaultConfig())
	caps := testCaps()
	caps.MaxAmplitude = 0.5
	if err := rt.AddDevice("dev-A", caps); err != nil {
		t.Fatal(err)
	}
	_, err := rt.Submit(Command{Device: "dev-A", Source: "s", Kind: KindContinuous,
		Priority: 1, Duration: 100 * time.Millisecond, Amplitude: 0.9, Frequency: 100})
	if err != nil {
		t.Fatal(err)
	}
	out := rt.Render(1)
	if out[0].Channels[0] != 0.5 {
		t.Fatalf("want clipped 0.5, got %v", out[0].Channels)
	}
	if len(out[0].Degraded) != 1 || !out[0].Degraded[0].AmplitudeClipped {
		t.Fatalf("degradation not recorded: %+v", out[0].Degraded)
	}
	if out[0].Degraded[0].RequestedAmplitude != 0.9 || out[0].Degraded[0].AppliedAmplitude != 0.5 {
		t.Fatalf("degradation values wrong: %+v", out[0].Degraded[0])
	}
}

// 频率越界被钳制到设备范围，绝不越界输出。
func TestFrequencyClamped(t *testing.T) {
	caps := testCaps() // [50, 500]
	m := mapToDevice(Command{Kind: KindContinuous, Amplitude: 0.5, Frequency: 10}, caps)
	if m.frequency != 50 || !m.deg.FrequencyClamped {
		t.Fatalf("low clamp: got freq=%v deg=%+v", m.frequency, m.deg)
	}
	m = mapToDevice(Command{Kind: KindContinuous, Amplitude: 0.5, Frequency: 9000}, caps)
	if m.frequency != 500 || !m.deg.FrequencyClamped {
		t.Fatalf("high clamp: got freq=%v deg=%+v", m.frequency, m.deg)
	}
	m = mapToDevice(Command{Kind: KindContinuous, Amplitude: 0.5, Frequency: 200}, caps)
	if m.frequency != 200 || m.deg.FrequencyClamped {
		t.Fatalf("in-range freq should pass through: got %v", m.frequency)
	}
}

// 低采样率设备的包络重采样：密集控制点按采样周期合并，保留峰值。
func TestEnvelopeResampled(t *testing.T) {
	env := []EnvelopePoint{
		{Time: 0, Amplitude: 0.1},
		{Time: 100 * time.Microsecond, Amplitude: 0.9}, // 与上一点同采样周期（1ms）
		{Time: 5 * time.Millisecond, Amplitude: 0.5},
	}
	m := mapToDevice(Command{Kind: KindEnvelope, Amplitude: 1, Frequency: 100, Envelope: env}, testCaps())
	if !m.deg.WaveformFlattened {
		t.Fatal("expected WaveformFlattened degradation")
	}
	if len(m.envelope) != 2 {
		t.Fatalf("want 2 resampled points, got %d", len(m.envelope))
	}
	// 峰值 0.9 必须保留，不得静默丢弃。
	if m.envelope[0].Amplitude != 0.9 {
		t.Fatalf("peak lost: got %v", m.envelope[0].Amplitude)
	}
}

// 非法能力描述被拒绝，错误原因可区分。
func TestInvalidCapabilities(t *testing.T) {
	rt := NewRuntime(DefaultConfig())
	bad := testCaps()
	bad.Channels = 0
	err := rt.AddDevice("dev-A", bad)
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("want ErrInvalidCommand, got %v", err)
	}
}

// 非法指令被拒绝且原因可区分，不会崩溃或产生未定义输出。
func TestInvalidCommandRejected(t *testing.T) {
	rt := newTestRuntime(t, DefaultConfig())
	cases := []Command{
		{Device: "dev-A", Source: "s", Kind: KindContinuous, Duration: time.Millisecond, Amplitude: 1.5}, // 振幅越界
		{Device: "dev-A", Source: "s", Kind: KindContinuous, Duration: 0, Amplitude: 0.5},                // 时长非正
		{Device: "dev-A", Source: "s", Kind: Kind(99), Duration: time.Millisecond, Amplitude: 0.5},       // 未知类型
		{Device: "dev-A", Source: "s", Kind: KindEnvelope, Duration: time.Millisecond, Amplitude: 0.5},   // 缺包络
		{Device: "", Source: "s", Kind: KindContinuous, Duration: time.Millisecond, Amplitude: 0.5},      // 缺设备
		{Device: "dev-A", Source: "", Kind: KindContinuous, Duration: time.Millisecond, Amplitude: 0.5},  // 缺来源
	}
	for i, c := range cases {
		_, err := rt.Submit(c)
		if !errors.Is(err, ErrInvalidCommand) {
			t.Fatalf("case %d: want ErrInvalidCommand, got %v", i, err)
		}
	}
	// 提交到未知设备。
	_, err := rt.Submit(Command{Device: "ghost", Source: "s", Kind: KindContinuous,
		Duration: time.Millisecond, Amplitude: 0.5})
	if !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("want ErrUnknownDevice, got %v", err)
	}
}

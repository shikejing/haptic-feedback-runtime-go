package haptic

import (
	"strings"
	"testing"
	"time"
)

// 振幅超出设备量程时被钳制，且输出绝不越界。
func TestCapabilityAmplitudeClamped(t *testing.T) {
	caps := testCaps()
	caps.MaxAmplitude = 0.5
	mapped, degs, err := mapEffectToDevice(rumble("r", "s", 1, 0.9, time.Second), caps)
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Amplitude != 0.5 {
		t.Fatalf("amplitude = %v, want 0.5", mapped.Amplitude)
	}
	if len(degs) == 0 || degs[0].Field != "amplitude" {
		t.Fatalf("expected amplitude degradation note, got %+v", degs)
	}
}

// 频率超出设备范围时映射到最近可表达值。
func TestCapabilityFrequencyMapped(t *testing.T) {
	caps := testCaps() // [20, 500]
	low := rumble("r", "s", 1, 0.5, time.Second)
	low.Frequency = 5
	mapped, degs, err := mapEffectToDevice(low, caps)
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Frequency != 20 {
		t.Fatalf("frequency = %v, want 20", mapped.Frequency)
	}
	if len(degs) == 0 || degs[0].Field != "frequency" {
		t.Fatalf("expected frequency degradation note, got %+v", degs)
	}

	high := rumble("r", "s", 1, 0.5, time.Second)
	high.Frequency = 2000
	mapped, _, err = mapEffectToDevice(high, caps)
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Frequency != 500 {
		t.Fatalf("frequency = %v, want 500", mapped.Frequency)
	}
}

// 波形采样率超限时确定性重采样，时长保持守恒。
func TestCapabilityWaveformResampled(t *testing.T) {
	caps := testCaps()
	caps.SampleRate = 1000
	w := Effect{
		ID: "w", Source: "s", Priority: 1, Kind: KindWaveform,
		Samples:    []float64{0, 0.5, 1, 0.5, 0, -0.5, -1, -0.5},
		SampleRate: 8000,
	}
	mapped, degs, err := mapEffectToDevice(w, caps)
	if err != nil {
		t.Fatal(err)
	}
	if mapped.SampleRate != 1000 {
		t.Fatalf("sample rate = %d, want 1000", mapped.SampleRate)
	}
	// 8 采样 @8kHz = 1ms → 1 采样 @1kHz。
	if len(mapped.Samples) != 1 {
		t.Fatalf("resampled length = %d, want 1", len(mapped.Samples))
	}
	found := false
	for _, d := range degs {
		if d.Field == "sample-rate" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected sample-rate degradation note, got %+v", degs)
	}
	// 重采样确定性：再跑一次结果一致。
	mapped2, _, err := mapEffectToDevice(w, caps)
	if err != nil {
		t.Fatal(err)
	}
	if len(mapped2.Samples) != len(mapped.Samples) || mapped2.Samples[0] != mapped.Samples[0] {
		t.Fatal("resample not deterministic")
	}
}

// 能力描述自身不合法时返回可区分错误，而不是静默接受。
func TestCapabilityInvalidRejected(t *testing.T) {
	bad := testCaps()
	bad.Channels = 0
	if _, _, err := mapEffectToDevice(rumble("r", "s", 1, 0.5, time.Second), bad); err == nil {
		t.Fatal("expected error for invalid capability")
	}
	rt, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.AttachDevice("pad", bad); err == nil {
		t.Fatal("expected attach error for invalid capability")
	}
}

// 降级后的效果在渲染时绝不越界输出。
func TestCapabilityNeverOutOfRange(t *testing.T) {
	rt, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	caps := testCaps()
	caps.Channels = 1
	caps.MaxAmplitude = 0.4
	if err := rt.AttachDevice("pad", caps); err != nil {
		t.Fatal(err)
	}
	w := Effect{
		ID: "w", Source: "s", Priority: 1, Kind: KindWaveform,
		Samples:    []float64{1, 1, 1, 1},
		SampleRate: 4000,
	}
	if _, err := rt.Submit("pad", w); err != nil {
		t.Fatal(err)
	}
	if err := rt.Advance(time.Millisecond); err != nil {
		t.Fatal(err)
	}
	frames, err := rt.Render("pad", 0, 2*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		for ch, v := range f.Channels {
			if v > 0.4 || v < -0.4 {
				t.Fatalf("frame %v ch %d out of range: %v", f.Time, ch, v)
			}
		}
	}
}

// 错误种类可区分。
func TestErrorKindsDistinguishable(t *testing.T) {
	rt, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	_, err = rt.Submit("ghost", rumble("x", "s", 1, 0.5, time.Second))
	he, ok := err.(*Error)
	if !ok || he.Kind != ErrKindUnknownDevice {
		t.Fatalf("want ErrKindUnknownDevice, got %v", err)
	}
	if err := rt.AttachDevice("pad", testCaps()); err != nil {
		t.Fatal(err)
	}
	bad := rumble("bad", "s", 1, 5.0, time.Second)
	_, err = rt.Submit("pad", bad)
	he, ok = err.(*Error)
	if !ok || he.Kind != ErrKindInvalidInput {
		t.Fatalf("want ErrKindInvalidInput, got %v", err)
	}
	if !strings.Contains(he.Error(), "amplitude") {
		t.Fatalf("error should explain the cause, got %q", he.Error())
	}
}

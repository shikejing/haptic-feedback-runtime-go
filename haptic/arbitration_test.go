package haptic

import (
	"testing"
	"time"
)

func testCaps() Capabilities {
	return Capabilities{
		Channels:     2,
		MinFrequency: 50,
		MaxFrequency: 500,
		SampleRate:   1000,
		MaxAmplitude: 1.0,
	}
}

func newTestRuntime(t *testing.T, cfg Config) *Runtime {
	t.Helper()
	rt := NewRuntime(cfg)
	if err := rt.AddDevice("dev-A", testCaps()); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	return rt
}

// 高优先级压制低优先级。
func TestArbitrationPrioritySuppression(t *testing.T) {
	rt := newTestRuntime(t, DefaultConfig())
	low := Command{Device: "dev-A", Source: "ui", Kind: KindContinuous,
		Priority: 1, Duration: 200 * time.Millisecond, Amplitude: 0.9, Frequency: 100}
	high := Command{Device: "dev-A", Source: "game", Kind: KindContinuous,
		Priority: 10, Duration: 200 * time.Millisecond, Amplitude: 0.4, Frequency: 100}
	if _, err := rt.Submit(low); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Submit(high); err != nil {
		t.Fatal(err)
	}
	out := rt.Render(1)
	if len(out) != 1 {
		t.Fatalf("want 1 frame output, got %d", len(out))
	}
	// 低优先级被压制，输出只能来自 high（0.4）。
	if out[0].Channels[0] != 0.4 {
		t.Fatalf("want 0.4 from high priority, got %v", out[0].Channels)
	}
	act, err := rt.ActiveAt("dev-A")
	if err != nil {
		t.Fatal(err)
	}
	if len(act) != 2 {
		t.Fatalf("want 2 active feedbacks, got %d", len(act))
	}
	var sawSuppressed bool
	for _, a := range act {
		if a.Source == "ui" && !a.Suppressed {
			t.Fatal("low priority feedback should be suppressed")
		}
		if a.Source == "ui" {
			sawSuppressed = true
		}
	}
	if !sawSuppressed {
		t.Fatal("expected ui feedback in active set")
	}
}

// 同优先级按 MergeMax 合并。
func TestArbitrationMergeMax(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Merge = MergeMax
	rt := newTestRuntime(t, cfg)
	for i, amp := range []float64{0.3, 0.7, 0.5} {
		c := Command{Device: "dev-A", Source: SourceID(string(rune('a' + i))), Kind: KindContinuous,
			Priority: 5, Duration: 200 * time.Millisecond, Amplitude: amp, Frequency: 100}
		if _, err := rt.Submit(c); err != nil {
			t.Fatal(err)
		}
	}
	out := rt.Render(1)
	// 通道分配按内容排序：振幅大者占前；溢出者按 MergeMax 并入最后通道。
	// ch0 = 0.7，ch1 = max(0.5, 0.3) = 0.5。
	if out[0].Channels[0] != 0.7 || out[0].Channels[1] != 0.5 {
		t.Fatalf("MergeMax want [0.7 0.5], got %v", out[0].Channels)
	}
}

// 同优先级按 MergeSum 合并并截断到设备上限（单通道设备强制合并）。
func TestArbitrationMergeSumClamp(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Merge = MergeSum
	rt := NewRuntime(cfg)
	caps := testCaps()
	caps.Channels = 1
	if err := rt.AddDevice("dev-A", caps); err != nil {
		t.Fatal(err)
	}
	for i, amp := range []float64{0.6, 0.6} {
		c := Command{Device: "dev-A", Source: SourceID(string(rune('a' + i))), Kind: KindContinuous,
			Priority: 5, Duration: 200 * time.Millisecond, Amplitude: amp, Frequency: 100}
		if _, err := rt.Submit(c); err != nil {
			t.Fatal(err)
		}
	}
	out := rt.Render(1)
	// 0.6+0.6=1.2，截断到 MaxAmplitude=1.0。
	if out[0].Channels[0] != 1.0 {
		t.Fatalf("MergeSum want clamped 1.0, got %v", out[0].Channels)
	}
}

// 仲裁结果与指令到达顺序无关。
func TestArbitrationOrderIndependence(t *testing.T) {
	mk := func() *Runtime {
		return NewRuntime(DefaultConfig())
	}
	cmds := []Command{
		{Device: "dev-A", Source: "s1", Kind: KindContinuous, Priority: 3, Duration: 200 * time.Millisecond, Amplitude: 0.3, Frequency: 100},
		{Device: "dev-A", Source: "s2", Kind: KindContinuous, Priority: 7, Duration: 200 * time.Millisecond, Amplitude: 0.5, Frequency: 100},
		{Device: "dev-A", Source: "s3", Kind: KindContinuous, Priority: 7, Duration: 200 * time.Millisecond, Amplitude: 0.8, Frequency: 100},
		{Device: "dev-A", Source: "s4", Kind: KindPulse, Priority: 1, Amplitude: 1.0, Frequency: 100},
	}
	// 正序与逆序提交，渲染结果必须逐帧一致。
	orders := [][]int{{0, 1, 2, 3}, {3, 2, 1, 0}, {2, 0, 3, 1}}
	var ref []frameOutput
	for k, order := range orders {
		rt := mk()
		if err := rt.AddDevice("dev-A", testCaps()); err != nil {
			t.Fatal(err)
		}
		for _, i := range order {
			if _, err := rt.Submit(cmds[i]); err != nil {
				t.Fatal(err)
			}
		}
		out := rt.Render(10)
		if k == 0 {
			ref = out
			continue
		}
		if len(out) != len(ref) {
			t.Fatalf("order %v: frame count mismatch", order)
		}
		for i := range out {
			if len(out[i].Channels) != len(ref[i].Channels) {
				t.Fatalf("order %v frame %d: channel count mismatch", order, i)
			}
			for ch := range out[i].Channels {
				if out[i].Channels[ch] != ref[i].Channels[ch] {
					t.Fatalf("order %v frame %d ch %d: got %v want %v",
						order, i, ch, out[i].Channels, ref[i].Channels)
				}
			}
		}
	}
}

// 获胜者超过通道数时合并进最后通道，不发生通道重复占用。
func TestChannelOverflowMerges(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Merge = MergeMax
	rt := NewRuntime(cfg)
	caps := testCaps()
	caps.Channels = 1
	if err := rt.AddDevice("dev-A", caps); err != nil {
		t.Fatal(err)
	}
	for i, amp := range []float64{0.4, 0.4, 0.4} {
		c := Command{Device: "dev-A", Source: SourceID(string(rune('a' + i))), Kind: KindContinuous,
			Priority: 5, Duration: 200 * time.Millisecond, Amplitude: amp, Frequency: 100}
		if _, err := rt.Submit(c); err != nil {
			t.Fatal(err)
		}
	}
	out := rt.Render(1)
	if len(out[0].Channels) != 1 {
		t.Fatalf("want 1 channel, got %d", len(out[0].Channels))
	}
	// 单通道下 3 个获胜者按 MergeMax 合并：max(0.4,0.4,0.4)=0.4。
	if out[0].Channels[0] != 0.4 {
		t.Fatalf("want merged 0.4, got %v", out[0].Channels)
	}
}

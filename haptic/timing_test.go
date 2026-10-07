package haptic

import (
	"testing"
	"time"
)

// 延迟补偿：输出按 (设备延迟+抖动窗口) 向上取整的帧数提前排程。
func TestLatencyCompensationLead(t *testing.T) {
	cfg := DefaultConfig() // FrameDuration=5ms, Latency=10ms, Jitter=2ms → lead=3 帧
	rt := newTestRuntime(t, cfg)
	if _, err := rt.Submit(Command{Device: "dev-A", Source: "s", Kind: KindContinuous,
		Priority: 1, Start: 10, Duration: 50 * time.Millisecond, Amplitude: 0.8, Frequency: 100}); err != nil {
		t.Fatal(err)
	}
	out := rt.Render(15)
	first := -1
	for _, fo := range out {
		if fo.Channels[0] > 0 {
			first = int(fo.Frame)
			break
		}
	}
	// 意图第 10 帧生效，提前 3 帧补偿 → 第 7 帧开始输出。
	if first != 7 {
		t.Fatalf("want output starting at frame 7, got %d", first)
	}
}

// 禁止时间倒流：起始帧在过去时被钳制到当前帧（再减提前量，不低于 0）。
func TestNoTimeReversal(t *testing.T) {
	rt := newTestRuntime(t, DefaultConfig())
	rt.Render(20) // 推进到帧 20
	if _, err := rt.Submit(Command{Device: "dev-A", Source: "s", Kind: KindContinuous,
		Priority: 1, Start: 5, Duration: 50 * time.Millisecond, Amplitude: 0.8, Frequency: 100}); err != nil {
		t.Fatal(err)
	}
	out := rt.Render(30)
	first := -1
	for _, fo := range out {
		if fo.Channels[0] > 0 {
			first = int(fo.Frame)
			break
		}
	}
	// 意图帧 5 在过去，被钳制到 now=20；补偿提前量指向的帧 17-19
	// 已经被渲染过，运行时不改写历史，输出从下一渲染帧 20 开始。
	// 关键是：绝不会在过去帧（<20）产生输出，时间轴不倒流。
	if first != 20 {
		t.Fatalf("want clamped start at frame 20, got %d", first)
	}
}

// 同一输入按不同节奏渲染（模拟不同负载），时间轴必须逐帧一致。
func TestRenderChunkingDeterministic(t *testing.T) {
	setup := func() *Runtime {
		rt := newTestRuntime(t, DefaultConfig())
		if _, err := rt.Submit(Command{Device: "dev-A", Source: "s1", Kind: KindContinuous,
			Priority: 1, Duration: 100 * time.Millisecond, Amplitude: 0.6, Frequency: 100}); err != nil {
			t.Fatal(err)
		}
		if _, err := rt.Submit(Command{Device: "dev-A", Source: "s2", Kind: KindEnvelope,
			Priority: 2, Duration: 60 * time.Millisecond, Amplitude: 0.9, Frequency: 100,
			Envelope: []EnvelopePoint{{Time: 0, Amplitude: 0}, {Time: 30 * time.Millisecond, Amplitude: 1}, {Time: 60 * time.Millisecond, Amplitude: 0}}}); err != nil {
			t.Fatal(err)
		}
		return rt
	}
	a := setup()
	outA := a.Render(12)
	b := setup()
	var outB []frameOutput
	for _, n := range []int{1, 4, 2, 5} {
		outB = append(outB, b.Render(n)...)
	}
	if len(outA) != len(outB) {
		t.Fatalf("frame count mismatch: %d vs %d", len(outA), len(outB))
	}
	for i := range outA {
		for ch := range outA[i].Channels {
			if outA[i].Channels[ch] != outB[i].Channels[ch] {
				t.Fatalf("frame %d ch %d differs: %v vs %v",
					i, ch, outA[i].Channels[ch], outB[i].Channels[ch])
			}
		}
	}
}

// 脉冲只触发一次，持续恰好 PulseDuration 对应的帧数，不重复。
func TestPulseFiresOnce(t *testing.T) {
	cfg := DefaultConfig() // PulseDuration=20ms / 5ms = 4 帧
	rt := newTestRuntime(t, cfg)
	if _, err := rt.Submit(Command{Device: "dev-A", Source: "s", Kind: KindPulse,
		Priority: 1, Amplitude: 0.9, Frequency: 100}); err != nil {
		t.Fatal(err)
	}
	out := rt.Render(20)
	on := 0
	for _, fo := range out {
		if fo.Channels[0] > 0 {
			on++
		}
	}
	if on != 4 {
		t.Fatalf("pulse should be on for exactly 4 frames, got %d", on)
	}
}

// 包络波形按线性插值随时间演化。
func TestEnvelopeInterpolation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DeviceLatency = 0
	cfg.MaxSchedulingJitter = 0 // 关闭提前量，便于断言
	rt := newTestRuntime(t, cfg)
	if _, err := rt.Submit(Command{Device: "dev-A", Source: "s", Kind: KindEnvelope,
		Priority: 1, Duration: 20 * time.Millisecond, Amplitude: 1.0, Frequency: 100,
		Envelope: []EnvelopePoint{{Time: 0, Amplitude: 0}, {Time: 20 * time.Millisecond, Amplitude: 1}}}); err != nil {
		t.Fatal(err)
	}
	out := rt.Render(4)
	// 帧 i 的相对时间 = i*5ms，包络从 0 线性升到 1。
	want := []float64{0, 0.25, 0.5, 0.75}
	for i, fo := range out {
		if fo.Channels[0] != want[i] {
			t.Fatalf("frame %d: want %v, got %v", i, want[i], fo.Channels[0])
		}
	}
}

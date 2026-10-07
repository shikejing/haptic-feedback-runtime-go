package haptic

import (
	"testing"
	"time"
)

// 构造一段包含多设备、多来源、撤销、热插拔的录制脚本。
func buildRecording(t *testing.T, cfg Config) (*Runtime, []Event) {
	t.Helper()
	rt, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rt.EnableRecording()
	if err := rt.AttachDevice("pad-1", testCaps()); err != nil {
		t.Fatal(err)
	}
	caps2 := testCaps()
	caps2.Channels = 1
	caps2.MaxAmplitude = 0.6
	if err := rt.AttachDevice("pad-2", caps2); err != nil {
		t.Fatal(err)
	}
	if err := rt.Advance(5 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Submit("pad-1", rumble("a", "game", 3, 0.5, 80*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Submit("pad-1", rumble("b", "alert", 9, 0.8, 50*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	w := Effect{
		ID: "w", Source: "ui", Priority: 5, Kind: KindWaveform,
		Samples:    []float64{0.2, 0.6, 0.9, 0.6, 0.2},
		SampleRate: 1000,
		Envelope: []EnvelopePoint{
			{Offset: 0, Gain: 0},
			{Offset: 2 * time.Millisecond, Gain: 1},
			{Offset: 5 * time.Millisecond, Gain: 0},
		},
	}
	if _, err := rt.Submit("pad-2", w); err != nil {
		t.Fatal(err)
	}
	if err := rt.Advance(20 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := rt.Revoke("pad-1", "a"); err != nil {
		t.Fatal(err)
	}
	if err := rt.Advance(40 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := rt.DetachDevice("pad-2"); err != nil {
		t.Fatal(err)
	}
	if err := rt.Advance(100 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	return rt, rt.Recording()
}

// 回放一致性：同一事件流重放后，渲染输出逐帧一致。
func TestReplayMatchesOriginal(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Merge = MergeMix
	orig, events := buildRecording(t, cfg)
	if len(events) == 0 {
		t.Fatal("no events recorded")
	}
	replayed, err := Replay(cfg, events)
	if err != nil {
		t.Fatal(err)
	}
	for _, dev := range []DeviceID{"pad-1"} {
		from := time.Duration(0)
		to := 120 * time.Millisecond
		a, err := orig.Render(dev, from, to)
		if err != nil {
			t.Fatal(err)
		}
		b, err := replayed.Render(dev, from, to)
		if err != nil {
			t.Fatal(err)
		}
		if len(a) != len(b) {
			t.Fatalf("frame count %d != %d", len(a), len(b))
		}
		for i := range a {
			if a[i].Time != b[i].Time {
				t.Fatalf("frame %d time %v != %v", i, a[i].Time, b[i].Time)
			}
			for ch := range a[i].Channels {
				if a[i].Channels[ch] != b[i].Channels[ch] {
					t.Fatalf("frame %d ch %d: %v != %v", i, ch, a[i].Channels[ch], b[i].Channels[ch])
				}
			}
		}
	}
	// 快照也应一致。
	sa, err := orig.Snapshot("pad-1")
	if err != nil {
		t.Fatal(err)
	}
	sb, err := replayed.Snapshot("pad-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sa.Active) != len(sb.Active) {
		t.Fatalf("snapshot active count %d != %d", len(sa.Active), len(sb.Active))
	}
	for i := range sa.Active {
		if sa.Active[i].Effect.ID != sb.Active[i].Effect.ID ||
			sa.Active[i].Start != sb.Active[i].Start ||
			sa.Active[i].End != sb.Active[i].End {
			t.Fatalf("snapshot %d differs: %+v vs %+v", i, sa.Active[i], sb.Active[i])
		}
	}
}

// 可复现性：固定输入与配置下，两次独立运行渲染结果逐帧一致。
func TestReproducibleAcrossRuns(t *testing.T) {
	cfg := DefaultConfig()
	_, events := buildRecording(t, cfg)
	r1, err := Replay(cfg, events)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Replay(cfg, events)
	if err != nil {
		t.Fatal(err)
	}
	f1, err := r1.Render("pad-1", 0, 120*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	f2, err := r2.Render("pad-1", 0, 120*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(f1) != len(f2) {
		t.Fatalf("frame count %d != %d", len(f1), len(f2))
	}
	for i := range f1 {
		for ch := range f1[i].Channels {
			if f1[i].Channels[ch] != f2[i].Channels[ch] {
				t.Fatalf("run divergence at frame %d ch %d", i, ch)
			}
		}
	}
}

// 波形包络：增益按折点插值，起止为 0，中间为峰值。
func TestWaveformEnvelope(t *testing.T) {
	rt, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	caps := testCaps()
	caps.Channels = 1
	if err := rt.AttachDevice("pad", caps); err != nil {
		t.Fatal(err)
	}
	w := Effect{
		ID: "w", Source: "ui", Priority: 1, Kind: KindWaveform,
		Samples:    []float64{1, 1, 1, 1, 1},
		SampleRate: 1000, // 5ms
		Envelope: []EnvelopePoint{
			{Offset: 0, Gain: 0},
			{Offset: 2500 * time.Microsecond, Gain: 1},
			{Offset: 5 * time.Millisecond, Gain: 0},
		},
	}
	start, err := rt.Submit("pad", w)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Advance(start + 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	frames, err := rt.Render("pad", start, start+5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 5 {
		t.Fatalf("want 5 frames, got %d", len(frames))
	}
	// 包络: t=0→0, t=1ms→0.4, t=2ms→0.8, t=3ms→0.8, t=4ms→0.4
	want := []float64{0, 0.4, 0.8, 0.8, 0.4}
	for i, f := range frames {
		got := f.Channels[0]
		if d := got - want[i]; d > 1e-9 || d < -1e-9 {
			t.Fatalf("frame %d (%v): got %v, want %v", i, f.Time, got, want[i])
		}
	}
}

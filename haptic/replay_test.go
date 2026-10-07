package haptic

import (
	"fmt"
	"testing"
	"time"
)

// runScript 执行一段固定脚本：多设备、多来源、撤销、热插拔。
func runScript(record bool) (*Runtime, []frameOutput, Recording) {
	rt := NewRuntime(DefaultConfig())
	if record {
		rt.StartRecording()
	}
	var out []frameOutput
	caps := testCaps()
	_ = rt.AddDevice("dev-A", caps)
	caps2 := caps
	caps2.Channels = 1
	caps2.MaxAmplitude = 0.6
	_ = rt.AddDevice("dev-B", caps2)

	mk := func(dev DeviceID, src string, prio int, amp float64, dur time.Duration) Command {
		return Command{Device: dev, Source: SourceID(src), Kind: KindContinuous,
			Priority: prio, Duration: dur, Amplitude: amp, Frequency: 150}
	}
	_, _ = rt.Submit(mk("dev-A", "game", 10, 0.8, 300*time.Millisecond))
	_, _ = rt.Submit(mk("dev-A", "ui", 3, 0.5, 300*time.Millisecond))
	idB, _ := rt.Submit(mk("dev-B", "game", 10, 0.9, 300*time.Millisecond))
	out = append(out, rt.Render(8)...)
	_, _ = rt.Submit(Command{Device: "dev-A", Source: "hazard", Kind: KindPulse,
		Priority: 20, Amplitude: 1.0, Frequency: 200})
	out = append(out, rt.Render(4)...)
	_ = rt.Revoke(idB)
	_ = rt.RemoveDevice("dev-B")
	out = append(out, rt.Render(6)...)
	_ = rt.AddDevice("dev-B", caps2)
	_, _ = rt.Submit(mk("dev-B", "ui", 5, 0.4, 100*time.Millisecond))
	out = append(out, rt.Render(10)...)
	var rec Recording
	if record {
		rec = rt.StopRecording()
	}
	return rt, out, rec
}

func outputsEqual(a, b []frameOutput) error {
	if len(a) != len(b) {
		return fmt.Errorf("frame count %d != %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Device != b[i].Device || a[i].Frame != b[i].Frame {
			return fmt.Errorf("frame %d: meta mismatch %v/%v vs %v/%v",
				i, a[i].Device, a[i].Frame, b[i].Device, b[i].Frame)
		}
		if len(a[i].Channels) != len(b[i].Channels) {
			return fmt.Errorf("frame %d: channel count mismatch", i)
		}
		for ch := range a[i].Channels {
			if a[i].Channels[ch] != b[i].Channels[ch] {
				return fmt.Errorf("frame %d (%s) ch %d: %v != %v",
					i, a[i].Device, ch, a[i].Channels[ch], b[i].Channels[ch])
			}
		}
	}
	return nil
}

// 固定输入 + 相同配置 ⇒ 逐帧一致（可复现）。
func TestReproducible(t *testing.T) {
	_, out1, _ := runScript(false)
	_, out2, _ := runScript(false)
	if err := outputsEqual(out1, out2); err != nil {
		t.Fatal(err)
	}
}

// 录制后回放，输出与原运行逐帧一致。
func TestRecordAndReplay(t *testing.T) {
	_, live, rec := runScript(true)
	if len(rec.events) == 0 || len(rec.renders) == 0 {
		t.Fatal("recording is empty")
	}
	replayed := Replay(DefaultConfig(), rec)
	if err := outputsEqual(live, replayed); err != nil {
		t.Fatalf("replay mismatch: %v", err)
	}
	// 回放本身也是可复现的。
	replayed2 := Replay(DefaultConfig(), rec)
	if err := outputsEqual(replayed, replayed2); err != nil {
		t.Fatalf("replay not deterministic: %v", err)
	}
}

// 多设备输出顺序与 map 遍历顺序无关：按 (Frame, DeviceID) 确定排序。
func TestDeviceOrderDeterministic(t *testing.T) {
	rt := NewRuntime(DefaultConfig())
	// 以会让 map 遍历顺序变化的方式插入多台设备。
	for _, id := range []string{"zeta", "alpha", "mid", "beta", "omega"} {
		if err := rt.AddDevice(DeviceID(id), testCaps()); err != nil {
			t.Fatal(err)
		}
	}
	out := rt.Render(3)
	for i := 1; i < len(out); i++ {
		prev, cur := out[i-1], out[i]
		if cur.Frame < prev.Frame {
			t.Fatal("frames must be non-decreasing")
		}
		if cur.Frame == prev.Frame && cur.Device < prev.Device {
			t.Fatalf("devices out of order at frame %d: %s after %s",
				cur.Frame, cur.Device, prev.Device)
		}
	}
}

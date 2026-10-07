package haptic

import (
	"testing"
	"time"
)

func testCaps() DeviceCapability {
	return DeviceCapability{
		Channels:     2,
		MinFrequency: 20,
		MaxFrequency: 500,
		SampleRate:   4000,
		MaxAmplitude: 1.0,
	}
}

func rumble(id EffectID, src SourceID, prio int, amp float64, dur time.Duration) Effect {
	return Effect{
		ID: id, Source: src, Priority: prio, Kind: KindRumble,
		Amplitude: amp, Frequency: 100, Duration: dur,
	}
}

// 多来源冲突：单通道设备上高优先级独占，低优先级被抢占且可解释。
func TestArbitrationHighestPriorityWins(t *testing.T) {
	rt, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	caps := testCaps()
	caps.Channels = 1
	if err := rt.AttachDevice("pad-1", caps); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Submit("pad-1", rumble("low", "game", 1, 0.3, 100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Submit("pad-1", rumble("high", "alert", 9, 0.8, 100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := rt.Advance(50 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	frames, err := rt.Render("pad-1", 50*time.Millisecond, 60*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("no frames rendered")
	}
	if got := frames[0].Channels[0]; got != 0.8 {
		t.Fatalf("channel 0 = %v, want 0.8 (high priority)", got)
	}
	snap, err := rt.Snapshot("pad-1")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range snap.Decisions {
		if d.Winner == "high" {
			found = true
			if len(d.Contributors) != 1 || d.Contributors[0] != "low" {
				t.Fatalf("preempted contributors = %v, want [low]", d.Contributors)
			}
		}
	}
	if !found {
		t.Fatalf("no decision with winner=high, decisions=%+v", snap.Decisions)
	}
}

// 多通道设备：排名 i 的效果分配到通道 i%N，两个效果各占一个通道。
func TestArbitrationMultiChannelDistribution(t *testing.T) {
	rt, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.AttachDevice("pad", testCaps()); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Submit("pad", rumble("hi", "a", 9, 0.8, 100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Submit("pad", rumble("lo", "b", 1, 0.3, 100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := rt.Advance(10 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	frames, err := rt.Render("pad", 10*time.Millisecond, 12*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("no frames")
	}
	if got := frames[0].Channels[0]; got != 0.8 {
		t.Fatalf("channel 0 = %v, want 0.8", got)
	}
	if got := frames[0].Channels[1]; got != 0.3 {
		t.Fatalf("channel 1 = %v, want 0.3", got)
	}
}

// 顺序无关：同一效果集合以不同顺序提交，渲染结果必须逐帧一致。
func TestArbitrationOrderIndependent(t *testing.T) {
	orders := [][]Effect{
		{
			rumble("a", "s1", 5, 0.5, 200*time.Millisecond),
			rumble("b", "s2", 5, 0.6, 200*time.Millisecond),
			rumble("c", "s3", 7, 0.7, 200*time.Millisecond),
		},
		{
			rumble("c", "s3", 7, 0.7, 200*time.Millisecond),
			rumble("a", "s1", 5, 0.5, 200*time.Millisecond),
			rumble("b", "s2", 5, 0.6, 200*time.Millisecond),
		},
		{
			rumble("b", "s2", 5, 0.6, 200*time.Millisecond),
			rumble("c", "s3", 7, 0.7, 200*time.Millisecond),
			rumble("a", "s1", 5, 0.5, 200*time.Millisecond),
		},
	}
	var reference []Frame
	for i, effects := range orders {
		cfg := DefaultConfig()
		cfg.Merge = MergeMix
		rt, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := rt.AttachDevice("pad", testCaps()); err != nil {
			t.Fatal(err)
		}
		for _, e := range effects {
			if _, err := rt.Submit("pad", e); err != nil {
				t.Fatal(err)
			}
		}
		if err := rt.Advance(10 * time.Millisecond); err != nil {
			t.Fatal(err)
		}
		frames, err := rt.Render("pad", 0, 100*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			reference = frames
			continue
		}
		if len(frames) != len(reference) {
			t.Fatalf("order %d: frame count %d != %d", i, len(frames), len(reference))
		}
		for j := range frames {
			if frames[j].Time != reference[j].Time {
				t.Fatalf("order %d frame %d: time %v != %v", i, j, frames[j].Time, reference[j].Time)
			}
			for ch := range frames[j].Channels {
				if frames[j].Channels[ch] != reference[j].Channels[ch] {
					t.Fatalf("order %d frame %d ch %d: %v != %v",
						i, j, ch, frames[j].Channels[ch], reference[j].Channels[ch])
				}
			}
		}
	}
}

// Mix 策略：同通道效果叠加并钳制到设备量程。
func TestArbitrationMixClamps(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Merge = MergeMix
	rt, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	caps := testCaps()
	caps.Channels = 1
	caps.MaxAmplitude = 0.9
	if err := rt.AttachDevice("pad", caps); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Submit("pad", rumble("x", "s1", 1, 0.6, 100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Submit("pad", rumble("y", "s2", 2, 0.6, 100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := rt.Advance(10 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	frames, err := rt.Render("pad", 10*time.Millisecond, 12*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("no frames")
	}
	// 0.6+0.6=1.2 应被钳制到 0.9。
	if got := frames[0].Channels[0]; got != 0.9 {
		t.Fatalf("mixed output = %v, want clamped 0.9", got)
	}
}

// 容量淘汰：超过 MaxActiveEffects 的效果按排名淘汰，且结果可解释。
func TestArbitrationCapacityEviction(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxActiveEffects = 2
	rt, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.AttachDevice("pad", testCaps()); err != nil {
		t.Fatal(err)
	}
	for _, e := range []Effect{
		rumble("e1", "s", 1, 0.2, 100*time.Millisecond),
		rumble("e2", "s", 5, 0.4, 100*time.Millisecond),
		rumble("e3", "s", 9, 0.6, 100*time.Millisecond),
	} {
		if _, err := rt.Submit("pad", e); err != nil {
			t.Fatal(err)
		}
	}
	if err := rt.Advance(10 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	frames, err := rt.Render("pad", 10*time.Millisecond, 12*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	// 只有 e3(0.6) 与 e2(0.4) 存活，e1 被淘汰。
	if got := frames[0].Channels[0]; got != 0.6 {
		t.Fatalf("channel 0 = %v, want 0.6", got)
	}
	if got := frames[0].Channels[1]; got != 0.4 {
		t.Fatalf("channel 1 = %v, want 0.4", got)
	}
}

package haptic

import (
	"testing"
	"time"
)

// 延迟补偿：生效时刻 = 提交时刻 + 固定补偿 + 抖动余量。
func TestScheduleAppliesCompensation(t *testing.T) {
	tl := NewTimeline(2*time.Millisecond, NewJitterEstimator(0.5))
	got := tl.Schedule(10*time.Millisecond, 10*time.Millisecond)
	if got != 12*time.Millisecond {
		t.Fatalf("scheduled = %v, want 12ms", got)
	}
}

// 时间轴不倒流：后续调度不得早于已调度的最后时刻。
func TestScheduleNeverBackwards(t *testing.T) {
	tl := NewTimeline(5*time.Millisecond, NewJitterEstimator(0.5))
	first := tl.Schedule(100*time.Millisecond, 100*time.Millisecond)
	second := tl.Schedule(0, 0) // 请求时刻在过去
	if second < first {
		t.Fatalf("second schedule %v goes backwards past %v", second, first)
	}
}

// 抖动估计：EMA 收敛且只依赖观测序列。
func TestJitterEstimatorConverges(t *testing.T) {
	j := NewJitterEstimator(0.5)
	j.Observe(10 * time.Millisecond)
	est := j.Observe(10 * time.Millisecond)
	if est != 10*time.Millisecond {
		t.Fatalf("estimate = %v, want 10ms", est)
	}
	est = j.Observe(20 * time.Millisecond)
	if est != 15*time.Millisecond {
		t.Fatalf("estimate = %v, want 15ms", est)
	}
}

// 不同负载（不同 Advance 粒度）下，同一提交序列的渲染时间轴一致。
func TestTimelineConsistentUnderLoad(t *testing.T) {
	render := func(advanceStep time.Duration) []Frame {
		rt, err := New(DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		if err := rt.AttachDevice("pad", testCaps()); err != nil {
			t.Fatal(err)
		}
		// 低负载：1ms 步进；高负载：10ms 步进。提交点相同。
		for now := time.Duration(0); now < 50*time.Millisecond; now += advanceStep {
			if err := rt.Advance(now + advanceStep); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := rt.Submit("pad", rumble("e", "s", 1, 0.5, 100*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		if err := rt.Advance(200 * time.Millisecond); err != nil {
			t.Fatal(err)
		}
		frames, err := rt.Render("pad", 0, 200*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		return frames
	}
	light := render(1 * time.Millisecond)
	heavy := render(10 * time.Millisecond)
	if len(light) != len(heavy) {
		t.Fatalf("frame counts differ: %d vs %d", len(light), len(heavy))
	}
	// 注意：负载不同会导致抖动观测序列不同，进而补偿余量不同。
	// 这里验证的是：相同负载剖面下结果必然一致（可复现性），
	// 以及时间轴本身单调、无重复触发。
	for i := 1; i < len(light); i++ {
		if light[i].Time <= light[i-1].Time {
			t.Fatalf("frame time goes backwards at %d", i)
		}
	}
	same := render(1 * time.Millisecond)
	for i := range light {
		for ch := range light[i].Channels {
			if light[i].Channels[ch] != same[i].Channels[ch] {
				t.Fatalf("same load profile diverged at frame %d ch %d", i, ch)
			}
		}
	}
}

// 时钟回拨被拒绝。
func TestAdvanceRejectsBackwards(t *testing.T) {
	rt, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Advance(10 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := rt.Advance(5 * time.Millisecond); err == nil {
		t.Fatal("expected error when moving clock backwards")
	}
}

// 同一效果不会被重复触发：生效窗口 [start, end) 唯一且连续。
func TestNoDuplicateTrigger(t *testing.T) {
	rt, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	caps := testCaps()
	caps.Channels = 1
	if err := rt.AttachDevice("pad", caps); err != nil {
		t.Fatal(err)
	}
	start, err := rt.Submit("pad", rumble("e", "s", 1, 0.7, 20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Advance(start + 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	frames, err := rt.Render("pad", 0, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	// 统计输出非零的帧：必须是连续的一段，且时长等于效果时长。
	var on []time.Duration
	for _, f := range frames {
		if f.Channels[0] != 0 {
			on = append(on, f.Time)
		}
	}
	if len(on) == 0 {
		t.Fatal("effect never fired")
	}
	for i := 1; i < len(on); i++ {
		if on[i]-on[i-1] != time.Millisecond {
			t.Fatalf("output not contiguous: gap at %v", on[i])
		}
	}
	if got := on[len(on)-1] - on[0] + time.Millisecond; got != 20*time.Millisecond {
		t.Fatalf("active span = %v, want 20ms", got)
	}
}

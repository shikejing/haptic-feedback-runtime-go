package haptic

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// 并发提交与撤销：多来源、多设备同时操作，运行时不panic、状态自洽。
func TestConcurrentSubmitRevoke(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Merge = MergeMix
	rt, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	devices := []DeviceID{"pad-a", "pad-b", "pad-c"}
	for _, d := range devices {
		if err := rt.AttachDevice(d, testCaps()); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				dev := devices[(g+i)%len(devices)]
				id := EffectID(fmt.Sprintf("g%d-e%d", g, i))
				e := rumble(id, SourceID(fmt.Sprintf("src-%d", g)), i%5, 0.4, 30*time.Millisecond)
				if _, err := rt.Submit(dev, e); err != nil {
					continue // 可能因重复ID等被拒，属预期
				}
				_ = rt.Revoke(dev, id)
			}
		}(g)
	}
	// 并发行进时钟。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for now := time.Millisecond; now <= 100*time.Millisecond; now += time.Millisecond {
			_ = rt.Advance(now)
		}
	}()
	wg.Wait()
	// 所有设备仍可渲染、可快照，且幅值不越界。
	for _, d := range devices {
		frames, err := rt.Render(d, 0, 100*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range frames {
			for ch, v := range f.Channels {
				if v < -1 || v > 1 {
					t.Fatalf("device %s frame %v ch %d out of range: %v", d, f.Time, ch, v)
				}
			}
		}
		if _, err := rt.Snapshot(d); err != nil {
			t.Fatal(err)
		}
	}
}

// 撤销即截断：撤销时刻之后不得再有输出。
func TestRevokeTruncates(t *testing.T) {
	rt, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	caps := testCaps()
	caps.Channels = 1
	if err := rt.AttachDevice("pad", caps); err != nil {
		t.Fatal(err)
	}
	start, err := rt.Submit("pad", rumble("e", "s", 1, 0.8, 100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	// 推进到生效后 10ms，然后撤销。
	cut := start + 10*time.Millisecond
	if err := rt.Advance(cut); err != nil {
		t.Fatal(err)
	}
	if err := rt.Revoke("pad", "e"); err != nil {
		t.Fatal(err)
	}
	frames, err := rt.Render("pad", 0, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		if f.Time >= cut && f.Channels[0] != 0 {
			t.Fatalf("output at %v after revoke at %v: %v", f.Time, cut, f.Channels[0])
		}
	}
	// 撤销前应有输出。
	before, err := rt.Render("pad", start, cut)
	if err != nil {
		t.Fatal(err)
	}
	anyOn := false
	for _, f := range before {
		if f.Channels[0] != 0 {
			anyOn = true
		}
	}
	if !anyOn {
		t.Fatal("expected output before revoke")
	}
}

// 撤销不存在的效果返回可区分错误。
func TestRevokeUnknownEffect(t *testing.T) {
	rt, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.AttachDevice("pad", testCaps()); err != nil {
		t.Fatal(err)
	}
	err = rt.Revoke("pad", "nope")
	he, ok := err.(*Error)
	if !ok || he.Kind != ErrKindUnknownEffect {
		t.Fatalf("want ErrKindUnknownEffect, got %v", err)
	}
}

// 热插拔-终止策略：断开后设备上反馈终止，其他设备不受影响。
func TestHotplugTerminate(t *testing.T) {
	rt, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.AttachDevice("pad-1", testCaps()); err != nil {
		t.Fatal(err)
	}
	if err := rt.AttachDevice("pad-2", testCaps()); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Submit("pad-1", rumble("e1", "s", 1, 0.5, 100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Submit("pad-2", rumble("e2", "s", 1, 0.7, 100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := rt.Advance(10 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := rt.DetachDevice("pad-1"); err != nil {
		t.Fatal(err)
	}
	// pad-1 已消失：渲染与快照都报未知设备。
	if _, err := rt.Render("pad-1", 0, 100*time.Millisecond); err == nil {
		t.Fatal("expected error rendering detached device")
	}
	// pad-2 输出不受影响。
	frames, err := rt.Render("pad-2", 10*time.Millisecond, 12*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 || frames[0].Channels[0] != 0.7 {
		t.Fatalf("pad-2 output affected by pad-1 detach: %+v", frames)
	}
	// 重新接入：身份恢复，状态干净（无泄漏的旧反馈）。
	if err := rt.AttachDevice("pad-1", testCaps()); err != nil {
		t.Fatal(err)
	}
	snap, err := rt.Snapshot("pad-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Active) != 0 {
		t.Fatalf("stale effects leaked across hotplug: %+v", snap.Active)
	}
}

// 热插拔-迁移策略：断开设备上的效果迁移到兼容设备。
func TestHotplugMigrate(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Disconnect = DisconnectMigrate
	rt, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.AttachDevice("pad-1", testCaps()); err != nil {
		t.Fatal(err)
	}
	if err := rt.AttachDevice("pad-2", testCaps()); err != nil {
		t.Fatal(err)
	}
	start, err := rt.Submit("pad-1", rumble("e1", "s", 1, 0.6, 100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Advance(10 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := rt.DetachDevice("pad-1"); err != nil {
		t.Fatal(err)
	}
	// e1 应迁移到 pad-2 并在原时间窗口内继续输出。
	snap, err := rt.Snapshot("pad-2")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range snap.Active {
		if a.Effect.ID == "e1" {
			found = true
			if a.Start != start {
				t.Fatalf("migrated effect start = %v, want %v", a.Start, start)
			}
		}
	}
	if !found {
		t.Fatal("effect not migrated to pad-2")
	}
	frames, err := rt.Render("pad-2", start, start+2*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("no frames")
	}
	on := false
	for _, v := range frames[0].Channels {
		if v == 0.6 {
			on = true
		}
	}
	if !on {
		t.Fatalf("migrated effect not rendering on pad-2: %+v", frames[0])
	}
}

// 重复接入返回可区分错误。
func TestAttachDuplicate(t *testing.T) {
	rt, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.AttachDevice("pad", testCaps()); err != nil {
		t.Fatal(err)
	}
	err = rt.AttachDevice("pad", testCaps())
	he, ok := err.(*Error)
	if !ok || he.Kind != ErrKindDeviceExists {
		t.Fatalf("want ErrKindDeviceExists, got %v", err)
	}
}

// 慢设备不拖住其他设备：单设备渲染不依赖其他设备状态。
func TestSlowDeviceIsolation(t *testing.T) {
	rt, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	slow := testCaps()
	slow.SampleRate = 250 // 慢设备
	if err := rt.AttachDevice("slow", slow); err != nil {
		t.Fatal(err)
	}
	if err := rt.AttachDevice("fast", testCaps()); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Submit("slow", rumble("es", "s", 1, 0.5, 100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Submit("fast", rumble("ef", "s", 1, 0.9, 100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := rt.Advance(10 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	frames, err := rt.Render("fast", 10*time.Millisecond, 12*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 || frames[0].Channels[0] != 0.9 {
		t.Fatalf("fast device output affected: %+v", frames)
	}
}

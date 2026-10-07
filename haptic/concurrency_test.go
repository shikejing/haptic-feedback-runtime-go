package haptic

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// 撤销到达后，反馈从下一渲染帧起被截断，且不再出现在生效集合中。
func TestRevokeTruncates(t *testing.T) {
	rt := newTestRuntime(t, DefaultConfig())
	id, err := rt.Submit(Command{Device: "dev-A", Source: "s", Kind: KindContinuous,
		Priority: 1, Duration: 500 * time.Millisecond, Amplitude: 0.8, Frequency: 100})
	if err != nil {
		t.Fatal(err)
	}
	out1 := rt.Render(5)
	for _, fo := range out1 {
		if fo.Channels[0] != 0.8 {
			t.Fatalf("frame %d: want 0.8 before revoke, got %v", fo.Frame, fo.Channels)
		}
	}
	if err := rt.Revoke(id); err != nil {
		t.Fatal(err)
	}
	act, err := rt.ActiveAt("dev-A")
	if err != nil {
		t.Fatal(err)
	}
	if len(act) != 0 {
		t.Fatalf("revoked command still active: %+v", act)
	}
	out2 := rt.Render(5)
	for _, fo := range out2 {
		if fo.Channels[0] != 0 {
			t.Fatalf("frame %d: revoked feedback still outputting %v", fo.Frame, fo.Channels)
		}
	}
	// 重复撤销 / 撤销未知指令：可区分错误。
	if err := rt.Revoke(id); !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("want ErrUnknownCommand, got %v", err)
	}
	if err := rt.Revoke(9999); !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("want ErrUnknownCommand, got %v", err)
	}
}

// 多设备、多来源并发提交与撤销（-race 下验证无数据竞争），
// 且每台设备的输出互不影响。
func TestConcurrentSubmitRevoke(t *testing.T) {
	rt := NewRuntime(DefaultConfig())
	devs := []DeviceID{"dev-A", "dev-B", "dev-C"}
	for _, d := range devs {
		if err := rt.AddDevice(d, testCaps()); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				dev := devs[(g+i)%len(devs)]
				id, err := rt.Submit(Command{
					Device: dev, Source: SourceID(fmt.Sprintf("src-%d", g)),
					Kind: KindContinuous, Priority: g % 4,
					Duration: 50 * time.Millisecond, Amplitude: 0.5, Frequency: 100,
				})
				if err != nil {
					continue // 队列满等背压错误是允许的
				}
				if i%3 == 0 {
					_ = rt.Revoke(id)
				}
				if i%7 == 0 {
					rt.Render(1)
				}
			}
		}(g)
	}
	wg.Wait()
	// 收尾渲染：所有设备都必须有输出帧，且通道数正确、值域合法。
	out := rt.Render(200)
	seen := map[DeviceID]int{}
	for _, fo := range out {
		seen[fo.Device]++
		if len(fo.Channels) != testCaps().Channels {
			t.Fatalf("device %s frame %d: bad channel count", fo.Device, fo.Frame)
		}
		for ch, v := range fo.Channels {
			if v < 0 || v > 1 {
				t.Fatalf("device %s frame %d ch %d: amplitude %v out of bounds",
					fo.Device, fo.Frame, ch, v)
			}
		}
	}
	for _, d := range devs {
		if seen[d] != 200 {
			t.Fatalf("device %s: want 200 frames, got %d", d, seen[d])
		}
	}
	// 所有指令在 200 帧后必然结束，生效集合必须为空（状态自洽）。
	for _, d := range devs {
		act, err := rt.ActiveAt(d)
		if err != nil {
			t.Fatal(err)
		}
		if len(act) != 0 {
			t.Fatalf("device %s: %d feedbacks leaked", d, len(act))
		}
	}
}

// 撤销只影响目标指令，同设备其他反馈不受影响。
func TestRevokeIsSelective(t *testing.T) {
	rt := newTestRuntime(t, DefaultConfig())
	victim, err := rt.Submit(Command{Device: "dev-A", Source: "victim", Kind: KindContinuous,
		Priority: 5, Duration: 500 * time.Millisecond, Amplitude: 0.5, Frequency: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, err = rt.Submit(Command{Device: "dev-A", Source: "survivor", Kind: KindContinuous,
		Priority: 9, Duration: 500 * time.Millisecond, Amplitude: 0.7, Frequency: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Revoke(victim); err != nil {
		t.Fatal(err)
	}
	out := rt.Render(1)
	if out[0].Channels[0] != 0.7 {
		t.Fatalf("survivor should keep outputting 0.7, got %v", out[0].Channels)
	}
	act, _ := rt.ActiveAt("dev-A")
	if len(act) != 1 || act[0].Source != "survivor" {
		t.Fatalf("want only survivor active, got %+v", act)
	}
}

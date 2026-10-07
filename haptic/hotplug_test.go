package haptic

import (
	"errors"
	"testing"
	"time"
)

// 断开策略 Terminate：设备消失即终止其全部反馈，其他设备不受影响。
func TestHotplugTerminate(t *testing.T) {
	cfg := DefaultConfig()
	cfg.OnDisconnect = DisconnectTerminate
	rt := NewRuntime(cfg)
	if err := rt.AddDevice("dev-A", testCaps()); err != nil {
		t.Fatal(err)
	}
	if err := rt.AddDevice("dev-B", testCaps()); err != nil {
		t.Fatal(err)
	}
	for _, d := range []DeviceID{"dev-A", "dev-B"} {
		if _, err := rt.Submit(Command{Device: d, Source: "s", Kind: KindContinuous,
			Priority: 1, Duration: 500 * time.Millisecond, Amplitude: 0.6, Frequency: 100}); err != nil {
			t.Fatal(err)
		}
	}
	rt.Render(2)
	if err := rt.RemoveDevice("dev-A"); err != nil {
		t.Fatal(err)
	}
	// dev-A 已消失：查询与提交都报可区分错误。
	if _, err := rt.ActiveAt("dev-A"); !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("want ErrUnknownDevice, got %v", err)
	}
	if _, err := rt.Submit(Command{Device: "dev-A", Source: "s", Kind: KindContinuous,
		Duration: time.Millisecond, Amplitude: 0.5}); !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("want ErrUnknownDevice, got %v", err)
	}
	// dev-B 输出不受影响。
	out := rt.Render(3)
	for _, fo := range out {
		if fo.Device == "dev-B" && fo.Channels[0] != 0.6 {
			t.Fatalf("dev-B frame %d: want 0.6, got %v", fo.Frame, fo.Channels)
		}
		if fo.Device == "dev-A" {
			t.Fatal("disconnected device must not render")
		}
	}
	// 同身份重连：旧反馈已终止，不得复活（状态不泄漏）。
	if err := rt.AddDevice("dev-A", testCaps()); err != nil {
		t.Fatal(err)
	}
	act, err := rt.ActiveAt("dev-A")
	if err != nil {
		t.Fatal(err)
	}
	if len(act) != 0 {
		t.Fatalf("terminated feedback leaked across reconnect: %+v", act)
	}
	out = rt.Render(1)
	for _, fo := range out {
		if fo.Device == "dev-A" && fo.Channels[0] != 0 {
			t.Fatalf("leaked output on reconnected device: %v", fo.Channels)
		}
	}
}

// 断开策略 Park：挂起的反馈在同身份重连后按剩余时长恢复。
func TestHotplugParkAndResume(t *testing.T) {
	cfg := DefaultConfig()
	cfg.OnDisconnect = DisconnectPark
	rt := NewRuntime(cfg)
	if err := rt.AddDevice("dev-A", testCaps()); err != nil {
		t.Fatal(err)
	}
	// 200ms = 40 帧。
	if _, err := rt.Submit(Command{Device: "dev-A", Source: "s", Kind: KindContinuous,
		Priority: 1, Duration: 200 * time.Millisecond, Amplitude: 0.6, Frequency: 100}); err != nil {
		t.Fatal(err)
	}
	rt.Render(10) // 消耗 10 帧，剩余 30 帧
	if err := rt.RemoveDevice("dev-A"); err != nil {
		t.Fatal(err)
	}
	rt.Render(5) // 断开期间渲染 5 帧，不应有 dev-A 输出
	if err := rt.AddDevice("dev-A", testCaps()); err != nil {
		t.Fatal(err)
	}
	act, err := rt.ActiveAt("dev-A")
	if err != nil {
		t.Fatal(err)
	}
	if len(act) != 1 {
		t.Fatalf("parked feedback not restored: %+v", act)
	}
	// 恢复后剩余 30 帧：渲染 30 帧有输出，第 31 帧起无输出。
	out := rt.Render(35)
	on := 0
	for _, fo := range out {
		if fo.Device == "dev-A" && fo.Channels[0] > 0 {
			on++
		}
	}
	if on != 30 {
		t.Fatalf("resumed feedback should last 30 frames, got %d", on)
	}
}

// 重复断开/未知设备断开返回可区分错误。
func TestRemoveUnknownDevice(t *testing.T) {
	rt := NewRuntime(DefaultConfig())
	if err := rt.RemoveDevice("ghost"); !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("want ErrUnknownDevice, got %v", err)
	}
}

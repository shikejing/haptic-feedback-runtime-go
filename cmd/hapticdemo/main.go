// hapticdemo 在本地进程内跑通整条触觉反馈链路：
// 多来源提交 → 仲裁 → 能力降级 → 延迟补偿 → 渲染到内存模拟通道。
package main

import (
	"fmt"
	"time"

	haptic "github.com/shikejing/haptic-feedback-runtime-go/haptic"
)

func main() {
	cfg := haptic.DefaultConfig()
	rt := haptic.NewRuntime(cfg)
	rt.StartRecording()

	// 接入两只能力不同的手柄。
	must(rt.AddDevice("left", haptic.Capabilities{
		Channels: 2, MinFrequency: 50, MaxFrequency: 500, SampleRate: 1000, MaxAmplitude: 1.0,
	}))
	must(rt.AddDevice("right", haptic.Capabilities{
		Channels: 1, MinFrequency: 80, MaxFrequency: 250, SampleRate: 500, MaxAmplitude: 0.7,
	}))

	// 多来源并发指令：游戏高优先级持续震动 + UI 低优先级脉冲 + 包络波形。
	mustSubmit(rt, haptic.Command{Device: "left", Source: "game", Kind: haptic.KindContinuous,
		Priority: 10, Duration: 100 * time.Millisecond, Amplitude: 0.8, Frequency: 120})
	mustSubmit(rt, haptic.Command{Device: "left", Source: "ui", Kind: haptic.KindPulse,
		Priority: 1, Amplitude: 1.0, Frequency: 100})
	mustSubmit(rt, haptic.Command{Device: "right", Source: "game", Kind: haptic.KindEnvelope,
		Priority: 10, Duration: 50 * time.Millisecond, Amplitude: 0.9, Frequency: 400, // 超出 right 的频率上限，会被钳制
		Envelope: []haptic.EnvelopePoint{
			{Time: 0, Amplitude: 0},
			{Time: 25 * time.Millisecond, Amplitude: 1},
			{Time: 50 * time.Millisecond, Amplitude: 0},
		}})

	// 渲染 20 帧（100ms）到内存通道并打印。
	for _, fo := range rt.Render(20) {
		fmt.Printf("frame=%02d dev=%-5s channels=%.2v degraded=%d\n",
			fo.Frame, fo.Device, fo.Channels, len(fo.Degraded))
	}

	// 查询生效反馈（可观测性）。
	act, _ := rt.ActiveAt("left")
	for _, a := range act {
		fmt.Printf("active: src=%s kind=%s prio=%d suppressed=%v\n",
			a.Source, a.Kind, a.Priority, a.Suppressed)
	}

	// 录制并回放，验证逐帧一致。
	rec := rt.StopRecording()
	_ = rec // 可用 haptic.Replay(cfg, rec) 原样回放
	fmt.Println("demo done")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func mustSubmit(rt *haptic.Runtime, c haptic.Command) {
	if _, err := rt.Submit(c); err != nil {
		panic(err)
	}
}

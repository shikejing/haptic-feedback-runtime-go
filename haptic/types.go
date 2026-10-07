// Package haptic 提供面向 VR 手柄的触觉反馈调度与渲染运行时。
// 整条链路在本地进程内运行，设备输出通过内存模拟通道提供。
package haptic

import (
	"fmt"
	"time"
)

// DeviceID 唯一标识一个手柄设备。
type DeviceID string

// SourceID 标识反馈指令的来源（例如某个游戏系统、UI 模块）。
type SourceID string

// CommandID 唯一标识一条反馈指令，用于撤销与追踪。
type CommandID uint64

// Frame 是渲染时间轴上的一帧，使用逻辑帧号而非墙钟时间，保证可复现。
type Frame uint64

// Kind 反馈指令的类型。
type Kind int

const (
	// KindContinuous 持续震动：在给定时长内以恒定强度输出。
	KindContinuous Kind = iota
	// KindPulse 脉冲：极短促的一次性震动。
	KindPulse
	// KindEnvelope 带包络的波形：振幅随包络曲线随时间变化。
	KindEnvelope
)

func (k Kind) String() string {
	switch k {
	case KindContinuous:
		return "continuous"
	case KindPulse:
		return "pulse"
	case KindEnvelope:
		return "envelope"
	default:
		return "unknown"
	}
}

// EnvelopePoint 包络曲线上的一个控制点，Time 为相对起点的偏移。
type EnvelopePoint struct {
	Time      time.Duration
	Amplitude float64 // [0,1]
}

// Command 是一条反馈指令。所有数值字段在提交时即被校验，
// 非法指令会以带原因的错误被拒绝，而不是产生未定义行为。
type Command struct {
	ID       CommandID
	Device   DeviceID
	Source   SourceID
	Kind     Kind
	Priority int // 数值越大优先级越高，仲裁时先比较它

	// Start 为请求的逻辑起始帧；零值表示尽快（由运行时对齐到时间轴）。
	Start Frame
	// Duration 指令持续时长。Pulse 类型忽略该字段，使用固定的脉冲长度。
	Duration time.Duration

	// Amplitude 目标振幅 [0,1]，渲染前会按设备能力做映射。
	Amplitude float64
	// Frequency 目标频率 Hz，渲染前会按设备能力做映射。
	Frequency float64

	// Envelope 仅 KindEnvelope 使用，控制点按 Time 升序。
	Envelope []EnvelopePoint
}

// Validate 校验指令是否合法，返回带可区分原因的错误。
func (c Command) Validate() error {
	if c.Device == "" {
		return fmt.Errorf("%w: device id is empty", ErrInvalidCommand)
	}
	if c.Source == "" {
		return fmt.Errorf("%w: source id is empty", ErrInvalidCommand)
	}
	if c.Amplitude < 0 || c.Amplitude > 1 {
		return fmt.Errorf("%w: amplitude %.3f out of range [0,1]", ErrInvalidCommand, c.Amplitude)
	}
	if c.Frequency < 0 {
		return fmt.Errorf("%w: frequency %.3f is negative", ErrInvalidCommand, c.Frequency)
	}
	switch c.Kind {
	case KindContinuous, KindEnvelope:
		if c.Duration <= 0 {
			return fmt.Errorf("%w: %s command requires positive duration", ErrInvalidCommand, c.Kind)
		}
	case KindPulse:
		// 脉冲时长由运行时固定，无需校验 Duration。
	default:
		return fmt.Errorf("%w: unknown kind %d", ErrInvalidCommand, int(c.Kind))
	}
	if c.Kind == KindEnvelope {
		if len(c.Envelope) == 0 {
			return fmt.Errorf("%w: envelope command requires at least one control point", ErrInvalidCommand)
		}
		prev := time.Duration(-1)
		for i, p := range c.Envelope {
			if p.Time < 0 {
				return fmt.Errorf("%w: envelope point %d has negative time", ErrInvalidCommand, i)
			}
			if p.Time <= prev {
				return fmt.Errorf("%w: envelope points must be strictly increasing in time", ErrInvalidCommand)
			}
			if p.Amplitude < 0 || p.Amplitude > 1 {
				return fmt.Errorf("%w: envelope point %d amplitude out of range [0,1]", ErrInvalidCommand, i)
			}
			prev = p.Time
		}
	}
	return nil
}

// ActiveFeedback 描述某时刻某设备上一条生效中的反馈，用于可观测查询。
type ActiveFeedback struct {
	CommandID CommandID
	Source    SourceID
	Kind      Kind
	Priority  int
	// StartFrame / EndFrame 为时间轴上的确定区间。
	StartFrame Frame
	EndFrame   Frame
	// Suppressed 为 true 表示该反馈仍在时间轴上存活，
	// 但因仲裁被更高优先级反馈压制，本帧不产生输出。
	Suppressed bool
}

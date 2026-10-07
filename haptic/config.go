package haptic

import "time"

// MergeStrategy 同优先级冲突时的合并策略。
type MergeStrategy int

const (
	// MergeMax 取各指令逐帧振幅的最大值。
	MergeMax MergeStrategy = iota
	// MergeSum 逐帧振幅相加后按设备最大振幅截断。
	MergeSum
)

func (s MergeStrategy) String() string {
	switch s {
	case MergeMax:
		return "max"
	case MergeSum:
		return "sum"
	default:
		return "unknown"
	}
}

// DisconnectPolicy 设备断开时其上进行中反馈的处理策略。
type DisconnectPolicy int

const (
	// DisconnectTerminate 直接终止该设备上所有进行中的反馈。
	DisconnectTerminate DisconnectPolicy = iota
	// DisconnectPark 把反馈挂起，设备以同一身份重新接入后恢复剩余部分。
	DisconnectPark
)

// Config 运行时配置，所有字段都有合理默认值（见 DefaultConfig）。
type Config struct {
	// FrameDuration 一帧的时长，渲染时间轴的基本粒度。
	FrameDuration time.Duration
	// PulseDuration KindPulse 指令的固定脉冲长度。
	PulseDuration time.Duration
	// Merge 同优先级冲突时的合并策略。
	Merge MergeStrategy
	// DeviceLatency 设备固有输出延迟，渲染时通过提前排程补偿。
	DeviceLatency time.Duration
	// MaxSchedulingJitter 允许吸收的最大调度抖动：
	// 提交时刻落在该窗口内的指令会被对齐到同一时间轴帧，
	// 保证不同负载下渲染结果一致。
	MaxSchedulingJitter time.Duration
	// QueueCapacity 每个设备的待处理指令队列容量。
	QueueCapacity int
	// OnDisconnect 设备断开时的反馈处理策略。
	OnDisconnect DisconnectPolicy
}

// DefaultConfig 返回带合理默认值的配置。
func DefaultConfig() Config {
	return Config{
		FrameDuration:       5 * time.Millisecond,  // 200Hz 渲染帧率
		PulseDuration:       20 * time.Millisecond, // 4 帧脉冲
		Merge:               MergeMax,
		DeviceLatency:       10 * time.Millisecond,
		MaxSchedulingJitter: 2 * time.Millisecond,
		QueueCapacity:       256,
		OnDisconnect:        DisconnectTerminate,
	}
}

// withDefaults 把零值字段替换为默认值，便于调用方只覆盖关心的字段。
func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.FrameDuration <= 0 {
		c.FrameDuration = d.FrameDuration
	}
	if c.PulseDuration <= 0 {
		c.PulseDuration = d.PulseDuration
	}
	if c.DeviceLatency < 0 {
		c.DeviceLatency = d.DeviceLatency
	}
	if c.MaxSchedulingJitter < 0 {
		c.MaxSchedulingJitter = d.MaxSchedulingJitter
	}
	if c.QueueCapacity <= 0 {
		c.QueueCapacity = d.QueueCapacity
	}
	return c
}

package haptic

import "time"

// MergeStrategy 决定同一设备同一时刻多条冲突效果的合并方式。
type MergeStrategy int

const (
	// MergeHighestPriority 仅最高优先级效果生效。
	MergeHighestPriority MergeStrategy = iota
	// MergeMix 将各通道上生效效果线性叠加并钳制到设备量程。
	MergeMix
	// MergeMax 取各效果逐点最大值。
	MergeMax
)

func (s MergeStrategy) String() string {
	switch s {
	case MergeHighestPriority:
		return "highest-priority"
	case MergeMix:
		return "mix"
	case MergeMax:
		return "max"
	default:
		return "unknown"
	}
}

// DisconnectPolicy 决定设备断开时其上进行中反馈的处理方式。
type DisconnectPolicy int

const (
	// DisconnectTerminate 断开即终止该设备所有反馈。
	DisconnectTerminate DisconnectPolicy = iota
	// DisconnectMigrate 尝试迁移到其他兼容设备，迁移失败则终止。
	DisconnectMigrate
)

// Config 是运行时全部可调参数，DefaultConfig 给出合理默认值。
type Config struct {
	FrameRate           int              // 控制帧率（Hz），渲染时间轴的最小粒度
	Merge               MergeStrategy    // 冲突合并策略
	LatencyCompensation time.Duration    // 附加的固定延迟补偿
	JitterAlpha         float64          // 抖动估计的 EMA 系数 (0,1]
	Disconnect          DisconnectPolicy // 设备断开时的处理策略
	MaxActiveEffects    int              // 单设备最大并发效果数，超出时按仲裁规则淘汰
}

// DefaultConfig 返回带合理默认值的配置。
func DefaultConfig() Config {
	return Config{
		FrameRate:           1000,
		Merge:               MergeHighestPriority,
		LatencyCompensation: 2 * time.Millisecond,
		JitterAlpha:         0.2,
		Disconnect:          DisconnectTerminate,
		MaxActiveEffects:    16,
	}
}

// normalize 把零值配置补齐为默认值，并校验。
func (c Config) normalize() (Config, error) {
	d := DefaultConfig()
	if c.FrameRate == 0 {
		c.FrameRate = d.FrameRate
	}
	if c.LatencyCompensation == 0 {
		c.LatencyCompensation = d.LatencyCompensation
	}
	if c.JitterAlpha == 0 {
		c.JitterAlpha = d.JitterAlpha
	}
	if c.MaxActiveEffects == 0 {
		c.MaxActiveEffects = d.MaxActiveEffects
	}
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) validate() error {
	if c.FrameRate <= 0 {
		return newError(ErrKindInvalidInput, "frame rate %d must be positive", c.FrameRate)
	}
	if c.Merge < MergeHighestPriority || c.Merge > MergeMax {
		return newError(ErrKindInvalidInput, "unknown merge strategy %d", int(c.Merge))
	}
	if c.LatencyCompensation < 0 {
		return newError(ErrKindInvalidInput, "latency compensation %v must be >= 0", c.LatencyCompensation)
	}
	if c.JitterAlpha <= 0 || c.JitterAlpha > 1 {
		return newError(ErrKindInvalidInput, "jitter alpha %v out of (0,1]", c.JitterAlpha)
	}
	if c.Disconnect < DisconnectTerminate || c.Disconnect > DisconnectMigrate {
		return newError(ErrKindInvalidInput, "unknown disconnect policy %d", int(c.Disconnect))
	}
	if c.MaxActiveEffects <= 0 {
		return newError(ErrKindInvalidInput, "max active effects %d must be positive", c.MaxActiveEffects)
	}
	return nil
}

// frameInterval 返回控制帧间隔。
func (c Config) frameInterval() time.Duration {
	return time.Second / time.Duration(c.FrameRate)
}

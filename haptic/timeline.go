package haptic

import "time"

// JitterEstimator 用指数滑动平均估计调度/采样抖动的绝对偏差。
// 只依赖观测序列本身，不读取系统时间，保证可复现。
type JitterEstimator struct {
	alpha       float64
	estimate    time.Duration
	initialized bool
}

// NewJitterEstimator 创建抖动估计器，alpha 为 EMA 系数 (0,1]。
func NewJitterEstimator(alpha float64) *JitterEstimator {
	if alpha <= 0 || alpha > 1 {
		alpha = 0.2
	}
	return &JitterEstimator{alpha: alpha}
}

// Observe 观测一次偏差（实际间隔与期望间隔之差），返回更新后的估计值。
func (j *JitterEstimator) Observe(deviation time.Duration) time.Duration {
	if deviation < 0 {
		deviation = -deviation
	}
	if !j.initialized {
		j.estimate = deviation
		j.initialized = true
		return j.estimate
	}
	// EMA: est = alpha*x + (1-alpha)*est，用整数运算保持确定性。
	a := j.alpha
	j.estimate = time.Duration(a*float64(deviation) + (1-a)*float64(j.estimate))
	return j.estimate
}

// Estimate 返回当前抖动估计。
func (j *JitterEstimator) Estimate() time.Duration {
	return j.estimate
}

// Timeline 维护单调推进的调度时间轴，负责延迟补偿。
type Timeline struct {
	fixedCompensation time.Duration
	jitter            *JitterEstimator
	lastScheduled     time.Duration
}

// NewTimeline 创建时间轴。
func NewTimeline(fixedCompensation time.Duration, jitter *JitterEstimator) *Timeline {
	if jitter == nil {
		jitter = NewJitterEstimator(0.2)
	}
	return &Timeline{fixedCompensation: fixedCompensation, jitter: jitter}
}

// Schedule 计算一条效果的实际生效时刻：
// max(请求时刻, 当前时刻) + 固定补偿 + 抖动余量，并保证不早于已调度的最后时刻（不倒流）。
// 同一输入序列下输出恒定，与负载无关。
func (t *Timeline) Schedule(requested, now time.Duration) time.Duration {
	base := requested
	if base < now {
		base = now
	}
	scheduled := base + t.fixedCompensation + t.jitter.Estimate()
	if scheduled < t.lastScheduled {
		scheduled = t.lastScheduled
	}
	t.lastScheduled = scheduled
	return scheduled
}

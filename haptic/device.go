package haptic

import (
	"sort"
	"time"
)

// activeEffect 是设备上一段已排期的效果实例。
type activeEffect struct {
	effect Effect        // 原始指令
	mapped Effect        // 经能力映射后的效果
	start  time.Duration // 时间轴上的生效时刻
	end    time.Duration // 时间轴上的结束时刻（撤销会截断）
}

// deviceState 是单台设备的全部运行时状态，仅由 Runtime 持锁访问。
type deviceState struct {
	id     DeviceID
	caps   DeviceCapability
	active map[EffectID]*activeEffect
}

func newDeviceState(id DeviceID, caps DeviceCapability) *deviceState {
	return &deviceState{
		id:     id,
		caps:   caps,
		active: make(map[EffectID]*activeEffect),
	}
}

// audibleAt 返回时刻 t 处于生效窗口内的效果（映射后版本），按 EffectID 排序保证遍历确定。
func (d *deviceState) audibleAt(t time.Duration) []Effect {
	var out []Effect
	for _, ae := range d.active {
		if ae.start <= t && t < ae.end {
			out = append(out, ae.mapped)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// sample 计算该设备在时刻 t 各通道的输出。
// 每个采样点独立做确定性仲裁，因此任意时刻生效集合自洽。
func (d *deviceState) sample(t time.Duration, strategy MergeStrategy, maxActive int) []float64 {
	out := make([]float64, d.caps.Channels)
	audible := d.audibleAt(t)
	if len(audible) == 0 {
		return out
	}
	res := arbitrate(audible, strategy, d.caps.Channels, maxActive)
	byID := make(map[EffectID]Effect, len(audible))
	for _, e := range audible {
		byID[e.ID] = e
	}
	for ch, ids := range res.channels {
		if len(ids) == 0 {
			continue
		}
		var v float64
		switch strategy {
		case MergeHighestPriority:
			ae := d.active[ids[0]]
			v = sampleEffect(byID[ids[0]], t-ae.start)
		case MergeMix:
			for _, id := range ids {
				ae := d.active[id]
				v += sampleEffect(byID[id], t-ae.start)
			}
		case MergeMax:
			for _, id := range ids {
				ae := d.active[id]
				cand := sampleEffect(byID[id], t-ae.start)
				if abs(cand) > abs(v) {
					v = cand
				}
			}
		}
		// 钳制到设备量程，绝不越界输出。
		if v > d.caps.MaxAmplitude {
			v = d.caps.MaxAmplitude
		} else if v < -d.caps.MaxAmplitude {
			v = -d.caps.MaxAmplitude
		}
		out[ch] = v
	}
	return out
}

// arbitrationAt 返回时刻 t 的仲裁结果与生效效果，供可观测查询使用。
func (d *deviceState) arbitrationAt(t time.Duration, strategy MergeStrategy, maxActive int) (arbitrationResult, []Effect) {
	audible := d.audibleAt(t)
	return arbitrate(audible, strategy, d.caps.Channels, maxActive), audible
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

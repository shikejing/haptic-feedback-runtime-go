package haptic

// device 是一个手柄在运行时内的状态。
// 热插拔通过 (DeviceID, generation) 识别：同一 DeviceID 重新接入时
// generation 递增，旧代次的内部状态被完整清理，不会泄漏到新代次。
type device struct {
	id         DeviceID
	caps       Capabilities
	generation uint64
	connected  bool

	// scheduled 已落到时间轴上的指令（含已结束待回收的）。
	scheduled []*scheduledCommand
	// parked 设备断开且策略为 DisconnectPark 时挂起的反馈快照，
	// 同身份重连后按剩余时长恢复。
	parked []*scheduledCommand

	// pending 已提交但起始帧尚未到的指令数上界由 QueueCapacity 限制。
}

// frameOutput 一帧渲染到一个设备的输出：每个通道一个振幅。
type frameOutput struct {
	Device   DeviceID
	Frame    Frame
	Channels []float64
	Degraded []Degradation // 本帧生效指令的降级记录（可观测）
}

// assignChannels 把仲裁获胜者确定性地分配到设备通道。
// 规则：获胜者已按内容确定排序，第 i 个获胜者占用第 i 个通道；
// 获胜者多于通道数时，多余者按合并策略并入最后一个通道并截断到设备上限。
// 通道占用是每帧从获胜集合推导出来的，因此不存在跨帧的重复占用。
func assignChannels(winners []*scheduledCommand, f Frame, frameDuration int64, caps Capabilities, merge MergeStrategy) []float64 {
	out := make([]float64, caps.Channels)
	for i, w := range winners {
		amp := w.amplitudeAt(f, frameDuration)
		ch := i
		if ch >= caps.Channels {
			ch = caps.Channels - 1
		}
		if ch == i || merge == MergeSum {
			out[ch] += amp
		} else if amp > out[ch] {
			out[ch] = amp
		}
		if out[ch] > caps.MaxAmplitude {
			out[ch] = caps.MaxAmplitude
		}
	}
	return out
}

// prune 回收已彻底结束（含被撤销截断）的指令，保持内部状态有界。
func (d *device) prune(now Frame) {
	kept := d.scheduled[:0]
	for _, s := range d.scheduled {
		end := s.end
		if s.revoked && s.revokedAt < end {
			end = s.revokedAt
		}
		if end > now {
			kept = append(kept, s)
		}
	}
	// 清空尾部引用，避免底层数组持有已回收指令。
	for i := len(kept); i < len(d.scheduled); i++ {
		d.scheduled[i] = nil
	}
	d.scheduled = kept
}

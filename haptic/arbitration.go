package haptic

import "sort"

// arbitration 的确定性保证：
// 仲裁结果只取决于指令集合本身，与到达顺序无关。
// 排序键全部来自指令内容（优先级、来源、类型、振幅、频率），
// CommandID 仅作为内容完全相同指令的最后 tie-break（此时指令可互换），
// 因此任意集合的仲裁结果唯一。

// scheduledCommand 是已落到时间轴上的指令（内部表示）。
type scheduledCommand struct {
	cmd       Command
	mapped    mappedCommand
	start     Frame // 对齐后的起始帧
	end       Frame // 结束帧（不含）
	revoked   bool  // 是否已被撤销
	revokedAt Frame // 撤销生效帧（仅 revoked 为真时有效）
}

// active 判断指令在帧 f 是否存活（含撤销截断）。
func (s *scheduledCommand) active(f Frame) bool {
	if f < s.start || f >= s.end {
		return false
	}
	if s.revoked && f >= s.revokedAt {
		return false
	}
	return true
}

// amplitudeAt 计算该指令在帧 f 的（已映射）振幅。
// frameDuration 用于把帧号换算为相对时间。
func (s *scheduledCommand) amplitudeAt(f Frame, frameDuration int64) float64 {
	rel := int64(f-s.start) * frameDuration
	switch s.cmd.Kind {
	case KindContinuous:
		return s.mapped.amplitude
	case KindPulse:
		return s.mapped.amplitude
	case KindEnvelope:
		return envelopeAmplitude(s.mapped.envelope, rel) * s.mapped.amplitude
	}
	return 0
}

// envelopeAmplitude 在包络曲线上按线性插值取 t（纳秒）处的归一化振幅。
func envelopeAmplitude(env []EnvelopePoint, t int64) float64 {
	if len(env) == 0 {
		return 0
	}
	if t <= int64(env[0].Time) {
		return env[0].Amplitude
	}
	last := env[len(env)-1]
	if t >= int64(last.Time) {
		return last.Amplitude
	}
	// 线性扫描对控制点数量级足够；包络点通常很少。
	for i := 1; i < len(env); i++ {
		if t <= int64(env[i].Time) {
			t0, t1 := int64(env[i-1].Time), int64(env[i].Time)
			a0, a1 := env[i-1].Amplitude, env[i].Amplitude
			if t1 == t0 {
				return a1
			}
			frac := float64(t-t0) / float64(t1-t0)
			return a0 + frac*(a1-a0)
		}
	}
	return last.Amplitude
}

// arbitrationResult 一帧的仲裁结果。
type arbitrationResult struct {
	// winners 是本帧真正产生输出的指令（同一优先级层），
	// 已按内容确定排序，供通道分配使用。
	winners []*scheduledCommand
}

// arbitrate 对一帧上存活的所有指令做确定性仲裁。
// 规则：优先级最高的一层获胜，更低优先级全部被压制（Suppressed）；
// 同层内按 MergeStrategy 合并。输入顺序不影响结果。
func arbitrate(cmds []*scheduledCommand, f Frame) arbitrationResult {
	var alive []*scheduledCommand
	topPriority := 0
	first := true
	for _, s := range cmds {
		if !s.active(f) {
			continue
		}
		if first || s.cmd.Priority > topPriority {
			topPriority = s.cmd.Priority
			first = false
		}
		alive = append(alive, s)
	}
	if len(alive) == 0 {
		return arbitrationResult{}
	}
	// 确定性排序：键全部来自指令内容（优先级、来源、类型、振幅、频率），
	// 与到达顺序无关；CommandID 仅作为内容完全相同指令的最后 tie-break，
	// 此时两条指令可互换，输出不受影响。
	sort.Slice(alive, func(i, j int) bool {
		a, b := alive[i], alive[j]
		if a.cmd.Priority != b.cmd.Priority {
			return a.cmd.Priority > b.cmd.Priority
		}
		if a.cmd.Amplitude != b.cmd.Amplitude {
			return a.cmd.Amplitude > b.cmd.Amplitude
		}
		if a.cmd.Source != b.cmd.Source {
			return a.cmd.Source < b.cmd.Source
		}
		if a.cmd.Kind != b.cmd.Kind {
			return a.cmd.Kind < b.cmd.Kind
		}
		if a.cmd.Frequency != b.cmd.Frequency {
			return a.cmd.Frequency < b.cmd.Frequency
		}
		return a.cmd.ID < b.cmd.ID
	})
	res := arbitrationResult{}
	for _, s := range alive {
		if s.cmd.Priority != topPriority {
			continue // 被压制
		}
		res.winners = append(res.winners, s)
	}
	return res
}

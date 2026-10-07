package haptic

import (
	"fmt"
	"sort"
)

// Decision 记录一次仲裁的结果，保证输出可解释。
type Decision struct {
	Channel      int
	Winner       EffectID
	Contributors []EffectID // 参与合并（Mix/Max）或被抢占的效果
	Strategy     MergeStrategy
	Reason       string
}

// arbitrationResult 是一次仲裁的输出：每个通道分到哪些效果。
type arbitrationResult struct {
	channels  [][]EffectID // channel -> 生效效果（按优先级降序）
	decisions []Decision
	evicted   []EffectID // 因容量或优先级被淘汰的效果
}

// rankEffects 对效果做全序排序：优先级降序，平局按 EffectID 字典序升序。
// 排序键不含任何到达顺序信息，因此结果与提交先后无关。
func rankEffects(effects []Effect) []Effect {
	ranked := make([]Effect, len(effects))
	copy(ranked, effects)
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Priority != ranked[j].Priority {
			return ranked[i].Priority > ranked[j].Priority
		}
		return ranked[i].ID < ranked[j].ID
	})
	return ranked
}

// arbitrate 对一组候选效果做确定性仲裁。
// 结果只取决于效果集合本身（Priority、ID），与到达顺序无关。
func arbitrate(effects []Effect, strategy MergeStrategy, channels, maxActive int) arbitrationResult {
	res := arbitrationResult{channels: make([][]EffectID, channels)}
	if channels <= 0 || len(effects) == 0 {
		return res
	}
	ranked := rankEffects(effects)

	// 容量淘汰：只保留排名前 maxActive 的效果。
	if maxActive > 0 && len(ranked) > maxActive {
		for _, e := range ranked[maxActive:] {
			res.evicted = append(res.evicted, e.ID)
		}
		ranked = ranked[:maxActive]
	}

	// 确定性通道分配：第 i 名分配到通道 i % channels。
	assignments := make([][]Effect, channels)
	for i, e := range ranked {
		ch := i % channels
		assignments[ch] = append(assignments[ch], e)
	}

	for ch, group := range assignments {
		if len(group) == 0 {
			continue
		}
		ids := make([]EffectID, len(group))
		for i, e := range group {
			ids[i] = e.ID
		}
		switch strategy {
		case MergeHighestPriority:
			// 通道内只有最高优先级者发声，其余记为被抢占。
			res.channels[ch] = ids[:1]
			d := Decision{
				Channel:  ch,
				Winner:   ids[0],
				Strategy: strategy,
				Reason:   "highest priority wins the channel",
			}
			if len(ids) > 1 {
				d.Contributors = append([]EffectID(nil), ids[1:]...)
				d.Reason = fmt.Sprintf("highest priority wins; %d effect(s) preempted on channel", len(ids)-1)
			}
			res.decisions = append(res.decisions, d)
		case MergeMix, MergeMax:
			res.channels[ch] = ids
			d := Decision{
				Channel:  ch,
				Winner:   ids[0],
				Strategy: strategy,
				Reason:   fmt.Sprintf("%d effect(s) merged on channel", len(group)),
			}
			if len(ids) > 1 {
				d.Contributors = append([]EffectID(nil), ids[1:]...)
			}
			res.decisions = append(res.decisions, d)
		}
	}
	return res
}

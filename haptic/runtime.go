package haptic

import (
	"sort"
	"sync"
	"time"
)

// Runtime 是触觉反馈调度与渲染运行时，全部状态在本地进程内。
// 所有公开方法可并发调用；时间轴由逻辑时钟驱动，与系统时间无关。
type Runtime struct {
	cfg      Config
	mu       sync.Mutex
	now      time.Duration // 逻辑时钟，只能前进
	devices  map[DeviceID]*deviceState
	registry map[DeviceID]DeviceCapability // 记住曾接入设备的能力，供热插拔恢复
	timeline *Timeline
	recorder *Recorder
}

// New 创建运行时。cfg 中的零值字段用 DefaultConfig 补齐。
func New(cfg Config) (*Runtime, error) {
	ncfg, err := cfg.normalize()
	if err != nil {
		return nil, err
	}
	return &Runtime{
		cfg:      ncfg,
		devices:  make(map[DeviceID]*deviceState),
		registry: make(map[DeviceID]DeviceCapability),
		timeline: NewTimeline(ncfg.LatencyCompensation, NewJitterEstimator(ncfg.JitterAlpha)),
	}, nil
}

// Config 返回运行时生效的配置。
func (r *Runtime) Config() Config { return r.cfg }

// Now 返回当前逻辑时刻。
func (r *Runtime) Now() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now
}

// AttachDevice 接入手柄；若该设备曾接入过，其身份与能力登记会被保留/更新。
func (r *Runtime) AttachDevice(id DeviceID, caps DeviceCapability) error {
	if id == "" {
		return newError(ErrKindInvalidInput, "device id is empty")
	}
	if err := caps.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.devices[id]; ok {
		return newError(ErrKindDeviceExists, "device %s already attached", id)
	}
	r.devices[id] = newDeviceState(id, caps)
	r.registry[id] = caps
	r.recordLocked(Event{At: r.now, Kind: EventAttach, Device: id, Caps: caps})
	return nil
}

// DetachDevice 断开手柄，按 DisconnectPolicy 终止或迁移其上的反馈。
// 只影响该设备自身状态，其他设备不受牵连。
func (r *Runtime) DetachDevice(id DeviceID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	dev, ok := r.devices[id]
	if !ok {
		return newError(ErrKindUnknownDevice, "device %s not attached", id)
	}
	if r.cfg.Disconnect == DisconnectMigrate && len(dev.active) > 0 {
		r.migrateLocked(id, dev)
	}
	delete(r.devices, id)
	r.recordLocked(Event{At: r.now, Kind: EventDetach, Device: id})
	return nil
}

// migrateLocked 把断开设备上的效果迁移到其他兼容设备（按 DeviceID 字典序选择，保证确定）。
func (r *Runtime) migrateLocked(from DeviceID, dev *deviceState) {
	targets := make([]DeviceID, 0, len(r.devices))
	for id := range r.devices {
		if id != from {
			targets = append(targets, id)
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i] < targets[j] })
	// 按 EffectID 排序处理，保证迁移结果与 map 遍历顺序无关。
	ids := make([]EffectID, 0, len(dev.active))
	for id := range dev.active {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, eid := range ids {
		ae := dev.active[eid]
		for _, tid := range targets {
			mapped, _, err := mapEffectToDevice(ae.effect, r.devices[tid].caps)
			if err != nil {
				continue
			}
			moved := &activeEffect{effect: ae.effect, mapped: mapped, start: ae.start, end: ae.end}
			r.devices[tid].active[eid] = moved
			break // 找不到兼容设备则按策略终止（丢弃）
		}
	}
}

// Submit 提交一条反馈指令，返回排期后的生效时刻。
// 指令先按目标设备能力映射/降级，再落到时间轴上。
func (r *Runtime) Submit(dev DeviceID, e Effect) (time.Duration, error) {
	if err := e.Validate(); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[dev]
	if !ok {
		return 0, newError(ErrKindUnknownDevice, "device %s not attached", dev)
	}
	if _, dup := d.active[e.ID]; dup {
		return 0, newError(ErrKindInvalidInput, "effect %s already active on device %s", e.ID, dev)
	}
	mapped, _, err := mapEffectToDevice(e, d.caps)
	if err != nil {
		return 0, err
	}
	start := r.timeline.Schedule(r.now, r.now)
	d.active[e.ID] = &activeEffect{
		effect: e,
		mapped: mapped,
		start:  start,
		end:    start + e.effectDuration(),
	}
	r.recordLocked(Event{At: r.now, Kind: EventSubmit, Device: dev, Effect: e})
	return start, nil
}

// Revoke 撤销一条反馈；已排期的部分按当前时刻截断，保证不再输出。
func (r *Runtime) Revoke(dev DeviceID, id EffectID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[dev]
	if !ok {
		return newError(ErrKindUnknownDevice, "device %s not attached", dev)
	}
	ae, ok := d.active[id]
	if !ok {
		return newError(ErrKindUnknownEffect, "effect %s not active on device %s", id, dev)
	}
	if r.now <= ae.start {
		// 尚未生效，直接移除。
		delete(d.active, id)
	} else {
		// 截断到当前时刻。
		ae.end = r.now
	}
	r.recordLocked(Event{At: r.now, Kind: EventRevoke, Device: dev, EffectID: id})
	return nil
}

// Advance 把逻辑时钟推进到 at（必须单调不减），驱动抖动估计。
// 已结束的效果保留在历史中，使 Render 是事件历史的纯函数（可复现）。
func (r *Runtime) Advance(at time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if at < r.now {
		return newError(ErrKindInvalidInput, "cannot move clock backwards: %v < %v", at, r.now)
	}
	if at > r.now {
		// 观测调度间隔相对期望帧间隔的偏差，更新抖动估计。
		deviation := (at - r.now) - r.cfg.frameInterval()
		r.timeline.jitter.Observe(deviation)
		r.now = at
		r.recordLocked(Event{At: r.now, Kind: EventAdvance})
	}
	return nil
}

// Render 拉取设备在 [from, to) 区间内的输出帧。
// 帧时刻对齐到控制帧网格；同一状态与区间下结果逐帧一致。
func (r *Runtime) Render(dev DeviceID, from, to time.Duration) ([]Frame, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[dev]
	if !ok {
		return nil, newError(ErrKindUnknownDevice, "device %s not attached", dev)
	}
	if from < 0 || to <= from {
		return nil, newError(ErrKindInvalidInput, "invalid render window [%v,%v)", from, to)
	}
	interval := r.cfg.frameInterval()
	// 对齐到帧网格。
	start := (from + interval - 1) / interval * interval
	var frames []Frame
	for t := start; t < to; t += interval {
		frames = append(frames, Frame{
			Device:   dev,
			Time:     t,
			Channels: d.sample(t, r.cfg.Merge, r.cfg.MaxActiveEffects),
		})
	}
	return frames, nil
}

// DeviceSnapshot 是某设备某时刻的可观测快照。
type DeviceSnapshot struct {
	Device    DeviceID
	At        time.Duration
	Active    []ActiveInfo
	Decisions []Decision
}

// ActiveInfo 描述一条生效中的效果及其来源。
type ActiveInfo struct {
	Effect  Effect
	Source  SourceID
	Channel int
	Start   time.Duration
	End     time.Duration
}

// Snapshot 查询设备在当前时刻生效的反馈及来源、仲裁结论。
func (r *Runtime) Snapshot(dev DeviceID) (DeviceSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[dev]
	if !ok {
		return DeviceSnapshot{}, newError(ErrKindUnknownDevice, "device %s not attached", dev)
	}
	res, _ := d.arbitrationAt(r.now, r.cfg.Merge, r.cfg.MaxActiveEffects)
	channelOf := make(map[EffectID]int)
	for ch, ids := range res.channels {
		for _, id := range ids {
			channelOf[id] = ch
		}
	}
	snap := DeviceSnapshot{Device: dev, At: r.now, Decisions: res.decisions}
	// 只列出当前仍生效或待生效的效果；按 EffectID 排序输出，保证可观测结果确定。
	ids := make([]EffectID, 0, len(d.active))
	for id, ae := range d.active {
		if ae.end > r.now {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		ae := d.active[id]
		snap.Active = append(snap.Active, ActiveInfo{
			Effect:  ae.effect,
			Source:  ae.effect.Source,
			Channel: channelOf[id],
			Start:   ae.start,
			End:     ae.end,
		})
	}
	return snap, nil
}

// EnableRecording 开启事件录制。
func (r *Runtime) EnableRecording() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recorder == nil {
		r.recorder = &Recorder{}
	}
}

// Recording 返回运行时至今录制的事件流副本（未开启录制时返回 nil）。
func (r *Runtime) Recording() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recorder == nil {
		return nil
	}
	return r.recorder.Events()
}

func (r *Runtime) recordLocked(ev Event) {
	if r.recorder != nil {
		r.recorder.record(ev)
	}
}

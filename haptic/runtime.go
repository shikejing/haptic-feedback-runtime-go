package haptic

import (
	"fmt"
	"sort"
	"sync"
)

// Runtime 是触觉反馈调度与渲染运行时。
//
// 时间模型：渲染由 Render(n) 显式驱动逻辑帧前进，不依赖墙钟，
// 因此同一段输入在不同负载下渲染出的时间轴完全一致。
// 提交/撤销可以并发调用，它们在两次渲染之间被记录，
// 并在下一次渲染时以确定顺序（CommandID 升序）统一生效——
// 到达时刻在帧内的抖动被时间轴对齐吸收（见 Config.MaxSchedulingJitter）。
type Runtime struct {
	cfg Config

	mu      sync.Mutex
	now     Frame // 下一个待渲染的帧
	nextID  CommandID
	devices map[DeviceID]*device
	// parked 保存已断开但按策略挂起的设备状态，按 DeviceID 索引。
	parked map[DeviceID]*device

	rec *recorder // 非 nil 时录制全部事件用于回放
}

// NewRuntime 创建运行时，零值字段使用默认配置。
func NewRuntime(cfg Config) *Runtime {
	return &Runtime{
		cfg:     cfg.withDefaults(),
		devices: make(map[DeviceID]*device),
		parked:  make(map[DeviceID]*device),
		nextID:  1,
	}
}

// Config 返回运行时实际生效的配置。
func (r *Runtime) Config() Config { return r.cfg }

// Now 返回下一个待渲染的逻辑帧号。
func (r *Runtime) Now() Frame {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now
}

// leadFrames 延迟补偿提前量：设备固有延迟与调度抖动窗口之和，
// 向上取整到帧。输出提前该帧数排程，使物理效果落在意图时刻。
func (r *Runtime) leadFrames() Frame {
	total := r.cfg.DeviceLatency + r.cfg.MaxSchedulingJitter
	return Frame((int64(total) + int64(r.cfg.FrameDuration) - 1) / int64(r.cfg.FrameDuration))
}

// AddDevice 接入手柄。同一 DeviceID 重复接入视为热插拔重连：
// 代次递增，若存在按策略挂起的反馈则恢复剩余部分。
func (r *Runtime) AddDevice(id DeviceID, caps Capabilities) error {
	if err := caps.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.devices[id]; ok {
		// 同身份重连：先按断开策略清理旧代次。
		r.disconnectLocked(old)
	}
	d := &device{id: id, caps: caps, connected: true}
	if p, ok := r.parked[id]; ok {
		d.generation = p.generation + 1
		// 恢复挂起的反馈：剩余时长从当前帧继续。
		for _, s := range p.parked {
			remaining := s.end // park 时 end 已被改写为剩余帧数
			restored := *s
			restored.start = r.now
			restored.end = r.now + remaining
			restored.revoked = false
			restored.revokedAt = 0
			d.scheduled = append(d.scheduled, &restored)
		}
		delete(r.parked, id)
	} else if old, ok := r.devices[id]; ok {
		d.generation = old.generation + 1
	}
	r.devices[id] = d
	if r.rec != nil {
		r.rec.add(event{frame: r.now, op: opAddDevice, device: id, caps: caps})
	}
	return nil
}

// RemoveDevice 断开手柄。进行中的反馈按 Config.OnDisconnect 处理，
// 不影响其他设备。
func (r *Runtime) RemoveDevice(id DeviceID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownDevice, id)
	}
	r.disconnectLocked(d)
	delete(r.devices, id)
	if r.rec != nil {
		r.rec.add(event{frame: r.now, op: opRemoveDevice, device: id})
	}
	return nil
}

func (r *Runtime) disconnectLocked(d *device) {
	d.connected = false
	switch r.cfg.OnDisconnect {
	case DisconnectPark:
		// 挂起：把存活反馈的剩余时长快照下来。
		for _, s := range d.scheduled {
			if !s.active(r.now) {
				continue
			}
			snap := *s
			snap.end = s.end - r.now // 剩余帧数
			if s.revoked && s.revokedAt-r.now < snap.end {
				snap.end = s.revokedAt - r.now
			}
			snap.revoked = false
			snap.revokedAt = 0
			d.parked = append(d.parked, &snap)
		}
		d.scheduled = nil
		r.parked[d.id] = d
	default: // DisconnectTerminate
		d.scheduled = nil
		d.parked = nil
		delete(r.parked, d.id)
	}
}

// Submit 提交一条反馈指令。指令被校验、按目标设备能力映射，
// 并对齐到时间轴。返回分配的 CommandID（若指令自带 ID 为 0）。
func (r *Runtime) Submit(cmd Command) (CommandID, error) {
	if err := cmd.Validate(); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[cmd.Device]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownDevice, cmd.Device)
	}
	// 背压：限制每设备存活指令数。
	alive := 0
	for _, s := range d.scheduled {
		if s.active(r.now) || s.start >= r.now {
			alive++
		}
	}
	if alive >= r.cfg.QueueCapacity {
		return 0, fmt.Errorf("%w: device %q", ErrQueueFull, cmd.Device)
	}
	if cmd.ID == 0 {
		cmd.ID = r.nextID
		r.nextID++
	} else if cmd.ID >= r.nextID {
		r.nextID = cmd.ID + 1
	}

	// 时序：意图起始帧不得早于当前帧（禁止时间倒流），
	// 再减去延迟补偿提前量，得到实际排程帧。
	intended := cmd.Start
	if intended < r.now {
		intended = r.now
	}
	lead := r.leadFrames()
	start := Frame(0)
	if intended > lead {
		start = intended - lead
	}
	dur := cmd.Duration
	if cmd.Kind == KindPulse {
		dur = r.cfg.PulseDuration
	}
	frames := Frame((int64(dur) + int64(r.cfg.FrameDuration) - 1) / int64(r.cfg.FrameDuration))
	if frames == 0 {
		frames = 1
	}
	s := &scheduledCommand{
		cmd:    cmd,
		mapped: mapToDevice(cmd, d.caps),
		start:  start,
		end:    start + frames,
	}
	d.scheduled = append(d.scheduled, s)
	if r.rec != nil {
		r.rec.add(event{frame: r.now, op: opSubmit, cmd: cmd})
	}
	return cmd.ID, nil
}

// Revoke 撤销一条指令。已提交的反馈从下一个渲染帧起被截断，
// 已渲染的过去帧不受影响（时间不倒流）。
func (r *Runtime) Revoke(id CommandID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range r.devices {
		for _, s := range d.scheduled {
			if s.cmd.ID == id {
				if s.end <= r.now {
					return fmt.Errorf("%w: command %d already finished", ErrUnknownCommand, id)
				}
				s.revoked = true
				s.revokedAt = r.now
				if r.rec != nil {
					r.rec.add(event{frame: r.now, op: opRevoke, id: id})
				}
				return nil
			}
		}
	}
	return fmt.Errorf("%w: command %d", ErrUnknownCommand, id)
}

// Render 推进 n 帧渲染，返回所有设备每帧的输出。
// 输出按 (Frame, DeviceID) 排序，与 map 遍历顺序无关。
// 每个设备的渲染是纯函数计算，慢设备不会拖住其他设备。
func (r *Runtime) Render(n int) []frameOutput {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recordRenderLocked(n)
	var out []frameOutput
	// 设备列表排序，保证帧内设备顺序确定。
	ids := make([]string, 0, len(r.devices))
	for id := range r.devices {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)

	for i := 0; i < n; i++ {
		f := r.now
		for _, id := range ids {
			d := r.devices[DeviceID(id)]
			res := arbitrate(d.scheduled, f)
			fo := frameOutput{
				Device:   d.id,
				Frame:    f,
				Channels: assignChannels(res.winners, f, int64(r.cfg.FrameDuration), d.caps, r.cfg.Merge),
			}
			for _, w := range res.winners {
				fo.Degraded = append(fo.Degraded, w.mapped.deg)
			}
			out = append(out, fo)
			d.prune(f)
		}
		r.now++
	}
	return out
}

// ActiveAt 查询某设备在当前帧生效的反馈及其来源（可观测性）。
// 返回按确定性顺序排序。
func (r *Runtime) ActiveAt(id DeviceID) ([]ActiveFeedback, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownDevice, id)
	}
	res := arbitrate(d.scheduled, r.now)
	winning := make(map[*scheduledCommand]bool, len(res.winners))
	for _, w := range res.winners {
		winning[w] = true
	}
	var out []ActiveFeedback
	for _, s := range d.scheduled {
		if !s.active(r.now) {
			continue
		}
		out = append(out, ActiveFeedback{
			CommandID:  s.cmd.ID,
			Source:     s.cmd.Source,
			Kind:       s.cmd.Kind,
			Priority:   s.cmd.Priority,
			StartFrame: s.start,
			EndFrame:   s.end,
			Suppressed: !winning[s],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CommandID < out[j].CommandID })
	return out, nil
}

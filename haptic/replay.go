package haptic

// 录制与回放：所有事件都打在逻辑帧号上，与墙钟无关，
// 因此回放结果与原运行逐帧一致，可用于问题定位。

type opKind int

const (
	opAddDevice opKind = iota
	opRemoveDevice
	opSubmit
	opRevoke
)

// event 一条被录制的事件，Frame 为事件生效的逻辑帧。
type event struct {
	frame  Frame
	op     opKind
	device DeviceID
	caps   Capabilities
	cmd    Command
	id     CommandID
}

// recorder 追加式事件日志。仅在 Runtime 持锁时访问。
type recorder struct {
	events []event
	// renders 记录每次 Render 的帧数，回放时按相同节奏推进。
	renders []int
}

func (r *recorder) add(e event) { r.events = append(r.events, e) }

// Recording 是一段可回放的录制。
type Recording struct {
	events  []event
	renders []int
}

// StartRecording 开始录制。之后该运行时的全部事件与渲染节奏
// 都会被记录，直到 StopRecording。
func (r *Runtime) StartRecording() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rec = &recorder{}
}

// StopRecording 停止录制并返回录制结果。
func (r *Runtime) StopRecording() Recording {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := Recording{}
	if r.rec != nil {
		rec.events = append(rec.events, r.rec.events...)
		rec.renders = append(rec.renders, r.rec.renders...)
	}
	r.rec = nil
	return rec
}

// recordRender 在 Render 中记录渲染节奏（由 Render 调用）。
func (r *Runtime) recordRenderLocked(n int) {
	if r.rec != nil {
		r.rec.renders = append(r.rec.renders, n)
	}
}

// Replay 用相同配置重放一段录制，返回逐帧输出。
// 回放是确定性的：相同 Recording 与配置必然产生相同输出。
func Replay(cfg Config, rec Recording) []frameOutput {
	rt := NewRuntime(cfg)
	var out []frameOutput
	ei := 0
	for _, n := range rec.renders {
		// 应用在该次渲染之前（帧号 <= 当前帧）记录的全部事件。
		for ei < len(rec.events) && rec.events[ei].frame <= rt.Now() {
			applyReplayEvent(rt, rec.events[ei])
			ei++
		}
		out = append(out, rt.Render(n)...)
	}
	// 尾部的剩余事件（录制结束后不再渲染的）也应用掉，保证不丢。
	for ei < len(rec.events) {
		applyReplayEvent(rt, rec.events[ei])
		ei++
	}
	return out
}

func applyReplayEvent(rt *Runtime, e event) {
	switch e.op {
	case opAddDevice:
		_ = rt.AddDevice(e.device, e.caps)
	case opRemoveDevice:
		_ = rt.RemoveDevice(e.device)
	case opSubmit:
		_, _ = rt.Submit(e.cmd)
	case opRevoke:
		_ = rt.Revoke(e.id)
	}
}

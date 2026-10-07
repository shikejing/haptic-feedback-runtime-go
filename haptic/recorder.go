package haptic

import "time"

// EventKind 录制事件的类型。
type EventKind int

const (
	EventAttach EventKind = iota
	EventDetach
	EventSubmit
	EventRevoke
	EventAdvance
)

func (k EventKind) String() string {
	switch k {
	case EventAttach:
		return "attach"
	case EventDetach:
		return "detach"
	case EventSubmit:
		return "submit"
	case EventRevoke:
		return "revoke"
	case EventAdvance:
		return "advance"
	default:
		return "unknown"
	}
}

// Event 是一条可回放的录制事件。
type Event struct {
	At       time.Duration
	Kind     EventKind
	Device   DeviceID
	Caps     DeviceCapability
	Effect   Effect
	EffectID EffectID
}

// Recorder 按逻辑时间顺序记录事件，用于问题复现。
type Recorder struct {
	events []Event
}

func (rc *Recorder) record(ev Event) {
	rc.events = append(rc.events, ev)
}

// Events 返回已录制事件的副本。
func (rc *Recorder) Events() []Event {
	out := make([]Event, len(rc.events))
	copy(out, rc.events)
	return out
}

// Replay 用相同配置重放事件流，返回重放后的运行时。
// 重放只依赖事件流与配置，与系统时间、随机源、遍历顺序无关；
// 在相同渲染查询下，重放运行时与原运行时的输出逐帧一致。
func Replay(cfg Config, events []Event) (*Runtime, error) {
	r, err := New(cfg)
	if err != nil {
		return nil, err
	}
	for i, ev := range events {
		if err := r.Advance(ev.At); err != nil {
			return nil, newError(ErrKindInvalidInput, "replay event %d (%s): %v", i, ev.Kind, err)
		}
		switch ev.Kind {
		case EventAttach:
			if err := r.AttachDevice(ev.Device, ev.Caps); err != nil {
				return nil, err
			}
		case EventDetach:
			if err := r.DetachDevice(ev.Device); err != nil {
				return nil, err
			}
		case EventSubmit:
			if _, err := r.Submit(ev.Device, ev.Effect); err != nil {
				return nil, err
			}
		case EventRevoke:
			if err := r.Revoke(ev.Device, ev.EffectID); err != nil {
				return nil, err
			}
		case EventAdvance:
			// 时钟已在上面推进。
		default:
			return nil, newError(ErrKindInvalidInput, "replay event %d: unknown kind %d", i, int(ev.Kind))
		}
	}
	return r, nil
}

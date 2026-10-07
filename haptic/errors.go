package haptic

import "fmt"

// ErrorKind 区分运行时可能返回的错误类别，调用方可据此分别处理。
type ErrorKind int

const (
	ErrKindInvalidInput ErrorKind = iota
	ErrKindUnknownDevice
	ErrKindUnknownEffect
	ErrKindCapabilityMismatch
	ErrKindDeviceExists
)

// Error 是运行时返回的结构化错误，包含类别与可读原因。
type Error struct {
	Kind   ErrorKind
	Reason string
}

func (e *Error) Error() string { return fmt.Sprintf("haptic: %v: %s", e.Kind, e.Reason) }

func newError(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Reason: fmt.Sprintf(format, args...)}
}

func (k ErrorKind) String() string {
	switch k {
	case ErrKindInvalidInput:
		return "invalid-input"
	case ErrKindUnknownDevice:
		return "unknown-device"
	case ErrKindUnknownEffect:
		return "unknown-effect"
	case ErrKindCapabilityMismatch:
		return "capability-mismatch"
	case ErrKindDeviceExists:
		return "device-exists"
	default:
		return "unknown"
	}
}

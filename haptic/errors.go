package haptic

import "errors"

// 可区分的错误原因，调用方可用 errors.Is 判定。
var (
	// ErrInvalidCommand 指令本身非法（振幅越界、时长非正等）。
	ErrInvalidCommand = errors.New("haptic: invalid command")
	// ErrUnknownDevice 目标设备未接入。
	ErrUnknownDevice = errors.New("haptic: unknown device")
	// ErrUnknownCommand 撤销的指令不存在或已结束。
	ErrUnknownCommand = errors.New("haptic: unknown command")
	// ErrQueueFull 某设备的待处理指令队列已满（背压保护）。
	ErrQueueFull = errors.New("haptic: device command queue full")
)

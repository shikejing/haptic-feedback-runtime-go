# haptic-feedback-runtime-go

面向 VR 手柄的触觉反馈调度与渲染运行时。整条链路在本地进程内运行，
不依赖任何外部服务；设备输出写入内存中的模拟通道（`Render` 的返回值），
可复现、可录制、可回放。

## 快速开始

```go
rt := haptic.NewRuntime(haptic.DefaultConfig())
rt.AddDevice("left", haptic.Capabilities{
    Channels: 2, MinFrequency: 50, MaxFrequency: 500,
    SampleRate: 1000, MaxAmplitude: 1.0,
})
id, _ := rt.Submit(haptic.Command{
    Device: "left", Source: "game", Kind: haptic.KindContinuous,
    Priority: 10, Duration: 100 * time.Millisecond,
    Amplitude: 0.8, Frequency: 120,
})
for _, frame := range rt.Render(20) { // 渲染 20 帧（默认 5ms/帧）
    // frame.Device / frame.Frame / frame.Channels / frame.Degraded
}
rt.Revoke(id) // 撤销：从下一渲染帧起截断
```

端到端示例见 `cmd/hapticdemo`（`go run ./cmd/hapticdemo`）。

## 指令类型

| Kind | 说明 |
|---|---|
| `KindContinuous` | 持续震动：恒定强度，持续 `Duration` |
| `KindPulse` | 脉冲：固定长度（`Config.PulseDuration`）的一次性震动，只触发一次 |
| `KindEnvelope` | 带包络的波形：振幅按控制点线性插值随时间变化 |

非法指令（振幅越界、时长非正、包络为空等）在 `Submit` 时以
`ErrInvalidCommand` 拒绝，错误原因可用 `errors.Is` 区分，不会崩溃或产生未定义输出。

## 仲裁语义（确定性）

同一帧、同一设备上存活的所有指令按以下规则仲裁：

1. **优先级分层**：`Priority` 数值最大的一层获胜，更低优先级的指令
   全部被压制（`ActiveFeedback.Suppressed=true`，不产生输出但仍存活）。
2. **同层合并**：获胜层内按 `Config.Merge` 合并——
   `MergeMax` 取逐帧最大值，`MergeSum` 求和后按设备 `MaxAmplitude` 截断。
3. **通道分配**：获胜者按内容确定排序
   （优先级降序 → 振幅降序 → 来源 → 类型 → 频率 → CommandID），
   第 i 个获胜者占用第 i 个通道；获胜者多于通道数时，
   多余者按合并策略并入最后一个通道并截断。
   通道占用每帧从获胜集合重新推导，不存在跨帧的重复占用。

排序键全部来自指令内容，因此**仲裁结果与指令到达顺序无关**：
同一指令集合任意顺序提交，渲染输出逐帧一致（有测试保证）。

## 能力协商与降级

每条指令在下发前按目标设备 `Capabilities` 映射：

| 能力 | 降级策略 |
|---|---|
| `MaxAmplitude` | 振幅截断到上限 |
| `Min/MaxFrequency` | 频率钳制到范围内 |
| `SampleRate` | 包络按采样周期重采样，同周期控制点取最大振幅（不丢峰值） |
| `Channels` | 获胜者溢出时并入最后通道 |

所有降级都记录在该帧输出的 `Degraded` 字段中（请求值/应用值/降级类型），
**既不静默丢弃，也不越界输出**。

## 时序与延迟补偿

- 渲染由 `Render(n)` 显式驱动**逻辑帧**前进，不依赖墙钟；
  同一输入按任意节奏渲染（不同负载），时间轴逐帧一致。
- 提交/撤销在两次渲染之间到达时，统一在下一次渲染边界生效，
  帧内到达时刻的抖动被时间轴对齐吸收。
- 输出提前 `ceil((DeviceLatency + MaxSchedulingJitter) / FrameDuration)` 帧排程，
  补偿设备固有延迟与调度抖动。
- 起始帧在过去时被钳制到当前帧；已渲染的历史帧绝不改写（时间不倒流）；
  脉冲等一次性反馈只触发一次，不会重复。

## 并发与撤销

- `Submit`/`Revoke`/`Render`/`AddDevice`/`RemoveDevice` 均可并发调用
  （内部单互斥锁，`-race` 测试覆盖）。
- `Revoke(id)` 使指令从下一渲染帧起截断；只影响目标指令。
- 每设备指令队列有容量上限（`QueueCapacity`），溢出返回 `ErrQueueFull` 背压错误。
- 渲染是纯函数计算，无设备间阻塞：慢设备不会拖住其他设备。

## 热插拔

- `RemoveDevice` 时按 `Config.OnDisconnect` 处理进行中的反馈：
  - `DisconnectTerminate`（默认）：立即终止，同身份重连后不复活；
  - `DisconnectPark`：挂起剩余时长，同一 `DeviceID` 重连后从当前帧恢复剩余部分。
- 设备断开期间不产生输出，其他设备不受影响；
  旧代次状态被完整清理，不向新代次泄漏。

## 可复现与回放

- 固定输入 + 相同配置 ⇒ 渲染结果逐帧一致；
  与系统时间、随机源、map 遍历顺序无关（输出按 `(Frame, DeviceID)` 排序）。
- `StartRecording`/`StopRecording` 录制全部事件与渲染节奏，
  `Replay(cfg, rec)` 原样回放，用于问题定位。

## 可观测与配置

- `ActiveAt(device)`：查询某设备当前生效的反馈及来源、是否被压制。
- 每帧输出携带 `Degraded` 降级记录。
- `Config` 字段与默认值：

| 字段 | 默认值 | 含义 |
|---|---|---|
| `FrameDuration` | 5ms | 渲染帧粒度（200Hz） |
| `PulseDuration` | 20ms | 脉冲固定长度 |
| `Merge` | `MergeMax` | 同优先级合并策略 |
| `DeviceLatency` | 10ms | 设备固有延迟（补偿提前量） |
| `MaxSchedulingJitter` | 2ms | 抖动吸收窗口（计入提前量） |
| `QueueCapacity` | 256 | 每设备指令队列容量 |
| `OnDisconnect` | `DisconnectTerminate` | 断开时反馈处理策略 |

零值字段自动取默认值，可只覆盖关心的项。

## 测试

```sh
go test ./...          # 全部测试
go test ./... -race    # 含并发竞争检测
```

覆盖：多来源冲突与顺序无关性、能力降级、延迟补偿与时间轴一致性、
并发提交/撤销、热插拔终止/挂起恢复、录制回放与可复现性。

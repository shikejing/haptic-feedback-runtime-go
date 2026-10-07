# haptic-feedback-runtime-go

面向虚拟现实手柄的触觉反馈调度与渲染运行时。整条链路在本地进程内完成，不依赖任何外部服务；设备输入由内存中的模拟通道（`AttachDevice` / `Submit` / `Advance`）提供。

核心包：`haptic`（`github.com/shikejing/haptic-feedback-runtime-go/haptic`）。

## 快速上手

```go
rt, _ := haptic.New(haptic.DefaultConfig())
rt.AttachDevice("left-hand", haptic.DeviceCapability{
    Channels: 2, MinFrequency: 20, MaxFrequency: 500,
    SampleRate: 4000, MaxAmplitude: 1.0,
})
start, _ := rt.Submit("left-hand", haptic.Effect{
    ID: "hit-1", Source: "game", Priority: 5,
    Kind: haptic.KindRumble, Amplitude: 0.8, Frequency: 120,
    Duration: 100 * time.Millisecond,
})
rt.Advance(50 * time.Millisecond)              // 推进逻辑时钟
frames, _ := rt.Render("left-hand", start, start+100*time.Millisecond)
rt.Revoke("left-hand", "hit-1")                // 撤销即截断
```

## 反馈指令模型

一条 `Effect` 支持三种形态（`Kind`）：

| Kind          | 语义           | 关键字段 |
|---------------|----------------|----------|
| `KindRumble`  | 持续震动       | `Amplitude`, `Frequency`, `Duration` |
| `KindPulse`   | 单次脉冲       | `Amplitude`, `Duration` |
| `KindWaveform`| 带包络的波形   | `Samples`, `SampleRate`, `Envelope`（折点间线性插值） |

每条指令携带 `Source`（来源）、`Priority`（优先级）、`ID`（唯一标识）。非法指令在 `Submit` 时被拒绝，返回 `*haptic.Error`，其 `Kind` 字段可区分原因（`ErrKindInvalidInput` / `ErrKindUnknownDevice` / `ErrKindUnknownEffect` / `ErrKindCapabilityMismatch` / `ErrKindDeviceExists`），不会 panic 或产生未定义行为。

## 仲裁语义

同一时刻同一设备上的冲突按以下**确定性**规则仲裁，结果只取决于效果集合本身，**与指令到达顺序无关**：

1. **排名**：所有候选效果按 `(Priority 降序, EffectID 字典序升序)` 全序排列。排序键不含任何到达顺序信息。
2. **容量淘汰**：排名超出 `MaxActiveEffects` 的效果被淘汰（记录在仲裁结果的 `evicted` 中）。
3. **通道分配**：第 `i` 名分配到通道 `i % Channels`。
4. **通道内合并**，由 `Config.Merge` 决定：
   - `MergeHighestPriority`（默认）：通道内只有最高排名者发声，其余被抢占；
   - `MergeMix`：通道内所有效果线性叠加，结果钳制到设备最大振幅；
   - `MergeMax`：取通道内各效果逐点绝对值最大者（保留符号），再钳制。

每次仲裁产生 `Decision` 记录（获胜者、被抢占/参与合并者、策略、原因），可通过 `Snapshot(device)` 查询，保证输出**唯一且可解释**。

## 能力协商与降级

`DeviceCapability` 描述设备的通道数、频率范围、采样率上限与最大振幅。提交时指令被确定性地映射到设备能力：

- 振幅超上限 → 钳制到 `MaxAmplitude`；
- 频率超出 `[MinFrequency, MaxFrequency]` → 映射到最近可表达值；
- 波形采样率超上限 → 线性插值重采样（时长守恒）；
- 波形采样点超量程 → 逐点钳制。

每一次降级都生成 `Degradation` 说明（字段、原值、新值、原因），**不静默丢弃**；能力描述自身不合法或效果完全无法表达时返回 `ErrKindCapabilityMismatch` / `ErrKindInvalidInput`。渲染输出在任何情况下都被钳制在设备量程内，**绝不越界**。

## 时序与延迟补偿

运行时由**逻辑时钟**驱动（`Advance(at)`，必须单调不减，回拨会被拒绝），不读取系统时间：

- 效果生效时刻 = `max(请求时刻, 当前时刻) + LatencyCompensation + 抖动余量`；
- 抖动由 `JitterEstimator` 用 EMA（系数 `JitterAlpha`）估计，只依赖观测序列；
- 时间轴单调：后续调度不得早于已调度的最后时刻，**不倒流**；
- 每条效果的生效窗口 `[start, end)` 唯一且连续，**不会重复触发**；
- 相同事件序列必然渲染出相同时间轴，与负载、系统时间、随机源、map 遍历顺序无关。

## 并发与撤销

所有公开方法可并发调用（内部互斥）。`Revoke` 的语义：

- 效果尚未生效 → 直接移除；
- 效果已生效 → 结束时刻截断到当前逻辑时刻，截断点之后保证无输出。

每台设备状态独立：慢设备（低采样率）不影响其他设备的渲染；任意时刻每台设备上真正生效的集合由当前窗口与仲裁结果唯一决定，自洽且可查询。

## 热插拔

- `AttachDevice(id, caps)`：接入。设备身份与能力登记在注册表中，重复接入返回 `ErrKindDeviceExists`；
- `DetachDevice(id)`：断开。按 `Config.Disconnect` 处理其上进行中的反馈：
  - `DisconnectTerminate`（默认）：立即终止；
  - `DisconnectMigrate`：按 `DeviceID` 字典序尝试迁移到第一个兼容设备（保持原时间窗口），无兼容设备则终止；
- 一个设备的变化不影响其他设备的输出；重新接入的设备状态干净，无旧反馈泄漏。

## 录制与回放

```go
rt.EnableRecording()
// ... 正常操作 ...
events := rt.Recording()          // 事件流（逻辑时间戳）
replayed, _ := haptic.Replay(cfg, events) // 之后原样重放
```

重放只依赖事件流与配置，在相同渲染查询下与原运行**逐帧一致**，用于问题定位。

## 配置项（`Config`，`DefaultConfig()` 给出默认值）

| 字段 | 默认 | 说明 |
|------|------|------|
| `FrameRate` | 1000 Hz | 控制帧率，渲染时间轴的最小粒度 |
| `Merge` | `MergeHighestPriority` | 冲突合并策略 |
| `LatencyCompensation` | 2ms | 固定延迟补偿 |
| `JitterAlpha` | 0.2 | 抖动估计 EMA 系数 (0,1] |
| `Disconnect` | `DisconnectTerminate` | 设备断开策略 |
| `MaxActiveEffects` | 16 | 单设备最大并发效果数 |

零值字段会被自动补齐为默认值；非法配置在 `New` 时返回错误。

## 可观测

- `Snapshot(device)`：当前时刻生效的反馈（来源、通道、起止时刻）与仲裁结论；
- `Render(device, from, to)`：拉取 `[from, to)` 内逐帧输出，是事件历史的纯函数；
- 所有错误为 `*haptic.Error`，`Kind` 可区分原因。

## 测试

```sh
go test ./...          # 单元测试（仲裁、降级、时序、并发、热插拔、回放）
go test ./... -race    # 含竞态检测
```

# Flame DJ Hub：QDC507 通信扩展与 Agent 语音链路实现记录

> 状态：实现与历史调试记录。本文中的历史实测数据不作为当前安装版验收结论；当前交付证据以 README“验证范围”和本文下方“当前验收状态”为准。

当前安装版只读回读已确认模块、SIM、中国电信 LTE 注册和信号状态；`usbnet=0`，Mac 默认出口保持为系统网络。真实短信收发、拨号/接听、非零 PCM、ASR、验证码提取和双方实际可听尚未在本轮完成复验。

## 1. 目标与边界

目标是在不改变 Mac 默认网络出口的前提下，将 QDC507 作为受控通信扩展：

- 短信收发；
- 来电检测、拨号、接听和挂断；
- QDC507 下行 PCM 采集；
- 本地中文 ASR；
- 语音验证码提取；
- 为后续 Agent 双向通话保留 PCM 数据面。

固定约束：

```text
usbnet=0
模块不作为 Mac 蜂窝网卡
不改变 Mac 默认网络出口
PCM16LE / 8000Hz / mono / 20ms / 320 bytes
```

本阶段只验证下行链路，不启用 TTS、自动 DTMF 或验证码自动提交。

## 2. 代码实现与当前验收状态

### 模块与基础通信

- 当前安装版已只读识别 QDC507 USB、AT、SIM、运营商、LTE 注册和信号状态；
- 当前已回读 `usbnet=0`，且默认出口仍为 `en1`；
- 代码包含短信、拨号、接听和挂断能力，但本轮尚未执行真实短信发送或运营商通话验收；
- 网卡识别修复不会自动创建/启用模块网络服务，也不会调整默认路由或网络服务顺序。

### Agent PCM（代码实现，当前未完成真实音频验收）

- Unix Socket 单客户端传输；
- HELLO/ACK、nonce、序列号和会话绑定；
- 固定 8 kHz、单声道、20 ms PCM 分帧；
- 环形缓冲、断线清理和 generation 隔离；
- AC Interface 负责 QDC507 → Mac 采集；
- AS Interface 负责 Mac → QDC507 播放；
- `module_io_ready` 仅在 AC、AS 都启动且产生真实回调后成立；
- 停止、失败、设备失活和挂断会撤销 readiness。

### ASR 与验证码（代码实现，当前未完成真实转写验收）

- 使用 macOS SpeechAnalyzer/SpeechTranscriber；
- CoreAudio 实时线程只投递 PCM，不直接执行阻塞 ASR；
- 转录结果绑定精确 `CaptureSessionID`；
- 缺失或过期 session 的转录上报被拒绝；
- 支持中文口语数字和英文数字提取；
- 转录与验证码摘要最多保留 5 分钟，并由定时器主动清除；
- 原始 PCM 默认只在内存处理，不长期保存 WAV/PCM 文件。

### 当前安装版验收结果

```text
call_state=idle
pcm_transport_available=true
transport_ready=true
active=false
module_io_ready=false
frames_received=0
nonzero_frames=0
asr_available=false
tts_available=false
voice_agent_available=false
```

以上状态只证明 Agent PCM 控制面和传输注册存在，不证明真实通话音频、非零 PCM 或 ASR 已工作。历史调试中记录过的帧数和转写结果需要在当前已提交版本上重新执行受控真实通话后才能成为发布验收证据。

## 3. 问题、原因与修复

### 3.1 AC Interface 间歇性无法启动

现象：

```text
CoreAudio device start failed
0x10004003
MACH_RCV_TIMED_OUT
AC 独立探针超时
AS 仍可启动
```

根因定位：

- 不是 USB 枚举失败；
- 不是 Agent PCM 协议独有问题；
- 独立 CoreAudio 探针同样卡住；
- 属于 macOS CoreAudio/HAL 的 AC 路径失活或资源生命周期问题。

处理：

- 修复 teardown 顺序；
- 禁止模块路由先于原生 AC/AS IOProc 停止；
- 增加安全 teardown 屏障、generation 和有界锁等待；
- 禁止 teardown 期间重新启动路由或 PCM；
- 加入 AC/AS 通话前生命周期预检；
- 不再把每次故障都交给用户临时授权重启 CoreAudio。

### 3.2 人工音频桥抢占 UAC owner

现象：

```text
module_audio_busy_retry
frames_received=0
```

原因：验证码 Agent session 与人工音频桥竞争共享模块音频 owner。

修复：

- 验证码会话保留 Agent 音频 owner；
- 验证码模式不启动普通人工桥；
- 路由、PCM 和恢复都绑定 session/generation。

### 3.3 假 readiness

原问题：仅根据设备枚举或 `AudioDeviceStart` 返回成功就可能报告 ready。

修复：

```text
AS playback 已启动并有真实回调
AND
AC capture 已启动并有真实回调
=> module_io_ready=true
```

任一端停止、失败、断线或设备失活均立即撤销 readiness。

### 3.4 挂断后错误的 teardown 顺序

原顺序可能是：

```text
先停止模块语音路由
→ 原生 AC/AS IOProc 仍占用设备
→ CoreAudio/HAL 卡死
```

修复后：

```text
撤销 Agent PCM
→ 等待 ClientConnected=false
→ 等待 ModuleIOReady=false
→ 确认原生 IOProc 已停止
→ 停止模块语音路由
```

并增加：

- `moduleVoiceTeardownMu`；
- `moduleVoiceTeardownInProgress`；
- `moduleVoiceTransitionGeneration`；
- TryLock + deadline；
- 超时后 fail-closed；
- pending teardown 可重试；
- 同一 session 的恢复耗尽状态不会被错误清除。

### 3.5 自动恢复可能无限重启

原风险：周期 reconcile 可能在恢复失败后每 500 ms 重新启动，形成无限循环。

修复：

```text
首次启动
→ 可恢复错误时只允许一次模块路由 stop/start
→ 只重试一次 Agent PCM
→ 再失败则标记 recovery exhausted
→ 同一 session 禁止继续自动恢复
```

旧恢复任务通过 generation 隔离，不能停止或污染新 session。

### 3.6 转录结果串会话

原风险：旧 ASR 回调可能写入当前新通话状态。

修复：

- 转录上报必须携带 `CaptureSessionID`；
- 仅接受与当前采集会话完全一致的 session；
- 缺失和 stale session 返回冲突错误；
- Swift 转录器在启动前后都校验 generation；
- 归档结果也要求 `TranscriptSessionID == CaptureSessionID`。

### 3.7 敏感结果仅惰性过期

原风险：如果没有状态读取，验证码和转录可能超过 5 分钟仍留在内存。

修复：

- 保存 LastCapture 时创建主动过期定时器；
- 到期主动清除转录、最终标记和验证码；
- 定时器通过记录 identity 防止清除后续新结果。

### 3.8 通话开头约 5～6 秒未采集

三次实测的接通到 PCM 开始间隔约为：

```text
5.27 秒
6.11 秒
5.16 秒
```

原因：

```text
先 ATA 接通
→ 等待 active
→ 启动模块路由
→ 固定 CoreAudio settle
→ 启动 AC/AS
```

优化：

```text
一次性窗口校验来电
→ 预热模块语音路由
→ 再次校验号码、SessionID、CallID 和窗口 generation
→ ATA
→ 接通后立即启动 AC/AS 和 PCM
```

安全加固：

- `disarm/rearm/过期`会使旧 attempt 失效；
- 预热后再次核对来电身份；
- 未知 `CallID` fail-closed；
- 号码比较统一规范化；
- ATA 或终态预热失败后窗口一次性消费，不会循环接听；
- start/recovery/teardown 暂态竞争只延期，不烧掉窗口；
- 路由 ownership 精确绑定 generation；
- 只有创建该路由的 attempt 才能回滚；
- 回滚复用安全 teardown，不能在原生音频仍活动时直接关闭路由；
- stale starter 的真实模块副作用会被识别并安全清理；
- reset 与 teardown 使用不同 generation，避免模块 reset 后误恢复旧路由。

### 3.9 一次性接听窗口竞态

已覆盖以下竞态：

- 预热期间 disarm；
- 预热期间 rearm；
- 预热期间窗口过期；
- 预热期间来电被替换；
- ATA 失败；
- 路由启动部分成功后失败；
- 预存路由不应被旧 attempt 错误拆除；
- 旧 ownership 不得停止已转交给 active session 的路由。

## 4. 当前设计的安全边界

- 必须由用户主动 arm 一次性窗口；
- 窗口有时限；
- 只允许指定号码；
- 最多消费一次；
- 通话结束后自动关闭；
- 未授权来电不得自动 ATA；
- 不自动发送 DTMF；
- 不自动提交验证码；
- 不向对端播放 TTS；
- 原始 PCM 默认不落盘；
- 正式版 App 与开发版不能并行占用同一控制 Socket。

## 5. 自动化验证

当前代码收口要求：

```text
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go vet ./...
go test -race ./cmd/djonehub-macos
git diff --check
Xcode Debug build
新增行敏感信息静态扫描
独立代码复审
```

当前安装版已完成构建、安装、签名、运行时能力回读和真机只读检查；真实来电、非零 PCM、ASR 和验证码提取仍是后续单独授权的验收阶段。

## 6. 当前遗留限制

### 已知限制

1. CoreAudio/HAL AC 路径在历史调试中出现过失活；当前版本尚未重新完成真实音频验收，不能宣称问题已彻底消除。
2. 路由预热会让主叫端多听几秒回铃音，以换取接通后尽快开始采集。
3. ASR 仍可能受播报速度、口音、静音和系统 Speech 资源影响。
4. 本轮验证码识别不是 100%，不能把 ASR 结果用于无人工确认的高风险操作。
5. 尚未验证上行 TTS、双向语音、自动 DTMF 和自动验证码提交。
6. 模块侧 helper 的完整运行观测仍有限，当前主要以 CoreAudio 回调和 PCM 指标判断链路。

### 建议后续单独立项

1. 将模块路由十余个布尔量和 generation 重构为明确有限状态机：

```text
idle → starting → active → stopping
                  ↘ recovering → exhausted
```

2. 把路由、Agent PCM 和验证码窗口统一为 lease/owner controller，明确创建、转交、回滚和销毁责任。
3. 为模块启动、settle 和 teardown 加入 context/cancellation，避免同步硬件操作阻塞自动化 ticker。
4. 为 CLCC/URC fallback 建立模块级不可变 call identity，而不是只依赖应用 session。
5. 增加可观测指标：route owner、transition generation、pending teardown、启动阶段耗时和失败分类。
6. 后续如优化首帧延迟，只做针对“接通后首帧耗时”的定量验收，不重复大规模拨号测试。

## 7. 当前结论

当前已确认 QDC507 在 `usbnet=0`、不改变 Mac 默认网络出口的前提下可完成只读管理和控制面回读。短信收发、通话控制、下行 PCM、本地中文 ASR 和验证码提取闭环必须在当前版本上逐层重新验收后才能对外宣称完成。

当前主要工程成果不是单次演示成功，而是建立了：

- 一次性授权边界；
- 精确 session/generation 隔离；
- AC/AS 真实回调 readiness；
- 安全 teardown；
- 单次有界恢复；
- 敏感结果主动过期；
- 路由 ownership 与 stale transition 防护；
- 自动化并发回归测试。

项目现阶段适合保留为受控开发功能，不应在未补充隐私提示、录音合规说明和更多设备兼容性验证前直接作为无监督生产电话 Agent 使用。

# 通话音频

输入和输出各自提供「跟随系统默认」与可用设备列表。选择按 CoreAudio 设备 UID 保存，模块 UAC 接口和 VoiceProcessingIO 内部设备不会作为用户收听/说话的设备列出；不会写入 macOS 的全局默认输入输出设置。

蜂窝通话进入 active 后，后端只启动一次模块音频路由。Mac 侧设备切换、默认设备变化、设备增删与采样率变化仅重建主机音频管线。所选设备断开时暂停整条主机音频，保留电话和模块路由；设备重连后恢复。暂停整条音频是为了保持回声消除的输入、输出配对，避免耳机断开时意外外放。

## 管线

- 下行：模块 UAC 输入 → 单声道重采样 → AVAudioSourceNode → 同一 AVAudioEngine 的 VoiceProcessingIO 输出。
- 上行：VoiceProcessingIO 的 AEC/AGC 输入 → AVAudioSinkNode → 单声道重采样 → 模块 UAC 输出。
- VoiceProcessingIO 启用后重新取得底层 AudioUnit，分别设置 global bus 1（输入）和 bus 0（输出）的 CurrentDevice。启动后回读两端设备 ID；失败或不匹配时停止音频并报告，不能静默回落到系统默认或无 AEC 的管线。
- 两端客户端格式统一为 48 kHz 单声道 Float32；VoiceProcessingIO 负责主机硬件格式转换，模块端保留 PCM 位深与采样率转换。
- 所有启动、停止与设备通知在专用串行队列处理；音频回调不执行设备重建，争用样本锁时输出静音或丢弃该帧而不阻塞实时线程。每次重建都有独立有界样本队列，旧设备的缓存不会进入新管线。
- 模块输入和输出为同一个全双工设备时只注册一个 IOProc。设备监听、引擎配置观察及健康检查分别在设备移除或管线停止时清理。

实现依据：[Apple AVAudioIONode](https://developer.apple.com/documentation/avfaudio/avaudioionode)、[Apple AVAudioSinkNode](https://developer.apple.com/documentation/avfaudio/avaudiosinknode)、[WebKit CoreAudioCaptureUnit](https://github.com/WebKit/WebKit/blob/main/Source/WebCore/platform/mediastream/cocoa/CoreAudioCaptureUnit.cpp)。最低系统版本保持 macOS 13。

## 验证边界

`mise run audio:probe` 直接验证生产音频核心的 PCM 满幅/异常值、交错声道、分块重采样及停止屏障，并只读枚举设备；不会启动麦克风或修改偏好。

`mise run build` 验证本机架构应用编译，不代表实际双向可听或回声抑制质量。没有 UI 单元测试，也不会为验证而自动拨号、改模块配置或启动实际通话。以下项目需由用户在已有可用通话环境中完成：

| 场景 | 预期 |
| --- | --- |
| 系统默认输出是 AirPods/有线耳机 | 声音从默认设备播放，不固定到内置扬声器 |
| 内置麦克风 + AirPods，或 USB 麦克风 + 内置扬声器 | 实际设备与两项选择一致；若系统拒绝组合，明确显示未连接及重试 |
| 通话中更改默认输入或输出 | 对应设置为“跟随系统默认”时切换，手动指定项保持不变 |
| 手动切换任一设备 | 电话保持接通，音频短暂停顿后恢复，界面显示实际设备 |
| 拔下指定耳机/关闭蓝牙设备 | 音频暂停并提示设备失踪，不外放、不重复启动模块路由 |
| 重新连接指定设备 | UID 匹配后自动恢复；跨进程重启也保留选择 |
| AirPods 切换采样率/通话配置 | 重新建立匹配格式，清空旧缓冲，无持续噪声或无声假运行 |
| 内置扬声器外放、双方交替和同时讲话 | 对方回声可接受；AEC/AGC 状态只有实际启用后才显示 |
| 启动失败后重试，切换中挂断/模块断连 | 明确错误，停止音频、清理注册和观察，下一通不播放旧缓存 |

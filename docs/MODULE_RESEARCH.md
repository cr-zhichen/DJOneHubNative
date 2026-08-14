# 大疆一代 4G 模块研究档案

> 面向 DJI 一代 4G 模块（USB `2ca3:4006`，产品名 Baiwang，固件 QDC507）的硬件识别、AT 指令与语音通话调查结论。本文件用于复用经验，避免重复踩坑。

## 1. 硬件识别

| 项目 | 值 | 说明 |
| --- | --- | --- |
| USB VID:PID | `2ca3:4006` | DJI 定制 ID（出厂状态） |
| 备用 USB ID | `2c7c:0125` | Quectel 默认 ID，配置变更后可能出现 |
| 产品名 | Baiwang | 系统识别名称（Audio 设备为 AC/AS Interface） |
| 固件 | `QDC507GLEFM21` | 供应商定制固件，**不是标准 Quectel 固件** |
| 芯片平台 | Qualcomm **MDM9207** | FCC 拆机确认（FCC ID: 2A2TS2021IG830） |
| 语音链路 | 呼叫控制完整；原厂 AT 音频出口被裁剪，但可通过 ADB 加载运行时恢复 UAC 路由 | 详见第 4 节 |

固件可识别大量 Quectel 风格 AT 指令，`AT+QCFG=?` 完整输出含 usbid/usbcfg/usbnet 等。

## 2. USB ID 恢复手册（重要排错经验）

**事故背景**：执行 `AT+QCFG="usbcfg",0x2C7C,0x0125,...,1`（开启 UAC 时误传 Quectel 默认 VID/PID）会把模块枚举 ID 从 `2ca3:4006` 改为 `2c7c:0125`，导致 DJOneHub 检测不到模块（"未检测到模块"）。

**恢复流程**（需要先能连上 AT 通道）：

1. 让后端兼容备用 ID：`openDJIUSBAT` 按候选列表尝试 `2ca3:4006` 与 `2c7c:0125`（本项目已实现，`usbat_darwin.go` 的 `usbDeviceIDs`）
2. 连上后执行：
   ```
   AT+QCFG="usbcfg",0x2CA3,0x4006,1,1,1,1,1,0,0
   ```
   **必须使用模块自己的 VID/PID（2CA3:4006）**，不要照抄 Quectel 默认值
3. `AT+CFUN=1,1` 重启（必要时物理拔插）
4. 验证 `ioreg`：idVendor=11427(0x2CA3)、idProduct=16390(0x4006)

**教训**：
- `usbcfg` 的 VID/PID 参数会覆盖实际枚举 ID（`usbid` 也可能被重置）
- 修改 usbcfg 前**必须先记录当前值**
- 参考的"原值"不要来自 demo 假数据（demo 返回硬编码 `0x2C7C,0x0125`）

## 3. USB 音频（UAC）实测

### 开启 UAC（保持 DJI ID）
```
AT+QCFG="usbcfg",0x2CA3,0x4006,1,1,1,1,1,0,1
AT+CFUN=1,1
```
- usbcfg 参数顺序：`<vid>,<pid>,<diag>,<nmea>,<at_port>,<modem>,<rmnet>,<adb>,<uac>`
- 修改后通常需要**物理拔插**才重新枚举
- UAC 枚举出的音频设备系统名为 **"AC Interface"（输入 8kHz）与 "AS Interface"（输出 8kHz）**，不是 "Baiwang"！设备发现逻辑必须匹配 "as interface"/"ac interface"

### 实测数据（通话中）
- AC Interface 输入流：8000Hz、1ch、32-bit float（flags=0x9）
- **通话中数据全零**（Float32/Int32/Int16 高低位/字节级五种解析全为 0）→ 语音未路由到 USB

## 4. 语音通话调查结论

### 4.1 可用的（呼叫控制层）
```
ATD+86138XXXXXXXX;   → OK（分号结尾 = 语音呼叫）
ATA                   → 接听
ATH                   → 挂断
AT+CLCC               → 通话状态查询（+CLCC: id,dir,stat,...）
AT+CEER               → 扩展错误（通话正常时返回 +CEER: 0,-1）
AT+QCFG="call_control" → 返回 0,0（MO/MT 均未禁用）
```

### 4.2 被裁剪的（音频出口，全部 ERROR）
| 指令 | 结果 |
| --- | --- |
| `AT+QPCMV=?` | ✅ 返回 `(0,1),(0-2)`（查询接口在） |
| `AT+QPCMV=1,0`（NMEA 原始 PCM 路径） | ❌ ERROR（含通话中、释放 NMEA 后） |
| `AT+QPCMV=1,2`（UAC 路由） | ❌ ERROR（含通话中） |
| `AT+QPCMV` 其他变体（0,0 / 1 / 2 / 1,0,0 / 1,1,0） | ❌ 全部 ERROR |
| `AT+QDAI=?` / `AT+QDAI?` | ❌ ERROR |
| `AT+CPCMREG` / `AT+QPCMREG` | ❌ ERROR |
| `AT+QCFG="pcmclk"` | ❌ ERROR |
| `AT+QCFG="usbaudio"?` | ❌ ERROR（该指令是 EG25-G 平台，本固件不支持） |
| `AT+QAUDCH` / `AT+QAUDMOD` | ❌ ERROR（老 GSM 平台指令/调音指令） |

**当时结论**：固件保留 QPCMV 查询 handler 但**裁剪了写入**；仅依赖公开 AT 指令时，UAC 与 NMEA PCM 两条路径都无法启用。这个结论仍适用于 AT 层，但不再等于“硬件无法传输音频”：后续确认模块可开启 root ADB，并可运行时加载匹配 `3.18.44` 的 QDC507 驱动与 D4/UAC helper，见 4.4。

### 4.3 QMI 检查
- `quectel-qmi-go` 有 VOICE 服务（DialCall/EndCall/AnswerCall/GetAllCallInfo/GetConfig）
- GetConfig 只有通话配置（自动应答/AMR/TTY 等），**无音频路由字段**
- 结论：QMI 不提供音频路由能力

### 4.4 已实现的模块侧运行时路径

参考 MaVo 的实机路径后，DJOneHub 采用无需刷机和硬件改造的运行时方案：

1. 读取模块身份、当前 `USBCFG`、`QCFG="ims"` 与 `QCFG="volte_disable"`，保存带 IMEI 的修改前备份。
2. ADB=0 时查询旧式 `AT+QADBKEY?`。只接受 8 位数字挑战；用户确认持久授权后，在本机派生 15 字符密码并提交，密码不记录、不回传。`QSECCFG="adb_auth"` 或未知挑战格式不会自动降级绕过。
3. 保留模块当前 VID/PID 和其他功能位，只把缺少的 ADB/UAC 位设为 1；确认 `volte_disabled=0` 并设置 `IMS=1`。重启前回读可处于原值与目标值之间，但 VID/PID、未修改功能位和语音能力必须完全符合预期。
4. 重启后核对同一 IMEI、完整 `USBCFG` 与 IMS/VoLTE。ADB 配置存在但不响应时重新确认并提交新挑战值的 QADBKEY；root 探测在 12 秒硬超时的隔离子进程中执行，避免 macOS libusb 同步读取异常拖死主服务。
5. 应用内置最小直接 USB ADB 协议（CNXN、shell、sync push），不要求用户安装 Android platform-tools。
6. 用户确认后，从固定提交下载 `qdc507_aprv3.ko`、`qdc507_voice.ko` 与 `mavo-pcm-bridge.armv7`，逐文件核对大小和 SHA-256。
7. 严格确认 `uname -r = 3.18.44` 后，将文件推送到模块 `/tmp/djonehub-call`；按顺序加载 APRv3 与 voice 驱动，等待 `hw:0,4`、D5/D6 和声卡节点出现。
8. 启动 `alsaucm_test`，应用 VoLTE / Auxpcm Rx / Auxpcm Tx 校准；通话 active 后启动 helper 的 `--voice-route-session`，打开 D4 playback/capture 并设置 `audio_enable=1`。
9. Mac 侧把 AC Interface 下行送到系统默认输出，把系统默认麦克风上行送到 AS Interface，并处理 8 kHz ↔ 主机采样率转换。
10. 挂断时以 PID + 进程启动时间 + argv 校验 helper 所有权，发送 SIGTERM，确认退出后设置 `audio_enable=0` 并写入 T/T/B 回滚路由。APR/voice 内核模块不做热卸载，留到模块重启，避免迟到 DSP 回调竞态。

配置修改与还原共享同一安全边界：写入前保存带 IMEI 的结构化 USB 与 IMS/VoLTE 备份，备份文件可以从通话页导入、导出和删除；导入会限制文件大小并校验 schema、USB 结构与还原命令一致性，只保存到本机，不触发模块写入。自动还原要求备份 IMEI 与当前模块、VID/PID 完全一致。还原操作会先为当前配置再生成一份保护备份，确认无通话、写入且排除意外漂移后才重启，重连后再核对 IMEI 与全部目标值。schema v2 备份只还原 USB；schema v3 同时还原语音配置。更早且缺少模块身份的格式只允许导出。`QADBKEY` 授权是独立的持久安全状态，配置备份只能关闭 ADB 接口，不能承诺重新锁定授权。

这个路径不写 boot、MTD、DIAG 或 EDL，也不需要拆机、焊接或 PCM-to-USB bridge。仍明确禁止刷标准 EG25 固件；已有真实 9008 变砖风险。

### 4.5 当前实机边界（2026-08-14）

- 已连接模块：`2ca3:4006`，固件 QDC507，SIM READY；实测 `USBCFG` 七个功能位均为 1，`IMS=1`、VoLTE capability=1、`volte_disabled=0`。
- 已确认 macOS 枚举 AC Interface 输入与 AS Interface 输出，均为单声道 8 kHz；显式模块语音路由启动与停止均成功。
- 该固件使用旧式 8 位 `AT+QADBKEY?` 挑战。授权密码仅在本机内存中派生；ADB 不响应的恢复流程在重启前后使用当前挑战重新授权，并在隔离进程中验证 root shell。
- QDC507 USB 组合中的 AT/Modem 是 interface 2/3。诊断 interface 0 在重启后可能让 macOS IOUSBLib 同步探测不返回，因此 USB AT 发现只探测 2/3；ADB 按完整 `ff/42/01` 描述符定位。
- GitHub 固定提交的 6 个文件共 1,080,625 bytes，已全部通过固定大小与 SHA-256，并成功部署 APRv3、voice 驱动、ALSA/ACDB 校准与 D4/UAC helper。
- 正常退出 GUI 后，后端子进程确认一并退出；再次启动会重新发现本地运行时、部署并完成路由启停自检，最终恢复 `ready`。
- 尚未提供测试号码或另一部电话，因此运营商侧呼叫建立、下行真实语音和上行双方可听仍需配合一次实际通话验收，不能从设备枚举与路由自检推断。

### 4.6 参考资源
- FCC 拆机照：https://fccid.io/2A2TS2021IG830
- Quectel 论坛固件讨论：https://forums.quectel.com/t/firmware-request-for-eg25g-qdc507/58093
- Quectel 官方社区 ADB 授权顺序：https://forums.quectel.com/t/how-to-enable-adb-interface-for-new-ec25-e-modem/21951
- 旧式 QADBKEY 兼容算法参考：https://github.com/carp4/qadbkey-unlock/tree/cab52a0a7429c8d8b8f31da8894c8c93155c0fc5
- Sparktour 博客（Asterisk + Baiwang）：https://blog.sparktour.me/en/posts/2026/07/04/dji-baiwang-eg25-asterisk-telegram-sms-gateway/
- zkl2333 博客（Windows 不刷机使用）：https://blog.zkl2333.com/posts/dji-4g-windows-no-flash/
- QDC507 固件研究归档：https://github.com/glasses666/dji4g-qdc507-research
- asterisk-chan-quectel：https://github.com/IchthysMaranatha/asterisk-chan-quectel

## 5. 语音音频引擎经验（AudioBridge）

- CoreAudio `AudioDeviceCreateIOProcID` + `AudioDeviceStart` 可对输入/输出设备注册回调（实测有效）
- **必须先读取设备实际格式**（采样率/声道/位深/浮点标志），不能假设 16-bit
- 本模块 UAC 全部是 32-bit float；内置设备 48kHz
- 模块音频是两个独立设备：AC（输入）与 AS（输出）
- 重采样必须跨帧保持状态（线性插值即可，8k↔48k）
- IOProc 对输出设备回调时 `inInputData` 为 nil（不要指望从输出设备读输入）

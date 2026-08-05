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
| 语音链路 | 呼叫控制完整，**音频出口被固件裁剪** | 详见第 4 节 |

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

**结论**：固件保留 QPCMV 查询 handler 但**裁剪了写入**；呼叫控制完整但音频出口（UAC 与 NMEA PCM 两条路径）都被禁用。MDM9207 硬件支持语音，Baiwang 定制固件没有暴露主机音频路由。

### 4.3 QMI 检查
- `quectel-qmi-go` 有 VOICE 服务（DialCall/EndCall/AnswerCall/GetAllCallInfo/GetConfig）
- GetConfig 只有通话配置（自动应答/AMR/TTY 等），**无音频路由字段**
- 结论：QMI 不提供音频路由能力

### 4.4 剩余可能性（未实现）
1. **寻找 `QDC507GLEFM21_01.001.02.001` 固件 donor**（比常见 `01.001.01.xxx` 新）：做只读探测 `AT+QPCMV=?` / `AT+QDAI=?`，若可写则语音可行
2. **拆机扫 PCM 测试点**：MDM9207 平台可能有 PCM/I²S 焊盘（2.048MHz CLK、8kHz SYNC），需示波器（1.8V）与 PCM-to-USB bridge
3. **QMI/CSD 绕过**：需取得模块 shell（不现实）
4. **刷标准 EG25 固件**：**明确禁止**，已有真实 9008 变砖案例

### 4.5 参考资源
- FCC 拆机照：https://fccid.io/2A2TS2021IG830
- Quectel 论坛固件讨论：https://forums.quectel.com/t/firmware-request-for-eg25g-qdc507/58093
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

# 项目架构与开发笔记

## 架构总览

```
DJOneHubNative.app
├── Contents/MacOS/DJOneHubNative      # SwiftUI 前端（进程宿主）
└── Contents/Resources/backend/
    ├── djonehubd                      # Go 后端（从 DJOneHub 移植）
    └── libusb-1.0.0.dylib             # 随包 USB 库（@loader_path 链接）
```

- **前端**：SwiftUI（`app/Sources/`）
- **通信**：Unix domain socket（`~/Library/Application Support/DJOneHubNative/djonehub.sock`），Swift 侧自定义 `URLProtocol` 以 `http+unix://` 协议走 URLSession
- **后端**：Go（`backend/`），监听支持 `unix:/path`（`listenWith`），硬件逻辑（libusb/QMI/eUICC/短信）来自原 DJOneHub
- **构建**：`scripts/build-app.sh`，仅需 CLT（swiftc）+ Go + brew libusb/pkg-config，不依赖完整 Xcode

## 关键源码文件

| 文件 | 职责 |
| --- | --- |
| `app/Sources/BackendProcess.swift` | 子进程管理（自动启动/停止、socket 路径） |
| `app/Sources/DashboardStore.swift` | 首页数据缓存：2s 轮询（状态/流量/通话/短信接管同步） |
| `app/Sources/UnixSocketURLProtocol.swift` | http+unix:// URLProtocol（POSIX socket + HTTP/1.1 解析，支持 Content-Length/chunked） |
| `app/Sources/APIClient.swift` | URLSession 封装；dateEncodingStrategy = .iso8601（Go RFC3339） |
| `app/Sources/AudioBridge.swift` | CoreAudio IOProc 音频桥（含重采样，当前语音不可用但架构保留） |
| `app/Sources/Views/HomeView.swift` | 首页：概览条/设备信息/网络与流量/网卡优先级/短信接管/语音卡片 |
| `app/Sources/Views/SMSView.swift` | 会话式短信（列表+聊天气泡），详情弹窗/删除/回复 |
| `app/Sources/Views/ESIMView.swift` | eSIM Profile 管理 |
| `app/Sources/Views/DiagnosticsView.swift` | 网络诊断（弹窗）+ AT 调试 |
| `backend/cmd/djonehub-macos/usbat_darwin.go` | libusb AT 桥（兼容 2ca3:4006 与 2c7c:0125） |
| `backend/cmd/djonehub-macos/main.go` | HTTP API + 短信归档 + 通话控制 |

## 关键设计决策

### 1. 短信存储模型（三层）
- **SIM 卡（SM）** 与 **模块（ME）** 存储：硬件源头，短信保留在这里（默认）
- **内存缓存**：后端进程内，最多 500 条
- **本机归档**（接管模式开启或发送记录）：`sms-archive.json`，最多 1000 条
- 短信列表 = 内存 + 归档合并去重（按 sender+content+timestamp 的 key）
- 单条删除按 存储+索引（SM/ME）或 sender+content+时间窗口（本机）
- 清空支持多选：SIM / 模块 / 本机（`POST /api/sms/clear`）

### 2. 短信接管模式（`smsAdopt`）
- 开启后：轮询读到的短信持久化到本机归档 → 清空 SM/ME 原始存储
- 状态持久化到 `sms-adopt.json`，**以后端为准**（app 启动拉取真实状态）
- 发送记录（direction=out）**总是**归档，不依赖开关

### 3. 网卡优先级
- `GET /api/network/services`：解析 `networksetup -listnetworkserviceorder`
- `PUT /api/network/services-order`：`osascript do shell script` 提权执行（base64 编码避免引号地狱）
- 模块网卡识别：模块 USB 产品名（Baiwang）匹配 Hardware Port 名
- 前端拖拽排序：onDrag/onDrop + DropDelegate（**不要用 List+onMove，ScrollView 内嵌 List 渲染不可靠**）

### 4. 通话控制（语音音频不可用，控制保留）
- `AT+CLCC` 轮询通话状态（stat: 0=active,2=dialing,3=alerting,4/5=incoming,6=disconnected）
- **无号码的 active 条目视为固件残留**（ATH/CHUP 清不掉），忽略
- 来电横幅（顶部 overlay）+ 拨号/挂断；状态卡住时轮询失败标记 "unknown"

### 5. 服务生命周期
- app 启动自动拉起后端、退出自动停止（无手动开关）
- `BackendProcess.start()` 的 guard 写法注意：`state == .failed("")` 永远不匹配，失败后无法重启——用 `if case .running = state, process?.isRunning == true { return }`

## 踩坑记录

1. **Go 端 dateDecodingStrategy**：Go time.Time 输出 RFC3339Nano（可能带小数秒），Swift 需自定义日期解析（ISO8601 带/不带小数秒都试）
2. **swiftc 编译**：ViewBuilder 里不能直接 for 循环（用 ForEach）；`onChange` 双参数版本需 macOS 14+（目标 13 用单参数）；超长表达式导致编译器 type-check 超时（抽子视图）
3. **@main 命令行程序**：顶层表达式 + semaphore 在 `-parse-as-library` 模式会卡死（测试工具用 main.swift 顶层代码模式）
4. **List 在 ScrollView 内**：macOS 渲染不可靠（空白/塌陷），用 VStack+ForEach 替代
5. **AudioBufferList**：Swift 中 `mBuffers` 不是数组，用 `UnsafeMutableAudioBufferListPointer` 遍历；IOProc 参数是非 Optional 指针（可判 nil 但不能 if let）
6. **语音开关状态**：`AT+QCFG="usbcfg"?` 响应含回显行（`AT+QCFG="usbcfg"?`），解析必须用 LastIndex 或正则；末尾 `\r\nOK` 会污染 Split 结果（正则提取最稳）
7. **模块 UAC 音频设备名**是 "AC/AS Interface" 而非 "Baiwang"

## 构建

```sh
./scripts/build-app.sh
# 产物：dist/DJOneHubNative.app
```

需要：Go 1.26+、pkg-config、libusb（brew）、Command Line Tools（swiftc）。不依赖完整 Xcode。

## 测试

- `app/Tests/APIProbe`：无硬件环境 API 解码验证（服务连通、模型解析、错误路径）
- 真机测试：手动跑 `djonehubd -listen unix:/tmp/test.sock` + curl 验证 AT 通道

# DJOneHubNative

DJOneHub（大疆第一代 4G 模块管理工具）的原生 macOS 重制版：SwiftUI 原生窗口界面 + Go 后端子进程，通过 Unix domain socket 通信，支持短信收发、eSIM Profile 管理、网络诊断与 AT 调试。

由 [ZenGeekLabs/DJOneHub](https://github.com/ZenGeekLabs/DJOneHub)（源自 [iniwex5/vohive](https://github.com/iniwex5/vohive)）改造而来：Web 套壳界面替换为原生 SwiftUI，后端 Go 逻辑保留自上游。

## 接入准备

**硬件**

- 大疆第一代 4G 模块
- 可正常使用的实体 SIM，或与当前实现兼容的实体 eUICC/eSIM 卡片
- 支持数据传输的 USB-C 线缆
- Apple Silicon 或 Intel Mac

模块的 USB 设备标识通常为 `2ca3:4006`。连接后若 macOS 完全识别不到 USB 设备，请优先确认线缆支持数据传输。

**指示灯**

| 状态 | 常见含义 |
| --- | --- |
| 红色常亮 | 未插入 SIM 卡 |
| 红色闪烁 | SIM 卡未被正常识别 |
| 绿色常亮 | SIM 已识别，蜂窝信号通常较好 |
| 绿色闪烁 | SIM 已识别，蜂窝信号可能较弱或仍在注册 |

## 构建

```sh
mise install                # 按 mise.toml 安装固定版本的 Go（1.26.3）
mise run build              # 构建 .app（本机架构）
mise run build:universal    # 构建通用包（arm64 + x86_64）
mise run build:arm64 / build:x86_64
mise run launch             # 启动已构建的 app
mise run clean              # 清理 build/ 与 dist/
mise run backend:test / backend:vet / backend:tidy
```

需要：`mise`、`pkg-config`、`libusb`（brew）、Command Line Tools（swiftc）。不依赖完整 Xcode。发行版同时提供 universal / arm64 / x86_64 三种包。

## 运行

```sh
open dist/DJOneHubNative.app
```

- 首页可选择开启/关闭后端服务，服务随 app 退出而停止
- 日志：`~/Library/Application Support/DJOneHubNative/djonehub.log`

## 状态

- [x] 首页服务开关（开启 / 关闭 / 状态与错误提示）
- [x] 短信收发页（列表 / 刷新 / 发送 / 验证码标记 / 清空模块旧短信）
- [x] eSIM Profile 管理页（卡片信息 / Profile 列表 / 下载 / 启用 / 改名 / 删除 / 号码资料）
- [x] 网络与流量页（实时流量 / 模式切换 / 4G 出口与代理检查 / 诊断 / 重启模块）
- [x] AT 调试页（快捷命令 / 命令输入 / 响应历史）
- [ ] 菜单栏图标与登录自启
- [ ] 签名与公证

API 模型与端点已通过无硬件环境自动验证（`app/Tests/APIProbe`）；完整功能需接真机验证。

## 文档

- `docs/MODULE_RESEARCH.md`：大疆一代 4G 模块研究档案（硬件识别、USB ID 恢复手册、语音通话调查结论与 AT 指令清单）
- `docs/ARCHITECTURE.md`：项目架构与开发笔记（关键设计决策、踩坑记录）

## 许可证

继承自上游：PolyForm Noncommercial License 1.0.0（仅限非商业用途），必须保留上游声明 `Copyright iniwex5 (https://github.com/iniwex5/vohive)`。详见 LICENSE 与 THIRD_PARTY_NOTICES.md。

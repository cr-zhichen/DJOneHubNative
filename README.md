<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/AppIcon-dark.png">
    <img alt="DJOneHub" src="docs/AppIcon-light.png" width="128">
  </picture>
</p>

<h1 align="center">DJOneHub</h1>

<p align="center">
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/License-PolyForm%20Noncommercial-orange"></a>
  <img alt="macOS" src="https://img.shields.io/badge/macOS-13.0%2B-black?logo=apple&logoColor=white">
  <img alt="SwiftUI" src="https://img.shields.io/badge/SwiftUI-5-orange?logo=swift&logoColor=white">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.26.3-blue?logo=go&logoColor=white">
  <a href="https://github.com/cr-zhichen/DJOneHubNative/releases"><img alt="Release" src="https://img.shields.io/github/v/release/cr-zhichen/DJOneHubNative"></a>
</p>

## 简介

DJOneHub 是大疆第一代 4G 模块管理工具的原生 macOS 重制版。SwiftUI 前端与 Go 后端通过 Unix domain socket 通信，在一个应用中完成模块状态查看、短信与 eSIM 管理、网络分流、来电提醒和调试诊断。

项目由 [ZenGeekLabs/DJOneHub](https://github.com/ZenGeekLabs/DJOneHub)（源自 [iniwex5/vohive](https://github.com/iniwex5/vohive)）改造而来，保留上游 Go 核心逻辑，并以原生 SwiftUI 界面替代 Web 套壳。

## 功能

| 模块 | 功能 |
| --- | --- |
| 首页与网络 | 模块、SIM、信号和设备信息；本次与累计流量；USB 网卡开关、网络服务排序、4G 默认出口检查和模块重启 |
| 短信 | 会话收发与回复、单条删除、验证码标记；SIM / 模块存储扫描与选择性清理；短信接管、本机归档和系统通知 |
| eSIM | 实体 SIM / eUICC 识别、卡片与 Profile 信息；Profile 下载、启用、改名和删除；号码资料与模块通讯录检测 |
| 语音与来电 | 语音状态开关、自定义来电卡片、铃声选择与试听、通话状态与挂断、通话记录管理 |
| 应用分流 | 独立分流支持默认及分应用选择 4G 直连、系统直连或系统 SOCKS5；包含 SOCKS5 握手与认证检测、运行预检、TUN 冲突检测和权限服务管理 |
| Clash 代管 | 提供本地 4G SOCKS5 出口，可配置监听端口并复制 Clash 配置 |
| 应用与菜单栏 | 开机自启、静默启动、关闭窗口后后台运行；菜单栏可显示信号强度与实时上下行速率，并可打开主界面或退出应用 |
| 调试与更新 | 网络诊断、AT 指令、通知模拟和窗口兼容模式；正式版 / 测试版渠道、手动或自动更新检查及版本跳过 |

## 界面预览

| 首页 | 关于与更新 |
| --- | --- |
| ![首页](docs/screenshot-home.png) | ![关于与更新](docs/screenshot-about.png) |

## 接入准备

- 大疆第一代 4G 模块
- 可正常使用的实体 SIM，或与当前实现兼容的实体 eUICC / eSIM 卡片
- 支持数据传输的 USB-C 线缆
- Apple Silicon 或 Intel Mac，macOS 13.0 及以上

模块的 USB 设备标识通常为 `2ca3:4006`。连接后若 macOS 完全识别不到设备，请先确认线缆支持数据传输。

| 指示灯 | 常见含义 |
| --- | --- |
| 红色常亮 | 未插入 SIM 卡 |
| 红色闪烁 | SIM 卡未被正常识别 |
| 绿色常亮 | SIM 已识别，蜂窝信号通常较好 |
| 绿色闪烁 | SIM 已识别，蜂窝信号可能较弱或仍在注册 |

## 安装与运行

从 [GitHub Releases](https://github.com/cr-zhichen/DJOneHubNative/releases) 下载适合当前架构的 DMG，将 App 拖入“应用程序”文件夹即可。发行版采用 ad-hoc 签名且未公证；若首次启动提示“无法验证开发者”，请先尝试打开一次，再前往“系统设置 → 隐私与安全性”点击“仍要打开”。

应用启动时会自动启动后端服务；关闭主窗口后仍在菜单栏运行，通过“退出 DJOneHub”才会停止服务。应用分流默认关闭，独立分流首次启用或更新权限服务时需要管理员授权。

日志位于 `~/Library/Application Support/DJOneHubNative/djonehub.log`。

## 构建

```sh
mise install                # 安装 mise.toml 固定的 Go 版本
mise run build              # 构建当前架构的 .app
mise run build:universal    # 构建 arm64 + x86_64 通用包
mise run launch             # 启动已构建的 app
mise run backend:test       # 运行后端测试
mise run clean              # 清理 build/ 与 dist/
```

需要 `mise`、Xcode 26 或更新版本、`pkg-config`、`libusb`、`git`，以及首次构建时可访问 GitHub 的网络。构建脚本会从固定提交编译独立的 sing-box 网络核心；发行版提供 universal、arm64 和 x86_64 三种 DMG。

## 验证范围

项目目前主要在 macOS 26.5.2、Apple M5 Pro、Xcode 26.6 和 Go 1.26.3 环境下开发与测试。最低部署目标为 macOS 13，但 macOS 13–25 尚未完成同等范围的真实设备验证。

API 模型与端点已通过无硬件环境自动验证（`app/Tests/APIProbe`）；短信、eSIM 写入、蜂窝链路和独立分流等完整功能仍需对应硬件与网络环境验证。当前模块固件不支持通话音频传输，应用仅提供来电提醒、状态查看、挂断和通话记录。

## 文档

- [`docs/MODULE_RESEARCH.md`](docs/MODULE_RESEARCH.md)：模块识别、USB ID 恢复、语音调查与 AT 指令
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)：架构、关键设计决策与开发记录

## 许可证

本项目继承上游的 PolyForm Noncommercial License 1.0.0，仅限非商业用途，并保留上游声明 `Copyright iniwex5 (https://github.com/iniwex5/vohive)`。详见 [LICENSE](LICENSE) 与 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

## 社区

本项目的开发契机源于 [LINUX DO](https://linux.do/) 社区。在社区中了解到大疆第一代 4G 模块后，开始进行相关研究并开发 DJOneHub 的原生 macOS 版本。

作者社区主页：[zgccrui](https://linux.do/u/zgccrui)

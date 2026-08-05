<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/AppIcon-dark.png">
    <img alt="DJOneHub Native" src="docs/AppIcon-light.png" width="128">
  </picture>
</p>

<h1 align="center">DJOneHub Native</h1>

<p align="center">
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/License-PolyForm%20Noncommercial-orange"></a>
  <img alt="macOS" src="https://img.shields.io/badge/macOS-13.0%2B-black?logo=apple&logoColor=white">
  <img alt="SwiftUI" src="https://img.shields.io/badge/SwiftUI-5-orange?logo=swift&logoColor=white">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.26.3-blue?logo=go&logoColor=white">
  <a href="https://github.com/cr-zhichen/DJOneHubNative/releases"><img alt="Release" src="https://img.shields.io/github/v/release/cr-zhichen/DJOneHubNative"></a>
</p>

## 简介

DJOneHub（大疆第一代 4G 模块管理工具）的原生 macOS 重制版：SwiftUI 原生窗口界面 + Go 后端子进程，通过 Unix domain socket 通信，支持短信收发、eSIM Profile 管理、网络诊断与 AT 调试。

由 [ZenGeekLabs/DJOneHub](https://github.com/ZenGeekLabs/DJOneHub)（源自 [iniwex5/vohive](https://github.com/iniwex5/vohive)）改造而来：Web 套壳界面替换为原生 SwiftUI，后端 Go 逻辑保留自上游。

## 功能特性

- 首页总览：模块状态、设备信息、实时流量、网卡优先级、短信保存与语音通话状态
- 短信收发：会话列表 / 新短信系统通知 / 验证码标记 / 模块旧短信清理
- eSIM Profile 管理：卡片信息 / Profile 列表 / 下载 / 启用 / 改名 / 删除 / 号码资料
- 网络与流量：实时流量 / 网卡模式切换 / 4G 出口与代理检查 / 模块重启
- 调试与诊断：网络诊断详情 / AT 指令调试 / 通知调试（分页组织）

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

需要：`mise`、Xcode（26 或更新）、`pkg-config`、`libusb`（brew）。发行版同时提供 universal / arm64 / x86_64 三种 DMG。

## 运行

```sh
open dist/DJOneHubNative.app
```

- 首页可选择开启/关闭后端服务，服务随 app 退出而停止
- 日志：`~/Library/Application Support/DJOneHubNative/djonehub.log`

### 首次启动（发行版）

发行版为 ad-hoc 签名（无公证），从 GitHub Releases 下载 **DMG**，双击挂载后将 App 拖入"应用程序"文件夹。首次启动时 macOS 可能提示"无法验证开发者"。如遇提示：

1. 尝试打开一次应用
2. 打开"系统设置"
3. 进入"隐私与安全性"
4. 点击"仍要打开"

之后即可正常使用。从源码本地构建（见上方"构建"）则不会触发该提示。

## 状态

- [x] 首页服务开关（开启 / 关闭 / 状态与错误提示）
- [x] 短信收发页（列表 / 刷新 / 发送 / 验证码标记 / 清空模块旧短信）
- [x] eSIM Profile 管理页（卡片信息 / Profile 列表 / 下载 / 启用 / 改名 / 删除 / 号码资料）
- [x] 网络与流量页（实时流量 / 模式切换 / 4G 出口与代理检查 / 诊断 / 重启模块）
- [x] AT 调试页（快捷命令 / 命令输入 / 响应历史）
- [ ] 语音通话的系统通知（来电 / 挂断时弹出系统通知）
- [ ] 分应用网络代理（按应用选择走模块网络）
- [ ] 软件开机自启与后台运行
- [ ] 菜单栏显示网络信号与实时流量
- [ ] 签名与公证

API 模型与端点已通过无硬件环境自动验证（`app/Tests/APIProbe`）；完整功能需接真机验证。

## 文档

- `docs/MODULE_RESEARCH.md`：大疆一代 4G 模块研究档案（硬件识别、USB ID 恢复手册、语音通话调查结论与 AT 指令清单）
- `docs/ARCHITECTURE.md`：项目架构与开发笔记（关键设计决策、踩坑记录）

## 许可证

继承自上游：PolyForm Noncommercial License 1.0.0（仅限非商业用途），必须保留上游声明 `Copyright iniwex5 (https://github.com/iniwex5/vohive)`。详见 LICENSE 与 THIRD_PARTY_NOTICES.md。

# Product

<!-- impeccable:product-schema 1 -->

## Platform

adaptive

## Users

DJI One 4G 模块的 macOS 用户。他们希望在 Mac 上集中查看模块状态、管理短信与 eSIM、控制网络出口，并直接使用模块的蜂窝语音能力，而不依赖额外的 Android 设备或手工命令。

## Product Purpose

DJOneHub 是 DJI One 4G 模块的原生 macOS 管理应用。成功意味着用户可以从一个常驻应用中安全地完成模块日常管理，并在明确知情与确认后启用通话模式、准备模块侧语音运行时、拨打和接听电话。

## Positioning

应用通过本地 Go 后端、直接 USB AT/ADB 通道和 macOS 原生音频设备完成端到端模块管理；通话运行时按需下载并部署到模块，不随应用安装包捆绑。

## Operating Context

- 应用以 macOS 菜单栏常驻程序运行，并提供侧边栏主窗口。
- 模块通过 USB 连接；模块重启或 USB 组合切换会导致短暂重新枚举和网络中断。
- 通话模式需要模块同时暴露可响应的 root ADB 与 UAC 接口、启用 IMS/VoLTE，并需要兼容模块固件内核的语音运行时。
- 运行时来自 GitHub 固定提交；中国大陆网络环境需要原始下载与转发下载两种渠道。
- 运行时保存于用户的 `~/Library/Application Support/DJOneHubNative`，并在每次使用前进行完整性校验。

## Capabilities and Constraints

- 已有模块状态、短信、eSIM、网络、AT 诊断、拨号/接听/挂断、来电提示和通话记录能力。
- 通话页面是侧边栏一级能力，负责首次启用、下载进度、环境诊断、拨号与通话状态。
- 开启或恢复通话模式可能先通过 `QADBKEY` 持久授权模块 ADB，再修改 USB/IMS 配置并重启模块；ADB 授权与可还原的模块配置是不同安全状态，必须分别解释后获得明确确认。
- USB 组合修改必须保留模块当前 VID/PID 和所有无关功能位，只按需开启 ADB 与 UAC；IMS/VoLTE 也必须独立回读，并与 USB 配置一起保存为可审计原值。
- `QADBKEY` 密码只允许在本机内存中生成和使用，不上传、不写日志、不进入状态响应或备份；未知挑战格式和未知授权机制保守停止。
- 用户必须可以导出、重新导入和删除本机保存的 USB 与 IMS/VoLTE 配置备份；导入只保存经过格式与一致性校验的 JSON，不立即写入模块。自动还原只允许写回同一 IMEI 与 VID/PID 的模块，并在每次还原前再次备份当前配置。旧版 USB-only 备份不得暗示会恢复语音配置。
- 下载必须使用固定版本、HTTPS、临时文件、大小限制和 SHA-256 校验；任何来源的内容都必须通过同一校验。
- 模块侧内核模块加载后不进行热卸载；退出通话模式只停止语音路由，完整卸载通过模块重启完成。
- 不捆绑或镜像第三方运行时；应用只提供用户发起的下载与部署流程。第三方转发渠道不视为信任来源，校验值才是信任边界。
- 真实蜂窝通话验证需要可用 SIM、运营商语音/VoLTE 服务和另一部电话配合，不能由自动化测试单独证明。

## Brand Commitments

- 产品名称保持 `DJOneHub`，界面以简洁、直接的中文为主。
- 延续当前原生 macOS 信息架构、系统字体、系统颜色和系统控件，不引入独立网页式视觉语言。

## Evidence on Hand

- 当前产品与导航：`app/Sources/Views/ContentView.swift`
- 当前模块控制和通话 API：`backend/cmd/djonehub-macos/main.go`
- 当前主机音频桥：`app/Sources/AudioBridge.swift`
- 当前模块研究记录：`docs/MODULE_RESEARCH.md`
- 当前第三方软件披露方式：`THIRD_PARTY_NOTICES.md`
- 参考实现：`rogerbush007-a11y/DJOneHub-mac-enhanced`
- 模块侧语音运行时来源：`moluncn/mavo` 的固定提交 `0443dfdaf8aec086fd76ba2ee9152fd908114524`
- 没有可用于宣传的通话成功率、兼容机型范围或运营商覆盖数据；后续界面与文档不得虚构这些证据。

## Product Principles

1. 模块配置可追溯：读取身份、保存原值、显式授权、最小修改、重启前排除意外漂移、重启后完整验证。
2. 高影响动作先解释再执行，下载来源与持久化位置对用户透明。
3. 网络来源可以替换，内容身份不能替换；固定版本和哈希校验始终生效。
4. 状态未知时保守停止，不在不确定的 ADB、内核或音频状态下发起通话。
5. 首页保持总览，复杂能力使用独立页面承载完整诊断与恢复路径。

## Accessibility & Inclusion

使用 macOS 原生控件、可见文本标签、键盘可达操作和 VoiceOver 描述；状态不能只依赖颜色表达。下载、重启和失败恢复文案使用可理解的中文，并允许用户复制诊断信息。

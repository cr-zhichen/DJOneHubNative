# DJOneHub Interface Design

## Product character

DJOneHub is a focused macOS utility for operating a connected 4G module. The interface should feel native, compact, and operationally trustworthy: important state is visible, dangerous ambiguity is avoided, and the app does not imitate a consumer VPN client.

## Native visual system

- Use SwiftUI and AppKit semantic colors so light mode, dark mode, increased contrast, and accent-color preferences remain correct. Do not replace semantic colors with fixed hex values.
- Use the macOS system typeface and standard SwiftUI text roles. Page titles use `title2.bold`, panel titles use `headline`, body controls use `body` or `callout`, and supporting copy uses `caption`.
- Use an 18-point page inset, 14-point spacing between page sections, and 14-point panel padding.
- Panels use a 10-point corner radius, `controlBackgroundColor`, and a 1-point `separatorColor` outline. Avoid decorative shadows and nested card stacks.
- Prefer native controls (`NavigationSplitView`, sidebar `List`, segmented `Picker`, `Toggle`, bordered buttons, and standard text fields) over custom replicas.
- Use semantic color on status icons; keep error and warning text in the primary foreground color for readable contrast. Secondary color is reserved for explanatory copy.

## Information architecture

- Major capabilities live as dedicated sidebar pages. Application routing is owned by the `应用分流` page and is not embedded into the dashboard or module settings.
- A page begins with one title, a short scope statement, and its primary runtime control. Configuration follows in bordered panels.
- Runtime status, readiness, conflicts, and saved-state feedback stay near the mode selector and enable control.

## Application routing interaction contract

- Routing is off after every DJOneHub launch. Configuration persists; enabled runtime state does not.
- Independent routing installs its privileged TUN service on first enable. Ordinary Stop and app exit stop the TUN but keep that service installed, so later enables do not request authorization again.
- A secondary destructive `卸载…` action appears only when the TUN service is installed. It confirms intent, stops an active independent session first, removes all privileged components, and preserves routing configuration; the next enable requests administrator authorization again.
- Independent routing exposes one explicit default exit. Per-application rules override it; existing version 1 configurations migrate to `系统直连` without dropping application rules.
- The two modes are mutually exclusive:
  - `独立分流` owns the single TUN and application rules. Its default exit can be `系统直连`, `4G 直连`, or `系统侧 SOCKS`; per-application rules remain explicit overrides.
  - Independent routing uses the gVisor TUN stack on macOS for both TCP and UDP. This avoids the mixed stack's system-TCP NAT path, which can drop IPv4 TCP before an outbound rule is selected.
  - `4G 直连` is dual-stack. The generated core configuration binds IPv4 and any available global IPv6 source address to the module interface; DNS from module-routed applications is handled by the module gateway so unsupported record types on the system resolver cannot stall Chromium. Preflight reports when the module has no usable IPv6 address or scoped default route.
  - The OpenClash/Clash Fake-IP benchmark range `198.18.0.0/15` bypasses the DJOneHub TUN and remains owned by the system network. This preserves an upstream router's transparent proxy mapping and avoids re-encapsulating unlisted applications' QUIC traffic.
  - A loopback `系统侧 SOCKS` listener is resolved to its owning process before start whenever that process would otherwise re-enter the TUN. The process is routed through `系统直连`; an unbound port or unresolved process blocks start instead of creating a proxy loop.
  - `Clash 代管` creates no DJOneHub TUN. It exposes a loopback SOCKS5 endpoint bound outward to the 4G interface, while Clash owns application selection and TUN routing.
- Editing, saving, and enabling are separate states. Any edit invalidates the previous preflight result. The user must explicitly save, obtain a current successful preflight, and then enable.
- Configuration controls lock while saving, switching state, or running. A running session must always retain a usable Stop control, even if the packaged network core later becomes unavailable.
- Loading and loading failure are explicit page states. A failed load never exposes fabricated editable defaults and always offers Retry.
- Independent mode fails closed: a conflicting broad-capture TUN blocks startup, and failed 4G or SOCKS routes do not silently fall back to the system network.

## Call mode interaction contract

- 通话是侧边栏一级页面，也是一张持续存在的桌面工作台。首次准备、日常拨号、当前通话和恢复操作共用同一页面，不使用完成后消失的向导，也不把拨号、准备、记录和备份拆成同权重卡片。
- 主通话面保持为一个连续的有边框操作面。空闲时先显示号码输入，再以最近通话为主要日常区域、以紧凑的 3×4 矩形拨号盘为辅助输入，并且只保留一个醒目的拨打动作；最近记录可从行内直接回拨，完整记录另行展开。拨号、来电或通话开始后，当前通话状态在同一操作面内原位替换空闲工作台，右侧运行证据不随之移动。
- 宽窗口使用两条独立纵向轨道：可伸展的左轨依次放置主通话面与来电铃声，约 340 点的右轨依次放置完整运行检查器与模块配置备份。这样不同高度的次级内容不会被同一横向行强制配对。默认与窄窗口改为单列，不再把主通话面和检查器压缩成狭窄双栏；准备尚未完成时检查器在前，进入日常就绪状态后主通话面在前。只有最窄宽度把最近通话与拨号盘也改为纵向排列。
- 运行检查器只保留用于判断通话模式是否就绪的 `模块接口`、`运行时下载`、`模块部署` 三个稳定步骤。实际通话音频只在当前通话面显示，不作为准备阶段的等待项。完整检查器显示每步说明；紧凑检查器只压缩说明，不删除步骤名称、文字状态、当前准备动作或必要的下载与错误详情。等待用户确认的步骤使用静态 `待操作` 状态，只有已经发起的 USB 写入、重启、下载、部署或还原任务才显示不确定进度；状态变化只更新对应行，避免用户在模块重启与 USB 重新枚举期间失去上下文。
- 来电铃声和模块配置备份是常驻次级选项，不进入主通话面，也不使用 disclosure 或抽屉。宽屏中铃声跟随日常拨号轨道，备份跟随模块检查轨道；单列中二者位于主任务之后。铃声选择、试听、备份状态和刷新入口直接可见；备份仍默认只展示最近三项，避免与拨号争夺视觉层级。
- ADB 未授权或配置位已开但 daemon 不响应时，确认对话必须把 `QADBKEY` 持久授权与可还原的 USB 接口开关分开说明：密码只在本机计算且不保存，备份可以关闭接口但不能保证撤销授权。随后才说明会保留 VID/PID 与无关功能位、重启模块并短暂中断网络；只有用户明确确认后才执行。
- 模块接口行应连续显示 `等待授权`、`正在授权 ADB`、`正在开启接口`、`模块重启中`、`正在验证` 和最终结果。授权失败、配置漂移、重连超时与 ADB root 不可用必须使用不同文案并给出恢复动作。
- 配置备份列表只显示 IMEI 尾号、保存原因、时间、ADB/UAC 与 IMS/VoLTE 摘要；完整 IMEI 只存在于导出的 JSON 中。schema v2 备份明确标记为仅还原 USB，schema v3 同时还原语音配置。用户可以导入 DJOneHub 导出的 JSON；导入时先校验格式、大小和结构化配置一致性，只保存到本机，不立即修改模块。每份本地备份都可以在明确确认后删除。
- `导出…` 是普通文件操作；`还原…` 仅在备份与当前模块身份匹配、通话空闲且没有其他模块操作时启用。还原确认必须说明会先生成保护备份、写入回读、重启和短暂断网。
- 运行时不随应用分发。模块接口准备完成后才显示并自动提示下载；原始 GitHub 与国内转发渠道是并列选择，固定提交、文件大小和 SHA-256 校验规则完全相同。
- 下载进度、固定版本、本地路径和上游版本保持可见。文件只有完整通过校验后才原子保存，校验失败必须成为可复制、可重试的错误状态。
- `已就绪` 只用于真实 ADB root、UAC、IMS/VoLTE、本地运行时、模块部署和 D4/UAC 启停自检全部通过的状态。拨号与接听在此之前保持禁用，不能用配置位或乐观状态绕过准备过程。
- 通话状态为 `unknown` 时，主通话面必须原位显示锁定说明与重新同步动作；在刷新得到明确状态前，不允许新拨号、行内回拨或接听，也不把未知状态解释为空闲。运行检查器在此期间继续可见。
- 蜂窝通话进入 active 后才启动模块 D4/UAC 路由和 Mac 音频桥；挂断、启动失败和应用退出都应停止自有 helper 并回滚音频路由。
- 使用系统语义色、SF Symbols 与文字共同表达状态。错误必须同时说明问题和恢复动作，不能只靠红色或图标。

## Accessibility and copy

- Never communicate health or failure by color alone; pair status color with text.
- Decorative symbols are hidden from accessibility, while icon-only actions receive contextual labels.
- Keep routing names literal and consistent: `4G 直连`, `系统直连`, `系统侧 SOCKS`, `独立分流`, and `Clash 代管`.
- Explain ownership and traffic path in plain language. Avoid marketing language such as acceleration, protection, or global proxying.

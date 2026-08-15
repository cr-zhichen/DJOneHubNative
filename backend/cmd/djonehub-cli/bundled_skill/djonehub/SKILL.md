---
name: djonehub
description: 通过 DJOneHub macOS CLI 安全检查蜂窝模块，读取或发送短信，查看或控制通话，读取网络状态，以及查看或切换已安装的 eSIM 配置文件。用户请求涉及 DJOneHub、SIM 卡、短信、蜂窝通话、eSIM 配置文件或模块网络状态时使用。
---

# DJOneHub

仅使用 `djonehub` CLI。原生应用会强制校验所有权限；此 Skill 本身不授予权限。

## 每次任务开始时

1. 运行 `djonehub capabilities --format json`。
2. 能力未授权时，运行 `djonehub permissions show --format json`。
3. 使用不熟悉的命令前，运行 `djonehub schema "<command>" --format json`。
4. 将标准输出按 JSON 或 NDJSON 解析。不要解析自然语言文本、抓取 App 界面、读取访问令牌或直接调用 Unix 套接字。
5. 检查顶层 `warnings`。出现 `cli_sync_required` 时，提醒用户前往 DJOneHub → AI 与 CLI 同步 CLI；同步前不要执行会改变状态的命令。

如果缺少 CLI，请让用户前往 DJOneHub → AI 与 CLI 安装。如果 AI 访问已关闭或权限范围被拒绝，请说明 `required_scope`，并让用户在该页面授权。不得绕过拒绝结果。

## 读取操作

- 设备与网络：运行 `djonehub device status`、`djonehub network status` 或 `djonehub network traffic`。
- 短信：先运行 `djonehub sms list --limit 20`，再通过 `djonehub sms get --id <stable-id>` 读取准确正文。不要根据元数据推测内容。
- 通话：运行 `djonehub call status` 或 `djonehub call history --limit 20`。仅在用户要求持续监控时使用 `djonehub call events --follow --format ndjson`。
- eSIM：运行 `djonehub esim list`；切换期间持续运行 `djonehub esim operation` 核对状态，直到操作结束。

只返回用户需要的数据。短信正文、电话号码、ICCID、IMSI 和 IMEI 均为敏感信息，不要无必要地重复展示。

## 执行操作

仅在用户明确要求该具体操作，或已获批准的流程确实需要时执行。

1. 先读取相关的当前状态。
2. 为同一次逻辑操作选择并保留一个稳定的 `--request-id`。
3. 使用相同命令加 `--dry-run` 运行，并检查返回的 JSON。
4. 根据用户请求核对收件人、号码、短信内容或目标 ICCID。
5. 使用相同参数和相同 `--request-id` 执行；要求确认的命令需加 `--yes`。
6. 再次读取状态，并报告实际观察到的结果。

示例：

- `djonehub sms send --to <number> --message-file <path> --request-id <id> --dry-run`
- `djonehub call dial --number <number> --request-id <id> --dry-run`
- `djonehub call answer --request-id <id> --dry-run`
- `djonehub call hangup --request-id <id> --dry-run`
- `djonehub esim switch --iccid <iccid> --request-id <id> --dry-run`

遇到超时或结果不确定时，不得创建新的请求 ID 后盲目重试。使用同一个 ID 重试一次，然后通过短信列表、通话状态/记录或 eSIM 操作状态进行核对。挂断属于安全退出操作，不要求 `--yes`；获得授权后应始终保留该能力。

## 安全边界

- 不得通过其他客户端使用原始 AT 命令、恢复通话模式、重启模块、修改 USB 模式、下载或删除 eSIM，以及管理应用分流。
- 不得呼叫紧急号码或向其发送短信。没有用户当次明确确认时，不得拨打付费号码或短号码。
- 通话进行中或已有其他 eSIM 操作时，不得切换 eSIM。
- DJOneHub 当前不提供实时 AI 语音流。不得声称能通过 SIM 通话与对方实时交谈；CLI 目前只能控制通话状态。

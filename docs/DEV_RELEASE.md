# Flame DJ Hub 开发版构建与发布

## 固定身份

- 显示名：`Flame DJ Hub`
- Bundle ID：`cn.flameapi.flamedjhub.dev`
- 本机签名：`DJOneHub Dev Local Signing`
- 安装位置：`~/Applications/Flame DJ Hub.app`
- 运行目录：`~/Library/Application Support/FlameDJHub`
- 后端 Socket：`flame-dj-hub.sock`
- Agent 音频 Socket：`agent-audio.sock`

旧 `DJONEHUB_*` 环境变量和 `djonehub` CLI 暂时保留兼容。旧目录 `DJOneHubNative` 只作为复制迁移源，不删除、不覆盖新目录已有文件，也不迁移 Socket、PID、日志、临时文件和符号链接。

## 标准流程

```bash
scripts/test-dev.sh
scripts/build-dev.sh
scripts/install-dev.sh
scripts/verify-dev.sh --runtime
```

默认构建产物位于：

```text
~/Library/Developer/FlameDJHubDev/Artifacts/Flame DJ Hub.app
```

构建使用独立 DerivedData，不覆盖当前安装版本。可以用环境变量覆盖：

```bash
FLAME_CONFIGURATION=Release \
FLAME_BUILD_ROOT=/path/to/build-root \
scripts/build-dev.sh
```

## 安装安全边界

`scripts/install-dev.sh` 会按顺序：

1. 严格验证待安装 App 的签名、Bundle ID、显示名及嵌入后端；
2. 通过当前 `djonehub` CLI 读取通话状态，非 `idle` 立即拒绝；
3. 将新 App 复制到同文件系统 staging 路径并再次验证；
4. 只停止 `cn.flameapi.flamedjhub.dev` 及当前安装路径下的后端；
5. 将旧 App 移到 `~/Library/Application Support/FlameDJHub/DevBackups/`；
6. 原子替换并启动新 App；
7. 读回 capabilities、相关 schema、通话状态、语音验证码状态、Agent 音频状态和新 Socket；
8. 任一步失败自动恢复旧 App。

禁止在活动通话、采集或验证码接听过程中部署。构建成功不代表设备、真实 PCM 或 ASR 已验收。

## 手动回滚

```bash
scripts/rollback-dev.sh
```

默认使用按时间排序的最新备份，也可以指定备份 App：

```bash
scripts/rollback-dev.sh "$HOME/Library/Application Support/FlameDJHub/DevBackups/Flame DJ Hub-<timestamp>.app"
```

回滚同样要求当前通话状态为 `idle`，并在替换后执行运行时验证。回滚前版本会保留为 `Flame DJ Hub-failed-<timestamp>.app`。

## 测试范围

`scripts/test-dev.sh` 执行：

- Flame 品牌残留检查；
- `git diff --check`；
- Info.plist 与 DMG JSON 校验；
- `go test ./...`；
- `go vet ./...`；
- 关键 Go 包 `-race`。

临时跳过 race 仅用于快速本地迭代：

```bash
FLAME_SKIP_RACE=1 scripts/test-dev.sh
```

正式安装前不得跳过。

## 真实设备验收

安装和运行时读回完成后，真实语音验证码仍需单独验证：

1. `armed → incoming → active → consumed/disarmed`；
2. Agent client 已连接；
3. `module_io_ready=true`；
4. `frames_received>0` 且 `nonzero_frames>0`；
5. ASR 返回非空转写；
6. `verification_code` 有结构化结果；
7. 挂断后 client、module I/O、owner 和窗口均清理。

Socket 存在、HELLO/ACK、电话接通或单个 availability 字段都不能代替上述证据。

#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "$0")" && pwd)/dev-common.sh"
assert_dev_identity

RUNTIME="${1:-}"
verify_app_static "$INSTALL_APP"
log "静态验证通过：$INSTALL_APP"

if [ "$RUNTIME" != "--runtime" ]; then
  exit 0
fi

CLI="$(resolve_cli "$INSTALL_APP")" || die "安装包内缺少 CLI"
CAPABILITIES="$(wait_for_runtime "$CLI" 30)" || die "30 秒内无法读取运行时 capabilities"

printf '%s' "$CAPABILITIES" | /usr/bin/python3 -c '
import json,sys
payload=json.load(sys.stdin)
if not payload.get("ok"):
    raise SystemExit("capabilities 返回失败")
names={item.get("name") for item in payload.get("data",{}).get("server",{}).get("capabilities",[])}
required={
 "call.voice-verification.status",
 "call.voice-verification.arm",
 "call.voice-verification.disarm",
 "call.agent_audio.status",
 "call.agent_audio.start",
 "call.agent_audio.stop",
}
missing=sorted(required-names)
if missing:
    raise SystemExit("缺少运行时能力："+", ".join(missing))
'

CALL_STATUS="$("$CLI" call status --format json)"
printf '%s' "$CALL_STATUS" | /usr/bin/python3 -c '
import json,sys
state=json.load(sys.stdin).get("data",{}).get("state")
if state not in {"idle","incoming","active"}:
    raise SystemExit("无效通话状态：%r" % state)
print("call_state="+state)
'

"$CLI" schema "call voice-verification arm" --format json >/dev/null
"$CLI" schema "agent-audio status" --format json >/dev/null
"$CLI" call voice-verification status --format json >/dev/null
"$CLI" agent-audio status --format json >/dev/null

SOCKET="$HOME/Library/Application Support/FlameDJHub/flame-dj-hub.sock"
[ -S "$SOCKET" ] || die "新运行时 Socket 不存在：$SOCKET"

log "运行时 capabilities、schema、status 与 Flame Socket 均已读回"

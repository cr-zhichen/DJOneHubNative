#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "$0")" && pwd)/dev-common.sh"

log "检查品牌、格式与配置"
cd "$REPO_ROOT"
/usr/bin/python3 scripts/check-flame-branding.py
git diff --check
/usr/bin/plutil -lint app/Resources/Info.plist
/usr/bin/python3 -c 'import json; json.load(open("scripts/dmgbuild.json", encoding="utf-8"))'

log "运行发布脚本与 Swift 策略探针"
"$SCRIPT_DIR/test-dev-release.sh"
"$SCRIPT_DIR/test-swift-probes.sh"

log "运行 Go 测试与静态检查"
cd "$REPO_ROOT/backend"
go test ./...
go vet ./...

if [ "${FLAME_SKIP_RACE:-0}" != "1" ]; then
  log "运行关键并发测试"
  go test -race ./cmd/djonehub-macos ./cmd/djonehub-cli ./internal/callnumber ./internal/modem
fi

log "全部开发检查通过"

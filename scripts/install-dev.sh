#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "$0")" && pwd)/dev-common.sh"

SOURCE_APP="${1:-$ARTIFACT_APP}"
verify_app_static "$SOURCE_APP"
require_idle_call

INSTALL_DIR="$(dirname "$INSTALL_APP")"
STAGE_APP="$INSTALL_DIR/.Flame DJ Hub.app.stage.$$"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
BACKUP_APP="$BACKUP_ROOT/Flame DJ Hub-$TIMESTAMP.app"
FAILED_NEW_APP="$BACKUP_ROOT/Flame DJ Hub-failed-install-$TIMESTAMP.app"
HAD_PREVIOUS=0
NEW_INSTALLED=0
COMMITTED=0
RELEASE_LOCK="$BACKUP_ROOT/.dev-release.lock"
LOCK_HELD=0

release_deploy_lock() {
  if [ "$LOCK_HELD" = "1" ]; then
    if ! rmdir "$RELEASE_LOCK"; then
      printf '[Flame DJ Hub] CRITICAL: 无法释放安装/回滚锁：%s\n' "$RELEASE_LOCK" >&2
      return 1
    fi
    LOCK_HELD=0
  fi
}

release_lock_on_exit() {
  local status=$?
  if ! release_deploy_lock; then
    status=1
  fi
  return "$status"
}

mkdir -p "$INSTALL_DIR" "$BACKUP_ROOT"
if ! mkdir "$RELEASE_LOCK" 2>/dev/null; then
  die "另一个安装或回滚流程正在运行，拒绝安装：$RELEASE_LOCK"
fi
LOCK_HELD=1
trap release_lock_on_exit EXIT
if [ -e "$BACKUP_APP" ] || [ -L "$BACKUP_APP" ]; then
  die "备份目标已存在，拒绝安装：$BACKUP_APP"
fi
if [ -e "$FAILED_NEW_APP" ] || [ -L "$FAILED_NEW_APP" ]; then
  die "失败版本暂存目标已存在，拒绝安装：$FAILED_NEW_APP"
fi

cleanup() {
  local status=$?
  set +e
  if ! rm -rf "$STAGE_APP"; then
    printf '[Flame DJ Hub] ERROR: 无法清理 staging 目录，继续执行版本恢复：%s\n' "$STAGE_APP" >&2
    status=1
  fi
  if [ "$COMMITTED" != "1" ]; then
    if [ "$NEW_INSTALLED" = "1" ] && [ -d "$INSTALL_APP" ]; then
      if ! stop_app_processes "$INSTALL_APP"; then
        printf '[Flame DJ Hub] ERROR: 新版本进程未退出，拒绝移动或恢复安装目录：%s\n' "$INSTALL_APP" >&2
        if ! release_deploy_lock; then
          status=1
        fi
        return "$status"
      fi
    fi
    if [ -d "$BACKUP_APP" ]; then
      if [ -d "$INSTALL_APP" ]; then
        if ! mv "$INSTALL_APP" "$FAILED_NEW_APP"; then
          printf '[Flame DJ Hub] CRITICAL: 旧版本位于 %s，但失败的新版本无法移出 %s；未执行覆盖恢复。\n' "$BACKUP_APP" "$INSTALL_APP" >&2
          if ! release_deploy_lock; then
            status=1
          fi
          return "$status"
        fi
      fi
      if mv "$BACKUP_APP" "$INSTALL_APP"; then
        open "$INSTALL_APP" >/dev/null 2>&1 || true
        log "安装失败，已恢复旧版本：$INSTALL_APP"
        [ -d "$FAILED_NEW_APP" ] && log "失败的新版本保留于：$FAILED_NEW_APP"
      else
        printf '[Flame DJ Hub] ERROR: 无法从备份恢复旧版本：%s -> %s\n' "$BACKUP_APP" "$INSTALL_APP" >&2
        if [ -d "$FAILED_NEW_APP" ] && mv "$FAILED_NEW_APP" "$INSTALL_APP"; then
          printf '[Flame DJ Hub] ERROR: 已将失败的新版本放回安装目录；旧版本备份仍保留于：%s\n' "$BACKUP_APP" >&2
        else
          printf '[Flame DJ Hub] CRITICAL: 安装目录可能为空；旧版本备份：%s；失败的新版本：%s\n' "$BACKUP_APP" "$FAILED_NEW_APP" >&2
        fi
      fi
    elif [ "$HAD_PREVIOUS" = "0" ] && [ "$NEW_INSTALLED" = "1" ]; then
      log "未找到旧版本；失败的新版本保留在安装目录并保持停止：$INSTALL_APP"
    fi
  fi
  if ! release_deploy_lock; then
    status=1
  fi
  return "$status"
}
trap cleanup EXIT

rm -rf "$STAGE_APP"
/usr/bin/ditto "$SOURCE_APP" "$STAGE_APP"
verify_app_static "$STAGE_APP"

if [ -d "$INSTALL_APP" ]; then
  HAD_PREVIOUS=1
fi

stop_app_processes "$INSTALL_APP" || die "现有进程未退出，拒绝替换：$INSTALL_APP"

if [ -d "$INSTALL_APP" ]; then
  mv "$INSTALL_APP" "$BACKUP_APP"
  log "旧版本已备份：$BACKUP_APP"
fi
mv "$STAGE_APP" "$INSTALL_APP"
NEW_INSTALLED=1
verify_app_static "$INSTALL_APP"
open "$INSTALL_APP"
"$SCRIPT_DIR/verify-dev.sh" --runtime

COMMITTED=1
if ! release_deploy_lock; then
  exit 1
fi
trap - EXIT
log "安装完成并通过运行时读回：$INSTALL_APP"
if [ -d "$BACKUP_APP" ]; then
  log "可回滚备份：$BACKUP_APP"
fi

#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "$0")" && pwd)/dev-common.sh"

require_idle_call
if [ "$#" -gt 0 ]; then
  TARGET="$1"
else
  TARGET="$(find_latest_good_backup "$BACKUP_ROOT")" || die "没有可用的日期备份：$BACKUP_ROOT"
fi
verify_app_static "$TARGET"

TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
FAILED_APP="$BACKUP_ROOT/Flame DJ Hub-failed-$TIMESTAMP.app"
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

mkdir -p "$BACKUP_ROOT"
if ! mkdir "$RELEASE_LOCK" 2>/dev/null; then
  die "另一个安装或回滚流程正在运行，拒绝回滚：$RELEASE_LOCK"
fi
LOCK_HELD=1
trap release_lock_on_exit EXIT
if [ -e "$FAILED_APP" ] || [ -L "$FAILED_APP" ]; then
  die "回滚暂存目标已存在，拒绝回滚：$FAILED_APP"
fi

stop_app_processes "$INSTALL_APP" || die "现有进程未退出，拒绝回滚：$INSTALL_APP"

if [ -d "$INSTALL_APP" ]; then
  if ! mv "$INSTALL_APP" "$FAILED_APP"; then
    die "无法保留回滚前版本，安装目录未改动：$INSTALL_APP"
  fi
fi
if ! mv "$TARGET" "$INSTALL_APP"; then
  if [ -d "$FAILED_APP" ]; then
    if ! mv "$FAILED_APP" "$INSTALL_APP"; then
      die "恢复备份失败，且无法放回回滚前版本；备份：$TARGET；回滚前版本：$FAILED_APP；安装目录：$INSTALL_APP"
    fi
    die "恢复备份失败，已恢复回滚前版本"
  fi
  die "恢复备份失败，且回滚前没有安装版本；备份仍保留于：${TARGET}；安装目录为空：${INSTALL_APP}"
fi

if ! open "$INSTALL_APP" || ! "$SCRIPT_DIR/verify-dev.sh" --runtime; then
  if ! stop_app_processes "$INSTALL_APP"; then
    die "回滚版本验证失败且进程未退出，拒绝移动安装目录；回滚前版本保留于：$FAILED_APP"
  fi
  if ! mv "$INSTALL_APP" "$TARGET"; then
    die "回滚版本运行验证失败，且无法移回备份位置；当前安装保留于：$INSTALL_APP；回滚前版本保留于：$FAILED_APP"
  fi
  if [ -d "$FAILED_APP" ]; then
    if mv "$FAILED_APP" "$INSTALL_APP"; then
      open "$INSTALL_APP" >/dev/null 2>&1 || true
      die "回滚版本运行验证失败，已恢复回滚前版本"
    fi
    if mv "$TARGET" "$INSTALL_APP"; then
      die "回滚前版本恢复失败；已将回滚版本放回安装目录但保持停止。回滚前版本保留于：$FAILED_APP"
    fi
    die "回滚恢复严重失败；安装目录为空，回滚版本：$TARGET；回滚前版本：$FAILED_APP"
  fi
  die "回滚版本运行验证失败；原安装不存在，失败版本已移回：$TARGET"
fi

log "已回滚到：$INSTALL_APP"
[ -d "$FAILED_APP" ] && log "回滚前版本保留于：$FAILED_APP"

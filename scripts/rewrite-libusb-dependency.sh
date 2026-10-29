#!/usr/bin/env bash
set -euo pipefail

DAEMON="${1:?用法: rewrite-libusb-dependency.sh <djonehubd>}"
OTOOL_BIN="${OTOOL_BIN:-/usr/bin/otool}"
INSTALL_NAME_TOOL_BIN="${INSTALL_NAME_TOOL_BIN:-/usr/bin/install_name_tool}"
TARGET='@loader_path/libusb-1.0.0.dylib'

[ -f "$DAEMON" ] || { printf '错误：后端不存在：%s\n' "$DAEMON" >&2; exit 2; }

libusb_dependencies() {
  sed -n 's/^[[:space:]]*\(.*libusb[^ ]*\.dylib\) (compatibility.*$/\1/p'
}

read_dependencies() {
  local output
  if ! output="$("$OTOOL_BIN" -L "$DAEMON")"; then
    printf '错误：无法读取后端动态库依赖：%s\n' "$DAEMON" >&2
    return 1
  fi
  printf '%s\n' "$output" | libusb_dependencies
}

require_single_dependency() {
  local dependencies="$1" count
  count="$(printf '%s\n' "$dependencies" | sed '/^$/d' | wc -l | tr -d '[:space:]')"
  if [ "$count" != 1 ]; then
    printf '错误：预期恰好一个 libusb 依赖，实际为 %s：%s\n' "$count" "$DAEMON" >&2
    return 1
  fi
}

before="$(read_dependencies)" || exit 1
require_single_dependency "$before" || exit 1

if [ "$before" != "$TARGET" ]; then
  if ! "$INSTALL_NAME_TOOL_BIN" -change "$before" "$TARGET" "$DAEMON"; then
    printf '错误：无法将 libusb 依赖改写为 %s：%s\n' "$TARGET" "$DAEMON" >&2
    exit 1
  fi
fi

after="$(read_dependencies)" || exit 1
require_single_dependency "$after" || exit 1
[ "$after" = "$TARGET" ] || {
  printf '错误：libusb 依赖改写验证失败，实际为 %s：%s\n' "$after" "$DAEMON" >&2
  exit 1
}

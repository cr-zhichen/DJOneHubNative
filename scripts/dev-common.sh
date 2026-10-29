#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
readonly DISPLAY_NAME="Flame DJ Hub"
readonly BUNDLE_ID="cn.flameapi.flamedjhub.dev"
readonly SIGNING_IDENTITY="DJOneHub Dev Local Signing"
INSTALL_APP="${FLAME_INSTALL_APP:-$HOME/Applications/Flame DJ Hub.app}"
BUILD_ROOT="${FLAME_BUILD_ROOT:-$HOME/Library/Developer/FlameDJHubDev}"
DERIVED_DATA="${FLAME_DERIVED_DATA:-$BUILD_ROOT/DerivedData}"
ARTIFACT_APP="${FLAME_ARTIFACT_APP:-$BUILD_ROOT/Artifacts/Flame DJ Hub.app}"
BACKUP_ROOT="${FLAME_BACKUP_ROOT:-$HOME/Library/Application Support/FlameDJHub/DevBackups}"

log() { printf '[Flame DJ Hub] %s\n' "$*"; }
die() { printf '[Flame DJ Hub] ERROR: %s\n' "$*" >&2; exit 1; }

assert_dev_identity() {
  [ "$DISPLAY_NAME" = "Flame DJ Hub" ] \
    && [ "$BUNDLE_ID" = "cn.flameapi.flamedjhub.dev" ] \
    && [ "$SIGNING_IDENTITY" = "DJOneHub Dev Local Signing" ] \
    || die "开发发布身份被修改"
}

plist_value() {
  /usr/libexec/PlistBuddy -c "Print :$2" "$1/Contents/Info.plist" 2>/dev/null
}

verify_app_static() {
  local app="$1"
  [ -d "$app" ] || die "App 不存在：$app"
  /usr/bin/codesign --verify --deep --strict "$app"
  local actual_bundle actual_name
  actual_bundle="$(plist_value "$app" CFBundleIdentifier)"
  actual_name="$(plist_value "$app" CFBundleDisplayName)"
  [ "$actual_bundle" = "$BUNDLE_ID" ] || die "Bundle ID 不匹配：$actual_bundle"
  [ "$actual_name" = "$DISPLAY_NAME" ] || die "显示名不匹配：$actual_name"
  /usr/bin/codesign -dvv "$app" 2>&1 | /usr/bin/grep -F "Authority=$SIGNING_IDENTITY" >/dev/null \
    || die "签名身份不是 $SIGNING_IDENTITY"
  [ -x "$app/Contents/Resources/backend/djonehubd" ] || die "缺少嵌入后端 djonehubd"
  [ -x "$app/Contents/Resources/backend/djonehub" ] || die "缺少嵌入 CLI djonehub"
}

resolve_cli() {
  local app="${1:-$INSTALL_APP}"
  if [ -x "$app/Contents/Resources/backend/djonehub" ]; then
    printf '%s\n' "$app/Contents/Resources/backend/djonehub"
  elif command -v djonehub >/dev/null 2>&1; then
    command -v djonehub
  else
    return 1
  fi
}

json_call_state() {
  /usr/bin/python3 -c 'import json,sys; print(json.load(sys.stdin).get("data",{}).get("state","unknown"))'
}

require_idle_call() {
  local cli output state
  cli="$(resolve_cli "$INSTALL_APP")" || die "无法找到当前 Flame DJ Hub CLI，不能安全确认通话状态"
  output="$("$cli" call status --format json)" || die "无法读取当前通话状态"
  state="$(printf '%s' "$output" | json_call_state)"
  [ "$state" = "idle" ] || die "当前通话状态为 $state，拒绝安装或替换"
  log "已确认当前通话状态：idle"
}

wait_for_runtime() {
  local cli="$1" attempts="${2:-30}" output i
  for ((i=1; i<=attempts; i++)); do
    if output="$("$cli" capabilities --format json 2>/dev/null)"; then
      printf '%s\n' "$output"
      return 0
    fi
    /bin/sleep 1
  done
  return 1
}

find_latest_good_backup() {
  local root="$1" candidate name timestamp latest=""
  shopt -s nullglob
  for candidate in "$root"/Flame\ DJ\ Hub-*.app; do
    [ -d "$candidate" ] || continue
    name="${candidate##*/}"
    if [[ "$name" =~ ^Flame\ DJ\ Hub-([0-9]{8})-([0-9]{6})\.app$ ]]; then
      timestamp="${BASH_REMATCH[1]}${BASH_REMATCH[2]}"
      [[ "${timestamp:4:2}" =~ ^(0[1-9]|1[0-2])$ ]] || continue
      [[ "${timestamp:6:2}" =~ ^(0[1-9]|[12][0-9]|3[01])$ ]] || continue
      [[ "${timestamp:8:2}" =~ ^([01][0-9]|2[0-3])$ ]] || continue
      [[ "${timestamp:10:2}" =~ ^[0-5][0-9]$ ]] || continue
      [[ "${timestamp:12:2}" =~ ^[0-5][0-9]$ ]] || continue
      if [ -z "$latest" ] || [[ "$candidate" > "$latest" ]]; then
        latest="$candidate"
      fi
    fi
  done
  [ -n "$latest" ] || return 1
  printf '%s\n' "$latest"
}

candidate_pids_for_executable() {
  local executable="$1" uid="$2" actual_uid pid
  while read -r actual_uid pid; do
    [ "$actual_uid" = "$uid" ] || continue
    [[ "$pid" =~ ^[0-9]+$ ]] && printf '%s\n' "$pid"
  done < <(/bin/ps -axo uid=,pid=)
}

process_uid() {
  /bin/ps -p "$1" -o uid= 2>/dev/null | /usr/bin/tr -d '[:space:]'
}

process_executable_path() {
  /bin/ps -ww -p "$1" -o comm= 2>/dev/null | /usr/bin/sed 's/^[[:space:]]*//'
}

find_exact_executable_pids() {
  local executable="$1" uid="$2" pid actual_uid actual_path
  while IFS= read -r pid; do
    [[ "$pid" =~ ^[0-9]+$ ]] || continue
    actual_uid="$(process_uid "$pid")"
    [ "$actual_uid" = "$uid" ] || continue
    actual_path="$(process_executable_path "$pid")"
    [ "$actual_path" = "$executable" ] || continue
    printf '%s\n' "$pid"
  done < <(candidate_pids_for_executable "$executable" "$uid")
}

find_app_pids() {
  local app="$1" uid="$2" executable pid seen=" "
  for executable in \
    "$app/Contents/MacOS/DJOneHub" \
    "$app/Contents/Resources/backend/djonehubd"; do
    while IFS= read -r pid; do
      [ -n "$pid" ] || continue
      case "$seen" in
        *" $pid "*) ;;
        *) printf '%s\n' "$pid"; seen="$seen$pid " ;;
      esac
    done < <(find_exact_executable_pids "$executable" "$uid")
  done
}

app_pid_matches() {
  local pid="$1" app="$2" uid="$3" actual_uid actual_path
  actual_uid="$(process_uid "$pid")"
  [ "$actual_uid" = "$uid" ] || return 1
  actual_path="$(process_executable_path "$pid")"
  [ "$actual_path" = "$app/Contents/MacOS/DJOneHub" ] \
    || [ "$actual_path" = "$app/Contents/Resources/backend/djonehubd" ]
}

dev_kill() {
  /bin/kill -"$1" "$2" 2>/dev/null
}

dev_sleep() {
  /bin/sleep "$1"
}

request_app_quit() {
  /usr/bin/osascript -e "tell application id \"$BUNDLE_ID\" to quit" >/dev/null 2>&1 || true
}

wait_for_app_exit() {
  local app="$1" uid="$2" attempts="$3" i pids
  for ((i=0; i<attempts; i++)); do
    pids="$(find_app_pids "$app" "$uid")"
    [ -z "$pids" ] && return 0
    dev_sleep 1
  done
  pids="$(find_app_pids "$app" "$uid")"
  [ -z "$pids" ]
}

stop_app_processes() {
  local app="$1" uid="${2:-$(/usr/bin/id -u)}" term_wait="${3:-10}" kill_wait="${4:-3}"
  local pid pids

  request_app_quit
  pids="$(find_app_pids "$app" "$uid")"
  [ -z "$pids" ] && return 0

  while IFS= read -r pid; do
    if [ -n "$pid" ] && app_pid_matches "$pid" "$app" "$uid"; then
      dev_kill TERM "$pid" || true
    fi
  done <<<"$pids"
  wait_for_app_exit "$app" "$uid" "$term_wait" && return 0

  pids="$(find_app_pids "$app" "$uid")"
  while IFS= read -r pid; do
    if [ -n "$pid" ] && app_pid_matches "$pid" "$app" "$uid"; then
      dev_kill KILL "$pid" || true
    fi
  done <<<"$pids"
  wait_for_app_exit "$app" "$uid" "$kill_wait"
}

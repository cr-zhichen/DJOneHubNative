#!/usr/bin/env bash
set -u

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PASS=0
FAIL=0

pass() { PASS=$((PASS + 1)); printf 'ok - %s\n' "$1"; }
fail() { FAIL=$((FAIL + 1)); printf 'not ok - %s\n' "$1" >&2; }

run_test() {
  local name="$1"
  shift
  if ( "$@" ); then pass "$name"; else fail "$name"; fi
}

assert_fixed_identity() {
  local output
  output="$(
    FLAME_DISPLAY_NAME=Wrong FLAME_BUNDLE_ID=wrong.id FLAME_SIGNING_IDENTITY=Wrong \
      bash -c 'source "$1/dev-common.sh"; printf "%s|%s|%s" "$DISPLAY_NAME" "$BUNDLE_ID" "$SIGNING_IDENTITY"' _ "$SCRIPT_DIR"
  )"
  [ "$output" = 'Flame DJ Hub|cn.flameapi.flamedjhub.dev|DJOneHub Dev Local Signing' ]
}

assert_backup_selection() {
  local root latest
  root="$(mktemp -d "${TMPDIR:-/tmp}/flame backups.XXXXXX")" || return 1
  trap 'rm -rf "$root"' RETURN
  mkdir -p \
    "$root/Flame DJ Hub-20260101-010101.app" \
    "$root/Flame DJ Hub-failed-20990101-010101.app" \
    "$root/Flame DJ Hub-20261231-235959.app" \
    "$root/Flame DJ Hub-not-a-date.app"
  latest="$(bash -c 'source "$1/dev-common.sh"; find_latest_good_backup "$2"' _ "$SCRIPT_DIR" "$root")" || return 1
  [ "$latest" = "$root/Flame DJ Hub-20261231-235959.app" ]
}

assert_exact_process_matching() {
  bash -c '
    source "$1/dev-common.sh"
    candidate_pids_for_executable() { printf "11\n12\n13\n"; }
    process_uid() { case "$1" in 11|12) printf "501\n";; 13) printf "502\n";; esac; }
    process_executable_path() {
      case "$1" in
        11) printf "%s\n" "$2/Contents/MacOS/DJOneHub" ;;
        12) printf "%s\n" "$2/Contents/MacOS/DJOneHub Helper" ;;
        13) printf "%s\n" "$2/Contents/MacOS/DJOneHub" ;;
      esac
    }
    export TEST_APP="$2"
    process_executable_path() {
      case "$1" in
        11) printf "%s\n" "$TEST_APP/Contents/MacOS/DJOneHub" ;;
        12) printf "%s\n" "$TEST_APP/Contents/MacOS/DJOneHub Helper" ;;
        13) printf "%s\n" "$TEST_APP/Contents/MacOS/DJOneHub" ;;
      esac
    }
    result="$(find_exact_executable_pids "$TEST_APP/Contents/MacOS/DJOneHub" 501)"
    [ "$result" = 11 ]
  ' _ "$SCRIPT_DIR" '/tmp/Flame Test/Flame DJ Hub.app'
}

assert_term_escalates_safely() {
  local root actions
  root="$(mktemp -d "${TMPDIR:-/tmp}/flame stop.XXXXXX")" || return 1
  actions="$root/actions"
  : >"$actions"
  ACTIONS="$actions" bash -c '
    source "$1/dev-common.sh"
    find_app_pids() {
      if grep -q "KILL" "$ACTIONS"; then return 0; fi
      printf "42\n99\n"
    }
    request_app_quit() { :; }
    app_pid_matches() { [ "$1" = 42 ]; }
    dev_kill() { printf "%s %s\n" "$1" "$2" >>"$ACTIONS"; }
    dev_sleep() { :; }
    stop_app_processes "$2" 501 1 1
  ' _ "$SCRIPT_DIR" '/tmp/Flame Test/Flame DJ Hub.app' || { rm -rf "$root"; return 1; }
  grep -q '^TERM 42$' "$actions" \
    && grep -q '^KILL 42$' "$actions" \
    && ! grep -q ' 99$' "$actions"
  local result=$?
  rm -rf "$root"
  return "$result"
}

assert_stop_timeout_fails() {
  local root actions
  root="$(mktemp -d "${TMPDIR:-/tmp}/flame stuck.XXXXXX")" || return 1
  actions="$root/actions"
  : >"$actions"
  if ACTIONS="$actions" bash -c '
    source "$1/dev-common.sh"
    find_app_pids() { printf "42\n"; }
    request_app_quit() { :; }
    app_pid_matches() { [ "$1" = 42 ]; }
    dev_kill() { printf "%s %s\n" "$1" "$2" >>"$ACTIONS"; }
    dev_sleep() { :; }
    stop_app_processes "$2" 501 1 1
  ' _ "$SCRIPT_DIR" '/tmp/Flame Test/Flame DJ Hub.app'; then
    rm -rf "$root"
    return 1
  fi
  grep -q '^TERM 42$' "$actions" && grep -q '^KILL 42$' "$actions"
  local result=$?
  rm -rf "$root"
  return "$result"
}

make_flow_sandbox() {
  local root="$1" mode="$2"
  mkdir -p "$root/scripts" "$root/install dir/Flame DJ Hub.app" "$root/source/Flame DJ Hub.app" "$root/backups/Flame DJ Hub-20260101-010101.app"
  cp "$SCRIPT_DIR/install-dev.sh" "$SCRIPT_DIR/rollback-dev.sh" "$root/scripts/"
  cat >"$root/scripts/dev-common.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DISPLAY_NAME='Flame DJ Hub'
BUNDLE_ID='cn.flameapi.flamedjhub.dev'
SIGNING_IDENTITY='DJOneHub Dev Local Signing'
INSTALL_APP="$TEST_ROOT/install dir/Flame DJ Hub.app"
ARTIFACT_APP="$TEST_ROOT/source/Flame DJ Hub.app"
BACKUP_ROOT="$TEST_ROOT/backups"
log() {
  printf 'log %s\n' "$*" >>"$TEST_ACTIONS"
  local count_file="${TEST_LOG_COUNT_FILE:-$TEST_ROOT/log-count}" count=0
  [ -f "$count_file" ] && count="$(<"$count_file")"
  count=$((count + 1))
  printf '%s\n' "$count" >"$count_file"
  if [ "${TEST_LOG_FAIL:-0}" = 1 ] && [ "$count" -ge "${TEST_LOG_FAIL_OCCURRENCE:-1}" ]; then
    return 1
  fi
}
die() { printf 'die %s\n' "$*" >>"$TEST_ACTIONS"; exit 1; }
require_idle_call() { :; }
verify_app_static() {
  printf 'verify %s\n' "$1" >>"$TEST_ACTIONS"
  if [ "${TEST_FAIL_INSTALLED_VERIFY:-0}" = 1 ] && [ "$1" = "$INSTALL_APP" ]; then return 1; fi
}
stop_app_processes() {
  printf 'stop %s\n' "$1" >>"$TEST_ACTIONS"
  [ "${TEST_STOP_FAIL:-0}" != 1 ]
}
find_latest_good_backup() { printf '%s\n' "$1/Flame DJ Hub-20260101-010101.app"; }
mv() {
  printf 'mv %s -> %s\n' "$1" "$2" >>"$TEST_ACTIONS"
  if [ -n "${TEST_FAIL_MV_FROM:-}" ] && [ "$1" = "$TEST_FAIL_MV_FROM" ] && [ "$2" = "${TEST_FAIL_MV_TO:-}" ]; then
    printf 'mv-failed %s -> %s\n' "$1" "$2" >>"$TEST_ACTIONS"
    return 1
  fi
  command mv "$@"
}
rm() {
  printf 'rm %s\n' "$*" >>"$TEST_ACTIONS"
  if [ -n "${TEST_FAIL_RM_MATCH:-}" ] && [[ "$*" == *"$TEST_FAIL_RM_MATCH"* ]]; then
    local count_file="${TEST_RM_COUNT_FILE:-$TEST_ROOT/rm-match-count}" count=0
    [ -f "$count_file" ] && count="$(<"$count_file")"
    count=$((count + 1))
    printf '%s\n' "$count" >"$count_file"
    if [ "$count" -ge "${TEST_FAIL_RM_OCCURRENCE:-1}" ]; then
      printf 'rm-failed %s\n' "$*" >>"$TEST_ACTIONS"
      return 1
    fi
  fi
  command rm "$@"
}
open() { printf 'open %s\n' "$1" >>"$TEST_ACTIONS"; return "${TEST_OPEN_STATUS:-0}"; }
date() { printf '20261028-120000\n'; }
SH
  cat >"$root/scripts/verify-dev.sh" <<'SH'
#!/usr/bin/env bash
printf 'runtime-verify\n' >>"$TEST_ACTIONS"
exit "${TEST_RUNTIME_STATUS:-0}"
SH
  chmod +x "$root/scripts/verify-dev.sh"
  : >"$root/actions"
}

assert_install_failure_stops_before_restore() {
  local root actions stop_line restore_line
  root="$(mktemp -d "${TMPDIR:-/tmp}/flame install flow.XXXXXX")" || return 1
  make_flow_sandbox "$root" install
  actions="$root/actions"
  TEST_ROOT="$root" TEST_ACTIONS="$actions" TEST_FAIL_INSTALLED_VERIFY=1 \
    bash "$root/scripts/install-dev.sh" "$root/source/Flame DJ Hub.app" >/dev/null 2>&1 && { rm -rf "$root"; return 1; }
  stop_line="$(grep -nF "stop $root/install dir/Flame DJ Hub.app" "$actions" | tail -1 | cut -d: -f1)"
  restore_line="$(grep -nF "mv $root/backups/Flame DJ Hub-20261028-120000.app -> $root/install dir/Flame DJ Hub.app" "$actions" | tail -1 | cut -d: -f1)"
  [ -n "$stop_line" ] && [ -n "$restore_line" ] && [ "$stop_line" -lt "$restore_line" ]
  local result=$?
  rm -rf "$root"
  return "$result"
}

assert_timeout_refuses_replace() {
  local root actions
  root="$(mktemp -d "${TMPDIR:-/tmp}/flame timeout flow.XXXXXX")" || return 1
  make_flow_sandbox "$root" install
  actions="$root/actions"
  TEST_ROOT="$root" TEST_ACTIONS="$actions" TEST_STOP_FAIL=1 \
    bash "$root/scripts/install-dev.sh" "$root/source/Flame DJ Hub.app" >/dev/null 2>&1 && { rm -rf "$root"; return 1; }
  ! grep -Fq "mv $root/install dir/Flame DJ Hub.app ->" "$actions"
  local result=$?
  rm -rf "$root"
  return "$result"
}

assert_rollback_failure_stops_before_restore() {
  local root actions stop_count restore_line second_stop_line
  root="$(mktemp -d "${TMPDIR:-/tmp}/flame rollback flow.XXXXXX")" || return 1
  make_flow_sandbox "$root" rollback
  actions="$root/actions"
  TEST_ROOT="$root" TEST_ACTIONS="$actions" TEST_RUNTIME_STATUS=1 \
    bash "$root/scripts/rollback-dev.sh" >/dev/null 2>&1 && { rm -rf "$root"; return 1; }
  stop_count="$(grep -cF "stop $root/install dir/Flame DJ Hub.app" "$actions" || true)"
  second_stop_line="$(grep -nF "stop $root/install dir/Flame DJ Hub.app" "$actions" | tail -1 | cut -d: -f1)"
  restore_line="$(grep -nF "mv $root/backups/Flame DJ Hub-failed-20261028-120000.app -> $root/install dir/Flame DJ Hub.app" "$actions" | tail -1 | cut -d: -f1)"
  [ "$stop_count" -ge 2 ] && [ -n "$restore_line" ] && [ "$second_stop_line" -lt "$restore_line" ]
  local result=$?
  rm -rf "$root"
  return "$result"
}

assert_install_restore_failure_preserves_installed_app() {
  local root actions backup install failed_new
  root="$(mktemp -d "${TMPDIR:-/tmp}/flame install restore failure.XXXXXX")" || return 1
  make_flow_sandbox "$root" install
  actions="$root/actions"
  backup="$root/backups/Flame DJ Hub-20261028-120000.app"
  install="$root/install dir/Flame DJ Hub.app"
  failed_new="$root/backups/Flame DJ Hub-failed-install-20261028-120000.app"
  TEST_ROOT="$root" TEST_ACTIONS="$actions" TEST_FAIL_INSTALLED_VERIFY=1 \
    TEST_FAIL_MV_FROM="$backup" TEST_FAIL_MV_TO="$install" \
    bash "$root/scripts/install-dev.sh" "$root/source/Flame DJ Hub.app" >/dev/null 2>&1 && { rm -rf "$root"; return 1; }
  [ -d "$install" ] && [ -d "$backup" ] && [ ! -d "$failed_new" ] && \
    grep -Fq "mv $failed_new -> $install" "$actions"
  local result=$?
  rm -rf "$root"
  return "$result"
}

assert_install_without_previous_preserves_failed_app() {
  local root actions install
  root="$(mktemp -d "${TMPDIR:-/tmp}/flame install no previous.XXXXXX")" || return 1
  make_flow_sandbox "$root" install
  actions="$root/actions"
  install="$root/install dir/Flame DJ Hub.app"
  rm -rf "$install"
  TEST_ROOT="$root" TEST_ACTIONS="$actions" TEST_FAIL_INSTALLED_VERIFY=1 \
    bash "$root/scripts/install-dev.sh" "$root/source/Flame DJ Hub.app" >/dev/null 2>&1 && { rm -rf "$root"; return 1; }
  [ -d "$install" ] && grep -Fq "未找到旧版本；失败的新版本保留在安装目录并保持停止：$install" "$actions"
  local result=$?
  rm -rf "$root"
  return "$result"
}

assert_install_successful_restore_preserves_failed_new_app() {
  local root actions install failed_new
  root="$(mktemp -d "${TMPDIR:-/tmp}/flame install preserve failed.XXXXXX")" || return 1
  make_flow_sandbox "$root" install
  actions="$root/actions"
  install="$root/install dir/Flame DJ Hub.app"
  failed_new="$root/backups/Flame DJ Hub-failed-install-20261028-120000.app"
  TEST_ROOT="$root" TEST_ACTIONS="$actions" TEST_FAIL_INSTALLED_VERIFY=1 \
    bash "$root/scripts/install-dev.sh" "$root/source/Flame DJ Hub.app" >/dev/null 2>&1 && { rm -rf "$root"; return 1; }
  [ -d "$install" ] && [ -d "$failed_new" ] && grep -Fq "失败的新版本保留于：$failed_new" "$actions"
  local result=$?
  rm -rf "$root"
  return "$result"
}

assert_rollback_restore_failure_preserves_installed_app() {
  local root actions install target failed_app
  root="$(mktemp -d "${TMPDIR:-/tmp}/flame rollback restore failure.XXXXXX")" || return 1
  make_flow_sandbox "$root" rollback
  actions="$root/actions"
  install="$root/install dir/Flame DJ Hub.app"
  target="$root/backups/Flame DJ Hub-20260101-010101.app"
  failed_app="$root/backups/Flame DJ Hub-failed-20261028-120000.app"
  TEST_ROOT="$root" TEST_ACTIONS="$actions" TEST_RUNTIME_STATUS=1 \
    TEST_FAIL_MV_FROM="$failed_app" TEST_FAIL_MV_TO="$install" \
    bash "$root/scripts/rollback-dev.sh" >/dev/null 2>&1 && { rm -rf "$root"; return 1; }
  [ -d "$install" ] && [ -d "$failed_app" ] && [ ! -d "$target" ] && \
    grep -Fq "mv $target -> $install" "$actions"
  local result=$?
  rm -rf "$root"
  return "$result"
}

assert_rollback_target_move_failure_without_current_is_precise() {
  local root actions install target
  root="$(mktemp -d "${TMPDIR:-/tmp}/flame rollback no current.XXXXXX")" || return 1
  make_flow_sandbox "$root" rollback
  actions="$root/actions"
  install="$root/install dir/Flame DJ Hub.app"
  target="$root/backups/Flame DJ Hub-20260101-010101.app"
  rm -rf "$install"
  TEST_ROOT="$root" TEST_ACTIONS="$actions" \
    TEST_FAIL_MV_FROM="$target" TEST_FAIL_MV_TO="$install" \
    bash "$root/scripts/rollback-dev.sh" >/dev/null 2>&1 && { rm -rf "$root"; return 1; }
  [ ! -d "$install" ] && [ -d "$target" ] && \
    grep -Fq "die 恢复备份失败，且回滚前没有安装版本；备份仍保留于：${target}；安装目录为空：${install}" "$actions"
  local result=$?
  rm -rf "$root"
  return "$result"
}

assert_install_refuses_timestamp_collision() {
  local root actions install collision
  root="$(mktemp -d "${TMPDIR:-/tmp}/flame install collision.XXXXXX")" || return 1
  make_flow_sandbox "$root" install
  actions="$root/actions"
  install="$root/install dir/Flame DJ Hub.app"
  collision="$root/backups/Flame DJ Hub-20261028-120000.app"
  mkdir -p "$collision"
  TEST_ROOT="$root" TEST_ACTIONS="$actions" \
    bash "$root/scripts/install-dev.sh" "$root/source/Flame DJ Hub.app" >/dev/null 2>&1 && { rm -rf "$root"; return 1; }
  [ -d "$install" ] && [ -d "$collision" ] && [ ! -e "$collision/Flame DJ Hub.app" ] && \
    ! grep -Fq "stop $install" "$actions" && \
    grep -Fq "die 备份目标已存在，拒绝安装：$collision" "$actions"
  local result=$?
  rm -rf "$root"
  return "$result"
}

assert_rollback_refuses_failed_path_collision() {
  local root actions install collision
  root="$(mktemp -d "${TMPDIR:-/tmp}/flame rollback collision.XXXXXX")" || return 1
  make_flow_sandbox "$root" rollback
  actions="$root/actions"
  install="$root/install dir/Flame DJ Hub.app"
  collision="$root/backups/Flame DJ Hub-failed-20261028-120000.app"
  mkdir -p "$collision"
  TEST_ROOT="$root" TEST_ACTIONS="$actions" \
    bash "$root/scripts/rollback-dev.sh" >/dev/null 2>&1 && { rm -rf "$root"; return 1; }
  [ -d "$install" ] && [ -d "$collision" ] && [ ! -e "$collision/Flame DJ Hub.app" ] && \
    ! grep -Fq "stop $install" "$actions" && \
    grep -Fq "die 回滚暂存目标已存在，拒绝回滚：$collision" "$actions"
  local result=$?
  rm -rf "$root"
  return "$result"
}

assert_install_refuses_active_release_lock() {
  local root actions install lock
  root="$(mktemp -d "${TMPDIR:-/tmp}/flame install lock.XXXXXX")" || return 1
  make_flow_sandbox "$root" install
  actions="$root/actions"
  install="$root/install dir/Flame DJ Hub.app"
  lock="$root/backups/.dev-release.lock"
  mkdir -p "$lock"
  TEST_ROOT="$root" TEST_ACTIONS="$actions" \
    bash "$root/scripts/install-dev.sh" "$root/source/Flame DJ Hub.app" >/dev/null 2>&1 && { rm -rf "$root"; return 1; }
  [ -d "$install" ] && [ -d "$lock" ] && ! grep -Fq "stop $install" "$actions" && \
    grep -Fq "die 另一个安装或回滚流程正在运行，拒绝安装：$lock" "$actions"
  local result=$?
  rm -rf "$root"
  return "$result"
}

assert_rollback_refuses_active_release_lock() {
  local root actions install lock
  root="$(mktemp -d "${TMPDIR:-/tmp}/flame rollback lock.XXXXXX")" || return 1
  make_flow_sandbox "$root" rollback
  actions="$root/actions"
  install="$root/install dir/Flame DJ Hub.app"
  lock="$root/backups/.dev-release.lock"
  mkdir -p "$lock"
  TEST_ROOT="$root" TEST_ACTIONS="$actions" \
    bash "$root/scripts/rollback-dev.sh" >/dev/null 2>&1 && { rm -rf "$root"; return 1; }
  [ -d "$install" ] && [ -d "$lock" ] && ! grep -Fq "stop $install" "$actions" && \
    grep -Fq "die 另一个安装或回滚流程正在运行，拒绝回滚：$lock" "$actions"
  local result=$?
  rm -rf "$root"
  return "$result"
}

assert_cleanup_rm_failure_still_restores_and_unlocks() {
  local root actions install lock backup failed_new
  root="$(mktemp -d "${TMPDIR:-/tmp}/flame cleanup rm failure.XXXXXX")" || return 1
  make_flow_sandbox "$root" install
  actions="$root/actions"
  install="$root/install dir/Flame DJ Hub.app"
  lock="$root/backups/.dev-release.lock"
  backup="$root/backups/Flame DJ Hub-20261028-120000.app"
  failed_new="$root/backups/Flame DJ Hub-failed-install-20261028-120000.app"
  TEST_ROOT="$root" TEST_ACTIONS="$actions" TEST_FAIL_INSTALLED_VERIFY=1 \
    TEST_FAIL_RM_MATCH='.Flame DJ Hub.app.stage.' TEST_FAIL_RM_OCCURRENCE=2 \
    bash "$root/scripts/install-dev.sh" "$root/source/Flame DJ Hub.app" >/dev/null 2>&1 && { rm -rf "$root"; return 1; }
  [ -d "$install" ] && [ ! -e "$lock" ] && [ ! -e "$backup" ] && [ -d "$failed_new" ] && \
    grep -Fq 'rm-failed -rf' "$actions" && \
    grep -Fq "mv $backup -> $install" "$actions"
  local result=$?
  rm -rf "$root"
  return "$result"
}

assert_cleanup_log_failure_still_restores_and_unlocks() {
  local root actions install lock backup failed_new
  root="$(mktemp -d "${TMPDIR:-/tmp}/flame cleanup log failure.XXXXXX")" || return 1
  make_flow_sandbox "$root" install
  actions="$root/actions"
  install="$root/install dir/Flame DJ Hub.app"
  lock="$root/backups/.dev-release.lock"
  backup="$root/backups/Flame DJ Hub-20261028-120000.app"
  failed_new="$root/backups/Flame DJ Hub-failed-install-20261028-120000.app"
  TEST_ROOT="$root" TEST_ACTIONS="$actions" TEST_FAIL_INSTALLED_VERIFY=1 \
    TEST_LOG_FAIL=1 TEST_LOG_FAIL_OCCURRENCE=2 \
    bash "$root/scripts/install-dev.sh" "$root/source/Flame DJ Hub.app" >/dev/null 2>&1 && { rm -rf "$root"; return 1; }
  [ -d "$install" ] && [ ! -e "$lock" ] && [ ! -e "$backup" ] && [ -d "$failed_new" ] && \
    grep -Fq "mv $backup -> $install" "$actions"
  local result=$?
  rm -rf "$root"
  return "$result"
}

assert_incremental_outer_signing_uses_stable_identity() {
  grep -Fq '/usr/bin/codesign --force --sign "${EXPANDED_CODE_SIGN_IDENTITY}" --timestamp=none "${APP_BUNDLE}"' "$SCRIPT_DIR/../project.yml"
}

assert_libusb_dependency_rewrite_is_fail_closed() {
  local root helper daemon marker otool install
  root="$(mktemp -d "${TMPDIR:-/tmp}/flame libusb rewrite.XXXXXX")" || return 1
  helper="$SCRIPT_DIR/rewrite-libusb-dependency.sh"
  daemon="$root/Flame DJ Hub/djonehubd"
  marker="$root/rewritten"
  otool="$root/fake-otool"
  install="$root/fake-install-name-tool"
  mkdir -p "$(dirname "$daemon")"
  : >"$daemon"

  cat >"$otool" <<'SH'
#!/usr/bin/env bash
set -eu
case "${TEST_OTOOL_MODE:-success}" in
  fail) exit 41 ;;
  multiple)
    printf '%s:\n' "$2"
    printf '\t/first/libusb-1.0.0.dylib (compatibility version 7.0.0, current version 7.0.0)\n'
    printf '\t/second/libusb-1.0.0.dylib (compatibility version 7.0.0, current version 7.0.0)\n'
    ;;
  *)
    printf '%s:\n' "$2"
    printf '\t/usr/lib/libSystem.B.dylib (compatibility version 1.0.0, current version 1.0.0)\n'
    if [ -f "$TEST_REWRITE_MARKER" ]; then
      printf '\t@loader_path/libusb-1.0.0.dylib (compatibility version 7.0.0, current version 7.0.0)\n'
    else
      printf '\t/Users/macmini/Library/Application Support/Flame DJ Hub/libusb-1.0.0.dylib (compatibility version 7.0.0, current version 7.0.0)\n'
    fi
    ;;
esac
SH
  cat >"$install" <<'SH'
#!/usr/bin/env bash
set -eu
[ "${TEST_INSTALL_MODE:-success}" != fail ] || exit 42
[ "$1" = -change ]
[ "$2" = '/Users/macmini/Library/Application Support/Flame DJ Hub/libusb-1.0.0.dylib' ]
[ "$3" = '@loader_path/libusb-1.0.0.dylib' ]
[ "$4" = "$TEST_DAEMON" ]
: >"$TEST_REWRITE_MARKER"
SH
  chmod +x "$otool" "$install"

  TEST_REWRITE_MARKER="$marker" TEST_DAEMON="$daemon" OTOOL_BIN="$otool" INSTALL_NAME_TOOL_BIN="$install" \
    "$helper" "$daemon" || { rm -rf "$root"; return 1; }
  [ -f "$marker" ] || { rm -rf "$root"; return 1; }
  rm -f "$marker"

  if TEST_OTOOL_MODE=fail TEST_REWRITE_MARKER="$marker" TEST_DAEMON="$daemon" OTOOL_BIN="$otool" INSTALL_NAME_TOOL_BIN="$install" \
    "$helper" "$daemon" >/dev/null 2>&1; then rm -rf "$root"; return 1; fi
  if TEST_OTOOL_MODE=multiple TEST_REWRITE_MARKER="$marker" TEST_DAEMON="$daemon" OTOOL_BIN="$otool" INSTALL_NAME_TOOL_BIN="$install" \
    "$helper" "$daemon" >/dev/null 2>&1; then rm -rf "$root"; return 1; fi
  if TEST_INSTALL_MODE=fail TEST_REWRITE_MARKER="$marker" TEST_DAEMON="$daemon" OTOOL_BIN="$otool" INSTALL_NAME_TOOL_BIN="$install" \
    "$helper" "$daemon" >/dev/null 2>&1; then rm -rf "$root"; return 1; fi

  grep -Fq 'rewrite-libusb-dependency.sh" "${BACKEND_DIR}/djonehubd"' "$SCRIPT_DIR/../project.yml" \
    && ! grep -Fq 'install_name_tool -change "$old"' "$SCRIPT_DIR/../project.yml"
  local result=$?
  rm -rf "$root"
  return "$result"
}

run_test 'identity is fixed and ignores environment overrides' assert_fixed_identity
run_test 'latest rollback backup excludes failed and malformed names' assert_backup_selection
run_test 'process matching requires exact executable path and UID' assert_exact_process_matching
run_test 'TERM timeout escalates only still-matching processes' assert_term_escalates_safely
run_test 'processes surviving KILL make stop fail' assert_stop_timeout_fails
run_test 'install verification failure stops new version before restore' assert_install_failure_stops_before_restore
run_test 'stop timeout refuses to replace installed app' assert_timeout_refuses_replace
run_test 'rollback verification failure stops rollback version before restore' assert_rollback_failure_stops_before_restore
run_test 'install restore failure preserves an installed app' assert_install_restore_failure_preserves_installed_app
run_test 'install without previous version preserves failed app' assert_install_without_previous_preserves_failed_app
run_test 'successful install restore preserves failed new app' assert_install_successful_restore_preserves_failed_new_app
run_test 'rollback restore failure preserves an installed app' assert_rollback_restore_failure_preserves_installed_app
run_test 'rollback target move failure without current app reports exact state' assert_rollback_target_move_failure_without_current_is_precise
run_test 'install refuses an existing timestamp backup target' assert_install_refuses_timestamp_collision
run_test 'rollback refuses an existing failed-app target' assert_rollback_refuses_failed_path_collision
run_test 'install refuses while release lock is held' assert_install_refuses_active_release_lock
run_test 'rollback refuses while release lock is held' assert_rollback_refuses_active_release_lock
run_test 'cleanup rm failure still restores and releases lock' assert_cleanup_rm_failure_still_restores_and_unlocks
run_test 'cleanup log failure still restores and releases lock' assert_cleanup_log_failure_still_restores_and_unlocks
run_test 'incremental outer signing uses the stable identity' assert_incremental_outer_signing_uses_stable_identity
run_test 'libusb dependency rewrite is fail-closed' assert_libusb_dependency_rewrite_is_fail_closed

printf '%s passed, %s failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]

#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "$0")" && pwd)/dev-common.sh"

PROBE_ROOT="${TMPDIR:-/tmp}/flame-dj-hub-swift-probes"
rm -rf "$PROBE_ROOT"
mkdir -p "$PROBE_ROOT"
trap 'rm -rf "$PROBE_ROOT"' EXIT

cd "$REPO_ROOT"

swiftc -parse-as-library \
  app/Sources/Models.swift \
  app/Tests/VoiceVerificationAudioPolicyProbe/main.swift \
  -o "$PROBE_ROOT/voice-verification-policy"
"$PROBE_ROOT/voice-verification-policy"

swiftc -parse-as-library \
  app/Sources/Models.swift \
  app/Sources/DashboardPollingState.swift \
  app/Sources/NativeSpeechTranscriber.swift \
  app/Tests/StateSemanticsProbe/main.swift \
  -framework AVFoundation \
  -framework Speech \
  -o "$PROBE_ROOT/state-semantics"
"$PROBE_ROOT/state-semantics"

swiftc -parse-as-library \
  -D FLAME_RUNTIME_MIGRATION_PROBE \
  app/Sources/BackendProcess.swift \
  app/Tests/RuntimeMigrationProbe/main.swift \
  -o "$PROBE_ROOT/runtime-migration"
"$PROBE_ROOT/runtime-migration"

swiftc -parse-as-library \
  app/Sources/Models.swift \
  app/Sources/RuntimeDiagnostics.swift \
  app/Tests/RuntimeDiagnosticsProbe/main.swift \
  -o "$PROBE_ROOT/runtime-diagnostics"
"$PROBE_ROOT/runtime-diagnostics"

swiftc -parse-as-library -D STRUCTURED_DIAGNOSTICS_PROBE \
  app/Sources/Models.swift \
  app/Sources/RuntimeDiagnostics.swift \
  app/Sources/StructuredDiagnostics.swift \
  app/Sources/SecureDiagnosticEventFile.swift \
  app/Sources/StructuredDiagnosticLogStore.swift \
  app/Tests/StructuredDiagnosticsProbe/main.swift \
  -o "$PROBE_ROOT/structured-diagnostics"
"$PROBE_ROOT/structured-diagnostics"

swiftc -parse-as-library \
  app/Sources/PermissionCenter.swift \
  app/Tests/PermissionCenterProbe/main.swift \
  -o "$PROBE_ROOT/permission-center"
"$PROBE_ROOT/permission-center"

log "Swift 策略、权限、结构化诊断与迁移探针全部通过"

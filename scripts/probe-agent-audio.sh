#!/bin/bash
# 离线验证 Agent PCM 数据平面；不访问 CoreAudio、USB、ADB 或模块。
set -euo pipefail
cd "$(dirname "$0")/.."
probe_dir=$(mktemp -d "${TMPDIR:-/tmp}/djonehub-agent-audio-probe.XXXXXX")
trap 'rm -rf "$probe_dir"' EXIT
cat app/Sources/AgentModuleCoreAudioIO.swift > "$probe_dir/AgentModuleCoreAudioIO.swift"
cat app/Sources/AgentAudioTransport.swift > "$probe_dir/AgentAudioTransport.swift"
cat app/Tests/AgentAudioProbe/main.swift > "$probe_dir/main.swift"
xcrun swiftc -swift-version 5 -target "$(uname -m)-apple-macos13.0" \
  "$probe_dir/AgentModuleCoreAudioIO.swift" "$probe_dir/AgentAudioTransport.swift" "$probe_dir/main.swift" \
  -framework CoreAudio -framework CryptoKit -o "$probe_dir/probe"
"$probe_dir/probe"
